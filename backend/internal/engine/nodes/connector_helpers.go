package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/agentmesh/backend/internal/models"
)

// secretVal reads a per-connector credential stored on the node's Secrets map.
// Returns "" if the node has no Secrets map or the key is unset.
func secretVal(node models.WorkflowNode, key string) string {
	if node.Secrets == nil {
		return ""
	}
	return node.Secrets[key]
}

// configVal reads a per-connector non-secret setting from the node's Config map,
// falling back to def when unset.
func configVal(node models.WorkflowNode, key, def string) string {
	if node.Config == nil || node.Config[key] == "" {
		return def
	}
	return node.Config[key]
}

// messageTemplateKey is the Config key a message-sending connector (Slack,
// Discord, Telegram, GitHub, Notion, ...) reads an optional template from.
// Empty (the default) preserves every such connector's original behavior
// exactly: send rc.Message() verbatim.
const messageTemplateKey = "messageTemplate"

// resolveMessage is what a connector should call instead of rc.Message()
// directly, so every one of them picks up template support for free. See
// resolveTemplate (resolve.go) for the placeholder syntax.
func resolveMessage(node models.WorkflowNode, rc RunContexter) string {
	tmpl := configVal(node, messageTemplateKey, "")
	if tmpl == "" {
		return rc.Message()
	}
	return resolveTemplate(tmpl, rc)
}

// ResolveMessageForTest is a test-only exported wrapper, used by
// connector_helpers_test.go (package nodes_test) to test resolveMessage
// without exporting it from the package's real API.
func ResolveMessageForTest(node models.WorkflowNode, rc RunContexter) string {
	return resolveMessage(node, rc)
}

// newJSONRequest builds a JSON request with the given method, target, and headers.
// Content-Type is always application/json; extraHeaders may add Authorization etc.
func newJSONRequest(ctx context.Context, method, target string, extraHeaders map[string]string, payload any) (*http.Request, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	return req, nil
}

// newFormRequest builds a POST request with a form-urlencoded body --
// shared by Stripe and Twilio, the two connectors whose APIs take
// form-encoded bodies rather than JSON, so the url.Values/Content-Type
// sequence has one implementation instead of two independently maintained
// copies. Auth (Bearer header, Basic auth, ...) is the caller's own concern:
// set it on the returned request before sending, the same as newJSONRequest.
func newFormRequest(ctx context.Context, target string, form url.Values) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req, nil
}

// postJSON POSTs payload as JSON to target and returns sentinel on success.
func postJSON(ctx context.Context, target string, extraHeaders map[string]string, payload any, sentinel, serviceName string) (any, error) {
	req, err := newJSONRequest(ctx, http.MethodPost, target, extraHeaders, payload)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", serviceName, err)
	}
	return doAndCheck(req, sentinel, serviceName)
}

// doValidatedRequest runs req through the SSRF guard and executes it on
// the shared toolHTTPClient, wrapping any transport failure with a
// redacted URL. Callers own reading and closing resp.Body — use this for
// callers that need the raw response (doAndCheck wraps it for the common
// sentinel-on-success case).
//
// A transport failure on req is wrapped Retryable when req.Method is
// idempotent (GET/HEAD/PUT/DELETE/OPTIONS) -- the exact same
// isIdempotentHTTPMethod reasoning callHTTP (tool.go) already applies to a
// plain HTTP Tool node's own transport failures: nothing non-repeatable
// can have happened server-side for one of these methods, so retrying is
// safe. This is every connector's shared low-level HTTP call, so gating on
// the request's own method here (rather than per-connector) automatically
// makes every current and future GET-based connector (RSS, OpenWeatherMap,
// Calendly, Telegram getUpdates, Google Sheets reads, ...) retryable
// without touching each one, while every POST-based send (Slack, Jira,
// Gmail, ...) stays correctly non-retryable -- a retry after an ambiguous
// POST failure could double-send a message/ticket/email, which is exactly
// what marking it Retryable would risk.
func doValidatedRequest(req *http.Request, serviceName string) (*http.Response, error) {
	if err := urlValidator(req.URL.String()); err != nil {
		return nil, err
	}
	resp, err := toolHTTPClient.Do(req)
	if err != nil {
		wrapped := fmt.Errorf("%s: request to %s failed: %w", serviceName, redactedURL(req.URL), unwrapURLError(err))
		if isIdempotentHTTPMethod(req.Method) {
			return nil, Retryable(wrapped)
		}
		return nil, wrapped
	}
	return resp, nil
}

// retryableIfIdempotent wraps err Retryable when method is idempotent,
// mirroring doValidatedRequest's own transport-failure reasoning above for
// the 5xx-with-a-response case: a >=500 status on a GET/HEAD/PUT/DELETE/
// OPTIONS request is safe to retry the same way a dropped connection is,
// while the identical status on a POST is not, since the server may have
// already acted on it. Shared by getJSON/getRaw/doAndCheck below so their
// >=500 branches can't drift from doValidatedRequest's own gating.
func retryableIfIdempotent(err error, method string) error {
	if isIdempotentHTTPMethod(method) {
		return Retryable(err)
	}
	return err
}

// readErrorBody reads a bounded excerpt of a non-2xx response body for the
// error message, then drains any remainder so the underlying connection can
// be returned to the transport's idle pool instead of being torn down on
// Close() — the same reason doAndCheck's success path drains in full.
func readErrorBody(resp *http.Response) string {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, httpResponseLimit))
	io.Copy(io.Discard, resp.Body)
	return string(b)
}

// doAndCheck executes req on the shared toolHTTPClient, treats any status >= 400
// as an error carrying a bounded body excerpt, and returns sentinel otherwise.
func doAndCheck(req *http.Request, sentinel, serviceName string) (any, error) {
	resp, err := doValidatedRequest(req, serviceName)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		apiErr := fmt.Errorf("%s API %d: %s", serviceName, resp.StatusCode, readErrorBody(resp))
		// >=500 only, matching callHTTP's (tool.go) own distinction: a 4xx
		// is a client error, retrying sends the identical broken request
		// again, never retryable regardless of method.
		if resp.StatusCode >= 500 {
			return nil, retryableIfIdempotent(apiErr, req.Method)
		}
		return nil, apiErr
	}
	io.Copy(io.Discard, resp.Body)
	return sentinel, nil
}

// getJSON executes req on the shared toolHTTPClient and decodes a successful
// JSON response body into `any`, returning it to the caller. Unlike
// doAndCheck -- which discards the body and hands back a fixed sentinel --
// this is for connector operations that read data (list/get) rather than
// just report success, so the result can flow to the next node.
func getJSON(req *http.Request, serviceName string) (any, error) {
	resp, err := doValidatedRequest(req, serviceName)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		apiErr := fmt.Errorf("%s API %d: %s", serviceName, resp.StatusCode, readErrorBody(resp))
		if resp.StatusCode >= 500 {
			return nil, retryableIfIdempotent(apiErr, req.Method)
		}
		return nil, apiErr
	}
	b, err := readBounded(resp.Body, httpResponseLimit)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", serviceName, err)
	}
	var result any
	if err := json.Unmarshal(b, &result); err != nil {
		return nil, fmt.Errorf("%s: decode response: %w", serviceName, err)
	}
	return result, nil
}

// GetJSONForTest is a test-only exported wrapper for getJSON, used by
// connector_helpers_test.go (package nodes_test) to test the unexported
// helper without exporting it from the package's real API.
func GetJSONForTest(req *http.Request, serviceName string) (any, error) {
	return getJSON(req, serviceName)
}

// mediaResponseLimit bounds binary media payloads (e.g. generated audio),
// distinct from httpResponseLimit which only bounds error-body excerpts.
//
// Kept well below what the underlying API allows: a node's result — audio
// included, base64-encoded — is JSON-marshaled into a run-log DB row and
// broadcast over the run's SSE stream in full, and the frontend log viewer
// renders it inline with no truncation. 5 MiB of audio (~6.7 MiB base64,
// ~5 min at 128kbps) keeps a single action node's worst case from bloating
// the DB or freezing that viewer; it is not meant as a generation-quality
// limit. Longer-form audio needs the node to return a storage reference
// instead of inline bytes, which is a larger change to the run-log/SSE
// pipeline than this connector alone should make.
const mediaResponseLimit = 5 << 20 // 5 MiB (~5 min of 128kbps audio)

// readBounded reads r fully but errors if it exceeds limit bytes, instead
// of silently truncating like io.ReadAll(io.LimitReader(r, limit)) would.
func readBounded(r io.Reader, limit int) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("response exceeds %d byte limit", limit)
	}
	return data, nil
}

// redactedURL renders just a URL's scheme and host, so request-failure errors
// never echo credentials embedded in the query string (e.g. Trello's
// key/token params) or the path (e.g. Telegram's bot token) into logs or SSE
// run output. The path/query never help diagnose a transport failure anyway —
// serviceName already identifies which connector failed.
func redactedURL(u *url.URL) string {
	c := *u
	c.User = nil
	c.Path = ""
	c.RawPath = ""
	c.RawQuery = ""
	c.Fragment = ""
	c.RawFragment = ""
	return c.String()
}

// unwrapURLError returns the underlying transport error for a *url.Error,
// whose own Error() string embeds the full request URL (including any query
// string). Returning the wrapped reason instead of err itself keeps error
// messages informative without leaking query-string credentials.
func unwrapURLError(err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		return uerr.Err
	}
	return err
}

// basicAuthHeader builds an Authorization: Basic header map from a
// username:password pair (RFC 7617).
func basicAuthHeader(user, pass string) map[string]string {
	// Built by net/http's own SetBasicAuth (RFC 7617) rather than by hand, so
	// the header can never drift from what a *http.Request would send (#12).
	req := http.Request{Header: http.Header{}}
	req.SetBasicAuth(user, pass)
	return map[string]string{"Authorization": req.Header.Get("Authorization")}
}

// apiBaseDefaults holds each connector's real API base URL, keyed by service
// name. A "" default means the connector builds its host per node from user
// config (a Shopify store, a Jira domain, a Mailchimp datacenter) and only
// reads apiBase to find out whether a test has pointed it at a local server.
//
// Together with apiBase and setAPIBaseForTest, this replaces the hand-written
// `var xAPIBase = "..."` plus eight-line SetXAPIBaseForTest pair every
// connector used to carry (#12). The exported SetXAPIBaseForTest functions
// remain as one-line wrappers, so no test call site changes.
var apiBaseDefaults = map[string]string{
	// action.go: email providers
	"resend":   "https://api.resend.com",
	"sendgrid": "https://api.sendgrid.com",
	"brevo":    "https://api.brevo.com",
	"postmark": "https://api.postmarkapp.com",

	// connectors_business.go
	"intercom":    "https://api.intercom.io",
	"openweather": "https://api.openweathermap.org",
	"calendly":    "https://api.calendly.com",
	"shopify":     "", // order notes: host built per node from the shop domain
	"baserow":     "https://api.baserow.io",

	// connectors_commerce.go
	"stripe":           "https://api.stripe.com",
	"shopify_customer": "", // host built per node from the store name
	"pipedrive":        "", // host built per node from the company domain

	// connectors_data.go
	"hubspot":   "https://api.hubapi.com",
	"mailchimp": "", // host built per node from the account's datacenter

	// connectors_devtools.go
	"github":       "https://api.github.com",
	"jira":         "", // host built per node: {domain}.atlassian.net, or api.atlassian.com for OAuth
	"linear":       "https://api.linear.app",
	"gitlab_oauth": "https://gitlab.com", // fixed on purpose, see SetGitLabOAuthAPIBaseForTest

	// connectors_feed.go
	"hackernews": "https://hn.algolia.com/api/v1",
	"coingecko":  "https://api.coingecko.com/api/v3",

	// connectors_media.go
	"elevenlabs": "https://api.elevenlabs.io",

	// connectors_messaging.go
	"slack":    "https://slack.com",
	"telegram": "https://api.telegram.org",

	// connectors_ops.go
	"twilio":    "https://api.twilio.com/2010-04-01",
	"pagerduty": "https://events.pagerduty.com",
	"zendesk":   "", // host built per node from the subdomain
	"monday":    "https://api.monday.com",

	// connectors_productivity.go
	"notion":   "https://api.notion.com",
	"airtable": "https://api.airtable.com",
	"trello":   "https://api.trello.com",
	"asana":    "https://app.asana.com",
	"clickup":  "https://api.clickup.com",
	"todoist":  "https://api.todoist.com",

	// google.go
	"gmail":        "https://gmail.googleapis.com/gmail/v1",
	"sheets":       "https://sheets.googleapis.com/v4/spreadsheets",
	"calendar":     "https://www.googleapis.com/calendar/v3/calendars",
	"google_token": "https://oauth2.googleapis.com/token",
}

// apiBases holds each service's current, possibly test-overridden, base URL,
// seeded from apiBaseDefaults. Guarded by apiBasesMu: a run executes
// connectors from concurrent goroutines, and Go aborts the process on a map
// read that races a write, where the old per-connector string vars only raced
// silently.
var (
	apiBasesMu sync.RWMutex
	apiBases   = func() map[string]string {
		m := make(map[string]string, len(apiBaseDefaults))
		for k, v := range apiBaseDefaults {
			m[k] = v
		}
		return m
	}()
)

// apiBase returns service's current base URL: its real API, unless a test
// has overridden it. "" for a per-node-host connector with no override.
func apiBase(service string) string {
	apiBasesMu.RLock()
	defer apiBasesMu.RUnlock()
	return apiBases[service]
}

// setAPIBaseForTest points service at base; "" restores its default. Panics
// on a service with no apiBaseDefaults entry, so a typo in a new wrapper fails
// the first test that calls it instead of silently overriding nothing.
func setAPIBaseForTest(service, base string) {
	def, ok := apiBaseDefaults[service]
	if !ok {
		panic("setAPIBaseForTest: unknown service " + service)
	}
	if base == "" {
		base = def
	}
	apiBasesMu.Lock()
	defer apiBasesMu.Unlock()
	apiBases[service] = base
}

// issueTitle derives a short title from a longer message: its first non-blank
// line, capped at 120 runes, falling back to a generic title when blank.
func issueTitle(message string) string {
	line := message
	if i := strings.IndexByte(message, '\n'); i >= 0 {
		line = message[:i]
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "AgentMesh workflow result"
	}
	r := []rune(line)
	if len(r) > 120 {
		return string(r[:120])
	}
	return line
}

// PostJSONForTest and IssueTitleForTest are test-only exported wrappers, used by
// connector_helpers_test.go (package nodes_test) to test the unexported helpers
// above without exporting them from the package's real API.
func PostJSONForTest(ctx context.Context, target string, extraHeaders map[string]string, payload any, sentinel, serviceName string) (any, error) {
	return postJSON(ctx, target, extraHeaders, payload, sentinel, serviceName)
}

func IssueTitleForTest(message string) string {
	return issueTitle(message)
}

func ReadBoundedForTest(r io.Reader, limit int) ([]byte, error) {
	return readBounded(r, limit)
}

// doAndDecode is getJSON under the name the GraphQL/Monday.com call sites
// already use — kept as a thin alias rather than two copies of the same
// validate/status-check/readBounded/unmarshal sequence that could silently
// drift apart on a future fix to either one.
func doAndDecode(req *http.Request, serviceName string) (any, error) {
	return getJSON(req, serviceName)
}

// getAndDecode GETs target and returns the decoded JSON body.
func getAndDecode(ctx context.Context, target string, extraHeaders map[string]string, serviceName string) (any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", serviceName, err)
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	return doAndDecode(req, serviceName)
}

// getRaw GETs target and returns the raw body, bounded by httpResponseLimit.
// Used by connectors whose payload is not JSON (RSS/Atom feeds).
func getRaw(ctx context.Context, target string, extraHeaders map[string]string, serviceName string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", serviceName, err)
	}
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	resp, err := doValidatedRequest(req, serviceName)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		apiErr := fmt.Errorf("%s API %d: %s", serviceName, resp.StatusCode, readErrorBody(resp))
		if resp.StatusCode >= 500 {
			return nil, retryableIfIdempotent(apiErr, req.Method)
		}
		return nil, apiErr
	}
	return io.ReadAll(io.LimitReader(resp.Body, httpResponseLimit))
}

// GetAndDecodeForTest exposes getAndDecode to the external nodes_test package.
func GetAndDecodeForTest(ctx context.Context, target string, h map[string]string, svc string) (any, error) {
	return getAndDecode(ctx, target, h, svc)
}
