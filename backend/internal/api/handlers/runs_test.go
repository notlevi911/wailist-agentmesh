package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/agentmesh/backend/internal/api/handlers"
	"github.com/agentmesh/backend/internal/engine"
	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/sse"
	"github.com/agentmesh/backend/internal/wallet"
)

func TestTriggerRun(t *testing.T) {
	d := testDeps(t)
	d.Broker = sse.NewBroker()
	d.Wallet = wallet.NewService("0123456789abcdef0123456789abcdef",
		"https://testnet-api.algonode.cloud", "", "testnet")
	d.Engine = engine.NewRunner(d.Store, d.Broker, d.Wallet, "http://localhost:8080", "", "", engine.X402Config{USDCAssetID: 10458941})

	wf, _ := d.Store.CreateWorkflow(t.Context(), "Run Test", "dev")
	t.Cleanup(func() { d.Store.DeleteWorkflow(context.Background(), wf.ID) })

	body, _ := json.Marshal(map[string]string{"message": "hello"})
	req := httptest.NewRequest(http.MethodPost, "/workflows/"+wf.ID+"/run", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), handlers.CtxUserID, "dev"))
	req = withURLParam(req, "id", wf.ID)
	w := httptest.NewRecorder()
	d.TriggerRun(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("want 202 got %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["runId"] == "" {
		t.Fatal("no runId")
	}
}

// TestTriggerRunCooldownRetryAfterNeverUndershoots guards against a
// regression to %.0f-style rounding: a Retry-After that rounds the true
// remaining cooldown DOWN (e.g. 4.6s -> "4") tells a compliant client to
// retry before the cooldown has actually elapsed, earning it a second,
// avoidable 429. The header must always be >= the real remaining seconds.
func TestTriggerRunCooldownRetryAfterNeverUndershoots(t *testing.T) {
	d := testDeps(t)
	d.Broker = sse.NewBroker()
	d.Wallet = wallet.NewService("0123456789abcdef0123456789abcdef",
		"https://testnet-api.algonode.cloud", "", "testnet")
	d.Engine = engine.NewRunner(d.Store, d.Broker, d.Wallet, "http://localhost:8080", "", "", engine.X402Config{USDCAssetID: 10458941})

	wf, _ := d.Store.CreateWorkflow(t.Context(), "Cooldown Retry-After Test", "dev")
	t.Cleanup(func() { d.Store.DeleteWorkflow(context.Background(), wf.ID) })

	trigger := func() *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"message": "hello"})
		req := httptest.NewRequest(http.MethodPost, "/workflows/"+wf.ID+"/run", bytes.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), handlers.CtxUserID, "dev"))
		req = withURLParam(req, "id", wf.ID)
		w := httptest.NewRecorder()
		d.TriggerRun(w, req)
		return w
	}

	if w := trigger(); w.Code != http.StatusAccepted {
		t.Fatalf("first trigger: want 202 got %d body=%s", w.Code, w.Body.String())
	}
	before := time.Now()
	w := trigger()
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("second trigger: want 429 got %d body=%s", w.Code, w.Body.String())
	}
	elapsed := time.Since(before)

	retryAfter := w.Header().Get("Retry-After")
	if strings.Contains(retryAfter, ".") {
		t.Fatalf("Retry-After should be a whole number of seconds, got %q", retryAfter)
	}
	got, err := strconv.Atoi(retryAfter)
	if err != nil {
		t.Fatalf("Retry-After not an integer: %q: %v", retryAfter, err)
	}
	// The true remaining cooldown when the header was produced was at most
	// (5s - elapsed since the first trigger), and math.Ceil never reports
	// less than that; %.0f-style rounding could have reported one second
	// less whenever the true remainder had a fractional part < 0.5.
	minAcceptable := int(math.Ceil((5*time.Second - elapsed).Seconds()))
	if got < minAcceptable {
		t.Fatalf("Retry-After=%d understates the remaining cooldown (want >= %d)", got, minAcceptable)
	}
}

// TestPublicTriggerRejectsNonWebhookTriggerEvenIfDeployed guards the actual
// security fix: the public /run/{workflowId} path used to accept ANY
// deployed workflow with a trigger node of any template, so a "manual" or
// "chat" trigger workflow could be fired unauthenticated the same way a
// "webhook" one legitimately can. It must now require the deployed
// workflow's trigger node be specifically template "webhook".
func TestPublicTriggerRejectsNonWebhookTriggerEvenIfDeployed(t *testing.T) {
	d := testDeps(t)
	d.Broker = sse.NewBroker()
	d.Wallet = wallet.NewService(testEncryptionKey, "https://testnet-api.algonode.cloud", "", "testnet")
	d.Engine = engine.NewRunner(d.Store, d.Broker, d.Wallet, "http://localhost:8080", "", "", engine.X402Config{USDCAssetID: 10458941})
	d.BaseURL = "http://localhost:8080"

	ctx := context.Background()
	wf, _ := d.Store.CreateWorkflow(ctx, "Manual Trigger Public Test", "dev")
	t.Cleanup(func() { d.Store.DeleteWorkflow(context.Background(), wf.ID) })

	graph := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{
			{ID: "n1", Type: models.NodeTypeTrigger, Template: "manual"},
			{ID: "n2", Type: models.NodeTypeAgent, Name: "My Agent"},
		},
		Edges: []models.WorkflowEdge{{ID: "e1", From: "n1", To: "n2", Kind: models.EdgeKindFlow}},
	}
	if _, err := d.Store.UpdateWorkflow(ctx, wf.ID, "Manual Trigger Public Test", graph); err != nil {
		t.Fatal(err)
	}

	deployReq := httptest.NewRequest(http.MethodPost, "/workflows/"+wf.ID+"/deploy", nil)
	deployReq = deployReq.WithContext(context.WithValue(deployReq.Context(), handlers.CtxUserID, "dev"))
	deployReq = withURLParam(deployReq, "id", wf.ID)
	dw := httptest.NewRecorder()
	d.Deploy(dw, deployReq)
	if dw.Code != http.StatusOK {
		t.Fatalf("deploy: want 200 got %d body=%s", dw.Code, dw.Body.String())
	}

	req := httptest.NewRequest(http.MethodPost, "/run/"+wf.ID, bytes.NewReader([]byte("{}")))
	req = withURLParam(req, "workflowId", wf.ID)
	w := httptest.NewRecorder()
	d.PublicTrigger(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("manual-trigger workflow via public endpoint: want 404 got %d body=%s", w.Code, w.Body.String())
	}
}

// TestPublicTriggerRequiresCorrectWebhookSecret covers the other half of the
// fix: a "webhook"-trigger workflow is still publicly reachable (that's the
// feature), but only with the secret generated for it -- missing or wrong
// secrets are rejected, and the right one starts a real run.
func TestPublicTriggerRequiresCorrectWebhookSecret(t *testing.T) {
	d := testDeps(t)
	d.Broker = sse.NewBroker()
	d.Wallet = wallet.NewService(testEncryptionKey, "https://testnet-api.algonode.cloud", "", "testnet")
	d.Engine = engine.NewRunner(d.Store, d.Broker, d.Wallet, "http://localhost:8080", "", "", engine.X402Config{USDCAssetID: 10458941})
	d.BaseURL = "http://localhost:8080"

	ctx := context.Background()
	wf, _ := d.Store.CreateWorkflow(ctx, "Webhook Public Test", "dev")
	t.Cleanup(func() { d.Store.DeleteWorkflow(context.Background(), wf.ID) })

	// Saved through the HTTP handler (not d.Store.UpdateWorkflow directly)
	// so ensureWebhookSecrets actually runs and generates a real secret.
	body, _ := json.Marshal(map[string]any{
		"name": "Webhook Public Test",
		"nodes": []map[string]any{
			{"id": "n1", "type": "trigger", "template": "webhook"},
			{"id": "n2", "type": "agent", "name": "My Agent"},
		},
		"edges": []map[string]any{{"id": "e1", "from": "n1", "to": "n2", "kind": "flow"}},
	})
	saveReq := httptest.NewRequest(http.MethodPut, "/workflows/"+wf.ID, bytes.NewReader(body))
	saveReq = saveReq.WithContext(context.WithValue(saveReq.Context(), handlers.CtxUserID, "dev"))
	saveReq = withURLParam(saveReq, "id", wf.ID)
	sw := httptest.NewRecorder()
	d.UpdateWorkflow(sw, saveReq)
	if sw.Code != http.StatusOK {
		t.Fatalf("save: want 200 got %d body=%s", sw.Code, sw.Body.String())
	}
	var saved models.Workflow
	json.NewDecoder(sw.Body).Decode(&saved)
	secret := saved.Nodes[0].Secrets["webhookSecret"]
	if secret == "" {
		t.Fatal("expected a webhookSecret to be generated")
	}

	deployReq := httptest.NewRequest(http.MethodPost, "/workflows/"+wf.ID+"/deploy", nil)
	deployReq = deployReq.WithContext(context.WithValue(deployReq.Context(), handlers.CtxUserID, "dev"))
	deployReq = withURLParam(deployReq, "id", wf.ID)
	dw := httptest.NewRecorder()
	d.Deploy(dw, deployReq)
	if dw.Code != http.StatusOK {
		t.Fatalf("deploy: want 200 got %d body=%s", dw.Code, dw.Body.String())
	}

	// Missing secret -> 404, same as a nonexistent workflow (no leak).
	noneReq := httptest.NewRequest(http.MethodPost, "/run/"+wf.ID, bytes.NewReader([]byte("{}")))
	noneReq = withURLParam(noneReq, "workflowId", wf.ID)
	nw := httptest.NewRecorder()
	d.PublicTrigger(nw, noneReq)
	if nw.Code != http.StatusNotFound {
		t.Fatalf("missing secret: want 404 got %d body=%s", nw.Code, nw.Body.String())
	}

	// Wrong secret -> 404.
	badReq := httptest.NewRequest(http.MethodPost, "/run/"+wf.ID, bytes.NewReader([]byte("{}")))
	badReq.Header.Set("X-Webhook-Secret", "wrong-secret")
	badReq = withURLParam(badReq, "workflowId", wf.ID)
	bw := httptest.NewRecorder()
	d.PublicTrigger(bw, badReq)
	if bw.Code != http.StatusNotFound {
		t.Fatalf("wrong secret: want 404 got %d body=%s", bw.Code, bw.Body.String())
	}

	// Correct secret -> 202, a real run starts.
	goodReq := httptest.NewRequest(http.MethodPost, "/run/"+wf.ID, bytes.NewReader([]byte("{}")))
	goodReq.Header.Set("X-Webhook-Secret", secret)
	goodReq = withURLParam(goodReq, "workflowId", wf.ID)
	gw := httptest.NewRecorder()
	d.PublicTrigger(gw, goodReq)
	if gw.Code != http.StatusAccepted {
		t.Fatalf("correct secret: want 202 got %d body=%s", gw.Code, gw.Body.String())
	}
	var runResp map[string]string
	json.NewDecoder(gw.Body).Decode(&runResp)
	if runResp["runId"] == "" {
		t.Fatal("no runId")
	}
}

// TestResumeRunNonOwnerGets404EvenWhileRunning guards the fix for a
// self-review finding: ResumeRun used to check run.Status == running
// before verifying the caller owns the run's workflow, so a non-owner (or
// a caller enumerating run IDs) got a 409 "already in progress" -- leaking
// that the run exists and is live -- instead of the generic 404 every
// other branch (and every sibling handler) returns for a run that isn't
// theirs. Ownership must be checked first regardless of run state.
func TestResumeRunNonOwnerGets404EvenWhileRunning(t *testing.T) {
	d := testDeps(t)
	ctx := context.Background()

	wf, err := d.Store.CreateWorkflow(ctx, "Resume Ownership Test", "owner-user")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Store.DeleteWorkflow(context.Background(), wf.ID) })

	// CreateRun inserts with status='running' directly, so this run is
	// live without needing a real engine to execute anything.
	run, err := d.Store.CreateRun(ctx, wf.ID, "manual", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/runs/"+run.ID+"/resume", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.CtxUserID, "attacker-user"))
	req = withURLParam(req, "runId", run.ID)
	w := httptest.NewRecorder()
	d.ResumeRun(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("non-owner resume of a running run: want 404 (no leak) got %d body=%s", w.Code, w.Body.String())
	}
}
