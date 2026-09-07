package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentmesh/backend/internal/api/handlers"
	"github.com/agentmesh/backend/internal/engine"
	"github.com/agentmesh/backend/internal/sse"
	"github.com/agentmesh/backend/internal/wallet"
)

// TestStopWorkflowWrongUser verifies that a user cannot stop another user's workflow.
func TestStopWorkflowWrongUser(t *testing.T) {
	d := testDeps(t)
	d.Broker = sse.NewBroker()
	d.Wallet = wallet.NewService("0123456789abcdef0123456789abcdef",
		"https://testnet-api.algonode.cloud", "", "testnet")
	d.Engine = engine.NewRunner(d.Store, d.Broker, d.Wallet, "http://localhost:8080", "", "", engine.X402Config{USDCAssetID: 10458941})

	ctx := context.Background()
	wf, _ := d.Store.CreateWorkflow(ctx, "Stop Test", "owner-user")
	t.Cleanup(func() { d.Store.DeleteWorkflow(context.Background(), wf.ID) })

	req := httptest.NewRequest(http.MethodPost, "/workflows/"+wf.ID+"/stop", nil)
	// Request authenticated as a different user.
	req = req.WithContext(context.WithValue(req.Context(), handlers.CtxUserID, "other-user"))
	req = withURLParam(req, "id", wf.ID)
	w := httptest.NewRecorder()
	d.StopWorkflow(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404 got %d body=%s", w.Code, w.Body.String())
	}
}

// TestStopWorkflowNoActiveRun verifies that stopping a workflow with no active
// run still returns 204 (idempotent — the workflow exists and belongs to the user).
func TestStopWorkflowNoActiveRun(t *testing.T) {
	d := testDeps(t)
	d.Broker = sse.NewBroker()
	d.Wallet = wallet.NewService("0123456789abcdef0123456789abcdef",
		"https://testnet-api.algonode.cloud", "", "testnet")
	d.Engine = engine.NewRunner(d.Store, d.Broker, d.Wallet, "http://localhost:8080", "", "", engine.X402Config{USDCAssetID: 10458941})

	ctx := context.Background()
	wf, _ := d.Store.CreateWorkflow(ctx, "Stop Idle Test", "dev")
	t.Cleanup(func() { d.Store.DeleteWorkflow(context.Background(), wf.ID) })

	req := httptest.NewRequest(http.MethodPost, "/workflows/"+wf.ID+"/stop", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.CtxUserID, "dev"))
	req = withURLParam(req, "id", wf.ID)
	w := httptest.NewRecorder()
	d.StopWorkflow(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204 got %d body=%s", w.Code, w.Body.String())
	}
}

// TestStopWorkflowNotFound verifies that stopping a nonexistent workflow returns 404.
func TestStopWorkflowNotFound(t *testing.T) {
	d := testDeps(t)
	d.Broker = sse.NewBroker()
	d.Wallet = wallet.NewService("0123456789abcdef0123456789abcdef",
		"https://testnet-api.algonode.cloud", "", "testnet")
	d.Engine = engine.NewRunner(d.Store, d.Broker, d.Wallet, "http://localhost:8080", "", "", engine.X402Config{USDCAssetID: 10458941})

	req := httptest.NewRequest(http.MethodPost, "/workflows/does-not-exist/stop", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.CtxUserID, "dev"))
	req = withURLParam(req, "id", "does-not-exist")
	w := httptest.NewRecorder()
	d.StopWorkflow(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404 got %d body=%s", w.Code, w.Body.String())
	}
}
