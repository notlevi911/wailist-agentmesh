package engine_test

import (
	"testing"

	"github.com/agentmesh/backend/internal/engine"
)

func TestMessageReturnsMostRecentOutputDeterministically(t *testing.T) {
	// Repeat: a single pass can pass by luck under randomized map iteration.
	for i := 0; i < 200; i++ {
		rc := engine.NewRunContext("r1", []byte(`"trigger input"`))
		rc.Set("n1", "first")
		rc.Set("n2", "second")
		rc.Set("n3", "third")
		if got := rc.Message(); got != "third" {
			t.Fatalf("iteration %d: want most recent output %q, got %q", i, "third", got)
		}
	}
}

func TestMessageFallsBackToTriggerInputWhenNoOutputs(t *testing.T) {
	rc := engine.NewRunContext("r1", []byte(`"trigger input"`))
	if got := rc.Message(); got != "trigger input" {
		t.Errorf("want trigger input, got %q", got)
	}
}

// Re-Setting an existing node must not duplicate it in the order, and must
// move it to the end — a node that re-emits is the newest output.
func TestReSetMovesNodeToMostRecent(t *testing.T) {
	rc := engine.NewRunContext("r1", nil)
	rc.Set("n1", "a")
	rc.Set("n2", "b")
	rc.Set("n1", "c")
	if got := rc.Message(); got != "c" {
		t.Errorf("want %q after re-set, got %q", "c", got)
	}
	order := rc.OutputOrder()
	if len(order) != 2 {
		t.Fatalf("want 2 entries in order, got %d: %v", len(order), order)
	}
	if order[len(order)-1] != "n1" {
		t.Errorf("want n1 last after re-set, got %v", order)
	}
}

// The single-upstream shape every x402 and Tendril node actually runs in:
// one producer feeding the payment node. Message() must return that producer's
// output byte-for-byte, because it becomes the paid request body
// (nodes/tool402.go: payBody) / the metered Python source (nodes/tendril.go).
func TestMessageIsUnchangedForSingleUpstreamPaymentNodes(t *testing.T) {
	rc := engine.NewRunContext("r1", []byte(`"ignored trigger"`))
	rc.Set("agent1", "print(6*7)")
	if got := rc.Message(); got != "print(6*7)" {
		t.Errorf("x402/Tendril payload changed: want %q, got %q", "print(6*7)", got)
	}
}

// Structured (non-string) outputs still flatten via anyToString's json.Marshal
// fallback, same as before — tool402 sends this as the request body.
func TestMessageFlattensStructuredOutputAsBefore(t *testing.T) {
	rc := engine.NewRunContext("r1", nil)
	rc.Set("n1", map[string]any{"query": "weather in Kolkata"})
	if got := rc.Message(); got != `{"query":"weather in Kolkata"}` {
		t.Errorf("structured flattening changed: got %q", got)
	}
}
