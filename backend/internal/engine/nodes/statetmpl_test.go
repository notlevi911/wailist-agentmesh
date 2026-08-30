package nodes_test

import (
	"testing"

	"github.com/agentmesh/backend/internal/engine/nodes"
)

func TestExpandStateLeavesEverythingElseAlone(t *testing.T) {
	state := map[string]any{"lastRowId": "row-42"}

	// Every one of these must come back byte-identical. Existing workflows
	// contain braces in prompts, JSON bodies and templates; expansion must
	// never touch anything that is not exactly a state reference.
	untouched := []string{
		"",
		"plain text with no braces",
		`{"a":1,"b":{"c":2}}`,
		"{{ other.thing }}",
		"{{lastRowId}}",
		"{{$json.foo}}",
		"{ {state.lastRowId} }",
		"{{statelastRowId}}",
		"{{state}}",
		"{{state.}}",
		"Use {{ notstate.x }} here",
	}
	for _, in := range untouched {
		if got := nodes.ExpandState(in, state); got != in {
			t.Errorf("ExpandState(%q) = %q, want it unchanged", in, got)
		}
	}

	// And with no state at all, nothing changes either.
	if got := nodes.ExpandState("{{state.lastRowId}}", nil); got != "{{state.lastRowId}}" {
		t.Errorf("with nil state the placeholder must be left intact, got %q", got)
	}
}

func TestExpandStateSubstitutes(t *testing.T) {
	state := map[string]any{
		"lastRowId": "row-42",
		"count":     float64(7),
		"cursor":    map[string]any{"page": float64(3), "token": "abc"},
		"flag":      true,
	}

	cases := []struct{ in, want string }{
		{"{{state.lastRowId}}", "row-42"},
		{"{{ state.lastRowId }}", "row-42"},
		{"resume from {{state.lastRowId}} please", "resume from row-42 please"},
		{"{{state.count}}", "7"},
		{"{{state.flag}}", "true"},
		{"{{state.cursor.page}}", "3"},
		{"{{state.cursor.token}}", "abc"},
		{"{{state.lastRowId}}-{{state.count}}", "row-42-7"},
		// An unknown key expands to empty rather than leaking the
		// placeholder into an outbound request.
		{"[{{state.missing}}]", "[]"},
		{"[{{state.cursor.missing}}]", "[]"},
		// Walking through a non-map is a miss, not a panic.
		{"[{{state.lastRowId.nope}}]", "[]"},
	}
	for _, tc := range cases {
		if got := nodes.ExpandState(tc.in, state); got != tc.want {
			t.Errorf("ExpandState(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A whole object renders as compact JSON, so a body template can inject a
// stored structure.
func TestExpandStateRendersObjectsAsJSON(t *testing.T) {
	state := map[string]any{"cursor": map[string]any{"page": float64(3)}}
	if got := nodes.ExpandState("{{state.cursor}}", state); got != `{"page":3}` {
		t.Fatalf("got %q", got)
	}
}
