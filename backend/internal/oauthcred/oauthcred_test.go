package oauthcred_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/oauthcred"
	"github.com/agentmesh/backend/internal/wallet"
)

const testKey = "0123456789abcdef0123456789abcdef" // exactly 32 bytes

// fakeStore is an in-memory Store for tests -- no real Postgres needed,
// matching how the rest of this codebase tests store-shaped interfaces
// (e.g. nodes.TendrilStore's test doubles).
type fakeStore struct {
	mu    sync.Mutex
	creds map[string]models.OAuthCredential
	// updates counts UpdateOAuthCredentialTokens calls, to assert
	// single-flight actually collapsed concurrent refreshes into one.
	updates int32
}

func newFakeStore(cred models.OAuthCredential) *fakeStore {
	return &fakeStore{creds: map[string]models.OAuthCredential{cred.ID: cred}}
}

func (s *fakeStore) GetOAuthCredential(ctx context.Context, id string) (models.OAuthCredential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.creds[id], nil
}

func (s *fakeStore) UpdateOAuthCredentialTokens(ctx context.Context, id, accessTokenEnc, refreshTokenEnc string, expiresAt time.Time) error {
	atomic.AddInt32(&s.updates, 1)
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.creds[id]
	c.AccessTokenEnc = accessTokenEnc
	if refreshTokenEnc != "" {
		c.RefreshTokenEnc = refreshTokenEnc
	}
	c.ExpiresAt = expiresAt
	s.creds[id] = c
	return nil
}

func mustEncrypt(t *testing.T, plaintext string) string {
	t.Helper()
	enc, err := wallet.Encrypt(plaintext, testKey)
	if err != nil {
		t.Fatal(err)
	}
	return enc
}

func TestGetValidAccessToken_ReturnsDecryptedTokenWithoutRefreshingWhenStillValid(t *testing.T) {
	var tokenEndpointHit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenEndpointHit = true
	}))
	defer srv.Close()

	cred := models.OAuthCredential{
		ID: "c1", AccessTokenEnc: mustEncrypt(t, "live-token"),
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}
	store := newFakeStore(cred)

	got, err := oauthcred.GetValidAccessToken(context.Background(), store, testKey, cred,
		oauthcred.ProviderConfig{TokenURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if got != "live-token" {
		t.Errorf("want decrypted live token, got %q", got)
	}
	if tokenEndpointHit {
		t.Error("want no refresh call for a still-valid token")
	}
}

func TestGetValidAccessToken_RefreshesExpiredToken(t *testing.T) {
	var gotGrantType, gotRefreshToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		gotGrantType = r.Form.Get("grant_type")
		gotRefreshToken = r.Form.Get("refresh_token")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "new-access-token", "refresh_token": "new-refresh-token", "expires_in": 3600,
		})
	}))
	defer srv.Close()

	cred := models.OAuthCredential{
		ID:              "c2",
		AccessTokenEnc:  mustEncrypt(t, "stale-token"),
		RefreshTokenEnc: mustEncrypt(t, "old-refresh-token"),
		ExpiresAt:       time.Now().Add(-1 * time.Minute), // already expired
	}
	store := newFakeStore(cred)

	got, err := oauthcred.GetValidAccessToken(context.Background(), store, testKey, cred,
		oauthcred.ProviderConfig{TokenURL: srv.URL, ClientID: "cid", ClientSecret: "csec"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "new-access-token" {
		t.Errorf("want the freshly refreshed token, got %q", got)
	}
	if gotGrantType != "refresh_token" {
		t.Errorf("want refresh_token grant, got %q", gotGrantType)
	}
	if gotRefreshToken != "old-refresh-token" {
		t.Errorf("want old refresh token sent to the token endpoint, got %q", gotRefreshToken)
	}

	// The store row should now hold the NEW encrypted tokens.
	updated, _ := store.GetOAuthCredential(context.Background(), "c2")
	decrypted, err := wallet.Decrypt(updated.AccessTokenEnc, testKey)
	if err != nil || decrypted != "new-access-token" {
		t.Errorf("want persisted access token to be the refreshed one, got %q (err %v)", decrypted, err)
	}
}

func TestGetValidAccessToken_PreservesRefreshTokenWhenProviderOmitsANewOne(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No refresh_token in the response -- common on a re-issue.
		json.NewEncoder(w).Encode(map[string]any{"access_token": "new-access-token", "expires_in": 3600})
	}))
	defer srv.Close()

	cred := models.OAuthCredential{
		ID:              "c3",
		AccessTokenEnc:  mustEncrypt(t, "stale-token"),
		RefreshTokenEnc: mustEncrypt(t, "keep-me"),
		ExpiresAt:       time.Now().Add(-1 * time.Minute),
	}
	store := newFakeStore(cred)

	if _, err := oauthcred.GetValidAccessToken(context.Background(), store, testKey, cred,
		oauthcred.ProviderConfig{TokenURL: srv.URL}); err != nil {
		t.Fatal(err)
	}

	updated, _ := store.GetOAuthCredential(context.Background(), "c3")
	decrypted, err := wallet.Decrypt(updated.RefreshTokenEnc, testKey)
	if err != nil || decrypted != "keep-me" {
		t.Errorf("want the original refresh token preserved, got %q (err %v)", decrypted, err)
	}
}

func TestGetValidAccessToken_ErrorsWhenExpiredWithNoRefreshToken(t *testing.T) {
	cred := models.OAuthCredential{
		ID: "c4", AccessTokenEnc: mustEncrypt(t, "stale-token"),
		ExpiresAt: time.Now().Add(-1 * time.Minute),
	}
	store := newFakeStore(cred)

	_, err := oauthcred.GetValidAccessToken(context.Background(), store, testKey, cred,
		oauthcred.ProviderConfig{})
	if err == nil {
		t.Fatal("want error when expired with no refresh token")
	}
	if !strings.Contains(err.Error(), "reconnect") {
		t.Errorf("want an actionable 'reconnect' message, got %v", err)
	}
}

func TestGetValidAccessToken_SingleFlightsConcurrentRefreshes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond) // widen the race window
		json.NewEncoder(w).Encode(map[string]any{"access_token": "refreshed", "expires_in": 3600})
	}))
	defer srv.Close()

	cred := models.OAuthCredential{
		ID:              "c5",
		AccessTokenEnc:  mustEncrypt(t, "stale"),
		RefreshTokenEnc: mustEncrypt(t, "refresh-me"),
		ExpiresAt:       time.Now().Add(-1 * time.Minute),
	}
	store := newFakeStore(cred)
	cfg := oauthcred.ProviderConfig{TokenURL: srv.URL}

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := oauthcred.GetValidAccessToken(context.Background(), store, testKey, cred, cfg); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&store.updates); got != 1 {
		t.Errorf("want exactly 1 refresh to reach the store (single-flighted), got %d", got)
	}
}

func TestExchangeCode_SendsAuthorizationCodeGrant(t *testing.T) {
	var gotForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		gotForm = r.Form
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok", "refresh_token": "ref", "expires_in": 3600,
		})
	}))
	defer srv.Close()

	tok, err := oauthcred.ExchangeCode(context.Background(),
		oauthcred.ProviderConfig{TokenURL: srv.URL, ClientID: "cid", ClientSecret: "csec"},
		"auth-code-123", "https://example.com/callback")
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "tok" || tok.RefreshToken != "ref" {
		t.Errorf("want decoded token response, got %+v", tok)
	}
	if gotForm.Get("grant_type") != "authorization_code" || gotForm.Get("code") != "auth-code-123" {
		t.Errorf("want authorization_code grant with the code, got %v", gotForm)
	}
}

func TestExchangeCode_ErrorsOnNoAccessToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant"})
	}))
	defer srv.Close()

	_, err := oauthcred.ExchangeCode(context.Background(), oauthcred.ProviderConfig{TokenURL: srv.URL}, "bad-code", "https://x")
	if err == nil {
		t.Fatal("want error when the token endpoint returns no access_token")
	}
}

func TestExchangeCode_ErrorStatusReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_request"}`))
	}))
	defer srv.Close()

	_, err := oauthcred.ExchangeCode(context.Background(), oauthcred.ProviderConfig{TokenURL: srv.URL}, "code", "https://x")
	if err == nil {
		t.Fatal("want error for 400 response")
	}
}

func TestEncryptTokens_RoundTrips(t *testing.T) {
	accessEnc, refreshEnc, err := oauthcred.EncryptTokens(
		oauthcred.TokenResponse{AccessToken: "a-token", RefreshToken: "r-token"}, testKey)
	if err != nil {
		t.Fatal(err)
	}
	gotAccess, err := wallet.Decrypt(accessEnc, testKey)
	if err != nil || gotAccess != "a-token" {
		t.Errorf("want access token round-trip, got %q (err %v)", gotAccess, err)
	}
	gotRefresh, err := wallet.Decrypt(refreshEnc, testKey)
	if err != nil || gotRefresh != "r-token" {
		t.Errorf("want refresh token round-trip, got %q (err %v)", gotRefresh, err)
	}
}

func TestEncryptTokens_EmptyRefreshTokenStaysEmpty(t *testing.T) {
	_, refreshEnc, err := oauthcred.EncryptTokens(oauthcred.TokenResponse{AccessToken: "a"}, testKey)
	if err != nil {
		t.Fatal(err)
	}
	if refreshEnc != "" {
		t.Errorf("want empty refreshEnc when no refresh token was issued, got %q", refreshEnc)
	}
}
