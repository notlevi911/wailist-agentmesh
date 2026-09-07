package payments

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Plausible bounds for 1 INR in USD. INR/USD has stayed within roughly 60-140 over any
// realistic window; these bounds (~7-200 INR/USD) are deliberately wide so routine market
// movement never trips them — they exist only to catch a broken/inverted upstream value
// before it gets locked into a ledger row and drives how much a user is credited.
const (
	minPlausibleINRToUSD = 0.005
	maxPlausibleINRToUSD = 0.15
)

var fxHTTPClient = &http.Client{Timeout: 5 * time.Second}

// fetchINRToUSD is swappable in tests via SetFetchRateForTest.
var fetchINRToUSD = liveFetchINRToUSD

const fxAPIURL = "https://open.er-api.com/v6/latest/INR"

func liveFetchINRToUSD(ctx context.Context) (float64, error) {
	return fetchINRToUSDFromURL(ctx, fxAPIURL)
}

// LiveFetchINRToUSDForTest exercises the real parsing/bounds-checking logic against an
// arbitrary URL (e.g. an httptest.Server). Call only from tests.
func LiveFetchINRToUSDForTest(ctx context.Context, url string) (float64, error) {
	return fetchINRToUSDFromURL(ctx, url)
}

func fetchINRToUSDFromURL(ctx context.Context, url string) (float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := fxHTTPClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("fx: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("fx: unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, err
	}
	var parsed struct {
		Rates map[string]float64 `json:"rates"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, fmt.Errorf("fx: parse response: %w", err)
	}
	rate, ok := parsed.Rates["USD"]
	if !ok || rate <= 0 {
		return 0, fmt.Errorf("fx: no USD rate in response")
	}
	if rate < minPlausibleINRToUSD || rate > maxPlausibleINRToUSD {
		return 0, fmt.Errorf("fx: rate %v outside plausible bounds [%v, %v]", rate, minPlausibleINRToUSD, maxPlausibleINRToUSD)
	}
	return rate, nil
}

// FetchINRToUSDRate returns the current conversion rate where 1 INR = rate USD.
func FetchINRToUSDRate(ctx context.Context) (float64, error) {
	return fetchINRToUSD(ctx)
}

// SetFetchRateForTest overrides the rate fetcher used by FetchINRToUSDRate. Pass nil to reset
// to the live implementation. Call only from tests.
func SetFetchRateForTest(fn func(context.Context) (float64, error)) {
	if fn == nil {
		fetchINRToUSD = liveFetchINRToUSD
	} else {
		fetchINRToUSD = fn
	}
}

// --- Cached rate -----------------------------------------------------------

// The rate is consulted on every /payments/providers request, which the
// billing page and checkout panel both hit on mount. Fetching live each time
// put a third-party host on the critical path of a page load, and made a brief
// outage at that host disable Stripe, PayPal and NOWPayments simultaneously on
// a fully configured deployment.
//
// So: serve from cache for fxCacheTTL, and on a failed refresh keep serving
// the last good rate rather than reporting none. A slightly stale rate is a
// far better failure than "all USD gateways are Coming soon". Only a cold
// cache (no rate ever fetched) can still fail.
const (
	fxCacheTTL = 10 * time.Minute
	// How long a stale rate may still be served after refreshes start
	// failing. Beyond this the rate is too old to price a real charge with,
	// and reporting no rate is the honest answer.
	fxStaleGrace = 24 * time.Hour
)

var fxCache struct {
	sync.Mutex
	rate      float64
	fetchedAt time.Time
}

// CachedINRToUSDRate returns the INR->USD rate, refreshing at most once per
// fxCacheTTL. On a refresh failure it falls back to the last good value while
// that value is younger than fxStaleGrace, so a transient upstream outage does
// not take the USD checkout options offline.
//
// The returned bool reports whether the value is stale (a refresh failed and
// this is the fallback), so callers can log it without having to guess.
func CachedINRToUSDRate(ctx context.Context) (rate float64, stale bool, err error) {
	fxCache.Lock()
	defer fxCache.Unlock()

	if fxCache.rate > 0 && time.Since(fxCache.fetchedAt) < fxCacheTTL {
		return fxCache.rate, false, nil
	}

	// Not ctx: this fills a cache shared by every concurrent caller, so one
	// caller's request being cancelled must not fail the refresh for the
	// others waiting on the same lock. fxHTTPClient's own 5s timeout still
	// bounds the call.
	fresh, err := fetchINRToUSD(context.Background())
	if err == nil {
		fxCache.rate = fresh
		fxCache.fetchedAt = time.Now()
		return fresh, false, nil
	}

	if fxCache.rate > 0 && time.Since(fxCache.fetchedAt) < fxStaleGrace {
		return fxCache.rate, true, nil
	}
	return 0, false, err
}

// ResetFXCacheForTest clears the cached rate. Call only from tests.
func ResetFXCacheForTest() {
	fxCache.Lock()
	defer fxCache.Unlock()
	fxCache.rate, fxCache.fetchedAt = 0, time.Time{}
}
