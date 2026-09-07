package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/agentmesh/backend/internal/api/handlers"
	"github.com/agentmesh/backend/internal/db"
	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/wallet"
)

const testEncryptionKey = "0123456789abcdef0123456789abcdef"

func testDeps(t *testing.T) *handlers.Deps {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	store, err := db.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	return &handlers.Deps{Store: store, EncryptionKey: testEncryptionKey}
}

func withURLParam(r *http.Request, key, val string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, val)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// TestAPIKeyEncryption verifies the full encrypt → mask → decrypt lifecycle:
//   - Saving a plaintext key stores an encrypted blob in the DB
//   - GET and UpdateWorkflow responses always return the sentinel, never plaintext or raw cipher
//   - Re-saving with the sentinel preserves the existing encrypted blob unchanged
//   - The encrypted DB value decrypts back to the original key
func TestAPIKeyEncryption(t *testing.T) {
	d := testDeps(t)

	wf, err := d.Store.CreateWorkflow(t.Context(), "SecretTest", "dev")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Store.DeleteWorkflow(context.Background(), wf.ID) })

	// ── 1. Save a workflow with a plaintext API key ──────────────────────────
	body, _ := json.Marshal(map[string]any{
		"name": "SecretTest",
		"nodes": []map[string]any{
			{"id": "n1", "type": "provider", "template": "gemini", "apiKey": "my-test-api-key-123"},
		},
		"edges": []any{},
	})
	req := httptest.NewRequest(http.MethodPut, "/workflows/"+wf.ID, bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), handlers.CtxUserID, "dev"))
	req = withURLParam(req, "id", wf.ID)
	w := httptest.NewRecorder()
	d.UpdateWorkflow(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: want 200 got %d body=%s", w.Code, w.Body.String())
	}

	var updated models.Workflow
	json.NewDecoder(w.Body).Decode(&updated)
	apiKeyInResponse := ""
	for _, n := range updated.Nodes {
		if n.ID == "n1" {
			apiKeyInResponse = n.APIKey
		}
	}
	if apiKeyInResponse != handlers.EncSentinel {
		t.Errorf("UpdateWorkflow response: want %q got %q", handlers.EncSentinel, apiKeyInResponse)
	}

	// ── 2. GET must also return the sentinel, never plaintext ────────────────
	req2 := httptest.NewRequest(http.MethodGet, "/workflows/"+wf.ID, nil)
	req2 = req2.WithContext(context.WithValue(req2.Context(), handlers.CtxUserID, "dev"))
	req2 = withURLParam(req2, "id", wf.ID)
	w2 := httptest.NewRecorder()
	d.GetWorkflow(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("get: want 200 got %d", w2.Code)
	}
	var got models.Workflow
	json.NewDecoder(w2.Body).Decode(&got)
	for _, n := range got.Nodes {
		if n.ID == "n1" && n.APIKey != handlers.EncSentinel {
			t.Errorf("GET response: want %q got %q", handlers.EncSentinel, n.APIKey)
		}
	}

	// ── 3. Raw DB value must be an encrypted blob, not plaintext ────────────
	rawWF, err := d.Store.GetWorkflow(t.Context(), wf.ID)
	if err != nil {
		t.Fatal(err)
	}
	var rawKey string
	for _, n := range rawWF.Nodes {
		if n.ID == "n1" {
			rawKey = n.APIKey
		}
	}
	if !strings.HasPrefix(rawKey, "enc:") {
		t.Errorf("DB value: want enc: prefix, got %q", rawKey)
	}

	// ── 4. DB blob decrypts back to the original key ─────────────────────────
	plain, err := wallet.Decrypt(strings.TrimPrefix(rawKey, "enc:"), testEncryptionKey)
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}
	if plain != "my-test-api-key-123" {
		t.Errorf("decrypted: want %q got %q", "my-test-api-key-123", plain)
	}

	// ── 5. Re-saving with the sentinel keeps the existing encrypted blob ─────
	body2, _ := json.Marshal(map[string]any{
		"name": "SecretTest",
		"nodes": []map[string]any{
			{"id": "n1", "type": "provider", "template": "gemini", "apiKey": handlers.EncSentinel},
		},
		"edges": []any{},
	})
	req3 := httptest.NewRequest(http.MethodPut, "/workflows/"+wf.ID, bytes.NewReader(body2))
	req3 = req3.WithContext(context.WithValue(req3.Context(), handlers.CtxUserID, "dev"))
	req3 = withURLParam(req3, "id", wf.ID)
	w3 := httptest.NewRecorder()
	d.UpdateWorkflow(w3, req3)
	if w3.Code != http.StatusOK {
		t.Fatalf("re-save sentinel: want 200 got %d", w3.Code)
	}
	rawWF2, _ := d.Store.GetWorkflow(t.Context(), wf.ID)
	for _, n := range rawWF2.Nodes {
		if n.ID == "n1" && n.APIKey != rawKey {
			t.Errorf("sentinel re-save: encrypted blob changed; was %q, now %q", rawKey, n.APIKey)
		}
	}
}

func TestCreateAndGetWorkflow(t *testing.T) {
	d := testDeps(t)

	body, _ := json.Marshal(map[string]string{"name": "My WF"})
	req := httptest.NewRequest(http.MethodPost, "/workflows", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), handlers.CtxUserID, "dev"))
	w := httptest.NewRecorder()
	d.CreateWorkflow(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: want 201 got %d body=%s", w.Code, w.Body.String())
	}

	var wf models.Workflow
	json.NewDecoder(w.Body).Decode(&wf)
	if wf.ID == "" {
		t.Fatal("no id")
	}
	t.Cleanup(func() { d.Store.DeleteWorkflow(context.Background(), wf.ID) })

	req2 := httptest.NewRequest(http.MethodGet, "/workflows/"+wf.ID, nil)
	req2 = req2.WithContext(context.WithValue(req2.Context(), handlers.CtxUserID, "dev"))
	req2 = withURLParam(req2, "id", wf.ID)
	w2 := httptest.NewRecorder()
	d.GetWorkflow(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("get: want 200 got %d", w2.Code)
	}
}

// TestWebhookSecretGeneratedAndPersisted verifies the webhookSecret
// lifecycle a webhook trigger node relies on for PublicTrigger auth (see
// runs_test.go): generated on first save, returned in plaintext (not the
// EncSentinel every other secret gets) so the owner can actually copy it,
// stable across re-saves, and stored encrypted at rest in the DB.
func TestWebhookSecretGeneratedAndPersisted(t *testing.T) {
	d := testDeps(t)

	wf, err := d.Store.CreateWorkflow(t.Context(), "Webhook Secret Test", "dev")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Store.DeleteWorkflow(context.Background(), wf.ID) })

	body, _ := json.Marshal(map[string]any{
		"name": "Webhook Secret Test",
		"nodes": []map[string]any{
			{"id": "n1", "type": "trigger", "template": "webhook"},
		},
		"edges": []any{},
	})
	req := httptest.NewRequest(http.MethodPut, "/workflows/"+wf.ID, bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), handlers.CtxUserID, "dev"))
	req = withURLParam(req, "id", wf.ID)
	w := httptest.NewRecorder()
	d.UpdateWorkflow(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: want 200 got %d body=%s", w.Code, w.Body.String())
	}

	var updated models.Workflow
	json.NewDecoder(w.Body).Decode(&updated)
	secret := updated.Nodes[0].Secrets["webhookSecret"]
	if secret == "" {
		t.Fatal("expected a webhookSecret to be generated")
	}
	if secret == handlers.EncSentinel || strings.HasPrefix(secret, "enc:") {
		t.Fatalf("expected plaintext secret in response, got %q", secret)
	}

	// GET returns the identical plaintext secret, not the sentinel other
	// secrets get.
	req2 := httptest.NewRequest(http.MethodGet, "/workflows/"+wf.ID, nil)
	req2 = req2.WithContext(context.WithValue(req2.Context(), handlers.CtxUserID, "dev"))
	req2 = withURLParam(req2, "id", wf.ID)
	w2 := httptest.NewRecorder()
	d.GetWorkflow(w2, req2)
	var fetched models.Workflow
	json.NewDecoder(w2.Body).Decode(&fetched)
	if fetched.Nodes[0].Secrets["webhookSecret"] != secret {
		t.Fatalf("GET secret: want %q got %q", secret, fetched.Nodes[0].Secrets["webhookSecret"])
	}

	// Raw DB value is encrypted, never plaintext at rest.
	rawWF, err := d.Store.GetWorkflow(t.Context(), wf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rawWF.Nodes[0].Secrets["webhookSecret"], "enc:") {
		t.Errorf("DB value: want enc: prefix, got %q", rawWF.Nodes[0].Secrets["webhookSecret"])
	}

	// Re-saving (a normal canvas autosave) does not rotate the secret.
	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodPut, "/workflows/"+wf.ID, bytes.NewReader(body))
	req3 = req3.WithContext(context.WithValue(req3.Context(), handlers.CtxUserID, "dev"))
	req3 = withURLParam(req3, "id", wf.ID)
	d.UpdateWorkflow(w3, req3)
	var resaved models.Workflow
	json.NewDecoder(w3.Body).Decode(&resaved)
	if resaved.Nodes[0].Secrets["webhookSecret"] != secret {
		t.Fatalf("secret rotated on re-save: want %q got %q", secret, resaved.Nodes[0].Secrets["webhookSecret"])
	}
}

// TestUpdateWorkflowClampsRetryFields guards against a node being saved
// with an unbounded retry configuration -- MaxRetries/RetryBackoffMs had no
// server-side cap, so a workflow could be saved to retry a node for hours
// against a third-party endpoint. UpdateWorkflow must clamp both fields to
// models.MaxNodeRetries/MaxNodeRetryBackoffMs (and floor a negative value
// to 0) regardless of what the client sends.
func TestUpdateWorkflowClampsRetryFields(t *testing.T) {
	d := testDeps(t)
	ctx := context.Background()

	wf, err := d.Store.CreateWorkflow(ctx, "Retry Clamp Test", "dev")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Store.DeleteWorkflow(context.Background(), wf.ID) })

	body, _ := json.Marshal(map[string]any{
		"name": "Retry Clamp Test",
		"nodes": []map[string]any{
			{"id": "n1", "type": "trigger", "template": "manual"},
			{"id": "n2", "type": "tool", "template": "http", "url": "http://example.com", "method": "GET",
				"maxRetries": 100000, "retryBackoffMs": 600000},
			{"id": "n3", "type": "end"},
			{"id": "n4", "type": "tool", "template": "http", "url": "http://example.com", "method": "GET",
				"maxRetries": -5, "retryBackoffMs": -5},
		},
		"edges": []map[string]any{
			{"id": "e1", "from": "n1", "to": "n2", "kind": "flow"},
			{"id": "e2", "from": "n2", "to": "n3", "kind": "flow"},
		},
	})
	req := httptest.NewRequest(http.MethodPut, "/workflows/"+wf.ID, bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), handlers.CtxUserID, "dev"))
	req = withURLParam(req, "id", wf.ID)
	w := httptest.NewRecorder()
	d.UpdateWorkflow(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", w.Code, w.Body.String())
	}

	var saved models.Workflow
	json.NewDecoder(w.Body).Decode(&saved)
	byID := map[string]models.WorkflowNode{}
	for _, n := range saved.Nodes {
		byID[n.ID] = n
	}
	if got := byID["n2"].MaxRetries; got != models.MaxNodeRetries {
		t.Errorf("n2 MaxRetries: want clamped to %d, got %d", models.MaxNodeRetries, got)
	}
	if got := byID["n2"].RetryBackoffMs; got != models.MaxNodeRetryBackoffMs {
		t.Errorf("n2 RetryBackoffMs: want clamped to %d, got %d", models.MaxNodeRetryBackoffMs, got)
	}
	if got := byID["n4"].MaxRetries; got != 0 {
		t.Errorf("n4 MaxRetries: want negative floored to 0, got %d", got)
	}
	if got := byID["n4"].RetryBackoffMs; got != 0 {
		t.Errorf("n4 RetryBackoffMs: want negative floored to 0, got %d", got)
	}

	// Re-fetching from the DB confirms the clamp was actually persisted,
	// not just reflected in this response.
	fetched, err := d.Store.GetWorkflow(ctx, wf.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range fetched.Nodes {
		if n.ID == "n2" && (n.MaxRetries != models.MaxNodeRetries || n.RetryBackoffMs != models.MaxNodeRetryBackoffMs) {
			t.Errorf("persisted n2: want clamped values, got maxRetries=%d retryBackoffMs=%d", n.MaxRetries, n.RetryBackoffMs)
		}
	}
}
