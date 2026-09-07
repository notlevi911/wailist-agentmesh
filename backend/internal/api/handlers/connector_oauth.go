package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"

	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/respond"
)

// ConnectorOAuthConfig describes one connector's OAuth 2.0 app registration.
// ClientIDEnvVal/ClientSecretEnvVal hold the already-resolved env var values
// (read once in main.go into Deps), not the env var names themselves.
type ConnectorOAuthConfig struct {
	AuthURL            string
	TokenURL           string
	Scope              string
	UsesPKCE           bool
	ClientIDEnvVal     string
	ClientSecretEnvVal string

	// TokenAuthStyle selects how exchangeConnectorCode presents client
	// credentials to TokenURL. "" (default) puts client_id/client_secret in
	// the form body, per the generic OAuth 2.0 authorization_code grant.
	// "basic" sends them instead as an HTTP Basic Authorization header and
	// omits them from the form body, which Notion's token endpoint requires.
	TokenAuthStyle string

	// ExtraAuthParams are added verbatim to the /authorize redirect's query
	// string, for providers that need fixed parameters beyond the generic
	// set below (e.g. Notion's required owner=user).
	ExtraAuthParams map[string]string

	// TokenBodyStyle selects how exchangeConnectorCode encodes the token
	// exchange request body. "" (default) sends
	// application/x-www-form-urlencoded, per the generic OAuth 2.0
	// authorization_code grant. "json" sends the same fields as a JSON
	// object instead with a Content-Type: application/json request, which
	// Atlassian's token endpoint requires — per
	// developer.atlassian.com/cloud/jira/platform/oauth-2-3lo-apps/ its
	// example exchange is a JSON body, not form-encoded, unlike every other
	// provider in this registry.
	TokenBodyStyle string

	// PostExchangeHook, when non-nil, runs once right after a successful
	// token exchange and its returned map is merged into the node's Config
	// alongside the token linkConnectorToken already writes. Jira and
	// Mailchimp need this: neither one's access token is directly usable
	// against any URL derivable from config the user typed in (unlike every
	// other provider here) — each first requires a separate authenticated
	// call to discover, respectively, the cloudId to address the Jira REST
	// API through api.atlassian.com/ex/jira/{cloudId}/, or the dc
	// (datacenter) suffix to address Mailchimp's API through
	// {dc}.api.mailchimp.com. Left nil for every provider that doesn't need
	// an extra post-exchange call.
	PostExchangeHook func(ctx context.Context, accessToken string) (map[string]string, error)
}

// testProviderOverrides holds provider configs injected by tests via
// SetConnectorProviderForTest, entirely separate from real Deps fields.
// registerConnectorProviders (below) merges these in first, then Tasks 3-14
// each add their real connector's entry (backed by that connector's actual
// Deps client id/secret fields) into the map it builds and returns. It is
// called once per request from the two handlers below so it always reflects
// the current Deps.
var testProviderOverrides = map[string]ConnectorOAuthConfig{}

// SetConnectorProviderForTest and ClearConnectorProviderForTest let tests
// register a fake provider without touching real Deps fields. Test-only.
func SetConnectorProviderForTest(name string, cfg ConnectorOAuthConfig) {
	testProviderOverrides[name] = cfg
}

func ClearConnectorProviderForTest(name string) {
	delete(testProviderOverrides, name)
}

// connectorTokenURLOverridesForTest lets a test point one real, already-wired
// provider's TokenURL (e.g. "airtable") at a local fake HTTP server, without
// touching any other field of that entry — so a test can exercise the actual
// registry entry (Scope, UsesPKCE, TokenAuthStyle, and Deps-sourced client
// id/secret all included) through ConnectorOAuthStart/Callback instead of
// re-declaring a look-alike config via SetConnectorProviderForTest. Test-only.
var connectorTokenURLOverridesForTest = map[string]string{}

func SetConnectorTokenURLForTest(name, url string) {
	connectorTokenURLOverridesForTest[name] = url
}

func ClearConnectorTokenURLForTest(name string) {
	delete(connectorTokenURLOverridesForTest, name)
}

// registerConnectorProviders builds the provider registry from Deps' client
// id/secret fields. Real provider entries (slack, github, notion, ...) are
// added to this map's returned literal by Tasks 3–14 as each connector is
// wired up.
func (d *Deps) registerConnectorProviders() map[string]ConnectorOAuthConfig {
	out := map[string]ConnectorOAuthConfig{}
	for k, v := range testProviderOverrides {
		out[k] = v
	}
	// Tasks 5-14 each append one entry here, e.g.:
	// out["notion"] = ConnectorOAuthConfig{AuthURL: ..., ClientIDEnvVal: d.NotionOAuthClientID, ...}
	out["slack"] = ConnectorOAuthConfig{
		AuthURL: "https://slack.com/oauth/v2/authorize", TokenURL: "https://slack.com/api/oauth.v2.access",
		Scope: "chat:write", ClientIDEnvVal: d.SlackOAuthClientID, ClientSecretEnvVal: d.SlackOAuthClientSecret,
	}
	// repo is required (not a finer-grained scope) because classic GitHub
	// OAuth Apps only support coarse-grained scopes, and issue creation needs
	// write access to the repo. This is a distinct provider key/app from the
	// pre-existing "github" login OAuth app used for sign-in.
	out["github"] = ConnectorOAuthConfig{
		AuthURL: "https://github.com/login/oauth/authorize", TokenURL: "https://github.com/login/oauth/access_token",
		Scope: "repo", ClientIDEnvVal: d.GitHubConnectorClientID, ClientSecretEnvVal: d.GitHubConnectorClientSecret,
	}
	// Notion has no OAuth scope string at all (consent is page-level, granted
	// by which pages the user picks to share), requires owner=user on the
	// authorize redirect, and its token endpoint wants client credentials as
	// HTTP Basic auth rather than form fields — see connectors_productivity.go's
	// sendNotion and TokenAuthStyle's doc comment above.
	out["notion"] = ConnectorOAuthConfig{
		AuthURL: "https://api.notion.com/v1/oauth/authorize", TokenURL: "https://api.notion.com/v1/oauth/token",
		TokenAuthStyle: "basic", ExtraAuthParams: map[string]string{"owner": "user"},
		ClientIDEnvVal: d.NotionClientID, ClientSecretEnvVal: d.NotionClientSecret,
	}
	// Airtable requires PKCE for every client (no confidential-client
	// exemption), hence UsesPKCE here — this is the first provider entry to
	// actually exercise Task 1's PKCE code path.
	//
	// KNOWN GAP: Airtable access tokens expire after ~60 minutes and the
	// token response includes a refresh_token (valid ~60 days). Re-exchanging
	// that refresh token and writing the refreshed access token back onto the
	// node via Store.UpdateWorkflow before each connector call is NOT
	// implemented here — deliberately out of scope for this task. Without it,
	// this connector will silently stop working about an hour after linking.
	// Follow-up task must add refresh support.
	// Airtable is a confidential client (it has a client_secret at all), so
	// like Notion above its token endpoint requires Basic auth and rejects
	// client_id/client_secret in the form body — don't copy this style onto a
	// future public/no-secret connector that doesn't need it.
	out["airtable"] = ConnectorOAuthConfig{
		AuthURL: "https://airtable.com/oauth2/v1/authorize", TokenURL: "https://airtable.com/oauth2/v1/token",
		Scope: "data.records:write", UsesPKCE: true, TokenAuthStyle: "basic",
		ClientIDEnvVal: d.AirtableClientID, ClientSecretEnvVal: d.AirtableClientSecret,
	}
	// HubSpot's CRM object model binds Notes to Contacts rather than giving
	// notes their own scope — developers.hubspot.com/docs/api/crm/notes lists
	// crm.objects.contacts.read/.write as the scope requirement for this
	// endpoint (including POST), not a crm.objects.notes.* scope, so don't
	// "fix" this back to the more obvious-looking guess. HubSpot's token
	// endpoint takes client_id/client_secret as regular form fields (no
	// TokenAuthStyle needed), unlike Notion/Airtable above.
	//
	// KNOWN GAP: HubSpot access tokens expire after ~30 minutes and the token
	// response includes a refresh_token. Re-exchanging that refresh token and
	// writing the refreshed access token back onto the node via
	// Store.UpdateWorkflow before each connector call is NOT implemented here
	// — deliberately out of scope for this task, same as Airtable's gap
	// above. Without it, this connector will silently stop working about half
	// an hour after linking. Follow-up task must add refresh support.
	out["hubspot"] = ConnectorOAuthConfig{
		AuthURL: "https://app.hubspot.com/oauth/authorize", TokenURL: "https://api.hubapi.com/oauth/v1/token",
		Scope: "crm.objects.contacts.write", ClientIDEnvVal: d.HubSpotClientID, ClientSecretEnvVal: d.HubSpotClientSecret,
	}
	// developers.asana.com/docs/oauth: "default" is Asana's own documented
	// special scope value (full permissions, for apps registered without
	// finer-grained scopes) — not a placeholder left over from scaffolding.
	// PKCE is optional there, and the token endpoint takes client_id/
	// client_secret as regular form fields (no TokenAuthStyle needed), same as
	// Slack/GitHub/HubSpot above, unlike Notion/Airtable's Basic auth.
	//
	// KNOWN GAP: Asana access tokens expire after ~60 minutes and the token
	// response includes a refresh_token. Re-exchanging that refresh token and
	// writing the refreshed access token back onto the node via
	// Store.UpdateWorkflow before each connector call is NOT implemented here
	// — deliberately out of scope for this task, same as Airtable/HubSpot's
	// gaps above. Without it, this connector will silently stop working about
	// an hour after linking. Follow-up task must add refresh support.
	out["asana"] = ConnectorOAuthConfig{
		AuthURL: "https://app.asana.com/-/oauth_authorize", TokenURL: "https://app.asana.com/-/oauth_token",
		Scope: "default", ClientIDEnvVal: d.AsanaClientID, ClientSecretEnvVal: d.AsanaClientSecret,
	}
	// developer.clickup.com/docs/authentication: ClickUp has no OAuth scope
	// concept at all (consent is per-Workspace, granted by which Workspace the
	// user picks on the authorize screen, not a scope string), and its
	// /oauth/token endpoint takes client_id/client_secret/code as regular
	// form-body fields (no TokenAuthStyle needed), same as Slack/GitHub/
	// HubSpot/Asana above. PKCE is not offered. The authorize URL really is
	// just https://app.clickup.com/api?client_id=...&redirect_uri=...  — not
	// a conventional /oauth/authorize-shaped path — confirmed against the docs
	// above, not a guess.
	//
	// No KNOWN GAP here: per that same page, "The access token currently does
	// not expire" and no refresh_token is issued, unlike Airtable/HubSpot/
	// Asana above, so there is no refresh gap to document for this connector.
	out["clickup"] = ConnectorOAuthConfig{
		AuthURL: "https://app.clickup.com/api", TokenURL: "https://api.clickup.com/api/v2/oauth/token",
		ClientIDEnvVal: d.ClickUpClientID, ClientSecretEnvVal: d.ClickUpClientSecret,
	}
	// developer.atlassian.com/cloud/jira/platform/oauth-2-3lo-apps/: audience
	// and prompt are both marked "(required)" on the /authorize redirect (not
	// just audience, which is the only one most write-ups mention), and the
	// token endpoint's own documented example is a JSON request body, not the
	// form-encoded body every other provider in this registry uses — hence
	// TokenBodyStyle: "json" (see its doc comment). "write:jira-work" is a
	// real documented classic scope ("Create and manage issues"), not a typo
	// for the granular write:issue:jira scope.
	//
	// Jira's OAuth access token is not usable directly against
	// https://{domain}.atlassian.net the way the manual API-token path in
	// sendJira works — it must instead be sent to
	// https://api.atlassian.com/ex/jira/{cloudId}/..., and cloudId is only
	// discoverable via a separate authenticated call to
	// /oauth/token/accessible-resources made once right after the token
	// exchange. PostExchangeHook (see its doc comment on ConnectorOAuthConfig)
	// carries that lookup and writes its result into
	// node.Config["jiraOAuthCloudID"].
	//
	// KNOWN GAP: unlike Airtable/HubSpot/Asana's refresh tokens above, which
	// stay valid across multiple uses until they expire, Atlassian's refresh
	// tokens ROTATE on every use — the docs state that using a refresh token
	// disables it and issues a new one in the same response. A future refresh
	// implementation here must persist the NEW refresh token every single
	// time it refreshes, not just the new access token, or the very next
	// refresh attempt will fail against an already-invalidated token. Same
	// deliberately-deferred scope as the other KNOWN GAP entries above.
	out["jira"] = ConnectorOAuthConfig{
		AuthURL: "https://auth.atlassian.com/authorize", TokenURL: "https://auth.atlassian.com/oauth/token",
		Scope: "write:jira-work", TokenBodyStyle: "json",
		ExtraAuthParams:  map[string]string{"audience": "api.atlassian.com", "prompt": "consent"},
		PostExchangeHook: jiraPostExchangeHook,
		ClientIDEnvVal:   d.JiraClientID, ClientSecretEnvVal: d.JiraClientSecret,
	}
	// linear.app/developers/oauth-2-0-authentication: PKCE is supported but
	// not required, and the token endpoint accepts client_id/client_secret
	// as regular form-body fields (Basic auth is offered as an alternative,
	// but not required, so no TokenAuthStyle needed) — same shape as Slack/
	// GitHub/HubSpot/Asana/ClickUp above. "issues:create" is used instead of
	// the broader "write" scope because sendLinear only ever creates issues
	// and Linear documents issues:create as its own narrower scope for
	// exactly that action.
	//
	// KNOWN GAP: Linear OAuth access tokens expire after 24 hours and the
	// token response includes a refresh_token. Re-exchanging that refresh
	// token and writing the refreshed access token back onto the node via
	// Store.UpdateWorkflow before each connector call is NOT implemented
	// here — deliberately out of scope for this task, same as Airtable/
	// HubSpot/Asana's gaps above. Without it, this connector will silently
	// stop working a day after linking. Follow-up task must add refresh
	// support.
	out["linear"] = ConnectorOAuthConfig{
		AuthURL: "https://linear.app/oauth/authorize", TokenURL: "https://api.linear.app/oauth/token",
		Scope: "issues:create", ClientIDEnvVal: d.LinearClientID, ClientSecretEnvVal: d.LinearClientSecret,
	}
	// mailchimp.com/developer/marketing/guides/access-user-data-oauth-2/:
	// Mailchimp has no scope concept (consent is account-wide) and no PKCE, and
	// its token endpoint takes client_id/client_secret as regular form-body
	// fields (no TokenAuthStyle needed) — same shape as Slack/GitHub/HubSpot/
	// Asana/ClickUp/Linear above.
	//
	// Mailchimp's OAuth access token isn't usable directly against any
	// derivable API host the way sendMailchimp's manual-key path works —
	// manual keys embed their datacenter suffix directly (<key>-<dc>), but an
	// OAuth token carries no such suffix. It must instead be sent, once right
	// after exchange, to https://login.mailchimp.com/oauth2/metadata, whose
	// JSON response's dc field gives the same suffix. PostExchangeHook (see
	// its doc comment) carries that lookup and writes its result into
	// node.Config["mailchimpOAuthDC"], same shape as Jira's cloudId lookup
	// above.
	//
	// No KNOWN GAP here: per the same docs, "Mailchimp Marketing access
	// tokens do not expire, so you don't need to use a refresh_token" — no
	// refresh_token is even issued, unlike Airtable/HubSpot/Asana/Linear
	// above, so there is no refresh gap to document for this connector.
	out["mailchimp"] = ConnectorOAuthConfig{
		AuthURL: "https://login.mailchimp.com/oauth2/authorize", TokenURL: "https://login.mailchimp.com/oauth2/token",
		PostExchangeHook: mailchimpPostExchangeHook,
		ClientIDEnvVal:   d.MailchimpClientID, ClientSecretEnvVal: d.MailchimpClientSecret,
	}
	// docs.gitlab.com/ee/api/oauth2.html: "api" is GitLab's own documented
	// scope for "complete read/write access to the API" — there is no
	// narrower scope that covers issue creation, unlike some other providers
	// in this registry. PKCE is offered as the more-secure option there but
	// not required for a confidential client (one with a client_secret), and
	// the token endpoint takes client_id/client_secret as regular form-body
	// fields (no TokenAuthStyle needed) — same shape as Slack/GitHub/HubSpot/
	// Asana/ClickUp/Linear above, unlike Notion/Airtable's Basic auth.
	//
	// This app is registered against gitlab.com itself, so the resulting
	// token is only ever valid there; self-hosted GitLab OAuth-linking is out
	// of scope for this task (see gitlabOAuthAPIBase's doc comment in
	// connectors_devtools.go's sendGitLab — the OAuth branch there hardcodes
	// gitlab.com and never reads the node's configurable gitlabBaseURL).
	//
	// KNOWN GAP: GitLab OAuth access tokens expire after ~2 hours and the
	// token response includes a refresh_token. Re-exchanging that refresh
	// token and writing the refreshed access token back onto the node via
	// Store.UpdateWorkflow before each connector call is NOT implemented here
	// — deliberately out of scope for this task, same as Airtable/HubSpot/
	// Asana/Linear's gaps above. Without it, this connector will silently
	// stop working about two hours after linking. Follow-up task must add
	// refresh support.
	//
	// Additionally, same as Atlassian/Jira's KNOWN GAP above: per GitLab's own
	// OAuth docs (docs.gitlab.com/ee/api/oauth2.html), refresh tokens ROTATE
	// on every use — refreshing invalidates the existing access_token and
	// refresh_token and returns a new pair. A future refresh implementation
	// here must persist the NEW refresh token every single time it refreshes,
	// not just the new access token, or the very next refresh attempt will
	// fail against an already-invalidated token.
	out["gitlab"] = ConnectorOAuthConfig{
		AuthURL: "https://gitlab.com/oauth/authorize", TokenURL: "https://gitlab.com/oauth/token",
		Scope: "api", ClientIDEnvVal: d.GitLabClientID, ClientSecretEnvVal: d.GitLabClientSecret,
	}
	// developer.todoist.com/guides/#oauth: the brief's starting URLs were both
	// wrong-subdomain guesses — the authorize endpoint is on app.todoist.com
	// (not todoist.com) and the token endpoint is on api.todoist.com (not
	// todoist.com); corrected here. "task:add" is confirmed as the exact,
	// narrowest documented scope name covering sendTodoist's only action
	// (creating a task). This app is a confidential client registered with a
	// client_id/client_secret via Todoist's App Management Console, not the
	// separate public-client "OAuth Client ID Metadata Document" flow, so
	// PKCE is not required here (that flow is the only one requiring it).
	// The token endpoint's documented example sends client_id/client_secret/
	// code/redirect_uri as a plain form body (client_secret_post, the
	// documented default) — same shape as Slack/GitHub/HubSpot/Asana/
	// ClickUp/Linear/GitLab above, so no TokenAuthStyle override needed.
	//
	// Confirmed against the same docs: OAuth access tokens and manual
	// personal API tokens both authenticate identically via "Authorization:
	// Bearer <token>", so sendTodoist needs only a preference swap, no
	// header-format branching (unlike Airtable/Trello-style pairs).
	//
	// KNOWN GAP, correcting the brief's "tokens do not expire" assumption:
	// that is only true of legacy Todoist apps created before refresh tokens
	// existed. Per the docs, "Refresh tokens are enabled by default for
	// newly-created applications" and "Access tokens issued to these
	// applications expire one hour after they are issued" — since this app
	// will be newly created (no live client_id/secret exist yet), its tokens
	// WILL expire hourly and DO need refresh. Re-exchanging the refresh token
	// and persisting the result via Store.UpdateWorkflow before each
	// connector call is NOT implemented here — deliberately out of scope for
	// this task, same as Airtable/HubSpot/Asana/Linear/GitLab's gaps above.
	// Also same as GitLab/Jira above: Todoist's refresh tokens rotate on
	// every use, so any future refresh implementation must persist the NEW
	// refresh token every time, not just the new access token.
	out["todoist"] = ConnectorOAuthConfig{
		AuthURL: "https://app.todoist.com/oauth/authorize", TokenURL: "https://api.todoist.com/oauth/access_token",
		Scope: "task:add", ClientIDEnvVal: d.TodoistClientID, ClientSecretEnvVal: d.TodoistClientSecret,
	}
	for name, url := range connectorTokenURLOverridesForTest {
		if cfg, ok := out[name]; ok {
			cfg.TokenURL = url
			out[name] = cfg
		}
	}
	return out
}

func (d *Deps) connectorProviderConfig(name string) (ConnectorOAuthConfig, bool) {
	cfg, ok := d.registerConnectorProviders()[name]
	if !ok || cfg.ClientIDEnvVal == "" || cfg.ClientSecretEnvVal == "" {
		return ConnectorOAuthConfig{}, false
	}
	return cfg, true
}

func connectorSecretKey(provider string) string {
	return provider + "OAuthAccessToken"
}

func connectorRefreshKey(provider string) string {
	return provider + "OAuthRefreshToken"
}

func connectorExpiresConfigKey(provider string) string {
	return provider + "OAuthExpiresAt"
}

// connectorLinkClaims is signed into the OAuth `state` param so the callback
// can recover which node this authorization is for without a server-side
// pending-link table. The matching HttpOnly cookie (connectorStateCookie)
// provides CSRF binding: state must equal the cookie's own value, so a
// forged start-URL clicked by a victim can't complete using an attacker's
// authorization against the victim's own node.
//
// The PKCE code_verifier deliberately does NOT live here. This state JWT is
// also sent as the `state` query parameter on the redirect to the
// third-party's /authorize endpoint — a front-channel value that can end up
// in the provider's server logs, browser history, or a leaked referrer (and,
// via chi's request logger, our own server logs). HS256 signing gives
// integrity, not confidentiality, so anything placed in these claims should
// be treated as readable by whoever observes that URL. The verifier instead
// travels in its own separate HttpOnly cookie (see connectorVerifierCookieName)
// that's never echoed back to the provider.
//
// Because this JWT is signed with the same secret as session tokens and
// shares the session claims' `sub` shape, it is minted with
// Issuer: ConnectorLinkIssuer and the auth middleware rejects any bearer
// token carrying that issuer — otherwise a leaked state value would work as
// a full session bearer token for connectorLinkTTL.
type connectorLinkClaims struct {
	UserID     string `json:"sub"`
	WorkflowID string `json:"wf"`
	NodeID     string `json:"node"`
	Provider   string `json:"provider"`
	jwt.RegisteredClaims
}

const connectorLinkTTL = 10 * time.Minute

// ConnectorLinkIssuer marks state JWTs as scoped to the OAuth link flow, not
// a session. It's signed with the same secret as session tokens (this JWT
// travels on the front channel to the provider and can't carry a secret of
// its own), so the auth middleware must reject any token carrying this
// issuer — otherwise a leaked state value (provider logs, Referer, or our
// own access logs) would work as a full session bearer token.
const ConnectorLinkIssuer = "connector-link"

func connectorStateCookieName(provider string) string {
	return "connector_oauth_state_" + provider
}

// connectorVerifierCookieName names the separate HttpOnly cookie that carries
// the raw PKCE code_verifier across the Start -> Callback round-trip. Kept
// out of the signed state JWT (see connectorLinkClaims) because that JWT
// also travels as the `state` query param on the front channel to the
// provider; this cookie never leaves the browser-to-our-server path.
func connectorVerifierCookieName(provider string) string {
	return "connector_oauth_verifier_" + provider
}

// ConnectorOAuthStart redirects the browser to the provider's consent screen
// for linking workflowId/nodeId's connector node to the caller's account on
// that provider. Requires JWT auth (mounted in the protected router group) —
// the caller must be signed in, and must own the workflow being linked.
func (d *Deps) ConnectorOAuthStart(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "provider")
	cfg, ok := d.connectorProviderConfig(provider)
	if !ok {
		respond.Error(w, http.StatusNotFound, "unknown or unconfigured connector")
		return
	}

	userID, _ := r.Context().Value(CtxUserID).(string)
	workflowID := r.URL.Query().Get("workflowId")
	nodeID := r.URL.Query().Get("nodeId")
	if workflowID == "" || nodeID == "" {
		respond.Error(w, http.StatusBadRequest, "workflowId and nodeId are required")
		return
	}

	wf, err := d.Store.GetWorkflow(r.Context(), workflowID)
	if err != nil || wf.UserID != userID {
		respond.Error(w, http.StatusNotFound, "workflow not found")
		return
	}
	found := false
	for _, n := range wf.Nodes {
		if n.ID == nodeID {
			found = true
		}
	}
	if !found {
		respond.Error(w, http.StatusNotFound, "node not found")
		return
	}

	var verifier, challenge string
	if cfg.UsesPKCE {
		verifier, err = randURLSafe(64)
		if err != nil {
			d.connectorRedirectFail(w, r, workflowID, "internal")
			return
		}
		sum := sha256.Sum256([]byte(verifier))
		challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	}

	claims := connectorLinkClaims{
		UserID: userID, WorkflowID: workflowID, NodeID: nodeID, Provider: provider,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    ConnectorLinkIssuer,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(connectorLinkTTL)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	state, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(d.JWTSecret))
	if err != nil {
		d.connectorRedirectFail(w, r, workflowID, "internal")
		return
	}

	secure := strings.HasPrefix(d.BaseURL, "https")
	http.SetCookie(w, &http.Cookie{
		Name: connectorStateCookieName(provider), Value: state, Path: "/",
		MaxAge: int(connectorLinkTTL.Seconds()), HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
	if cfg.UsesPKCE {
		http.SetCookie(w, &http.Cookie{
			Name: connectorVerifierCookieName(provider), Value: verifier, Path: "/",
			MaxAge: int(connectorLinkTTL.Seconds()), HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
		})
	}

	q := url.Values{}
	q.Set("client_id", cfg.ClientIDEnvVal)
	q.Set("redirect_uri", d.connectorRedirectURI(provider))
	if cfg.Scope != "" {
		q.Set("scope", cfg.Scope)
	}
	q.Set("state", state)
	q.Set("response_type", "code")
	if cfg.UsesPKCE {
		q.Set("code_challenge", challenge)
		q.Set("code_challenge_method", "S256")
	}
	for k, v := range cfg.ExtraAuthParams {
		q.Set(k, v)
	}
	http.Redirect(w, r, cfg.AuthURL+"?"+q.Encode(), http.StatusFound)
}

// ConnectorOAuthCallback verifies state, exchanges the code, and writes the
// resulting token onto the target node's Secrets/Config maps.
func (d *Deps) ConnectorOAuthCallback(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "provider")
	cfg, ok := d.connectorProviderConfig(provider)
	if !ok {
		respond.Error(w, http.StatusNotFound, "unknown or unconfigured connector")
		return
	}

	cookieName := connectorStateCookieName(provider)
	cookie, err := r.Cookie(cookieName)
	stateParam := r.URL.Query().Get("state")
	if err != nil || cookie.Value == "" ||
		subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(stateParam)) != 1 {
		d.connectorRedirectFail(w, r, "", "invalid_state")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: strings.HasPrefix(d.BaseURL, "https"), SameSite: http.SameSiteLaxMode,
	})

	claims := &connectorLinkClaims{}
	_, err = jwt.ParseWithClaims(stateParam, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return []byte(d.JWTSecret), nil
	})
	if err != nil || claims.Provider != provider {
		if err != nil {
			log.Printf("connector oauth %s: state token: %v", provider, err)
		} else {
			log.Printf("connector oauth %s: state provider mismatch (claims=%s)", provider, claims.Provider)
		}
		d.connectorRedirectFail(w, r, "", "invalid_state")
		return
	}

	userID, _ := r.Context().Value(CtxUserID).(string)
	if userID != claims.UserID {
		d.connectorRedirectFail(w, r, claims.WorkflowID, "session_mismatch")
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		d.connectorRedirectFail(w, r, claims.WorkflowID, "no_code")
		return
	}

	var verifier string
	if cfg.UsesPKCE {
		verifierCookieName := connectorVerifierCookieName(provider)
		verifierCookie, err := r.Cookie(verifierCookieName)
		if err != nil || verifierCookie.Value == "" {
			d.connectorRedirectFail(w, r, claims.WorkflowID, "invalid_state")
			return
		}
		verifier = verifierCookie.Value
		http.SetCookie(w, &http.Cookie{
			Name: verifierCookieName, Value: "", Path: "/", MaxAge: -1,
			HttpOnly: true, Secure: strings.HasPrefix(d.BaseURL, "https"), SameSite: http.SameSiteLaxMode,
		})
	}

	accessToken, refreshToken, expiresIn, err := exchangeConnectorCode(cfg, code, d.connectorRedirectURI(provider), verifier)
	if err != nil {
		log.Printf("connector oauth %s: token exchange: %v", provider, err)
		d.connectorRedirectFail(w, r, claims.WorkflowID, "token_exchange")
		return
	}

	if err := d.linkConnectorToken(r.Context(), claims.WorkflowID, claims.NodeID, cfg, provider, accessToken, refreshToken, expiresIn); err != nil {
		log.Printf("connector oauth %s: link token: %v", provider, err)
		d.connectorRedirectFail(w, r, claims.WorkflowID, "link_failed")
		return
	}

	http.Redirect(w, r, d.FrontendURL+"/workflows/"+claims.WorkflowID+"?connected="+provider, http.StatusFound)
}

func (d *Deps) connectorRedirectURI(provider string) string {
	// Route through the frontend's /api proxy so the auth cookie (set on the
	// frontend domain) is forwarded by the browser on the callback redirect.
	// In production this is the same domain; in dev localhost:3000/api proxies
	// to localhost:8081 via next.config.ts rewrites.
	return strings.TrimRight(d.FrontendURL, "/") + "/api/connectors/oauth/" + provider + "/callback"
}

func (d *Deps) connectorRedirectFail(w http.ResponseWriter, r *http.Request, workflowID, reason string) {
	dest := d.FrontendURL + "/workflows"
	if workflowID != "" {
		dest = d.FrontendURL + "/workflows/" + workflowID
	}
	http.Redirect(w, r, dest+"?connectError="+url.QueryEscape(reason), http.StatusFound)
}

// linkConnectorToken loads the workflow, writes the encrypted token (and
// optional refresh token / expiry) onto the target node, and persists the
// whole graph back — the same read-mutate-write shape UpdateWorkflow's
// handler already uses, just triggered from the OAuth callback instead of a
// frontend save.
func (d *Deps) linkConnectorToken(ctx context.Context, workflowID, nodeID string, cfg ConnectorOAuthConfig, provider, accessToken, refreshToken string, expiresIn int) error {
	wf, err := d.Store.GetWorkflow(ctx, workflowID)
	if err != nil {
		return err
	}
	idx := -1
	for i, n := range wf.Nodes {
		if n.ID == nodeID {
			idx = i
		}
	}
	if idx == -1 {
		return fmt.Errorf("node %s not found in workflow %s", nodeID, workflowID)
	}
	if wf.Nodes[idx].Secrets == nil {
		wf.Nodes[idx].Secrets = map[string]string{}
	}
	if wf.Nodes[idx].Config == nil {
		wf.Nodes[idx].Config = map[string]string{}
	}
	wf.Nodes[idx].Secrets[connectorSecretKey(provider)] = encryptField(accessToken, "", d.EncryptionKey)
	if refreshToken != "" {
		wf.Nodes[idx].Secrets[connectorRefreshKey(provider)] = encryptField(refreshToken, "", d.EncryptionKey)
	}
	if expiresIn > 0 {
		wf.Nodes[idx].Config[connectorExpiresConfigKey(provider)] = time.Now().Add(time.Duration(expiresIn) * time.Second).Format(time.RFC3339)
	}
	if cfg.PostExchangeHook != nil {
		extra, err := cfg.PostExchangeHook(ctx, accessToken)
		if err != nil {
			return fmt.Errorf("post-exchange hook: %w", err)
		}
		for k, v := range extra {
			wf.Nodes[idx].Config[k] = v
		}
	}
	_, err = d.Store.UpdateWorkflow(ctx, workflowID, wf.Name, models.WorkflowGraph{Nodes: wf.Nodes, Edges: wf.Edges})
	return err
}

// exchangeConnectorCode POSTs the standard OAuth 2.0 authorization_code grant
// and parses the standard token response shape (access_token/refresh_token/
// expires_in) that every in-scope provider in this plan uses. verifier is
// sent as code_verifier only when non-empty (PKCE providers).
func exchangeConnectorCode(cfg ConnectorOAuthConfig, code, redirectURI, verifier string) (accessToken, refreshToken string, expiresIn int, err error) {
	var req *http.Request
	if cfg.TokenBodyStyle == "json" {
		payload := map[string]string{
			"code":         code,
			"redirect_uri": redirectURI,
			"grant_type":   "authorization_code",
		}
		if cfg.TokenAuthStyle != "basic" {
			payload["client_id"] = cfg.ClientIDEnvVal
			payload["client_secret"] = cfg.ClientSecretEnvVal
		}
		if verifier != "" {
			payload["code_verifier"] = verifier
		}
		body, mErr := json.Marshal(payload)
		if mErr != nil {
			return "", "", 0, mErr
		}
		req, err = http.NewRequest(http.MethodPost, cfg.TokenURL, strings.NewReader(string(body)))
		if err != nil {
			return "", "", 0, err
		}
		req.Header.Set("Content-Type", "application/json")
	} else {
		form := url.Values{}
		if cfg.TokenAuthStyle != "basic" {
			form.Set("client_id", cfg.ClientIDEnvVal)
			form.Set("client_secret", cfg.ClientSecretEnvVal)
		}
		form.Set("code", code)
		form.Set("redirect_uri", redirectURI)
		form.Set("grant_type", "authorization_code")
		if verifier != "" {
			form.Set("code_verifier", verifier)
		}

		req, err = http.NewRequest(http.MethodPost, cfg.TokenURL, strings.NewReader(form.Encode()))
		if err != nil {
			return "", "", 0, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("Accept", "application/json")
	if cfg.TokenAuthStyle == "basic" {
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(cfg.ClientIDEnvVal+":"+cfg.ClientSecretEnvVal)))
	}

	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return "", "", 0, err
	}
	defer res.Body.Close()

	body, rErr := io.ReadAll(res.Body)
	if rErr != nil {
		return "", "", 0, fmt.Errorf("reading token response: %w", rErr)
	}
	if res.StatusCode >= 400 {
		return "", "", 0, fmt.Errorf("token endpoint returned %d: %s", res.StatusCode, body)
	}
	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", "", 0, fmt.Errorf("decoding token response: %w", err)
	}
	if tok.AccessToken == "" {
		return "", "", 0, fmt.Errorf("no access token in response: %s", body)
	}
	return tok.AccessToken, tok.RefreshToken, tok.ExpiresIn, nil
}

// jiraAccessibleResourcesURL is overridden in tests via
// SetJiraAccessibleResourcesURLForTest, mirroring
// connectorTokenURLOverridesForTest's role for the token endpoint — so a test
// can drive the real "jira" registry entry's PostExchangeHook against a local
// fake server instead of the real api.atlassian.com host.
var jiraAccessibleResourcesURL = "https://api.atlassian.com/oauth/token/accessible-resources"

// SetJiraAccessibleResourcesURLForTest overrides the accessible-resources
// lookup URL. Call only from tests. Pass "" to reset to the real endpoint.
func SetJiraAccessibleResourcesURLForTest(u string) {
	if u == "" {
		jiraAccessibleResourcesURL = "https://api.atlassian.com/oauth/token/accessible-resources"
	} else {
		jiraAccessibleResourcesURL = u
	}
}

// jiraPostExchangeHook calls accessible-resources with the just-exchanged
// token and returns the first accessible site's cloudId, keyed the same way
// sendJira (connectors_devtools.go) reads it back out of node.Config. A token
// can in principle have access to more than one Atlassian site, but this
// registry entry has no per-node way to ask the user which one to use at
// link time, so — like the rest of this plan's connectors, which all target
// a single fixed destination per node — it takes the first and only one.
func jiraPostExchangeHook(ctx context.Context, accessToken string) (map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jiraAccessibleResourcesURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	var resources []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &resources); err != nil {
		return nil, err
	}
	if len(resources) == 0 {
		return nil, fmt.Errorf("jira: token has no accessible resources")
	}
	return map[string]string{"jiraOAuthCloudID": resources[0].ID}, nil
}

// mailchimpMetadataURL is overridden in tests via
// SetMailchimpMetadataURLForTest, mirroring jiraAccessibleResourcesURL's role
// for Jira — so a test can drive the real "mailchimp" registry entry's
// PostExchangeHook against a local fake server instead of the real
// login.mailchimp.com host.
var mailchimpMetadataURL = "https://login.mailchimp.com/oauth2/metadata"

// SetMailchimpMetadataURLForTest overrides the metadata lookup URL. Call only
// from tests. Pass "" to reset to the real endpoint.
func SetMailchimpMetadataURLForTest(u string) {
	if u == "" {
		mailchimpMetadataURL = "https://login.mailchimp.com/oauth2/metadata"
	} else {
		mailchimpMetadataURL = u
	}
}

// mailchimpPostExchangeHook calls the metadata endpoint with the
// just-exchanged token and returns its dc (datacenter) field, keyed the same
// way sendMailchimp (connectors_data.go) reads it back out of node.Config.
// The metadata endpoint takes the access token via a literal "OAuth <token>"
// Authorization scheme, not the "Bearer <token>" every other provider in this
// registry uses — confirmed against Mailchimp's own docs, not a guess.
func mailchimpPostExchangeHook(ctx context.Context, accessToken string) (map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mailchimpMetadataURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "OAuth "+accessToken)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	var meta struct {
		DC string `json:"dc"`
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		return nil, err
	}
	if meta.DC == "" {
		return nil, fmt.Errorf("mailchimp: metadata response has no dc")
	}
	return map[string]string{"mailchimpOAuthDC": meta.DC}, nil
}

func randURLSafe(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
