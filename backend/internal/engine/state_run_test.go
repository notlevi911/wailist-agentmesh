package engine_test

import (
	"context"
	"testing"

	"github.com/agentmesh/backend/internal/models"
)

// A set node persists across runs, and a later run reads it back.
func TestStateNodePersistsAcrossRuns(t *testing.T) {
	runner, store := newTestRunner(t)
	ctx := context.Background()

	wf, _ := store.CreateWorkflow(ctx, "StateNode", "dev")
	t.Cleanup(func() { store.DeleteWorkflow(ctx, wf.ID) })

	wf.Nodes = []models.WorkflowNode{
		{ID: "t1", Type: models.NodeTypeTrigger},
		{ID: "s1", Type: models.NodeTypeState, StateOp: "set", StateKey: "lastRowId", StateValue: "row-42"},
	}
	wf.Edges = []models.WorkflowEdge{
		{ID: "x1", From: "t1", To: "s1", Kind: models.EdgeKindFlow},
	}

	run1, _ := store.CreateRun(ctx, wf.ID, "manual", []byte(`{"message":"go"}`))
	runner.Run(ctx, wf, run1)

	vars, err := store.GetWorkflowVariables(ctx, wf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if vars["lastRowId"] != "row-42" {
		t.Fatalf("state was not persisted: %#v", vars)
	}

	// A second run reads it back through a get node.
	wf.Nodes = append(wf.Nodes, models.WorkflowNode{
		ID: "g1", Type: models.NodeTypeState, StateOp: "get", StateKey: "lastRowId",
	})
	wf.Edges = append(wf.Edges, models.WorkflowEdge{
		ID: "x2", From: "s1", To: "g1", Kind: models.EdgeKindFlow,
	})
	run2, _ := store.CreateRun(ctx, wf.ID, "manual", []byte(`{"message":"go"}`))
	runner.Run(ctx, wf, run2)

	logs, _ := store.GetRunLogs(ctx, run2.ID)
	var got any
	for _, l := range logs {
		if l.NodeID == "g1" {
			got = l.Output
		}
	}
	if got != "row-42" {
		t.Fatalf("get node did not read persisted state: %#v", got)
	}
}

func TestStateNodeIncrementsCounter(t *testing.T) {
	runner, store := newTestRunner(t)
	ctx := context.Background()

	wf, _ := store.CreateWorkflow(ctx, "StateCounter", "dev")
	t.Cleanup(func() { store.DeleteWorkflow(ctx, wf.ID) })

	wf.Nodes = []models.WorkflowNode{
		{ID: "t1", Type: models.NodeTypeTrigger},
		{ID: "s1", Type: models.NodeTypeState, StateOp: "increment", StateKey: "runs", StateValue: "1"},
	}
	wf.Edges = []models.WorkflowEdge{
		{ID: "x1", From: "t1", To: "s1", Kind: models.EdgeKindFlow},
	}

	for i := 0; i < 3; i++ {
		run, _ := store.CreateRun(ctx, wf.ID, "manual", []byte(`{"message":"go"}`))
		runner.Run(ctx, wf, run)
	}

	vars, _ := store.GetWorkflowVariables(ctx, wf.ID)
	if vars["runs"] != float64(3) {
		t.Fatalf("counter should be 3 after 3 runs, got %#v", vars["runs"])
	}
}

// A write earlier in a run is visible to a later node in the SAME run,
// without a second round-trip to the database.
func TestStateWriteIsVisibleLaterInTheSameRun(t *testing.T) {
	runner, store := newTestRunner(t)
	ctx := context.Background()

	wf, _ := store.CreateWorkflow(ctx, "StateSameRun", "dev")
	t.Cleanup(func() { store.DeleteWorkflow(ctx, wf.ID) })

	wf.Nodes = []models.WorkflowNode{
		{ID: "t1", Type: models.NodeTypeTrigger},
		{ID: "s1", Type: models.NodeTypeState, StateOp: "set", StateKey: "token", StateValue: "abc"},
		{ID: "g1", Type: models.NodeTypeState, StateOp: "get", StateKey: "token"},
	}
	wf.Edges = []models.WorkflowEdge{
		{ID: "x1", From: "t1", To: "s1", Kind: models.EdgeKindFlow},
		{ID: "x2", From: "s1", To: "g1", Kind: models.EdgeKindFlow},
	}

	run, _ := store.CreateRun(ctx, wf.ID, "manual", []byte(`{"message":"go"}`))
	runner.Run(ctx, wf, run)

	logs, _ := store.GetRunLogs(ctx, run.ID)
	var got any
	for _, l := range logs {
		if l.NodeID == "g1" {
			got = l.Output
		}
	}
	if got != "abc" {
		t.Fatalf("a state write must be visible later in the same run, got %#v", got)
	}
}

// An unknown stateOp fails the node loudly rather than silently doing
// nothing.
func TestStateNodeRejectsUnknownOp(t *testing.T) {
	runner, store := newTestRunner(t)
	ctx := context.Background()

	wf, _ := store.CreateWorkflow(ctx, "StateBadOp", "dev")
	t.Cleanup(func() { store.DeleteWorkflow(ctx, wf.ID) })

	wf.Nodes = []models.WorkflowNode{
		{ID: "t1", Type: models.NodeTypeTrigger},
		{ID: "s1", Type: models.NodeTypeState, StateOp: "frobnicate", StateKey: "k"},
	}
	wf.Edges = []models.WorkflowEdge{
		{ID: "x1", From: "t1", To: "s1", Kind: models.EdgeKindFlow},
	}

	run, _ := store.CreateRun(ctx, wf.ID, "manual", []byte(`{"message":"go"}`))
	runner.Run(ctx, wf, run)

	finished, _ := store.GetRun(ctx, run.ID)
	if finished.Status != models.RunStatusFailed {
		t.Fatalf("an unknown stateOp must fail the run, got %s", finished.Status)
	}
}
