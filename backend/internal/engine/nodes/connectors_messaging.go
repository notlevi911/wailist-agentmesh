package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/agentmesh/backend/internal/models"
)

// slackAPIBase is overridden in tests via SetSlackAPIBaseForTest so requests
// hit an httptest server instead of the real Slack API.
var slackAPIBase = "https://slack.com"

// SetSlackAPIBaseForTest overrides the Slack API base URL. Call only from
// tests. Pass "" to reset to the real API.
func SetSlackAPIBaseForTest(base string) {
	if base == "" {
		slackAPIBase = "https://slack.com"
	} else {
		slackAPIBase = base
	}
}

func sendSlack(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	if botToken := secretVal(node, "slackOAuthAccessToken"); botToken != "" {
		channel := configVal(node, "slackChannel", "")
		if channel == "" {
			return "slack_skipped_no_channel", ErrActionSkipped
		}
		payload := map[string]any{"channel": channel, "text": resolveMessage(node, rc)}
		headers := map[string]string{"Authorization": "Bearer " + botToken}
		// Slack's chat.postMessage returns HTTP 200 for application-level
		// failures (bad channel, revoked token, missing scope), putting them in
		// a top-level "ok"/"error" field instead of the status code —
		// postJSON/doAndCheck only inspects the status code, so this can't
		// route through them the way the webhook-URL connectors do.
		req, err := newJSONRequest(ctx, http.MethodPost, slackAPIBase+"/api/chat.postMessage", headers, payload)
		if err != nil {
			return nil, fmt.Errorf("Slack: %w", err)
		}
		resp, err := doValidatedRequest(req, "Slack")
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("Slack API %d: %s", resp.StatusCode, readErrorBody(resp))
		}
		body, err := readBounded(resp.Body, httpResponseLimit)
		if err != nil {
			return nil, fmt.Errorf("Slack: read response: %w", err)
		}
		var result struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("Slack: decode response: %w", err)
		}
		if !result.OK {
			return nil, fmt.Errorf("Slack: %s", result.Error)
		}
		return "slack_sent", nil
	}
	webhookURL := secretVal(node, "slackWebhookURL")
	if webhookURL == "" {
		return "slack_skipped_no_webhook_url", ErrActionSkipped
	}
	payload := map[string]any{"text": resolveMessage(node, rc)}
	return postJSON(ctx, webhookURL, nil, payload, "slack_sent", "Slack")
}

func sendDiscord(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	webhookURL := secretVal(node, "discordWebhookURL")
	if webhookURL == "" {
		return "discord_skipped_no_webhook_url", ErrActionSkipped
	}
	payload := map[string]any{"content": resolveMessage(node, rc)}
	return postJSON(ctx, webhookURL, nil, payload, "discord_sent", "Discord")
}

func sendTeams(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	webhookURL := secretVal(node, "teamsWebhookURL")
	if webhookURL == "" {
		return "teams_skipped_no_webhook_url", ErrActionSkipped
	}
	payload := map[string]any{
		"@type":    "MessageCard",
		"@context": "http://schema.org/extensions",
		"text":     resolveMessage(node, rc),
	}
	return postJSON(ctx, webhookURL, nil, payload, "teams_sent", "Teams")
}

func sendGoogleChat(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	webhookURL := secretVal(node, "googleChatWebhookURL")
	if webhookURL == "" {
		return "google_chat_skipped_no_webhook_url", ErrActionSkipped
	}
	payload := map[string]any{"text": resolveMessage(node, rc)}
	return postJSON(ctx, webhookURL, nil, payload, "google_chat_sent", "Google Chat")
}

func sendNtfy(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	topic := configVal(node, "ntfyTopic", "")
	if topic == "" {
		return "ntfy_skipped_no_topic", ErrActionSkipped
	}
	server := configVal(node, "ntfyServerURL", "https://ntfy.sh")
	target := strings.TrimRight(server, "/") + "/" + url.PathEscape(topic)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(resolveMessage(node, rc)))
	if err != nil {
		return nil, fmt.Errorf("ntfy: build request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain")
	if token := secretVal(node, "ntfyAuthToken"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return doAndCheck(req, "ntfy_sent", "ntfy")
}

// telegramAPIBase is overridden in tests via SetTelegramAPIBaseForTest so requests
// hit an httptest server instead of the real Telegram Bot API.
var telegramAPIBase = "https://api.telegram.org"

// SetTelegramAPIBaseForTest overrides the Telegram API base URL. Call only from
// tests. Pass "" to reset to the real API.
func SetTelegramAPIBaseForTest(base string) {
	if base == "" {
		telegramAPIBase = "https://api.telegram.org"
	} else {
		telegramAPIBase = base
	}
}

func sendTelegram(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	// TrimSpace guards against a stray newline or trailing space surviving
	// a copy-paste from BotFather's message -- Telegram's own router keys
	// off the token in the URL path, so any such byte turns a genuinely
	// valid token into a 404 "Not Found" that looks identical to a wrong
	// token, with nothing in the response to tell them apart.
	token := strings.TrimSpace(secretVal(node, "telegramBotToken"))
	if token == "" {
		return "telegram_skipped_no_bot_token", ErrActionSkipped
	}
	chatID := strings.TrimSpace(configVal(node, "telegramChatID", ""))
	if chatID == "" {
		return "telegram_skipped_no_chat_id", ErrActionSkipped
	}
	target := telegramAPIBase + "/bot" + token + "/sendMessage"
	payload := map[string]any{"chat_id": chatID, "text": resolveMessage(node, rc)}
	return postJSON(ctx, target, nil, payload, "telegram_sent", "Telegram")
}

// getTelegramUpdates reads new messages the bot has received via long
// polling's non-blocking form (getUpdates without a wait), rather than
// only ever being able to send. It's the first genuinely read-capable
// connector op -- see connector_helpers.go's getJSON.
func getTelegramUpdates(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	token := strings.TrimSpace(secretVal(node, "telegramBotToken"))
	if token == "" {
		return "telegram_skipped_no_bot_token", ErrActionSkipped
	}
	q := url.Values{}
	if offset := configVal(node, "telegramOffset", ""); offset != "" {
		q.Set("offset", offset)
	}
	if limit := configVal(node, "telegramLimit", ""); limit != "" {
		q.Set("limit", limit)
	}
	target := telegramAPIBase + "/bot" + token + "/getUpdates"
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("telegram: build request: %w", err)
	}
	return getJSON(req, "Telegram")
}
