package engine_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentmesh/backend/internal/db"
	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/sse"
)

// fundedTestUser creates a real user row with a real credit balance --
// a "http" template Tool node is billable (nodes.BillableFlatFee), and
// preflightCheck's GetCreditBalance query fails outright for a user ID with
// no row at all (not just an insufficient one), unlike the calc/end nodes
// the other runner tests use, which skip the billing path entirely.
func fundedTestUser(t *testing.T, store *db.Store) string {
	t.Helper()
	ctx := context.Background()
	email := fmt.Sprintf("retry-test-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(ctx, email, "hash")
	if err != nil {
		t.Fatal(err)
	}
	orderID := fmt.Sprintf("order_retry_test_%d", time.Now().UnixNano())
	// 50000 INR paise * 0.012 fx = $6 USD credit, well above the $0.50
	// ByokFlatFeeUSDMicros fee a single billable Tool node call costs.
	if _, err := store.CreateCreditTransaction(ctx, user.ID, orderID, 50000, 0.012); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CompleteCreditTransaction(ctx, "cashfree", orderID, "pay_retry_test"); err != nil {
		t.Fatal(err)
	}
	return user.ID
}

// TestRetrySucceedsAfterTransientFailures verifies the runner's
// retry-in-place loop: a GET node configured with MaxRetries actually
// re-attempts on a retryable (idempotent-method, 5xx) failure and the run
// completes successfully once the underlying call does, with exactly one
// run_logs row for the node (attempts collapse into the final outcome, not
// one row per attempt).
func TestRetrySucceedsAfterTransientFailures(t *testing.T) {
	runner, store := newTestRunner(t)
	ctx := context.Background()

	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&requests, 1) <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	wf, err := store.CreateWorkflow(ctx, "Retry Success Test", fundedTestUser(t, store))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })

	graph := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{
			{ID: "n1", Type: models.NodeTypeTrigger},
			{ID: "n2", Type: models.NodeTypeTool, Template: "http", URL: srv.URL, Method: "GET", MaxRetries: 2},
			{ID: "n3", Type: models.NodeTypeEnd},
		},
		Edges: []models.WorkflowEdge{
			{ID: "e1", From: "n1", To: "n2", Kind: models.EdgeKindFlow},
			{ID: "e2", From: "n2", To: "n3", Kind: models.EdgeKindFlow},
		},
	}
	wf, _ = store.UpdateWorkflow(ctx, wf.ID, wf.Name, graph)

	run, err := store.CreateRun(ctx, wf.ID, "test", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}

	broker := sse.NewBroker()
	broker.Create(run.ID)

	runner.Run(ctx, wf, run, 0)

	if got := atomic.LoadInt32(&requests); got != 3 {
		t.Fatalf("server saw %d requests, want 3 (2 failed attempts + 1 success)", got)
	}

	finalRun, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finalRun.Status != models.RunStatusSuccess {
		t.Fatalf("run status = %s, want success", finalRun.Status)
	}

	logs, err := store.GetRunLogs(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var n2Count int
	for _, l := range logs {
		if l.NodeID == "n2" {
			n2Count++
			if l.Status != models.LogStatusSuccess {
				t.Errorf("n2 status = %s, want success", l.Status)
			}
		}
	}
	if n2Count != 1 {
		t.Errorf("n2 has %d run_logs rows, want exactly 1", n2Count)
	}
}

// TestRetryNotAttemptedForNonRetryableError verifies MaxRetries has no
// effect on a POST failure: a POST's 500 is not classified retryable
// (nodes.IsRetryable), so the runner must not retry it even though the node
// asked for retries -- retrying could double a write that already landed.
func TestRetryNotAttemptedForNonRetryableError(t *testing.T) {
	runner, store := newTestRunner(t)
	ctx := context.Background()

	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	wf, err := store.CreateWorkflow(ctx, "Retry Non-Retryable Test", fundedTestUser(t, store))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })

	graph := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{
			{ID: "n1", Type: models.NodeTypeTrigger},
			{ID: "n2", Type: models.NodeTypeTool, Template: "http", URL: srv.URL, Method: "POST", MaxRetries: 3},
			{ID: "n3", Type: models.NodeTypeEnd},
		},
		Edges: []models.WorkflowEdge{
			{ID: "e1", From: "n1", To: "n2", Kind: models.EdgeKindFlow},
			{ID: "e2", From: "n2", To: "n3", Kind: models.EdgeKindFlow},
		},
	}
	wf, _ = store.UpdateWorkflow(ctx, wf.ID, wf.Name, graph)

	run, err := store.CreateRun(ctx, wf.ID, "test", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}

	broker := sse.NewBroker()
	broker.Create(run.ID)

	runner.Run(ctx, wf, run, 0)

	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("server saw %d requests, want exactly 1 (POST 500 must not be retried)", got)
	}

	finalRun, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finalRun.Status != models.RunStatusFailed {
		t.Fatalf("run status = %s, want failed", finalRun.Status)
	}
}
