package engine_test

import (
	"testing"

	"github.com/agentmesh/backend/internal/engine"
	"github.com/agentmesh/backend/internal/models"
)

func TestTopologicalSort(t *testing.T) {
	nodes := []models.WorkflowNode{
		{ID: "n4", Type: models.NodeTypeEnd},
		{ID: "n3", Type: models.NodeTypeAction},
		{ID: "n1", Type: models.NodeTypeTrigger},
		{ID: "n2", Type: models.NodeTypeAgent},
	}
	edges := []models.WorkflowEdge{
		{ID: "e1", From: "n1", To: "n2", Kind: models.EdgeKindFlow},
		{ID: "e2", From: "n2", To: "n3", Kind: models.EdgeKindFlow},
		{ID: "e3", From: "n3", To: "n4", Kind: models.EdgeKindFlow},
		{ID: "e4", From: "p1", To: "n2", Kind: models.EdgeKindAttach, ToPort: "model"},
	}

	levels, err := engine.TopologicalSort(nodes, edges)
	if err != nil {
		t.Fatal(err)
	}
	if len(levels) != 4 {
		t.Fatalf("want 4 levels got %d", len(levels))
	}
	if levels[0][0].ID != "n1" {
		t.Fatalf("first node should be trigger, got %s", levels[0][0].ID)
	}
	if levels[3][0].ID != "n4" {
		t.Fatalf("last node should be end, got %s", levels[3][0].ID)
	}
}

// An unterminated "{{ node.b" (a typo, or stray example text) must not add
// an implicit dependency edge -- nodeRefPattern requires the closing "}}",
// same as the resolver that actually treats these as live references.
// Node "a" has a real flow edge into "b"; if the broken text in "a"'s own
// Description were still matched, it would add a spurious b -> a edge and
// turn this into a cycle.
func TestTopologicalSort_UnterminatedNodeRefAddsNoImplicitEdge(t *testing.T) {
	nodes := []models.WorkflowNode{
		{ID: "a", Type: models.NodeTypeAction, Description: "see {{ node.b for details"},
		{ID: "b", Type: models.NodeTypeAction},
	}
	edges := []models.WorkflowEdge{
		{ID: "e1", From: "a", To: "b", Kind: models.EdgeKindFlow},
	}

	levels, err := engine.TopologicalSort(nodes, edges)
	if err != nil {
		t.Fatalf("unterminated ref should not create a cycle: %v", err)
	}
	if len(levels) != 2 || levels[0][0].ID != "a" || levels[1][0].ID != "b" {
		t.Fatalf("want [a] [b], got %v", levels)
	}
}

func TestCycleDetected(t *testing.T) {
	nodes := []models.WorkflowNode{{ID: "a"}, {ID: "b"}}
	edges := []models.WorkflowEdge{
		{ID: "e1", From: "a", To: "b", Kind: models.EdgeKindFlow},
		{ID: "e2", From: "b", To: "a", Kind: models.EdgeKindFlow},
	}
	_, err := engine.TopologicalSort(nodes, edges)
	if err == nil {
		t.Fatal("expected cycle error")
	}
}

func TestBuildAttachMap(t *testing.T) {
	nodes := []models.WorkflowNode{
		{ID: "provider1", Type: models.NodeTypeProvider, Template: "openai"},
		{ID: "tool1", Type: models.NodeTypeTool, Template: "http"},
		{ID: "agent1", Type: models.NodeTypeAgent},
	}
	edges := []models.WorkflowEdge{
		{ID: "e1", From: "provider1", To: "agent1", Kind: models.EdgeKindAttach, ToPort: "model"},
		{ID: "e2", From: "tool1", To: "agent1", Kind: models.EdgeKindAttach, ToPort: "tools"},
	}
	m := engine.BuildAttachMap(nodes, edges)
	cfg, ok := m["agent1"]
	if !ok {
		t.Fatal("no attach config for agent1")
	}
	if cfg.Provider == nil || cfg.Provider.ID != "provider1" {
		t.Fatal("provider not attached")
	}
	if len(cfg.Tools) != 1 || cfg.Tools[0].ID != "tool1" {
		t.Fatal("tools not attached")
	}
}

// A Google node was never made agent-attachable (the frontend blocks
// wiring it to a "tools" port), but nothing on the backend enforced that --
// UpdateWorkflow persists edges straight from request JSON, so a
// hand-crafted PUT (or a future UI regression) could still produce this
// edge. Before the fix, BuildAttachMap accepted any node.Type into
// cfg.Tools unconditionally, and executeFunctionCall's dispatch (only
// tool402 is special-cased; everything else falls through to ExecuteTool)
// would then silently no-op on a Google template while still billing the
// call as a successful tool result. This pins that such a node never
// enters cfg.Tools in the first place, regardless of what reaches
// executeFunctionCall downstream.
func TestBuildAttachMap_IgnoresNonDispatchableToolType(t *testing.T) {
	nodes := []models.WorkflowNode{
		{ID: "google1", Type: models.NodeTypeGoogle, Template: "gmail_send"},
		{ID: "agent1", Type: models.NodeTypeAgent},
	}
	edges := []models.WorkflowEdge{
		{ID: "e1", From: "google1", To: "agent1", Kind: models.EdgeKindAttach, ToPort: "tools"},
	}
	m := engine.BuildAttachMap(nodes, edges)
	cfg := m["agent1"]
	if len(cfg.Tools) != 0 {
		t.Fatalf("want a Google node never entering cfg.Tools, got %v", cfg.Tools)
	}
}

// Same class of guard on the "model" port: only a real Provider node should
// ever populate cfg.Provider.
func TestBuildAttachMap_IgnoresNonProviderOnModelPort(t *testing.T) {
	nodes := []models.WorkflowNode{
		{ID: "action1", Type: models.NodeTypeAction, Template: "slack"},
		{ID: "agent1", Type: models.NodeTypeAgent},
	}
	edges := []models.WorkflowEdge{
		{ID: "e1", From: "action1", To: "agent1", Kind: models.EdgeKindAttach, ToPort: "model"},
	}
	m := engine.BuildAttachMap(nodes, edges)
	cfg := m["agent1"]
	if cfg.Provider != nil {
		t.Fatalf("want a non-Provider node never populating cfg.Provider, got %v", cfg.Provider)
	}
}
