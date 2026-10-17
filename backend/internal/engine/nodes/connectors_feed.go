package nodes

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/agentmesh/backend/internal/models"
)

// rssFeed is the subset of RSS 2.0 this node reads. Atom is not handled — a
// separate element tree — and is left for a follow-up rather than
// half-supported here.
type rssFeed struct {
	XMLName xml.Name `xml:"rss"`
	Channel struct {
		Title string `xml:"title"`
		Items []struct {
			Title       string `xml:"title"`
			Link        string `xml:"link"`
			PubDate     string `xml:"pubDate"`
			Description string `xml:"description"`
			GUID        string `xml:"guid"`
		} `xml:"item"`
	} `xml:"channel"`
}

// feedDefaultLimit is the default item cap for every feed-style connector
// (RSS, HackerNews) that caps how many results it returns.
const feedDefaultLimit = 10

// parsePositiveLimit reads a positive-integer limit from node.Config[key],
// falling back to def when unset. Shared by every feed-style connector that
// caps how many results it returns, so the "must be a positive number" rule
// and its error wording can't drift between them.
func parsePositiveLimit(node models.WorkflowNode, key string, def int) (int, error) {
	raw := configVal(node, key, "")
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("`%s` %q is not a positive number", key, raw)
	}
	return n, nil
}

// fetchRSS reads an RSS 2.0 feed and returns its items as structured data.
// Unlike every other connector this returns a value rather than a sentinel —
// downstream nodes address it with {{ node.<id>.title }}.
func fetchRSS(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	feedURL := resolveTemplate(configVal(node, "rssURL", ""), rc)
	if feedURL == "" {
		return "rss_skipped_no_url", ErrActionSkipped
	}
	limit, err := parsePositiveLimit(node, "rssLimit", feedDefaultLimit)
	if err != nil {
		return nil, fmt.Errorf("rss: %w", err)
	}

	body, err := getRaw(ctx, feedURL, map[string]string{"Accept": "application/rss+xml, application/xml, text/xml"}, "RSS")
	if err != nil {
		return nil, err
	}

	var feed rssFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("rss: could not parse the feed: %w", err)
	}
	if feed.Channel.Title == "" && len(feed.Channel.Items) == 0 {
		return nil, errors.New("rss: the response is not an RSS 2.0 feed (no channel title or items found)")
	}

	items := make([]map[string]any, 0, limit)
	for i, it := range feed.Channel.Items {
		if i >= limit {
			break
		}
		items = append(items, map[string]any{
			"title":       it.Title,
			"link":        it.Link,
			"pubDate":     it.PubDate,
			"description": it.Description,
			"guid":        it.GUID,
		})
	}
	return map[string]any{
		"title": feed.Channel.Title,
		"count": len(items),
		"items": items,
	}, nil
}

// SetHackerNewsAPIBaseForTest overrides the API base. Pass "" to restore the default.
func SetHackerNewsAPIBaseForTest(base string) {
	setAPIBaseForTest("hackernews", base)
}

// fetchHackerNews searches Hacker News. No credential of any kind.
func fetchHackerNews(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	query := resolveTemplate(configVal(node, "hnQuery", ""), rc)
	if query == "" {
		return "hackernews_skipped_no_query", ErrActionSkipped
	}
	q := url.Values{}
	q.Set("query", query)
	q.Set("tags", configVal(node, "hnTags", "story"))
	target := apiBase("hackernews") + "/search?" + q.Encode()

	raw, err := getAndDecode(ctx, target, nil, "HackerNews")
	if err != nil {
		return nil, err
	}
	body, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("hackernews: unexpected response shape %T", raw)
	}
	hits, _ := body["hits"].([]any)

	limit, err := parsePositiveLimit(node, "hnLimit", feedDefaultLimit)
	if err != nil {
		return nil, fmt.Errorf("hackernews: %w", err)
	}

	items := make([]map[string]any, 0, limit)
	for i, h := range hits {
		if i >= limit {
			break
		}
		hit, ok := h.(map[string]any)
		if !ok {
			continue
		}
		id, _ := hit["objectID"].(string)
		items = append(items, map[string]any{
			"title":   hit["title"],
			"url":     hit["url"],
			"points":  hit["points"],
			"author":  hit["author"],
			"hnURL":   "https://news.ycombinator.com/item?id=" + id,
			"created": hit["created_at"],
		})
	}
	return map[string]any{"count": len(items), "items": items}, nil
}

// SetCoinGeckoAPIBaseForTest overrides the API base. Pass "" to restore the default.
func SetCoinGeckoAPIBaseForTest(base string) {
	resetCoinGeckoCache()
	setAPIBaseForTest("coingecko", base)
}

// fetchCoinGecko returns spot prices for the configured coin ids. No key.
func fetchCoinGecko(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	ids := resolveTemplate(configVal(node, "cgIDs", ""), rc)
	if ids == "" {
		return "coingecko_skipped_no_ids", ErrActionSkipped
	}
	currencies := configVal(node, "cgCurrencies", "usd")
	q := url.Values{}
	q.Set("ids", ids)
	q.Set("vs_currencies", currencies)
	out, err := coinGeckoGet(ctx, apiBase("coingecko")+"/simple/price?"+q.Encode())
	if err == nil || ctx.Err() != nil {
		return out, err
	}
	// CoinGecko refused or failed; see coinfallback.go.
	if fb, ok := spotFallback(ctx, ids, currencies); ok {
		logPriceFallback("spot "+ids+" from "+fmt.Sprint(fb["source"]), err)
		return fb, nil
	}
	return nil, err
}

// maxHistoryPointsReturned bounds what goes downstream. market_chart at 28
// days is hourly, so 672 points -- far more than an agent can use and enough
// to crowd a model's context on its own. The summary fields are what a report
// actually needs; the points are for a chart.
const maxHistoryPointsReturned = 200

// fetchCoinGeckoHistory returns a price series for one coin, with the summary
// an agent would otherwise have to compute from it.
//
// high, low and changePct are computed here on purpose, and from every point,
// not only the ones passed on. market_chart returns hourly points, and an
// agent asked to find a 28-day high from 672 of them gets it wrong -- a real
// report quoted a high from a date two years outside the window it was asked
// about.
func fetchCoinGeckoHistory(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	id := strings.TrimSpace(resolveTemplate(configVal(node, "cgID", ""), rc))
	if id == "" {
		return "coingecko_history_skipped_no_id", ErrActionSkipped
	}
	days := strings.TrimSpace(configVal(node, "cgDays", "30"))
	currency := strings.TrimSpace(configVal(node, "cgCurrency", "usd"))

	q := url.Values{}
	q.Set("vs_currency", currency)
	q.Set("days", days)
	source := "coingecko"
	raw, err := coinGeckoGet(ctx, apiBase("coingecko")+"/coins/"+url.PathEscape(id)+"/market_chart?"+q.Encode())
	if err != nil {
		if ctx.Err() != nil {
			return nil, err
		}
		// CoinGecko refused or failed; see coinfallback.go.
		fb, ok := historyFallback(ctx, id, currency, days, time.Now())
		if !ok {
			return nil, err
		}
		logPriceFallback("history "+id+" from coinbase", err)
		raw, source = fb, "coinbase"
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("CoinGecko: %w", err)
	}
	var body struct {
		Prices [][]float64 `json:"prices"`
	}
	if err := json.Unmarshal(b, &body); err != nil {
		return nil, fmt.Errorf("CoinGecko: unexpected market_chart shape: %w", err)
	}
	// A point is [unix millis, price]; anything shorter is dropped before the
	// summary rather than indexed into.
	prices := body.Prices[:0]
	for _, p := range body.Prices {
		if len(p) >= 2 {
			prices = append(prices, p)
		}
	}
	if len(prices) == 0 {
		return "coingecko_history_skipped_no_data", ErrActionSkipped
	}

	first, last := prices[0][1], prices[len(prices)-1][1]
	high, low := first, first
	// Rounded up, so the kept points never exceed the cap.
	step := (len(prices) + maxHistoryPointsReturned - 1) / maxHistoryPointsReturned
	points := make([]map[string]any, 0, (len(prices)+step-1)/step)
	for i, p := range prices {
		high = max(high, p[1])
		low = min(low, p[1])
		if i%step == 0 {
			points = append(points, map[string]any{
				"time":  time.UnixMilli(int64(p[0])).UTC().Format(time.RFC3339),
				"price": p[1],
			})
		}
	}
	out := map[string]any{
		"id": id, "currency": currency, "days": days,
		"first": first, "last": last, "high": high, "low": low,
		"points": points,
	}
	if source != "coingecko" {
		// Only on a backup answer, so a normal one is unchanged.
		out["source"] = source
		out["note"] = "CoinGecko was unavailable, so this history comes from Coinbase daily or hourly closing prices."
	}
	// Left absent rather than zero when it cannot be computed: a reported
	// "0% change" against a zero first price is a statement, and a wrong one.
	if first != 0 {
		out["changePct"] = (last - first) / first * 100
	}
	return out, nil
}
