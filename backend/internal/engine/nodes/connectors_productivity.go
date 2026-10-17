package nodes

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/agentmesh/backend/internal/models"
)

// SetNotionAPIBaseForTest overrides the Notion API base URL. Call only from
// tests. Pass "" to reset to the real API.
func SetNotionAPIBaseForTest(base string) { setAPIBaseForTest("notion", base) }

func sendNotion(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	// OAuth-linked token takes priority: Notion's OAuth access token works
	// identically to a manual internal integration secret here (same Bearer scheme).
	apiKey := secretVal(node, "notionOAuthAccessToken")
	if apiKey == "" {
		apiKey = secretVal(node, "notionAPIKey")
	}
	if apiKey == "" {
		return "notion_skipped_no_api_key", ErrActionSkipped
	}
	pageID := configVal(node, "notionPageID", "")
	if pageID == "" {
		return "notion_skipped_no_page_id", ErrActionSkipped
	}
	target := apiBase("notion") + "/v1/blocks/" + url.PathEscape(pageID) + "/children"
	payload := map[string]any{
		"children": []map[string]any{{
			"object": "block",
			"type":   "paragraph",
			"paragraph": map[string]any{
				"rich_text": []map[string]any{{
					"type": "text",
					"text": map[string]any{"content": resolveMessage(node, rc)},
				}},
			},
		}},
	}
	headers := map[string]string{
		"Authorization":  "Bearer " + apiKey,
		"Notion-Version": "2022-06-28",
	}
	req, err := newJSONRequest(ctx, http.MethodPatch, target, headers, payload)
	if err != nil {
		return nil, fmt.Errorf("Notion: %w", err)
	}
	return doAndCheck(req, "notion_block_appended", "Notion")
}

// SetAirtableAPIBaseForTest overrides the Airtable API base URL. Call only
// from tests. Pass "" to reset to the real API.
func SetAirtableAPIBaseForTest(base string) { setAPIBaseForTest("airtable", base) }

func sendAirtable(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	// OAuth-linked token takes priority: Airtable's OAuth access token works
	// identically to a manual personal access token here (same Bearer scheme).
	apiKey := secretVal(node, "airtableOAuthAccessToken")
	if apiKey == "" {
		apiKey = secretVal(node, "airtableAPIKey")
	}
	if apiKey == "" {
		return "airtable_skipped_no_api_key", ErrActionSkipped
	}
	baseID := configVal(node, "airtableBaseID", "")
	table := configVal(node, "airtableTable", "")
	if baseID == "" || table == "" {
		return "airtable_skipped_missing_config", ErrActionSkipped
	}
	fieldName := configVal(node, "airtableFieldName", "Notes")
	target := apiBase("airtable") + "/v0/" + url.PathEscape(baseID) + "/" + url.PathEscape(table)
	payload := map[string]any{"fields": map[string]any{fieldName: resolveMessage(node, rc)}}
	headers := map[string]string{"Authorization": "Bearer " + apiKey}
	return postJSON(ctx, target, headers, payload, "airtable_record_created", "Airtable")
}

// SetTrelloAPIBaseForTest overrides the Trello API base URL. Call only
// from tests. Pass "" to reset to the real API.
func SetTrelloAPIBaseForTest(base string) { setAPIBaseForTest("trello", base) }

func sendTrello(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	apiKey := secretVal(node, "trelloAPIKey")
	token := secretVal(node, "trelloToken")
	if apiKey == "" || token == "" {
		return "trello_skipped_no_credentials", ErrActionSkipped
	}
	listID := configVal(node, "trelloListID", "")
	if listID == "" {
		return "trello_skipped_no_list_id", ErrActionSkipped
	}
	// Trello requires key/token as query params, but name/desc go in the JSON
	// body — putting the (unbounded) agent message in the query string risked
	// exceeding common proxy/server URL-length limits for long messages.
	q := url.Values{}
	q.Set("key", apiKey)
	q.Set("token", token)
	target := apiBase("trello") + "/1/cards?" + q.Encode()
	msg := resolveMessage(node, rc)
	payload := map[string]any{
		"idList": listID,
		"name":   issueTitle(msg),
		"desc":   msg,
	}
	return postJSON(ctx, target, nil, payload, "trello_card_created", "Trello")
}

// SetAsanaAPIBaseForTest overrides the Asana API base URL. Call only
// from tests. Pass "" to reset to the real API.
func SetAsanaAPIBaseForTest(base string) { setAPIBaseForTest("asana", base) }

func sendAsana(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	// OAuth-linked token takes priority: Asana's OAuth access token works
	// identically to a manual personal access token here (same Bearer scheme).
	apiKey := secretVal(node, "asanaOAuthAccessToken")
	if apiKey == "" {
		apiKey = secretVal(node, "asanaAPIKey")
	}
	if apiKey == "" {
		return "asana_skipped_no_api_key", ErrActionSkipped
	}
	projectID := configVal(node, "asanaProjectID", "")
	if projectID == "" {
		return "asana_skipped_no_project_id", ErrActionSkipped
	}
	msg := resolveMessage(node, rc)
	payload := map[string]any{
		"data": map[string]any{
			"name":     issueTitle(msg),
			"notes":    msg,
			"projects": []string{projectID},
		},
	}
	headers := map[string]string{"Authorization": "Bearer " + apiKey}
	return postJSON(ctx, apiBase("asana")+"/api/1.0/tasks", headers, payload, "asana_task_created", "Asana")
}

// SetClickUpAPIBaseForTest overrides the ClickUp API base URL. Call only
// from tests. Pass "" to reset to the real API.
func SetClickUpAPIBaseForTest(base string) { setAPIBaseForTest("clickup", base) }

func sendClickUp(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	// Unlike Notion/Airtable/Asana above, ClickUp's manual personal-token and
	// OAuth-token headers are NOT the same scheme: personal tokens go raw with
	// no prefix (see the apiKey branch below, untouched), while ClickUp's docs
	// specify "Authorization: Bearer {access_token}" for OAuth-issued tokens.
	// So this can't share one headers construction across both paths the way
	// the other connectors do.
	oauthToken := secretVal(node, "clickupOAuthAccessToken")
	apiKey := secretVal(node, "clickupAPIKey")
	if oauthToken == "" && apiKey == "" {
		return "clickup_skipped_no_api_key", ErrActionSkipped
	}
	listID := configVal(node, "clickupListID", "")
	if listID == "" {
		return "clickup_skipped_no_list_id", ErrActionSkipped
	}
	target := apiBase("clickup") + "/api/v2/list/" + url.PathEscape(listID) + "/task"
	msg := resolveMessage(node, rc)
	payload := map[string]any{"name": issueTitle(msg), "description": msg}
	var headers map[string]string
	if oauthToken != "" {
		headers = map[string]string{"Authorization": "Bearer " + oauthToken}
	} else {
		headers = map[string]string{"Authorization": apiKey}
	}
	return postJSON(ctx, target, headers, payload, "clickup_task_created", "ClickUp")
}

// SetTodoistAPIBaseForTest overrides the Todoist API base URL. Call only
// from tests. Pass "" to reset to the real API.
func SetTodoistAPIBaseForTest(base string) { setAPIBaseForTest("todoist", base) }

func sendTodoist(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	// OAuth-linked token takes priority: Todoist's OAuth access token works
	// identically to a manual personal API token here (same Bearer scheme).
	apiKey := secretVal(node, "todoistOAuthAccessToken")
	if apiKey == "" {
		apiKey = secretVal(node, "todoistAPIKey")
	}
	if apiKey == "" {
		return "todoist_skipped_no_api_key", ErrActionSkipped
	}
	msg := resolveMessage(node, rc)
	payload := map[string]any{"content": issueTitle(msg), "description": msg}
	if projectID := configVal(node, "todoistProjectID", ""); projectID != "" {
		payload["project_id"] = projectID
	}
	headers := map[string]string{"Authorization": "Bearer " + apiKey}
	return postJSON(ctx, apiBase("todoist")+"/rest/v2/tasks", headers, payload, "todoist_task_created", "Todoist")
}
