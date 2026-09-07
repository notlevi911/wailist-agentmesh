package nodes_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/agentmesh/backend/internal/engine"
	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
)

func TestNotionAction_AppendsParagraphBlock(t *testing.T) {
	var gotPath, gotMethod, gotVersion string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotVersion = r.Header.Get("Notion-Version")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	nodes.SetNotionAPIBaseForTest(srv.URL)
	defer nodes.SetNotionAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "no1", Type: models.NodeTypeAction, Template: "notion",
		Secrets: map[string]string{"notionAPIKey": "secret_xxx"},
		Config:  map[string]string{"notionPageID": "abc123"},
	}
	rc := engine.NewRunContext("r1", []byte(`"daily summary"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "notion_block_appended" {
		t.Errorf("want 'notion_block_appended', got %v", result)
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("want PATCH, got %s", gotMethod)
	}
	if gotPath != "/v1/blocks/abc123/children" {
		t.Errorf("want block children path, got %q", gotPath)
	}
	if gotVersion == "" {
		t.Error("want Notion-Version header set")
	}
	if gotBody["children"] == nil {
		t.Errorf("want children in payload, got %v", gotBody)
	}
}

func TestNotionAction_SkipsWhenNoAPIKey(t *testing.T) {
	node := models.WorkflowNode{
		ID: "no2", Type: models.NodeTypeAction, Template: "notion",
		Config: map[string]string{"notionPageID": "abc123"},
	}
	rc := engine.NewRunContext("r1", []byte(`"daily summary"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "notion_skipped_no_api_key" {
		t.Errorf("want 'notion_skipped_no_api_key', got %v", result)
	}
}

func TestNotionAction_PrefersOAuthTokenOverManualToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	nodes.SetNotionAPIBaseForTest(srv.URL)
	defer nodes.SetNotionAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "no4", Type: models.NodeTypeAction, Template: "notion",
		Secrets: map[string]string{"notionAPIKey": "manual-secret-xxx", "notionOAuthAccessToken": "oauth-derived-token"},
		Config:  map[string]string{"notionPageID": "abc123"},
	}
	rc := engine.NewRunContext("r1", []byte(`"daily summary"`))
	_, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer oauth-derived-token" {
		t.Errorf("want OAuth token in Authorization header, got %q", gotAuth)
	}
}

func TestNotionAction_SkipsWhenNoPageID(t *testing.T) {
	node := models.WorkflowNode{
		ID: "no3", Type: models.NodeTypeAction, Template: "notion",
		Secrets: map[string]string{"notionAPIKey": "secret_xxx"},
	}
	rc := engine.NewRunContext("r1", []byte(`"daily summary"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "notion_skipped_no_page_id" {
		t.Errorf("want 'notion_skipped_no_page_id', got %v", result)
	}
}

func TestAirtableAction_CreatesRecord(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	nodes.SetAirtableAPIBaseForTest(srv.URL)
	defer nodes.SetAirtableAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "at1", Type: models.NodeTypeAction, Template: "airtable",
		Secrets: map[string]string{"airtableAPIKey": "pat_xxx"},
		Config:  map[string]string{"airtableBaseID": "appXXX", "airtableTable": "Tasks"},
	}
	rc := engine.NewRunContext("r1", []byte(`"new lead captured"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "airtable_record_created" {
		t.Errorf("want 'airtable_record_created', got %v", result)
	}
	if gotPath != "/v0/appXXX/Tasks" {
		t.Errorf("want base/table path, got %q", gotPath)
	}
	if gotAuth != "Bearer pat_xxx" {
		t.Errorf("want bearer auth, got %q", gotAuth)
	}
	fields, _ := gotBody["fields"].(map[string]any)
	if fields["Notes"] != "new lead captured" {
		t.Errorf("want default field 'Notes' with message, got %v", gotBody)
	}
}

func TestAirtableAction_PrefersOAuthTokenOverManualToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	nodes.SetAirtableAPIBaseForTest(srv.URL)
	defer nodes.SetAirtableAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "at4", Type: models.NodeTypeAction, Template: "airtable",
		Secrets: map[string]string{"airtableAPIKey": "pat_xxx", "airtableOAuthAccessToken": "oauth-derived-token"},
		Config:  map[string]string{"airtableBaseID": "appXXX", "airtableTable": "Tasks"},
	}
	rc := engine.NewRunContext("r1", []byte(`"new lead captured"`))
	_, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer oauth-derived-token" {
		t.Errorf("want OAuth token in Authorization header, got %q", gotAuth)
	}
}

func TestAirtableAction_SkipsWhenNoAPIKey(t *testing.T) {
	node := models.WorkflowNode{
		ID: "at2", Type: models.NodeTypeAction, Template: "airtable",
		Config: map[string]string{"airtableBaseID": "appXXX", "airtableTable": "Tasks"},
	}
	rc := engine.NewRunContext("r1", []byte(`"new lead captured"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "airtable_skipped_no_api_key" {
		t.Errorf("want 'airtable_skipped_no_api_key', got %v", result)
	}
}

func TestAirtableAction_SkipsWhenMissingConfig(t *testing.T) {
	node := models.WorkflowNode{
		ID: "at3", Type: models.NodeTypeAction, Template: "airtable",
		Secrets: map[string]string{"airtableAPIKey": "pat_xxx"},
	}
	rc := engine.NewRunContext("r1", []byte(`"new lead captured"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "airtable_skipped_missing_config" {
		t.Errorf("want 'airtable_skipped_missing_config', got %v", result)
	}
}

func TestTrelloAction_CreatesCard(t *testing.T) {
	var gotQuery url.Values
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	nodes.SetTrelloAPIBaseForTest(srv.URL)
	defer nodes.SetTrelloAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "tr1", Type: models.NodeTypeAction, Template: "trello",
		Secrets: map[string]string{"trelloAPIKey": "key123", "trelloToken": "tok456"},
		Config:  map[string]string{"trelloListID": "list789"},
	}
	rc := engine.NewRunContext("r1", []byte(`"ship the release"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "trello_card_created" {
		t.Errorf("want 'trello_card_created', got %v", result)
	}
	if gotQuery.Get("key") != "key123" || gotQuery.Get("token") != "tok456" {
		t.Errorf("want key/token in query, got %v", gotQuery)
	}
	if gotBody["idList"] != "list789" {
		t.Errorf("want idList in body, got %v", gotBody)
	}
	if gotBody["name"] != "ship the release" {
		t.Errorf("want name in body, got %v", gotBody)
	}
	if gotBody["desc"] != "ship the release" {
		t.Errorf("want desc in body, got %v", gotBody)
	}
}

func TestTrelloAction_SkipsWhenNoCredentials(t *testing.T) {
	node := models.WorkflowNode{
		ID: "tr2", Type: models.NodeTypeAction, Template: "trello",
		Config: map[string]string{"trelloListID": "list789"},
	}
	rc := engine.NewRunContext("r1", []byte(`"ship the release"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "trello_skipped_no_credentials" {
		t.Errorf("want 'trello_skipped_no_credentials', got %v", result)
	}
}

func TestTrelloAction_SkipsWhenNoListID(t *testing.T) {
	node := models.WorkflowNode{
		ID: "tr3", Type: models.NodeTypeAction, Template: "trello",
		Secrets: map[string]string{"trelloAPIKey": "key123", "trelloToken": "tok456"},
	}
	rc := engine.NewRunContext("r1", []byte(`"ship the release"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "trello_skipped_no_list_id" {
		t.Errorf("want 'trello_skipped_no_list_id', got %v", result)
	}
}

func TestAsanaAction_CreatesTask(t *testing.T) {
	var gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	nodes.SetAsanaAPIBaseForTest(srv.URL)
	defer nodes.SetAsanaAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "as1", Type: models.NodeTypeAction, Template: "asana",
		Secrets: map[string]string{"asanaAPIKey": "1/xxx"},
		Config:  map[string]string{"asanaProjectID": "proj123"},
	}
	rc := engine.NewRunContext("r1", []byte(`"review pull request"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "asana_task_created" {
		t.Errorf("want 'asana_task_created', got %v", result)
	}
	if gotAuth != "Bearer 1/xxx" {
		t.Errorf("want bearer auth, got %q", gotAuth)
	}
	data, _ := gotBody["data"].(map[string]any)
	if data["name"] != "review pull request" {
		t.Errorf("want task name from message, got %v", gotBody)
	}
	projects, _ := data["projects"].([]any)
	if len(projects) != 1 || projects[0] != "proj123" {
		t.Errorf("want project id in projects, got %v", data["projects"])
	}
}

func TestAsanaAction_PrefersOAuthTokenOverManualToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	nodes.SetAsanaAPIBaseForTest(srv.URL)
	defer nodes.SetAsanaAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "as4", Type: models.NodeTypeAction, Template: "asana",
		Secrets: map[string]string{"asanaAPIKey": "1/xxx", "asanaOAuthAccessToken": "oauth-derived-token"},
		Config:  map[string]string{"asanaProjectID": "proj123"},
	}
	rc := engine.NewRunContext("r1", []byte(`"review pull request"`))
	_, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer oauth-derived-token" {
		t.Errorf("want OAuth token in Authorization header, got %q", gotAuth)
	}
}

func TestAsanaAction_SkipsWhenNoAPIKey(t *testing.T) {
	node := models.WorkflowNode{
		ID: "as2", Type: models.NodeTypeAction, Template: "asana",
		Config: map[string]string{"asanaProjectID": "proj123"},
	}
	rc := engine.NewRunContext("r1", []byte(`"review pull request"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "asana_skipped_no_api_key" {
		t.Errorf("want 'asana_skipped_no_api_key', got %v", result)
	}
}

func TestAsanaAction_SkipsWhenNoProjectID(t *testing.T) {
	node := models.WorkflowNode{
		ID: "as3", Type: models.NodeTypeAction, Template: "asana",
		Secrets: map[string]string{"asanaAPIKey": "1/xxx"},
	}
	rc := engine.NewRunContext("r1", []byte(`"review pull request"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "asana_skipped_no_project_id" {
		t.Errorf("want 'asana_skipped_no_project_id', got %v", result)
	}
}

func TestClickUpAction_CreatesTask(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	nodes.SetClickUpAPIBaseForTest(srv.URL)
	defer nodes.SetClickUpAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "cu1", Type: models.NodeTypeAction, Template: "clickup",
		Secrets: map[string]string{"clickupAPIKey": "pk_xxx"},
		Config:  map[string]string{"clickupListID": "list42"},
	}
	rc := engine.NewRunContext("r1", []byte(`"triage bug reports"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "clickup_task_created" {
		t.Errorf("want 'clickup_task_created', got %v", result)
	}
	if gotPath != "/api/v2/list/list42/task" {
		t.Errorf("want list task path, got %q", gotPath)
	}
	if gotAuth != "pk_xxx" {
		t.Errorf("want raw token (no Bearer prefix), got %q", gotAuth)
	}
	if gotBody["name"] != "triage bug reports" {
		t.Errorf("want task name from message, got %v", gotBody)
	}
}

func TestClickUpAction_PrefersOAuthTokenOverManualToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	nodes.SetClickUpAPIBaseForTest(srv.URL)
	defer nodes.SetClickUpAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "cu4", Type: models.NodeTypeAction, Template: "clickup",
		Secrets: map[string]string{"clickupAPIKey": "pk_xxx", "clickupOAuthAccessToken": "oauth-derived-token"},
		Config:  map[string]string{"clickupListID": "list42"},
	}
	rc := engine.NewRunContext("r1", []byte(`"triage bug reports"`))
	_, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	// The whole point of this test: ClickUp's OAuth tokens need "Bearer ",
	// unlike the raw manual personal-token header asserted in
	// TestClickUpAction_CreatesTask above — these two paths use different
	// header formats on the very same endpoint, so both must be pinned
	// exactly, not just "contains the token".
	if gotAuth != "Bearer oauth-derived-token" {
		t.Errorf("want Bearer-prefixed OAuth token in Authorization header, got %q", gotAuth)
	}
}

func TestClickUpAction_SkipsWhenNoAPIKey(t *testing.T) {
	node := models.WorkflowNode{
		ID: "cu2", Type: models.NodeTypeAction, Template: "clickup",
		Config: map[string]string{"clickupListID": "list42"},
	}
	rc := engine.NewRunContext("r1", []byte(`"triage bug reports"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "clickup_skipped_no_api_key" {
		t.Errorf("want 'clickup_skipped_no_api_key', got %v", result)
	}
}

func TestClickUpAction_SkipsWhenNoListID(t *testing.T) {
	node := models.WorkflowNode{
		ID: "cu3", Type: models.NodeTypeAction, Template: "clickup",
		Secrets: map[string]string{"clickupAPIKey": "pk_xxx"},
	}
	rc := engine.NewRunContext("r1", []byte(`"triage bug reports"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "clickup_skipped_no_list_id" {
		t.Errorf("want 'clickup_skipped_no_list_id', got %v", result)
	}
}

func TestTodoistAction_CreatesTask(t *testing.T) {
	var gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	nodes.SetTodoistAPIBaseForTest(srv.URL)
	defer nodes.SetTodoistAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "td1", Type: models.NodeTypeAction, Template: "todoist",
		Secrets: map[string]string{"todoistAPIKey": "tok_xxx"},
		Config:  map[string]string{"todoistProjectID": "proj42"},
	}
	rc := engine.NewRunContext("r1", []byte(`"buy milk"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "todoist_task_created" {
		t.Errorf("want 'todoist_task_created', got %v", result)
	}
	if gotAuth != "Bearer tok_xxx" {
		t.Errorf("want bearer auth, got %q", gotAuth)
	}
	if gotBody["content"] != "buy milk" {
		t.Errorf("want content from message, got %v", gotBody)
	}
	if gotBody["project_id"] != "proj42" {
		t.Errorf("want project_id when configured, got %v", gotBody)
	}
}

func TestTodoistAction_OmitsProjectIDWhenUnset(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	nodes.SetTodoistAPIBaseForTest(srv.URL)
	defer nodes.SetTodoistAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "td2", Type: models.NodeTypeAction, Template: "todoist",
		Secrets: map[string]string{"todoistAPIKey": "tok_xxx"},
	}
	rc := engine.NewRunContext("r1", []byte(`"buy eggs"`))
	if _, err := nodes.ExecuteAction(context.Background(), node, rc); err != nil {
		t.Fatal(err)
	}
	if _, ok := gotBody["project_id"]; ok {
		t.Errorf("want project_id omitted when unset, got %v", gotBody)
	}
}

func TestTodoistAction_SkipsWhenNoAPIKey(t *testing.T) {
	node := models.WorkflowNode{
		ID: "td3", Type: models.NodeTypeAction, Template: "todoist",
	}
	rc := engine.NewRunContext("r1", []byte(`"buy eggs"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "todoist_skipped_no_api_key" {
		t.Errorf("want 'todoist_skipped_no_api_key', got %v", result)
	}
}

func TestTodoistAction_PrefersOAuthTokenOverManualToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	nodes.SetTodoistAPIBaseForTest(srv.URL)
	defer nodes.SetTodoistAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "td4", Type: models.NodeTypeAction, Template: "todoist",
		Secrets: map[string]string{"todoistAPIKey": "manual-secret-xxx", "todoistOAuthAccessToken": "oauth-derived-token"},
	}
	rc := engine.NewRunContext("r1", []byte(`"buy milk"`))
	_, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer oauth-derived-token" {
		t.Errorf("want OAuth token in Authorization header, got %q", gotAuth)
	}
}
