package nodes

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/agentmesh/backend/internal/models"
)

// hubspotAPIBase is overridden in tests via SetHubSpotAPIBaseForTest.
var hubspotAPIBase = "https://api.hubapi.com"

// SetHubSpotAPIBaseForTest overrides the HubSpot API base URL. Call only
// from tests. Pass "" to reset to the real API.
func SetHubSpotAPIBaseForTest(base string) {
	if base == "" {
		hubspotAPIBase = "https://api.hubapi.com"
	} else {
		hubspotAPIBase = base
	}
}

func sendHubSpot(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	// OAuth-linked token takes priority: HubSpot's OAuth access token works
	// identically to a manual private-app token here (same Bearer scheme).
	apiKey := secretVal(node, "hubspotOAuthAccessToken")
	if apiKey == "" {
		apiKey = secretVal(node, "hubspotAPIKey")
	}
	if apiKey == "" {
		return "hubspot_skipped_no_api_key", ErrActionSkipped
	}
	payload := map[string]any{
		"properties": map[string]any{
			"hs_note_body": resolveMessage(node, rc),
			"hs_timestamp": time.Now().UnixMilli(),
		},
	}
	headers := map[string]string{"Authorization": "Bearer " + apiKey}
	return postJSON(ctx, hubspotAPIBase+"/crm/v3/objects/notes", headers, payload, "hubspot_note_created", "HubSpot")
}

// mailchimpAPIBase is overridden in tests via SetMailchimpAPIBaseForTest —
// normally "https://{dc}.api.mailchimp.com" is built per-node from the API
// key's datacenter suffix, so the test override replaces the whole
// scheme+host and sendMailchimp skips that construction when set.
var mailchimpAPIBase = ""

// SetMailchimpAPIBaseForTest overrides the Mailchimp API base URL entirely.
// Call only from tests. Pass "" to reset to the real per-datacenter host.
func SetMailchimpAPIBaseForTest(base string) {
	mailchimpAPIBase = base
}

func sendMailchimp(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	listID := configVal(node, "mailchimpListID", "")
	if listID == "" {
		return "mailchimp_skipped_no_list_id", ErrActionSkipped
	}
	email := configVal(node, "mailchimpEmail", "")
	if email == "" {
		email = resolveMessage(node, rc)
	}
	email = strings.TrimSpace(email)
	if email == "" {
		return "mailchimp_skipped_no_email", ErrActionSkipped
	}

	// OAuth-linked and manual API-key paths derive the base URL from two
	// genuinely different sources, not a credential swap like the other
	// connectors in this plan: a manual key's datacenter suffix comes from
	// parsing the key itself (mailchimpDatacenter), but an OAuth token
	// carries no such suffix — it's looked up once at link time instead (see
	// mailchimpPostExchangeHook in connector_oauth.go) and stored in
	// node.Config["mailchimpOAuthDC"]. Calling mailchimpDatacenter on an
	// OAuth token would fail since it has no "<key>-<dc>" shape. Handled as
	// two independent blocks on purpose, same shape as sendJira.
	if oauthToken := secretVal(node, "mailchimpOAuthAccessToken"); oauthToken != "" {
		dc := configVal(node, "mailchimpOAuthDC", "")
		if dc == "" {
			return "mailchimp_skipped_missing_config", ErrActionSkipped
		}
		base := mailchimpAPIBase
		if base == "" {
			base = "https://" + dc + ".api.mailchimp.com"
		}
		hash := md5.Sum([]byte(strings.ToLower(email)))
		subscriberHash := hex.EncodeToString(hash[:])
		target := base + "/3.0/lists/" + url.PathEscape(listID) + "/members/" + subscriberHash
		payload := map[string]any{
			"email_address": email,
			"status_if_new": "subscribed",
			"status":        "subscribed",
		}
		headers := map[string]string{"Authorization": "Bearer " + oauthToken}
		req, err := newJSONRequest(ctx, http.MethodPut, target, headers, payload)
		if err != nil {
			return nil, fmt.Errorf("Mailchimp: %w", err)
		}
		return doAndCheck(req, "mailchimp_subscriber_added", "Mailchimp")
	}

	apiKey := secretVal(node, "mailchimpAPIKey")
	if apiKey == "" {
		return "mailchimp_skipped_no_api_key", ErrActionSkipped
	}
	base := mailchimpAPIBase
	if base == "" {
		dc, err := mailchimpDatacenter(apiKey)
		if err != nil {
			return nil, err
		}
		base = "https://" + dc + ".api.mailchimp.com"
	}
	hash := md5.Sum([]byte(strings.ToLower(email)))
	subscriberHash := hex.EncodeToString(hash[:])
	target := base + "/3.0/lists/" + url.PathEscape(listID) + "/members/" + subscriberHash
	payload := map[string]any{
		"email_address": email,
		"status_if_new": "subscribed",
		"status":        "subscribed",
	}
	headers := basicAuthHeader("anystring", apiKey)
	req, err := newJSONRequest(ctx, http.MethodPut, target, headers, payload)
	if err != nil {
		return nil, fmt.Errorf("Mailchimp: %w", err)
	}
	return doAndCheck(req, "mailchimp_subscriber_added", "Mailchimp")
}

// mailchimpDatacenterPattern matches Mailchimp's datacenter suffix format
// (e.g. "us21"): letters and digits only. The suffix is interpolated
// directly into the request host in sendMailchimp, so it must be validated
// before use — otherwise an API key containing a crafted suffix like
// "evil.com/x" could redirect the request (carrying the real key in the
// Basic-auth header) to an attacker-controlled host. Mirrors jiraDomainPattern
// in connectors_devtools.go, which guards the same class of host injection.
var mailchimpDatacenterPattern = regexp.MustCompile(`^[a-zA-Z0-9]+$`)

// mailchimpDatacenter extracts the data-center suffix (e.g. "us21") from a
// Mailchimp API key of the form "<key>-<dc>".
func mailchimpDatacenter(apiKey string) (string, error) {
	i := strings.LastIndexByte(apiKey, '-')
	if i < 0 || i == len(apiKey)-1 {
		return "", fmt.Errorf("Mailchimp: API key missing datacenter suffix")
	}
	dc := apiKey[i+1:]
	if !mailchimpDatacenterPattern.MatchString(dc) {
		return "", fmt.Errorf("Mailchimp: API key has invalid datacenter suffix")
	}
	return dc, nil
}

// MailchimpDatacenterForTest is a test-only exported wrapper for mailchimpDatacenter.
func MailchimpDatacenterForTest(apiKey string) (string, error) {
	return mailchimpDatacenter(apiKey)
}

func sendSupabase(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	apiKey := secretVal(node, "supabaseAPIKey")
	if apiKey == "" {
		return "supabase_skipped_no_api_key", ErrActionSkipped
	}
	projectURL := configVal(node, "supabaseProjectURL", "")
	table := configVal(node, "supabaseTable", "")
	if projectURL == "" || table == "" {
		return "supabase_skipped_missing_config", ErrActionSkipped
	}
	column := configVal(node, "supabaseColumn", "content")
	target := strings.TrimRight(projectURL, "/") + "/rest/v1/" + url.PathEscape(table)
	payload := map[string]any{column: resolveMessage(node, rc)}
	headers := map[string]string{
		"apikey":        apiKey,
		"Authorization": "Bearer " + apiKey,
		"Prefer":        "return=minimal",
	}
	return postJSON(ctx, target, headers, payload, "supabase_row_inserted", "Supabase")
}

func sendWooCommerce(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	consumerKey := secretVal(node, "woocommerceConsumerKey")
	consumerSecret := secretVal(node, "woocommerceConsumerSecret")
	if consumerKey == "" || consumerSecret == "" {
		return "woocommerce_skipped_no_credentials", ErrActionSkipped
	}
	storeURL := configVal(node, "woocommerceStoreURL", "")
	orderID := configVal(node, "woocommerceOrderID", "")
	if storeURL == "" || orderID == "" {
		return "woocommerce_skipped_missing_config", ErrActionSkipped
	}
	target := strings.TrimRight(storeURL, "/") + "/wp-json/wc/v3/orders/" + url.PathEscape(orderID) + "/notes"
	payload := map[string]any{"note": resolveMessage(node, rc), "customer_note": false}
	headers := basicAuthHeader(consumerKey, consumerSecret)
	return postJSON(ctx, target, headers, payload, "woocommerce_note_added", "WooCommerce")
}
