package nodes_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentmesh/backend/internal/engine"
	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
)

func TestSlackAction_PostsMessageText(t *testing.T) {
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	node := models.WorkflowNode{
		ID: "s1", Type: models.NodeTypeAction, Template: "slack",
		Secrets: map[string]string{"slackWebhookURL": srv.URL},
	}
	rc := engine.NewRunContext("r1", []byte(`"hello from agent"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "slack_sent" {
		t.Errorf("want 'slack_sent', got %v", result)
	}
	if received["text"] != "hello from agent" {
		t.Errorf("want text field with message, got %v", received)
	}
}

// End-to-end proof that a real connector's payload actually reflects a
// configured messageTemplate, not just that expandTemplate works in
// isolation -- this is the wiring (resolveMessage replacing rc.Message()
// at the call site) that the unit tests above can't catch on their own.
func TestSlackAction_AppliesMessageTemplate(t *testing.T) {
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	node := models.WorkflowNode{
		ID: "s2", Type: models.NodeTypeAction, Template: "slack",
		Secrets: map[string]string{"slackWebhookURL": srv.URL},
		Config:  map[string]string{"messageTemplate": "New summary: {{ result.extract }}"},
	}
	rc := engine.NewRunContext("r1", nil)
	rc.Set("h1", map[string]any{"extract": "Algorand is a proof-of-stake blockchain."})

	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "slack_sent" {
		t.Errorf("want 'slack_sent', got %v", result)
	}
	want := "New summary: Algorand is a proof-of-stake blockchain."
	if received["text"] != want {
		t.Errorf("want templated text %q, got %v", want, received["text"])
	}
}

func TestSlackAction_SkipsWhenNoWebhookURL(t *testing.T) {
	node := models.WorkflowNode{ID: "s2", Type: models.NodeTypeAction, Template: "slack"}
	rc := engine.NewRunContext("r1", []byte(`"hi"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "slack_skipped_no_webhook_url" {
		t.Errorf("want skip sentinel, got %v", result)
	}
}

func TestSlackAction_BotTokenModePostsToChannel(t *testing.T) {
	var gotAuth string
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&received)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()
	nodes.SetSlackAPIBaseForTest(srv.URL)
	defer nodes.SetSlackAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "s3", Type: models.NodeTypeAction, Template: "slack",
		Secrets: map[string]string{"slackOAuthAccessToken": "xoxb-fake-bot-token"},
		Config:  map[string]string{"slackChannel": "C0123456789"},
	}
	rc := engine.NewRunContext("r1", []byte(`"hello from a test"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "slack_sent" {
		t.Errorf("want 'slack_sent', got %v", result)
	}
	if gotAuth != "Bearer xoxb-fake-bot-token" {
		t.Errorf("want bot token in Authorization header, got %q", gotAuth)
	}
	if received["channel"] != "C0123456789" {
		t.Errorf("channel = %v, want C0123456789", received["channel"])
	}
	if received["text"] != "hello from a test" {
		t.Errorf("text = %v, want hello from a test", received["text"])
	}
}

func TestSlackAction_BotTokenModeSurfacesOKFalse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "channel_not_found"})
	}))
	defer srv.Close()
	nodes.SetSlackAPIBaseForTest(srv.URL)
	defer nodes.SetSlackAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "s3b", Type: models.NodeTypeAction, Template: "slack",
		Secrets: map[string]string{"slackOAuthAccessToken": "xoxb-fake-bot-token"},
		Config:  map[string]string{"slackChannel": "C0123456789"},
	}
	rc := engine.NewRunContext("r1", []byte(`"hello from a test"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err == nil {
		t.Fatalf("want error for ok:false response, got result %v", result)
	}
	if !strings.Contains(err.Error(), "channel_not_found") {
		t.Errorf("want error mentioning channel_not_found, got %q", err.Error())
	}
	if result == "slack_sent" {
		t.Errorf("want failure sentinel, got success sentinel 'slack_sent'")
	}
}

func TestSlackAction_BotTokenModeSkipsWhenNoChannel(t *testing.T) {
	node := models.WorkflowNode{
		ID: "s4", Type: models.NodeTypeAction, Template: "slack",
		Secrets: map[string]string{"slackOAuthAccessToken": "xoxb-fake-bot-token"},
	}
	rc := engine.NewRunContext("r1", []byte(`"hi"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "slack_skipped_no_channel" {
		t.Errorf("want skip sentinel, got %v", result)
	}
}

func TestSlackAction_FallsBackToWebhookWhenNoOAuthToken(t *testing.T) {
	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	node := models.WorkflowNode{
		ID: "s5", Type: models.NodeTypeAction, Template: "slack",
		Secrets: map[string]string{"slackWebhookURL": srv.URL},
	}
	rc := engine.NewRunContext("r1", []byte(`"hello"`))
	_, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if !hit {
		t.Fatal("expected webhook URL to be hit when no OAuth token present")
	}
}

func TestDiscordAction_PostsMessageContent(t *testing.T) {
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	node := models.WorkflowNode{
		ID: "d1", Type: models.NodeTypeAction, Template: "discord",
		Secrets: map[string]string{"discordWebhookURL": srv.URL},
	}
	rc := engine.NewRunContext("r1", []byte(`"hello discord"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "discord_sent" {
		t.Errorf("want 'discord_sent', got %v", result)
	}
	if received["content"] != "hello discord" {
		t.Errorf("want content field with message, got %v", received)
	}
}

func TestTeamsAction_PostsMessageCard(t *testing.T) {
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	node := models.WorkflowNode{
		ID: "t1", Type: models.NodeTypeAction, Template: "teams",
		Secrets: map[string]string{"teamsWebhookURL": srv.URL},
	}
	rc := engine.NewRunContext("r1", []byte(`"hello teams"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "teams_sent" {
		t.Errorf("want 'teams_sent', got %v", result)
	}
	if received["text"] != "hello teams" || received["@type"] != "MessageCard" {
		t.Errorf("want MessageCard payload with text, got %v", received)
	}
}

func TestGoogleChatAction_PostsMessageText(t *testing.T) {
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	node := models.WorkflowNode{
		ID: "g1", Type: models.NodeTypeAction, Template: "google_chat",
		Secrets: map[string]string{"googleChatWebhookURL": srv.URL},
	}
	rc := engine.NewRunContext("r1", []byte(`"hello chat"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "google_chat_sent" {
		t.Errorf("want 'google_chat_sent', got %v", result)
	}
	if received["text"] != "hello chat" {
		t.Errorf("want text field with message, got %v", received)
	}
}

func TestNtfyAction_PostsPlainTextToTopic(t *testing.T) {
	var gotPath, gotBody, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		b := make([]byte, r.ContentLength)
		r.Body.Read(b)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	node := models.WorkflowNode{
		ID: "nt1", Type: models.NodeTypeAction, Template: "ntfy",
		Config:  map[string]string{"ntfyTopic": "agentmesh-alerts", "ntfyServerURL": srv.URL},
		Secrets: map[string]string{"ntfyAuthToken": "tk_123"},
	}
	rc := engine.NewRunContext("r1", []byte(`"disk full"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "ntfy_sent" {
		t.Errorf("want 'ntfy_sent', got %v", result)
	}
	if gotPath != "/agentmesh-alerts" {
		t.Errorf("want path /agentmesh-alerts, got %q", gotPath)
	}
	if gotBody != "disk full" {
		t.Errorf("want plain-text body, got %q", gotBody)
	}
	if gotAuth != "Bearer tk_123" {
		t.Errorf("want bearer auth header, got %q", gotAuth)
	}
}

func TestNtfyAction_EscapesTopicWithSpecialChars(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	node := models.WorkflowNode{
		ID: "nt3", Type: models.NodeTypeAction, Template: "ntfy",
		Config: map[string]string{"ntfyTopic": "alerts/../admin", "ntfyServerURL": srv.URL},
	}
	rc := engine.NewRunContext("r1", []byte(`"disk full"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "ntfy_sent" {
		t.Errorf("want 'ntfy_sent', got %v", result)
	}
	if gotPath != "/alerts%2F..%2Fadmin" {
		t.Errorf("want escaped topic, got %q", gotPath)
	}
}

func TestNtfyAction_SkipsWhenNoTopic(t *testing.T) {
	node := models.WorkflowNode{ID: "nt2", Type: models.NodeTypeAction, Template: "ntfy"}
	rc := engine.NewRunContext("r1", []byte(`"hi"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "ntfy_skipped_no_topic" {
		t.Errorf("want skip sentinel, got %v", result)
	}
}

func TestTelegramAction_SendsMessageToChatID(t *testing.T) {
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	node := models.WorkflowNode{
		ID: "tg1", Type: models.NodeTypeAction, Template: "telegram",
		Secrets: map[string]string{"telegramBotToken": "123:ABC"},
		Config:  map[string]string{"telegramChatID": "999"},
	}
	// SetTelegramAPIBaseForTest lets the test point at httptest instead of api.telegram.org.
	nodes.SetTelegramAPIBaseForTest(srv.URL)
	defer nodes.SetTelegramAPIBaseForTest("")

	rc := engine.NewRunContext("r1", []byte(`"build finished"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "telegram_sent" {
		t.Errorf("want 'telegram_sent', got %v", result)
	}
	if received["chat_id"] != "999" || received["text"] != "build finished" {
		t.Errorf("want chat_id/text in payload, got %v", received)
	}
}

// A trailing newline or space surviving a copy-paste from BotFather's
// message used to reach the URL verbatim, turning a genuinely valid token
// into a 404 that looked identical to a wrong one.
func TestTelegramAction_TrimsWhitespaceFromTokenAndChatID(t *testing.T) {
	var gotPath string
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	node := models.WorkflowNode{
		ID: "tg7", Type: models.NodeTypeAction, Template: "telegram",
		Secrets: map[string]string{"telegramBotToken": "123:ABC\n"},
		Config:  map[string]string{"telegramChatID": " 999 "},
	}
	nodes.SetTelegramAPIBaseForTest(srv.URL)
	defer nodes.SetTelegramAPIBaseForTest("")

	rc := engine.NewRunContext("r1", []byte(`"hi"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "telegram_sent" {
		t.Errorf("want 'telegram_sent', got %v", result)
	}
	if gotPath != "/bot123:ABC/sendMessage" {
		t.Errorf("want trimmed token in path, got %q", gotPath)
	}
	if received["chat_id"] != "999" {
		t.Errorf("want trimmed chat_id, got %v", received["chat_id"])
	}
}

func TestTelegramAction_SkipsWhenNoBotToken(t *testing.T) {
	node := models.WorkflowNode{
		ID: "tg2", Type: models.NodeTypeAction, Template: "telegram",
		Config: map[string]string{"telegramChatID": "999"},
	}
	rc := engine.NewRunContext("r1", []byte(`"build finished"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "telegram_skipped_no_bot_token" {
		t.Errorf("want 'telegram_skipped_no_bot_token', got %v", result)
	}
}

func TestTelegramAction_SkipsWhenNoChatID(t *testing.T) {
	node := models.WorkflowNode{
		ID: "tg3", Type: models.NodeTypeAction, Template: "telegram",
		Secrets: map[string]string{"telegramBotToken": "123:ABC"},
	}
	rc := engine.NewRunContext("r1", []byte(`"build finished"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "telegram_skipped_no_chat_id" {
		t.Errorf("want 'telegram_skipped_no_chat_id', got %v", result)
	}
}

func TestTelegramGetUpdates_ReturnsDecodedResponse(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true,"result":[{"update_id":1,"message":{"text":"hi"}}]}`))
	}))
	defer srv.Close()

	node := models.WorkflowNode{
		ID: "tg4", Type: models.NodeTypeAction, Template: "telegram_get_updates",
		Secrets: map[string]string{"telegramBotToken": "123:ABC"},
		Config:  map[string]string{"telegramOffset": "5", "telegramLimit": "10"},
	}
	nodes.SetTelegramAPIBaseForTest(srv.URL)
	defer nodes.SetTelegramAPIBaseForTest("")

	rc := engine.NewRunContext("r1", []byte(`""`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/bot123:ABC/getUpdates" {
		t.Errorf("want getUpdates path, got %q", gotPath)
	}
	if gotQuery != "limit=10&offset=5" {
		t.Errorf("want offset/limit query params, got %q", gotQuery)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("want decoded map, got %T: %v", result, result)
	}
	if m["ok"] != true {
		t.Errorf("want ok=true in decoded response, got %v", m)
	}
	updates, ok := m["result"].([]any)
	if !ok || len(updates) != 1 {
		t.Errorf("want one decoded update, got %v", m["result"])
	}
}

func TestTelegramGetUpdates_SkipsWhenNoBotToken(t *testing.T) {
	node := models.WorkflowNode{ID: "tg5", Type: models.NodeTypeAction, Template: "telegram_get_updates"}
	rc := engine.NewRunContext("r1", []byte(`""`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "telegram_skipped_no_bot_token" {
		t.Errorf("want 'telegram_skipped_no_bot_token', got %v", result)
	}
}

func TestTelegramGetUpdates_PropagatesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"ok":false,"description":"Unauthorized"}`))
	}))
	defer srv.Close()

	node := models.WorkflowNode{
		ID: "tg6", Type: models.NodeTypeAction, Template: "telegram_get_updates",
		Secrets: map[string]string{"telegramBotToken": "bad-token"},
	}
	nodes.SetTelegramAPIBaseForTest(srv.URL)
	defer nodes.SetTelegramAPIBaseForTest("")

	rc := engine.NewRunContext("r1", []byte(`""`))
	if _, err := nodes.ExecuteAction(context.Background(), node, rc); err == nil {
		t.Fatal("want error on 401 response, got nil")
	}
}
