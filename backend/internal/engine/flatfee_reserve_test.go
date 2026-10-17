package engine_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/sse"
)

// TestSiblingFlatFeeNodesCannotBothRunOnOneFee is the regression test for
// #31: two billable standalone nodes in the same topology level used to each
// pass a read-only balance check, both fire their real request, and only
// then race to debit -- so a balance covering one fee paid for two calls.
// The fee is now reserved atomically before the request goes out, so the
// sibling that loses the reservation never reaches the network at all.
//
// The server holds every request open long enough that, without an up-front
// reservation, both siblings are guaranteed to be past their balance check
// before either one could debit.
func TestSiblingFlatFeeNodesCannotBothRunOnOneFee(t *testing.T) {
	runner, store := newTestRunner(t)
	ctx := context.Background()

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		time.Sleep(200 * time.Millisecond)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	email := fmt.Sprintf("sibling-flatfee-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(ctx, email, "hash")
	if err != nil {
		t.Fatal(err)
	}
	// Exactly one flat fee: enough for one sibling, never both.
	fundUser(t, store, user.ID, models.ByokFlatFeeUSDMicros)

	wf, err := store.CreateWorkflow(ctx, "Sibling Flat Fee Race Test", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })

	// t1 and t2 hang off the trigger with no edge between them, so they share
	// a topology level and Run executes them concurrently. POST keeps the
	// runner from retrying the node that loses the race.
	graph := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{
			{ID: "n1", Type: models.NodeTypeTrigger},
			{ID: "t1", Type: models.NodeTypeTool, Template: "http", URL: srv.URL, Method: "POST"},
			{ID: "t2", Type: models.NodeTypeTool, Template: "http", URL: srv.URL, Method: "POST"},
			{ID: "n3", Type: models.NodeTypeEnd},
		},
		Edges: []models.WorkflowEdge{
			{ID: "e1", From: "n1", To: "t1", Kind: models.EdgeKindFlow},
			{ID: "e2", From: "n1", To: "t2", Kind: models.EdgeKindFlow},
			{ID: "e3", From: "t1", To: "n3", Kind: models.EdgeKindFlow},
			{ID: "e4", From: "t2", To: "n3", Kind: models.EdgeKindFlow},
		},
	}
	wf, _ = store.UpdateWorkflow(ctx, wf.ID, wf.Name, graph)

	run, err := store.CreateRun(ctx, wf.ID, "test", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	broker := sse.NewBroker()
	broker.Create(run.ID)

	runner.Start(wf, run)
	final := waitForRunDone(t, store, run.ID)
	if final.Status != models.RunStatusFailed {
		t.Fatalf("want failed (one sibling cannot be paid for), got %s", final.Status)
	}

	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("want exactly 1 request to reach the server on a one-fee balance, got %d", got)
	}

	balance, err := store.GetCreditBalance(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if balance != 0 {
		t.Fatalf("want balance 0 (exactly one fee charged), got %d", balance)
	}

	entries, err := store.ListDebitLedger(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Kind != models.DebitKindByokFlatFee {
		t.Fatalf("want exactly 1 byok_flat_fee ledger entry, got %+v", entries)
	}
}

// TestFailedFlatFeeNodeReleasesReservation covers the other half of reserving
// up front: a billable node whose call fails must hand the reserved fee back,
// leaving the balance exactly where it started and writing no ledger row --
// the same "failed work is never billed" rule the old debit-after path had.
func TestFailedFlatFeeNodeReleasesReservation(t *testing.T) {
	runner, store := newTestRunner(t)
	ctx := context.Background()

	// A server that is already closed: every request to it fails to connect.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := srv.URL
	srv.Close()

	email := fmt.Sprintf("failed-flatfee-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(ctx, email, "hash")
	if err != nil {
		t.Fatal(err)
	}
	fundUser(t, store, user.ID, models.ByokFlatFeeUSDMicros)

	wf, err := store.CreateWorkflow(ctx, "Failed Flat Fee Release Test", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })

	graph := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{
			{ID: "n1", Type: models.NodeTypeTrigger},
			{ID: "t1", Type: models.NodeTypeTool, Template: "http", URL: deadURL, Method: "POST"},
			{ID: "n3", Type: models.NodeTypeEnd},
		},
		Edges: []models.WorkflowEdge{
			{ID: "e1", From: "n1", To: "t1", Kind: models.EdgeKindFlow},
			{ID: "e2", From: "t1", To: "n3", Kind: models.EdgeKindFlow},
		},
	}
	wf, _ = store.UpdateWorkflow(ctx, wf.ID, wf.Name, graph)

	run, err := store.CreateRun(ctx, wf.ID, "test", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	broker := sse.NewBroker()
	broker.Create(run.ID)

	runner.Start(wf, run)
	final := waitForRunDone(t, store, run.ID)
	if final.Status != models.RunStatusFailed {
		t.Fatalf("want failed got %s", final.Status)
	}

	balance, err := store.GetCreditBalance(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if balance != models.ByokFlatFeeUSDMicros {
		t.Fatalf("want balance restored to %d after the failed call, got %d", models.ByokFlatFeeUSDMicros, balance)
	}

	entries, err := store.ListDebitLedger(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("want 0 ledger entries for a failed call, got %+v", entries)
	}
}
