package nodes_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentmesh/backend/internal/engine"
	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
)

func TestWebhookAction(t *testing.T) {
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	node := models.WorkflowNode{ID: "a1", Type: models.NodeTypeAction, Template: "webhook", URL: srv.URL}
	rc := engine.NewRunContext("r1", []byte(`{"message":"test payload"}`))
	_, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if received == nil {
		t.Fatal("webhook not called")
	}
}

func TestLogAction(t *testing.T) {
	node := models.WorkflowNode{ID: "a2", Type: models.NodeTypeAction, Template: "log"}
	rc := engine.NewRunContext("r1", []byte(`"hello"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "logged" {
		t.Fatalf("want 'logged' got %v", result)
	}
}

func TestEmailAction_SendGridProvider(t *testing.T) {
	var gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	nodes.SetSendGridAPIBaseForTest(srv.URL)
	defer nodes.SetSendGridAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "e1", Type: models.NodeTypeAction, Template: "email",
		EmailProvider: "sendgrid", EmailAPIKey: "SG.xxx",
		EmailFrom: "AgentMesh <you@yourdomain.com>", EmailTo: "user@example.com", EmailSubject: "Result",
	}
	rc := engine.NewRunContext("r1", []byte(`"done"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "email_sent" {
		t.Errorf("want 'email_sent', got %v", result)
	}
	if gotAuth != "Bearer SG.xxx" {
		t.Errorf("want bearer auth, got %q", gotAuth)
	}
	from, _ := gotBody["from"].(map[string]any)
	if from["email"] != "you@yourdomain.com" || from["name"] != "AgentMesh" {
		t.Errorf("want parsed from name/email, got %v", from)
	}
}

func TestEmailAction_BrevoProvider(t *testing.T) {
	var gotAPIKeyHeader string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKeyHeader = r.Header.Get("api-key")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	nodes.SetBrevoAPIBaseForTest(srv.URL)
	defer nodes.SetBrevoAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "e2", Type: models.NodeTypeAction, Template: "email",
		EmailProvider: "brevo", EmailAPIKey: "xkeysib-xxx",
		EmailFrom: "AgentMesh <you@yourdomain.com>", EmailTo: "user@example.com", EmailSubject: "Result",
	}
	rc := engine.NewRunContext("r1", []byte(`"done"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "email_sent" {
		t.Errorf("want 'email_sent', got %v", result)
	}
	if gotAPIKeyHeader != "xkeysib-xxx" {
		t.Errorf("want api-key header, got %q", gotAPIKeyHeader)
	}
	sender, _ := gotBody["sender"].(map[string]any)
	if sender["email"] != "you@yourdomain.com" {
		t.Errorf("want parsed sender email, got %v", sender)
	}
}

func TestEmailAction_SkipsWhenNonResendProviderHasNoFromAddress(t *testing.T) {
	node := models.WorkflowNode{
		ID: "e3", Type: models.NodeTypeAction, Template: "email",
		EmailProvider: "sendgrid", EmailAPIKey: "SG.xxx",
		EmailTo: "user@example.com", EmailSubject: "Result",
	}
	rc := engine.NewRunContext("r1", []byte(`"done"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "email_skipped_no_from_address" {
		t.Errorf("want 'email_skipped_no_from_address', got %v", result)
	}
}

func TestParseEmailAddress(t *testing.T) {
	name, email := nodes.ParseEmailAddressForTest("AgentMesh <you@yourdomain.com>")
	if name != "AgentMesh" || email != "you@yourdomain.com" {
		t.Errorf("want name=AgentMesh email=you@yourdomain.com, got name=%q email=%q", name, email)
	}
	name2, email2 := nodes.ParseEmailAddressForTest("plain@yourdomain.com")
	if name2 != "" || email2 != "plain@yourdomain.com" {
		t.Errorf("want name='' email=plain@yourdomain.com, got name=%q email=%q", name2, email2)
	}
	name3, email3 := nodes.ParseEmailAddressForTest("Some <Nickname> Person <real@example.com>")
	if name3 != "Some <Nickname> Person" || email3 != "real@example.com" {
		t.Errorf("want name='Some <Nickname> Person' email=real@example.com, got name=%q email=%q", name3, email3)
	}
}

func TestEmailAction_ResendProvider(t *testing.T) {
	var gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	nodes.SetResendAPIBaseForTest(srv.URL)
	defer nodes.SetResendAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "e3", Type: models.NodeTypeAction, Template: "email",
		EmailProvider: "resend", EmailAPIKey: "re_xxx",
		EmailFrom: "AgentMesh <you@yourdomain.com>", EmailTo: "user@example.com", EmailSubject: "Result",
	}
	rc := engine.NewRunContext("r1", []byte(`"done"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "email_sent" {
		t.Errorf("want 'email_sent', got %v", result)
	}
	if gotAuth != "Bearer re_xxx" {
		t.Errorf("want bearer auth, got %q", gotAuth)
	}
	if gotBody["from"] != "AgentMesh <you@yourdomain.com>" {
		t.Errorf("want from field passed through, got %v", gotBody["from"])
	}
}

// Every template offered in the palette must reach a real implementation.
// A template that falls through to ExecuteAction's `default:` returns
// "logged" and renders a green, successful node that did nothing — exactly
// the bug the db template shipped with.
func TestEveryNewActionTemplateIsDispatched(t *testing.T) {
	templates := []string{
		"stripe", "twilio", "mattermost", "pagerduty",
		"zendesk", "monday", "shopify", "shopify_customer", "pipedrive", "db", "rss",
		"graphql", "hackernews", "coingecko",
		"intercom", "openweathermap", "calendly", "baserow",
	}
	for _, tpl := range templates {
		t.Run(tpl, func(t *testing.T) {
			node := models.WorkflowNode{ID: "n1", Type: models.NodeTypeAction, Template: tpl}
			rc := engine.NewRunContext("r1", nil)
			got, _ := nodes.ExecuteAction(context.Background(), node, rc)
			if got == "logged" {
				t.Errorf("template %q fell through to ExecuteAction's default branch — "+
					"it renders as a successful node but does nothing", tpl)
			}
		})
	}
}

// EmailBody's {{ result }} placeholder predates the shared resolveTemplate
// helper (it used to be its own narrow replaceVar substitution) -- this
// pins that the migration to resolveTemplate didn't change its behavior.
func TestEmailAction_BodyTemplateBareResultPlaceholder(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	nodes.SetResendAPIBaseForTest(srv.URL)
	defer nodes.SetResendAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "e4", Type: models.NodeTypeAction, Template: "email",
		EmailProvider: "resend", EmailAPIKey: "re_xxx",
		EmailFrom: "AgentMesh <you@yourdomain.com>", EmailTo: "user@example.com",
		EmailBody: "Your result: {{ result }}",
	}
	rc := engine.NewRunContext("r1", []byte(`"42"`))
	if _, err := nodes.ExecuteAction(context.Background(), node, rc); err != nil {
		t.Fatal(err)
	}
	if gotBody["text"] != "Your result: 42" {
		t.Errorf("want bare {{ result }} substitution, got %v", gotBody["text"])
	}
}

// New capability from the same migration: a dotted path picks one field out
// of a structured upstream output, instead of the email body only ever
// being able to embed the whole thing.
func TestEmailAction_BodyTemplateDottedPathPlaceholder(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	nodes.SetResendAPIBaseForTest(srv.URL)
	defer nodes.SetResendAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "e5", Type: models.NodeTypeAction, Template: "email",
		EmailProvider: "resend", EmailAPIKey: "re_xxx",
		EmailFrom: "AgentMesh <you@yourdomain.com>", EmailTo: "user@example.com",
		EmailBody: "Summary: {{ result.extract }}",
	}
	rc := engine.NewRunContext("r1", nil)
	rc.Set("h1", map[string]any{"extract": "Algorand is fast."})
	if _, err := nodes.ExecuteAction(context.Background(), node, rc); err != nil {
		t.Fatal(err)
	}
	if gotBody["text"] != "Summary: Algorand is fast." {
		t.Errorf("want dotted-path substitution, got %v", gotBody["text"])
	}
}
