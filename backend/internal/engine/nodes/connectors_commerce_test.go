package nodes_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/agentmesh/backend/internal/engine"
	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
)

func TestStripeAction_CreatesCustomer(t *testing.T) {
	var gotAuth, gotCT, gotPath string
	var gotForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(body))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"cus_123"}`))
	}))
	defer srv.Close()
	nodes.SetStripeAPIBaseForTest(srv.URL)
	defer nodes.SetStripeAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "st1", Type: models.NodeTypeAction, Template: "stripe",
		Secrets: map[string]string{"stripeAPIKey": "sk_test_xxx"},
		Config:  map[string]string{"stripeEmail": "buyer@example.com"},
	}
	rc := engine.NewRunContext("r1", []byte(`"new signup from the workflow"`))

	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "stripe_customer_created" {
		t.Errorf("want 'stripe_customer_created', got %v", result)
	}
	if gotAuth != "Bearer sk_test_xxx" {
		t.Errorf("want bearer auth, got %q", gotAuth)
	}
	if gotCT != "application/x-www-form-urlencoded" {
		t.Errorf("Stripe requires form encoding, got %q", gotCT)
	}
	if gotPath != "/v1/customers" {
		t.Errorf("want /v1/customers, got %q", gotPath)
	}
	if gotForm.Get("email") != "buyer@example.com" {
		t.Errorf("email: got %q", gotForm.Get("email"))
	}
	if gotForm.Get("description") != "new signup from the workflow" {
		t.Errorf("description should be the run output, got %q", gotForm.Get("description"))
	}
}

// A node saved under the old stripeSecretKey field name (before it was
// renamed to stripeAPIKey) must still resolve its key, or every
// pre-existing Stripe node silently breaks on deploy.
func TestStripeAction_APIKeyFallsBackToLegacySecretName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"cus_123"}`))
	}))
	defer srv.Close()
	nodes.SetStripeAPIBaseForTest(srv.URL)
	defer nodes.SetStripeAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "st2", Type: models.NodeTypeAction, Template: "stripe",
		Secrets: map[string]string{"stripeSecretKey": "sk_test_legacy"},
		Config:  map[string]string{"stripeEmail": "buyer@example.com"},
	}
	rc := engine.NewRunContext("r1", nil)

	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "stripe_customer_created" {
		t.Errorf("want 'stripe_customer_created', got %v", result)
	}
}

// When stripeEmail isn't configured, Stripe falls back to the upstream
// node's own message as the customer email -- the documented default this
// PR previously dropped.
func TestStripeAction_EmailFallsBackToMessage(t *testing.T) {
	var gotForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(body))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"cus_123"}`))
	}))
	defer srv.Close()
	nodes.SetStripeAPIBaseForTest(srv.URL)
	defer nodes.SetStripeAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "st3", Type: models.NodeTypeAction, Template: "stripe",
		Secrets: map[string]string{"stripeAPIKey": "sk_test_xxx"},
	}
	rc := engine.NewRunContext("r1", []byte(`"buyer@example.com"`))

	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "stripe_customer_created" {
		t.Errorf("want 'stripe_customer_created', got %v", result)
	}
	if gotForm.Get("email") != "buyer@example.com" {
		t.Errorf("email should fall back to the upstream message, got %q", gotForm.Get("email"))
	}
}

func TestStripeAction_SkipsWithoutAPIKey(t *testing.T) {
	node := models.WorkflowNode{ID: "st1", Type: models.NodeTypeAction, Template: "stripe"}
	rc := engine.NewRunContext("r1", nil)
	got, _ := nodes.ExecuteAction(context.Background(), node, rc)
	if got != "stripe_skipped_no_api_key" {
		t.Errorf("want skip sentinel, got %v", got)
	}
}

func TestStripeAction_SkipsWithoutEmail(t *testing.T) {
	node := models.WorkflowNode{
		ID: "st1", Type: models.NodeTypeAction, Template: "stripe",
		Secrets: map[string]string{"stripeAPIKey": "sk_test_xxx"},
	}
	rc := engine.NewRunContext("r1", nil)
	got, _ := nodes.ExecuteAction(context.Background(), node, rc)
	if got != "stripe_skipped_no_email" {
		t.Errorf("want skip sentinel, got %v", got)
	}
}

func TestShopifyAction_CreatesCustomerNote(t *testing.T) {
	var gotToken, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Shopify-Access-Token")
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	nodes.SetShopifyAPIBaseForTest(srv.URL)
	defer nodes.SetShopifyAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "sh1", Type: models.NodeTypeAction, Template: "shopify_customer",
		Secrets: map[string]string{"shopifyAccessToken": "shpat_xxx"},
		Config:  map[string]string{"shopifyStore": "acme-store", "shopifyEmail": "buyer@example.com"},
	}
	rc := engine.NewRunContext("r1", []byte(`"VIP customer from the workflow"`))

	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "shopify_customer_created" {
		t.Errorf("want 'shopify_customer_created', got %v", result)
	}
	if gotToken != "shpat_xxx" {
		t.Errorf("want the Shopify access-token header, got %q", gotToken)
	}
	if gotPath != "/admin/api/2024-10/customers.json" {
		t.Errorf("want the versioned admin path, got %q", gotPath)
	}
	cust := gotBody["customer"].(map[string]any)
	if cust["email"] != "buyer@example.com" {
		t.Errorf("email: got %v", cust["email"])
	}
	if cust["note"] != "VIP customer from the workflow" {
		t.Errorf("note: got %v", cust["note"])
	}
}

func TestShopifyAction_SkipsWhenUnconfigured(t *testing.T) {
	rc := engine.NewRunContext("r1", nil)
	noToken := models.WorkflowNode{Type: models.NodeTypeAction, Template: "shopify_customer",
		Config: map[string]string{"shopifyStore": "s", "shopifyEmail": "a@b.com"}}
	if got, _ := nodes.ExecuteAction(context.Background(), noToken, rc); got != "shopify_skipped_no_access_token" {
		t.Errorf("want skip sentinel, got %v", got)
	}
	noStore := models.WorkflowNode{Type: models.NodeTypeAction, Template: "shopify_customer",
		Secrets: map[string]string{"shopifyAccessToken": "t"},
		Config:  map[string]string{"shopifyEmail": "a@b.com"}}
	if got, _ := nodes.ExecuteAction(context.Background(), noStore, rc); got != "shopify_skipped_missing_config" {
		t.Errorf("want skip sentinel, got %v", got)
	}
}

func TestPipedriveAction_CreatesNote(t *testing.T) {
	var gotQueryToken string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQueryToken = r.URL.Query().Get("api_token")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	nodes.SetPipedriveAPIBaseForTest(srv.URL)
	defer nodes.SetPipedriveAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "pi1", Type: models.NodeTypeAction, Template: "pipedrive",
		Secrets: map[string]string{"pipedriveAPIToken": "pdtok"},
		Config:  map[string]string{"pipedriveCompanyDomain": "acme", "pipedriveDealID": "77"},
	}
	rc := engine.NewRunContext("r1", []byte(`"call scheduled for Tuesday"`))

	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "pipedrive_note_created" {
		t.Errorf("want 'pipedrive_note_created', got %v", result)
	}
	if gotQueryToken != "pdtok" {
		t.Errorf("Pipedrive takes the token as a query param; got %q", gotQueryToken)
	}
	if gotBody["content"] != "call scheduled for Tuesday" {
		t.Errorf("content: got %v", gotBody["content"])
	}
	// Pipedrive's Notes API documents deal_id as an integer -- decoded back
	// through encoding/json, a real JSON number lands as float64(77), not
	// the string "77" a naive resolveTemplate pass-through would have sent.
	if gotBody["deal_id"] != float64(77) {
		t.Errorf("deal_id: want real JSON number 77, got %v (%T)", gotBody["deal_id"], gotBody["deal_id"])
	}
}

// dealID/personID must resolve {{ }} references — a deal ID commonly comes
// from an upstream CRM lookup node rather than being hardcoded.
func TestPipedriveAction_ResolvesTemplatesInDealAndPersonID(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	nodes.SetPipedriveAPIBaseForTest(srv.URL)
	defer nodes.SetPipedriveAPIBaseForTest("")

	rc := engine.NewRunContext("r1", nil)
	rc.Set("lookup", map[string]any{"dealId": "77", "personId": "42"})

	node := models.WorkflowNode{
		ID: "pi1", Type: models.NodeTypeAction, Template: "pipedrive",
		Secrets: map[string]string{"pipedriveAPIToken": "pdtok"},
		Config: map[string]string{
			"pipedriveCompanyDomain": "acme",
			"pipedriveDealID":        "{{ node.lookup.dealId }}",
			"pipedrivePersonID":      "{{ node.lookup.personId }}",
		},
	}
	if _, err := nodes.ExecuteAction(context.Background(), node, rc); err != nil {
		t.Fatal(err)
	}
	if gotBody["deal_id"] != float64(77) {
		t.Errorf("deal_id should resolve the template as a real number, got %v (%T)", gotBody["deal_id"], gotBody["deal_id"])
	}
	if gotBody["person_id"] != float64(42) {
		t.Errorf("person_id should resolve the template as a real number, got %v (%T)", gotBody["person_id"], gotBody["person_id"])
	}
}

func TestPipedriveAction_SkipsWithoutToken(t *testing.T) {
	node := models.WorkflowNode{Type: models.NodeTypeAction, Template: "pipedrive"}
	rc := engine.NewRunContext("r1", nil)
	got, _ := nodes.ExecuteAction(context.Background(), node, rc)
	if got != "pipedrive_skipped_no_api_token" {
		t.Errorf("want skip sentinel, got %v", got)
	}
}
