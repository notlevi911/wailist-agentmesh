package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// CoinMatch is one candidate from CoinGecko's own search. Rank is market cap
// rank, and zero means unranked -- which is itself a signal, since a token
// nobody trades is rarely the one the user meant.
type CoinMatch struct {
	ID     string `json:"id"`
	Symbol string `json:"symbol"`
	Name   string `json:"name"`
	Rank   int    `json:"marketCapRank,omitempty"`
}

// maxCoinMatches bounds what goes back to the model. CoinGecko returns every
// token whose name contains the query, and for a common word that is dozens.
const maxCoinMatches = 8

// resolveCoin asks CoinGecko which coins match a name or symbol.
//
// An empty result is a successful answer, not an error: "this token is not
// listed on CoinGecko" is exactly what the builder needs to be able to tell
// the user, and the alternative -- guessing an id from the name -- produced a
// 404 and then a confident report about an entirely different asset.
func resolveCoin(ctx context.Context, query string) ([]CoinMatch, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("resolve_coin: a query is required")
	}
	q := url.Values{}
	q.Set("query", query)
	raw, err := coinGeckoGet(ctx, apiBase("coingecko")+"/search?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("resolve_coin: %w", err)
	}
	// getAndDecode returns the decoded body as any; re-encode and decode into
	// the shape we want rather than walking map[string]any by hand, which is
	// where a silent type assertion failure would turn into an empty result
	// indistinguishable from "not listed".
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("resolve_coin: %w", err)
	}
	var body struct {
		Coins []struct {
			ID     string `json:"id"`
			Symbol string `json:"symbol"`
			Name   string `json:"name"`
			Rank   int    `json:"market_cap_rank"`
		} `json:"coins"`
	}
	if err := json.Unmarshal(b, &body); err != nil {
		return nil, fmt.Errorf("resolve_coin: %w", err)
	}
	out := make([]CoinMatch, 0, min(len(body.Coins), maxCoinMatches))
	for _, c := range body.Coins {
		if len(out) >= maxCoinMatches {
			break
		}
		out = append(out, CoinMatch{ID: c.ID, Symbol: strings.ToUpper(c.Symbol), Name: c.Name, Rank: c.Rank})
	}
	return out, nil
}
