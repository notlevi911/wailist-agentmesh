package nodes

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/agentmesh/backend/internal/models"
)

// twilioAPIBase is overridden in tests via SetTwilioAPIBaseForTest.
var twilioAPIBase = "https://api.twilio.com/2010-04-01"

// SetTwilioAPIBaseForTest overrides the Twilio API base URL. Call only from
// tests. Pass "" to reset to the real API.
func SetTwilioAPIBaseForTest(base string) {
	if base == "" {
		twilioAPIBase = "https://api.twilio.com/2010-04-01"
	} else {
		twilioAPIBase = base
	}
}

// sendTwilio sends the run output as an SMS. Twilio uses HTTP Basic auth
// (Account SID / Auth Token) and form encoding.
func sendTwilio(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	token := secretVal(node, "twilioAuthToken")
	if token == "" {
		return "twilio_skipped_no_auth_token", ErrActionSkipped
	}
	// Config is where the Inspector saves this field today, but a node saved
	// before this PR has it under Secrets (master's prior schema) -- fall
	// back there so already-configured Twilio nodes don't silently break on
	// deploy. Deliberately in Config, not Secrets, even though Config isn't
	// masked by handlers/secrets.go: unlike twilioAuthToken (the real
	// credential, correctly kept in Secrets), an Account SID is designed by
	// Twilio to be semi-public -- it appears in every API request URL and
	// isn't a rotation-sensitive value on its own.
	sid := configVal(node, "twilioAccountSID", "")
	if sid == "" {
		sid = secretVal(node, "twilioAccountSID")
	}
	if sid == "" {
		return "twilio_skipped_no_account_sid", ErrActionSkipped
	}
	to := resolveTemplate(configVal(node, "twilioTo", ""), rc)
	if to == "" {
		return "twilio_skipped_no_recipient", ErrActionSkipped
	}
	from := resolveTemplate(configVal(node, "twilioFrom", ""), rc)
	if from == "" {
		return "twilio_skipped_no_sender", ErrActionSkipped
	}
	form := url.Values{}
	form.Set("To", to)
	form.Set("From", from)
	form.Set("Body", resolveMessage(node, rc))

	req, err := newFormRequest(ctx, twilioAPIBase+"/Accounts/"+url.PathEscape(sid)+"/Messages.json", form)
	if err != nil {
		return nil, fmt.Errorf("Twilio: %w", err)
	}
	req.SetBasicAuth(sid, token)
	return doAndCheck(req, "twilio_sms_sent", "Twilio")
}

// sendMattermost posts the run output to a Mattermost incoming webhook. The
// webhook URL is the credential — there is no separate token.
func sendMattermost(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	hookURL := secretVal(node, "mattermostWebhookURL")
	if hookURL == "" {
		return "mattermost_skipped_no_webhook_url", ErrActionSkipped
	}
	payload := map[string]any{"text": resolveMessage(node, rc)}
	if ch := resolveTemplate(configVal(node, "mattermostChannel", ""), rc); ch != "" {
		payload["channel"] = ch
	}
	if user := resolveTemplate(configVal(node, "mattermostUsername", ""), rc); user != "" {
		payload["username"] = user
	}
	return postJSON(ctx, hookURL, nil, payload, "mattermost_sent", "Mattermost")
}

// pagerdutyAPIBase is overridden in tests via SetPagerDutyAPIBaseForTest.
var pagerdutyAPIBase = "https://events.pagerduty.com"

// SetPagerDutyAPIBaseForTest overrides the PagerDuty Events API base URL.
// Call only from tests. Pass "" to reset to the real API.
func SetPagerDutyAPIBaseForTest(base string) {
	if base == "" {
		pagerdutyAPIBase = "https://events.pagerduty.com"
	} else {
		pagerdutyAPIBase = base
	}
}

// sendPagerDuty triggers a PagerDuty Events API v2 alert with the run output
// as the incident summary.
func sendPagerDuty(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	routingKey := secretVal(node, "pagerdutyRoutingKey")
	if routingKey == "" {
		return "pagerduty_skipped_no_routing_key", ErrActionSkipped
	}
	// issueTitle, not the raw message: PagerDuty's Events API v2 rejects a
	// summary over 1024 chars outright, and a multi-KB LLM result would hit
	// that cap right when an alert matters most. Default severity is "info",
	// matching every already-deployed node that never set this explicitly --
	// "error" would silently escalate all of their alerts.
	payload := map[string]any{
		"routing_key":  routingKey,
		"event_action": "trigger",
		"payload": map[string]any{
			"summary":  issueTitle(resolveMessage(node, rc)),
			"severity": resolveTemplate(configVal(node, "pagerdutySeverity", "info"), rc),
			"source":   resolveTemplate(configVal(node, "pagerdutySource", "agentmesh"), rc),
		},
	}
	return postJSON(ctx, pagerdutyAPIBase+"/v2/enqueue", nil, payload,
		"pagerduty_event_triggered", "PagerDuty")
}

// zendeskAPIBase is overridden in tests via SetZendeskAPIBaseForTest.
// Normally "https://{subdomain}.zendesk.com" is built per-node, so the test
// override replaces the whole scheme+host and sendZendesk skips that
// construction when it is set. Same shape as mailchimpAPIBase.
var zendeskAPIBase = ""

// SetZendeskAPIBaseForTest overrides the Zendesk API base URL entirely.
// Call only from tests. Pass "" to reset to the real per-subdomain host.
func SetZendeskAPIBaseForTest(base string) { zendeskAPIBase = base }

// sendZendesk opens a support ticket with the run output as the first comment.
// Zendesk authenticates with Basic auth where the username is
// "{email}/token" and the password is the API token.
func sendZendesk(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	token := secretVal(node, "zendeskAPIToken")
	if token == "" {
		return "zendesk_skipped_no_api_token", ErrActionSkipped
	}
	subdomain := resolveTemplate(configVal(node, "zendeskSubdomain", ""), rc)
	email := resolveTemplate(configVal(node, "zendeskEmail", ""), rc)
	if subdomain == "" || email == "" {
		return "zendesk_skipped_missing_config", ErrActionSkipped
	}
	// subdomain is user-supplied config interpolated directly into the
	// request host below -- validate it first (mirrors jiraDomainPattern's
	// own reasoning in connectors_devtools.go), otherwise a crafted value on
	// a copied/imported workflow could redirect the request, and the API
	// token with it, to an attacker-controlled host.
	if !jiraDomainPattern.MatchString(subdomain) {
		return "zendesk_skipped_invalid_subdomain", ErrActionSkipped
	}
	base := zendeskAPIBase
	if base == "" {
		base = "https://" + url.PathEscape(subdomain) + ".zendesk.com"
	}
	msg := resolveMessage(node, rc)
	payload := map[string]any{"ticket": map[string]any{
		"subject": issueTitle(msg),
		"comment": map[string]any{"body": msg},
	}}
	req, err := newJSONRequest(ctx, http.MethodPost, base+"/api/v2/tickets.json", nil, payload)
	if err != nil {
		return nil, fmt.Errorf("Zendesk: %w", err)
	}
	req.SetBasicAuth(email+"/token", token)
	return doAndCheck(req, "zendesk_ticket_created", "Zendesk")
}

// mondayAPIBase is overridden in tests via SetMondayAPIBaseForTest.
var mondayAPIBase = "https://api.monday.com"

// SetMondayAPIBaseForTest overrides the Monday.com API base URL. Call only
// from tests. Pass "" to reset to the real API.
func SetMondayAPIBaseForTest(base string) {
	if base == "" {
		mondayAPIBase = "https://api.monday.com"
	} else {
		mondayAPIBase = base
	}
}

// mondayCreateItem is the GraphQL mutation Monday.com's v2 API takes. Board
// IDs are ID! and item names String! — passed as variables rather than
// interpolated, so a message containing quotes cannot break the query.
const mondayCreateItem = `mutation ($boardId: ID!, $itemName: String!) {
  create_item(board_id: $boardId, item_name: $itemName) { id }
}`

// sendMonday creates a Monday.com board item named after the run output.
func sendMonday(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	apiKey := secretVal(node, "mondayAPIKey")
	if apiKey == "" {
		return "monday_skipped_no_api_key", ErrActionSkipped
	}
	boardID := resolveTemplate(configVal(node, "mondayBoardID", ""), rc)
	if boardID == "" {
		return "monday_skipped_no_board_id", ErrActionSkipped
	}
	payload := map[string]any{
		"query": mondayCreateItem,
		"variables": map[string]any{
			"boardId":  boardID,
			"itemName": resolveMessage(node, rc),
		},
	}
	// Monday.com expects the bare token, with no "Bearer " prefix.
	headers := map[string]string{"Authorization": apiKey, "API-Version": "2023-10"}
	req, err := newJSONRequest(ctx, http.MethodPost, mondayAPIBase+"/v2", headers, payload)
	if err != nil {
		return nil, fmt.Errorf("Monday.com: %w", err)
	}
	out, err := doAndDecode(req, "Monday.com")
	if err != nil {
		return nil, err
	}
	// Monday.com is a GraphQL API: it reports failures (bad board ID, no
	// write access, expired key) as HTTP 200 with an "errors" array, same as
	// sendGraphQL guards against -- returning that as success would render a
	// green node for an item that was never created.
	if err := checkGraphQLErrors(out, "Monday.com"); err != nil {
		return nil, err
	}
	return "monday_item_created", nil
}
