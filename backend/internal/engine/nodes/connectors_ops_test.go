package nodes_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/agentmesh/backend/internal/engine"
	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
)

func TestTwilioAction_SendsSMS(t *testing.T) {
	var gotUser, gotPass, gotPath string
	var gotForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, _ = r.BasicAuth()
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(body))
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"sid":"SM123"}`))
	}))
	defer srv.Close()
	nodes.SetTwilioAPIBaseForTest(srv.URL)
	defer nodes.SetTwilioAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "tw1", Type: models.NodeTypeAction, Template: "twilio",
		Secrets: map[string]string{"twilioAuthToken": "authtok"},
		Config: map[string]string{
			"twilioAccountSID": "AC123",
			"twilioFrom":       "+15550001111",
			"twilioTo":         "+15550002222",
		},
	}
	rc := engine.NewRunContext("r1", []byte(`"deploy finished"`))

	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "twilio_sms_sent" {
		t.Errorf("want 'twilio_sms_sent', got %v", result)
	}
	if gotUser != "AC123" || gotPass != "authtok" {
		t.Errorf("want basic auth SID/token, got %q/%q", gotUser, gotPass)
	}
	if !strings.HasSuffix(gotPath, "/Accounts/AC123/Messages.json") {
		t.Errorf("want the account-scoped Messages path, got %q", gotPath)
	}
	if gotForm.Get("To") != "+15550002222" || gotForm.Get("From") != "+15550001111" {
		t.Errorf("To/From: got %v", gotForm)
	}
	if gotForm.Get("Body") != "deploy finished" {
		t.Errorf("Body should be the run output, got %q", gotForm.Get("Body"))
	}
}

// A configured messageTemplate must actually be honored, not silently
// ignored in favor of the raw upstream output -- resolveMessage, not
// rc.Message() directly.
func TestTwilioAction_HonorsMessageTemplate(t *testing.T) {
	var gotForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(body))
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"sid":"SM123"}`))
	}))
	defer srv.Close()
	nodes.SetTwilioAPIBaseForTest(srv.URL)
	defer nodes.SetTwilioAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "tw3", Type: models.NodeTypeAction, Template: "twilio",
		Secrets: map[string]string{"twilioAuthToken": "authtok"},
		Config: map[string]string{
			"twilioAccountSID": "AC123",
			"twilioFrom":       "+15550001111",
			"twilioTo":         "+15550002222",
			"messageTemplate":  "alert: {{ result }}",
		},
	}
	rc := engine.NewRunContext("r1", []byte(`"disk full"`))

	if _, err := nodes.ExecuteAction(context.Background(), node, rc); err != nil {
		t.Fatal(err)
	}
	if gotForm.Get("Body") != "alert: disk full" {
		t.Errorf("want templated body, got %q", gotForm.Get("Body"))
	}
}

// A node saved before this repo's Twilio Inspector field moved
// twilioAccountSID from a secret to a config value has its SID under
// Secrets, not Config -- must still resolve, or every pre-existing Twilio
// node silently breaks on deploy.
func TestTwilioAction_AccountSIDFallsBackToLegacySecret(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"sid":"SM123"}`))
	}))
	defer srv.Close()
	nodes.SetTwilioAPIBaseForTest(srv.URL)
	defer nodes.SetTwilioAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "tw2", Type: models.NodeTypeAction, Template: "twilio",
		Secrets: map[string]string{
			"twilioAuthToken":  "authtok",
			"twilioAccountSID": "AC999",
		},
		Config: map[string]string{
			"twilioFrom": "+15550001111",
			"twilioTo":   "+15550002222",
		},
	}
	rc := engine.NewRunContext("r1", []byte(`"deploy finished"`))

	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "twilio_sms_sent" {
		t.Errorf("want 'twilio_sms_sent', got %v", result)
	}
}

func TestTwilioAction_SkipsWhenUnconfigured(t *testing.T) {
	cases := []struct {
		name string
		node models.WorkflowNode
		want string
	}{
		{"no token", models.WorkflowNode{
			Template: "twilio",
			Config:   map[string]string{"twilioAccountSID": "AC1", "twilioFrom": "+1", "twilioTo": "+2"},
		}, "twilio_skipped_no_auth_token"},
		{"no sid", models.WorkflowNode{
			Template: "twilio",
			Secrets:  map[string]string{"twilioAuthToken": "t"},
			Config:   map[string]string{"twilioFrom": "+1", "twilioTo": "+2"},
		}, "twilio_skipped_no_account_sid"},
		{"no recipient", models.WorkflowNode{
			Template: "twilio",
			Secrets:  map[string]string{"twilioAuthToken": "t"},
			Config:   map[string]string{"twilioAccountSID": "AC1", "twilioFrom": "+1"},
		}, "twilio_skipped_no_recipient"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.node.Type = models.NodeTypeAction
			rc := engine.NewRunContext("r1", nil)
			got, _ := nodes.ExecuteAction(context.Background(), tc.node, rc)
			if got != tc.want {
				t.Errorf("want %q, got %v", tc.want, got)
			}
		})
	}
}

func TestMattermostAction_PostsToWebhook(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = jsonDecode(r.Body, &gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	node := models.WorkflowNode{
		ID: "mm1", Type: models.NodeTypeAction, Template: "mattermost",
		Secrets: map[string]string{"mattermostWebhookURL": srv.URL},
		Config:  map[string]string{"mattermostChannel": "town-square"},
	}
	rc := engine.NewRunContext("r1", []byte(`"build passed"`))

	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "mattermost_sent" {
		t.Errorf("want 'mattermost_sent', got %v", result)
	}
	if gotBody["text"] != "build passed" {
		t.Errorf("text: got %v", gotBody["text"])
	}
	if gotBody["channel"] != "town-square" {
		t.Errorf("channel: got %v", gotBody["channel"])
	}
}

func TestMattermostAction_SkipsWithoutWebhookURL(t *testing.T) {
	node := models.WorkflowNode{ID: "mm1", Type: models.NodeTypeAction, Template: "mattermost"}
	rc := engine.NewRunContext("r1", nil)
	got, _ := nodes.ExecuteAction(context.Background(), node, rc)
	if got != "mattermost_skipped_no_webhook_url" {
		t.Errorf("want skip sentinel, got %v", got)
	}
}

func TestPagerDutyAction_TriggersIncident(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = jsonDecode(r.Body, &gotBody)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	nodes.SetPagerDutyAPIBaseForTest(srv.URL)
	defer nodes.SetPagerDutyAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "pd1", Type: models.NodeTypeAction, Template: "pagerduty",
		Secrets: map[string]string{"pagerdutyRoutingKey": "routing_xxx"},
		Config:  map[string]string{"pagerdutySeverity": "warning"},
	}
	rc := engine.NewRunContext("r1", []byte(`"disk usage above 90%"`))

	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "pagerduty_event_triggered" {
		t.Errorf("want 'pagerduty_event_triggered', got %v", result)
	}
	if gotBody["routing_key"] != "routing_xxx" {
		t.Errorf("routing_key: got %v", gotBody["routing_key"])
	}
	if gotBody["event_action"] != "trigger" {
		t.Errorf("event_action: got %v", gotBody["event_action"])
	}
	payload, ok := gotBody["payload"].(map[string]any)
	if !ok {
		t.Fatalf("want a payload object, got %#v", gotBody["payload"])
	}
	if payload["summary"] != "disk usage above 90%" {
		t.Errorf("summary: got %v", payload["summary"])
	}
	if payload["severity"] != "warning" {
		t.Errorf("severity: got %v", payload["severity"])
	}
	if payload["source"] != "agentmesh" {
		t.Errorf("source: got %v", payload["source"])
	}
}

// Default severity is "info", not "error": every already-deployed PagerDuty
// node that never set pagerdutySeverity explicitly must not have its alerts
// silently escalate to "error" with no config change on the user's side.
func TestPagerDutyAction_DefaultsSeverityToInfo(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = jsonDecode(r.Body, &gotBody)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	nodes.SetPagerDutyAPIBaseForTest(srv.URL)
	defer nodes.SetPagerDutyAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "pd1", Type: models.NodeTypeAction, Template: "pagerduty",
		Secrets: map[string]string{"pagerdutyRoutingKey": "routing_xxx"},
	}
	rc := engine.NewRunContext("r1", []byte(`"boom"`))
	if _, err := nodes.ExecuteAction(context.Background(), node, rc); err != nil {
		t.Fatal(err)
	}
	payload := gotBody["payload"].(map[string]any)
	if payload["severity"] != "info" {
		t.Errorf("want default severity 'info', got %v", payload["severity"])
	}
}

// A summary over PagerDuty's Events API v2 1024-char cap must be truncated
// (issueTitle), not sent raw -- a multi-KB LLM result would otherwise be
// rejected with a 400 right when an alert matters most.
func TestPagerDutyAction_TruncatesLongSummary(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = jsonDecode(r.Body, &gotBody)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	nodes.SetPagerDutyAPIBaseForTest(srv.URL)
	defer nodes.SetPagerDutyAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "pd1", Type: models.NodeTypeAction, Template: "pagerduty",
		Secrets: map[string]string{"pagerdutyRoutingKey": "routing_xxx"},
	}
	longMsg := strings.Repeat("x", 2000)
	rc := engine.NewRunContext("r1", []byte(`"`+longMsg+`"`))
	if _, err := nodes.ExecuteAction(context.Background(), node, rc); err != nil {
		t.Fatal(err)
	}
	payload := gotBody["payload"].(map[string]any)
	summary, _ := payload["summary"].(string)
	if len(summary) >= 1024 {
		t.Errorf("summary not truncated: %d chars", len(summary))
	}
}

// A configured messageTemplate must actually be honored, not silently
// ignored in favor of the raw upstream output.
func TestPagerDutyAction_HonorsMessageTemplate(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = jsonDecode(r.Body, &gotBody)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	nodes.SetPagerDutyAPIBaseForTest(srv.URL)
	defer nodes.SetPagerDutyAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "pd1", Type: models.NodeTypeAction, Template: "pagerduty",
		Secrets: map[string]string{"pagerdutyRoutingKey": "routing_xxx"},
		Config:  map[string]string{"messageTemplate": "alert: {{ result }}"},
	}
	rc := engine.NewRunContext("r1", []byte(`"disk full"`))
	if _, err := nodes.ExecuteAction(context.Background(), node, rc); err != nil {
		t.Fatal(err)
	}
	payload := gotBody["payload"].(map[string]any)
	if payload["summary"] != "alert: disk full" {
		t.Errorf("want templated summary, got %v", payload["summary"])
	}
}

func TestPagerDutyAction_SkipsWithoutRoutingKey(t *testing.T) {
	node := models.WorkflowNode{ID: "pd1", Type: models.NodeTypeAction, Template: "pagerduty"}
	rc := engine.NewRunContext("r1", nil)
	got, _ := nodes.ExecuteAction(context.Background(), node, rc)
	if got != "pagerduty_skipped_no_routing_key" {
		t.Errorf("want skip sentinel, got %v", got)
	}
}

// jsonDecode is a tiny helper so these tests read the same way as the
// existing connector tests without repeating the decoder boilerplate.
func jsonDecode(r io.Reader, v any) error {
	return json.NewDecoder(r).Decode(v)
}

func TestZendeskAction_CreatesTicket(t *testing.T) {
	var gotUser, gotPass string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, _ = r.BasicAuth()
		_ = jsonDecode(r.Body, &gotBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	nodes.SetZendeskAPIBaseForTest(srv.URL)
	defer nodes.SetZendeskAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "zd1", Type: models.NodeTypeAction, Template: "zendesk",
		Secrets: map[string]string{"zendeskAPIToken": "zdtok"},
		Config: map[string]string{
			"zendeskSubdomain": "acme",
			"zendeskEmail":     "agent@acme.com",
		},
	}
	rc := engine.NewRunContext("r1", []byte(`"customer cannot log in"`))

	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "zendesk_ticket_created" {
		t.Errorf("want 'zendesk_ticket_created', got %v", result)
	}
	if gotUser != "agent@acme.com/token" || gotPass != "zdtok" {
		t.Errorf("want Zendesk's email/token basic auth, got %q/%q", gotUser, gotPass)
	}
	ticket, ok := gotBody["ticket"].(map[string]any)
	if !ok {
		t.Fatalf("want a ticket envelope, got %#v", gotBody)
	}
	comment := ticket["comment"].(map[string]any)
	if comment["body"] != "customer cannot log in" {
		t.Errorf("comment body: got %v", comment["body"])
	}
	if ticket["subject"] == "" || ticket["subject"] == nil {
		t.Error("want a non-empty subject derived from the message")
	}
}

func TestZendeskAction_SkipsWhenUnconfigured(t *testing.T) {
	cases := []struct {
		name string
		node models.WorkflowNode
		want string
	}{
		{"no token", models.WorkflowNode{Template: "zendesk",
			Config: map[string]string{"zendeskSubdomain": "acme", "zendeskEmail": "a@b.com"}},
			"zendesk_skipped_no_api_token"},
		{"no subdomain", models.WorkflowNode{Template: "zendesk",
			Secrets: map[string]string{"zendeskAPIToken": "t"},
			Config:  map[string]string{"zendeskEmail": "a@b.com"}},
			"zendesk_skipped_missing_config"},
		{"no email", models.WorkflowNode{Template: "zendesk",
			Secrets: map[string]string{"zendeskAPIToken": "t"},
			Config:  map[string]string{"zendeskSubdomain": "acme"}},
			"zendesk_skipped_missing_config"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.node.Type = models.NodeTypeAction
			rc := engine.NewRunContext("r1", nil)
			got, _ := nodes.ExecuteAction(context.Background(), tc.node, rc)
			if got != tc.want {
				t.Errorf("want %q, got %v", tc.want, got)
			}
		})
	}
}

func TestMondayAction_CreatesItem(t *testing.T) {
	var gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = jsonDecode(r.Body, &gotBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":{"create_item":{"id":"1"}}}`))
	}))
	defer srv.Close()
	nodes.SetMondayAPIBaseForTest(srv.URL)
	defer nodes.SetMondayAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "mo1", Type: models.NodeTypeAction, Template: "monday",
		Secrets: map[string]string{"mondayAPIKey": "mtok"},
		Config:  map[string]string{"mondayBoardID": "12345"},
	}
	rc := engine.NewRunContext("r1", []byte(`"follow up with the lead"`))

	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "monday_item_created" {
		t.Errorf("want 'monday_item_created', got %v", result)
	}
	if gotAuth != "mtok" {
		t.Errorf("Monday sends the raw token, not a Bearer prefix; got %q", gotAuth)
	}
	if _, ok := gotBody["query"].(string); !ok {
		t.Errorf("want a GraphQL query field, got %#v", gotBody)
	}
	vars, ok := gotBody["variables"].(map[string]any)
	if !ok {
		t.Fatalf("want GraphQL variables, got %#v", gotBody["variables"])
	}
	if vars["boardId"] != "12345" {
		t.Errorf("boardId: got %v", vars["boardId"])
	}
	if vars["itemName"] != "follow up with the lead" {
		t.Errorf("itemName: got %v", vars["itemName"])
	}
}

// Monday.com is a GraphQL API: it reports failures as HTTP 200 with an
// "errors" array, the same as any other GraphQL endpoint. Returning that as
// success would render a green node for an item that was never created.
func TestMondayAction_ReturnsErrorOnGraphQLErrorsArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"errors":[{"message":"board not found"}]}`))
	}))
	defer srv.Close()
	nodes.SetMondayAPIBaseForTest(srv.URL)
	defer nodes.SetMondayAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "mo2", Type: models.NodeTypeAction, Template: "monday",
		Secrets: map[string]string{"mondayAPIKey": "mtok"},
		Config:  map[string]string{"mondayBoardID": "bad-id"},
	}
	rc := engine.NewRunContext("r1", []byte(`"follow up with the lead"`))

	_, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err == nil {
		t.Fatal("want an error for a GraphQL errors-array response, got nil")
	}
	if !strings.Contains(err.Error(), "board not found") {
		t.Errorf("want the GraphQL error message surfaced, got %v", err)
	}
}

// A configured messageTemplate must actually be honored, not silently
// ignored in favor of the raw upstream output.
func TestMondayAction_HonorsMessageTemplate(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = jsonDecode(r.Body, &gotBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":{"create_item":{"id":"1"}}}`))
	}))
	defer srv.Close()
	nodes.SetMondayAPIBaseForTest(srv.URL)
	defer nodes.SetMondayAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "mo3", Type: models.NodeTypeAction, Template: "monday",
		Secrets: map[string]string{"mondayAPIKey": "mtok"},
		Config:  map[string]string{"mondayBoardID": "12345", "messageTemplate": "task: {{ result }}"},
	}
	rc := engine.NewRunContext("r1", []byte(`"follow up with the lead"`))

	if _, err := nodes.ExecuteAction(context.Background(), node, rc); err != nil {
		t.Fatal(err)
	}
	vars := gotBody["variables"].(map[string]any)
	if vars["itemName"] != "task: follow up with the lead" {
		t.Errorf("itemName: got %v", vars["itemName"])
	}
}

func TestMondayAction_SkipsWithoutBoardID(t *testing.T) {
	node := models.WorkflowNode{
		ID: "mo1", Type: models.NodeTypeAction, Template: "monday",
		Secrets: map[string]string{"mondayAPIKey": "mtok"},
	}
	rc := engine.NewRunContext("r1", nil)
	got, _ := nodes.ExecuteAction(context.Background(), node, rc)
	if got != "monday_skipped_no_board_id" {
		t.Errorf("want skip sentinel, got %v", got)
	}
}
