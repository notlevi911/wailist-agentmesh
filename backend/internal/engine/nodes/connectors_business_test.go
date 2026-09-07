package nodes_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/agentmesh/backend/internal/engine"
	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
)

// Twilio, Stripe, PagerDuty, and Zendesk each had two independent
// implementations after merging this PR with master (see
// connectors_business.go's apiBaseDefaults comment) -- the ops.go/
// commerce.go versions were kept as canonical, so their coverage lives in
// connectors_ops_test.go (Twilio/PagerDuty/Zendesk) and
// connectors_commerce_test.go (Stripe) instead of here.

func TestIntercomAction_CreatesLeadFromMessageEmail(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	nodes.SetIntercomAPIBaseForTest(srv.URL)
	defer nodes.SetIntercomAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "ic1", Type: models.NodeTypeAction, Template: "intercom",
		Secrets: map[string]string{"intercomAccessToken": "tok"},
	}
	rc := engine.NewRunContext("r1", []byte(`"lead@example.com"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "intercom_lead_created" {
		t.Errorf("want sentinel, got %v", result)
	}
	if gotBody["email"] != "lead@example.com" || gotBody["role"] != "lead" {
		t.Errorf("want email/role in body, got %v", gotBody)
	}
}

func TestIntercomAction_SkipsWhenNoAccessToken(t *testing.T) {
	node := models.WorkflowNode{ID: "ic2", Type: models.NodeTypeAction, Template: "intercom"}
	rc := engine.NewRunContext("r1", []byte(`"x@example.com"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) || result != "intercom_skipped_no_access_token" {
		t.Errorf("want skip sentinel, got %v / %v", result, err)
	}
}

func TestOpenWeatherMapAction_ReturnsDecodedWeather(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Write([]byte(`{"weather":[{"main":"Clear"}],"main":{"temp":22.5}}`))
	}))
	defer srv.Close()
	nodes.SetOpenWeatherAPIBaseForTest(srv.URL)
	defer nodes.SetOpenWeatherAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "ow1", Type: models.NodeTypeAction, Template: "openweathermap",
		Secrets: map[string]string{"openWeatherAPIKey": "key123"},
		Config:  map[string]string{"weatherCity": "London"},
	}
	rc := engine.NewRunContext("r1", nil)
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if gotQuery.Get("q") != "London" || gotQuery.Get("appid") != "key123" {
		t.Errorf("want city/key query params, got %v", gotQuery)
	}
	m, ok := result.(map[string]any)
	if !ok || m["weather"] == nil {
		t.Errorf("want decoded weather response, got %v", result)
	}
}

func TestOpenWeatherMapAction_FallsBackToMessageAsCity(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	nodes.SetOpenWeatherAPIBaseForTest(srv.URL)
	defer nodes.SetOpenWeatherAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "ow2", Type: models.NodeTypeAction, Template: "openweathermap",
		Secrets: map[string]string{"openWeatherAPIKey": "key123"},
	}
	rc := engine.NewRunContext("r1", []byte(`"Tokyo"`))
	if _, err := nodes.ExecuteAction(context.Background(), node, rc); err != nil {
		t.Fatal(err)
	}
	if gotQuery.Get("q") != "Tokyo" {
		t.Errorf("want message used as city fallback, got %v", gotQuery.Get("q"))
	}
}

func TestOpenWeatherMapAction_SkipsWhenNoAPIKey(t *testing.T) {
	node := models.WorkflowNode{ID: "ow3", Type: models.NodeTypeAction, Template: "openweathermap"}
	rc := engine.NewRunContext("r1", []byte(`"Paris"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) || result != "weather_skipped_no_api_key" {
		t.Errorf("want skip sentinel, got %v / %v", result, err)
	}
}

func TestCalendlyAction_ReturnsDecodedEvents(t *testing.T) {
	var gotAuth string
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotQuery = r.URL.Query()
		w.Write([]byte(`{"collection":[{"name":"1:1 call"}]}`))
	}))
	defer srv.Close()
	nodes.SetCalendlyAPIBaseForTest(srv.URL)
	defer nodes.SetCalendlyAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "cl1", Type: models.NodeTypeAction, Template: "calendly",
		Secrets: map[string]string{"calendlyAccessToken": "tok"},
		Config:  map[string]string{"calendlyUserURI": "https://api.calendly.com/users/abc"},
	}
	rc := engine.NewRunContext("r1", nil)
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("want bearer auth, got %q", gotAuth)
	}
	if gotQuery.Get("user") != "https://api.calendly.com/users/abc" {
		t.Errorf("want user uri query param, got %v", gotQuery.Get("user"))
	}
	m, ok := result.(map[string]any)
	if !ok || m["collection"] == nil {
		t.Errorf("want decoded events collection, got %v", result)
	}
}

func TestCalendlyAction_SkipsWhenNoUserURI(t *testing.T) {
	node := models.WorkflowNode{
		ID: "cl2", Type: models.NodeTypeAction, Template: "calendly",
		Secrets: map[string]string{"calendlyAccessToken": "tok"},
	}
	rc := engine.NewRunContext("r1", nil)
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) || result != "calendly_skipped_no_user_uri" {
		t.Errorf("want skip sentinel, got %v / %v", result, err)
	}
}

func TestShopifyAction_AddsOrderNoteWithAccessTokenHeader(t *testing.T) {
	var gotToken string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Shopify-Access-Token")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	nodes.SetShopifyOrderNoteAPIBaseForTest(srv.URL)
	defer nodes.SetShopifyOrderNoteAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "sh3", Type: models.NodeTypeAction, Template: "shopify",
		Secrets: map[string]string{"shopifyAccessToken": "shpat_123"},
		Config:  map[string]string{"shopifyShopDomain": "mystore.myshopify.com", "shopifyOrderID": "9001"},
	}
	rc := engine.NewRunContext("r1", []byte(`"refunded per customer request"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "shopify_order_note_added" {
		t.Errorf("want sentinel, got %v", result)
	}
	if gotToken != "shpat_123" {
		t.Errorf("want access token header, got %q", gotToken)
	}
	order, ok := gotBody["order"].(map[string]any)
	if !ok || order["note"] != "refunded per customer request" {
		t.Errorf("want note from message, got %v", gotBody["order"])
	}
}

// shopifyShopDomain/shopifyOrderID must resolve {{ }} references, matching
// the sibling create-customer connector -- a templated order ID (e.g. from
// an upstream order-lookup node) previously got spliced in literally instead
// of resolved.
func TestShopifyAction_ResolvesTemplatesInDomainAndOrderID(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	nodes.SetShopifyOrderNoteAPIBaseForTest(srv.URL)
	defer nodes.SetShopifyOrderNoteAPIBaseForTest("")

	rc := engine.NewRunContext("r1", []byte(`"refunded per customer request"`))
	rc.Set("lookup", map[string]any{"domain": "mystore.myshopify.com", "orderID": "9002"})

	node := models.WorkflowNode{
		ID: "sh5", Type: models.NodeTypeAction, Template: "shopify",
		Secrets: map[string]string{"shopifyAccessToken": "shpat_123"},
		Config: map[string]string{
			"shopifyShopDomain": "{{ node.lookup.domain }}",
			"shopifyOrderID":    "{{ node.lookup.orderID }}",
		},
	}
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "shopify_order_note_added" {
		t.Errorf("want 'shopify_order_note_added' (templates resolved), got %v", result)
	}
	if !strings.Contains(gotPath, "/orders/9002.json") {
		t.Errorf("orderID should resolve into the request path, got %q", gotPath)
	}
	order, ok := gotBody["order"].(map[string]any)
	if !ok || order["id"] != "9002" {
		t.Errorf("orderID should resolve in the request body too, got %v", gotBody["order"])
	}
}

func TestShopifyAction_SkipsWhenDomainInvalid(t *testing.T) {
	var requestReceived bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestReceived = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	nodes.SetShopifyOrderNoteAPIBaseForTest(srv.URL)
	defer nodes.SetShopifyOrderNoteAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "sh4", Type: models.NodeTypeAction, Template: "shopify",
		Secrets: map[string]string{"shopifyAccessToken": "shpat_123"},
		Config:  map[string]string{"shopifyShopDomain": "evil.com", "shopifyOrderID": "9001"},
	}
	rc := engine.NewRunContext("r1", nil)
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "shopify_skipped_invalid_domain" {
		t.Errorf("want 'shopify_skipped_invalid_domain', got %v", result)
	}
	if requestReceived {
		t.Error("expected no HTTP request to be dispatched for an invalid domain")
	}
}

func TestShopifyAction_SkipsWhenMissingConfig(t *testing.T) {
	node := models.WorkflowNode{
		ID: "sh1", Type: models.NodeTypeAction, Template: "shopify",
		Secrets: map[string]string{"shopifyAccessToken": "tok"},
	}
	rc := engine.NewRunContext("r1", nil)
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) || result != "shopify_skipped_missing_config" {
		t.Errorf("want skip sentinel, got %v / %v", result, err)
	}
}

func TestShopifyAction_SkipsWhenNoAccessToken(t *testing.T) {
	node := models.WorkflowNode{ID: "sh2", Type: models.NodeTypeAction, Template: "shopify"}
	rc := engine.NewRunContext("r1", nil)
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) || result != "shopify_skipped_no_access_token" {
		t.Errorf("want skip sentinel, got %v / %v", result, err)
	}
}

func TestBaserowAction_CreatesRowWithTokenAuth(t *testing.T) {
	var gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	nodes.SetBaserowAPIBaseForTest(srv.URL)
	defer nodes.SetBaserowAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "br1", Type: models.NodeTypeAction, Template: "baserow",
		Secrets: map[string]string{"baserowAPIToken": "tok"},
		Config:  map[string]string{"baserowTableID": "42", "baserowFieldName": "Summary"},
	}
	rc := engine.NewRunContext("r1", []byte(`"row content"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "baserow_row_created" {
		t.Errorf("want sentinel, got %v", result)
	}
	if gotAuth != "Token tok" {
		t.Errorf("want Baserow's own 'Token' auth scheme, got %q", gotAuth)
	}
	if gotBody["Summary"] != "row content" {
		t.Errorf("want configured field name used, got %v", gotBody)
	}
}

func TestBaserowAction_SkipsWhenNoTableID(t *testing.T) {
	node := models.WorkflowNode{
		ID: "br2", Type: models.NodeTypeAction, Template: "baserow",
		Secrets: map[string]string{"baserowAPIToken": "tok"},
	}
	rc := engine.NewRunContext("r1", nil)
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) || result != "baserow_skipped_no_table_id" {
		t.Errorf("want skip sentinel, got %v / %v", result, err)
	}
}
