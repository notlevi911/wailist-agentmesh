package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func challenge(t *testing.T, raw string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("bad test fixture: %v", err)
	}
	return m
}

// TestBazaarSummaryFlagsTheEnumMismatch is the reason this command reports a
// bazaar line at all. A declaration whose schema enum does not exactly match
// the declared method is accepted by verify and settle -- real money moves,
// the response says success -- and then dropped by the catalog validator, so
// nothing about the payment's outcome reveals that it failed to catalog.
// Seeing it before spending is the whole point, so this checks the summary
// actually distinguishes the three cases rather than always looking fine.
func TestBazaarSummary(t *testing.T) {
	cases := []struct {
		name      string
		challenge string
		wantSub   string
	}{
		{
			name: "valid single-value enum matching the declared method",
			challenge: `{"extensions":{"bazaar":{
				"info":{"input":{"type":"http","method":"GET"}},
				"schema":{"properties":{"input":{"properties":{
					"method":{"type":"string","enum":["GET"]}}}}}}}}`,
			wantSub: `matches enum ["GET"]`,
		},
		{
			// The exact shape that silently cost every settlement its
			// catalog entry: declared GET, enum a superset of it.
			name: "superset enum -- the real bug",
			challenge: `{"extensions":{"bazaar":{
				"info":{"input":{"type":"http","method":"GET"}},
				"schema":{"properties":{"input":{"properties":{
					"method":{"type":"string","enum":["GET","HEAD","DELETE"]}}}}}}}}`,
			wantSub: "PRESENT BUT INVALID",
		},
		{
			// Enum holds one value, but not the declared one -- still a
			// mismatch, and easy to miss if the check only counted entries.
			name: "single-value enum naming a different method",
			challenge: `{"extensions":{"bazaar":{
				"info":{"input":{"type":"http","method":"GET"}},
				"schema":{"properties":{"input":{"properties":{
					"method":{"type":"string","enum":["POST"]}}}}}}}}`,
			wantSub: "PRESENT BUT INVALID",
		},
		{
			name:      "no declaration at all",
			challenge: `{"x402Version":2}`,
			wantSub:   "absent",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := bazaarSummary(challenge(t, tc.challenge))
			if !strings.Contains(got, tc.wantSub) {
				t.Errorf("want a summary containing %q, got %q", tc.wantSub, got)
			}
		})
	}
}

// TestBazaarSummarySurvivesMalformedDeclarations guards the inspector against
// being the thing that crashes: it runs against arbitrary third-party
// endpoints, which are under no obligation to send a well-formed extension.
// Reporting something useless is fine here; panicking before the operator
// sees the price is not.
func TestBazaarSummarySurvivesMalformedDeclarations(t *testing.T) {
	malformed := []string{
		`{"extensions":{"bazaar":{}}}`,
		`{"extensions":{"bazaar":{"info":"not an object"}}}`,
		`{"extensions":{"bazaar":{"info":{"input":{"method":42}},"schema":null}}}`,
		`{"extensions":{"bazaar":{"info":{"input":{"method":"GET"}},
			"schema":{"properties":{"input":{"properties":{"method":{"enum":"not an array"}}}}}}}}`,
		`{"extensions":"not an object"}`,
		`{}`,
	}
	for _, raw := range malformed {
		t.Run(raw[:min(len(raw), 40)], func(t *testing.T) {
			if got := bazaarSummary(challenge(t, raw)); got == "" {
				t.Error("want some summary string, got empty")
			}
		})
	}
}
