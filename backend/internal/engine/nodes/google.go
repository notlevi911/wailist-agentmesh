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

// gmailAPIBase/sheetsAPIBase/calendarAPIBase/driveAPIBase/googleTokenEndpoint
// are overridden in tests via SetGoogleAPIBasesForTest /
// SetGoogleTokenEndpointForTest, matching the SetXAPIBaseForTest pattern
// used throughout this package.
var (
	gmailAPIBase        = "https://gmail.googleapis.com/gmail/v1"
	sheetsAPIBase       = "https://sheets.googleapis.com/v4/spreadsheets"
	calendarAPIBase     = "https://www.googleapis.com/calendar/v3/calendars"
	driveAPIBase        = "https://www.googleapis.com/drive/v3/files"
	googleTokenEndpoint = "https://oauth2.googleapis.com/token"
)

// SetGoogleAPIBasesForTest overrides all four Google product API bases at
// once. Call only from tests. Pass all "" to reset to the real APIs.
func SetGoogleAPIBasesForTest(gmail, sheets, calendar, drive string) {
	if gmail == "" && sheets == "" && calendar == "" && drive == "" {
		gmailAPIBase = "https://gmail.googleapis.com/gmail/v1"
		sheetsAPIBase = "https://sheets.googleapis.com/v4/spreadsheets"
		calendarAPIBase = "https://www.googleapis.com/calendar/v3/calendars"
		driveAPIBase = "https://www.googleapis.com/drive/v3/files"
		return
	}
	gmailAPIBase = gmail
	sheetsAPIBase = sheets
	calendarAPIBase = calendar
	driveAPIBase = drive
}

// SetGoogleTokenEndpointForTest overrides the OAuth2 token-refresh endpoint
// googleAccessToken refreshes an expired credential against. Call only from
// tests. Pass "" to reset to the real endpoint.
func SetGoogleTokenEndpointForTest(base string) {
	if base == "" {
		googleTokenEndpoint = "https://oauth2.googleapis.com/token"
	} else {
		googleTokenEndpoint = base
	}
}

// ExecuteGoogle dispatches to the Gmail/Sheets/Calendar/Drive connector
// matching node.Template's prefix -- one node type covering all four
// products (see models.NodeTypeGoogle's doc comment) since they share the
// identical OAuth-credential-lookup mechanics and differ only in which API
// they call.
func ExecuteGoogle(ctx context.Context, node models.WorkflowNode, rc RunContexter, cfg GoogleConfig) (any, error) {
	switch {
	case strings.HasPrefix(node.Template, "gmail_"):
		return executeGmail(ctx, node, rc, cfg)
	case strings.HasPrefix(node.Template, "sheets_"):
		return executeSheets(ctx, node, rc, cfg)
	case strings.HasPrefix(node.Template, "calendar_"):
		return executeCalendar(ctx, node, rc, cfg)
	case strings.HasPrefix(node.Template, "drive_"):
		return executeDrive(ctx, node, rc, cfg)
	}
	return nil, fmt.Errorf("google: unknown template %q", node.Template)
}

// googleAccessToken resolves node.Config["oauthCredentialID"] to a live
// bearer token, refreshing it first if needed. The ownership check here is
// load-bearing, not defensive dressing: a node's Config is workflow data a
// user (or, once workflow-sharing/templates exist, someone else's copied
// workflow) controls, so without explicitly checking cred.UserID against
// the RUN's actual owner, any workflow could read any user's Gmail/Sheets/
// Calendar/Drive by supplying a credential ID it doesn't own — the oauthcred
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
		TokenURL:     googleTokenEndpoint,
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
	})
}

func bearerHeader(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

// googleGET builds and runs an authenticated GET, returning the decoded
// JSON body via getJSON (connector_helpers.go) -- shared by every read
// operation across all four Google products below.
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
	case "gmail_list":
		q := url.Values{}
		if query := resolveTemplate(configVal(node, "gmailQuery", ""), rc); query != "" {
			q.Set("q", query)
		}
		if max := resolveTemplate(configVal(node, "gmailMaxResults", ""), rc); max != "" {
			q.Set("maxResults", max)
		}
		target := gmailAPIBase + "/users/me/messages"
		if len(q) > 0 {
			target += "?" + q.Encode()
		}
		return googleGET(ctx, target, token, "Gmail")

	case "gmail_get":
		id := resolveTemplate(configVal(node, "gmailMessageID", ""), rc)
		if id == "" {
			return "gmail_skipped_no_message_id", ErrActionSkipped
		}
		target := gmailAPIBase + "/users/me/messages/" + url.PathEscape(id)
		raw, err := googleGET(ctx, target, token, "Gmail")
		if err != nil {
			return nil, err
		}
		return decodeGmailMessage(raw), nil

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
		body := resolveMessage(node, rc)
		payload := map[string]any{"raw": buildRFC2822Message(to, subject, body)}
		if node.Template == "gmail_reply" {
			if threadID := resolveTemplate(configVal(node, "gmailThreadID", ""), rc); threadID != "" {
				payload["threadId"] = threadID
			}
		}
		req, err := newJSONRequest(ctx, http.MethodPost, gmailAPIBase+"/users/me/messages/send", bearerHeader(token), payload)
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

// decodeGmailMessage flattens Gmail's real response shape (id/threadId/
// snippet plus a deeply nested payload.headers[]/payload.parts[] MIME tree)
// into the handful of fields a workflow actually wants -- so {{ result.body
// }} / {{ result.subject }} / {{ result.from }} (see connector_helpers.go's
// resolveTemplate) can pick them out directly instead of the next node
// having to know Gmail's own schema.
func decodeGmailMessage(raw any) any {
	m, ok := raw.(map[string]any)
	if !ok {
		return raw
	}
	payload, _ := m["payload"].(map[string]any)
	headers, _ := payload["headers"].([]any)
	header := func(name string) string {
		for _, h := range headers {
			hm, ok := h.(map[string]any)
			if !ok {
				continue
			}
			if n, _ := hm["name"].(string); strings.EqualFold(n, name) {
				v, _ := hm["value"].(string)
				return v
			}
		}
		return ""
	}
	return map[string]any{
		"id":       m["id"],
		"threadId": m["threadId"],
		"snippet":  m["snippet"],
		"subject":  header("Subject"),
		"from":     header("From"),
		"to":       header("To"),
		"date":     header("Date"),
		"body":     extractPlainTextBody(payload),
	}
}

// extractPlainTextBody walks a Gmail MessagePart tree depth-first for the
// first text/plain part and decodes its base64url body.data. Real messages
// are usually multipart/alternative (text/plain + text/html siblings) or
// multipart/mixed (with attachments) -- the recursion into "parts" handles
// both without needing to know which shape a given message used.
func extractPlainTextBody(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	if mimeType, _ := payload["mimeType"].(string); strings.HasPrefix(mimeType, "text/plain") {
		if b, ok := payload["body"].(map[string]any); ok {
			if data, ok := b["data"].(string); ok {
				if decoded, err := decodeGmailBase64(data); err == nil {
					return decoded
				}
			}
		}
	}
	parts, _ := payload["parts"].([]any)
	for _, p := range parts {
		if pm, ok := p.(map[string]any); ok {
			if body := extractPlainTextBody(pm); body != "" {
				return body
			}
		}
	}
	return ""
}

// decodeGmailBase64 handles both padded and unpadded base64url, since real
// Gmail responses aren't fully consistent about padding.
func decodeGmailBase64(s string) (string, error) {
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return string(b), nil
	}
	b, err := base64.URLEncoding.DecodeString(s)
	return string(b), err
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
		target := sheetsAPIBase + "/" + url.PathEscape(spreadsheetID) + "/values/" + url.PathEscape(rangeA1)
		return googleGET(ctx, target, token, "Google Sheets")

	case "sheets_append":
		target := sheetsAPIBase + "/" + url.PathEscape(spreadsheetID) + "/values/" + url.PathEscape(rangeA1) + ":append?valueInputOption=USER_ENTERED"
		msg := resolveMessage(node, rc)
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
		target := calendarAPIBase + "/" + url.PathEscape(calendarID) + "/events"
		return googleGET(ctx, target, token, "Google Calendar")

	case "calendar_create":
		summary := resolveTemplate(configVal(node, "calendarSummary", ""), rc)
		if summary == "" {
			summary = resolveMessage(node, rc)
		}
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
		target := calendarAPIBase + "/" + url.PathEscape(calendarID) + "/events"
		req, err := newJSONRequest(ctx, http.MethodPost, target, bearerHeader(token), payload)
		if err != nil {
			return nil, fmt.Errorf("Google Calendar: %w", err)
		}
		return doAndCheck(req, "calendar_event_created", "Google Calendar")
	}
	return nil, fmt.Errorf("google: unknown calendar template %q", node.Template)
}

// ── Google Drive ─────────────────────────────────────────────────────────
//
// Read-only (list/get metadata/download) — matches the drive.readonly scope
// requested in handlers/oauth2creds.go's googleConnectorScopes. Upload
// needs drive.file or the full drive scope, deliberately not requested:
// narrower scope keeps Google's app-review surface smaller, and this can be
// added later as its own scope bump if upload is actually needed.

func executeDrive(ctx context.Context, node models.WorkflowNode, rc RunContexter, cfg GoogleConfig) (any, error) {
	token, err := googleAccessToken(ctx, node, cfg)
	if err != nil {
		return nil, err
	}

	switch node.Template {
	case "drive_list":
		q := url.Values{}
		if query := resolveTemplate(configVal(node, "driveQuery", ""), rc); query != "" {
			q.Set("q", query)
		}
		target := driveAPIBase
		if len(q) > 0 {
			target += "?" + q.Encode()
		}
		return googleGET(ctx, target, token, "Google Drive")

	case "drive_get":
		id := resolveTemplate(configVal(node, "driveFileID", ""), rc)
		if id == "" {
			return "drive_skipped_no_file_id", ErrActionSkipped
		}
		return googleGET(ctx, driveAPIBase+"/"+url.PathEscape(id), token, "Google Drive")

	case "drive_download":
		id := resolveTemplate(configVal(node, "driveFileID", ""), rc)
		if id == "" {
			return "drive_skipped_no_file_id", ErrActionSkipped
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, driveAPIBase+"/"+url.PathEscape(id)+"?alt=media", nil)
		if err != nil {
			return nil, fmt.Errorf("Google Drive: build request: %w", err)
		}
		for k, v := range bearerHeader(token) {
			req.Header.Set(k, v)
		}
		resp, err := doValidatedRequest(req, "Google Drive")
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			apiErr := fmt.Errorf("Google Drive API %d: %s", resp.StatusCode, readErrorBody(resp))
			if resp.StatusCode >= 500 {
				// GET, so idempotent -- see doValidatedRequest's own
				// isIdempotentHTTPMethod reasoning in connector_helpers.go.
				return nil, retryableIfIdempotent(apiErr, req.Method)
			}
			return nil, apiErr
		}
		// Same limit and same base64-in-JSON shape as the ElevenLabs audio
		// connector (connectors_media.go) -- a proven precedent for
		// carrying binary content through a node's JSON output.
		data, err := readBounded(resp.Body, mediaResponseLimit)
		if err != nil {
			return nil, fmt.Errorf("Google Drive: read file: %w", err)
		}
		return map[string]any{
			"status":        "drive_file_downloaded",
			"contentBase64": base64.StdEncoding.EncodeToString(data),
		}, nil
	}
	return nil, fmt.Errorf("google: unknown drive template %q", node.Template)
}
