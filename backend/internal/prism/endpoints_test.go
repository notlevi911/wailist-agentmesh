package prism

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestEndpointsMatchProbedQuotes pins every endpoint's price to the value read
// off its live 402 challenge on 2026-09-05. This is deliberately a hardcoded
// duplicate of the table rather than a loop over it: the point is that an
// edit to endpoints.go has to be made twice, in two places, by someone who
// looked at both. A wrong amount here overcharges or undercharges a real
// user; a wrong PayTo pays a stranger.
func TestEndpointsMatchProbedQuotes(t *testing.T) {
	want := map[string]int64{
		"code-review-fast":       100_000,
		"code-review-accurate":   200_000,
		"resume-screen-fast":     100_000,
		"resume-screen-accurate": 250_000,
	}
	got := Endpoints()
	if len(got) != len(want) {
		t.Fatalf("want %d endpoints, got %d", len(want), len(got))
	}
	for _, e := range got {
		amount, ok := want[e.ID]
		if !ok {
			t.Errorf("unexpected endpoint %q — add it to this test's table with a freshly probed amount", e.ID)
			continue
		}
		if e.AmountMicros != amount {
			t.Errorf("%s: amount = %d, want %d (re-probe the live challenge before changing this)", e.ID, e.AmountMicros, amount)
		}
		delete(want, e.ID)
	}
	for id := range want {
		t.Errorf("endpoint %q is missing from Endpoints()", id)
	}
}

// TestPayToIsTheOneProbedAddress guards the single most dangerous constant in
// the package. All four challenges declared the same settlement address.
func TestPayToIsTheOneProbedAddress(t *testing.T) {
	const probed = "FL7U7GHUZB2R6RACPGY5UFD2K47CP2IL4RQWX7LKYE5QSFGXVJCDGPRLBE"
	if PayTo != probed {
		t.Fatalf("PayTo = %q, want the address probed 2026-09-05 (%q). If PRISM really did rotate its address, re-probe all four endpoints and update both.", PayTo, probed)
	}
	if AssetID != "31566704" {
		t.Errorf("AssetID = %q, want mainnet USDC 31566704", AssetID)
	}
	if !strings.HasPrefix(Network, "algorand:") {
		t.Errorf("Network = %q, want a CAIP-2 algorand id", Network)
	}
}

func TestEndpointsAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	tasks := map[string]bool{}
	for _, task := range Tasks() {
		tasks[task.Key] = true
	}

	for _, e := range Endpoints() {
		if seen[e.ID] {
			t.Errorf("duplicate endpoint id %q", e.ID)
		}
		seen[e.ID] = true

		if !strings.HasPrefix(e.URL(), "https://"+Host+"/") {
			t.Errorf("%s: URL %q is not on PRISM's host", e.ID, e.URL())
		}
		if e.Method != http.MethodGet && e.Method != http.MethodPost {
			t.Errorf("%s: method %q — PRISM documents GET and POST only", e.ID, e.Method)
		}
		if e.AmountMicros <= 0 {
			t.Errorf("%s: amount must be positive, got %d", e.ID, e.AmountMicros)
		}
		if !tasks[e.Task] {
			t.Errorf("%s: task %q is not in Tasks()", e.ID, e.Task)
		}
		if e.Tier != TierFast && e.Tier != TierAccurate {
			t.Errorf("%s: tier %q", e.ID, e.Tier)
		}
		if len(e.Fields) == 0 {
			t.Errorf("%s: no fields — every PRISM endpoint takes input", e.ID)
		}
		switch e.Verified {
		case VerifiedLive, VerifiedDocumented, VerifiedSibling:
		default:
			t.Errorf("%s: unknown Verified state %q", e.ID, e.Verified)
		}

		for _, f := range e.Fields {
			if f.Name == "" || f.Label == "" {
				t.Errorf("%s: field %+v needs a name and a label", e.ID, f)
			}
			switch f.Kind {
			case FieldText, FieldTextarea, FieldFile:
			default:
				t.Errorf("%s/%s: unknown field kind %q", e.ID, f.Name, f.Kind)
			}
		}
	}
}

// TestSiblingEndpointsReallyShareATemplate backs the claim VerifiedSibling
// makes. That state exists to say "this sends the same request as a sibling we
// have confirmed", and the console shows a softer note because of it — so if a
// sibling's template ever diverges, the reassurance becomes false and the
// state has to be downgraded rather than quietly left in place.
func TestSiblingEndpointsReallyShareATemplate(t *testing.T) {
	byTask := map[string][]Endpoint{}
	for _, e := range Endpoints() {
		byTask[e.Task] = append(byTask[e.Task], e)
	}
	for task, eps := range byTask {
		var sibling, confirmed []Endpoint
		for _, e := range eps {
			if e.Verified == VerifiedSibling {
				sibling = append(sibling, e)
			} else {
				confirmed = append(confirmed, e)
			}
		}
		for _, s := range sibling {
			if len(confirmed) == 0 {
				t.Errorf("%s is marked %q but no endpoint in task %q is live or documented — there is no sibling to inherit confidence from", s.ID, VerifiedSibling, task)
				continue
			}
			matched := false
			for _, c := range confirmed {
				if c.BodyTemplate == s.BodyTemplate && sameFieldNames(c.Fields, s.Fields) {
					matched = true
				}
			}
			if !matched {
				t.Errorf("%s claims %q but its request template does not match any confirmed sibling in task %q", s.ID, VerifiedSibling, task)
			}
		}
	}
}

func sameFieldNames(a, b []Field) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Kind != b[i].Kind {
			return false
		}
	}
	return true
}

// TestEveryFieldIsReachableFromTheRequest is the test that actually prevents a
// silently-dropped input. A field has to end up SOMEWHERE in the outbound
// request: in a JSON body template (referenced by one of its tokens) for a
// body-mode endpoint, or on the query string for a query-mode one. A field
// nobody reads is a form input the user fills in, pays for, and never sends.
func TestEveryFieldIsReachableFromTheRequest(t *testing.T) {
	for _, e := range Endpoints() {
		if e.BodyTemplate == "" {
			// Query mode: buildTargetRequest appends every CustomParam to the
			// query string, so a file field has nowhere to go.
			for _, f := range e.Fields {
				if f.Kind == FieldFile {
					t.Errorf("%s: file field %q on a query-parameter endpoint — a file cannot be a query param, this endpoint needs a BodyTemplate", e.ID, f.Name)
				}
			}
			continue
		}
		for _, f := range e.Fields {
			var token string
			if f.Kind == FieldFile {
				token = "{{file:" + f.Name + "}}"
			} else {
				token = "{{param:" + f.Name + "}}"
			}
			if !strings.Contains(e.BodyTemplate, token) {
				t.Errorf("%s: field %q is never referenced by the body template (looked for %s) — it would be collected from the user and then dropped", e.ID, f.Name, token)
			}
		}
	}
}

// TestBodyTemplatesAreValidJSONOnceExpanded catches an unbalanced brace or a
// stray comma in a template. An invalid body is only discovered by the
// endpoint, after it has been paid.
func TestBodyTemplatesAreValidJSONOnceExpanded(t *testing.T) {
	replacer := strings.NewReplacer(
		"{{param:task_description}}", "a job description",
		"{{fileName:resume}}", "resume.pdf",
		"{{file:resume}}", "QUFB",
	)
	for _, e := range Endpoints() {
		if e.BodyTemplate == "" {
			continue
		}
		expanded := replacer.Replace(e.BodyTemplate)
		if strings.Contains(expanded, "{{") {
			t.Errorf("%s: template still has an unexpanded token after substitution: %s", e.ID, expanded)
		}
		var any map[string]any
		if err := json.Unmarshal([]byte(expanded), &any); err != nil {
			t.Errorf("%s: expanded template is not valid JSON: %v (%s)", e.ID, err, expanded)
		}
	}
}

func TestLookupRejectsUnknownIDs(t *testing.T) {
	if _, ok := Lookup("code-review-fast"); !ok {
		t.Error("Lookup should find a real endpoint id")
	}
	for _, bad := range []string{"", "code-review", "https://evil.example.com", "../code-review-fast"} {
		if _, ok := Lookup(bad); ok {
			t.Errorf("Lookup(%q) returned an endpoint — an unknown id must never resolve", bad)
		}
	}
}

// TestTiersPairUp confirms every task has exactly one fast and one accurate
// endpoint, which is the shape the console's task/tier picker assumes.
func TestTiersPairUp(t *testing.T) {
	byTask := map[string][]string{}
	for _, e := range Endpoints() {
		byTask[e.Task] = append(byTask[e.Task], e.Tier)
	}
	for _, task := range Tasks() {
		tiers := byTask[task.Key]
		if len(tiers) != 2 {
			t.Errorf("task %q has %d endpoints, want a fast and an accurate one", task.Key, len(tiers))
			continue
		}
		if !(tiers[0] != tiers[1]) {
			t.Errorf("task %q has two %q tiers", task.Key, tiers[0])
		}
	}
}
