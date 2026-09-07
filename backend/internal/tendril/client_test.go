package tendril

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func fakeRegistry(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/platform":
			w.Write([]byte(`{"payTo":"PAYTO","network":"algorand:wGHE2","asset":{"id":"31566704","decimals":6,"symbol":"USDC"},"minTopUpAtomic":100000,"maxTopUpAtomic":1000000000}`))
		case "/explorer":
			w.Write([]byte(`{"nodes":[
				{"id":"cheap","cpuCores":2,"ramMb":4096,"pricePerHourUsd":1.5,"status":"online"},
				{"id":"dear","cpuCores":8,"ramMb":32768,"pricePerHourUsd":6,"status":"online"},
				{"id":"gone","cpuCores":4,"ramMb":8192,"pricePerHourUsd":0.5,"status":"offline"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestPlatformParsesAssetAndBounds(t *testing.T) {
	srv := fakeRegistry(t)
	defer srv.Close()

	p, err := NewClient(srv.URL).Platform(context.Background())
	if err != nil {
		t.Fatalf("Platform: %v", err)
	}
	if p.Asset.ID != "31566704" || p.Asset.Decimals != 6 {
		t.Errorf("asset = %+v, want id 31566704 decimals 6", p.Asset)
	}
	if p.MinTopUpAtomic != 100000 || p.MaxTopUpAtomic != 1000000000 {
		t.Errorf("topup bounds = %d..%d", p.MinTopUpAtomic, p.MaxTopUpAtomic)
	}
}

// Offline machines are unrentable, and the market must be cheapest-first so the
// canvas's default pick is the cheapest box rather than whatever Tendril
// happened to list first.
func TestOnlineNodesFiltersOfflineAndSortsByPrice(t *testing.T) {
	srv := fakeRegistry(t)
	defer srv.Close()

	nodes, err := NewClient(srv.URL).OnlineNodes(context.Background())
	if err != nil {
		t.Fatalf("OnlineNodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("got %d nodes, want 2 (offline filtered)", len(nodes))
	}
	if nodes[0].ID != "cheap" || nodes[1].ID != "dear" {
		t.Errorf("order = %s,%s; want cheap,dear", nodes[0].ID, nodes[1].ID)
	}
	if nodes[1].RateUSDMicrosPerHour() != 6_000_000 {
		t.Errorf("rate = %d, want 6000000", nodes[1].RateUSDMicrosPerHour())
	}
}

// A trailing slash on the configured base URL must not produce "//platform".
func TestBaseURLTrailingSlashTrimmed(t *testing.T) {
	srv := fakeRegistry(t)
	defer srv.Close()

	if _, err := NewClient(srv.URL + "/").Platform(context.Background()); err != nil {
		t.Fatalf("Platform with trailing slash: %v", err)
	}
}
