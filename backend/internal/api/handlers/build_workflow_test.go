package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentmesh/backend/internal/api/handlers"
	"github.com/agentmesh/backend/internal/engine/nodes"
)

// buildWorkflowReq issues POST /workflows/{id}/build as userID.
func buildWorkflowReq(d *handlers.Deps, workflowID, userID, message string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]string{"message": message})
	req := withURLParam(httptest.NewRequest(http.MethodPost, "/workflows/"+workflowID+"/build", bytes.NewReader(body)), "id", workflowID)
	d.BuildWorkflow(rec, withUser(req, userID))
	return rec
}

func TestBuildWorkflowAddsNode(t *testing.T) {
	d := testDeps(t)
	d.PlatformGeminiAPIKey = "test-key"
	ctx := context.Background()

	user, err := d.Store.CreateUser(ctx, "wf-build-"+randSuffix(t)+"@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	wf, err := d.Store.CreateWorkflow(ctx, "Build Me", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Store.DeleteWorkflow(context.Background(), wf.ID) })

	// Mock Gemini: first response is a functionCall for add_node (driving the
	// meta-agent's tool-calling loop), second is the final text reply -- the
	// same two-step shape as TestBuildGraphAddsNodeThenReturnsReply in
	// engine/nodes/graphbuilder_test.go.
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if callCount == 1 {
			json.NewEncoder(w).Encode(map[string]any{
				"candidates": []map[string]any{
					{"content": map[string]any{"parts": []map[string]any{
						{"functionCall": map[string]any{
							"name": "add_node",
							"args": map[string]any{"type": "trigger", "template": "chat", "name": "On Chat"},
						}},
					}}},
				},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{
				{"content": map[string]any{"parts": []map[string]any{
					{"text": "Added a chat trigger node."},
				}}},
			},
		})
	}))
	defer srv.Close()
	nodes.SetGeminiBaseURL(srv.URL)
	defer nodes.SetGeminiBaseURL("https://generativelanguage.googleapis.com")

	rec := buildWorkflowReq(d, wf.ID, user.ID, "add a chat trigger")
	if rec.Code != http.StatusOK {
		t.Fatalf("build got %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var out struct {
		Reply    string `json:"reply"`
		Workflow struct {
			Nodes []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Template string `json:"template"`
				Name     string `json:"name"`
			} `json:"nodes"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Reply != "Added a chat trigger node." {
		t.Fatalf("unexpected reply: %q", out.Reply)
	}
	if len(out.Workflow.Nodes) != 1 {
		t.Fatalf("expected 1 node in response.workflow.nodes, got %d: %+v", len(out.Workflow.Nodes), out.Workflow.Nodes)
	}
	if got := out.Workflow.Nodes[0]; got.Type != "trigger" || got.Template != "chat" {
		t.Fatalf("expected added trigger/chat node, got type=%q template=%q", got.Type, got.Template)
	}
}

// TestBuildWorkflowPreservesUnrelatedSecret verifies the security-critical
// round trip the handler relies on: an existing node's real API key must
// never be sent to the meta-agent, and an untouched node's encrypted DB
// value must survive a build-mode edit byte-for-byte even when that edit
// only adds an unrelated node elsewhere in the graph.
func TestBuildWorkflowPreservesUnrelatedSecret(t *testing.T) {
	d := testDeps(t)
	d.PlatformGeminiAPIKey = "test-key"
	ctx := context.Background()

	user, err := d.Store.CreateUser(ctx, "wf-build-secret-"+randSuffix(t)+"@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	wf, err := d.Store.CreateWorkflow(ctx, "Secret Build", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Store.DeleteWorkflow(context.Background(), wf.ID) })

	// Seed a node with a real API key through the same save path
	// UpdateWorkflow uses, mirroring TestAPIKeyEncryption's setup style.
	seedBody, _ := json.Marshal(map[string]any{
		"name": "Secret Build",
		"nodes": []map[string]any{
			{"id": "n1", "type": "provider", "template": "gemini", "apiKey": "my-real-secret-key-456"},
		},
		"edges": []any{},
	})
	seedReq := withURLParam(httptest.NewRequest(http.MethodPut, "/workflows/"+wf.ID, bytes.NewReader(seedBody)), "id", wf.ID)
	seedRec := httptest.NewRecorder()
	d.UpdateWorkflow(seedRec, withUser(seedReq, user.ID))
	if seedRec.Code != http.StatusOK {
		t.Fatalf("seed: want 200 got %d (body: %s)", seedRec.Code, seedRec.Body.String())
	}

	rawBefore, err := d.Store.GetWorkflow(ctx, wf.ID)
	if err != nil {
		t.Fatal(err)
	}
	var encryptedBefore string
	for _, n := range rawBefore.Nodes {
		if n.ID == "n1" {
			encryptedBefore = n.APIKey
		}
	}
	if !strings.HasPrefix(encryptedBefore, "enc:") {
		t.Fatalf("seed: want encrypted blob (enc: prefix), got %q", encryptedBefore)
	}

	// Mock Gemini adds an unrelated trigger node without ever referencing n1.
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if callCount == 1 {
			json.NewEncoder(w).Encode(map[string]any{
				"candidates": []map[string]any{
					{"content": map[string]any{"parts": []map[string]any{
						{"functionCall": map[string]any{
							"name": "add_node",
							"args": map[string]any{"type": "trigger", "template": "chat", "name": "On Chat"},
						}},
					}}},
				},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{
				{"content": map[string]any{"parts": []map[string]any{
					{"text": "Added an unrelated trigger node."},
				}}},
			},
		})
	}))
	defer srv.Close()
	nodes.SetGeminiBaseURL(srv.URL)
	defer nodes.SetGeminiBaseURL("https://generativelanguage.googleapis.com")

	rec := buildWorkflowReq(d, wf.ID, user.ID, "add an unrelated trigger")
	if rec.Code != http.StatusOK {
		t.Fatalf("build got %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	var out struct {
		Workflow struct {
			Nodes []struct {
				ID     string `json:"id"`
				Type   string `json:"type"`
				APIKey string `json:"apiKey"`
			} `json:"nodes"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}

	// (a) The HTTP response must never contain the plaintext or raw
	// ciphertext for n1's API key -- only the mask sentinel.
	found := false
	for _, n := range out.Workflow.Nodes {
		if n.ID != "n1" {
			continue
		}
		found = true
		if n.APIKey != handlers.EncSentinel {
			t.Errorf("response: n1 apiKey want sentinel %q, got %q", handlers.EncSentinel, n.APIKey)
		}
	}
	if !found {
		t.Fatal("n1 missing from build response -- unrelated edit should not have removed it")
	}

	// (b) The DB's encrypted blob for n1 must be byte-for-byte unchanged --
	// proving the round trip didn't corrupt or blank the real secret.
	rawAfter, err := d.Store.GetWorkflow(ctx, wf.ID)
	if err != nil {
		t.Fatal(err)
	}
	var encryptedAfter string
	for _, n := range rawAfter.Nodes {
		if n.ID == "n1" {
			encryptedAfter = n.APIKey
		}
	}
	if encryptedAfter != encryptedBefore {
		t.Fatalf("secret round trip: DB blob changed; was %q, now %q", encryptedBefore, encryptedAfter)
	}
}

func TestBuildWorkflowRequiresPlatformKey(t *testing.T) {
	d := testDeps(t)
	ctx := context.Background()

	user, err := d.Store.CreateUser(ctx, "wf-build-nokey-"+randSuffix(t)+"@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	wf, err := d.Store.CreateWorkflow(ctx, "No Key", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Store.DeleteWorkflow(context.Background(), wf.ID) })

	rec := buildWorkflowReq(d, wf.ID, user.ID, "add a trigger")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestBuildWorkflowOtherUserGets404(t *testing.T) {
	d := testDeps(t)
	d.PlatformGeminiAPIKey = "test-key"
	ctx := context.Background()

	owner, err := d.Store.CreateUser(ctx, "wf-build-owner-"+randSuffix(t)+"@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	other, err := d.Store.CreateUser(ctx, "wf-build-other-"+randSuffix(t)+"@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	wf, err := d.Store.CreateWorkflow(ctx, "Not Yours", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Store.DeleteWorkflow(context.Background(), wf.ID) })

	rec := buildWorkflowReq(d, wf.ID, other.ID, "add a trigger")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("other user got %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
}
