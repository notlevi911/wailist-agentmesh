package payments

import (
	"context"
	"errors"
	"testing"
)

// The rate is consulted on every /payments/providers request; without a cache
// that put a third-party host on the critical path of every billing page load.
func TestCachedINRToUSDRateFetchesOnceWithinTheTTL(t *testing.T) {
	ResetFXCacheForTest()
	defer ResetFXCacheForTest()

	calls := 0
	SetFetchRateForTest(func(context.Context) (float64, error) {
		calls++
		return 0.012, nil
	})
	defer SetFetchRateForTest(nil)

	for i := 0; i < 5; i++ {
		rate, stale, err := CachedINRToUSDRate(context.Background())
		if err != nil || rate != 0.012 || stale {
			t.Fatalf("call %d: rate=%v stale=%v err=%v", i, rate, stale, err)
		}
	}
	if calls != 1 {
		t.Errorf("upstream called %d times for 5 reads, want 1", calls)
	}
}

// A brief outage at the FX host must not disable Stripe, PayPal and
// NOWPayments all at once on an otherwise fully configured deployment.
func TestCachedINRToUSDRateServesTheLastGoodRateOnFailure(t *testing.T) {
	ResetFXCacheForTest()
	defer ResetFXCacheForTest()

	SetFetchRateForTest(func(context.Context) (float64, error) { return 0.012, nil })
	if _, _, err := CachedINRToUSDRate(context.Background()); err != nil {
		t.Fatalf("priming the cache: %v", err)
	}

	// Force the next read past the TTL, then make refreshes fail.
	fxCache.Lock()
	fxCache.fetchedAt = fxCache.fetchedAt.Add(-2 * fxCacheTTL)
	fxCache.Unlock()
	SetFetchRateForTest(func(context.Context) (float64, error) {
		return 0, errors.New("upstream down")
	})
	defer SetFetchRateForTest(nil)

	rate, stale, err := CachedINRToUSDRate(context.Background())
	if err != nil {
		t.Fatalf("a failed refresh took the rate offline entirely: %v", err)
	}
	if rate != 0.012 {
		t.Errorf("rate = %v, want the last good 0.012", rate)
	}
	if !stale {
		t.Error("stale = false, want true so the caller can log it")
	}
}

// Past the grace window the rate is too old to price a real charge with, and
// reporting none is the honest answer.
func TestCachedINRToUSDRateGivesUpOnAVeryStaleRate(t *testing.T) {
	ResetFXCacheForTest()
	defer ResetFXCacheForTest()

	SetFetchRateForTest(func(context.Context) (float64, error) { return 0.012, nil })
	if _, _, err := CachedINRToUSDRate(context.Background()); err != nil {
		t.Fatalf("priming the cache: %v", err)
	}
	fxCache.Lock()
	fxCache.fetchedAt = fxCache.fetchedAt.Add(-2 * fxStaleGrace)
	fxCache.Unlock()
	SetFetchRateForTest(func(context.Context) (float64, error) {
		return 0, errors.New("upstream down")
	})
	defer SetFetchRateForTest(nil)

	if _, _, err := CachedINRToUSDRate(context.Background()); err == nil {
		t.Error("a day-old rate was still served as usable")
	}
}

// A cold cache has nothing to fall back on.
func TestCachedINRToUSDRateFailsOnAColdCache(t *testing.T) {
	ResetFXCacheForTest()
	defer ResetFXCacheForTest()

	SetFetchRateForTest(func(context.Context) (float64, error) {
		return 0, errors.New("upstream down")
	})
	defer SetFetchRateForTest(nil)

	if _, _, err := CachedINRToUSDRate(context.Background()); err == nil {
		t.Error("expected an error when no rate has ever been fetched")
	}
}
