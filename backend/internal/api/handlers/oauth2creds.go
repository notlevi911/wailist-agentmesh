package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/oauthcred"
	"github.com/agentmesh/backend/internal/respond"
)

// oauth2CredProvider is the consent-screen/token-endpoint identity for a
// connector-facing OAuth provider. Distinct from handlers/oauth.go's
// oauthProvider: that one authenticates a person INTO AgentMesh (openid/
// email/profile scope, no offline access, token discarded after one
// userinfo fetch); this one connects an EXTERNAL account a workflow node
// calls on the user's behalf, long after this request ends -- so it always
// requests offline access and persists what it gets back.
type oauth2CredProvider struct {
	authURL     string
	userInfoURL string
	scopes      []string
	cfg         oauthcred.ProviderConfig
}

// googleConnectorScopes covers all four Google products in ONE consent
// screen rather than four separate connections -- Google allows granting
// multiple scopes in a single OAuth flow, and asking once is much less
// friction than making the user reconnect per product. drive.readonly (not
// the broader "drive" scope) deliberately keeps the review surface smaller:
// it can list/download but not overwrite/delete arbitrary files.
var googleConnectorScopes = []string{
	"https://www.googleapis.com/auth/gmail.readonly",
	"https://www.googleapis.com/auth/gmail.send",
	"https://www.googleapis.com/auth/spreadsheets",
	"https://www.googleapis.com/auth/calendar",
	"https://www.googleapis.com/auth/drive.readonly",
}

// googleAuthURL/googleTokenURL/googleUserInfoURL are overridden in tests via
// SetGoogleOAuthURLsForTest, matching the SetXAPIBaseForTest pattern used
// throughout engine/nodes for the same reason: a real handler test needs
// to point these at an httptest server instead of the real Google APIs.
var (
	googleAuthURL     = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL    = "https://oauth2.googleapis.com/token"
	googleUserInfoURL = "https://www.googleapis.com/oauth2/v2/userinfo"
)

// SetGoogleOAuthURLsForTest overrides all three Google OAuth endpoints at
// once. Call only from tests. Pass "" for every argument to reset to the
// real endpoints.
func SetGoogleOAuthURLsForTest(authURL, tokenURL, userInfoURL string) {
	if authURL == "" && tokenURL == "" && userInfoURL == "" {
		googleAuthURL = "https://accounts.google.com/o/oauth2/v2/auth"
		googleTokenURL = "https://oauth2.googleapis.com/token"
		googleUserInfoURL = "https://www.googleapis.com/oauth2/v2/userinfo"
		return
	}
	googleAuthURL = authURL
	googleTokenURL = tokenURL
	googleUserInfoURL = userInfoURL
}

func (d *Deps) oauth2CredProviderConfig(name string) (oauth2CredProvider, bool) {
	switch name {
	case "google":
		return oauth2CredProvider{
			authURL:     googleAuthURL,
			userInfoURL: googleUserInfoURL,
			scopes:      googleConnectorScopes,
			cfg: oauthcred.ProviderConfig{
				TokenURL:     googleTokenURL,
				ClientID:     d.GoogleClientID,
				ClientSecret: d.GoogleClientSecret,
			},
		}, d.GoogleClientID != "" && d.GoogleClientSecret != ""
	}
	return oauth2CredProvider{}, false
}

// oauth2CredRedirectURI mirrors oauthRedirectURI's exact reasoning (see
// oauth.go): it must go through the frontend's /api rewrite proxy, the same
// origin the browser used to reach the start endpoint, or the state cookie
// set there won't come back on the callback request.
func (d *Deps) oauth2CredRedirectURI(provider string) string {
	return strings.TrimRight(d.FrontendURL, "/") + "/api/oauth2/" + provider + "/callback"
}

func oauth2CredStateCookie(provider string) string {
	return "oauth2cred_state_" + provider
}

// OAuth2CredStart redirects the browser to the provider's consent screen to
// CONNECT an account to the current (already-logged-in) AgentMesh user --
// unlike OAuthStart, this lives in the JWT-protected route group, since
// there's already a signed-in user whose account this credential belongs
// to.
func (d *Deps) OAuth2CredStart(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "provider")
	p, ok := d.oauth2CredProviderConfig(name)
	if !ok {
		respond.Error(w, http.StatusNotFound, "unknown or unconfigured provider")
		return
	}

	state, err := randHex(16)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	secure := strings.HasPrefix(d.BaseURL, "https")
	http.SetCookie(w, &http.Cookie{
		Name:     oauth2CredStateCookie(name),
		Value:    state,
		Path:     "/",
		MaxAge:   600,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})

	q := url.Values{}
	q.Set("client_id", p.cfg.ClientID)
	q.Set("redirect_uri", d.oauth2CredRedirectURI(name))
	q.Set("response_type", "code")
	q.Set("scope", strings.Join(p.scopes, " "))
	q.Set("state", state)
	// access_type=offline is what makes Google ever return a refresh_token
	// at all (see oauthcred package doc); prompt=consent forces the consent
	// screen even on a returning user, which is the only reliable way to
	// GUARANTEE a refresh_token on this call -- Google only issues one on a
	// user's very first grant otherwise, and re-running this flow to add a
	// second Google account (or after a revoke) must not silently come back
	// with no refresh_token and an access-only credential that dies in an
	// hour.
	q.Set("access_type", "offline")
	q.Set("prompt", "consent")
	http.Redirect(w, r, p.authURL+"?"+q.Encode(), http.StatusFound)
}

// OAuth2CredCallback verifies state, exchanges the code, fetches an
// account label for display, and persists the encrypted credential —
// then bounces the browser back to the canvas.
func (d *Deps) OAuth2CredCallback(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "provider")
	userID, _ := r.Context().Value(CtxUserID).(string)
	p, ok := d.oauth2CredProviderConfig(name)
	if !ok {
		d.oauth2CredRedirectFail(w, r, "unknown_provider")
		return
	}

	cookieName := oauth2CredStateCookie(name)
	cookie, err := r.Cookie(cookieName)
	if err != nil || cookie.Value == "" ||
		subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(r.URL.Query().Get("state"))) != 1 {
		d.oauth2CredRedirectFail(w, r, "invalid_state")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: strings.HasPrefix(d.BaseURL, "https"), SameSite: http.SameSiteLaxMode,
	})

	code := r.URL.Query().Get("code")
	if code == "" {
		d.oauth2CredRedirectFail(w, r, "no_code")
		return
	}

	tok, err := oauthcred.ExchangeCode(r.Context(), p.cfg, code, d.oauth2CredRedirectURI(name))
	if err != nil {
		d.oauth2CredRedirectFail(w, r, "token_exchange")
		return
	}
	if tok.RefreshToken == "" {
		// Should be unreachable given access_type=offline&prompt=consent
		// above, but a credential that can never refresh is worse than
		// useless -- it silently dies in ~1 hour with no way to recover
		// short of reconnecting, so fail loudly here instead.
		d.oauth2CredRedirectFail(w, r, "no_refresh_token")
		return
	}

	label, err := fetchGoogleAccountLabel(tok.AccessToken, p.userInfoURL)
	if err != nil {
		// account_label is part of the (user_id, provider, account_label)
		// upsert key (migration 000021_oauth_credentials_unique) -- falling
		// back to a fixed value like "" here would make every failed-label
		// connection for this user+provider collide on that key, silently
		// overwriting one connected account's tokens with another's on the
		// next failed lookup.
		//
		// But a random-every-time label is its own bug: on a RECONNECT (the
		// common case -- refreshing a stale grant, fixing scopes) it would
		// never match the existing row's label either, so InsertOAuthCredential
		// inserts a brand-new row instead of updating in place, orphaning the
		// old row's still-valid refresh token and leaving two entries for the
		// same account. So first check whether this user already has exactly
		// one credential for this provider; if so, this is almost certainly a
		// reconnect of that same account, and reusing its label lets the
		// upsert update it in place. Only fall back to a random label when
		// that's ambiguous (zero, i.e. genuinely first connection with a
		// transient label-fetch failure, or more than one, where guessing
		// which existing account this is would risk the same clobber this
		// whole label scheme exists to prevent).
		label = "unlabeled-" + randomLabelSuffix()
		if existing, lerr := d.Store.ListOAuthCredentials(r.Context(), userID, name); lerr == nil && len(existing) == 1 {
			label = existing[0].AccountLabel
		}
	}

	accessEnc, refreshEnc, err := oauthcred.EncryptTokens(tok, d.EncryptionKey)
	if err != nil {
		d.oauth2CredRedirectFail(w, r, "encrypt_failed")
		return
	}

	// tok.Scope carries what Google's consent screen actually granted, which
	// can be a subset of p.scopes if the user declines part of the combined
	// grant -- storing p.scopes unconditionally would record full access
	// even when only some of it was actually approved, silently misleading
	// anything that trusts this column until a call 403s at runtime. Only a
	// provider that omits the scope field at all (not Google, but keep this
	// defensive since ExchangeCode is shared code) falls back to what was
	// requested.
	scopes := tok.Scope
	if scopes == "" {
		scopes = strings.Join(p.scopes, " ")
	}

	expiresAt := oauthcred.TokenExpiry(tok)
	if _, err := d.Store.InsertOAuthCredential(r.Context(), models.OAuthCredential{
		UserID:          userID,
		Provider:        name,
		AccountLabel:    label,
		AccessTokenEnc:  accessEnc,
		RefreshTokenEnc: refreshEnc,
		Scopes:          scopes,
		ExpiresAt:       expiresAt,
	}); err != nil {
		d.oauth2CredRedirectFail(w, r, "save_failed")
		return
	}

	http.Redirect(w, r, strings.TrimRight(d.FrontendURL, "/")+"/workflows?connected="+url.QueryEscape(name), http.StatusFound)
}

func (d *Deps) oauth2CredRedirectFail(w http.ResponseWriter, r *http.Request, reason string) {
	http.Redirect(w, r, strings.TrimRight(d.FrontendURL, "/")+"/workflows?connect_error="+url.QueryEscape(reason), http.StatusFound)
}

// randomLabelSuffix backs the ambiguous-fallback case in OAuth2CredCallback's
// label-fetch failure handling. randHex's error case (crypto/rand exhausted)
// is unreachable in practice, but a suffix collision there is still better
// than crashing the callback -- fall back to a timestamp instead.
func randomLabelSuffix() string {
	if suffix, err := randHex(4); err == nil {
		return suffix
	}
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

// OAuth2CredList returns the current user's connected accounts for a
// provider — never the tokens themselves (OAuthCredential's json tags
// already omit both).
func (d *Deps) OAuth2CredList(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(CtxUserID).(string)
	provider := r.URL.Query().Get("provider")
	if provider == "" {
		respond.Error(w, http.StatusBadRequest, "provider query param required")
		return
	}
	creds, err := d.Store.ListOAuthCredentials(r.Context(), userID, provider)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if creds == nil {
		creds = []models.OAuthCredential{}
	}
	respond.JSON(w, http.StatusOK, creds)
}

// OAuth2CredDelete disconnects an account. Owner-checked the same way
// DeleteWorkflow is: fetch first, compare UserID, 404 rather than 403 on
// mismatch so a guess can't distinguish "not yours" from "doesn't exist".
func (d *Deps) OAuth2CredDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	userID, _ := r.Context().Value(CtxUserID).(string)
	existing, err := d.Store.GetOAuthCredential(r.Context(), id)
	if err != nil || existing.UserID != userID {
		respond.Error(w, http.StatusNotFound, "credential not found")
		return
	}
	if err := d.Store.DeleteOAuthCredential(r.Context(), id); err != nil {
		respond.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	oauthcred.ForgetCredential(id)
	w.WriteHeader(http.StatusNoContent)
}

// oauth2CredHTTPClient bounds fetchGoogleAccountLabel's call the same way
// oauth.go's login-flow equivalent (fetchAccessToken/fetchEmail) and this
// file's own postForm/oauthcred's client already do -- http.DefaultClient
// has no timeout, so an unbounded call here would hang OAuth2CredCallback
// indefinitely on a slow/hanging userinfo endpoint, mid-redirect, right
// after the user already granted consent.
var oauth2CredHTTPClient = &http.Client{Timeout: 10 * time.Second}

func fetchGoogleAccountLabel(accessToken, userInfoURL string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, userInfoURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := oauth2CredHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return "", err
	}
	// A non-2xx status (expired/invalid access_token, transient 5xx) must be
	// treated as failure even if the body still happens to unmarshal cleanly
	// (Google error responses are JSON objects too, just without an "email"
	// field) -- returning (nil error, "") here instead of an error would
	// skip the caller's whole label-collision fallback (reusing an existing
	// single credential's label, or a random label as last resort) and
	// silently store account_label = "", walking straight back into the
	// exact upsert-key collision that fallback exists to prevent.
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("userinfo endpoint %d: %s", resp.StatusCode, string(body))
	}
	var info struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return "", err
	}
	if info.Email == "" {
		return "", fmt.Errorf("userinfo response had no email")
	}
	return info.Email, nil
}
