package nodes_test

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentmesh/backend/internal/engine"
	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
)

func TestHubSpotAction_CreatesNote(t *testing.T) {
	var gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	nodes.SetHubSpotAPIBaseForTest(srv.URL)
	defer nodes.SetHubSpotAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "hs1", Type: models.NodeTypeAction, Template: "hubspot",
		Secrets: map[string]string{"hubspotAPIKey": "pat-na1-xxx"},
	}
	rc := engine.NewRunContext("r1", []byte(`"follow up with lead"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "hubspot_note_created" {
		t.Errorf("want 'hubspot_note_created', got %v", result)
	}
	if gotAuth != "Bearer pat-na1-xxx" {
		t.Errorf("want bearer auth, got %q", gotAuth)
	}
	props, _ := gotBody["properties"].(map[string]any)
	if props["hs_note_body"] != "follow up with lead" {
		t.Errorf("want note body from message, got %v", gotBody)
	}
}

func TestHubSpotAction_PrefersOAuthTokenOverManualToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	nodes.SetHubSpotAPIBaseForTest(srv.URL)
	defer nodes.SetHubSpotAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "hs4", Type: models.NodeTypeAction, Template: "hubspot",
		Secrets: map[string]string{"hubspotAPIKey": "pat-na1-xxx", "hubspotOAuthAccessToken": "oauth-derived-token"},
	}
	rc := engine.NewRunContext("r1", []byte(`"follow up with lead"`))
	_, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer oauth-derived-token" {
		t.Errorf("want OAuth token in Authorization header, got %q", gotAuth)
	}
}

func TestHubSpotAction_SkipsWhenNoAPIKey(t *testing.T) {
	node := models.WorkflowNode{
		ID: "hs2", Type: models.NodeTypeAction, Template: "hubspot",
	}
	rc := engine.NewRunContext("r1", []byte(`"follow up with lead"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "hubspot_skipped_no_api_key" {
		t.Errorf("want 'hubspot_skipped_no_api_key', got %v", result)
	}
}

func TestMailchimpAction_AddsSubscriber(t *testing.T) {
	var gotPath, gotMethod, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	nodes.SetMailchimpAPIBaseForTest(srv.URL)
	defer nodes.SetMailchimpAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "mc1", Type: models.NodeTypeAction, Template: "mailchimp",
		Secrets: map[string]string{"mailchimpAPIKey": "abc123-us21"},
		Config:  map[string]string{"mailchimpListID": "list42", "mailchimpEmail": "New.User@Example.com"},
	}
	rc := engine.NewRunContext("r1", []byte(`"signup"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "mailchimp_subscriber_added" {
		t.Errorf("want 'mailchimp_subscriber_added', got %v", result)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("want PUT (upsert), got %s", gotMethod)
	}
	wantHash := md5.Sum([]byte("new.user@example.com"))
	wantPath := "/3.0/lists/list42/members/" + hex.EncodeToString(wantHash[:])
	if gotPath != wantPath {
		t.Errorf("want subscriber-hash path %q, got %q", wantPath, gotPath)
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("anystring:abc123-us21"))
	if gotAuth != wantAuth {
		t.Errorf("want basic auth, got %q", gotAuth)
	}
	if gotBody["email_address"] != "New.User@Example.com" {
		t.Errorf("want original-case email in body, got %v", gotBody)
	}
}

func TestMailchimpAction_TrimsWhitespaceFromEmail(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	nodes.SetMailchimpAPIBaseForTest(srv.URL)
	defer nodes.SetMailchimpAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "mc4", Type: models.NodeTypeAction, Template: "mailchimp",
		Secrets: map[string]string{"mailchimpAPIKey": "abc123-us21"},
		Config:  map[string]string{"mailchimpListID": "list42", "mailchimpEmail": "  new.user@example.com  "},
	}
	rc := engine.NewRunContext("r1", []byte(`"signup"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "mailchimp_subscriber_added" {
		t.Errorf("want 'mailchimp_subscriber_added', got %v", result)
	}
	wantHash := md5.Sum([]byte("new.user@example.com"))
	wantPath := "/3.0/lists/list42/members/" + hex.EncodeToString(wantHash[:])
	if gotPath != wantPath {
		t.Errorf("want trimmed-email subscriber-hash path %q, got %q", wantPath, gotPath)
	}
	if gotBody["email_address"] != "new.user@example.com" {
		t.Errorf("want trimmed email in body, got %v", gotBody)
	}
}

func TestMailchimpDatacenter(t *testing.T) {
	dc, err := nodes.MailchimpDatacenterForTest("abc123-us21")
	if err != nil || dc != "us21" {
		t.Errorf("want 'us21', got %q err=%v", dc, err)
	}
	if _, err := nodes.MailchimpDatacenterForTest("nodashesheresuffix"); err == nil {
		t.Error("want error for malformed key")
	}
}

func TestMailchimpDatacenter_RejectsHostInjection(t *testing.T) {
	if _, err := nodes.MailchimpDatacenterForTest("abc123-evil.com/x"); err == nil {
		t.Error("want error for datacenter suffix containing host-breaking characters")
	}
}

func TestMailchimpAction_SkipsWhenNoAPIKey(t *testing.T) {
	node := models.WorkflowNode{
		ID: "mc2", Type: models.NodeTypeAction, Template: "mailchimp",
		Config: map[string]string{"mailchimpListID": "list42", "mailchimpEmail": "user@example.com"},
	}
	rc := engine.NewRunContext("r1", []byte(`"test"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "mailchimp_skipped_no_api_key" {
		t.Errorf("want 'mailchimp_skipped_no_api_key', got %v", result)
	}
}

func TestMailchimpAction_SkipsWhenNoListID(t *testing.T) {
	node := models.WorkflowNode{
		ID: "mc3", Type: models.NodeTypeAction, Template: "mailchimp",
		Secrets: map[string]string{"mailchimpAPIKey": "abc123-us21"},
		Config:  map[string]string{"mailchimpEmail": "user@example.com"},
	}
	rc := engine.NewRunContext("r1", []byte(`"test"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "mailchimp_skipped_no_list_id" {
		t.Errorf("want 'mailchimp_skipped_no_list_id', got %v", result)
	}
}

func TestMailchimpAction_SkipsWhenNoEmail(t *testing.T) {
	node := models.WorkflowNode{
		ID: "mc4", Type: models.NodeTypeAction, Template: "mailchimp",
		Secrets: map[string]string{"mailchimpAPIKey": "abc123-us21"},
		Config:  map[string]string{"mailchimpListID": "list42"},
	}
	rc := engine.NewRunContext("r1", []byte(`""`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "mailchimp_skipped_no_email" {
		t.Errorf("want 'mailchimp_skipped_no_email', got %v", result)
	}
}

func TestMailchimpAction_OAuthTokenUsesStoredDCAndBearerAuth(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	nodes.SetMailchimpAPIBaseForTest(srv.URL)
	defer nodes.SetMailchimpAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "mc5", Type: models.NodeTypeAction, Template: "mailchimp",
		Secrets: map[string]string{"mailchimpOAuthAccessToken": "oauth-derived-token", "mailchimpAPIKey": "manual-key-should-be-ignored"},
		Config: map[string]string{
			"mailchimpListID": "list42", "mailchimpEmail": "new.user@example.com", "mailchimpOAuthDC": "us21",
		},
	}
	rc := engine.NewRunContext("r1", []byte(`"signup"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "mailchimp_subscriber_added" {
		t.Errorf("want 'mailchimp_subscriber_added', got %v", result)
	}
	wantHash := md5.Sum([]byte("new.user@example.com"))
	wantPath := "/3.0/lists/list42/members/" + hex.EncodeToString(wantHash[:])
	if gotPath != wantPath {
		t.Errorf("want subscriber-hash path %q, got %q", wantPath, gotPath)
	}
	if gotAuth != "Bearer oauth-derived-token" {
		t.Errorf("want bearer auth with OAuth token, got %q", gotAuth)
	}
	if gotBody["email_address"] != "new.user@example.com" {
		t.Errorf("want email in body, got %v", gotBody)
	}
}

func TestMailchimpAction_OAuthTokenSkipsWhenNoDC(t *testing.T) {
	requestReceived := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestReceived = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	nodes.SetMailchimpAPIBaseForTest(srv.URL)
	defer nodes.SetMailchimpAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "mc6", Type: models.NodeTypeAction, Template: "mailchimp",
		Secrets: map[string]string{"mailchimpOAuthAccessToken": "oauth-derived-token"},
		Config:  map[string]string{"mailchimpListID": "list42", "mailchimpEmail": "new.user@example.com"},
	}
	rc := engine.NewRunContext("r1", []byte(`"signup"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "mailchimp_skipped_missing_config" {
		t.Errorf("want 'mailchimp_skipped_missing_config', got %v", result)
	}
	if requestReceived {
		t.Error("want no HTTP request when dc is missing")
	}
}

func TestMailchimpAction_ManualAPIKeyPathUnchangedWhenNoOAuthToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	nodes.SetMailchimpAPIBaseForTest(srv.URL)
	defer nodes.SetMailchimpAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "mc7", Type: models.NodeTypeAction, Template: "mailchimp",
		Secrets: map[string]string{"mailchimpAPIKey": "abc123-us21"},
		Config:  map[string]string{"mailchimpListID": "list42", "mailchimpEmail": "new.user@example.com"},
	}
	rc := engine.NewRunContext("r1", []byte(`"signup"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "mailchimp_subscriber_added" {
		t.Errorf("want 'mailchimp_subscriber_added', got %v", result)
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("anystring:abc123-us21"))
	if gotAuth != wantAuth {
		t.Errorf("want basic auth from manual key path, got %q", gotAuth)
	}
}

func TestSupabaseAction_InsertsRow(t *testing.T) {
	var gotPath, gotAPIKey, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAPIKey = r.Header.Get("apikey")
		gotAuth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	node := models.WorkflowNode{
		ID: "sb1", Type: models.NodeTypeAction, Template: "supabase",
		Secrets: map[string]string{"supabaseAPIKey": "svc_key_xxx"},
		Config:  map[string]string{"supabaseProjectURL": srv.URL, "supabaseTable": "logs"},
	}
	rc := engine.NewRunContext("r1", []byte(`"agent finished run"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "supabase_row_inserted" {
		t.Errorf("want 'supabase_row_inserted', got %v", result)
	}
	if gotPath != "/rest/v1/logs" {
		t.Errorf("want rest/v1/table path, got %q", gotPath)
	}
	if gotAPIKey != "svc_key_xxx" || gotAuth != "Bearer svc_key_xxx" {
		t.Errorf("want apikey and bearer auth headers, got apikey=%q auth=%q", gotAPIKey, gotAuth)
	}
	if gotBody["content"] != "agent finished run" {
		t.Errorf("want default column 'content' with message, got %v", gotBody)
	}
}

func TestSupabaseAction_SkipsWhenNoAPIKey(t *testing.T) {
	node := models.WorkflowNode{
		ID: "sb2", Type: models.NodeTypeAction, Template: "supabase",
		Config: map[string]string{"supabaseProjectURL": "https://x.supabase.co", "supabaseTable": "logs"},
	}
	rc := engine.NewRunContext("r1", []byte(`"test"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "supabase_skipped_no_api_key" {
		t.Errorf("want 'supabase_skipped_no_api_key', got %v", result)
	}
}

func TestSupabaseAction_SkipsWhenMissingConfig(t *testing.T) {
	node := models.WorkflowNode{
		ID: "sb3", Type: models.NodeTypeAction, Template: "supabase",
		Secrets: map[string]string{"supabaseAPIKey": "svc_key_xxx"},
	}
	rc := engine.NewRunContext("r1", []byte(`"test"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "supabase_skipped_missing_config" {
		t.Errorf("want 'supabase_skipped_missing_config', got %v", result)
	}
}

func TestWooCommerceAction_AddsOrderNote(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	node := models.WorkflowNode{
		ID: "wc1", Type: models.NodeTypeAction, Template: "woocommerce",
		Secrets: map[string]string{"woocommerceConsumerKey": "ck_xxx", "woocommerceConsumerSecret": "cs_xxx"},
		Config:  map[string]string{"woocommerceStoreURL": srv.URL, "woocommerceOrderID": "77"},
	}
	rc := engine.NewRunContext("r1", []byte(`"refund processed"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "woocommerce_note_added" {
		t.Errorf("want 'woocommerce_note_added', got %v", result)
	}
	if gotPath != "/wp-json/wc/v3/orders/77/notes" {
		t.Errorf("want order notes path, got %q", gotPath)
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("ck_xxx:cs_xxx"))
	if gotAuth != wantAuth {
		t.Errorf("want basic auth, got %q", gotAuth)
	}
	if gotBody["note"] != "refund processed" {
		t.Errorf("want note from message, got %v", gotBody)
	}
}

func TestWooCommerceAction_SkipsWhenNoCredentials(t *testing.T) {
	node := models.WorkflowNode{
		ID: "wc2", Type: models.NodeTypeAction, Template: "woocommerce",
		Config: map[string]string{"woocommerceStoreURL": "https://x.com", "woocommerceOrderID": "77"},
	}
	rc := engine.NewRunContext("r1", []byte(`"refund processed"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "woocommerce_skipped_no_credentials" {
		t.Errorf("want 'woocommerce_skipped_no_credentials', got %v", result)
	}
}

func TestWooCommerceAction_SkipsWhenMissingConfig(t *testing.T) {
	node := models.WorkflowNode{
		ID: "wc3", Type: models.NodeTypeAction, Template: "woocommerce",
		Secrets: map[string]string{"woocommerceConsumerKey": "ck_xxx", "woocommerceConsumerSecret": "cs_xxx"},
	}
	rc := engine.NewRunContext("r1", []byte(`"refund processed"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "woocommerce_skipped_missing_config" {
		t.Errorf("want 'woocommerce_skipped_missing_config', got %v", result)
	}
}
