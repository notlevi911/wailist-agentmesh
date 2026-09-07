package nodes_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/agentmesh/backend/internal/engine"
	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
)

const sampleRSS = `<?xml version="1.0"?>
<rss version="2.0"><channel>
  <title>Example Feed</title>
  <item><title>First post</title><link>https://example.com/1</link><pubDate>Mon, 04 Aug 2026 10:00:00 GMT</pubDate></item>
  <item><title>Second post</title><link>https://example.com/2</link><pubDate>Tue, 05 Aug 2026 10:00:00 GMT</pubDate></item>
  <item><title>Third post</title><link>https://example.com/3</link><pubDate>Wed, 06 Aug 2026 10:00:00 GMT</pubDate></item>
</channel></rss>`

func TestRSSAction_ReturnsItems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write([]byte(sampleRSS))
	}))
	defer srv.Close()

	node := models.WorkflowNode{
		ID: "rss1", Type: models.NodeTypeAction, Template: "rss",
		Config: map[string]string{"rssURL": srv.URL},
	}
	rc := engine.NewRunContext("r1", nil)

	out, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("want a structured result, got %T", out)
	}
	if got["title"] != "Example Feed" {
		t.Errorf("feed title: got %v", got["title"])
	}
	items, ok := got["items"].([]map[string]any)
	if !ok {
		t.Fatalf("want an items slice, got %T", got["items"])
	}
	if len(items) != 3 {
		t.Fatalf("want 3 items, got %d", len(items))
	}
	if items[0]["title"] != "First post" {
		t.Errorf("first item title: got %v", items[0]["title"])
	}
	if items[0]["link"] != "https://example.com/1" {
		t.Errorf("first item link: got %v", items[0]["link"])
	}
}

func TestRSSAction_RespectsLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sampleRSS))
	}))
	defer srv.Close()

	node := models.WorkflowNode{
		ID: "rss1", Type: models.NodeTypeAction, Template: "rss",
		Config: map[string]string{"rssURL": srv.URL, "rssLimit": "2"},
	}
	rc := engine.NewRunContext("r1", nil)
	out, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	items := out.(map[string]any)["items"].([]map[string]any)
	if len(items) != 2 {
		t.Errorf("want 2 items with rssLimit=2, got %d", len(items))
	}
}

func TestRSSAction_SkipsWithoutURL(t *testing.T) {
	node := models.WorkflowNode{ID: "rss1", Type: models.NodeTypeAction, Template: "rss"}
	rc := engine.NewRunContext("r1", nil)
	got, _ := nodes.ExecuteAction(context.Background(), node, rc)
	if got != "rss_skipped_no_url" {
		t.Errorf("want skip sentinel, got %v", got)
	}
}

func TestRSSAction_ErrorsOnMalformedFeed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not a feed at all"))
	}))
	defer srv.Close()

	node := models.WorkflowNode{
		ID: "rss1", Type: models.NodeTypeAction, Template: "rss",
		Config: map[string]string{"rssURL": srv.URL},
	}
	rc := engine.NewRunContext("r1", nil)
	if _, err := nodes.ExecuteAction(context.Background(), node, rc); err == nil {
		t.Error("want an error for a non-feed response, got nil")
	}
}

func TestHackerNewsAction_SearchesStories(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("query")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"hits":[
			{"title":"Show HN: a thing","url":"https://example.com/a","points":120,"objectID":"1"},
			{"title":"Ask HN: another","url":"https://example.com/b","points":80,"objectID":"2"}
		]}`))
	}))
	defer srv.Close()
	nodes.SetHackerNewsAPIBaseForTest(srv.URL)
	defer nodes.SetHackerNewsAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "hn1", Type: models.NodeTypeAction, Template: "hackernews",
		Config: map[string]string{"hnQuery": "{{ input }}"},
	}
	rc := engine.NewRunContext("r1", []byte(`"golang"`))

	out, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if gotQuery != "golang" {
		t.Errorf("query should resolve templates, got %q", gotQuery)
	}
	res := out.(map[string]any)
	items := res["items"].([]map[string]any)
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}
	if items[0]["title"] != "Show HN: a thing" {
		t.Errorf("got %#v", items[0])
	}
	if items[0]["points"] != float64(120) {
		t.Errorf("points: got %#v", items[0]["points"])
	}
}

func TestHackerNewsAction_SkipsWithoutQuery(t *testing.T) {
	node := models.WorkflowNode{ID: "hn1", Type: models.NodeTypeAction, Template: "hackernews"}
	rc := engine.NewRunContext("r1", nil)
	got, _ := nodes.ExecuteAction(context.Background(), node, rc)
	if got != "hackernews_skipped_no_query" {
		t.Errorf("want skip sentinel, got %v", got)
	}
}

func TestCoinGeckoAction_ReturnsPrices(t *testing.T) {
	var gotIDs, gotVs string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIDs = r.URL.Query().Get("ids")
		gotVs = r.URL.Query().Get("vs_currencies")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"bitcoin":{"usd":64000.5},"ethereum":{"usd":3100}}`))
	}))
	defer srv.Close()
	nodes.SetCoinGeckoAPIBaseForTest(srv.URL)
	defer nodes.SetCoinGeckoAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "cg1", Type: models.NodeTypeAction, Template: "coingecko",
		Config: map[string]string{"cgIDs": "bitcoin,ethereum"},
	}
	rc := engine.NewRunContext("r1", nil)

	out, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if gotIDs != "bitcoin,ethereum" {
		t.Errorf("ids: got %q", gotIDs)
	}
	if gotVs != "usd" {
		t.Errorf("want the default vs_currency usd, got %q", gotVs)
	}
	res := out.(map[string]any)
	btc := res["bitcoin"].(map[string]any)
	if btc["usd"] != float64(64000.5) {
		t.Errorf("got %#v", res)
	}
}

func TestCoinGeckoAction_SkipsWithoutIDs(t *testing.T) {
	node := models.WorkflowNode{ID: "cg1", Type: models.NodeTypeAction, Template: "coingecko"}
	rc := engine.NewRunContext("r1", nil)
	got, _ := nodes.ExecuteAction(context.Background(), node, rc)
	if got != "coingecko_skipped_no_ids" {
		t.Errorf("want skip sentinel, got %v", got)
	}
}

func TestQuickChartToolBuildsURL(t *testing.T) {
	rc := engine.NewRunContext("r1", nil)
	node := models.WorkflowNode{
		ID: "qc1", Type: models.NodeTypeTool, Template: "quickchart",
		Config: map[string]string{
			"qcConfig": `{"type":"bar","data":{"labels":["a","b"],"datasets":[{"data":[1,2]}]}}`,
			"qcWidth":  "600",
		},
	}
	out, err := nodes.ExecuteTool(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	got := out.(string)
	if !strings.HasPrefix(got, "https://quickchart.io/chart?") {
		t.Fatalf("want a quickchart URL, got %q", got)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("w") != "600" {
		t.Errorf("width: got %q", u.Query().Get("w"))
	}
	// The chart config must be URL-encoded, not spliced in raw.
	if u.Query().Get("c") != `{"type":"bar","data":{"labels":["a","b"],"datasets":[{"data":[1,2]}]}}` {
		t.Errorf("chart config: got %q", u.Query().Get("c"))
	}
}

func TestQuickChartToolRejectsInvalidConfig(t *testing.T) {
	rc := engine.NewRunContext("r1", nil)
	for _, cfg := range []string{"", `{"type": }`} {
		node := models.WorkflowNode{
			ID: "qc1", Type: models.NodeTypeTool, Template: "quickchart",
			Config: map[string]string{"qcConfig": cfg},
		}
		if _, err := nodes.ExecuteTool(context.Background(), node, rc); err == nil {
			t.Errorf("config %q should error, got nil", cfg)
		}
	}
}
