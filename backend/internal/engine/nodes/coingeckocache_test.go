package nodes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentmesh/backend/internal/models"
)

// cgStub counts the requests CoinGecko receives and answers each with status.
func cgStub(t *testing.T, status *atomic.Int32, delay time.Duration) *atomic.Int64 {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(delay)
		if s := int(status.Load()); s != 0 && s != http.StatusOK {
			http.Error(w, `{"status":{"error_code":429,"error_message":"rate limited"}}`, s)
			return
		}
		w.Write([]byte(`{"algorand":{"usd":0.0894}}`))
	}))
	t.Cleanup(srv.Close)
	SetURLValidatorForTest(func(string) error { return nil })
	t.Cleanup(func() { SetURLValidatorForTest(func(string) error { return nil }) })
	SetCoinGeckoAPIBaseForTest(srv.URL)
	t.Cleanup(func() { SetCoinGeckoAPIBaseForTest("") })
	// These tests are about CoinGecko and the cache alone. Without this a
	// refusal falls through to the real Coinbase over the network.
	offlineBackups(t)
	return &hits
}

// offlineBackups points the price backups at a server that knows nothing.
func offlineBackups(t *testing.T) {
	t.Helper()
	dead := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(dead.Close)
	SetPriceFallbackBasesForTest(dead.URL, dead.URL, dead.URL)
	t.Cleanup(restoreOfflineBackups)
}

// fakeClock replaces the cache's clock for one test.
func fakeClock(t *testing.T) *time.Time {
	t.Helper()
	now := time.Date(2026, 9, 17, 9, 0, 0, 0, time.UTC)
	coinGeckoCache.Lock()
	coinGeckoCache.now = func() time.Time { return now }
	coinGeckoCache.Unlock()
	t.Cleanup(func() {
		coinGeckoCache.Lock()
		coinGeckoCache.now = time.Now
		coinGeckoCache.Unlock()
	})
	return &now
}

func price(t *testing.T) (any, error) {
	t.Helper()
	node := models.WorkflowNode{Type: models.NodeTypeAction, Template: "coingecko",
		Config: map[string]string{"cgIDs": "algorand"}}
	return fetchCoinGecko(context.Background(), node, emptyRunContext{})
}

// A build resolves a coin, test-runs, and test-runs again. Within a minute
// that is one request, not three against a per-IP limit.
func TestCoinGeckoRepeatsWithinAMinuteAreOneRequest(t *testing.T) {
	var status atomic.Int32
	hits := cgStub(t, &status, 0)
	now := fakeClock(t)
	for i := 0; i < 3; i++ {
		if _, err := price(t); err != nil {
			t.Fatal(err)
		}
		*now = now.Add(20 * time.Second)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("upstream requests = %d, want 1", got)
	}
}

func TestCoinGeckoAPIBaseOverrideClearsCachedData(t *testing.T) {
	var status atomic.Int32
	hits := cgStub(t, &status, 0)
	if _, err := price(t); err != nil {
		t.Fatal(err)
	}
	SetCoinGeckoAPIBaseForTest(apiBase("coingecko"))
	if _, err := price(t); err != nil {
		t.Fatal(err)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("requests after resetting the same endpoint = %d, want 2", got)
	}
}

func TestCoinGeckoAskedAgainAfterAMinute(t *testing.T) {
	var status atomic.Int32
	hits := cgStub(t, &status, 0)
	now := fakeClock(t)
	price(t)
	*now = now.Add(61 * time.Second)
	price(t)
	if got := hits.Load(); got != 2 {
		t.Errorf("upstream requests = %d, want 2", got)
	}
}

// A refusal is not remembered, so the next call really tries again...
func TestCoinGeckoDoesNotCacheARefusal(t *testing.T) {
	var status atomic.Int32
	status.Store(http.StatusTooManyRequests)
	hits := cgStub(t, &status, 0)
	fakeClock(t)
	if _, err := price(t); err == nil {
		t.Fatal("a 429 must be an error")
	}
	status.Store(http.StatusOK)
	if _, err := price(t); err != nil {
		t.Fatalf("the retry after a refusal failed: %v", err)
	}
	if got := hits.Load(); got != 2 {
		t.Errorf("upstream requests = %d, want 2", got)
	}
}

// ...and an old price is never passed off as the current one when CoinGecko
// refuses.
func TestCoinGeckoNeverServesAnExpiredPriceOnARefusal(t *testing.T) {
	var status atomic.Int32
	cgStub(t, &status, 0)
	now := fakeClock(t)
	if _, err := price(t); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(10 * time.Minute)
	status.Store(http.StatusTooManyRequests)
	if _, err := price(t); err == nil {
		t.Fatal("an expired price was returned in place of the refusal")
	}
}

// Two nodes asking for the same coin at the same moment share one request.
func TestCoinGeckoCollapsesConcurrentIdenticalRequests(t *testing.T) {
	var status atomic.Int32
	hits := cgStub(t, &status, 50*time.Millisecond)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := price(t); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got := hits.Load(); got != 1 {
		t.Errorf("upstream requests = %d, want 1", got)
	}
}

// Each caller gets its own copy: one connector editing its result must not
// change what the next one reads.
func TestCoinGeckoCachedResultsAreNotShared(t *testing.T) {
	var status atomic.Int32
	cgStub(t, &status, 0)
	fakeClock(t)
	first, _ := price(t)
	first.(map[string]any)["algorand"] = "tampered"
	second, _ := price(t)
	if _, ok := second.(map[string]any)["algorand"].(map[string]any); !ok {
		t.Fatalf("second read saw the first caller's edit: %v", second)
	}
}

// A run that is stopped while waiting must not fail the others sharing the
// request.
func TestCoinGeckoOneCancelledCallerDoesNotFailTheRest(t *testing.T) {
	var status atomic.Int32
	cgStub(t, &status, 100*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	node := models.WorkflowNode{Type: models.NodeTypeAction, Template: "coingecko",
		Config: map[string]string{"cgIDs": "algorand"}}
	var cancelledErr, otherErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, cancelledErr = fetchCoinGecko(ctx, node, emptyRunContext{})
	}()
	go func() {
		defer wg.Done()
		time.Sleep(10 * time.Millisecond)
		_, otherErr = fetchCoinGecko(context.Background(), node, emptyRunContext{})
	}()
	time.Sleep(30 * time.Millisecond)
	cancel()
	wg.Wait()
	if cancelledErr == nil {
		t.Error("the cancelled caller should see its cancellation")
	}
	if otherErr != nil {
		t.Errorf("the other caller failed because someone else cancelled: %v", otherErr)
	}
}
