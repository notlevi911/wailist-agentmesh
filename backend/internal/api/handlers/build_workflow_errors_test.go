package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/agentmesh/backend/internal/engine/nodes"
)

// Coverage the #67 review found missing for POST /workflows/{id}/build's two
// refusal paths: a request with nothing to build, and an upstream model
// failure. Both are cheap to get wrong silently -- the first spends a
// platform-key model call on nothing, the second puts the provider's raw
// error text (which can echo request details) into the user's chat bubble.

// TestBuildWorkflowEmptyMessageIs400 pins that a blank message is refused
// before any model call is made and before anything is recorded.
func TestBuildWorkflowEmptyMessageIs400(t *testing.T) {
	d := testDeps(t)
	d.PlatformGeminiAPIKey = "test-key"
	ctx := context.Background()

	user, err := d.Store.CreateUser(ctx, "wf-build-empty-"+randSuffix(t)+"@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	wf, err := d.Store.CreateWorkflow(ctx, "Empty Message", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Store.DeleteWorkflow(context.Background(), wf.ID) })

	var modelCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&modelCalls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	nodes.SetGeminiBaseURL(srv.URL)
	defer nodes.SetGeminiBaseURL("https://generativelanguage.googleapis.com")

	for _, message := range []string{"", "   ", "\n\t"} {
		rec := buildWorkflowReq(d, wf.ID, user.ID, message)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("message %q: got %d, want 400 (body: %s)", message, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "message required") {
			t.Fatalf("message %q: body %q does not say the message is required", message, rec.Body.String())
		}
	}

	if got := atomic.LoadInt32(&modelCalls); got != 0 {
		t.Fatalf("a blank message must not reach the model, got %d calls", got)
	}
	msgs, err := d.Store.GetBuildMessages(ctx, wf.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Fatalf("a refused build must record nothing, got %d turns: %+v", len(msgs), msgs)
	}
}

// TestBuildWorkflowUpstreamErrorIsSanitized502 pins that a model failure
// before anything was built comes back as a plain 502, never with the
// upstream response body in it. The fake upstream answers 400, which is not
// a transient failure, so no retry policy changes what this test sees.
func TestBuildWorkflowUpstreamErrorIsSanitized502(t *testing.T) {
	d := testDeps(t)
	d.PlatformGeminiAPIKey = "test-key"
	ctx := context.Background()

	user, err := d.Store.CreateUser(ctx, "wf-build-502-"+randSuffix(t)+"@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	wf, err := d.Store.CreateWorkflow(ctx, "Upstream Fails", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Store.DeleteWorkflow(context.Background(), wf.ID) })

	// Stands in for a provider error body that echoes request details.
	const leak = "UPSTREAM-DETAIL-x-goog-api-key=test-key"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"code":400,"message":"` + leak + `","status":"INVALID_ARGUMENT"}}`))
	}))
	defer srv.Close()
	nodes.SetGeminiBaseURL(srv.URL)
	defer nodes.SetGeminiBaseURL("https://generativelanguage.googleapis.com")

	rec := buildWorkflowReq(d, wf.ID, user.ID, "add a chat trigger")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("got %d, want 502 (body: %s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, leak) || strings.Contains(body, "INVALID_ARGUMENT") {
		t.Fatalf("502 body leaks the upstream error: %s", body)
	}
	if !strings.Contains(body, "could not complete this request") {
		t.Fatalf("502 body %q is not the plain builder error", body)
	}
}
