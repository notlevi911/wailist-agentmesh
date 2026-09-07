package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentmesh/backend/internal/bazaar"
)

const bzMainnet = "algorand:wGHE2Pwdvd7S12BL5FaOP20EGYesN73ktiC1qzkkit8="

// fakeCatalog serves n synthetic resources on the first page and counts how
// often the upstream was hit, so caching can be asserted.
func fakeCatalog(n int, hits *int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		items := []map[string]any{}
		// Everything fits on page 0 (n stays well under the 100 page limit),
		// so any later offset correctly returns an empty page.
		if r.URL.Query().Get("offset") == "0" {
			for i := 0; i < n; i++ {
				id := fmt.Sprintf("r%d", i)
				items = append(items, map[string]any{
					"id":          id,
					"resourceUrl": fmt.Sprintf("https://h%d.example/api", i),
					"method":      "GET",
					"description": "synthetic resource",
					"accepts": []any{map[string]any{
						"network": bzMainnet, "amount": "5000",
						"asset": "31566704", "payTo": "P",
					}},
					// Descending, so the handler's settle-count ordering is
					// observable: r0 must come first.
					"settleCount": n - i,
				})
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"items": items})
	}))
}

func TestBazaarResourcesPages(t *testing.T) {
	var hits int32
	srv := fakeCatalog(30, &hits)
	defer srv.Close()
	d := &Deps{BazaarBaseURL: srv.URL}

	rec := httptest.NewRecorder()
	d.BazaarResources(rec, httptest.NewRequest(http.MethodGet, "/bazaar/resources?offset=0&limit=10", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var page struct {
		Items []bazaar.Resource `json:"items"`
		Total int               `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Items) != 10 {
		t.Errorf("first page has %d items, want 10", len(page.Items))
	}
	// 30 synthetic + however many curated entries the registry appends.
	if page.Total < 30 {
		t.Errorf("Total = %d, want at least 30", page.Total)
	}

	rec2 := httptest.NewRecorder()
	d.BazaarResources(rec2, httptest.NewRequest(http.MethodGet, "/bazaar/resources?offset=10&limit=10", nil))
	var page2 struct {
		Items []bazaar.Resource `json:"items"`
	}
	json.Unmarshal(rec2.Body.Bytes(), &page2)
	if len(page2.Items) != 10 {
		t.Fatalf("second page has %d items, want 10", len(page2.Items))
	}
	if page.Items[0].ID == page2.Items[0].ID {
		t.Error("second page repeated the first page's first item")
	}
}

func TestBazaarResourcesCachesUpstream(t *testing.T) {
	var hits int32
	srv := fakeCatalog(5, &hits)
	defer srv.Close()
	d := &Deps{BazaarBaseURL: srv.URL}

	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		d.BazaarResources(rec, httptest.NewRequest(http.MethodGet, "/bazaar/resources", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status %d", i, rec.Code)
		}
	}
	// One crawl is two upstream calls at most (page 0 plus the short-page
	// stop); three handler calls must not multiply that.
	if got := atomic.LoadInt32(&hits); got > 2 {
		t.Errorf("upstream hit %d times across 3 cached requests, want <= 2", got)
	}
}

func TestBazaarResourcesSearchFilters(t *testing.T) {
	var hits int32
	srv := fakeCatalog(5, &hits)
	defer srv.Close()
	d := &Deps{BazaarBaseURL: srv.URL}

	rec := httptest.NewRecorder()
	d.BazaarResources(rec, httptest.NewRequest(http.MethodGet, "/bazaar/resources?q=tendril", nil))
	var page struct {
		Items []bazaar.Resource `json:"items"`
	}
	json.Unmarshal(rec.Body.Bytes(), &page)
	for _, it := range page.Items {
		if it.Provider != "Tendril" {
			t.Errorf("q=tendril returned unrelated entry %s", it.URL)
		}
	}
}

func TestBazaarResourcesSupportedFilterIncludesUnmatchedCuratedEntries(t *testing.T) {
	var hits int32
	srv := fakeCatalog(5, &hits) // none of these match any curated URL
	defer srv.Close()
	d := &Deps{BazaarBaseURL: srv.URL}

	rec := httptest.NewRecorder()
	d.BazaarResources(rec, httptest.NewRequest(http.MethodGet, "/bazaar/resources?supported=1&limit=100", nil))
	var page struct {
		Items []bazaar.Resource `json:"items"`
	}
	json.Unmarshal(rec.Body.Bytes(), &page)
	if len(page.Items) == 0 {
		t.Fatal("supported=1 returned nothing, but curated entries with zero catalog matches must still appear")
	}
	for _, it := range page.Items {
		if !it.Supported {
			t.Errorf("supported=1 returned an unsupported entry: %s", it.URL)
		}
	}
}

func TestBazaarResourcesSupportedFalseExcludesSupportedEntries(t *testing.T) {
	var hits int32
	srv := fakeCatalog(5, &hits)
	defer srv.Close()
	d := &Deps{BazaarBaseURL: srv.URL}

	rec := httptest.NewRecorder()
	d.BazaarResources(rec, httptest.NewRequest(http.MethodGet, "/bazaar/resources?supported=0&limit=100", nil))
	var page struct {
		Items []bazaar.Resource `json:"items"`
	}
	json.Unmarshal(rec.Body.Bytes(), &page)
	if len(page.Items) == 0 {
		t.Fatal("supported=0 returned nothing, want the synthetic unsupported entries")
	}
	for _, it := range page.Items {
		if it.Supported {
			t.Errorf("supported=0 returned a supported entry: %s", it.URL)
		}
	}
}

func TestBazaarResourcesSupportedOtherValueLeavesItemsUntouched(t *testing.T) {
	var hits int32
	srv := fakeCatalog(5, &hits)
	defer srv.Close()
	d := &Deps{BazaarBaseURL: srv.URL}

	rec := httptest.NewRecorder()
	d.BazaarResources(rec, httptest.NewRequest(http.MethodGet, "/bazaar/resources?supported=yes&limit=100", nil))
	var page struct {
		Items []bazaar.Resource `json:"items"`
	}
	json.Unmarshal(rec.Body.Bytes(), &page)
	// supported=yes is neither "1"/"true" (want=true) nor "0"/"false"
	// (want=false); the doc comment says any other value leaves items
	// untouched, so both supported and unsupported entries must appear.
	var sawSupported, sawUnsupported bool
	for _, it := range page.Items {
		if it.Supported {
			sawSupported = true
		} else {
			sawUnsupported = true
		}
	}
	if !sawSupported || !sawUnsupported {
		t.Errorf("supported=yes must leave items untouched; sawSupported=%v sawUnsupported=%v", sawSupported, sawUnsupported)
	}
}

func TestBazaarResourcesSupportedCountReflectsFullCatalogNotFilteredResult(t *testing.T) {
	var hits int32
	srv := fakeCatalog(5, &hits)
	defer srv.Close()
	d := &Deps{BazaarBaseURL: srv.URL}

	unfiltered := httptest.NewRecorder()
	d.BazaarResources(unfiltered, httptest.NewRequest(http.MethodGet, "/bazaar/resources?limit=100", nil))
	var unfilteredPage struct {
		SupportedCount int `json:"supportedCount"`
	}
	json.Unmarshal(unfiltered.Body.Bytes(), &unfilteredPage)
	if unfilteredPage.SupportedCount == 0 {
		t.Fatal("want at least the curated entries counted as supported")
	}

	// A search that matches nothing must still report the same total
	// supportedCount, not 0 — the field describes the whole catalog, not the
	// filtered result.
	searched := httptest.NewRecorder()
	d.BazaarResources(searched, httptest.NewRequest(http.MethodGet, "/bazaar/resources?q=zzz-no-match&limit=100", nil))
	var searchedPage struct {
		SupportedCount int `json:"supportedCount"`
	}
	json.Unmarshal(searched.Body.Bytes(), &searchedPage)
	if searchedPage.SupportedCount != unfilteredPage.SupportedCount {
		t.Errorf("supportedCount = %d under a non-matching search, want %d (unfiltered total)", searchedPage.SupportedCount, unfilteredPage.SupportedCount)
	}
}

func TestBazaarResourcesColdStartBackoffThrottlesRetries(t *testing.T) {
	orig := bazaarRetryBackoff
	bazaarRetryBackoff = 50 * time.Millisecond
	defer func() { bazaarRetryBackoff = orig }()

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	d := &Deps{BazaarBaseURL: srv.URL}

	// Two requests in immediate succession, before the very first fetch has
	// ever succeeded (cold start): the second must be throttled by the
	// backoff instead of starting its own crawl.
	rec1 := httptest.NewRecorder()
	d.BazaarResources(rec1, httptest.NewRequest(http.MethodGet, "/bazaar/resources", nil))
	rec2 := httptest.NewRecorder()
	d.BazaarResources(rec2, httptest.NewRequest(http.MethodGet, "/bazaar/resources", nil))

	if rec1.Code != http.StatusBadGateway || rec2.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, %d, want both 502", rec1.Code, rec2.Code)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("upstream hit %d times across 2 cold-start requests within the backoff window, want 1", got)
	}
}

// TestCatalogFollowerWaitsOnInflightCrawlInsteadOfTakingBackoffBranch guards
// against checking the backoff window before the inflight channel: a
// follower arriving while a crawl is already running (and still within the
// backoff window that crawl's own start just set) must wait on that crawl,
// not read items/lastErr while both are still nil on a cold start and
// return a spurious empty "200 {items: [], total: 0}".
func TestCatalogFollowerWaitsOnInflightCrawlInsteadOfTakingBackoffBranch(t *testing.T) {
	orig := bazaarRetryBackoff
	bazaarRetryBackoff = 5 * time.Second
	defer func() { bazaarRetryBackoff = orig }()

	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		items := []map[string]any{}
		if r.URL.Query().Get("offset") == "0" {
			items = append(items, map[string]any{
				"id": "r0", "resourceUrl": "https://h0.example/api", "method": "GET",
				"accepts": []any{map[string]any{
					"network": bzMainnet, "amount": "5000", "asset": "31566704", "payTo": "P",
				}},
			})
		}
		json.NewEncoder(w).Encode(map[string]any{"items": items})
	}))
	defer srv.Close()
	d := &Deps{BazaarBaseURL: srv.URL}

	firstDone := make(chan struct{})
	go func() {
		d.catalog(context.Background())
		close(firstDone)
	}()
	time.Sleep(20 * time.Millisecond) // let the first caller actually start the crawl

	secondResult := make(chan []bazaar.Resource, 1)
	go func() {
		items, _ := d.catalog(context.Background())
		secondResult <- items
	}()

	select {
	case items := <-secondResult:
		t.Fatalf("second caller returned early with %d items instead of waiting for the in-flight crawl", len(items))
	case <-time.After(50 * time.Millisecond):
		// Still waiting on the shared crawl, as expected.
	}

	close(release)
	<-firstDone
	items := <-secondResult
	// Merge() also appends the curated registry's own entries, so the total
	// isn't exactly 1 -- what matters is that the fetched item made it
	// through, proving the second caller saw the real crawl result rather
	// than a spurious pre-completion nil/nil.
	found := false
	for _, r := range items {
		if r.ID == "r0" {
			found = true
		}
	}
	if !found {
		t.Errorf("want the second caller to see the real crawl result (item r0 present), got %d items: %+v", len(items), items)
	}
}

func TestCatalogConcurrentColdRequestsShareOneCrawlAndHonourCancellation(t *testing.T) {
	var hits int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		<-release // block the whole crawl until the test says go
		json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{}})
	}))
	defer srv.Close()
	d := &Deps{BazaarBaseURL: srv.URL}

	// A caller whose own context is cancelled must not wait for the shared
	// crawl to finish — it should return as soon as its ctx is done, freeing
	// its handler goroutine/connection instead of blocking for the full
	// crawl duration alongside every other waiter.
	cancelledCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := d.catalog(cancelledCtx)
		done <- err
	}()
	// Give the first goroutine time to actually start the crawl (acquire
	// inflight) before a second concurrent caller arrives.
	time.Sleep(20 * time.Millisecond)

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.catalog(context.Background())
		}()
	}

	cancel()
	select {
	case err := <-done:
		if err == nil || !errors.Is(err, context.Canceled) {
			t.Errorf("cancelled caller returned err=%v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled caller did not return promptly — it's blocking on the shared crawl")
	}

	close(release)
	wg.Wait()

	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("upstream hit %d times across 4 concurrent cold-start callers, want exactly 1 shared crawl", got)
	}
}

func TestBazaarResourcesUpstreamFailureIsBadGateway(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	d := &Deps{BazaarBaseURL: srv.URL}

	rec := httptest.NewRecorder()
	d.BazaarResources(rec, httptest.NewRequest(http.MethodGet, "/bazaar/resources", nil))
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rec.Code)
	}
}
