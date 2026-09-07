package bazaar

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const mainnet = "algorand:wGHE2Pwdvd7S12BL5FaOP20EGYesN73ktiC1qzkkit8="
const testnet = "algorand:SGO1GKSzyE7IEPItTxCByw9x8FmnrCDexi9/cOUJOiI="

// upstreamItem builds one raw catalog entry the way GoPlausible returns it.
func upstreamItem(id, url, network string) map[string]any {
	return map[string]any{
		"id":          id,
		"resourceUrl": url,
		"method":      "GET",
		"description": "a test resource",
		"merchantId":  "m1",
		"accepts": []any{map[string]any{
			"network": network,
			"amount":  "5000",
			"asset":   "31566704",
			"payTo":   "PAYTOADDR",
		}},
		"discoveryInfo": map[string]any{
			"input": map[string]any{
				"method":      "GET",
				"queryParams": map[string]any{"symbol": "ALGO"},
			},
		},
		"settleCount": 7,
		"lastSeen":    "2026-08-03T13:50:43.356Z",
	}
}

// fakeUpstream serves a paginated catalog like the real facilitator does.
func fakeUpstream(t *testing.T, items []map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/discovery/resources" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"x402Version": 2,
			"items":       items,
			"pagination":  map[string]any{"limit": 100, "offset": 0, "total": len(items)},
		})
	}))
}

func TestFetchAllKeepsOnlyPublicAlgorandResources(t *testing.T) {
	srv := fakeUpstream(t, []map[string]any{
		upstreamItem("keep-main", "https://good.example/api", mainnet),
		upstreamItem("keep-test", "https://also.example/api", testnet),
		upstreamItem("drop-evm", "https://evm.example/api", "eip155:8453"),
		upstreamItem("drop-local", "http://localhost:3000/api", mainnet),
		upstreamItem("drop-private", "https://192.168.1.4/api", mainnet),
		upstreamItem("drop-plain", "http://plain.example/api", mainnet),
	})
	defer srv.Close()

	got, err := FetchAll(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 kept resources, got %d: %+v", len(got), got)
	}
	byID := map[string]Resource{}
	for _, r := range got {
		byID[r.ID] = r
	}
	if _, ok := byID["keep-main"]; !ok {
		t.Error("mainnet https resource was dropped")
	}
	if !byID["keep-test"].Testnet {
		t.Error("testnet resource must be flagged Testnet=true")
	}
	if byID["keep-main"].Testnet {
		t.Error("mainnet resource must have Testnet=false")
	}
}

func TestFetchAllNormalisesFields(t *testing.T) {
	srv := fakeUpstream(t, []map[string]any{
		upstreamItem("r1", "https://good.example/api", mainnet),
	})
	defer srv.Close()

	got, err := FetchAll(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	r := got[0]
	if r.AmountMicros != 5000 {
		t.Errorf("AmountMicros = %d, want 5000", r.AmountMicros)
	}
	if r.PayTo != "PAYTOADDR" || r.Asset != "31566704" {
		t.Errorf("payTo/asset not carried through: %+v", r)
	}
	if len(r.Params) != 1 || r.Params[0].Name != "symbol" {
		t.Fatalf("want one param named symbol, got %+v", r.Params)
	}
	// Catalog examples are placeholders, so they belong in the description,
	// never as a usable default.
	if r.Params[0].Description != "example: ALGO" {
		t.Errorf("Description = %q, want %q", r.Params[0].Description, "example: ALGO")
	}
	if r.SettleCount != 7 {
		t.Errorf("SettleCount = %d, want 7", r.SettleCount)
	}
}

func TestParamsFromCapsDescriptionLength(t *testing.T) {
	raw := upstreamItem("big-example", "https://big.example/api", mainnet)
	raw["discoveryInfo"] = map[string]any{
		"input": map[string]any{
			"method":      "GET",
			"queryParams": map[string]any{"blob": strings.Repeat("x", 5000)},
		},
	}
	srv := fakeUpstream(t, []map[string]any{raw})
	defer srv.Close()

	got, err := FetchAll(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(got[0].Params) != 1 {
		t.Fatalf("want 1 param, got %d", len(got[0].Params))
	}
	desc := got[0].Params[0].Description
	if n := len([]rune(desc)); n > paramDescriptionMax+len("example: ")+1 {
		t.Errorf("Description is %d runes, want capped near %d", n, paramDescriptionMax)
	}
}

func TestNormaliseCapsResourceDescriptionLength(t *testing.T) {
	raw := upstreamItem("big-desc", "https://big.example/api", mainnet)
	raw["description"] = strings.Repeat("x", 5000)
	srv := fakeUpstream(t, []map[string]any{raw})
	defer srv.Close()

	got, err := FetchAll(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if n := len([]rune(got[0].Description)); n > resourceDescriptionMax+1 {
		t.Errorf("Description is %d runes, want capped near %d", n, resourceDescriptionMax)
	}
}

func TestFetchAllPagesUntilShortPage(t *testing.T) {
	// 250 items across 3 pages proves the loop follows offset rather than
	// stopping after the first response.
	all := make([]map[string]any, 0, 250)
	for i := 0; i < 250; i++ {
		all = append(all, upstreamItem(fmt.Sprintf("r%d", i), fmt.Sprintf("https://h%d.example/api", i), mainnet))
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		off := 0
		fmt.Sscanf(r.URL.Query().Get("offset"), "%d", &off)
		end := off + 100
		if end > len(all) {
			end = len(all)
		}
		page := []map[string]any{}
		if off < len(all) {
			page = all[off:end]
		}
		json.NewEncoder(w).Encode(map[string]any{"items": page})
	}))
	defer srv.Close()

	got, err := FetchAll(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(got) != 250 {
		t.Fatalf("want 250 resources across pages, got %d", len(got))
	}
}

func TestNormaliseNeverProducesNilParams(t *testing.T) {
	raw := upstreamItem("no-params", "https://noparams.example/api", mainnet)
	delete(raw, "discoveryInfo") // no declared inputs at all
	srv := fakeUpstream(t, []map[string]any{raw})
	defer srv.Close()
	got, err := FetchAll(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if got[0].Params == nil {
		t.Fatal("Params must never be nil (marshals as JSON null, crashes the frontend) — want []Param{}")
	}
	b, _ := json.Marshal(got[0])
	if strings.Contains(string(b), `"params":null`) {
		t.Fatal("Params marshaled as JSON null")
	}
}

func TestFetchAllUpstreamErrorIsReturned(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	if _, err := FetchAll(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Fatal("want an error when upstream fails, got nil")
	}
}

// TestFetchAllDropsCGNATResource guards keepResource sharing
// netutil.IsPrivateIP rather than a narrower, independently maintained
// range list. Before this, a literal IP in the CGNAT range (100.64.0.0/10)
// passed keepResource and showed as pickable in the Bazaar UI, but the real
// payment path's dial-time check (which does use IsPrivateIP) would reject
// it once added -- a confusing dead end.
func TestFetchAllDropsCGNATResource(t *testing.T) {
	srv := fakeUpstream(t, []map[string]any{
		upstreamItem("keep-main", "https://good.example/api", mainnet),
		upstreamItem("drop-cgnat", "https://100.64.0.5/api", mainnet),
	})
	defer srv.Close()

	got, err := FetchAll(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(got) != 1 || got[0].ID != "keep-main" {
		t.Fatalf("want only the non-CGNAT resource kept, got %+v", got)
	}
}

// TestFetchAllDropsIPv6Unspecified guards against a regression uncovered
// while moving IsPrivateIP into netutil: the original hand-rolled CIDR list
// here (and in engine/nodes) had no entry matching "::" (IPv6 unspecified),
// unlike the narrower per-package check it replaced (net.IP.IsUnspecified),
// so a catalog entry literally hosted at "::" would have passed keepResource
// despite being exactly as unreachable as its IPv4 counterpart 0.0.0.0.
func TestFetchAllDropsIPv6Unspecified(t *testing.T) {
	srv := fakeUpstream(t, []map[string]any{
		upstreamItem("keep-main", "https://good.example/api", mainnet),
		upstreamItem("drop-unspecified", "https://[::]/api", mainnet),
	})
	defer srv.Close()

	got, err := FetchAll(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(got) != 1 || got[0].ID != "keep-main" {
		t.Fatalf("want only the non-unspecified resource kept, got %+v", got)
	}
}
