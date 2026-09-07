package engine_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/sse"
)

// TestStartIfNotRunningRefusesInsteadOfCancelling is a regression test for a
// review finding: the scheduler used to call Start (via s.engine.Start),
// whose registry unconditionally cancels any run already registered for
// the same workflow ID. That's correct for a user deliberately
// re-triggering their own workflow, but wrong for the scheduler's overlap
// guard, which is check-then-act -- a manual trigger or resume for the
// same workflow could register in the window between the scheduler's own
// checks and this call, and Start would silently cancel it, possibly
// mid-payment. StartIfNotRunning must refuse (false) instead, leaving
// whatever's already registered completely untouched.
func TestStartIfNotRunningRefusesInsteadOfCancelling(t *testing.T) {
	runner, store := newTestRunner(t)
	ctx := context.Background()

	release := make(chan struct{})
	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		<-release
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	wf, err := store.CreateWorkflow(ctx, "StartIfNotRunning Test", fundedTestUser(t, store))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })

	graph := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{
			{ID: "n1", Type: models.NodeTypeTrigger},
			{ID: "n2", Type: models.NodeTypeTool, Template: "http", URL: srv.URL, Method: "GET"},
			{ID: "n3", Type: models.NodeTypeEnd},
		},
		Edges: []models.WorkflowEdge{
			{ID: "e1", From: "n1", To: "n2", Kind: models.EdgeKindFlow},
			{ID: "e2", From: "n2", To: "n3", Kind: models.EdgeKindFlow},
		},
	}
	wf, _ = store.UpdateWorkflow(ctx, wf.ID, wf.Name, graph)

	// This "manual trigger" registers first and blocks mid-node.
	run1, err := store.CreateRun(ctx, wf.ID, "manual", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	broker := sse.NewBroker()
	broker.Create(run1.ID)
	runner.Start(wf, run1)

	deadline := time.Now().Add(3 * time.Second)
	for atomic.LoadInt32(&requests) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("server saw %d requests before StartIfNotRunning, want 1 (run1 genuinely in-flight)", got)
	}

	// This "scheduler tick" races in second, for the same workflow.
	run2, err := store.CreateRun(ctx, wf.ID, "schedule", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if runner.StartIfNotRunning(wf, run2) {
		t.Fatal("StartIfNotRunning should refuse (false) when the workflow already has a registered run")
	}

	// run1 must be completely untouched -- no second request, no cancellation.
	time.Sleep(100 * time.Millisecond)
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("server saw %d requests after the refused StartIfNotRunning, want still 1 (run1 must not be cancelled or re-triggered)", got)
	}

	close(release)
	final := waitForRunDone(t, store, run1.ID)
	if final.Status != models.RunStatusSuccess {
		t.Fatalf("run1 status = %s, want success (it was never cancelled)", final.Status)
	}
}
