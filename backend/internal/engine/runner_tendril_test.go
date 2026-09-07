package engine

import (
	"testing"

	"github.com/agentmesh/backend/internal/models"
)

// A tendril node is a flow step in its own right, not an agent resource: the
// user's Tendril-only workflow is trigger -> rent -> end with no agent at all.
// It must therefore survive topological sort as a normal node.
func TestTendrilNodeIsATopologicalStep(t *testing.T) {
	nodes := []models.WorkflowNode{
		{ID: "n1", Type: models.NodeTypeTrigger},
		{ID: "n2", Type: models.NodeTypeTendril, TendrilAction: "rent"},
		{ID: "n3", Type: models.NodeTypeEnd},
	}
	edges := []models.WorkflowEdge{
		{ID: "e1", From: "n1", To: "n2", Kind: models.EdgeKindFlow},
		{ID: "e2", From: "n2", To: "n3", Kind: models.EdgeKindFlow},
	}
	levels, err := TopologicalSort(nodes, edges)
	if err != nil {
		t.Fatalf("TopologicalSort: %v", err)
	}
	if len(levels) != 3 {
		t.Fatalf("got %d levels, want 3", len(levels))
	}
	if levels[1][0].ID != "n2" {
		t.Errorf("level 1 = %s, want n2", levels[1][0].ID)
	}
}

// Without a configured registry the node must refuse before any money moves,
// rather than nil-panicking inside the executor.
func TestTendrilNodeWithoutConfigErrors(t *testing.T) {
	r := &Runner{}
	_, err := r.executeNode(t.Context(),
		models.WorkflowNode{ID: "n2", Type: models.NodeTypeTendril, TendrilAction: "rent"},
		nil, nil, NewRunContext("run1", nil), models.Run{ID: "run1"}, models.Workflow{ID: "wf1"})
	if err == nil {
		t.Fatal("expected an error when Tendril is not configured")
	}
}
