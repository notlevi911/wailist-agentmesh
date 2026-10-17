package nodes

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/oauthcred"
)

// GoogleConfig is threaded from runner.go exactly like TendrilConfig --
// Google connector nodes need DB + encryption-key access to resolve a live
// access token, which the generic RunContexter doesn't carry.
type GoogleConfig struct {
	Store        oauthcred.Store
	EncryptKey   string
	ClientID     string
	ClientSecret string
	// UserID scopes which connected account a node is allowed to use --
	// see googleAccessToken's ownership check.
	UserID string
}

// SetGoogleAPIBasesForTest overrides the three Google product API bases
// ("gmail", "sheets", "calendar" in apiBaseDefaults) at once. Call only from
// tests. Pass all "" to reset to the real APIs; a single "" restores just that
// product's real base.
func SetGoogleAPIBasesForTest(gmail, sheets, calendar string) {
	setAPIBaseForTest("gmail", gmail)
	setAPIBaseForTest("sheets", sheets)
	setAPIBaseForTest("calendar", calendar)
}

// SetGoogleTokenEndpointForTest overrides the OAuth2 token-refresh endpoint
// googleAccessToken refreshes an expired credential against. Call only from
// tests. Pass "" to reset to the real endpoint.
func SetGoogleTokenEndpointForTest(base string) { setAPIBaseForTest("google_token", base) }

// removedGoogleTemplates are templates dropped with their Google "restricted"
// scopes (gmail.readonly, drive.readonly), which cannot pass Google's OAuth
// verification without a paid annual CASA assessment. Workflows saved before
// the removal still hold such nodes; they fail with a reason the user can act
// on, before any credential lookup or token refresh.
var removedGoogleTemplates = map[string]string{
	"gmail_list":     "Gmail read access",
	"gmail_get":      "Gmail read access",
	"drive_list":     "Drive access",
	"drive_get":      "Drive access",
	"drive_download": "Drive access",
}

// ExecuteGoogle dispatches to the Gmail/Sheets/Calendar connector matching
// node.Template's prefix -- one node type covering all three products (see
// models.NodeTypeGoogle's doc comment) since they share the identical
// OAuth-credential-lookup mechanics and differ only in which API they call.
func ExecuteGoogle(ctx context.Context, node models.WorkflowNode, rc RunContexter, cfg GoogleConfig) (any, error) {
	if access, removed := removedGoogleTemplates[node.Template]; removed {
		return nil, fmt.Errorf("google: %s has been removed (AgentMesh no longer requests %s); remove this node", node.Template, access)
	}
	switch {
	case strings.HasPrefix(node.Template, "gmail_"):
		return executeGmail(ctx, node, rc, cfg)
	case strings.HasPrefix(node.Template, "sheets_"):
		return executeSheets(ctx, node, rc, cfg)
	case strings.HasPrefix(node.Template, "calendar_"):
		return executeCalendar(ctx, node, rc, cfg)
	}
	return nil, fmt.Errorf("google: unknown template %q", node.Template)
}

// googleAccessToken resolves node.Config["oauthCredentialID"] to a live
// bearer token, refreshing it first if needed. The ownership check here is
// load-bearing, not defensive dressing: a node's Config is workflow data a
// user (or, once workflow-sharing/templates exist, someone else's copied
// workflow) controls, so without explicitly checking cred.UserID against
// the RUN's actual owner, any workflow could use any user's Gmail/Sheets/
// Calendar by supplying a credential ID it doesn't own — the oauthcred
// package itself is provider/user-agnostic and deliberately doesn't make
// this check, since it has no notion of "the workflow calling it."
func googleAccessToken(ctx context.Context, node models.WorkflowNode, cfg GoogleConfig) (string, error) {
	credID := configVal(node, "oauthCredentialID", "")
	if credID == "" {
		return "", fmt.Errorf("google: no connected account selected -- connect one in the node's Inspector")
	}
	cred, err := cfg.Store.GetOAuthCredential(ctx, credID)
	if err != nil {
		return "", fmt.Errorf("google: connected account not found -- it may have been disconnected")
	}
	if cred.UserID != cfg.UserID {
		return "", fmt.Errorf("google: connected account does not belong to this user")
	}
	if cred.Provider != "google" {
		return "", fmt.Errorf("google: connected account is not a Google credential")
	}
	return oauthcred.GetValidAccessToken(ctx, cfg.Store, cfg.EncryptKey, cred, oauthcred.ProviderConfig{
		TokenURL:     apiBase("google_token"),
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
	})
}

func bearerHeader(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

// googleGET builds and runs an authenticated GET, returning the decoded
// JSON body via getJSON (connector_helpers.go) -- shared by every read
// operation across the Google products below.
func googleGET(ctx context.Context, target, token, serviceName string) (any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: build request: %w", serviceName, err)
	}
	for k, v := range bearerHeader(token) {
		req.Header.Set(k, v)
	}
	return getJSON(req, serviceName)
}

// ── Gmail ──────────────────────────────────────────────────────────────────

func executeGmail(ctx context.Context, node models.WorkflowNode, rc RunContexter, cfg GoogleConfig) (any, error) {
	token, err := googleAccessToken(ctx, node, cfg)
	if err != nil {
		return nil, err
	}

	switch node.Template {
	case "gmail_send", "gmail_reply":
		// resolveTemplate, not plain configVal: the Inspector's own hint text
		// for gmailThreadID below suggests "{{ result.threadId }}", so these
		// fields have to actually go through the resolver or that documented
		// usage silently sends the literal placeholder text to Gmail instead.
		to := resolveTemplate(configVal(node, "gmailTo", ""), rc)
		if to == "" {
			return "gmail_skipped_no_recipient", ErrActionSkipped
		}
		subject := resolveTemplate(configVal(node, "gmailSubject", "AgentMesh workflow result"), rc)
		if node.Template == "gmail_reply" && !strings.HasPrefix(strings.ToLower(subject), "re:") {
			subject = "Re: " + subject
		}
		body, _, _ := googleOutgoingText(node, rc)
		payload := map[string]any{"raw": buildRFC2822Message(to, subject, body)}
		if node.Template == "gmail_reply" {
			if threadID := resolveTemplate(configVal(node, "gmailThreadID", ""), rc); threadID != "" {
				payload["threadId"] = threadID
			}
		}
		req, err := newJSONRequest(ctx, http.MethodPost, apiBase("gmail")+"/users/me/messages/send", bearerHeader(token), payload)
		if err != nil {
			return nil, fmt.Errorf("Gmail: %w", err)
		}
		return doAndCheck(req, "gmail_sent", "Gmail")
	}
	return nil, fmt.Errorf("google: unknown gmail template %q", node.Template)
}

// buildRFC2822Message builds a minimal plain-text RFC 2822 message and
// base64url-encodes it (no padding) the way Gmail's send endpoint requires
// for its "raw" field. Deliberately simple: no RFC 2047 subject encoding
// for non-ASCII, no HTML/multipart body, no attachments -- matches what
// n8n's own basic "Send" operation covers, not a full mail client.
func buildRFC2822Message(to, subject, body string) string {
	// to/subject are now resolveTemplate'd (an upstream node's output can
	// land here via {{ node.<id> }} / {{ result }}), so a stray \r or \n in
	// either would otherwise let that upstream value splice extra header
	// lines into the raw message -- e.g. an LLM reply containing a literal
	// newline followed by "Bcc: ..." would silently blind-copy an address
	// the workflow author never configured. Strip embedded CR/LF from both
	// before they ever reach the header block; body is unaffected; it's
	// already correctly separated by the blank line above.
	to = stripCRLF(to)
	subject = stripCRLF(subject)
	msg := "To: " + to + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"Content-Type: text/plain; charset=\"UTF-8\"\r\n\r\n" +
		body
	return base64.RawURLEncoding.EncodeToString([]byte(msg))
}

// stripCRLF removes carriage returns and line feeds from a value destined
// for a raw email header line, where an embedded newline would otherwise be
// interpreted as the start of a new (attacker- or upstream-controlled)
// header.
func stripCRLF(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	return strings.ReplaceAll(s, "\n", "")
}

// ── Google Sheets ────────────────────────────────────────────────────────

func executeSheets(ctx context.Context, node models.WorkflowNode, rc RunContexter, cfg GoogleConfig) (any, error) {
	token, err := googleAccessToken(ctx, node, cfg)
	if err != nil {
		return nil, err
	}
	spreadsheetID := resolveTemplate(configVal(node, "sheetsSpreadsheetID", ""), rc)
	if spreadsheetID == "" {
		return "sheets_skipped_no_spreadsheet_id", ErrActionSkipped
	}
	rangeA1 := resolveTemplate(configVal(node, "sheetsRange", "A1:Z1000"), rc)

	switch node.Template {
	case "sheets_read":
		// PathEscape, not QueryEscape -- rangeA1 sits in the URL PATH
		// (/values/{range}), not a query string. QueryEscape encodes a
		// space as '+', which Google's server never decodes back to a
		// space in a path segment, so any range/sheet name containing a
		// space (e.g. a sheet literally named "My Sheet") silently
		// targeted the wrong (nonexistent) range.
		target := apiBase("sheets") + "/" + url.PathEscape(spreadsheetID) + "/values/" + url.PathEscape(rangeA1)
		return googleGET(ctx, target, token, "Google Sheets")

	case "sheets_append":
		target := apiBase("sheets") + "/" + url.PathEscape(spreadsheetID) + "/values/" + url.PathEscape(rangeA1) + ":append?valueInputOption=USER_ENTERED"
		msg, _, _ := googleOutgoingText(node, rc)
		// A row is an array of cell values. If the upstream message is
		// already a JSON array (e.g. via a {{ result.someArrayField }}
		// template), append it as one row of that many cells; otherwise
		// fall back to one single-cell row holding the whole message --
		// the simplest possible "append a row" behavior when the input
		// isn't already tabular.
		values := [][]any{{msg}}
		var parsed any
		if json.Unmarshal([]byte(msg), &parsed) == nil {
			if arr, ok := parsed.([]any); ok {
				values = [][]any{arr}
			}
		}
		req, err := newJSONRequest(ctx, http.MethodPost, target, bearerHeader(token), map[string]any{"values": values})
		if err != nil {
			return nil, fmt.Errorf("Google Sheets: %w", err)
		}
		return doAndCheck(req, "sheets_row_appended", "Google Sheets")
	}
	return nil, fmt.Errorf("google: unknown sheets template %q", node.Template)
}

// ── Google Calendar ──────────────────────────────────────────────────────

func executeCalendar(ctx context.Context, node models.WorkflowNode, rc RunContexter, cfg GoogleConfig) (any, error) {
	token, err := googleAccessToken(ctx, node, cfg)
	if err != nil {
		return nil, err
	}
	calendarID := resolveTemplate(configVal(node, "calendarID", "primary"), rc)

	switch node.Template {
	case "calendar_list":
		target := apiBase("calendar") + "/" + url.PathEscape(calendarID) + "/events"
		return googleGET(ctx, target, token, "Google Calendar")

	case "calendar_create":
		summary, _, _ := googleOutgoingText(node, rc)
		startISO := resolveTemplate(configVal(node, "calendarStart", ""), rc)
		endISO := resolveTemplate(configVal(node, "calendarEnd", ""), rc)
		if startISO == "" || endISO == "" {
			return "calendar_skipped_missing_times", ErrActionSkipped
		}
		payload := map[string]any{
			"summary": summary,
			"start":   map[string]any{"dateTime": startISO},
			"end":     map[string]any{"dateTime": endISO},
		}
		target := apiBase("calendar") + "/" + url.PathEscape(calendarID) + "/events"
		req, err := newJSONRequest(ctx, http.MethodPost, target, bearerHeader(token), payload)
		if err != nil {
			return nil, fmt.Errorf("Google Calendar: %w", err)
		}
		return doAndCheck(req, "calendar_event_created", "Google Calendar")
	}
	return nil, fmt.Errorf("google: unknown calendar template %q", node.Template)
}

// googleOutgoingText is the content a Google write step sends -- an email's
// body, an appended row, an event's summary -- whether the template sends any
// at all, and whether that content came from a template. The one place this
// decision lives: the connector above sends it, and the builder's dry run
// reports it as what a simulated step would have carried, so the two cannot
// disagree. Read-only templates send nothing.
func googleOutgoingText(node models.WorkflowNode, rc RunContexter) (text string, sends, templated bool) {
	switch node.Template {
	case "gmail_send", "gmail_reply", "sheets_append":
		return resolveMessage(node, rc), true, configVal(node, messageTemplateKey, "") != ""
	case "calendar_create":
		// The event's own summary wins; the run's message only fills in for
		// a summary that is unset or resolves to nothing.
		if summary := resolveTemplate(configVal(node, "calendarSummary", ""), rc); summary != "" {
			return summary, true, true
		}
		return resolveMessage(node, rc), true, configVal(node, messageTemplateKey, "") != ""
	}
	return "", false, false
}

// GoogleOutgoingForDryRun exports googleOutgoingText for the builder's dry run.
func GoogleOutgoingForDryRun(node models.WorkflowNode, rc RunContexter) (text string, sends, templated bool) {
	return googleOutgoingText(node, rc)
}
