// Package bazaar mirrors GoPlausible's public x402 discovery catalog into a
// shape AgentMesh's canvas can consume directly.
//
// The upstream catalog is a raw firehose: it carries entries for networks we
// cannot pay (EVM, Solana), and entries pointing at hosts that can never be
// reached from a server (localhost, RFC1918). Both are filtered here rather
// than in the handler, so every consumer of a Resource can assume it names a
// publicly-resolvable Algorand endpoint our relay is capable of paying.
package bazaar

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/agentmesh/backend/internal/netutil"
)

// AlgorandMainnet and AlgorandTestnet are the CAIP-2 network ids the catalog
// uses. Kept as literals rather than derived from config: this is a property
// of the Algorand chain itself, not of our deployment.
const (
	AlgorandMainnet = "algorand:wGHE2Pwdvd7S12BL5FaOP20EGYesN73ktiC1qzkkit8="
	AlgorandTestnet = "algorand:SGO1GKSzyE7IEPItTxCByw9x8FmnrCDexi9/cOUJOiI="
)

// pageLimit is the upstream's max page size. Paging stops on the first short
// page rather than trusting pagination.total, which a mid-crawl catalog can
// report inconsistently.
const pageLimit = 100

// maxPages bounds a runaway crawl. The live catalog is ~780 entries; 40 pages
// (4000 entries) is far above that while still terminating if the upstream
// ever returns a full page forever.
const maxPages = 40

// Param is one caller-supplied input a resource declares for itself.
type Param struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
}

// Resource is one catalog entry, normalised. AmountMicros is integer atomic
// USDC (6 decimals) — never a float, so a price can round-trip exactly.
type Resource struct {
	ID            string  `json:"id"`
	URL           string  `json:"url"`
	Method        string  `json:"method"`
	Description   string  `json:"description"`
	MerchantID    string  `json:"merchantId"`
	Network       string  `json:"network"`
	Testnet       bool    `json:"testnet"`
	AmountMicros  int64   `json:"amountMicros"`
	Asset         string  `json:"asset"`
	PayTo         string  `json:"payTo"`
	Params        []Param `json:"params"`
	OutputExample string  `json:"outputExample,omitempty"`
	SettleCount   int     `json:"settleCount"`
	LastSeen      string  `json:"lastSeen,omitempty"`
	Host          string  `json:"host"`

	// Supported and Curated are filled in by curated.go, not by the fetch.
	Supported bool   `json:"supported"`
	Provider  string `json:"provider,omitempty"`

	// Console marks an entry as backed by a dedicated console page rather
	// than by a canvas node, and is the ONLY signal the frontend uses to
	// decide which of the two an "Add"/"Open" button does.
	//
	// It is deliberately a provider key ("tendril", "prism") and never a
	// URL: the frontend maps it to a route it already owns, so a catalog
	// entry — which mirrors data from an external registry — can never
	// redirect a user anywhere. Empty means the ordinary canvas-node path.
	//
	// Entries sharing a Console key are one product with several endpoints;
	// the Bazaar collapses them into a single card that opens that console
	// (four "Prism" cards all opening the same page would be four buttons
	// for one destination).
	Console string `json:"console,omitempty"`
}

// rawResource mirrors the upstream JSON exactly. Kept separate from Resource
// so the wire shape can drift without changing what the frontend sees.
type rawResource struct {
	ID          string `json:"id"`
	ResourceURL string `json:"resourceUrl"`
	Method      string `json:"method"`
	Description string `json:"description"`
	MerchantID  string `json:"merchantId"`
	Accepts     []struct {
		Network string `json:"network"`
		Amount  string `json:"amount"`
		Asset   string `json:"asset"`
		PayTo   string `json:"payTo"`
	} `json:"accepts"`
	DiscoveryInfo struct {
		Input struct {
			Method      string         `json:"method"`
			QueryParams map[string]any `json:"queryParams"`
			Body        map[string]any `json:"body"`
		} `json:"input"`
		Output struct {
			Example any `json:"example"`
		} `json:"output"`
	} `json:"discoveryInfo"`
	SettleCount int    `json:"settleCount"`
	LastSeen    string `json:"lastSeen"`
}

// FetchAll pages the whole catalog and returns only the resources AgentMesh
// can actually pay and reach.
//
// Paging is strictly sequential -- one real client.Do call at a time, not
// concurrent. A concurrent-batch version of this was tried and reverted: it
// cuts wall-clock time, but pages routinely land past the real end of the
// catalog (we don't know a page is short until its response arrives, so a
// batch's later pages are already in flight by then) — a query shape the
// upstream may not tolerate the same way as an in-bounds one. Rather than
// gamble on unverified upstream behavior for an out-of-range offset,
// runCatalogFetch's caller-side timeout is sized generously instead (see its
// own doc comment) — slower, but only ever asks for offsets a real page
// count justifies.
func FetchAll(ctx context.Context, client *http.Client, baseURL string) ([]Resource, error) {
	var out []Resource
	seen := map[string]bool{}
	for page := 0; page < maxPages; page++ {
		u := fmt.Sprintf("%s/discovery/resources?limit=%d&offset=%d",
			strings.TrimRight(baseURL, "/"), pageLimit, page*pageLimit)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, fmt.Errorf("bazaar: build request: %w", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("bazaar: request page %d: %w", page, err)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("bazaar: upstream returned %d", resp.StatusCode)
		}
		if readErr != nil {
			return nil, fmt.Errorf("bazaar: read page %d body: %w", page, readErr)
		}
		var envelope struct {
			Items []rawResource `json:"items"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			return nil, fmt.Errorf("bazaar: unmarshal page %d: %w", page, err)
		}
		for _, raw := range envelope.Items {
			r, ok := normalise(raw)
			if !ok || seen[r.ID] {
				continue
			}
			seen[r.ID] = true
			out = append(out, r)
		}
		if len(envelope.Items) < pageLimit {
			break
		}
	}
	// Most-settled first: settle count is the only real popularity signal the
	// catalog carries, and it keeps a single publisher's 500 unused entries
	// from burying everything else on the first screen.
	sort.SliceStable(out, func(i, j int) bool { return out[i].SettleCount > out[j].SettleCount })
	return out, nil
}

// normalise converts one upstream entry, reporting false if it must be dropped.
func normalise(raw rawResource) (Resource, bool) {
	if len(raw.Accepts) == 0 {
		return Resource{}, false
	}
	a := raw.Accepts[0]
	parsed, _ := url.Parse(raw.ResourceURL)
	if !keepResource(parsed, a.Network) {
		return Resource{}, false
	}
	amount, err := strconv.ParseInt(a.Amount, 10, 64)
	if err != nil || amount < 0 {
		return Resource{}, false
	}
	method := raw.Method
	if method == "" {
		method = raw.DiscoveryInfo.Input.Method
	}
	if method == "" {
		method = http.MethodGet
	}
	res := Resource{
		ID:           raw.ID,
		URL:          raw.ResourceURL,
		Method:       strings.ToUpper(method),
		Description:  truncateRunes(raw.Description, resourceDescriptionMax),
		MerchantID:   raw.MerchantID,
		Network:      a.Network,
		Testnet:      a.Network == AlgorandTestnet,
		AmountMicros: amount,
		Asset:        a.Asset,
		PayTo:        a.PayTo,
		SettleCount:  raw.SettleCount,
		LastSeen:     raw.LastSeen,
		Host:         parsed.Hostname(),
		Params:       paramsFrom(raw),
	}
	if res.Params == nil {
		// paramsFrom returns nil (not an empty slice) when an entry declares no
		// inputs. A nil slice marshals as JSON null, which crashes every
		// frontend consumer that iterates/dereferences `params` unguarded.
		res.Params = []Param{}
	}
	if ex := raw.DiscoveryInfo.Output.Example; ex != nil {
		if b, err := json.Marshal(ex); err == nil && len(b) <= 4096 {
			res.OutputExample = string(b)
		}
	}
	return res, true
}

// paramDescriptionMax bounds one param's example-value description, the same
// way OutputExample is bounded at 4096 bytes above. Without a cap, a catalog
// entry with a large/nested example value (arrays/objects formatted via
// fmt's %v) flows unbounded into resourceToNode's base64url-encoded `?add=`
// query param on the frontend, risking a truncated or rejected request.
const paramDescriptionMax = 500

// resourceDescriptionMax bounds a resource's own free-text description,
// same rationale as paramDescriptionMax: this is an externally-writable,
// permissionless catalog field that flows into the same `?add=` payload.
const resourceDescriptionMax = 500

// truncateRunes bounds s to max runes, appending an ellipsis if it had to cut.
func truncateRunes(s string, max int) string {
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

// paramsFrom turns a catalog entry's declared inputs into Params. Query params
// win over body params when both are present, matching the precedence in
// nodes.ParamsFromChallenge.
//
// The catalog's values are EXAMPLES, not usable values (canix402 publishes the
// Algorand zero address), so they land in Description and never in a default.
func paramsFrom(raw rawResource) []Param {
	src := raw.DiscoveryInfo.Input.QueryParams
	if len(src) == 0 {
		src = raw.DiscoveryInfo.Input.Body
	}
	if len(src) == 0 {
		return nil
	}
	names := make([]string, 0, len(src))
	for k := range src {
		names = append(names, k)
	}
	// Map order is randomised; these render as an ordered form that must not
	// reshuffle between two reads of the same entry.
	sort.Strings(names)
	out := make([]Param, 0, len(names))
	for _, n := range names {
		desc := truncateRunes(fmt.Sprintf("example: %v", src[n]), paramDescriptionMax)
		out = append(out, Param{
			Name:        n,
			Type:        "string",
			Required:    false,
			Description: desc,
		})
	}
	return out
}

// keepResource reports whether a catalog entry is one we could actually pay
// and reach: an Algorand network, over https, at a public hostname.
func keepResource(u *url.URL, network string) bool {
	if network != AlgorandMainnet && network != AlgorandTestnet {
		return false
	}
	if u == nil || u.Scheme != "https" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".local") {
		return false
	}
	// Shares netutil.IsPrivateIP (the same check the real payment dial
	// enforces at connect time) rather than a narrower, independently
	// maintained list -- a catalog entry that passes this but would be
	// rejected at dial time is a confusing dead end: it looks pickable in
	// the Bazaar UI but always fails once added.
	if ip := net.ParseIP(host); ip != nil {
		if netutil.IsPrivateIP(ip) {
			return false
		}
	}
	return true
}
