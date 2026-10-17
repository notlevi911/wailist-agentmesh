package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/agentmesh/backend/internal/db"
	"github.com/agentmesh/backend/internal/respond"
)

type oauthProvider struct {
	authURL      string
	tokenURL     string
	userInfoURL  string
	scope        string
	clientID     string
	clientSecret string
}

func (d *Deps) providerConfig(name string) (oauthProvider, bool) {
	switch name {
	case "github":
		return oauthProvider{
			authURL:      "https://github.com/login/oauth/authorize",
			tokenURL:     "https://github.com/login/oauth/access_token",
			userInfoURL:  "https://api.github.com/user",
			scope:        "user:email",
			clientID:     d.GithubClientID,
			clientSecret: d.GithubClientSecret,
		}, d.GithubClientID != "" && d.GithubClientSecret != ""
	case "google":
		return oauthProvider{
			authURL:      "https://accounts.google.com/o/oauth2/v2/auth",
			tokenURL:     "https://oauth2.googleapis.com/token",
			userInfoURL:  "https://www.googleapis.com/oauth2/v2/userinfo",
			scope:        "openid email",
			clientID:     d.GoogleClientID,
			clientSecret: d.GoogleClientSecret,
		}, d.GoogleClientID != "" && d.GoogleClientSecret != ""
	}
	return oauthProvider{}, false
}

// OAuthStartURL gives native clients the browser URL on the callback's origin.
func (d *Deps) OAuthStartURL(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "provider")
	if _, ok := d.providerConfig(name); !ok {
		respond.Error(w, http.StatusNotFound, "unknown or unconfigured provider")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	respond.JSON(w, http.StatusOK, map[string]string{
		"url": strings.TrimSuffix(d.oauthRedirectURI(name), "/callback"),
	})
}

// OAuthStart redirects the browser to the provider's consent screen.
func (d *Deps) OAuthStart(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "provider")
	p, ok := d.providerConfig(name)
	if !ok {
		respond.Error(w, http.StatusNotFound, "unknown or unconfigured provider")
		return
	}

	random, err := randHex(16)
	if err != nil {
		d.redirectFail(w, r, "internal")
		return
	}
	// A native flow carries its client and its PKCE challenge in the state, so
	// the callback can tell the two apart without a second cookie. See
	// oauth_native.go. A web flow's state is unchanged: a bare random string.
	state := random
	if isNativeStart(r) {
		challenge := strings.TrimSpace(r.URL.Query().Get("challenge"))
		if challenge == "" {
			nativeFail(w, r, "no_challenge")
			return
		}
		state = encodeNativeState(challenge, random)
	}
	secure := strings.HasPrefix(d.BaseURL, "https")
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookie(name),
		Value:    state,
		Path:     "/",
		MaxAge:   600,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})

	q := url.Values{}
	q.Set("client_id", p.clientID)
	q.Set("redirect_uri", d.oauthRedirectURI(name))
	q.Set("scope", p.scope)
	q.Set("state", state)
	q.Set("response_type", "code")
	http.Redirect(w, r, p.authURL+"?"+q.Encode(), http.StatusFound)
}

// OAuthCallback handles the provider redirect: verifies state, exchanges the
// code, fetches the email, upserts the user, issues a JWT, and bounces the
// browser back to the frontend with the token.
func (d *Deps) OAuthCallback(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "provider")
	p, ok := d.providerConfig(name)
	if !ok {
		respond.Error(w, http.StatusNotFound, "unknown or unconfigured provider")
		return
	}

	cookieName := oauthStateCookie(name)
	cookie, cookieErr := r.Cookie(cookieName)

	// Which channel a failure goes back on, decided before the state has been
	// trusted. This chooses only where an error message is shown, never whether
	// the flow is authentic, so falling back to the query when there is no
	// cookie is safe -- and it means a native user sees the failure in the app
	// rather than stranded on a page in a browser tab with no way back.
	stateSeen := r.URL.Query().Get("state")
	if cookieErr == nil && cookie.Value != "" {
		stateSeen = cookie.Value
	}
	native, challenge := decodeNativeState(stateSeen)
	fail := d.redirectFail
	if native {
		fail = nativeFail
	}

	if cookieErr != nil || cookie.Value == "" || cookie.Value != r.URL.Query().Get("state") {
		fail(w, r, "invalid_state")
		return
	}
	// One-time use: clear the state cookie so it cannot be replayed.
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: strings.HasPrefix(d.BaseURL, "https"), SameSite: http.SameSiteLaxMode,
	})

	code := r.URL.Query().Get("code")
	if code == "" {
		fail(w, r, "no_code")
		return
	}

	accessToken, err := exchangeCode(p, code, d.oauthRedirectURI(name))
	if err != nil {
		fail(w, r, "token_exchange")
		return
	}

	email, err := fetchEmail(name, p, accessToken)
	if err != nil || email == "" {
		fail(w, r, "no_email")
		return
	}

	user, err := d.Store.GetOrCreateOAuthUser(r.Context(), strings.ToLower(strings.TrimSpace(email)))
	if errors.Is(err, db.ErrPasswordAccountExists) {
		fail(w, r, "account_exists")
		return
	}
	if err != nil {
		fail(w, r, "user_upsert")
		return
	}

	// The app's session is not issued here. It gets a one-time code instead,
	// which it swaps for a token over HTTPS from inside the app -- so the
	// session never travels on this front channel at all. See oauth_native.go.
	if native {
		if challenge == "" {
			fail(w, r, "invalid_state")
			return
		}
		exchange, err := d.issueExchangeCode(user.ID, user.Email, challenge)
		if err != nil {
			fail(w, r, "token_issue")
			return
		}
		q := url.Values{}
		q.Set("code", exchange)
		nativeRedirect(w, r, q)
		return
	}

	token, err := d.issueToken(user)
	if err != nil {
		fail(w, r, "token_issue")
		return
	}

	// Set the token as an HttpOnly cookie so it never appears in URLs or logs.
	// Also set the UI signal cookie the Next.js middleware gates protected
	// routes on — see uiCookieName's doc comment: with no client JS in this
	// server-redirect flow, skipping this would bounce the just-authenticated
	// user straight back to /signin.
	d.setAuthCookie(w, token)
	d.setUICookie(w)
	http.Redirect(w, r, d.FrontendURL+"/workflows", http.StatusFound)
}

// oauthRedirectURI must resolve to the SAME origin the browser used to reach
// OAuthStart, or the oauth_state_* cookie set there won't be sent back on the
// callback request. In production the browser talks to the frontend's own
// domain, which Next.js rewrites proxy at /api/* through to this backend
// (see frontend/next.config.ts) — so the callback also has to go through
// that /api proxy rather than hitting BaseURL (the raw Railway host)
// directly, which would land the callback on a different origin than the
// cookie and produce invalid_state ("Login session expired").
func (d *Deps) oauthRedirectURI(provider string) string {
	return strings.TrimRight(d.FrontendURL, "/") + "/api/auth/oauth/" + provider + "/callback"
}

func (d *Deps) redirectFail(w http.ResponseWriter, r *http.Request, reason string) {
	http.Redirect(w, r, d.FrontendURL+"/signin?error="+url.QueryEscape(reason), http.StatusFound)
}

func oauthStateCookie(provider string) string {
	return "oauth_state_" + provider
}

func randHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func exchangeCode(p oauthProvider, code, redirectURI string) (string, error) {
	form := url.Values{}
	form.Set("client_id", p.clientID)
	form.Set("client_secret", p.clientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("grant_type", "authorization_code")

	req, _ := http.NewRequest(http.MethodPost, p.tokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	body, _ := io.ReadAll(res.Body)
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", err
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("no access token in response")
	}
	return tok.AccessToken, nil
}

func fetchEmail(provider string, p oauthProvider, accessToken string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}

	get := func(u string) ([]byte, error) {
		req, _ := http.NewRequest(http.MethodGet, u, nil)
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "agentmesh") // GitHub rejects requests without UA
		res, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()
		return io.ReadAll(res.Body)
	}

	if provider == "google" {
		body, err := get(p.userInfoURL)
		if err != nil {
			return "", err
		}
		var info struct {
			Email         string `json:"email"`
			VerifiedEmail bool   `json:"verified_email"`
		}
		json.Unmarshal(body, &info)
		// Only trust an email Google has confirmed the user owns.
		if info.Email == "" || !info.VerifiedEmail {
			return "", fmt.Errorf("email not verified")
		}
		return info.Email, nil
	}

	// github: ignore the (possibly unverified) profile email and require a
	// primary, verified address from /user/emails.
	body, err := get("https://api.github.com/user/emails")
	if err != nil {
		return "", err
	}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	json.Unmarshal(body, &emails)
	for _, e := range emails {
		if e.Primary && e.Verified {
			return e.Email, nil
		}
	}
	for _, e := range emails {
		if e.Verified {
			return e.Email, nil
		}
	}
	return "", fmt.Errorf("no verified email")
}
