package nodes

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/agentmesh/backend/internal/models"
)

// stripeAPIBase is overridden in tests via SetStripeAPIBaseForTest.
var stripeAPIBase = "https://api.stripe.com"

// SetStripeAPIBaseForTest overrides the Stripe API base URL. Call only from
// tests. Pass "" to reset to the real API.
func SetStripeAPIBaseForTest(base string) {
	if base == "" {
		stripeAPIBase = "https://api.stripe.com"
	} else {
		stripeAPIBase = base
	}
}

// sendStripe creates a Stripe customer, using the run output as the
// description. Stripe's API takes form encoding, not JSON, so this builds the
// request directly rather than going through postJSON.
func sendStripe(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	// stripeAPIKey is the current field name; a node saved before this PR has
	// its key under the old stripeSecretKey name -- fall back there so
	// already-configured Stripe nodes don't silently break on deploy.
	apiKey := secretVal(node, "stripeAPIKey")
	if apiKey == "" {
		apiKey = secretVal(node, "stripeSecretKey")
	}
	if apiKey == "" {
		return "stripe_skipped_no_api_key", ErrActionSkipped
	}
	email := resolveTemplate(configVal(node, "stripeEmail", ""), rc)
	if email == "" {
		// Documented default: fall back to the upstream node's own message
		// when no explicit stripeEmail is configured.
		email = rc.Message()
	}
	email = strings.TrimSpace(email)
	if email == "" {
		return "stripe_skipped_no_email", ErrActionSkipped
	}
	form := url.Values{}
	form.Set("email", email)
	form.Set("description", resolveMessage(node, rc))
	if name := resolveTemplate(configVal(node, "stripeName", ""), rc); name != "" {
		form.Set("name", name)
	}
	req, err := newFormRequest(ctx, stripeAPIBase+"/v1/customers", form)
	if err != nil {
		return nil, fmt.Errorf("Stripe: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	return doAndCheck(req, "stripe_customer_created", "Stripe")
}

// shopifyAPIBase is overridden in tests via SetShopifyAPIBaseForTest.
// Normally "https://{store}.myshopify.com" is built per-node, so the test
// override replaces the whole scheme+host.
var shopifyAPIBase = ""

// SetShopifyAPIBaseForTest overrides the Shopify API base URL entirely.
// Call only from tests. Pass "" to reset to the real per-store host.
func SetShopifyAPIBaseForTest(base string) { shopifyAPIBase = base }

// shopifyAPIVersion pins the Admin API version. Shopify dates its API and
// removes versions after ~12 months — bumping this is a deliberate, tested
// change, not something to leave floating.
const shopifyAPIVersion = "2024-10"

// sendShopify creates a Shopify customer with the run output as the note.
func sendShopify(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	token := secretVal(node, "shopifyAccessToken")
	if token == "" {
		return "shopify_skipped_no_access_token", ErrActionSkipped
	}
	store := resolveTemplate(configVal(node, "shopifyStore", ""), rc)
	email := resolveTemplate(configVal(node, "shopifyEmail", ""), rc)
	if store == "" || email == "" {
		return "shopify_skipped_missing_config", ErrActionSkipped
	}
	// store is user-supplied config interpolated directly into the request
	// host below -- validate it first (mirrors jiraDomainPattern's own
	// reasoning in connectors_devtools.go), otherwise a crafted value on a
	// copied/imported workflow could redirect the request, and the access
	// token with it, to an attacker-controlled host. url.PathEscape alone
	// only makes the result a well-formed URL, not a safe one.
	if !jiraDomainPattern.MatchString(store) {
		return "shopify_skipped_invalid_store", ErrActionSkipped
	}
	base := shopifyAPIBase
	if base == "" {
		base = "https://" + url.PathEscape(store) + ".myshopify.com"
	}
	payload := map[string]any{"customer": map[string]any{
		"email": email,
		"note":  resolveMessage(node, rc),
	}}
	headers := map[string]string{"X-Shopify-Access-Token": token}
	return postJSON(ctx, base+"/admin/api/"+shopifyAPIVersion+"/customers.json",
		headers, payload, "shopify_customer_created", "Shopify")
}

// pipedriveAPIBase is overridden in tests via SetPipedriveAPIBaseForTest.
var pipedriveAPIBase = ""

// SetPipedriveAPIBaseForTest overrides the Pipedrive API base URL entirely.
// Call only from tests. Pass "" to reset to the real per-company host.
func SetPipedriveAPIBaseForTest(base string) { pipedriveAPIBase = base }

// sendPipedrive logs a CRM note with the run output. Pipedrive takes its API
// token as a query parameter rather than a header.
func sendPipedrive(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	token := secretVal(node, "pipedriveAPIToken")
	if token == "" {
		return "pipedrive_skipped_no_api_token", ErrActionSkipped
	}
	base := pipedriveAPIBase
	if base == "" {
		domain := resolveTemplate(configVal(node, "pipedriveCompanyDomain", ""), rc)
		if domain == "" {
			return "pipedrive_skipped_missing_config", ErrActionSkipped
		}
		// domain is user-supplied config interpolated directly into the
		// request host below -- validate it first, same reasoning as
		// jiraDomainPattern (connectors_devtools.go): a crafted value on a
		// copied/imported workflow could otherwise redirect the request,
		// and the API token with it, to an attacker-controlled host.
		if !jiraDomainPattern.MatchString(domain) {
			return "pipedrive_skipped_invalid_domain", ErrActionSkipped
		}
		base = "https://" + domain + ".pipedrive.com"
	}
	payload := map[string]any{"content": resolveMessage(node, rc)}
	// Pipedrive's Notes API documents deal_id/person_id as integers --
	// resolveTemplate always returns a string, so send them as real JSON
	// numbers (via strconv.Atoi) rather than quoted strings, which a strict
	// server-side type check would otherwise reject with a 400. A ref that
	// doesn't resolve to a number is dropped rather than sent malformed.
	if dealID := resolveTemplate(configVal(node, "pipedriveDealID", ""), rc); dealID != "" {
		if n, err := strconv.Atoi(dealID); err == nil {
			payload["deal_id"] = n
		}
	}
	if personID := resolveTemplate(configVal(node, "pipedrivePersonID", ""), rc); personID != "" {
		if n, err := strconv.Atoi(personID); err == nil {
			payload["person_id"] = n
		}
	}
	target := base + "/api/v1/notes?api_token=" + url.QueryEscape(token)
	return postJSON(ctx, target, nil, payload, "pipedrive_note_created", "Pipedrive")
}
