package nodes_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentmesh/backend/internal/engine"
	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
)

// TestHTTPTool_ServerErrorRetryableOnlyForIdempotentMethod verifies the
// retry-safety rule: a GET failing with a 5xx is safe to retry (nothing
// non-repeatable happened server-side), but the identical failure on a POST
// is not, because the write may have already been applied before the 500
// came back.
func TestHTTPTool_ServerErrorRetryableOnlyForIdempotentMethod(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	get := models.WorkflowNode{ID: "t1", Type: models.NodeTypeTool, Template: "http", URL: srv.URL, Method: "GET"}
	_, err := nodes.ExecuteTool(context.Background(), get, engine.NewRunContext("r1", nil))
	if err == nil {
		t.Fatal("expected an error from a 500 response")
	}
	if !nodes.IsRetryable(err) {
		t.Errorf("GET + 500 should be retryable, got non-retryable error: %v", err)
	}

	post := models.WorkflowNode{ID: "t2", Type: models.NodeTypeTool, Template: "http", URL: srv.URL, Method: "POST"}
	_, err = nodes.ExecuteTool(context.Background(), post, engine.NewRunContext("r1", nil))
	if err == nil {
		t.Fatal("expected an error from a 500 response")
	}
	if nodes.IsRetryable(err) {
		t.Errorf("POST + 500 should NOT be retryable (write may have already applied), got: %v", err)
	}
}

// TestHTTPTool_ClientErrorNeverRetryable verifies a 4xx is never retryable
// regardless of method -- it's a permanent request problem, not a
// transient one, and retrying just repeats the same broken call.
func TestHTTPTool_ClientErrorNeverRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	node := models.WorkflowNode{ID: "t1", Type: models.NodeTypeTool, Template: "http", URL: srv.URL, Method: "GET"}
	_, err := nodes.ExecuteTool(context.Background(), node, engine.NewRunContext("r1", nil))
	if err == nil {
		t.Fatal("expected an error from a 400 response")
	}
	if nodes.IsRetryable(err) {
		t.Errorf("4xx should never be retryable, got: %v", err)
	}
}

// TestHTTPTool_TransportFailureRetryableOnlyForIdempotentMethod mirrors the
// 5xx case above for a connection-level failure (server never responds at
// all) rather than an HTTP status.
func TestHTTPTool_TransportFailureRetryableOnlyForIdempotentMethod(t *testing.T) {
	// Port 0 on a closed listener: httptest server started then closed
	// immediately gives a real "connection refused" address without a
	// network dependency.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := srv.URL
	srv.Close()

	get := models.WorkflowNode{ID: "t1", Type: models.NodeTypeTool, Template: "http", URL: deadURL, Method: "GET"}
	_, err := nodes.ExecuteTool(context.Background(), get, engine.NewRunContext("r1", nil))
	if err == nil {
		t.Fatal("expected a connection error against a closed server")
	}
	if !nodes.IsRetryable(err) {
		t.Errorf("GET transport failure should be retryable, got: %v", err)
	}

	post := models.WorkflowNode{ID: "t2", Type: models.NodeTypeTool, Template: "http", URL: deadURL, Method: "POST"}
	_, err = nodes.ExecuteTool(context.Background(), post, engine.NewRunContext("r1", nil))
	if err == nil {
		t.Fatal("expected a connection error against a closed server")
	}
	if nodes.IsRetryable(err) {
		t.Errorf("POST transport failure should NOT be retryable, got: %v", err)
	}
}
