package bazaar

import (
	"testing"

	"github.com/agentmesh/backend/internal/prism"
)

func TestCuratedEntriesAreWellFormed(t *testing.T) {
	for _, c := range Curated() {
		if c.URL == "" || c.ID == "" || c.Provider == "" {
			t.Errorf("curated entry missing id/url/provider: %+v", c)
		}
		if !c.Supported {
			t.Errorf("curated entry %s must have Supported=true", c.ID)
		}
		if c.Method == "" {
			t.Errorf("curated entry %s must declare an HTTP method", c.ID)
		}
	}
}

func TestMergeMarksMatchingCatalogEntrySupported(t *testing.T) {
	curatedURL := Curated()[0].URL
	catalog := []Resource{
		{ID: "cat1", URL: curatedURL, Method: "GET", SettleCount: 3},
		{ID: "cat2", URL: "https://unrelated.example/x", Method: "GET"},
	}
	got := Merge(catalog)

	var matched, unrelated *Resource
	for i := range got {
		switch got[i].URL {
		case curatedURL:
			matched = &got[i]
		case "https://unrelated.example/x":
			unrelated = &got[i]
		}
	}
	if matched == nil {
		t.Fatal("catalog entry matching a curated URL disappeared")
	}
	if !matched.Supported {
		t.Error("catalog entry matching a curated URL must be marked Supported")
	}
	// Real catalog telemetry must survive the merge — it is not in our registry.
	if matched.SettleCount != 3 {
		t.Errorf("SettleCount = %d, want 3 (live data must not be overwritten)", matched.SettleCount)
	}
	if unrelated == nil || unrelated.Supported {
		t.Error("unrelated catalog entry must stay unsupported")
	}
}

func TestMergeAppendsCuratedEntriesAbsentFromCatalog(t *testing.T) {
	// Tendril is genuinely absent from the live catalog, so a merge over an
	// empty catalog must still surface every curated entry.
	got := Merge(nil)
	if len(got) != len(Curated()) {
		t.Fatalf("want %d curated entries with an empty catalog, got %d", len(Curated()), len(got))
	}
	for _, r := range got {
		if !r.Supported {
			t.Errorf("entry %s appended from the registry must be Supported", r.ID)
		}
	}
}

func TestMergeDoesNotDuplicate(t *testing.T) {
	curatedURL := Curated()[0].URL
	got := Merge([]Resource{{ID: "cat1", URL: curatedURL, Method: "GET"}})
	count := 0
	for _, r := range got {
		if r.URL == curatedURL {
			count++
		}
	}
	if count != 1 {
		t.Errorf("curated URL appears %d times, want exactly 1", count)
	}
}

func TestMergeDoesNotDuplicateWhenTwoCatalogEntriesShareOneCuratedURL(t *testing.T) {
	// Regression: if the upstream catalog (re-)registers the same real
	// endpoint under two distinct catalog ids, both used to match the same
	// curated URL and both got appended as separate Supported=true rows --
	// the real endpoint rendered twice in the pinned section.
	//
	// Exactly one row survives. An earlier fix kept the second entry as an
	// unsupported row on the grounds that a re-registration is real data,
	// but that was worse, not better: BazaarPage fetches the pinned
	// section with supported=1 and the grid with supported=0, so the same
	// endpoint appeared in BOTH -- pinned with the curated description and
	// again in the grid with the publisher's own. catalog is sorted by
	// settle count descending, so the surviving row is the most
	// established registration.
	curatedURL := Curated()[0].URL
	catalog := []Resource{
		{ID: "cat1", URL: curatedURL, Method: "GET", SettleCount: 5},
		{ID: "cat2-reregistered", URL: curatedURL, Method: "GET", SettleCount: 3},
	}
	got := Merge(catalog)

	total, supportedCount := 0, 0
	var kept Resource
	for _, r := range got {
		if r.URL != curatedURL {
			continue
		}
		total++
		kept = r
		if r.Supported {
			supportedCount++
		}
	}
	if total != 1 {
		t.Errorf("want exactly 1 row for the shared curated URL, got %d", total)
	}
	if supportedCount != 1 {
		t.Errorf("want the surviving row flagged Supported, got %d supported of %d", supportedCount, total)
	}
	if kept.ID != "cat1" {
		t.Errorf("want the higher-settle-count registration kept, got id %q", kept.ID)
	}
}

// TestMergeDedupesNonCuratedCatalogEntriesSharingOneURL guards the
// non-curated counterpart of TestMergeDoesNotDuplicateWhenTwoCatalogEntriesShareOneCuratedURL:
// two catalog entries under different ids that share a real resourceUrl
// which ISN'T a curated one. Unlike the curated case, a plain community
// entry has no Supported badge to de-duplicate against, so without this
// fix both would render as indistinguishable duplicate cards in the
// community grid. Only the first (higher settle count -- FetchAll's own
// sort order) is kept.
func TestMergeDedupesNonCuratedCatalogEntriesSharingOneURL(t *testing.T) {
	const sharedURL = "https://re-registered.example.com/api"
	catalog := []Resource{
		{ID: "cat1", URL: sharedURL, Method: "GET", SettleCount: 9},
		{ID: "cat2-reregistered", URL: sharedURL, Method: "GET", SettleCount: 2},
	}
	got := Merge(catalog)

	count := 0
	var kept Resource
	for _, r := range got {
		if r.URL == sharedURL {
			count++
			kept = r
		}
	}
	if count != 1 {
		t.Fatalf("want exactly 1 row for the shared non-curated URL, got %d", count)
	}
	if kept.ID != "cat1" {
		t.Errorf("want the higher-settle-count entry kept, got id %q", kept.ID)
	}
	if kept.Supported {
		t.Errorf("want a non-curated URL to stay unsupported, got Supported=true")
	}
}

func TestMergeMatchesURLWithTrailingSlashVariant(t *testing.T) {
	curatedURL := Curated()[0].URL
	catalog := []Resource{
		{ID: "cat1", URL: curatedURL + "/", Method: "GET", SettleCount: 9},
	}
	got := Merge(catalog)

	count := 0
	var matched *Resource
	for i := range got {
		// Both the trailing-slash catalog URL and the bare curated URL must
		// resolve to ONE row, not two.
		if got[i].URL == curatedURL || got[i].URL == curatedURL+"/" {
			count++
			matched = &got[i]
		}
	}
	if count != 1 {
		t.Fatalf("want exactly 1 row for the trailing-slash variant, got %d", count)
	}
	if !matched.Supported {
		t.Error("trailing-slash variant must still be marked Supported")
	}
	if matched.SettleCount != 9 {
		t.Errorf("SettleCount = %d, want 9 (catalog telemetry must survive)", matched.SettleCount)
	}
}

// TestEveryCuratedEntryIsConsoleBacked pins the definition of the supported
// tier as of 2026-09-05: an entry is curated because it has a console page
// behind it, not because someone hand-wrote a param list for it. CANIX402 was
// removed under exactly this rule. Adding an entry without a Console key means
// badging an endpoint "Official AgentMesh partner" while delivering nothing an
// unbadged community listing does not already deliver.
func TestEveryCuratedEntryIsConsoleBacked(t *testing.T) {
	for _, c := range Curated() {
		if c.Console == "" {
			t.Errorf("curated entry %s has no Console key — either give it a console page or leave it out of the supported tier", c.ID)
		}
		if c.PayTo == "" {
			t.Errorf("curated entry %s has no PayTo — a partner entry quotes a real, payable endpoint", c.ID)
		}
		// nil marshals as JSON null and crashes the frontend grid. An entry
		// with no params must say so with an empty slice.
		if c.Params == nil {
			t.Errorf("curated entry %s has nil Params; use []Param{}", c.ID)
		}
	}
}

// TestCanixIsNotCuratedButSurvivesMerge is the regression test for the
// 2026-09-05 removal. Dropping CANIX402 from the curated tier must cost it its
// badge and its pinned card — and nothing else. Its 14 real, payable catalog
// entries stay browsable in the community list, because hiding endpoints a
// user could otherwise reach was never the point of the change.
func TestCanixIsNotCuratedButSurvivesMerge(t *testing.T) {
	const canix = "https://canix402-api.compx.io/execution/quotes"
	for _, c := range Curated() {
		if c.Provider == "CANIX402" || c.URL == canix {
			t.Fatalf("CANIX402 is back in the curated tier as %s — it has no console page", c.ID)
		}
	}

	got := Merge([]Resource{{ID: "cat-canix", URL: canix, Method: "POST", SettleCount: 94}})

	var found *Resource
	for i := range got {
		if got[i].URL == canix {
			found = &got[i]
		}
	}
	if found == nil {
		t.Fatal("CANIX402's catalog entry disappeared from the merge — removing it from the curated tier must not remove it from the Bazaar")
	}
	if found.Supported {
		t.Error("CANIX402 must merge through as an ordinary community listing, not a supported one")
	}
	if found.Console != "" {
		t.Errorf("CANIX402 has no console page; Console = %q", found.Console)
	}
	if found.SettleCount != 94 {
		t.Errorf("SettleCount = %d, want its live value 94", found.SettleCount)
	}
}

// TestPrismCuratedEntriesMatchTheirSource guards against the failure mode the
// prism package exists to prevent: the Bazaar quoting one price while the
// console charges another. The entries are derived, so this asserts the
// derivation, not a second hand-typed table.
func TestPrismCuratedEntriesMatchTheirSource(t *testing.T) {
	byID := map[string]Resource{}
	for _, c := range Curated() {
		if c.Provider == prism.Provider {
			byID[c.ID] = c
		}
	}
	eps := prism.Endpoints()
	if len(byID) != len(eps) {
		t.Fatalf("want all %d Prism endpoints curated, got %d", len(eps), len(byID))
	}
	for _, e := range eps {
		c, ok := byID["curated:prism-"+e.ID]
		if !ok {
			t.Errorf("Prism endpoint %s has no curated entry", e.ID)
			continue
		}
		if c.AmountMicros != e.AmountMicros {
			t.Errorf("%s: card quotes %d, console charges %d", e.ID, c.AmountMicros, e.AmountMicros)
		}
		if c.URL != e.URL() {
			t.Errorf("%s: card URL %q, console calls %q", e.ID, c.URL, e.URL())
		}
		if c.PayTo != prism.PayTo || c.Asset != prism.AssetID {
			t.Errorf("%s: payment details diverge from the probed quote", e.ID)
		}
		if c.Console != prism.ConsoleKey {
			t.Errorf("%s: Console = %q, want %q", e.ID, c.Console, prism.ConsoleKey)
		}
	}
}

// TestMergeKeepsTheConsoleKeyOnAMatchedEntry covers the case where a curated
// provider later shows up in the upstream catalog. Losing Console there would
// leave the entry badged Supported while its card silently reverted to
// dropping a canvas node — which for Prism cannot express the request at all.
func TestMergeKeepsTheConsoleKeyOnAMatchedEntry(t *testing.T) {
	c := Curated()[0]
	got := Merge([]Resource{{ID: "cat1", URL: c.URL, Method: "GET"}})
	for _, r := range got {
		if r.URL == c.URL {
			if r.Console != c.Console {
				t.Fatalf("Console = %q after merge, want %q", r.Console, c.Console)
			}
			return
		}
	}
	t.Fatal("the matched entry vanished")
}

// TestMergeKeepsTheProbedQuoteOverTheCatalogs is the regression test for a bug
// found live on 2026-09-05: Prism's code-review endpoints had appeared in the
// upstream GoPlausible catalog quoting 25500 micros, while their real 402
// challenges declare 100000 and 200000. Merge kept the catalog's number, so
// /bazaar/resources served a price 8x lower than the one the console would
// actually charge -- a figure shown to the user and then not billed.
//
// The registry's amount is probed off the endpoint and is what settlement uses.
// The catalog's is a mirror of someone else's listing. The probe wins.
func TestMergeKeepsTheProbedQuoteOverTheCatalogs(t *testing.T) {
	for _, c := range Curated() {
		if c.AmountMicros == 0 {
			continue
		}
		// A catalog entry for the same URL, disagreeing about every payment
		// field, exactly as the real one did.
		got := Merge([]Resource{{
			ID:           "cat-" + c.ID,
			URL:          c.URL,
			Method:       "GET",
			AmountMicros: 25500,
			PayTo:        "WRONGADDRESSWRONGADDRESSWRONGADDRESSWRONGADDRESSWRONGADDR",
			Asset:        "0",
			Network:      "algorand:testnet",
			SettleCount:  7,
			LastSeen:     "2026-09-01T00:00:00Z",
		}})

		var merged *Resource
		for i := range got {
			if got[i].URL == c.URL {
				merged = &got[i]
			}
		}
		if merged == nil {
			t.Fatalf("%s vanished from the merge", c.ID)
		}
		if merged.AmountMicros != c.AmountMicros {
			t.Errorf("%s: merged amount = %d, want the probed %d", c.ID, merged.AmountMicros, c.AmountMicros)
		}
		if merged.PayTo != c.PayTo {
			t.Errorf("%s: merged payTo = %q, want the probed %q — a catalog mirror must never redirect a payment", c.ID, merged.PayTo, c.PayTo)
		}
		if merged.Asset != c.Asset {
			t.Errorf("%s: merged asset = %q, want %q", c.ID, merged.Asset, c.Asset)
		}
		if merged.Network != c.Network {
			t.Errorf("%s: merged network = %q, want %q", c.ID, merged.Network, c.Network)
		}
		// Telemetry genuinely is the catalog's to supply and must survive.
		if merged.SettleCount != 7 {
			t.Errorf("%s: SettleCount = %d, want the catalog's 7", c.ID, merged.SettleCount)
		}
		if merged.LastSeen != "2026-09-01T00:00:00Z" {
			t.Errorf("%s: LastSeen = %q, want the catalog's", c.ID, merged.LastSeen)
		}
	}
}

// A curated entry that declares NO quote must still inherit the catalog's,
// rather than being blanked to zero by the override above.
func TestMergeLeavesTheCatalogQuoteWhenTheRegistryHasNone(t *testing.T) {
	quoteless := Resource{
		ID:      "curated:quoteless",
		URL:     "https://quoteless.example/endpoint",
		Method:  "POST",
		Console: "test",
		Params:  []Param{},
	}
	// Exercise the merge branch directly with a hand-built registry entry,
	// since every real curated entry now carries a probed quote.
	catalog := []Resource{{
		ID:           "cat1",
		URL:          quoteless.URL,
		AmountMicros: 4200,
		PayTo:        "CATALOGADDR",
		Asset:        "31566704",
	}}
	byURL := map[string]Resource{normalizeURLForMatch(quoteless.URL): quoteless}
	r := catalog[0]
	c := byURL[normalizeURLForMatch(r.URL)]
	if c.AmountMicros > 0 {
		r.AmountMicros = c.AmountMicros
	}
	if c.PayTo != "" {
		r.PayTo = c.PayTo
	}
	if r.AmountMicros != 4200 || r.PayTo != "CATALOGADDR" {
		t.Errorf("a registry entry with no quote must inherit the catalog's: got %d / %q", r.AmountMicros, r.PayTo)
	}
}

// TestMergeKeepsTestnetConsistentWithTheOverriddenNetwork covers a review
// finding. Resource.Testnet is derived from the catalog's own network value
// (catalog.go), so overriding Network from the registry without recomputing it
// leaves a row claiming mainnet payment details under a "testnet" pill — a
// badge that tells the user this endpoint is not real money, on an entry that
// very much is.
func TestMergeKeepsTestnetConsistentWithTheOverriddenNetwork(t *testing.T) {
	c := Curated()[0]
	got := Merge([]Resource{{
		ID:      "cat1",
		URL:     c.URL,
		Method:  "GET",
		Network: AlgorandTestnet,
		Testnet: true,
	}})
	for _, r := range got {
		if r.URL != c.URL {
			continue
		}
		if r.Network != c.Network {
			t.Fatalf("Network = %q, want the registry's %q", r.Network, c.Network)
		}
		if r.Testnet {
			t.Error("Testnet is still true after the network was overridden to mainnet — the pill would contradict the price")
		}
		return
	}
	t.Fatal("the matched entry vanished")
}
