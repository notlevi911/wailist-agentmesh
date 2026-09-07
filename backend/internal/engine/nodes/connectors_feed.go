package nodes

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/url"
	"strconv"

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

// hackerNewsAPIBase is overridden in tests via SetHackerNewsAPIBaseForTest.
// This is Algolia's HN search API — one request returns matching stories,
// unlike the Firebase API which needs an N+1 fetch per item id. No auth.
var hackerNewsAPIBase = "https://hn.algolia.com/api/v1"

// SetHackerNewsAPIBaseForTest overrides the HN search API base URL. Call only
// from tests. Pass "" to reset to the real API.
func SetHackerNewsAPIBaseForTest(base string) {
	if base == "" {
		hackerNewsAPIBase = "https://hn.algolia.com/api/v1"
	} else {
		hackerNewsAPIBase = base
	}
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
	target := hackerNewsAPIBase + "/search?" + q.Encode()

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

// coinGeckoAPIBase is overridden in tests via SetCoinGeckoAPIBaseForTest.
// CoinGecko's /simple/price endpoint is usable without an API key.
var coinGeckoAPIBase = "https://api.coingecko.com/api/v3"

// SetCoinGeckoAPIBaseForTest overrides the CoinGecko API base URL. Call only
// from tests. Pass "" to reset to the real API.
func SetCoinGeckoAPIBaseForTest(base string) {
	if base == "" {
		coinGeckoAPIBase = "https://api.coingecko.com/api/v3"
	} else {
		coinGeckoAPIBase = base
	}
}

// fetchCoinGecko returns spot prices for the configured coin ids. No key.
func fetchCoinGecko(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	ids := resolveTemplate(configVal(node, "cgIDs", ""), rc)
	if ids == "" {
		return "coingecko_skipped_no_ids", ErrActionSkipped
	}
	q := url.Values{}
	q.Set("ids", ids)
	q.Set("vs_currencies", configVal(node, "cgCurrencies", "usd"))
	return getAndDecode(ctx, coinGeckoAPIBase+"/simple/price?"+q.Encode(), nil, "CoinGecko")
}
