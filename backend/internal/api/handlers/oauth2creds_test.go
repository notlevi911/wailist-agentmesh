package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/agentmesh/backend/internal/api/handlers"
	"github.com/agentmesh/backend/internal/models"
)

// oauth2Deps returns Deps configured with a fake Google client id/secret
// (oauth2CredProviderConfig treats an empty id/secret as "unconfigured" and
// 404s, so every real test needs these set) plus FrontendURL/BaseURL, which
// the redirect-URI and cookie-Secure logic both read.
func oauth2Deps(t *testing.T) *handlers.Deps {
	t.Helper()
	d := testDeps(t)
	d.GoogleClientID = "test-client-id"
	d.GoogleClientSecret = "test-client-secret"
	d.FrontendURL = "http://localhost:3000"
	d.BaseURL = "http://localhost:8080"
	return d
}

func TestOAuth2CredStart_RedirectsToConsentScreenWithOfflineAccess(t *testing.T) {
	d := oauth2Deps(t)
	req := withURLParam(withUser(httptest.NewRequest(http.MethodGet, "/oauth2/google/start", nil), "u1"), "provider", "google")
	rec := httptest.NewRecorder()
	d.OAuth2CredStart(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("want 302, got %d", rec.Code)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	q := loc.Query()
	if q.Get("client_id") != "test-client-id" {
		t.Errorf("want client_id passed through, got %q", q.Get("client_id"))
	}
	if q.Get("access_type") != "offline" {
		t.Errorf("want access_type=offline (required to ever get a refresh_token), got %q", q.Get("access_type"))
	}
	if q.Get("prompt") != "consent" {
		t.Errorf("want prompt=consent (guarantees a refresh_token even on re-consent), got %q", q.Get("prompt"))
	}
	if q.Get("state") == "" {
		t.Error("want a non-empty state param")
	}
	if !strings.Contains(q.Get("scope"), "gmail.send") || !strings.Contains(q.Get("scope"), "spreadsheets") ||
		!strings.Contains(q.Get("scope"), "calendar") || !strings.Contains(q.Get("scope"), "drive.readonly") {
		t.Errorf("want all four Google product scopes requested in one consent screen, got %q", q.Get("scope"))
	}
	if len(rec.Result().Cookies()) == 0 {
		t.Error("want a state cookie set")
	}
}

func TestOAuth2CredStart_404sWhenProviderUnconfigured(t *testing.T) {
	d := testDeps(t) // no GoogleClientID/Secret set
	req := withURLParam(withUser(httptest.NewRequest(http.MethodGet, "/oauth2/google/start", nil), "u1"), "provider", "google")
	rec := httptest.NewRecorder()
	d.OAuth2CredStart(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("want 404 for an unconfigured provider, got %d", rec.Code)
	}
}

// googleMockServers spins up fake token + userinfo endpoints and points the
// package's Google URL vars at them for the duration of the test.
func googleMockServers(t *testing.T, tokenBody, userInfoBody string) {
	t.Helper()
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(tokenBody))
	}))
	userInfoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(userInfoBody))
	}))
	t.Cleanup(func() {
		tokenSrv.Close()
		userInfoSrv.Close()
		handlers.SetGoogleOAuthURLsForTest("", "", "")
	})
	handlers.SetGoogleOAuthURLsForTest("https://unused-auth-url.example", tokenSrv.URL, userInfoSrv.URL)
}

// callbackReq builds a GET request carrying both the state cookie
// OAuth2CredStart would have set and the query params Google's redirect
// would carry.
func callbackReq(userID, provider, cookieState, queryState, code string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/oauth2/"+provider+"/callback?state="+queryState+"&code="+code, nil)
	req.AddCookie(&http.Cookie{Name: "oauth2cred_state_" + provider, Value: cookieState})
	return withURLParam(withUser(req, userID), "provider", provider)
}

func TestOAuth2CredCallback_PersistsCredentialOnSuccess(t *testing.T) {
	d := oauth2Deps(t)
	ctx := context.Background()
	user, err := d.Store.CreateUser(ctx, "oauth2-cb-"+randSuffix(t)+"@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}

	googleMockServers(t,
		`{"access_token":"live-access","refresh_token":"live-refresh","expires_in":3600}`,
		`{"email":"connected@gmail.com"}`)

	rec := httptest.NewRecorder()
	d.OAuth2CredCallback(rec, callbackReq(user.ID, "google", "state123", "state123", "auth-code"))

	if rec.Code != http.StatusFound {
		t.Fatalf("want 302 redirect back to the frontend, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Location"), "connected=google") {
		t.Errorf("want a success redirect, got %q", rec.Header().Get("Location"))
	}

	creds, err := d.Store.ListOAuthCredentials(ctx, user.ID, "google")
	if err != nil {
		t.Fatal(err)
	}
	if len(creds) != 1 {
		t.Fatalf("want exactly 1 persisted credential, got %d", len(creds))
	}
	if creds[0].AccountLabel != "connected@gmail.com" {
		t.Errorf("want account label from userinfo, got %q", creds[0].AccountLabel)
	}
	if !strings.Contains(creds[0].Scopes, "gmail.send") {
		t.Errorf("want all requested scopes recorded, got %q", creds[0].Scopes)
	}
}

func TestOAuth2CredCallback_RejectsMismatchedState(t *testing.T) {
	d := oauth2Deps(t)
	ctx := context.Background()
	user, err := d.Store.CreateUser(ctx, "oauth2-badstate-"+randSuffix(t)+"@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	googleMockServers(t, `{"access_token":"x","refresh_token":"y","expires_in":3600}`, `{"email":"x@gmail.com"}`)

	rec := httptest.NewRecorder()
	d.OAuth2CredCallback(rec, callbackReq(user.ID, "google", "cookie-state", "different-query-state", "code"))

	if !strings.Contains(rec.Header().Get("Location"), "invalid_state") {
		t.Errorf("want invalid_state redirect, got %q", rec.Header().Get("Location"))
	}
	creds, _ := d.Store.ListOAuthCredentials(ctx, user.ID, "google")
	if len(creds) != 0 {
		t.Error("want no credential persisted when state doesn't match (CSRF guard)")
	}
}

func TestOAuth2CredCallback_FailsWhenNoRefreshTokenReturned(t *testing.T) {
	d := oauth2Deps(t)
	ctx := context.Background()
	user, err := d.Store.CreateUser(ctx, "oauth2-norefresh-"+randSuffix(t)+"@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	// No refresh_token in the response -- should be unreachable given
	// access_type=offline&prompt=consent on the start step, but the
	// callback must still refuse to save a credential that can never
	// refresh rather than silently accepting an access-only one.
	googleMockServers(t, `{"access_token":"x","expires_in":3600}`, `{"email":"x@gmail.com"}`)

	rec := httptest.NewRecorder()
	d.OAuth2CredCallback(rec, callbackReq(user.ID, "google", "s1", "s1", "code"))

	if !strings.Contains(rec.Header().Get("Location"), "no_refresh_token") {
		t.Errorf("want no_refresh_token redirect, got %q", rec.Header().Get("Location"))
	}
	creds, _ := d.Store.ListOAuthCredentials(ctx, user.ID, "google")
	if len(creds) != 0 {
		t.Error("want no credential persisted without a refresh token")
	}
}

func TestOAuth2CredCallback_ReconnectWithFailedLabelFetchUpdatesExistingRow(t *testing.T) {
	d := oauth2Deps(t)
	ctx := context.Background()
	user, err := d.Store.CreateUser(ctx, "oauth2-relabel-"+randSuffix(t)+"@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}

	// First connection: userinfo succeeds normally.
	googleMockServers(t,
		`{"access_token":"first-access","refresh_token":"first-refresh","expires_in":3600}`,
		`{"email":"reconnect@gmail.com"}`)
	rec := httptest.NewRecorder()
	d.OAuth2CredCallback(rec, callbackReq(user.ID, "google", "s1", "s1", "code1"))
	if rec.Code != http.StatusFound || !strings.Contains(rec.Header().Get("Location"), "connected=google") {
		t.Fatalf("setup: first connect failed, got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	before, err := d.Store.ListOAuthCredentials(ctx, user.ID, "google")
	if err != nil || len(before) != 1 {
		t.Fatalf("setup: want exactly 1 credential after first connect, got %d (err %v)", len(before), err)
	}
	firstID := before[0].ID

	// Reconnect the same account, but this time the userinfo endpoint
	// returns invalid JSON -- the label fetch fails. Since this user
	// already has exactly one Google credential, the callback should reuse
	// that credential's existing label so the upsert updates it in place,
	// instead of falling back to a random label that would miss the
	// existing row's unique-key match and insert a duplicate.
	googleMockServers(t,
		`{"access_token":"second-access","refresh_token":"second-refresh","expires_in":3600}`,
		`not valid json`)
	rec = httptest.NewRecorder()
	d.OAuth2CredCallback(rec, callbackReq(user.ID, "google", "s2", "s2", "code2"))
	if rec.Code != http.StatusFound || !strings.Contains(rec.Header().Get("Location"), "connected=google") {
		t.Fatalf("want reconnect to still succeed despite the label-fetch failure, got %d %q", rec.Code, rec.Header().Get("Location"))
	}

	after, err := d.Store.ListOAuthCredentials(ctx, user.ID, "google")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 {
		t.Fatalf("want the reconnect to update the existing row in place, not insert a duplicate -- got %d credentials", len(after))
	}
	if after[0].ID != firstID {
		t.Errorf("want the same credential id preserved across reconnect, got %q want %q", after[0].ID, firstID)
	}
	if after[0].AccountLabel != "reconnect@gmail.com" {
		t.Errorf("want the original label reused when the reconnect's label fetch fails, got %q", after[0].AccountLabel)
	}
}

func TestOAuth2CredCallback_StoresGrantedScopesNotRequestedScopes(t *testing.T) {
	d := oauth2Deps(t)
	ctx := context.Background()
	user, err := d.Store.CreateUser(ctx, "oauth2-scopes-"+randSuffix(t)+"@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}

	// Google's token response includes only a subset of the requested
	// scopes (the user declined part of the combined consent grant) --
	// the stored Scopes column must reflect that subset, not silently
	// claim the full set that was requested.
	googleMockServers(t,
		`{"access_token":"x","refresh_token":"y","expires_in":3600,"scope":"https://www.googleapis.com/auth/gmail.readonly"}`,
		`{"email":"partial@gmail.com"}`)

	rec := httptest.NewRecorder()
	d.OAuth2CredCallback(rec, callbackReq(user.ID, "google", "s1", "s1", "code"))
	if rec.Code != http.StatusFound || !strings.Contains(rec.Header().Get("Location"), "connected=google") {
		t.Fatalf("want successful connect, got %d %q", rec.Code, rec.Header().Get("Location"))
	}

	creds, err := d.Store.ListOAuthCredentials(ctx, user.ID, "google")
	if err != nil || len(creds) != 1 {
		t.Fatalf("want exactly 1 persisted credential, got %d (err %v)", len(creds), err)
	}
	if creds[0].Scopes != "https://www.googleapis.com/auth/gmail.readonly" {
		t.Errorf("want only the granted scope stored, got %q", creds[0].Scopes)
	}
	if strings.Contains(creds[0].Scopes, "spreadsheets") {
		t.Errorf("want ungranted scopes NOT recorded as if they were granted, got %q", creds[0].Scopes)
	}
}

func TestOAuth2CredList_ReturnsOnlyCurrentUsersCredentials(t *testing.T) {
	d := oauth2Deps(t)
	ctx := context.Background()
	userA, err := d.Store.CreateUser(ctx, "oauth2-list-a-"+randSuffix(t)+"@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	userB, err := d.Store.CreateUser(ctx, "oauth2-list-b-"+randSuffix(t)+"@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	insert := func(userID, label string) {
		if _, err := d.Store.InsertOAuthCredential(ctx, models.OAuthCredential{
			UserID: userID, Provider: "google", AccountLabel: label,
			AccessTokenEnc: "enc-a", RefreshTokenEnc: "enc-r",
		}); err != nil {
			t.Fatal(err)
		}
	}
	insert(userA.ID, "a1@gmail.com")
	insert(userA.ID, "a2@gmail.com")
	insert(userB.ID, "b1@gmail.com")

	req := withURLParam(withUser(httptest.NewRequest(http.MethodGet, "/oauth2/credentials?provider=google", nil), userA.ID), "provider", "google")
	rec := httptest.NewRecorder()
	d.OAuth2CredList(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var got []models.OAuthCredential
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want only userA's 2 credentials, got %d", len(got))
	}
	// The tokens must never round-trip to the client -- json:"-" on both
	// fields should make this structurally impossible, but assert the raw
	// body too so a future field rename can't silently reopen the leak.
	if strings.Contains(rec.Body.String(), "enc-a") || strings.Contains(rec.Body.String(), "enc-r") {
		t.Error("want encrypted tokens never present in the response body")
	}
}

func TestOAuth2CredDelete_OwnerCanDelete(t *testing.T) {
	d := oauth2Deps(t)
	ctx := context.Background()
	user, err := d.Store.CreateUser(ctx, "oauth2-del-"+randSuffix(t)+"@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	cred, err := d.Store.InsertOAuthCredential(ctx, models.OAuthCredential{
		UserID: user.ID, Provider: "google", AccessTokenEnc: "e", RefreshTokenEnc: "r",
	})
	if err != nil {
		t.Fatal(err)
	}

	req := withURLParam(withUser(httptest.NewRequest(http.MethodDelete, "/oauth2/credentials/"+cred.ID, nil), user.ID), "id", cred.ID)
	rec := httptest.NewRecorder()
	d.OAuth2CredDelete(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", rec.Code)
	}
	remaining, _ := d.Store.ListOAuthCredentials(ctx, user.ID, "google")
	if len(remaining) != 0 {
		t.Error("want the credential actually gone")
	}
}

func TestOAuth2CredDelete_DeniesOtherUser(t *testing.T) {
	d := oauth2Deps(t)
	ctx := context.Background()
	owner, err := d.Store.CreateUser(ctx, "oauth2-owner-"+randSuffix(t)+"@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	other, err := d.Store.CreateUser(ctx, "oauth2-other-"+randSuffix(t)+"@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	cred, err := d.Store.InsertOAuthCredential(ctx, models.OAuthCredential{
		UserID: owner.ID, Provider: "google", AccessTokenEnc: "e", RefreshTokenEnc: "r",
	})
	if err != nil {
		t.Fatal(err)
	}

	req := withURLParam(withUser(httptest.NewRequest(http.MethodDelete, "/oauth2/credentials/"+cred.ID, nil), other.ID), "id", cred.ID)
	rec := httptest.NewRecorder()
	d.OAuth2CredDelete(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("want 404 for another user's credential, got %d", rec.Code)
	}
	remaining, _ := d.Store.ListOAuthCredentials(ctx, owner.ID, "google")
	if len(remaining) != 1 {
		t.Error("want the owner's credential untouched")
	}
}
