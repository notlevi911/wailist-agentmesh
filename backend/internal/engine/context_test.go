package engine_test

import (
	"strconv"
	"testing"

	"github.com/agentmesh/backend/internal/engine"
)

// Message() used to pick "most recent output" by iterating rc.outputs, a Go
// map whose iteration order is randomized. Run enough distinct keys that a
// map-order implementation would almost certainly disagree with insertion
// order at least once, to catch a regression back to that approach.
func TestRunContext_MessageReturnsMostRecentlySetInInsertionOrder(t *testing.T) {
	rc := engine.NewRunContext("r1", []byte(`"trigger input"`))
	if got := rc.Message(); got != "trigger input" {
		t.Fatalf("want trigger input before any Set, got %q", got)
	}

	ids := []string{"n1", "n2", "n3", "n4", "n5", "n6", "n7", "n8", "n9", "n10"}
	for i, id := range ids {
		rc.Set(id, i)
		want := strconv.Itoa(i)
		if got := rc.Message(); got != want {
			t.Fatalf("after Set(%s, %d): want Message() == %q, got %q", id, i, want, got)
		}
	}
}

// A re-Set moves a node to the tail of the order rather than leaving it at
// its first-insertion position -- a node that re-emits a new value IS the
// newest output chronologically, so Message() should reflect that value,
// not whatever was inserted after it but never updated again. (This never
// actually fires in the real engine today -- TopologicalSort rejects
// cycles, so no node runs twice in one pass -- but the contract should
// still say what "most recent" means if that ever changes.) See
// context_order_test.go's TestReSetMovesNodeToMostRecent for the ordering
// assertion this pairs with.
func TestRunContext_MessageReflectsOverwriteAsReinsertion(t *testing.T) {
	rc := engine.NewRunContext("r1", nil)
	rc.Set("a", "first")
	rc.Set("b", "second")
	rc.Set("a", "first-updated")
	if got := rc.Message(); got != "first-updated" {
		t.Fatalf("want overwrite to become the most recent output, got %q", got)
	}
}

// LastOutput() exists specifically so a connector's message template can
// pick one field out of a structured output -- unlike Message(), it must
// return the raw value, not a pre-stringified one.
func TestRunContext_LastOutputReturnsRawValueNotStringified(t *testing.T) {
	rc := engine.NewRunContext("r1", nil)
	rc.Set("n1", map[string]any{"extract": "hello", "count": 3})

	got, ok := rc.LastOutput().(map[string]any)
	if !ok {
		t.Fatalf("want map[string]any, got %T: %v", rc.LastOutput(), rc.LastOutput())
	}
	if got["extract"] != "hello" || got["count"] != 3 {
		t.Errorf("want raw map preserved, got %v", got)
	}
}

func TestRunContext_LastOutputBeforeAnySetReturnsInput(t *testing.T) {
	rc := engine.NewRunContext("r1", []byte(`{"city":"NYC"}`))
	got, ok := rc.LastOutput().(map[string]any)
	if !ok || got["city"] != "NYC" {
		t.Fatalf("want raw trigger input map, got %T: %v", rc.LastOutput(), rc.LastOutput())
	}
}
