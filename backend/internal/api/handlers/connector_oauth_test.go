package handlers_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/agentmesh/backend/internal/api/handlers"
	"github.com/agentmesh/backend/internal/db"
	"github.com/agentmesh/backend/internal/models"
)

// testStore returns a *db.Store backed by TEST_DATABASE_URL, skipping the
// test when it isn't set — same convention as testDeps in workflows_test.go,
// just returning the bare store since these tests build Deps by hand.
func testStore(t *testing.T) *db.Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	store, err := db.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	return store
}

// setupConnectorTestFixtures creates a user id, a workflow owned by it, and a
// single action node on that workflow (template "fakeprovider"), returning
// (userID, workflowID, nodeID) for the OAuth-linking tests to target.
func setupConnectorTestFixtures(t *testing.T, store *db.Store) (string, string, string) {
	t.Helper()
	ctx := context.Background()
	userID := "connector-oauth-test-user"

	wf, err := store.CreateWorkflow(ctx, "test", userID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })

	_, err = store.UpdateWorkflow(ctx, wf.ID, wf.Name, models.WorkflowGraph{
		Nodes: []models.WorkflowNode{{ID: "node1", Type: models.NodeTypeAction, Template: "fakeprovider"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	return userID, wf.ID, "node1"
}

// mustParseLocationState parses the Location header's state query param.
func mustParseLocationState(t *testing.T, location string) string {
	t.Helper()
	loc, err := url.Parse(location)
	if err != nil {
		t.Fatal(err)
	}
	return loc.Query().Get("state")
}

// fakeProviderServer stands in for a third-party OAuth provider: it serves
// both the authorize redirect target (never actually hit by Go code — the
// browser would go there) and the token endpoint the callback handler POSTs
// to, so the test only needs to fake the token endpoint.
//
// capturedVerifier, when non-nil, is set to whatever `code_verifier` form
// value (if any) arrived in the token-exchange POST, so PKCE tests can
// assert on exactly what reached the token endpoint. Pass nil when a test
// doesn't care about PKCE.
func fakeProviderServer(t *testing.T, wantCode string, capturedVerifier *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if r.FormValue("code") != wantCode {
			t.Fatalf("code = %q, want %q", r.FormValue("code"), wantCode)
		}
		if r.FormValue("client_id") != "test-client-id" || r.FormValue("client_secret") != "test-client-secret" {
			t.Fatalf("client credentials missing from token exchange: %v", r.Form)
		}
		if capturedVerifier != nil {
			*capturedVerifier = r.FormValue("code_verifier")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"fake-access-token","refresh_token":"fake-refresh-token","expires_in":3600}`))
	}))
}

func TestConnectorOAuthStartRedirectsToProviderWithSignedState(t *testing.T) {
	store := testStore(t)
	d := &handlers.Deps{Store: store, JWTSecret: "test-jwt-secret-not-for-production-use-only-32b", BaseURL: "https://example.test", EncryptionKey: "00000000000000000000000000000000"}
	handlers.SetConnectorProviderForTest("fakeprovider", handlers.ConnectorOAuthConfig{
		AuthURL: "https://fakeprovider.test/authorize", TokenURL: "https://fakeprovider.test/token",
		Scope: "read write", ClientIDEnvVal: "test-client-id", ClientSecretEnvVal: "test-client-secret",
	})
	defer handlers.ClearConnectorProviderForTest("fakeprovider")

	userID, workflowID, nodeID := setupConnectorTestFixtures(t, store)

	req := httptest.NewRequest(http.MethodGet, "/connectors/oauth/fakeprovider/start?workflowId="+workflowID+"&nodeId="+nodeID, nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.CtxUserID, userID))
	req = withURLParam(req, "provider", "fakeprovider")
	rec := httptest.NewRecorder()

	d.ConnectorOAuthStart(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusFound, rec.Body.String())
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if loc.Scheme+"://"+loc.Host+loc.Path != "https://fakeprovider.test/authorize" {
		t.Fatalf("redirected to %q, want fakeprovider authorize URL", rec.Header().Get("Location"))
	}
	if loc.Query().Get("state") == "" {
		t.Fatal("missing state param")
	}
	if len(rec.Result().Cookies()) == 0 {
		t.Fatal("expected a state cookie to be set")
	}
}

func TestConnectorOAuthCallbackWritesEncryptedTokenIntoNodeSecrets(t *testing.T) {
	provider := fakeProviderServer(t, "the-auth-code", nil)
	defer provider.Close()

	store := testStore(t)
	d := &handlers.Deps{Store: store, JWTSecret: "test-jwt-secret-not-for-production-use-only-32b", BaseURL: "https://example.test", EncryptionKey: "00000000000000000000000000000000"}
	handlers.SetConnectorProviderForTest("fakeprovider", handlers.ConnectorOAuthConfig{
		AuthURL: provider.URL + "/authorize", TokenURL: provider.URL + "/token",
		Scope: "read write", ClientIDEnvVal: "test-client-id", ClientSecretEnvVal: "test-client-secret",
	})
	defer handlers.ClearConnectorProviderForTest("fakeprovider")

	userID, workflowID, nodeID := setupConnectorTestFixtures(t, store)

	// Drive Start first to get a real signed state + matching cookie, exactly
	// as the browser round-trip would.
	startReq := httptest.NewRequest(http.MethodGet, "/connectors/oauth/fakeprovider/start?workflowId="+workflowID+"&nodeId="+nodeID, nil)
	startReq = startReq.WithContext(context.WithValue(startReq.Context(), handlers.CtxUserID, userID))
	startReq = withURLParam(startReq, "provider", "fakeprovider")
	startRec := httptest.NewRecorder()
	d.ConnectorOAuthStart(startRec, startReq)
	state := mustParseLocationState(t, startRec.Header().Get("Location"))
	stateCookie := startRec.Result().Cookies()[0]

	cbReq := httptest.NewRequest(http.MethodGet, "/connectors/oauth/fakeprovider/callback?code=the-auth-code&state="+url.QueryEscape(state), nil)
	cbReq = cbReq.WithContext(context.WithValue(cbReq.Context(), handlers.CtxUserID, userID))
	cbReq = withURLParam(cbReq, "provider", "fakeprovider")
	cbReq.AddCookie(stateCookie)
	cbRec := httptest.NewRecorder()

	d.ConnectorOAuthCallback(cbRec, cbReq)

	if cbRec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d, body=%s", cbRec.Code, http.StatusFound, cbRec.Body.String())
	}

	wf, err := store.GetWorkflow(context.Background(), workflowID)
	if err != nil {
		t.Fatal(err)
	}
	var node models.WorkflowNode
	for _, n := range wf.Nodes {
		if n.ID == nodeID {
			node = n
		}
	}
	enc := node.Secrets["fakeproviderOAuthAccessToken"]
	if enc == "" || !strings.HasPrefix(enc, "enc:") {
		t.Fatalf("fakeproviderOAuthAccessToken = %q, want encrypted (enc: prefix)", enc)
	}
	if node.Config["fakeproviderOAuthExpiresAt"] == "" {
		t.Fatal("expected fakeproviderOAuthExpiresAt to be set in Config")
	}
}

// TestConnectorOAuthCallbackUsesBasicAuthForTokenExchangeWhenConfigured covers
// Notion-style providers (TokenAuthStyle: "basic") whose token endpoint wants
// client credentials as an HTTP Basic Authorization header instead of the
// default form-body fields exchangeConnectorCode otherwise sends.
func TestConnectorOAuthCallbackUsesBasicAuthForTokenExchangeWhenConfigured(t *testing.T) {
	var gotAuthHeader string
	var sawClientIDInForm bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		gotAuthHeader = r.Header.Get("Authorization")
		sawClientIDInForm = r.FormValue("client_id") != "" || r.FormValue("client_secret") != ""
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"fake-access-token"}`))
	}))
	defer provider.Close()

	store := testStore(t)
	d := &handlers.Deps{Store: store, JWTSecret: "test-jwt-secret-not-for-production-use-only-32b", BaseURL: "https://example.test", EncryptionKey: "00000000000000000000000000000000"}
	handlers.SetConnectorProviderForTest("basicprovider", handlers.ConnectorOAuthConfig{
		AuthURL: provider.URL + "/authorize", TokenURL: provider.URL + "/token",
		TokenAuthStyle: "basic", ClientIDEnvVal: "test-client-id", ClientSecretEnvVal: "test-client-secret",
	})
	defer handlers.ClearConnectorProviderForTest("basicprovider")

	userID, workflowID, nodeID := setupConnectorTestFixtures(t, store)

	startReq := httptest.NewRequest(http.MethodGet, "/connectors/oauth/basicprovider/start?workflowId="+workflowID+"&nodeId="+nodeID, nil)
	startReq = startReq.WithContext(context.WithValue(startReq.Context(), handlers.CtxUserID, userID))
	startReq = withURLParam(startReq, "provider", "basicprovider")
	startRec := httptest.NewRecorder()
	d.ConnectorOAuthStart(startRec, startReq)
	state := mustParseLocationState(t, startRec.Header().Get("Location"))
	stateCookie := startRec.Result().Cookies()[0]

	cbReq := httptest.NewRequest(http.MethodGet, "/connectors/oauth/basicprovider/callback?code=the-auth-code&state="+url.QueryEscape(state), nil)
	cbReq = cbReq.WithContext(context.WithValue(cbReq.Context(), handlers.CtxUserID, userID))
	cbReq = withURLParam(cbReq, "provider", "basicprovider")
	cbReq.AddCookie(stateCookie)
	cbRec := httptest.NewRecorder()

	d.ConnectorOAuthCallback(cbRec, cbReq)

	if cbRec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d, body=%s", cbRec.Code, http.StatusFound, cbRec.Body.String())
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("test-client-id:test-client-secret"))
	if gotAuthHeader != wantAuth {
		t.Fatalf("Authorization header = %q, want %q", gotAuthHeader, wantAuth)
	}
	if sawClientIDInForm {
		t.Fatal("client_id/client_secret should not be sent in the form body when TokenAuthStyle is basic")
	}
}

// TestConnectorOAuthStartOmitsEmptyScopeAndSetsExtraAuthParams covers
// Notion-style providers that use no scope string at all (access is
// controlled by page-level consent, not scopes) but do require extra fixed
// query parameters (e.g. Notion's owner=user) on the authorize redirect.
func TestConnectorOAuthStartOmitsEmptyScopeAndSetsExtraAuthParams(t *testing.T) {
	store := testStore(t)
	d := &handlers.Deps{Store: store, JWTSecret: "test-jwt-secret-not-for-production-use-only-32b", BaseURL: "https://example.test", EncryptionKey: "00000000000000000000000000000000"}
	handlers.SetConnectorProviderForTest("noscopeprovider", handlers.ConnectorOAuthConfig{
		AuthURL: "https://noscopeprovider.test/authorize", TokenURL: "https://noscopeprovider.test/token",
		Scope: "", ExtraAuthParams: map[string]string{"owner": "user"},
		ClientIDEnvVal: "test-client-id", ClientSecretEnvVal: "test-client-secret",
	})
	defer handlers.ClearConnectorProviderForTest("noscopeprovider")

	userID, workflowID, nodeID := setupConnectorTestFixtures(t, store)
	req := httptest.NewRequest(http.MethodGet, "/connectors/oauth/noscopeprovider/start?workflowId="+workflowID+"&nodeId="+nodeID, nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.CtxUserID, userID))
	req = withURLParam(req, "provider", "noscopeprovider")
	rec := httptest.NewRecorder()

	d.ConnectorOAuthStart(rec, req)

	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if _, present := loc.Query()["scope"]; present {
		t.Fatalf("expected no scope param at all, got %q", loc.Query().Get("scope"))
	}
	if loc.Query().Get("owner") != "user" {
		t.Fatalf("owner = %q, want %q", loc.Query().Get("owner"), "user")
	}
}

func TestConnectorOAuthCallbackRejectsMismatchedState(t *testing.T) {
	store := testStore(t)
	d := &handlers.Deps{Store: store, JWTSecret: "test-jwt-secret-not-for-production-use-only-32b", BaseURL: "https://example.test", EncryptionKey: "00000000000000000000000000000000"}
	handlers.SetConnectorProviderForTest("fakeprovider", handlers.ConnectorOAuthConfig{
		AuthURL: "https://fakeprovider.test/authorize", TokenURL: "https://fakeprovider.test/token",
		ClientIDEnvVal: "test-client-id", ClientSecretEnvVal: "test-client-secret",
	})
	defer handlers.ClearConnectorProviderForTest("fakeprovider")

	userID, workflowID, nodeID := setupConnectorTestFixtures(t, store)
	startReq := httptest.NewRequest(http.MethodGet, "/connectors/oauth/fakeprovider/start?workflowId="+workflowID+"&nodeId="+nodeID, nil)
	startReq = startReq.WithContext(context.WithValue(startReq.Context(), handlers.CtxUserID, userID))
	startReq = withURLParam(startReq, "provider", "fakeprovider")
	startRec := httptest.NewRecorder()
	d.ConnectorOAuthStart(startRec, startReq)
	stateCookie := startRec.Result().Cookies()[0]

	cbReq := httptest.NewRequest(http.MethodGet, "/connectors/oauth/fakeprovider/callback?code=whatever&state=tampered-state-value", nil)
	cbReq = cbReq.WithContext(context.WithValue(cbReq.Context(), handlers.CtxUserID, userID))
	cbReq = withURLParam(cbReq, "provider", "fakeprovider")
	cbReq.AddCookie(stateCookie)
	cbRec := httptest.NewRecorder()

	d.ConnectorOAuthCallback(cbRec, cbReq)

	loc := cbRec.Header().Get("Location")
	if !strings.Contains(loc, "connectError=") {
		t.Fatalf("expected redirect to carry a connectError, got %q", loc)
	}
}

// TestConnectorOAuthPKCEVerifierNeverLeavesFrontChannel proves the PKCE
// code_verifier travels only through a dedicated HttpOnly cookie, never
// through the `state` query param that also gets sent to the third-party
// /authorize endpoint (the front channel). It exercises both ends:
//
//  1. Start: the redirect carries a code_challenge, and the decoded state
//     JWT payload contains neither a "verifier" claim nor the raw verifier
//     value itself.
//  2. Callback: the verifier read back from the separate cookie — not
//     anything derived from the state JWT — is what actually reaches the
//     token endpoint, and that cookie gets cleared in the response.
func TestConnectorOAuthPKCEVerifierNeverLeavesFrontChannel(t *testing.T) {
	var capturedVerifier string
	provider := fakeProviderServer(t, "the-pkce-auth-code", &capturedVerifier)
	defer provider.Close()

	store := testStore(t)
	d := &handlers.Deps{Store: store, JWTSecret: "test-jwt-secret-not-for-production-use-only-32b", BaseURL: "https://example.test", EncryptionKey: "00000000000000000000000000000000"}
	handlers.SetConnectorProviderForTest("pkceprovider", handlers.ConnectorOAuthConfig{
		AuthURL: provider.URL + "/authorize", TokenURL: provider.URL + "/token",
		Scope: "read", UsesPKCE: true, ClientIDEnvVal: "test-client-id", ClientSecretEnvVal: "test-client-secret",
	})
	defer handlers.ClearConnectorProviderForTest("pkceprovider")

	userID, workflowID, nodeID := setupConnectorTestFixtures(t, store)

	startReq := httptest.NewRequest(http.MethodGet, "/connectors/oauth/pkceprovider/start?workflowId="+workflowID+"&nodeId="+nodeID, nil)
	startReq = startReq.WithContext(context.WithValue(startReq.Context(), handlers.CtxUserID, userID))
	startReq = withURLParam(startReq, "provider", "pkceprovider")
	startRec := httptest.NewRecorder()
	d.ConnectorOAuthStart(startRec, startReq)

	if startRec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d, body=%s", startRec.Code, http.StatusFound, startRec.Body.String())
	}

	loc, err := url.Parse(startRec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	challenge := loc.Query().Get("code_challenge")
	if challenge == "" {
		t.Fatal("expected a code_challenge in the authorize redirect URL")
	}
	if loc.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("code_challenge_method = %q, want S256", loc.Query().Get("code_challenge_method"))
	}

	state := loc.Query().Get("state")
	if state == "" {
		t.Fatal("missing state param")
	}

	// Locate the two cookies Start should have set: the signed state cookie,
	// and a SEPARATE cookie carrying the raw PKCE verifier.
	var stateCookie, verifierCookie *http.Cookie
	for _, c := range startRec.Result().Cookies() {
		switch c.Name {
		case "connector_oauth_state_pkceprovider":
			stateCookie = c
		case "connector_oauth_verifier_pkceprovider":
			verifierCookie = c
		}
	}
	if stateCookie == nil {
		t.Fatal("expected a state cookie")
	}
	if verifierCookie == nil || verifierCookie.Value == "" {
		t.Fatal("expected a separate, non-empty PKCE verifier cookie")
	}

	// Decode the JWT payload (the middle of its 3 dot-separated segments)
	// exactly as anyone observing this URL (provider server logs, browser
	// history, a leaked referrer) could, without needing the signing key.
	parts := strings.Split(state, ".")
	if len(parts) != 3 {
		t.Fatalf("state is not a JWT: %q", state)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode JWT payload: %v", err)
	}
	if strings.Contains(strings.ToLower(string(payload)), "verifier") {
		t.Fatalf("state JWT payload names the PKCE verifier: %s", payload)
	}
	if strings.Contains(string(payload), verifierCookie.Value) {
		t.Fatalf("state JWT payload contains the raw PKCE verifier value: %s", payload)
	}

	// Confirm the challenge in the URL really is S256(cookie's verifier),
	// i.e. Start didn't invent an unrelated challenge alongside a leaked one.
	sum := sha256.Sum256([]byte(verifierCookie.Value))
	wantChallenge := base64.RawURLEncoding.EncodeToString(sum[:])
	if challenge != wantChallenge {
		t.Fatalf("code_challenge = %q, want S256(verifier cookie) = %q", challenge, wantChallenge)
	}

	// Drive Callback presenting both cookies, exactly as a real browser
	// round-trip would, and confirm the cookie's verifier — not anything
	// from the state JWT — is what reaches the token endpoint.
	cbReq := httptest.NewRequest(http.MethodGet, "/connectors/oauth/pkceprovider/callback?code=the-pkce-auth-code&state="+url.QueryEscape(state), nil)
	cbReq = cbReq.WithContext(context.WithValue(cbReq.Context(), handlers.CtxUserID, userID))
	cbReq = withURLParam(cbReq, "provider", "pkceprovider")
	cbReq.AddCookie(stateCookie)
	cbReq.AddCookie(verifierCookie)
	cbRec := httptest.NewRecorder()

	d.ConnectorOAuthCallback(cbRec, cbReq)

	if cbRec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d, body=%s", cbRec.Code, http.StatusFound, cbRec.Body.String())
	}
	if capturedVerifier == "" {
		t.Fatal("token exchange never received a code_verifier")
	}
	if capturedVerifier != verifierCookie.Value {
		t.Fatalf("code_verifier sent to token endpoint = %q, want cookie value %q", capturedVerifier, verifierCookie.Value)
	}

	wf, err := store.GetWorkflow(context.Background(), workflowID)
	if err != nil {
		t.Fatal(err)
	}
	var node models.WorkflowNode
	for _, n := range wf.Nodes {
		if n.ID == nodeID {
			node = n
		}
	}
	if node.Secrets["pkceproviderOAuthAccessToken"] == "" {
		t.Fatal("expected pkceproviderOAuthAccessToken to be set after a successful PKCE exchange")
	}

	// The verifier cookie must be cleared in the callback response, same as
	// the state cookie already is.
	cleared := false
	for _, c := range cbRec.Result().Cookies() {
		if c.Name == "connector_oauth_verifier_pkceprovider" && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("expected the PKCE verifier cookie to be cleared in the callback response")
	}
}

// TestConnectorOAuthAirtableTokenExchangeUsesBasicAuth exercises the real,
// registered "airtable" provider entry (not a synthetic SetConnectorProviderForTest
// stand-in) end-to-end through ConnectorOAuthStart/ConnectorOAuthCallback,
// with Deps' real AirtableClientID/AirtableClientSecret fields set to test
// values and only the entry's TokenURL redirected at a local fake server (via
// SetConnectorTokenURLForTest) so the exchange never leaves the process.
// Airtable's token endpoint requires client credentials as HTTP Basic auth,
// never in the form body — see TokenAuthStyle's doc comment and the "airtable"
// entry in registerConnectorProviders.
func TestConnectorOAuthAirtableTokenExchangeUsesBasicAuth(t *testing.T) {
	var gotAuthHeader string
	var sawClientCredsInForm bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		gotAuthHeader = r.Header.Get("Authorization")
		sawClientCredsInForm = r.FormValue("client_id") != "" || r.FormValue("client_secret") != ""
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"fake-airtable-access-token"}`))
	}))
	defer provider.Close()

	handlers.SetConnectorTokenURLForTest("airtable", provider.URL+"/token")
	defer handlers.ClearConnectorTokenURLForTest("airtable")

	store := testStore(t)
	d := &handlers.Deps{
		Store: store, JWTSecret: "test-jwt-secret-not-for-production-use-only-32b", BaseURL: "https://example.test", EncryptionKey: "00000000000000000000000000000000",
		AirtableClientID: "test-airtable-client-id", AirtableClientSecret: "test-airtable-client-secret",
	}

	userID, workflowID, nodeID := setupConnectorTestFixtures(t, store)

	startReq := httptest.NewRequest(http.MethodGet, "/connectors/oauth/airtable/start?workflowId="+workflowID+"&nodeId="+nodeID, nil)
	startReq = startReq.WithContext(context.WithValue(startReq.Context(), handlers.CtxUserID, userID))
	startReq = withURLParam(startReq, "provider", "airtable")
	startRec := httptest.NewRecorder()
	d.ConnectorOAuthStart(startRec, startReq)

	if startRec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d, body=%s", startRec.Code, http.StatusFound, startRec.Body.String())
	}
	state := mustParseLocationState(t, startRec.Header().Get("Location"))

	var stateCookie, verifierCookie *http.Cookie
	for _, c := range startRec.Result().Cookies() {
		switch c.Name {
		case "connector_oauth_state_airtable":
			stateCookie = c
		case "connector_oauth_verifier_airtable":
			verifierCookie = c
		}
	}
	if stateCookie == nil {
		t.Fatal("expected a state cookie")
	}
	if verifierCookie == nil || verifierCookie.Value == "" {
		t.Fatal("expected a PKCE verifier cookie since airtable's real entry requires PKCE")
	}

	cbReq := httptest.NewRequest(http.MethodGet, "/connectors/oauth/airtable/callback?code=the-auth-code&state="+url.QueryEscape(state), nil)
	cbReq = cbReq.WithContext(context.WithValue(cbReq.Context(), handlers.CtxUserID, userID))
	cbReq = withURLParam(cbReq, "provider", "airtable")
	cbReq.AddCookie(stateCookie)
	cbReq.AddCookie(verifierCookie)
	cbRec := httptest.NewRecorder()

	d.ConnectorOAuthCallback(cbRec, cbReq)

	if cbRec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d, body=%s", cbRec.Code, http.StatusFound, cbRec.Body.String())
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("test-airtable-client-id:test-airtable-client-secret"))
	if gotAuthHeader != wantAuth {
		t.Fatalf("Authorization header = %q, want %q", gotAuthHeader, wantAuth)
	}
	if sawClientCredsInForm {
		t.Fatal("client_id/client_secret should not be sent in the form body for airtable's token exchange")
	}
}

// TestConnectorOAuthJiraTokenExchangeUsesJSONBodyAndAudienceParam exercises
// the real, registered "jira" provider entry end-to-end through
// ConnectorOAuthStart/ConnectorOAuthCallback, confirming two of Jira's
// documented divergences from every other provider in this registry: the
// /authorize redirect carries audience=api.atlassian.com and prompt=consent,
// and the token exchange POSTs a JSON body (Content-Type: application/json)
// instead of the form-encoded body every other connector sends.
func TestConnectorOAuthJiraTokenExchangeUsesJSONBodyAndAudienceParam(t *testing.T) {
	var gotContentType string
	var gotBody map[string]any
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"fake-jira-access-token","refresh_token":"fake-jira-refresh-token","expires_in":3600}`))
	}))
	defer provider.Close()

	resources := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"id":"cloud-id-abc123","name":"Acme","url":"https://acme.atlassian.net"}]`))
	}))
	defer resources.Close()

	handlers.SetConnectorTokenURLForTest("jira", provider.URL+"/token")
	defer handlers.ClearConnectorTokenURLForTest("jira")
	handlers.SetJiraAccessibleResourcesURLForTest(resources.URL)
	defer handlers.SetJiraAccessibleResourcesURLForTest("")

	store := testStore(t)
	d := &handlers.Deps{
		Store: store, JWTSecret: "test-jwt-secret-not-for-production-use-only-32b", BaseURL: "https://example.test", EncryptionKey: "00000000000000000000000000000000",
		JiraClientID: "test-jira-client-id", JiraClientSecret: "test-jira-client-secret",
	}

	userID, workflowID, nodeID := setupConnectorTestFixtures(t, store)

	startReq := httptest.NewRequest(http.MethodGet, "/connectors/oauth/jira/start?workflowId="+workflowID+"&nodeId="+nodeID, nil)
	startReq = startReq.WithContext(context.WithValue(startReq.Context(), handlers.CtxUserID, userID))
	startReq = withURLParam(startReq, "provider", "jira")
	startRec := httptest.NewRecorder()
	d.ConnectorOAuthStart(startRec, startReq)

	if startRec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d, body=%s", startRec.Code, http.StatusFound, startRec.Body.String())
	}
	loc, err := url.Parse(startRec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if loc.Query().Get("audience") != "api.atlassian.com" {
		t.Fatalf("audience = %q, want api.atlassian.com", loc.Query().Get("audience"))
	}
	if loc.Query().Get("prompt") != "consent" {
		t.Fatalf("prompt = %q, want consent", loc.Query().Get("prompt"))
	}
	state := loc.Query().Get("state")
	stateCookie := startRec.Result().Cookies()[0]

	cbReq := httptest.NewRequest(http.MethodGet, "/connectors/oauth/jira/callback?code=the-auth-code&state="+url.QueryEscape(state), nil)
	cbReq = cbReq.WithContext(context.WithValue(cbReq.Context(), handlers.CtxUserID, userID))
	cbReq = withURLParam(cbReq, "provider", "jira")
	cbReq.AddCookie(stateCookie)
	cbRec := httptest.NewRecorder()

	d.ConnectorOAuthCallback(cbRec, cbReq)

	if cbRec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d, body=%s", cbRec.Code, http.StatusFound, cbRec.Body.String())
	}
	if gotContentType != "application/json" {
		t.Fatalf("token exchange Content-Type = %q, want application/json", gotContentType)
	}
	if gotBody["client_id"] != "test-jira-client-id" || gotBody["client_secret"] != "test-jira-client-secret" {
		t.Fatalf("expected client credentials in JSON token exchange body, got %v", gotBody)
	}
	if gotBody["grant_type"] != "authorization_code" || gotBody["code"] != "the-auth-code" {
		t.Fatalf("unexpected token exchange body: %v", gotBody)
	}

	wf, err := store.GetWorkflow(context.Background(), workflowID)
	if err != nil {
		t.Fatal(err)
	}
	var node models.WorkflowNode
	for _, n := range wf.Nodes {
		if n.ID == nodeID {
			node = n
		}
	}
	if node.Secrets["jiraOAuthAccessToken"] == "" {
		t.Fatal("expected jiraOAuthAccessToken to be set after a successful exchange")
	}
	if node.Config["jiraOAuthCloudID"] != "cloud-id-abc123" {
		t.Fatalf("jiraOAuthCloudID = %q, want %q", node.Config["jiraOAuthCloudID"], "cloud-id-abc123")
	}
}

// TestConnectorOAuthJiraLinkFailsWhenAccessibleResourcesLookupFails proves the
// whole link is rejected (no partial state written) when the post-exchange
// accessible-resources call itself fails — e.g. because the token somehow
// isn't valid against that endpoint. Without cloudId, sendJira's OAuth branch
// can never build a usable request, so silently linking anyway would leave a
// connector that looks connected but can never work.
func TestConnectorOAuthJiraLinkFailsWhenAccessibleResourcesLookupFails(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"fake-jira-access-token"}`))
	}))
	defer provider.Close()

	resources := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	}))
	defer resources.Close()

	handlers.SetConnectorTokenURLForTest("jira", provider.URL+"/token")
	defer handlers.ClearConnectorTokenURLForTest("jira")
	handlers.SetJiraAccessibleResourcesURLForTest(resources.URL)
	defer handlers.SetJiraAccessibleResourcesURLForTest("")

	store := testStore(t)
	d := &handlers.Deps{
		Store: store, JWTSecret: "test-jwt-secret-not-for-production-use-only-32b", BaseURL: "https://example.test", EncryptionKey: "00000000000000000000000000000000",
		JiraClientID: "test-jira-client-id", JiraClientSecret: "test-jira-client-secret",
	}

	userID, workflowID, nodeID := setupConnectorTestFixtures(t, store)

	startReq := httptest.NewRequest(http.MethodGet, "/connectors/oauth/jira/start?workflowId="+workflowID+"&nodeId="+nodeID, nil)
	startReq = startReq.WithContext(context.WithValue(startReq.Context(), handlers.CtxUserID, userID))
	startReq = withURLParam(startReq, "provider", "jira")
	startRec := httptest.NewRecorder()
	d.ConnectorOAuthStart(startRec, startReq)
	state := mustParseLocationState(t, startRec.Header().Get("Location"))
	stateCookie := startRec.Result().Cookies()[0]

	cbReq := httptest.NewRequest(http.MethodGet, "/connectors/oauth/jira/callback?code=the-auth-code&state="+url.QueryEscape(state), nil)
	cbReq = cbReq.WithContext(context.WithValue(cbReq.Context(), handlers.CtxUserID, userID))
	cbReq = withURLParam(cbReq, "provider", "jira")
	cbReq.AddCookie(stateCookie)
	cbRec := httptest.NewRecorder()

	d.ConnectorOAuthCallback(cbRec, cbReq)

	loc := cbRec.Header().Get("Location")
	if !strings.Contains(loc, "connectError=") {
		t.Fatalf("expected redirect to carry a connectError when the accessible-resources lookup fails, got %q", loc)
	}

	wf, err := store.GetWorkflow(context.Background(), workflowID)
	if err != nil {
		t.Fatal(err)
	}
	var node models.WorkflowNode
	for _, n := range wf.Nodes {
		if n.ID == nodeID {
			node = n
		}
	}
	if node.Secrets["jiraOAuthAccessToken"] != "" {
		t.Fatal("expected no token to be persisted when the post-exchange hook fails")
	}
}

// TestConnectorOAuthMailchimpLinkStoresOAuthDC exercises the real, registered
// "mailchimp" provider entry end-to-end through ConnectorOAuthStart/
// ConnectorOAuthCallback, proving its PostExchangeHook calls the metadata
// endpoint and writes the returned dc into node.Config, same shape as Jira's
// cloudId test above.
func TestConnectorOAuthMailchimpLinkStoresOAuthDC(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"fake-mailchimp-access-token"}`))
	}))
	defer provider.Close()

	var gotAuth string
	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"dc":"us21"}`))
	}))
	defer metadata.Close()

	handlers.SetConnectorTokenURLForTest("mailchimp", provider.URL+"/token")
	defer handlers.ClearConnectorTokenURLForTest("mailchimp")
	handlers.SetMailchimpMetadataURLForTest(metadata.URL)
	defer handlers.SetMailchimpMetadataURLForTest("")

	store := testStore(t)
	d := &handlers.Deps{
		Store: store, JWTSecret: "test-jwt-secret-not-for-production-use-only-32b", BaseURL: "https://example.test", EncryptionKey: "00000000000000000000000000000000",
		MailchimpClientID: "test-mailchimp-client-id", MailchimpClientSecret: "test-mailchimp-client-secret",
	}

	userID, workflowID, nodeID := setupConnectorTestFixtures(t, store)

	startReq := httptest.NewRequest(http.MethodGet, "/connectors/oauth/mailchimp/start?workflowId="+workflowID+"&nodeId="+nodeID, nil)
	startReq = startReq.WithContext(context.WithValue(startReq.Context(), handlers.CtxUserID, userID))
	startReq = withURLParam(startReq, "provider", "mailchimp")
	startRec := httptest.NewRecorder()
	d.ConnectorOAuthStart(startRec, startReq)

	if startRec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d, body=%s", startRec.Code, http.StatusFound, startRec.Body.String())
	}
	loc, err := url.Parse(startRec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	state := loc.Query().Get("state")
	stateCookie := startRec.Result().Cookies()[0]

	cbReq := httptest.NewRequest(http.MethodGet, "/connectors/oauth/mailchimp/callback?code=the-auth-code&state="+url.QueryEscape(state), nil)
	cbReq = cbReq.WithContext(context.WithValue(cbReq.Context(), handlers.CtxUserID, userID))
	cbReq = withURLParam(cbReq, "provider", "mailchimp")
	cbReq.AddCookie(stateCookie)
	cbRec := httptest.NewRecorder()

	d.ConnectorOAuthCallback(cbRec, cbReq)

	if cbRec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d, body=%s", cbRec.Code, http.StatusFound, cbRec.Body.String())
	}
	if gotAuth != "OAuth fake-mailchimp-access-token" {
		t.Fatalf("metadata Authorization header = %q, want literal OAuth scheme", gotAuth)
	}

	wf, err := store.GetWorkflow(context.Background(), workflowID)
	if err != nil {
		t.Fatal(err)
	}
	var node models.WorkflowNode
	for _, n := range wf.Nodes {
		if n.ID == nodeID {
			node = n
		}
	}
	if node.Secrets["mailchimpOAuthAccessToken"] == "" {
		t.Fatal("expected mailchimpOAuthAccessToken to be set after a successful exchange")
	}
	if node.Config["mailchimpOAuthDC"] != "us21" {
		t.Fatalf("mailchimpOAuthDC = %q, want %q", node.Config["mailchimpOAuthDC"], "us21")
	}
}

// TestConnectorOAuthMailchimpLinkFailsWhenMetadataLookupFails proves the whole
// link is rejected (no partial state written) when the post-exchange metadata
// call itself fails, same shape as Jira's equivalent failure test above.
func TestConnectorOAuthMailchimpLinkFailsWhenMetadataLookupFails(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"fake-mailchimp-access-token"}`))
	}))
	defer provider.Close()

	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{}`))
	}))
	defer metadata.Close()

	handlers.SetConnectorTokenURLForTest("mailchimp", provider.URL+"/token")
	defer handlers.ClearConnectorTokenURLForTest("mailchimp")
	handlers.SetMailchimpMetadataURLForTest(metadata.URL)
	defer handlers.SetMailchimpMetadataURLForTest("")

	store := testStore(t)
	d := &handlers.Deps{
		Store: store, JWTSecret: "test-jwt-secret-not-for-production-use-only-32b", BaseURL: "https://example.test", EncryptionKey: "00000000000000000000000000000000",
		MailchimpClientID: "test-mailchimp-client-id", MailchimpClientSecret: "test-mailchimp-client-secret",
	}

	userID, workflowID, nodeID := setupConnectorTestFixtures(t, store)

	startReq := httptest.NewRequest(http.MethodGet, "/connectors/oauth/mailchimp/start?workflowId="+workflowID+"&nodeId="+nodeID, nil)
	startReq = startReq.WithContext(context.WithValue(startReq.Context(), handlers.CtxUserID, userID))
	startReq = withURLParam(startReq, "provider", "mailchimp")
	startRec := httptest.NewRecorder()
	d.ConnectorOAuthStart(startRec, startReq)
	state := mustParseLocationState(t, startRec.Header().Get("Location"))
	stateCookie := startRec.Result().Cookies()[0]

	cbReq := httptest.NewRequest(http.MethodGet, "/connectors/oauth/mailchimp/callback?code=the-auth-code&state="+url.QueryEscape(state), nil)
	cbReq = cbReq.WithContext(context.WithValue(cbReq.Context(), handlers.CtxUserID, userID))
	cbReq = withURLParam(cbReq, "provider", "mailchimp")
	cbReq.AddCookie(stateCookie)
	cbRec := httptest.NewRecorder()

	d.ConnectorOAuthCallback(cbRec, cbReq)

	loc := cbRec.Header().Get("Location")
	if !strings.Contains(loc, "connectError=") {
		t.Fatalf("expected redirect to carry a connectError when the metadata lookup fails, got %q", loc)
	}

	wf, err := store.GetWorkflow(context.Background(), workflowID)
	if err != nil {
		t.Fatal(err)
	}
	var node models.WorkflowNode
	for _, n := range wf.Nodes {
		if n.ID == nodeID {
			node = n
		}
	}
	if node.Secrets["mailchimpOAuthAccessToken"] != "" {
		t.Fatal("expected no token to be persisted when the post-exchange hook fails")
	}
}
