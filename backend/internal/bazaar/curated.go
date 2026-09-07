package bazaar

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/agentmesh/backend/internal/prism"
)

// Curated is AgentMesh's own registry of officially-supported x402 providers.
//
// It is deliberately NOT derived from the GoPlausible catalog. Verified live
// 2026-08-03: of the launch providers, only CANIX402 appeared in
// /discovery/resources at all (14 entries) — Tendril and Prism returned zero
// matches across all 779 entries despite Tendril sitting at rank 4 on the
// challenge leaderboard. A "supported" list built by filtering catalog data
// would silently omit both.
//
// # What being curated means, as of 2026-09-05
//
// An entry here is a partner with a dedicated console page (see
// Resource.Console). That is now the definition of the supported tier, not a
// coincidence of which entries happen to have hand-authored params:
// TestEveryCuratedEntryIsConsoleBacked enforces it.
//
// The two members are Tendril and Prism. CANIX402 was removed from the tier
// on 2026-09-05 — it has no console, and a badge promising more than an
// endpoint URL was more than we were actually delivering for it. Removing it
// from this registry does NOT remove it from the Bazaar: its 14 real catalog
// entries flow through Merge untouched and stay browsable as community
// listings, which TestCanixIsNotCuratedButSurvivesMerge pins.
//
// # Prism's entries, and why they took until now
//
// This comment used to explain why Prism was absent, on two grounds. One is
// resolved and one is not:
//
//  1. RESOLVED — "no verified static payTo/amount". All four Prism endpoints
//     were probed live on 2026-09-05 and their challenges agreed exactly with
//     Prism's written spec. internal/prism holds the results and is the single
//     source of truth for them; the entries below are derived from it rather
//     than retyped, so the price a Bazaar card quotes is literally the value
//     the console charges. Guessing a payment address remains the hazard it
//     always was — see that package's doc comment.
//
//  2. STILL TRUE — "not expressible as labelled params". Prism's real body
//     needs a nested files array carrying base64 file content alongside a text
//     field (see TestBuildTargetRequestJSONBodyModeProducesPrismShape), while
//     Param and the frontend's discoveredParams pipeline only ever render a
//     flat list of plain-text inputs. There is still no file kind in either.
//
// The console is what resolves (2): it renders a purpose-built form per
// endpoint and builds the CustomParams and BodyTemplate server-side. So Prism
// is listed here with Params deliberately EMPTY and Console set — the entry
// advertises and prices the endpoints, and the console is where they are
// actually called. Exposing a file-taking endpoint as a plain canvas node
// still needs a file kind in discoveredParams first, which is a model change
// and not a registry addition.
func Curated() []Resource {
	out := []Resource{
		{
			ID:          "curated:tendril-run",
			URL:         "https://tendrilregister.007575.xyz/x402/run",
			Method:      http.MethodPost,
			Provider:    "Tendril",
			Host:        "tendrilregister.007575.xyz",
			Description: "Run a Python script on rented compute and get its stdout back. No lease needed — Tendril picks the machine, runs the job in a throwaway sandbox, and destroys it. Requires a positive Tendril credit balance on the paying wallet.",
			Network:     AlgorandMainnet,
			Asset:       "31566704",
			PayTo:       "ZIK7QQE7ZX446TW3PN7PQ5UDZNTY7JI5RYNTIU3LPEYBOSTVWI6PTNSWKI",
			// Flat gate fee only. Execution time is billed separately from a
			// Tendril-side credit balance keyed to the paying wallet address.
			AmountMicros: 10000,
			Supported:    true,
			Console:      "tendril",
			Params: []Param{{
				Name:        "payload",
				Type:        "string",
				Required:    true,
				Description: "Python source to execute. Its stdout is returned as `result`.",
			}},
		},
	}
	return append(out, prismCurated()...)
}

// prismCurated derives Prism's catalog entries from internal/prism, the same
// table the console builds and pays its real requests from. Derived, never
// retyped: a copy could drift, and a drifted price means the Bazaar quotes one
// number while the user is charged another.
func prismCurated() []Resource {
	eps := prism.Endpoints()
	out := make([]Resource, 0, len(eps))
	for _, e := range eps {
		out = append(out, Resource{
			ID:           "curated:prism-" + e.ID,
			URL:          e.URL(),
			Method:       e.Method,
			Provider:     prism.Provider,
			Host:         prism.Host,
			Description:  e.Description,
			Network:      prism.Network,
			Asset:        prism.AssetID,
			PayTo:        prism.PayTo,
			AmountMicros: e.AmountMicros,
			Supported:    true,
			Console:      prism.ConsoleKey,
			// Deliberately empty, and explicitly []Param{} rather than the nil
			// zero value — nil marshals as JSON null and crashes the frontend
			// grid. Empty is also the honest answer: Prism's inputs are not
			// expressible as flat params, which is why it has a console at
			// all. The console reads prism.Endpoints().Fields instead.
			Params: []Param{},
		})
	}
	return out
}

// normalizeURLForMatch makes two URLs comparable for Merge's purposes: it
// lowercases the scheme and host (case-insensitive per RFC 3986) and trims
// exactly one trailing slash from the path, so a catalog entry differing from
// a curated URL only by trailing-slash or scheme/host case still matches
// instead of silently producing a duplicate row for the same real endpoint.
// It is used only for comparison — Resource.URL itself is never rewritten.
func normalizeURLForMatch(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	if len(u.Path) > 1 {
		u.Path = strings.TrimSuffix(u.Path, "/")
	}
	return u.String()
}

// Merge annotates catalog entries that match a curated URL as Supported, and
// appends curated entries the catalog does not list at all.
//
// Live telemetry on a matched entry (settle counts, last seen) is preserved —
// the registry supplies curation, the catalog supplies facts.
func Merge(catalog []Resource) []Resource {
	curated := Curated()
	byURL := make(map[string]Resource, len(curated))
	for _, c := range curated {
		byURL[normalizeURLForMatch(c.URL)] = c
	}

	out := make([]Resource, 0, len(catalog)+len(curated))
	matched := make(map[string]bool, len(curated))
	// One row per real endpoint URL, curated or not. The upstream catalog
	// can list the same resourceUrl under two different ids (a
	// re-registration), and emitting both puts the same endpoint on the
	// page twice.
	//
	// Applied to curated matches too, not just community ones: there the
	// duplicate is actually worse. Only the first row gets Supported=true,
	// so the second is served under supported=0 -- and since BazaarPage
	// fetches the pinned section with supported=1 and the grid with
	// supported=0, one endpoint would appear in BOTH, pinned with the
	// curated description and again in the grid with the publisher's own.
	// That is precisely the "same card twice under contradictory copy"
	// case this whole merge exists to prevent, so treating curated
	// re-registrations as data worth keeping traded one duplicate for a
	// strictly more confusing one.
	//
	// catalog is sorted by settle count descending (FetchAll), so keeping
	// the first occurrence keeps the most established registration along
	// with its own id/SettleCount/LastSeen.
	seenURL := make(map[string]bool, len(catalog))
	for _, r := range catalog {
		key := normalizeURLForMatch(r.URL)
		if seenURL[key] {
			continue
		}
		seenURL[key] = true
		if c, ok := byURL[key]; ok {
			matched[key] = true
			r.Supported = true
			r.Provider = c.Provider
			// Console has to travel with the curation, not just the
			// provider name: without this, a Prism or Tendril endpoint
			// that later appears in the upstream catalog would be badged
			// Supported but lose its console key, and its Bazaar card
			// would silently fall back to dropping a canvas node the
			// endpoint's real input shape cannot be expressed in.
			r.Console = c.Console
			// The PAYMENT QUOTE comes from the registry, not the catalog,
			// whenever the registry declares one.
			//
			// This is the one place the "registry curates, catalog supplies
			// facts" split does not hold, and it was found the hard way:
			// Prism's code-review endpoints appeared in the upstream catalog
			// listing 25500 micros, while their live 402 challenges (probed
			// 2026-09-05) declare 100000 and 200000. Keeping the catalog's
			// number meant the Bazaar quoting $0.0255 for a call the console
			// charges $0.20 for -- a price the user is shown and then not
			// billed.
			//
			// Between a mirrored third-party listing and an amount we probed
			// off the endpoint itself and will settle against, the probe wins.
			// Only overwrite what the registry actually declares, so an entry
			// that carries no quote (a curated URL with no verified price)
			// still inherits the catalog's.
			//
			// Live telemetry -- SettleCount, LastSeen, the entry's own ID --
			// is untouched: that genuinely is the catalog's to supply.
			if c.AmountMicros > 0 {
				r.AmountMicros = c.AmountMicros
			}
			if c.PayTo != "" {
				r.PayTo = c.PayTo
			}
			if c.Asset != "" {
				r.Asset = c.Asset
			}
			if c.Network != "" {
				r.Network = c.Network
				// Testnet was derived from the CATALOG's network
				// (catalog.go:207), so overriding Network without it leaves the
				// two disagreeing: a curated entry that upstream happens to
				// list on testnet would merge out priced and payable on
				// mainnet while ResourceCard/EndpointRow still render a
				// "testnet" pill over it.
				r.Testnet = r.Network == AlgorandTestnet
			}
			// A hand-authored description and param set are strictly better
			// than the publisher's own, which is why the entry is curated.
			if c.Description != "" {
				r.Description = c.Description
			}
			if len(c.Params) > 0 {
				r.Params = c.Params
			}
			if c.Method != "" {
				r.Method = c.Method
			}
		}
		out = append(out, r)
	}
	for _, c := range curated {
		if !matched[normalizeURLForMatch(c.URL)] {
			out = append(out, c)
		}
	}
	return out
}
