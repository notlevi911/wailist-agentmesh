package nodes_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentmesh/backend/internal/engine"
	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/wallet"
)

const googleTestKey = "0123456789abcdef0123456789abcdef" // exactly 32 bytes

// fakeOAuthStore is an in-memory oauthcred.Store double, scoped to this
// package's tests -- mirrors internal/oauthcred's own fakeStore, but that
// one is unexported in a different package, so this is a small deliberate
// duplication rather than a shared testutil dependency neither package
// otherwise has.
type fakeOAuthStore struct {
	creds map[string]models.OAuthCredential
}

func (s *fakeOAuthStore) GetOAuthCredential(ctx context.Context, id string) (models.OAuthCredential, error) {
	c, ok := s.creds[id]
	if !ok {
		return models.OAuthCredential{}, context.DeadlineExceeded // any non-nil error
	}
	return c, nil
}

func (s *fakeOAuthStore) UpdateOAuthCredentialTokens(ctx context.Context, id, accessTokenEnc, refreshTokenEnc string, expiresAt time.Time) error {
	c := s.creds[id]
	c.AccessTokenEnc = accessTokenEnc
	c.ExpiresAt = expiresAt
	s.creds[id] = c
	return nil
}

// googleTestNode builds a live (non-expired) fake credential owned by
// userID and returns a GoogleConfig + WorkflowNode config wired to it, so
// individual tests only need to add their own operation-specific Config
// keys and Template.
func googleTestSetup(t *testing.T, userID string) (nodes.GoogleConfig, map[string]string) {
	t.Helper()
	accessEnc, err := wallet.Encrypt("live-access-token", googleTestKey)
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeOAuthStore{creds: map[string]models.OAuthCredential{
		"cred1": {ID: "cred1", UserID: userID, Provider: "google", AccessTokenEnc: accessEnc, ExpiresAt: time.Now().Add(time.Hour)},
	}}
	cfg := nodes.GoogleConfig{Store: store, EncryptKey: googleTestKey, UserID: userID}
	return cfg, map[string]string{"oauthCredentialID": "cred1"}
}

func TestExecuteGoogle_UnknownTemplateErrors(t *testing.T) {
	cfg, config := googleTestSetup(t, "u1")
	node := models.WorkflowNode{ID: "g1", Type: models.NodeTypeGoogle, Template: "gmail_nonexistent", Config: config}
	rc := engine.NewRunContext("r1", nil)
	if _, err := nodes.ExecuteGoogle(context.Background(), node, rc, cfg); err == nil {
		t.Fatal("want error for unknown gmail template")
	}
}

func TestGoogleAccessToken_ErrorsWhenNoCredentialSelected(t *testing.T) {
	cfg, _ := googleTestSetup(t, "u1")
	node := models.WorkflowNode{ID: "g2", Type: models.NodeTypeGoogle, Template: "gmail_list"}
	rc := engine.NewRunContext("r1", nil)
	_, err := nodes.ExecuteGoogle(context.Background(), node, rc, cfg)
	if err == nil || !strings.Contains(err.Error(), "no connected account") {
		t.Fatalf("want 'no connected account' error, got %v", err)
	}
}

// Dormant today since only "google" is ever registered in oauth_credentials,
// but the moment a second provider shares that table, a node's
// oauthCredentialID must not be pointable at one of the user's own
// credentials for a DIFFERENT provider just because the user ID matches.
func TestGoogleAccessToken_DeniesNonGoogleCredential(t *testing.T) {
	cfg, config := googleTestSetup(t, "u1")
	store := cfg.Store.(*fakeOAuthStore)
	cred := store.creds["cred1"]
	cred.Provider = "slack"
	store.creds["cred1"] = cred

	node := models.WorkflowNode{ID: "g3b", Type: models.NodeTypeGoogle, Template: "gmail_list", Config: config}
	rc := engine.NewRunContext("r1", nil)
	_, err := nodes.ExecuteGoogle(context.Background(), node, rc, cfg)
	if err == nil || !strings.Contains(err.Error(), "not a Google credential") {
		t.Fatalf("want 'not a Google credential' error, got %v", err)
	}
}

// The single most important test in this file: a node must never be able
// to use a Google connection that belongs to a DIFFERENT AgentMesh user,
// even if it somehow ends up with that user's credential ID in its config
// (a copied/shared workflow, a leaked ID, etc).
func TestGoogleAccessToken_DeniesAccessToAnotherUsersCredential(t *testing.T) {
	cfg, config := googleTestSetup(t, "owner-user")
	node := models.WorkflowNode{
		ID: "g3", Type: models.NodeTypeGoogle, Template: "gmail_list", Config: config,
	}
	// Same node, but the RUN belongs to a different user than the
	// credential's owner.
	cfg.UserID = "attacker-user"
	rc := engine.NewRunContext("r1", nil)
	_, err := nodes.ExecuteGoogle(context.Background(), node, rc, cfg)
	if err == nil || !strings.Contains(err.Error(), "does not belong to this user") {
		t.Fatalf("want ownership-denied error, got %v", err)
	}
}

func TestGmailList_ReturnsDecodedMessages(t *testing.T) {
	var gotAuth, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotQuery = r.URL.RawQuery
		w.Write([]byte(`{"messages":[{"id":"m1"},{"id":"m2"}]}`))
	}))
	defer srv.Close()
	nodes.SetGoogleAPIBasesForTest(srv.URL, "", "x", "x")
	defer nodes.SetGoogleAPIBasesForTest("", "", "", "")

	cfg, config := googleTestSetup(t, "u1")
	config["gmailQuery"] = "is:unread"
	config["gmailMaxResults"] = "5"
	node := models.WorkflowNode{ID: "g4", Type: models.NodeTypeGoogle, Template: "gmail_list", Config: config}
	rc := engine.NewRunContext("r1", nil)

	result, err := nodes.ExecuteGoogle(context.Background(), node, rc, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer live-access-token" {
		t.Errorf("want bearer token, got %q", gotAuth)
	}
	if !strings.Contains(gotQuery, "is%3Aunread") && !strings.Contains(gotQuery, "is:unread") {
		t.Errorf("want query param passed through, got %q", gotQuery)
	}
	m, ok := result.(map[string]any)
	if !ok || m["messages"] == nil {
		t.Errorf("want decoded messages list, got %v", result)
	}
}

// A real (trimmed) multipart/alternative Gmail message: text/plain sibling
// next to text/html, which is the common real-world shape this has to
// handle correctly, not just a flat single-part message.
func gmailFixtureMessage(t *testing.T, plainText string) string {
	t.Helper()
	encoded := base64.RawURLEncoding.EncodeToString([]byte(plainText))
	msg := map[string]any{
		"id": "msg123", "threadId": "thread123", "snippet": "preview...",
		"payload": map[string]any{
			"mimeType": "multipart/alternative",
			"headers": []map[string]any{
				{"name": "Subject", "value": "Test Subject"},
				{"name": "From", "value": "sender@example.com"},
				{"name": "To", "value": "me@example.com"},
				{"name": "Date", "value": "Thu, 6 Aug 2026 12:00:00 +0000"},
			},
			"parts": []map[string]any{
				{"mimeType": "text/plain", "body": map[string]any{"data": encoded}},
				{"mimeType": "text/html", "body": map[string]any{"data": base64.RawURLEncoding.EncodeToString([]byte("<p>" + plainText + "</p>"))}},
			},
		},
	}
	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestGmailGet_ExtractsPlainTextBodyAndHeadersFromMultipartMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(gmailFixtureMessage(t, "Hello, this is the real message body.")))
	}))
	defer srv.Close()
	nodes.SetGoogleAPIBasesForTest(srv.URL, "", "x", "x")
	defer nodes.SetGoogleAPIBasesForTest("", "", "", "")

	cfg, config := googleTestSetup(t, "u1")
	config["gmailMessageID"] = "msg123"
	node := models.WorkflowNode{ID: "g5", Type: models.NodeTypeGoogle, Template: "gmail_get", Config: config}
	rc := engine.NewRunContext("r1", nil)

	result, err := nodes.ExecuteGoogle(context.Background(), node, rc, cfg)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("want a decoded map, got %T", result)
	}
	if m["subject"] != "Test Subject" || m["from"] != "sender@example.com" {
		t.Errorf("want headers extracted, got subject=%v from=%v", m["subject"], m["from"])
	}
	if m["body"] != "Hello, this is the real message body." {
		t.Errorf("want plain-text body decoded from base64url, got %v", m["body"])
	}
}

func TestGmailGet_SkipsWhenNoMessageID(t *testing.T) {
	cfg, config := googleTestSetup(t, "u1")
	node := models.WorkflowNode{ID: "g6", Type: models.NodeTypeGoogle, Template: "gmail_get", Config: config}
	rc := engine.NewRunContext("r1", nil)
	result, err := nodes.ExecuteGoogle(context.Background(), node, rc, cfg)
	if err != nodes.ErrActionSkipped {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "gmail_skipped_no_message_id" {
		t.Errorf("want skip sentinel, got %v", result)
	}
}

func TestGmailSend_BuildsCorrectRawRFC2822MessageAndSkipsWithoutRecipient(t *testing.T) {
	var gotRaw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		gotRaw, _ = body["raw"].(string)
		w.Write([]byte(`{"id":"sent1"}`))
	}))
	defer srv.Close()
	nodes.SetGoogleAPIBasesForTest(srv.URL, "", "x", "x")
	defer nodes.SetGoogleAPIBasesForTest("", "", "", "")

	cfg, config := googleTestSetup(t, "u1")
	config["gmailTo"] = "recipient@example.com"
	config["gmailSubject"] = "Hello"
	node := models.WorkflowNode{ID: "g7", Type: models.NodeTypeGoogle, Template: "gmail_send", Config: config}
	rc := engine.NewRunContext("r1", []byte(`"the message body"`))

	result, err := nodes.ExecuteGoogle(context.Background(), node, rc, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if result != "gmail_sent" {
		t.Errorf("want gmail_sent sentinel, got %v", result)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(gotRaw)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(decoded)
	if !strings.Contains(raw, "To: recipient@example.com") ||
		!strings.Contains(raw, "Subject: Hello") ||
		!strings.Contains(raw, "the message body") {
		t.Errorf("want a well-formed RFC2822 message, got %q", raw)
	}

	// No recipient -> clean skip, no request sent.
	config2 := map[string]string{"oauthCredentialID": "cred1"}
	node2 := models.WorkflowNode{ID: "g7b", Type: models.NodeTypeGoogle, Template: "gmail_send", Config: config2}
	result2, err2 := nodes.ExecuteGoogle(context.Background(), node2, rc, cfg)
	if err2 != nodes.ErrActionSkipped || result2 != "gmail_skipped_no_recipient" {
		t.Errorf("want skip sentinel for missing recipient, got %v / %v", result2, err2)
	}
}

// TestGmailSend_ResolvesTemplateRefsInToSubjectAndThreadID guards against the
// same class of bug found and fixed for Twilio/Pipedrive/Shopify/Zendesk
// elsewhere in this PR: gmailTo/gmailSubject/gmailThreadID were read via
// plain configVal with no resolveTemplate call, so a workflow following the
// Inspector's own hint text for gmailThreadID ("e.g. {{ result.threadId }}")
// would have gotten the literal placeholder string sent to Gmail instead of
// the referenced value.
func TestGmailSend_ResolvesTemplateRefsInToSubjectAndThreadID(t *testing.T) {
	var gotRaw string
	var gotThreadID any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		gotRaw, _ = body["raw"].(string)
		gotThreadID = body["threadId"]
		w.Write([]byte(`{"id":"sent3"}`))
	}))
	defer srv.Close()
	nodes.SetGoogleAPIBasesForTest(srv.URL, "", "x", "x")
	defer nodes.SetGoogleAPIBasesForTest("", "", "", "")

	cfg, config := googleTestSetup(t, "u1")
	config["gmailTo"] = "{{ node.lookup.email }}"
	config["gmailSubject"] = "{{ node.lookup.subject }}"
	config["gmailThreadID"] = "{{ node.trigger.threadId }}"
	node := models.WorkflowNode{ID: "g9", Type: models.NodeTypeGoogle, Template: "gmail_reply", Config: config}
	rc := engine.NewRunContext("r1", nil)
	rc.Set("trigger", map[string]any{"threadId": "resolved-thread-99"})
	rc.Set("lookup", map[string]any{"email": "resolved@example.com", "subject": "Resolved Subject"})

	if _, err := nodes.ExecuteGoogle(context.Background(), node, rc, cfg); err != nil {
		t.Fatal(err)
	}
	decoded, _ := base64.RawURLEncoding.DecodeString(gotRaw)
	raw := string(decoded)
	if !strings.Contains(raw, "To: resolved@example.com") {
		t.Errorf("want gmailTo resolved, got %q", raw)
	}
	if !strings.Contains(raw, "Subject: Re: Resolved Subject") {
		t.Errorf("want gmailSubject resolved (with Re: prefix), got %q", raw)
	}
	if gotThreadID != "resolved-thread-99" {
		t.Errorf("want gmailThreadID resolved per the Inspector's own documented usage, got %v", gotThreadID)
	}
}

// TestGmailSend_StripsCRLFFromToAndSubject guards the header-injection
// surface that resolving gmailTo/gmailSubject through resolveTemplate opens
// up: an upstream node's output can now land in a raw email header line, so
// an embedded \r or \n must not be allowed to start a new (attacker- or
// upstream-controlled) header.
func TestGmailSend_StripsCRLFFromToAndSubject(t *testing.T) {
	var gotRaw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		gotRaw, _ = body["raw"].(string)
		w.Write([]byte(`{"id":"sent4"}`))
	}))
	defer srv.Close()
	nodes.SetGoogleAPIBasesForTest(srv.URL, "", "x", "x")
	defer nodes.SetGoogleAPIBasesForTest("", "", "", "")

	cfg, config := googleTestSetup(t, "u1")
	config["gmailTo"] = "{{ node.lookup.email }}"
	node := models.WorkflowNode{ID: "g10", Type: models.NodeTypeGoogle, Template: "gmail_send", Config: config}
	rc := engine.NewRunContext("r1", []byte(`"body"`))
	rc.Set("lookup", map[string]any{"email": "victim@example.com\r\nBcc: attacker@evil.com"})

	if _, err := nodes.ExecuteGoogle(context.Background(), node, rc, cfg); err != nil {
		t.Fatal(err)
	}
	decoded, _ := base64.RawURLEncoding.DecodeString(gotRaw)
	raw := string(decoded)
	// The security property is that "Bcc:" never starts its own header line
	// (i.e. is never preceded by \r\n) -- not that the literal text "Bcc:"
	// can't appear at all. With CR/LF stripped, the injected text collapses
	// into harmless trailing garbage on the To: line instead of a real header.
	if strings.Contains(raw, "\r\nBcc:") || strings.Contains(raw, "\nBcc:") {
		t.Fatalf("want embedded CRLF stripped so \"Bcc:\" can't start a new header line, got %q", raw)
	}
	if !strings.Contains(raw, "To: victim@example.com") {
		t.Errorf("want the recipient address preserved minus the injected CRLF, got %q", raw)
	}
}

func TestGmailReply_PrefixesSubjectAndSetsThreadID(t *testing.T) {
	var gotRaw string
	var gotThreadID any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		gotRaw, _ = body["raw"].(string)
		gotThreadID = body["threadId"]
		w.Write([]byte(`{"id":"sent2"}`))
	}))
	defer srv.Close()
	nodes.SetGoogleAPIBasesForTest(srv.URL, "", "x", "x")
	defer nodes.SetGoogleAPIBasesForTest("", "", "", "")

	cfg, config := googleTestSetup(t, "u1")
	config["gmailTo"] = "sender@example.com"
	config["gmailSubject"] = "Original Subject"
	config["gmailThreadID"] = "thread123"
	node := models.WorkflowNode{ID: "g8", Type: models.NodeTypeGoogle, Template: "gmail_reply", Config: config}
	rc := engine.NewRunContext("r1", []byte(`"my reply"`))

	if _, err := nodes.ExecuteGoogle(context.Background(), node, rc, cfg); err != nil {
		t.Fatal(err)
	}
	decoded, _ := base64.RawURLEncoding.DecodeString(gotRaw)
	if !strings.Contains(string(decoded), "Subject: Re: Original Subject") {
		t.Errorf("want Re: prefix added, got %q", string(decoded))
	}
	if gotThreadID != "thread123" {
		t.Errorf("want threadId carried through for real threading, got %v", gotThreadID)
	}
}

func TestSheetsRead_ReturnsDecodedValues(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(`{"values":[["a","b"],["c","d"]]}`))
	}))
	defer srv.Close()
	nodes.SetGoogleAPIBasesForTest("x", srv.URL, "x", "x")
	defer nodes.SetGoogleAPIBasesForTest("", "", "", "")

	cfg, config := googleTestSetup(t, "u1")
	config["sheetsSpreadsheetID"] = "sheet123"
	config["sheetsRange"] = "Sheet1!A1:B2"
	node := models.WorkflowNode{ID: "g9", Type: models.NodeTypeGoogle, Template: "sheets_read", Config: config}
	rc := engine.NewRunContext("r1", nil)

	result, err := nodes.ExecuteGoogle(context.Background(), node, rc, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotPath, "sheet123") {
		t.Errorf("want spreadsheet id in path, got %q", gotPath)
	}
	m, ok := result.(map[string]any)
	if !ok || m["values"] == nil {
		t.Errorf("want decoded values, got %v", result)
	}
}

// Regression test for the QueryEscape-in-a-path-segment bug: rangeA1 sits
// in the URL path (/values/{range}), and QueryEscape encodes a space as
// '+' -- which only means "space" inside a query string, so a range/sheet
// name containing a space (e.g. a sheet literally named "My Sheet") was
// sent as a literal '+' character in the path and silently targeted a
// nonexistent range. PathEscape encodes it as %20 instead, which decodes
// back to a real space in the path -- this asserts both the raw wire form
// (no literal '+' in the request line) and that it round-trips correctly.
func TestSheetsRead_EncodesSpaceInRangeAsPathSegmentNotQueryString(t *testing.T) {
	var gotRequestURI, gotDecodedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRequestURI = r.RequestURI
		gotDecodedPath = r.URL.Path
		w.Write([]byte(`{"values":[]}`))
	}))
	defer srv.Close()
	nodes.SetGoogleAPIBasesForTest("x", srv.URL, "x", "x")
	defer nodes.SetGoogleAPIBasesForTest("", "", "", "")

	cfg, config := googleTestSetup(t, "u1")
	config["sheetsSpreadsheetID"] = "sheet123"
	config["sheetsRange"] = "My Sheet!A1:B2"
	node := models.WorkflowNode{ID: "g9b", Type: models.NodeTypeGoogle, Template: "sheets_read", Config: config}
	rc := engine.NewRunContext("r1", nil)

	if _, err := nodes.ExecuteGoogle(context.Background(), node, rc, cfg); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(gotRequestURI, "+") {
		t.Errorf("want no literal '+' on the wire (query-string escaping in a path segment), got %q", gotRequestURI)
	}
	if !strings.Contains(gotDecodedPath, "My Sheet!A1:B2") {
		t.Errorf("want the range to decode back to its real form with a space, got %q", gotDecodedPath)
	}
}

func TestSheetsAppend_SingleCellRowForPlainTextMessage(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	nodes.SetGoogleAPIBasesForTest("x", srv.URL, "x", "x")
	defer nodes.SetGoogleAPIBasesForTest("", "", "", "")

	cfg, config := googleTestSetup(t, "u1")
	config["sheetsSpreadsheetID"] = "sheet123"
	node := models.WorkflowNode{ID: "g10", Type: models.NodeTypeGoogle, Template: "sheets_append", Config: config}
	rc := engine.NewRunContext("r1", []byte(`"plain text row"`))

	result, err := nodes.ExecuteGoogle(context.Background(), node, rc, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if result != "sheets_row_appended" {
		t.Errorf("want sentinel, got %v", result)
	}
	values, ok := gotBody["values"].([]any)
	if !ok || len(values) != 1 {
		t.Fatalf("want one row, got %v", gotBody["values"])
	}
	row, ok := values[0].([]any)
	if !ok || len(row) != 1 || row[0] != "plain text row" {
		t.Errorf("want single-cell row with the whole message, got %v", row)
	}
}

func TestSheetsAppend_ArrayRowWhenMessageIsJSONArray(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	nodes.SetGoogleAPIBasesForTest("x", srv.URL, "x", "x")
	defer nodes.SetGoogleAPIBasesForTest("", "", "", "")

	cfg, config := googleTestSetup(t, "u1")
	config["sheetsSpreadsheetID"] = "sheet123"
	node := models.WorkflowNode{ID: "g11", Type: models.NodeTypeGoogle, Template: "sheets_append", Config: config}
	rc := engine.NewRunContext("r1", nil)
	rc.Set("prev", []any{"cell1", "cell2", "cell3"})

	if _, err := nodes.ExecuteGoogle(context.Background(), node, rc, cfg); err != nil {
		t.Fatal(err)
	}
	values, ok := gotBody["values"].([]any)
	if !ok || len(values) != 1 {
		t.Fatalf("want one row, got %v", gotBody["values"])
	}
	row, ok := values[0].([]any)
	if !ok || len(row) != 3 {
		t.Errorf("want a 3-cell row from the JSON array message, got %v", row)
	}
}

func TestSheetsAppend_SkipsWhenNoSpreadsheetID(t *testing.T) {
	cfg, config := googleTestSetup(t, "u1")
	node := models.WorkflowNode{ID: "g12", Type: models.NodeTypeGoogle, Template: "sheets_append", Config: config}
	rc := engine.NewRunContext("r1", nil)
	result, err := nodes.ExecuteGoogle(context.Background(), node, rc, cfg)
	if err != nodes.ErrActionSkipped || result != "sheets_skipped_no_spreadsheet_id" {
		t.Errorf("want skip sentinel, got %v / %v", result, err)
	}
}

func TestCalendarList_ReturnsDecodedEvents(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(`{"items":[{"summary":"Meeting"}]}`))
	}))
	defer srv.Close()
	nodes.SetGoogleAPIBasesForTest("x", "x", srv.URL, "x")
	defer nodes.SetGoogleAPIBasesForTest("", "", "", "")

	cfg, config := googleTestSetup(t, "u1")
	node := models.WorkflowNode{ID: "g13", Type: models.NodeTypeGoogle, Template: "calendar_list", Config: config}
	rc := engine.NewRunContext("r1", nil)

	result, err := nodes.ExecuteGoogle(context.Background(), node, rc, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotPath, "primary") {
		t.Errorf("want default 'primary' calendar id in path, got %q", gotPath)
	}
	m, ok := result.(map[string]any)
	if !ok || m["items"] == nil {
		t.Errorf("want decoded events, got %v", result)
	}
}

func TestCalendarCreate_SendsSummaryAndTimes(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`{"id":"evt1"}`))
	}))
	defer srv.Close()
	nodes.SetGoogleAPIBasesForTest("x", "x", srv.URL, "x")
	defer nodes.SetGoogleAPIBasesForTest("", "", "", "")

	cfg, config := googleTestSetup(t, "u1")
	config["calendarSummary"] = "Team Sync"
	config["calendarStart"] = "2026-08-10T10:00:00Z"
	config["calendarEnd"] = "2026-08-10T11:00:00Z"
	node := models.WorkflowNode{ID: "g14", Type: models.NodeTypeGoogle, Template: "calendar_create", Config: config}
	rc := engine.NewRunContext("r1", nil)

	result, err := nodes.ExecuteGoogle(context.Background(), node, rc, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if result != "calendar_event_created" {
		t.Errorf("want sentinel, got %v", result)
	}
	if gotBody["summary"] != "Team Sync" {
		t.Errorf("want summary passed through, got %v", gotBody["summary"])
	}
	start, ok := gotBody["start"].(map[string]any)
	if !ok || start["dateTime"] != "2026-08-10T10:00:00Z" {
		t.Errorf("want start dateTime, got %v", gotBody["start"])
	}
}

func TestCalendarCreate_SkipsWhenMissingTimes(t *testing.T) {
	cfg, config := googleTestSetup(t, "u1")
	config["calendarSummary"] = "No times set"
	node := models.WorkflowNode{ID: "g15", Type: models.NodeTypeGoogle, Template: "calendar_create", Config: config}
	rc := engine.NewRunContext("r1", nil)
	result, err := nodes.ExecuteGoogle(context.Background(), node, rc, cfg)
	if err != nodes.ErrActionSkipped || result != "calendar_skipped_missing_times" {
		t.Errorf("want skip sentinel, got %v / %v", result, err)
	}
}

func TestDriveList_ReturnsDecodedFiles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"files":[{"id":"f1","name":"report.pdf"}]}`))
	}))
	defer srv.Close()
	nodes.SetGoogleAPIBasesForTest("x", "x", "x", srv.URL)
	defer nodes.SetGoogleAPIBasesForTest("", "", "", "")

	cfg, config := googleTestSetup(t, "u1")
	node := models.WorkflowNode{ID: "g16", Type: models.NodeTypeGoogle, Template: "drive_list", Config: config}
	rc := engine.NewRunContext("r1", nil)

	result, err := nodes.ExecuteGoogle(context.Background(), node, rc, cfg)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := result.(map[string]any)
	if !ok || m["files"] == nil {
		t.Errorf("want decoded files, got %v", result)
	}
}

func TestDriveGet_SkipsWhenNoFileID(t *testing.T) {
	cfg, config := googleTestSetup(t, "u1")
	node := models.WorkflowNode{ID: "g17", Type: models.NodeTypeGoogle, Template: "drive_get", Config: config}
	rc := engine.NewRunContext("r1", nil)
	result, err := nodes.ExecuteGoogle(context.Background(), node, rc, cfg)
	if err != nodes.ErrActionSkipped || result != "drive_skipped_no_file_id" {
		t.Errorf("want skip sentinel, got %v / %v", result, err)
	}
}

func TestDriveDownload_ReturnsBase64Content(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("alt") != "media" {
			t.Errorf("want alt=media query param, got %q", r.URL.RawQuery)
		}
		w.Write([]byte("raw file bytes"))
	}))
	defer srv.Close()
	nodes.SetGoogleAPIBasesForTest("x", "x", "x", srv.URL)
	defer nodes.SetGoogleAPIBasesForTest("", "", "", "")

	cfg, config := googleTestSetup(t, "u1")
	config["driveFileID"] = "file123"
	node := models.WorkflowNode{ID: "g18", Type: models.NodeTypeGoogle, Template: "drive_download", Config: config}
	rc := engine.NewRunContext("r1", nil)

	result, err := nodes.ExecuteGoogle(context.Background(), node, rc, cfg)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("want a map result, got %T", result)
	}
	decoded, err := base64.StdEncoding.DecodeString(m["contentBase64"].(string))
	if err != nil || string(decoded) != "raw file bytes" {
		t.Errorf("want base64-encoded file content round-tripping, got %v (err %v)", m["contentBase64"], err)
	}
}
