// Package oauthcred manages persisted, refreshable OAuth2 connections
// (Google today; Slack/Microsoft could reuse this same shape) that a
// workflow node reads a live access token from. Distinct from the sign-in
// OAuth flow in handlers/oauth.go: that flow authenticates a person into
// AgentMesh and never stores a token; a credential here is used to call the
// provider's own API on the user's behalf, from inside a running workflow,
// long after the browser session that connected it is gone.
//
// A low-level package (imports only models and wallet, not db or engine) so
// both internal/api/handlers (the connect/callback endpoints) and
// internal/engine/nodes (actual node execution) can depend on it without a
// circular import — the same reason nodes.RunContexter is a local interface
// rather than importing engine directly.
package oauthcred

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/wallet"
)

// Store is the slice of *db.Store this package needs.
type Store interface {
	GetOAuthCredential(ctx context.Context, id string) (models.OAuthCredential, error)
	UpdateOAuthCredentialTokens(ctx context.Context, id, accessTokenEnc, refreshTokenEnc string, expiresAt time.Time) error
}

// crossProcessLocker is implemented by *db.Store (see
// LockOAuthCredentialForRefresh's doc comment there) but deliberately kept
// OUT of the required Store interface above and checked via a type
// assertion in GetValidAccessToken instead of being a hard dependency --
// the in-memory fakeStore test doubles in this package's and nodes'
// *_test.go files only implement the two methods on Store, and requiring
// this one too would be a test-only concern leaking into production code's
// interface. A store that doesn't implement it (any test double) just
// doesn't get the cross-process lock, keeping refreshLocks' in-process
// mutex as its only protection -- correct for a single-process test, and
// the real *db.Store used in production always implements it.
type crossProcessLocker interface {
	LockOAuthCredentialForRefresh(ctx context.Context, id string) (release func(context.Context), err error)
}

// ProviderConfig is the token-endpoint identity needed to exchange a code or
// refresh a token. Deliberately not stored per-credential-row: rotating a
// client secret must not require touching every already-connected user's
// saved connection.
type ProviderConfig struct {
	TokenURL     string
	ClientID     string
	ClientSecret string
}

// expiryLeeway refreshes an access token slightly before its real expiry, so
// a node mid-execution never races the exact expiry instant.
const expiryLeeway = 60 * time.Second

// defaultExpiresIn is used when a token response omits expires_in (or sends
// 0). Treating that as "expires right now" would force a refresh round trip
// on every single call for that token -- most OAuth2 providers default an
// access token to a 1 hour lifetime when the field is left off, so assume
// the same rather than the worst case.
const defaultExpiresIn = time.Hour

// TokenExpiry computes the wall-clock expiry for tok, floored at
// defaultExpiresIn so a missing/zero expires_in doesn't make the token look
// pre-expired.
func TokenExpiry(tok TokenResponse) time.Time {
	ttl := time.Duration(tok.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = defaultExpiresIn
	}
	return time.Now().Add(ttl)
}

// Timeout matches handlers/connector_oauth.go's exchangeConnectorCode client
// -- that package has its own separate OAuth2 token-exchange implementation
// for the other 12 connector providers (Slack, GitHub, Notion, ...), and
// letting the two drift on something as basic as timeout is exactly the
// kind of divergence duplicated logic invites. A full merge of the two
// isn't safe to do blind (connector_oauth.go's version also carries PKCE
// and per-provider auth/body-style quirks this package doesn't need for
// Google), but the timeout has no reason to differ.
var httpClient = &http.Client{Timeout: 10 * time.Second}

// refreshLocks single-flights concurrent refreshes of the SAME credential.
// An agent's tool-calling loop, or two parallel flow branches attached to
// the same connected account, could otherwise both see an expired token,
// both refresh, and the second write would silently discard the first
// refresh's still-valid rotated tokens as a lost update.
var refreshLocks sync.Map // credentialID string -> *sync.Mutex

func lockFor(credentialID string) *sync.Mutex {
	v, _ := refreshLocks.LoadOrStore(credentialID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// ForgetCredential drops credentialID's refresh mutex once its row is gone
// (disconnected, or replaced by a reconnect's upsert). Without this,
// refreshLocks grows by one entry per credential ID ever seen and never
// shrinks -- a slow unbounded leak over a long-running process with repeated
// connect/disconnect cycles.
func ForgetCredential(credentialID string) {
	refreshLocks.Delete(credentialID)
}

// GetValidAccessToken returns a ready-to-use bearer token for cred,
// transparently refreshing and persisting it first if it's expired (or
// close to it). This is the one function a connector node actually calls —
// it never sees ciphertext or the refresh mechanics.
//
// cred is normally already in hand: a caller that also needs to check
// credential ownership (e.g. nodes/google.go) has already fetched the row
// for that check, and passing it in here instead of a bare id avoids a
// second identical DB round-trip on every node execution. The still-valid
// case below trusts that pre-fetched row outright; only the expired/refresh
// path re-fetches, and does so under the lock, since a concurrent refresh
// may have rotated the tokens since cred was read.
func GetValidAccessToken(ctx context.Context, store Store, encKey string, cred models.OAuthCredential, cfg ProviderConfig) (string, error) {
	if time.Now().Add(expiryLeeway).Before(cred.ExpiresAt) {
		return wallet.Decrypt(cred.AccessTokenEnc, encKey)
	}

	mu := lockFor(cred.ID)
	mu.Lock()
	defer mu.Unlock()

	// crossProcessLocker (see its doc comment) additionally serializes this
	// section across backend replicas, not just goroutines in this process --
	// held for the full check-refresh-persist sequence below, released
	// before returning either branch.
	if locker, ok := store.(crossProcessLocker); ok {
		release, err := locker.LockOAuthCredentialForRefresh(ctx, cred.ID)
		if err != nil {
			return "", fmt.Errorf("lock oauth credential for refresh: %w", err)
		}
		defer release(context.WithoutCancel(ctx))
	}

	fresh, err := store.GetOAuthCredential(ctx, cred.ID)
	if err != nil {
		return "", fmt.Errorf("oauth credential not found: %w", err)
	}
	if time.Now().Add(expiryLeeway).Before(fresh.ExpiresAt) {
		return wallet.Decrypt(fresh.AccessTokenEnc, encKey)
	}
	if fresh.RefreshTokenEnc == "" {
		return "", fmt.Errorf("oauth connection has expired and has no refresh token -- reconnect the account")
	}
	refreshToken, err := wallet.Decrypt(fresh.RefreshTokenEnc, encKey)
	if err != nil {
		return "", fmt.Errorf("decrypt refresh token: %w", err)
	}
	tok, err := refreshAccessToken(ctx, cfg, refreshToken)
	if err != nil {
		return "", fmt.Errorf("refresh oauth token: %w", err)
	}
	accessEnc, refreshEnc, err := EncryptTokens(tok, encKey)
	if err != nil {
		return "", err
	}
	expiresAt := TokenExpiry(tok)
	if err := store.UpdateOAuthCredentialTokens(ctx, cred.ID, accessEnc, refreshEnc, expiresAt); err != nil {
		return "", fmt.Errorf("persist refreshed token: %w", err)
	}
	return tok.AccessToken, nil
}

// TokenResponse is the token endpoint's response shape (RFC 6749 section
// 5.1), shared by both the authorization-code exchange and the refresh
// grant.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
}

// ExchangeCode trades a fresh authorization code for tokens — called once,
// right after the user completes the provider's consent screen.
func ExchangeCode(ctx context.Context, cfg ProviderConfig, code, redirectURI string) (TokenResponse, error) {
	form := url.Values{}
	form.Set("client_id", cfg.ClientID)
	form.Set("client_secret", cfg.ClientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("grant_type", "authorization_code")
	return postForm(ctx, cfg.TokenURL, form)
}

func refreshAccessToken(ctx context.Context, cfg ProviderConfig, refreshToken string) (TokenResponse, error) {
	form := url.Values{}
	form.Set("client_id", cfg.ClientID)
	form.Set("client_secret", cfg.ClientSecret)
	form.Set("refresh_token", refreshToken)
	form.Set("grant_type", "refresh_token")
	return postForm(ctx, cfg.TokenURL, form)
}

func postForm(ctx context.Context, tokenURL string, form url.Values) (TokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return TokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return TokenResponse{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return TokenResponse{}, err
	}
	if resp.StatusCode >= 400 {
		return TokenResponse{}, fmt.Errorf("token endpoint %d: %s", resp.StatusCode, string(body))
	}
	var tok TokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return TokenResponse{}, fmt.Errorf("decode token response: %w", err)
	}
	if tok.AccessToken == "" {
		return TokenResponse{}, fmt.Errorf("token endpoint returned no access_token")
	}
	return tok, nil
}

// EncryptTokens is used by the OAuth callback handler right after the very
// first exchange (before the first InsertOAuthCredential) and by
// GetValidAccessToken after a refresh — kept here, not duplicated in the
// handlers package, so a token is only ever encrypted in one place.
func EncryptTokens(tok TokenResponse, encKey string) (accessEnc, refreshEnc string, err error) {
	accessEnc, err = wallet.Encrypt(tok.AccessToken, encKey)
	if err != nil {
		return "", "", fmt.Errorf("encrypt access token: %w", err)
	}
	if tok.RefreshToken != "" {
		refreshEnc, err = wallet.Encrypt(tok.RefreshToken, encKey)
		if err != nil {
			return "", "", fmt.Errorf("encrypt refresh token: %w", err)
		}
	}
	return accessEnc, refreshEnc, nil
}
