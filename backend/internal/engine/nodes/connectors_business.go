package nodes

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/agentmesh/backend/internal/models"
)

// apiBaseDefaults holds each connector's real API base URL, keyed by
// service name -- "" for Shopify, whose base is built per-node from
// user-supplied shop config rather than a fixed host. apiBases holds the
// current (possibly test-overridden) value, seeded from the defaults.
// Together with apiBase/setAPIBaseForTest below, this replaces what would
// otherwise be a hand-written var + SetXAPIBaseForTest pair per connector.
//
// Twilio, Stripe, PagerDuty, and Zendesk are deliberately absent: this PR
// and master's independently added connectors for all four, and the
// versions in connectors_ops.go/connectors_commerce.go (with their own
// local *APIBase vars) were kept as canonical on reconciliation -- see
// those files instead.
var apiBaseDefaults = map[string]string{
	"intercom":    "https://api.intercom.io",
	"openweather": "https://api.openweathermap.org",
	"calendly":    "https://api.calendly.com",
	"shopify":     "",
	"baserow":     "https://api.baserow.io",
}

var apiBases = cloneAPIBaseDefaults()

func cloneAPIBaseDefaults() map[string]string {
	m := make(map[string]string, len(apiBaseDefaults))
	for k, v := range apiBaseDefaults {
		m[k] = v
	}
	return m
}

// apiBase returns service's current API base URL (real, or test-overridden).
func apiBase(service string) string {
	return apiBases[service]
}

// setAPIBaseForTest overrides service's API base URL, resetting to its real
// default when base is "". Backs every SetXAPIBaseForTest export below.
func setAPIBaseForTest(service, base string) {
	if base == "" {
		base = apiBaseDefaults[service]
	}
	apiBases[service] = base
}

// SetIntercomAPIBaseForTest overrides the Intercom API base URL. Call only
// from tests. Pass "" to reset to the real API.
func SetIntercomAPIBaseForTest(base string) { setAPIBaseForTest("intercom", base) }

// sendIntercom creates a lead Contact -- same "message as email" fallback
// as sendStripe/sendMailchimp, and the simplest Intercom operation that
// doesn't need a pre-existing contact/admin id to already be known.
func sendIntercom(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	apiKey := secretVal(node, "intercomAccessToken")
	if apiKey == "" {
		return "intercom_skipped_no_access_token", ErrActionSkipped
	}
	email := configVal(node, "intercomEmail", "")
	if email == "" {
		email = resolveMessage(node, rc)
	}
	email = strings.TrimSpace(email)
	if email == "" {
		return "intercom_skipped_no_email", ErrActionSkipped
	}
	payload := map[string]any{"role": "lead", "email": email}
	headers := map[string]string{
		"Authorization": "Bearer " + apiKey,
		"Accept":        "application/json",
	}
	return postJSON(ctx, apiBase("intercom")+"/contacts", headers, payload, "intercom_lead_created", "Intercom")
}

// SetOpenWeatherAPIBaseForTest overrides the OpenWeatherMap API base URL.
// Call only from tests. Pass "" to reset to the real API.
func SetOpenWeatherAPIBaseForTest(base string) { setAPIBaseForTest("openweather", base) }

// getOpenWeather is a read op (like getTelegramUpdates) -- the first of
// this batch that returns data rather than just confirming a side effect.
func getOpenWeather(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	apiKey := secretVal(node, "openWeatherAPIKey")
	if apiKey == "" {
		return "weather_skipped_no_api_key", ErrActionSkipped
	}
	city := configVal(node, "weatherCity", "")
	if city == "" {
		city = resolveMessage(node, rc)
	}
	city = strings.TrimSpace(city)
	if city == "" {
		return "weather_skipped_no_city", ErrActionSkipped
	}
	q := url.Values{}
	q.Set("q", city)
	q.Set("appid", apiKey)
	q.Set("units", configVal(node, "weatherUnits", "metric"))
	target := apiBase("openweather") + "/data/2.5/weather?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("OpenWeatherMap: build request: %w", err)
	}
	return getJSON(req, "OpenWeatherMap")
}

// SetCalendlyAPIBaseForTest overrides the Calendly API base URL. Call only
// from tests. Pass "" to reset to the real API.
func SetCalendlyAPIBaseForTest(base string) { setAPIBaseForTest("calendly", base) }

// getCalendlyEvents is a read op: lists this account's scheduled events.
func getCalendlyEvents(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	token := secretVal(node, "calendlyAccessToken")
	if token == "" {
		return "calendly_skipped_no_access_token", ErrActionSkipped
	}
	userURI := configVal(node, "calendlyUserURI", "")
	if userURI == "" {
		return "calendly_skipped_no_user_uri", ErrActionSkipped
	}
	q := url.Values{}
	q.Set("user", userURI)
	q.Set("count", configVal(node, "calendlyCount", "10"))
	target := apiBase("calendly") + "/scheduled_events?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("Calendly: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return getJSON(req, "Calendly")
}

// shopifyDomainPattern mirrors jiraDomainPattern's reasoning (connectors_
// devtools.go): shopifyShopDomain is user-supplied config interpolated
// directly into the request host (along with the real Shopify access
// token, via X-Shopify-Access-Token), so it must be validated before use --
// otherwise a crafted value on a copied/imported workflow could redirect
// the request, and the token with it, to an attacker-controlled host.
// Unlike Jira/Zendesk's bare subdomain, shopifyShopDomain is already a full
// host, so this anchors dnsLabelPattern (the same subdomain character class
// jiraDomainPattern validates) to the real "*.myshopify.com" shape rather
// than just checking character set -- and rather than hand-duplicating that
// character class into its own regex literal, which is what let this
// pattern and sendShopify's bare-subdomain check (connectors_commerce.go,
// which reuses jiraDomainPattern directly) drift into two independently-
// written regexes for the same underlying label shape.
var shopifyDomainPattern = regexp.MustCompile(`^` + dnsLabelPattern + `\.myshopify\.com$`)

// SetShopifyOrderNoteAPIBaseForTest overrides the Shopify API base URL entirely
// (including scheme+host) -- normally "https://{shop}" is built per-node
// (shopifyShopDomain is already a full host like "mystore.myshopify.com"),
// so the override replaces that "https://" + shop prefix entirely, letting
// a test point at a plain http:// httptest server. Call only from tests.
// Pass "" to reset to the real per-node construction.
func SetShopifyOrderNoteAPIBaseForTest(base string) { setAPIBaseForTest("shopify", base) }

// sendShopifyOrderNote adds a note to an existing order -- distinct from
// sendShopify (connectors_commerce.go, template id "shopify_customer"),
// which creates a new customer. Template id "shopify" (this function, via
// action.go's dispatch) is master's original id and behavior for this
// operation; a prior version of this PR repointed "shopify" at the
// customer-creation operation instead, which would have made every
// already-saved order-note node silently stop writing notes on its next
// deploy with no config change on the user's side.
func sendShopifyOrderNote(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	accessToken := secretVal(node, "shopifyAccessToken")
	if accessToken == "" {
		return "shopify_skipped_no_access_token", ErrActionSkipped
	}
	shop := resolveTemplate(configVal(node, "shopifyShopDomain", ""), rc)
	orderID := resolveTemplate(configVal(node, "shopifyOrderID", ""), rc)
	if shop == "" || orderID == "" {
		return "shopify_skipped_missing_config", ErrActionSkipped
	}
	if !shopifyDomainPattern.MatchString(shop) {
		return "shopify_skipped_invalid_domain", ErrActionSkipped
	}
	base := apiBase("shopify")
	if base == "" {
		base = "https://" + shop
	}
	target := base + "/admin/api/2024-01/orders/" + url.PathEscape(orderID) + ".json"
	payload := map[string]any{
		"order": map[string]any{"id": orderID, "note": resolveMessage(node, rc)},
	}
	req, err := newJSONRequest(ctx, http.MethodPut, target, map[string]string{"X-Shopify-Access-Token": accessToken}, payload)
	if err != nil {
		return nil, fmt.Errorf("Shopify: %w", err)
	}
	return doAndCheck(req, "shopify_order_note_added", "Shopify")
}

// SetBaserowAPIBaseForTest overrides the Baserow API base URL. Call only
// from tests. Pass "" to reset to the real API.
func SetBaserowAPIBaseForTest(base string) { setAPIBaseForTest("baserow", base) }

func sendBaserow(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	token := secretVal(node, "baserowAPIToken")
	if token == "" {
		return "baserow_skipped_no_api_token", ErrActionSkipped
	}
	tableID := configVal(node, "baserowTableID", "")
	if tableID == "" {
		return "baserow_skipped_no_table_id", ErrActionSkipped
	}
	fieldName := configVal(node, "baserowFieldName", "Notes")
	target := apiBase("baserow") + "/api/database/rows/table/" + url.PathEscape(tableID) + "/?user_field_names=true"
	payload := map[string]any{fieldName: resolveMessage(node, rc)}
	// Baserow's own auth scheme: "Authorization: Token <api_token>", not
	// the Bearer prefix most of the rest of these connectors use.
	headers := map[string]string{"Authorization": "Token " + token}
	return postJSON(ctx, target, headers, payload, "baserow_row_created", "Baserow")
}
