package nodes_test

import (
	"encoding/json"
	"testing"

	"github.com/agentmesh/backend/internal/engine"
	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
)

func TestResolveTemplate(t *testing.T) {
	rc := engine.NewRunContext("r1", []byte(`"original question"`))
	rc.Set("n1", "agent answer")
	rc.Set("n3", map[string]any{"items": []any{
		map[string]any{"title": "first"},
		map[string]any{"title": "second"},
	}})
	// n2 last: {{ result }} below must keep resolving to n2's output, not n3's.
	rc.Set("n2", map[string]any{"city": "Kolkata", "temp": 31.5})

	cases := []struct{ name, in, want string }{
		{"result", "Answer: {{ result }}", `Answer: {"city":"Kolkata","temp":31.5}`},
		{"no spaces", "{{result}}", `{"city":"Kolkata","temp":31.5}`},
		{"input", "Q: {{ input }}", "Q: original question"},
		{"node by id", "got {{ node.n1 }}", "got agent answer"},
		{"node field", "in {{ node.n2.city }}", "in Kolkata"},
		{"numeric field", "temp {{ node.n2.temp }}", "temp 31.5"},
		{"array index field", "t: {{ node.n3.items.0.title }}", "t: first"},
		{"second array index", "t: {{ node.n3.items.1.title }}", "t: second"},
		{"array index out of range left verbatim", "x {{ node.n3.items.5.title }} y", "x {{ node.n3.items.5.title }} y"},
		{"two refs", "{{ node.n1 }} / {{ input }}", "agent answer / original question"},
		{"unknown node left verbatim", "x {{ node.nope }} y", "x {{ node.nope }} y"},
		{"unknown field left verbatim", "x {{ node.n2.nope }} y", "x {{ node.n2.nope }} y"},
		{"field on a string output left verbatim", "x {{ node.n1.city }} y", "x {{ node.n1.city }} y"},
		{"unknown keyword left verbatim", "x {{ bogus }} y", "x {{ bogus }} y"},
		{"no refs untouched", "plain text", "plain text"},
		{"empty string", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := nodes.ResolveTemplateForTest(tc.in, rc); got != tc.want {
				t.Errorf("want %q, got %q", tc.want, got)
			}
		})
	}
}

// TestResolveTemplateWalksConcreteSliceTypesFromConnectors is the
// template-resolver half of the same guard
// compute_test.go's TestJSONExtractWalksConcreteSliceTypesFromConnectors
// covers for json_extract: both go through walkPath, and a connector's
// concrete []map[string]any (what fetchRSS/fetchHackerNews actually
// return) is not matched by a `case []any` type switch. Here the failure
// was quieter than json_extract's -- lookupRef blanks a "result." miss
// rather than erroring, so an RSS-fed {{ result.items.0.title }} silently
// rendered as an empty Slack message / email body / SMS with no trace.
func TestResolveTemplateWalksConcreteSliceTypesFromConnectors(t *testing.T) {
	rc := engine.NewRunContext("r1", nil)
	// Exactly the shape fetchRSS/fetchHackerNews return.
	rc.Set("feed", map[string]any{
		"count": 1,
		"items": []map[string]any{{"title": "headline", "link": "https://example.test"}},
	})

	cases := []struct{ name, in, want string }{
		{"node ref into concrete slice", "t: {{ node.feed.items.0.title }}", "t: headline"},
		{"result ref into concrete slice", "t: {{ result.items.0.title }}", "t: headline"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := nodes.ResolveTemplateForTest(tc.in, rc); got != tc.want {
				t.Errorf("want %q, got %q", tc.want, got)
			}
		})
	}
}

// TestResolveTemplateTreatsByteSlicesAsScalars pins the one behavior the
// move from `case []any` to a reflect-based slice case could otherwise
// have widened by accident: a []byte (or json.RawMessage, or any named
// byte-slice type) is JSON-shaped as a string, not an indexable array of
// numbers. Indexing one would interpolate a byte's numeric value -- 'h'
// as "104" -- into a Slack message or SMS, which is worse than the ref
// simply not resolving.
func TestResolveTemplateTreatsByteSlicesAsScalars(t *testing.T) {
	rc := engine.NewRunContext("r1", nil)
	rc.Set("raw", map[string]any{
		"bytes": []byte("hello"),
		"msg":   json.RawMessage(`{"a":1}`),
	})

	cases := []struct{ name, in, want string }{
		{"byte slice index left verbatim", "x {{ node.raw.bytes.0 }} y", "x {{ node.raw.bytes.0 }} y"},
		{"json.RawMessage key left verbatim", "x {{ node.raw.msg.a }} y", "x {{ node.raw.msg.a }} y"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := nodes.ResolveTemplateForTest(tc.in, rc); got != tc.want {
				t.Errorf("want %q, got %q", tc.want, got)
			}
		})
	}
}

// The email body's {{ result }} contract predates this resolver and must keep
// working exactly as before.
func TestEmailBodyStillResolvesResult(t *testing.T) {
	rc := engine.NewRunContext("r1", nil)
	rc.Set("n1", "the answer")
	node := models.WorkflowNode{
		ID: "e1", Type: models.NodeTypeAction, Template: "email",
		EmailBody: "Result was: {{ result }}",
	}
	// No API key -> skipped before any network call; we are asserting the
	// template contract compiles and the skip sentinel is unchanged.
	got, err := nodes.ExecuteAction(t.Context(), node, rc)
	if got != "email_skipped_no_api_key" {
		t.Errorf("want skip sentinel, got %v (err %v)", got, err)
	}
}
