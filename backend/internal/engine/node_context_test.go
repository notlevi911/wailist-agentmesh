package engine

import (
	"reflect"
	"testing"

	"github.com/agentmesh/backend/internal/models"
)

// A node's message is its predecessor's output, even when an unrelated node
// on another branch Set its output later (#68).
func TestNodeRunContextPrefersPredecessorOverLaterUnrelatedSet(t *testing.T) {
	rc := NewRunContext("r1", []byte(`"trigger input"`))
	rc.Set("left", "from left")
	rc.Set("right", "from right")

	view := rc.forNode([]string{"left"})
	if got := view.Message(); got != "from left" {
		t.Fatalf("view.Message() = %q, want the predecessor's output", got)
	}
	if got := view.LastOutput(); got != "from left" {
		t.Fatalf("view.LastOutput() = %v, want the predecessor's output", got)
	}
	// The run-wide rule itself is unchanged.
	if got := rc.Message(); got != "from right" {
		t.Fatalf("rc.Message() = %q, want the most recent Set", got)
	}
}

// Precedence, not Set order, decides between predecessors that both have
// output -- so concurrent predecessors give the same answer every run.
func TestNodeRunContextIgnoresSetOrderBetweenPredecessors(t *testing.T) {
	for _, order := range [][]string{{"a", "b"}, {"b", "a"}} {
		rc := NewRunContext("r1", nil)
		for _, id := range order {
			rc.Set(id, "from "+id)
		}
		if got := rc.forNode([]string{"a", "b"}).Message(); got != "from a" {
			t.Fatalf("Set order %v: Message() = %q, want %q", order, got, "from a")
		}
	}
}

// A predecessor with no output yet is skipped in favour of the next one.
func TestNodeRunContextSkipsPredecessorWithoutOutput(t *testing.T) {
	rc := NewRunContext("r1", nil)
	rc.Set("b", map[string]any{"message": "from b"})
	if got := rc.forNode([]string{"a", "b"}).Message(); got != "from b" {
		t.Fatalf("Message() = %q, want %q", got, "from b")
	}
}

// With no predecessor output at all, the view behaves exactly like the run
// context: the most recent output, or the trigger input when nothing ran.
func TestNodeRunContextFallsBackToRunWideRule(t *testing.T) {
	rc := NewRunContext("r1", []byte(`"trigger input"`))
	view := rc.forNode(nil)
	if got := view.Message(); got != "trigger input" {
		t.Fatalf("no outputs: Message() = %q, want the trigger input", got)
	}

	rc.Set("x", "from x")
	if got := rc.forNode([]string{"missing"}).Message(); got != "from x" {
		t.Fatalf("no predecessor output: Message() = %q, want the most recent output", got)
	}
}

// Writes through the view land on the shared run context.
func TestNodeRunContextSetWritesThrough(t *testing.T) {
	rc := NewRunContext("r1", nil)
	rc.forNode([]string{"a"}).Set("n1", 42)
	if v, ok := rc.Get("n1"); !ok || v != 42 {
		t.Fatalf("rc.Get(n1) = %v, %v; want 42, true", v, ok)
	}
}

func TestMessagePredecessorsOrder(t *testing.T) {
	nodes := []models.WorkflowNode{
		{ID: "t", Type: models.NodeTypeTrigger},
		{ID: "a"}, {ID: "b"}, {ID: "m"}, {ID: "sink"}, {ID: "tool"},
	}
	edges := []models.WorkflowEdge{
		{ID: "e1", From: "t", To: "a", Kind: models.EdgeKindFlow},
		{ID: "e2", From: "t", To: "b", Kind: models.EdgeKindFlow},
		{ID: "e3", From: "a", To: "m", Kind: models.EdgeKindFlow},
		{ID: "e4", From: "b", To: "sink", Kind: models.EdgeKindFlow},
		{ID: "e5", From: "m", To: "sink", Kind: models.EdgeKindFlow},
		{ID: "e6", From: "a", To: "sink", Kind: models.EdgeKindFlow},
		{ID: "e7", From: "b", To: "sink", Kind: models.EdgeKindFlow},      // duplicate of e4
		{ID: "e8", From: "tool", To: "sink", Kind: models.EdgeKindAttach}, // not a flow edge
	}
	levels, err := TopologicalSort(nodes, edges)
	if err != nil {
		t.Fatal(err)
	}

	preds := messagePredecessors(levels, edges)

	// m is one level deeper than a and b, so it comes first. a and b share a
	// level; b's latest edge (e7) is later than a's (e6), so b leads.
	if got, want := preds["sink"], []string{"m", "b", "a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("preds[sink] = %v, want %v", got, want)
	}
	if got, want := preds["m"], []string{"a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("preds[m] = %v, want %v", got, want)
	}
	if got := preds["t"]; len(got) != 0 {
		t.Fatalf("preds[t] = %v, want none", got)
	}
}
