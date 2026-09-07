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

// TestResumeSkipsAlreadySucceededNode verifies the core resume contract: a
// node already logged as success is never re-executed. n2's real output
// from a fresh "1+1" calc would be 2 -- seeding a deliberately different
// value (9999) and confirming it survives to n3 (End, which reports the
// most recently set output) proves n2 was skipped rather than recomputed.
// This is the guarantee retry/dead-letter (#87) builds on: a node that
// already paid or debited must not run twice on resume.
func TestResumeSkipsAlreadySucceededNode(t *testing.T) {
	runner, store := newTestRunner(t)
	ctx := context.Background()

	wf, err := store.CreateWorkflow(ctx, "Resume Skip Test", "test-user")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })

	graph := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{
			{ID: "n1", Type: models.NodeTypeTrigger},
			{ID: "n2", Type: models.NodeTypeTool, Template: "calc", URL: "1+1"},
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

	// Seed n2 as already-succeeded from a prior attempt, with an output a
	// real "1+1" calc would never produce -- if Resume re-executes it, the
	// seeded 9999 gets overwritten with 2 and the assertions below fail.
	seedEntry, err := store.InsertRunLog(ctx, models.RunLog{
		RunID:    run.ID,
		NodeID:   "n2",
		NodeType: models.NodeTypeTool,
		Status:   models.LogStatusRunning,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateRunLog(ctx, seedEntry.ID, models.LogStatusSuccess, []byte("9999"), 5, ""); err != nil {
		t.Fatal(err)
	}

	// A real dead-lettered run has already transitioned to "failed" by the
	// time anything calls Resume -- CreateRun's own initial status is
	// "running". Mirror that realistic precondition here rather than
	// relying on the pre-transition default (Resume itself no longer gates
	// on this -- StartResume's MarkRunRunning claim does, one layer up, and
	// this test calls Resume directly).
	if err := store.FinishRun(ctx, run.ID, models.RunStatusFailed); err != nil {
		t.Fatal(err)
	}

	broker := sse.NewBroker()
	broker.Create(run.ID)

	runner.Resume(ctx, wf, run, 0, false)

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
	var n3Output any
	for _, l := range logs {
		if l.NodeID == "n2" {
			n2Count++
			if l.Output != float64(9999) {
				t.Errorf("n2 output = %v, want the seeded 9999 (node was re-executed)", l.Output)
			}
		}
		if l.NodeID == "n3" {
			n3Output = l.Output
		}
	}
	if n2Count != 1 {
		t.Fatalf("n2 has %d run_logs rows, want exactly 1 (the seed) -- Resume executed it again", n2Count)
	}
	// End's result is rc.Message(), which stringifies the most recent
	// output -- "9999" here, not the numeric 9999 n2's own row carries.
	if n3Output != "9999" {
		t.Errorf("n3 (End) output = %v, want \"9999\" propagated from the skipped n2", n3Output)
	}
}

// TestGetLatestNodeStatesReturnsMostRecentStatus verifies the resume query
// picks the latest logged row per node, not an arbitrary one -- a node can
// have multiple run_logs rows (e.g. a crashed attempt followed by a manual
// retry), and Resume must only treat it as done if the LATEST row says so.
func TestGetLatestNodeStatesReturnsMostRecentStatus(t *testing.T) {
	_, store := newTestRunner(t)
	ctx := context.Background()

	wf, err := store.CreateWorkflow(ctx, "GetLatestNodeStates Test", "test-user")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })

	graph := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{{ID: "n1", Type: models.NodeTypeTrigger}},
	}
	wf, _ = store.UpdateWorkflow(ctx, wf.ID, wf.Name, graph)

	run, err := store.CreateRun(ctx, wf.ID, "test", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}

	// First attempt: failed.
	first, err := store.InsertRunLog(ctx, models.RunLog{RunID: run.ID, NodeID: "n1", NodeType: models.NodeTypeTrigger, Status: models.LogStatusRunning})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateRunLog(ctx, first.ID, models.LogStatusFailed, []byte(`"boom"`), 1, ""); err != nil {
		t.Fatal(err)
	}

	// Second attempt: succeeded. This is the row GetLatestNodeStates must
	// return, even though the failed row for the same node is older.
	second, err := store.InsertRunLog(ctx, models.RunLog{RunID: run.ID, NodeID: "n1", NodeType: models.NodeTypeTrigger, Status: models.LogStatusRunning})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateRunLog(ctx, second.ID, models.LogStatusSuccess, []byte(`"ok"`), 1, ""); err != nil {
		t.Fatal(err)
	}

	states, err := store.GetLatestNodeStates(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := states["n1"]
	if !ok {
		t.Fatal("n1 missing from GetLatestNodeStates result")
	}
	if got.Status != models.LogStatusSuccess {
		t.Errorf("n1 status = %s, want success (the latest row, not the older failed one)", got.Status)
	}
	if got.Output != "ok" {
		t.Errorf("n1 output = %v, want \"ok\"", got.Output)
	}
}

// TestConcurrentStartResumeNeverCancelsTheWinner covers a real reviewer
// finding: registry.register() unconditionally cancels whatever run is
// currently registered for a workflow ID. If a losing concurrent
// StartResume call reached registry.register() before finding out it lost
// the claim, it would cancel the winner's context mid-flight -- possibly
// mid-payment. StartResume now does the atomic claim (MarkRunRunning)
// BEFORE registry.register(), specifically so a losing call never reaches
// the registry at all. This proves it end to end: the first call's http
// node is genuinely blocked in flight when the second call is made, and
// the first must still reach success afterward -- if the second call had
// cancelled it, the blocked node's own ctx.Done() would fire and the run
// would end up "stopped", not "success".
func TestConcurrentStartResumeNeverCancelsTheWinner(t *testing.T) {
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

	wf, err := store.CreateWorkflow(ctx, "Concurrent Resume Test", fundedTestUser(t, store))
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

	run, err := store.CreateRun(ctx, wf.ID, "test", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishRun(ctx, run.ID, models.RunStatusFailed); err != nil {
		t.Fatal(err)
	}

	claimed1, err := runner.StartResume(ctx, wf, run, false)
	if err != nil {
		t.Fatal(err)
	}
	if !claimed1 {
		t.Fatal("first StartResume call: claimed = false, want true")
	}

	// Wait for the first resume's http node to genuinely be in flight
	// before racing the second call against it.
	deadline := time.Now().Add(3 * time.Second)
	for atomic.LoadInt32(&requests) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("server saw %d requests before the second StartResume call, want 1", got)
	}

	claimed2, err := runner.StartResume(ctx, wf, run, false)
	if err != nil {
		t.Fatal(err)
	}
	if claimed2 {
		t.Fatal("second concurrent StartResume call: claimed = true, want false (run is already claimed)")
	}

	// Give a cancelled context time to actually propagate before checking --
	// if the second call HAD cancelled the first (the bug this test
	// guards against), the blocked node would see ctx.Done() right about now.
	time.Sleep(100 * time.Millisecond)
	close(release)

	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		finalRun, err := store.GetRun(ctx, run.ID)
		if err == nil && finalRun.Status == models.RunStatusSuccess {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	finalRun, _ := store.GetRun(ctx, run.ID)
	t.Fatalf("run status = %s, want success -- the losing StartResume call may have cancelled the winner", finalRun.Status)
}
