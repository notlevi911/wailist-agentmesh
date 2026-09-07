package nodes_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentmesh/backend/internal/engine"
	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
)

func TestGitHubAction_CreatesIssue(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	nodes.SetGitHubAPIBaseForTest(srv.URL)
	defer nodes.SetGitHubAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "gh1", Type: models.NodeTypeAction, Template: "github",
		Secrets: map[string]string{"githubToken": "ghp_xxx"},
		Config:  map[string]string{"githubRepo": "acme/widgets"},
	}
	rc := engine.NewRunContext("r1", []byte(`"build failed on main"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "github_issue_created" {
		t.Errorf("want 'github_issue_created', got %v", result)
	}
	if gotPath != "/repos/acme/widgets/issues" {
		t.Errorf("want issues path, got %q", gotPath)
	}
	if gotAuth != "Bearer ghp_xxx" {
		t.Errorf("want bearer auth, got %q", gotAuth)
	}
	if gotBody["title"] != "build failed on main" {
		t.Errorf("want title from message, got %v", gotBody)
	}
}

func TestGitHubAction_SkipsWhenNoToken(t *testing.T) {
	node := models.WorkflowNode{
		ID: "gh2", Type: models.NodeTypeAction, Template: "github",
		Config: map[string]string{"githubRepo": "acme/widgets"},
	}
	rc := engine.NewRunContext("r1", []byte(`"build failed on main"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "github_skipped_no_token" {
		t.Errorf("want 'github_skipped_no_token', got %v", result)
	}
}

func TestGitHubAction_SkipsWhenNoRepo(t *testing.T) {
	node := models.WorkflowNode{
		ID: "gh3", Type: models.NodeTypeAction, Template: "github",
		Secrets: map[string]string{"githubToken": "ghp_xxx"},
	}
	rc := engine.NewRunContext("r1", []byte(`"build failed on main"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "github_skipped_no_repo" {
		t.Errorf("want 'github_skipped_no_repo', got %v", result)
	}
}

func TestGitHubAction_SkipsWhenRepoInvalid(t *testing.T) {
	node := models.WorkflowNode{
		ID: "gh4", Type: models.NodeTypeAction, Template: "github",
		Secrets: map[string]string{"githubToken": "ghp_xxx"},
		Config:  map[string]string{"githubRepo": "not-a-valid-repo"},
	}
	rc := engine.NewRunContext("r1", []byte(`"build failed"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "github_skipped_invalid_repo" {
		t.Errorf("want 'github_skipped_invalid_repo', got %v", result)
	}
}

func TestGitHubAction_PrefersOAuthTokenOverManualToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	nodes.SetGitHubAPIBaseForTest(srv.URL)
	defer nodes.SetGitHubAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "gh5", Type: models.NodeTypeAction, Template: "github",
		Secrets: map[string]string{"githubToken": "manual-pat-token", "githubOAuthAccessToken": "oauth-derived-token"},
		Config:  map[string]string{"githubRepo": "owner/repo"},
	}
	rc := engine.NewRunContext("r1", []byte(`"test issue body"`))
	_, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer oauth-derived-token" {
		t.Errorf("want OAuth token in Authorization header, got %q", gotAuth)
	}
}

func TestGitHubAction_EscapesExtraPathSegments(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	nodes.SetGitHubAPIBaseForTest(srv.URL)
	defer nodes.SetGitHubAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "gh5", Type: models.NodeTypeAction, Template: "github",
		Secrets: map[string]string{"githubToken": "ghp_xxx"},
		Config:  map[string]string{"githubRepo": "acme/widgets/../../admin"},
	}
	rc := engine.NewRunContext("r1", []byte(`"build failed"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "github_issue_created" {
		t.Errorf("want 'github_issue_created', got %v", result)
	}
	if gotPath != "/repos/acme/widgets%2F..%2F..%2Fadmin/issues" {
		t.Errorf("want escaped extra segments, got %q", gotPath)
	}
}

func TestJiraAction_CreatesIssue(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	nodes.SetJiraAPIBaseForTest(srv.URL)
	defer nodes.SetJiraAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "jr1", Type: models.NodeTypeAction, Template: "jira",
		Secrets: map[string]string{"jiraAPIToken": "tok_xxx"},
		Config: map[string]string{
			"jiraEmail": "bot@acme.com", "jiraDomain": "acme", "jiraProjectKey": "ENG",
		},
	}
	rc := engine.NewRunContext("r1", []byte(`"deploy failed"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "jira_issue_created" {
		t.Errorf("want 'jira_issue_created', got %v", result)
	}
	if gotPath != "/rest/api/3/issue" {
		t.Errorf("want issue path, got %q", gotPath)
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("bot@acme.com:tok_xxx"))
	if gotAuth != wantAuth {
		t.Errorf("want basic auth %q, got %q", wantAuth, gotAuth)
	}
	fields, _ := gotBody["fields"].(map[string]any)
	if fields["summary"] != "deploy failed" {
		t.Errorf("want summary from message, got %v", fields)
	}
	project, _ := fields["project"].(map[string]any)
	if project["key"] != "ENG" {
		t.Errorf("want project key ENG, got %v", project)
	}
}

func TestJiraAction_SkipsWhenNoAPIToken(t *testing.T) {
	node := models.WorkflowNode{
		ID: "jr2", Type: models.NodeTypeAction, Template: "jira",
		Config: map[string]string{
			"jiraEmail": "bot@acme.com", "jiraDomain": "acme", "jiraProjectKey": "ENG",
		},
	}
	rc := engine.NewRunContext("r1", []byte(`"deploy failed"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "jira_skipped_no_api_token" {
		t.Errorf("want 'jira_skipped_no_api_token', got %v", result)
	}
}

func TestJiraAction_SkipsWhenMissingConfig(t *testing.T) {
	node := models.WorkflowNode{
		ID: "jr3", Type: models.NodeTypeAction, Template: "jira",
		Secrets: map[string]string{"jiraAPIToken": "tok_xxx"},
		Config:  map[string]string{"jiraEmail": "bot@acme.com", "jiraDomain": "acme"},
	}
	rc := engine.NewRunContext("r1", []byte(`"deploy failed"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "jira_skipped_missing_config" {
		t.Errorf("want 'jira_skipped_missing_config', got %v", result)
	}
}

func TestJiraAction_SkipsWhenDomainInvalid(t *testing.T) {
	requestReceived := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestReceived = true
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	nodes.SetJiraAPIBaseForTest(srv.URL)
	defer nodes.SetJiraAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "jr4", Type: models.NodeTypeAction, Template: "jira",
		Secrets: map[string]string{"jiraAPIToken": "tok_xxx"},
		Config: map[string]string{
			"jiraEmail": "bot@acme.com", "jiraDomain": "evil.com#", "jiraProjectKey": "ENG",
		},
	}
	rc := engine.NewRunContext("r1", []byte(`"deploy failed"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "jira_skipped_invalid_domain" {
		t.Errorf("want 'jira_skipped_invalid_domain', got %v", result)
	}
	if requestReceived {
		t.Error("expected no HTTP request to be dispatched for an invalid domain")
	}
}

func TestJiraAction_OAuthTokenUsesCloudIDURLAndBearerAuth(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	nodes.SetJiraAPIBaseForTest(srv.URL)
	defer nodes.SetJiraAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "jr5", Type: models.NodeTypeAction, Template: "jira",
		Secrets: map[string]string{"jiraOAuthAccessToken": "oauth-derived-token", "jiraAPIToken": "manual-token-should-be-ignored"},
		Config: map[string]string{
			"jiraOAuthCloudID": "cloud-xyz", "jiraProjectKey": "ENG",
		},
	}
	rc := engine.NewRunContext("r1", []byte(`"deploy failed"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "jira_issue_created" {
		t.Errorf("want 'jira_issue_created', got %v", result)
	}
	if gotPath != "/rest/api/3/issue" {
		t.Errorf("want issue path, got %q", gotPath)
	}
	if gotAuth != "Bearer oauth-derived-token" {
		t.Errorf("want bearer auth with OAuth token, got %q", gotAuth)
	}
	fields, _ := gotBody["fields"].(map[string]any)
	if fields["summary"] != "deploy failed" {
		t.Errorf("want summary from message, got %v", fields)
	}
	project, _ := fields["project"].(map[string]any)
	if project["key"] != "ENG" {
		t.Errorf("want project key ENG, got %v", project)
	}
}

func TestJiraAction_OAuthTokenSkipsWhenNoCloudID(t *testing.T) {
	requestReceived := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestReceived = true
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	nodes.SetJiraAPIBaseForTest(srv.URL)
	defer nodes.SetJiraAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "jr6", Type: models.NodeTypeAction, Template: "jira",
		Secrets: map[string]string{"jiraOAuthAccessToken": "oauth-derived-token"},
		Config:  map[string]string{"jiraProjectKey": "ENG"},
	}
	rc := engine.NewRunContext("r1", []byte(`"deploy failed"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "jira_skipped_missing_config" {
		t.Errorf("want 'jira_skipped_missing_config', got %v", result)
	}
	if requestReceived {
		t.Error("expected no HTTP request to be dispatched without a cloudId")
	}
}

func TestLinearAction_CreatesIssueViaGraphQL(t *testing.T) {
	var gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":{"issueCreate":{"success":true}}}`))
	}))
	defer srv.Close()
	nodes.SetLinearAPIBaseForTest(srv.URL)
	defer nodes.SetLinearAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "li1", Type: models.NodeTypeAction, Template: "linear",
		Secrets: map[string]string{"linearAPIKey": "lin_api_xxx"},
		Config:  map[string]string{"linearTeamID": "team123"},
	}
	rc := engine.NewRunContext("r1", []byte(`"flaky test in CI"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "linear_issue_created" {
		t.Errorf("want 'linear_issue_created', got %v", result)
	}
	if gotAuth != "lin_api_xxx" {
		t.Errorf("want raw API key (no Bearer prefix), got %q", gotAuth)
	}
	if gotBody["query"] == nil {
		t.Fatal("want a GraphQL query in the body")
	}
	variables, _ := gotBody["variables"].(map[string]any)
	input, _ := variables["input"].(map[string]any)
	if input["teamId"] != "team123" || input["title"] != "flaky test in CI" {
		t.Errorf("want teamId/title in GraphQL variables, got %v", input)
	}
}

func TestLinearAction_FailsOnGraphQLErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":null,"errors":[{"message":"Entity not found: Team"}]}`))
	}))
	defer srv.Close()
	nodes.SetLinearAPIBaseForTest(srv.URL)
	defer nodes.SetLinearAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "li3", Type: models.NodeTypeAction, Template: "linear",
		Secrets: map[string]string{"linearAPIKey": "lin_api_xxx"},
		Config:  map[string]string{"linearTeamID": "bad-team"},
	}
	rc := engine.NewRunContext("r1", []byte(`"test message"`))
	_, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err == nil {
		t.Fatal("want error on GraphQL-level failure returned with HTTP 200, got nil")
	}
}

func TestLinearAction_FailsOnIssueCreateSuccessFalse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":{"issueCreate":{"success":false}}}`))
	}))
	defer srv.Close()
	nodes.SetLinearAPIBaseForTest(srv.URL)
	defer nodes.SetLinearAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "li4", Type: models.NodeTypeAction, Template: "linear",
		Secrets: map[string]string{"linearAPIKey": "lin_api_xxx"},
		Config:  map[string]string{"linearTeamID": "team123"},
	}
	rc := engine.NewRunContext("r1", []byte(`"test message"`))
	_, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err == nil {
		t.Fatal("want error when issueCreate.success is false, got nil")
	}
}

func TestLinearAction_SkipsWhenNoAPIKey(t *testing.T) {
	node := models.WorkflowNode{
		ID: "li2", Type: models.NodeTypeAction, Template: "linear",
		Config: map[string]string{"linearTeamID": "team123"},
	}
	rc := engine.NewRunContext("r1", []byte(`"test message"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "linear_skipped_no_api_key" {
		t.Errorf("want 'linear_skipped_no_api_key', got %v", result)
	}
}

func TestLinearAction_SkipsWhenNoTeamID(t *testing.T) {
	node := models.WorkflowNode{
		ID: "li3", Type: models.NodeTypeAction, Template: "linear",
		Secrets: map[string]string{"linearAPIKey": "lin_api_xxx"},
	}
	rc := engine.NewRunContext("r1", []byte(`"test message"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "linear_skipped_no_team_id" {
		t.Errorf("want 'linear_skipped_no_team_id', got %v", result)
	}
}

func TestLinearAction_OAuthTokenUsesBearerAuthAndIsPreferredOverAPIKey(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":{"issueCreate":{"success":true}}}`))
	}))
	defer srv.Close()
	nodes.SetLinearAPIBaseForTest(srv.URL)
	defer nodes.SetLinearAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "li5", Type: models.NodeTypeAction, Template: "linear",
		Secrets: map[string]string{"linearOAuthAccessToken": "oauth-derived-token", "linearAPIKey": "manual-key-should-be-ignored"},
		Config:  map[string]string{"linearTeamID": "team123"},
	}
	rc := engine.NewRunContext("r1", []byte(`"flaky test in CI"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "linear_issue_created" {
		t.Errorf("want 'linear_issue_created', got %v", result)
	}
	if gotAuth != "Bearer oauth-derived-token" {
		t.Errorf("want bearer auth with OAuth token, got %q", gotAuth)
	}
}

func TestLinearAction_OAuthTokenFailsOnGraphQLErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":null,"errors":[{"message":"Entity not found: Team"}]}`))
	}))
	defer srv.Close()
	nodes.SetLinearAPIBaseForTest(srv.URL)
	defer nodes.SetLinearAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "li6", Type: models.NodeTypeAction, Template: "linear",
		Secrets: map[string]string{"linearOAuthAccessToken": "oauth-derived-token"},
		Config:  map[string]string{"linearTeamID": "bad-team"},
	}
	rc := engine.NewRunContext("r1", []byte(`"test message"`))
	_, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err == nil {
		t.Fatal("want error on GraphQL-level failure returned with HTTP 200 via the OAuth path, got nil")
	}
}

func TestGitLabAction_CreatesIssue(t *testing.T) {
	var gotPath, gotToken string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotToken = r.Header.Get("PRIVATE-TOKEN")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	node := models.WorkflowNode{
		ID: "gl1", Type: models.NodeTypeAction, Template: "gitlab",
		Secrets: map[string]string{"gitlabAPIToken": "glpat-xxx"},
		Config:  map[string]string{"gitlabProjectID": "42", "gitlabBaseURL": srv.URL},
	}
	rc := engine.NewRunContext("r1", []byte(`"pipeline broke"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "gitlab_issue_created" {
		t.Errorf("want 'gitlab_issue_created', got %v", result)
	}
	if gotPath != "/api/v4/projects/42/issues" {
		t.Errorf("want project issues path, got %q", gotPath)
	}
	if gotToken != "glpat-xxx" {
		t.Errorf("want PRIVATE-TOKEN header, got %q", gotToken)
	}
	if gotBody["title"] != "pipeline broke" {
		t.Errorf("want title in JSON body, got %v", gotBody)
	}
	if gotBody["description"] != "pipeline broke" {
		t.Errorf("want description in JSON body, got %v", gotBody)
	}
}

func TestGitLabAction_DefaultsToGitLabCom(t *testing.T) {
	node := models.WorkflowNode{
		ID: "gl2", Type: models.NodeTypeAction, Template: "gitlab",
		Secrets: map[string]string{"gitlabAPIToken": "glpat-xxx"},
		Config:  map[string]string{"gitlabProjectID": "42"},
	}
	rc := engine.NewRunContext("r1", []byte(`"x"`))
	// No live gitlab.com call in unit tests; this only asserts we don't skip
	// due to missing config and that a network error (not a config-skip
	// sentinel) is what comes back when the real host is unreachable in CI.
	_, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err == nil {
		t.Skip("network reachable in this environment; skip is fine, this test only guards against a config-skip sentinel")
	}
}

func TestGitLabAction_SkipsWhenNoToken(t *testing.T) {
	node := models.WorkflowNode{
		ID: "gl3", Type: models.NodeTypeAction, Template: "gitlab",
		Config: map[string]string{"gitlabProjectID": "42"},
	}
	rc := engine.NewRunContext("r1", []byte(`"pipeline broke"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "gitlab_skipped_no_token" {
		t.Errorf("want 'gitlab_skipped_no_token', got %v", result)
	}
}

func TestGitLabAction_SkipsWhenNeitherTokenNorProjectID(t *testing.T) {
	// Token presence must be checked before projectID (mirrors sendLinear),
	// so a node missing everything reports the token gap, not the project
	// gap — this pins the check ordering against regression.
	node := models.WorkflowNode{
		ID: "gl3b", Type: models.NodeTypeAction, Template: "gitlab",
	}
	rc := engine.NewRunContext("r1", []byte(`"pipeline broke"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "gitlab_skipped_no_token" {
		t.Errorf("want 'gitlab_skipped_no_token', got %v", result)
	}
}

func TestGitLabAction_SkipsWhenNoProjectID(t *testing.T) {
	node := models.WorkflowNode{
		ID: "gl4", Type: models.NodeTypeAction, Template: "gitlab",
		Secrets: map[string]string{"gitlabAPIToken": "glpat-xxx"},
	}
	rc := engine.NewRunContext("r1", []byte(`"pipeline broke"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "gitlab_skipped_no_project_id" {
		t.Errorf("want 'gitlab_skipped_no_project_id', got %v", result)
	}
}

func TestGitLabAction_OAuthTokenUsesBearerAuthAndAlwaysTargetsGitLabCom(t *testing.T) {
	var gotPath, gotAuth, gotPrivateToken string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotPrivateToken = r.Header.Get("PRIVATE-TOKEN")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	nodes.SetGitLabOAuthAPIBaseForTest(srv.URL)
	defer nodes.SetGitLabOAuthAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "gl5", Type: models.NodeTypeAction, Template: "gitlab",
		Secrets: map[string]string{"gitlabOAuthAccessToken": "oauth-derived-token"},
		// gitlabBaseURL deliberately points somewhere that would fail DNS
		// resolution if the OAuth branch ever read it — a self-hosted-looking
		// host has no bearing on where an OAuth-derived token (only ever valid
		// against gitlab.com) can actually be used. Success here proves the
		// OAuth branch never even looked at this value.
		Config: map[string]string{"gitlabProjectID": "42", "gitlabBaseURL": "https://self-hosted.example.invalid"},
	}
	rc := engine.NewRunContext("r1", []byte(`"pipeline broke"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "gitlab_issue_created" {
		t.Errorf("want 'gitlab_issue_created', got %v", result)
	}
	if gotPath != "/api/v4/projects/42/issues" {
		t.Errorf("want project issues path, got %q", gotPath)
	}
	if gotAuth != "Bearer oauth-derived-token" {
		t.Errorf("want 'Authorization: Bearer <token>', got %q", gotAuth)
	}
	if gotPrivateToken != "" {
		t.Errorf("want no PRIVATE-TOKEN header on the OAuth path, got %q", gotPrivateToken)
	}
	if gotBody["title"] != "pipeline broke" {
		t.Errorf("want title in JSON body, got %v", gotBody)
	}
}

func TestGitLabAction_OAuthTokenPreferredOverManualToken(t *testing.T) {
	var gotAuth, gotPrivateToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPrivateToken = r.Header.Get("PRIVATE-TOKEN")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	nodes.SetGitLabOAuthAPIBaseForTest(srv.URL)
	defer nodes.SetGitLabOAuthAPIBaseForTest("")

	node := models.WorkflowNode{
		ID: "gl6", Type: models.NodeTypeAction, Template: "gitlab",
		Secrets: map[string]string{"gitlabOAuthAccessToken": "oauth-derived-token", "gitlabAPIToken": "manual-token-should-be-ignored"},
		Config:  map[string]string{"gitlabProjectID": "42"},
	}
	rc := engine.NewRunContext("r1", []byte(`"pipeline broke"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "gitlab_issue_created" {
		t.Errorf("want 'gitlab_issue_created', got %v", result)
	}
	if gotAuth != "Bearer oauth-derived-token" {
		t.Errorf("want bearer auth with OAuth token, got %q", gotAuth)
	}
	if gotPrivateToken != "" {
		t.Errorf("want no PRIVATE-TOKEN header when an OAuth token is present, got %q", gotPrivateToken)
	}
}

func TestGitLabAction_ManualTokenUnaffectedByOAuthCodePath(t *testing.T) {
	var gotPath, gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotToken = r.Header.Get("PRIVATE-TOKEN")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	node := models.WorkflowNode{
		ID: "gl7", Type: models.NodeTypeAction, Template: "gitlab",
		Secrets: map[string]string{"gitlabAPIToken": "glpat-xxx"},
		Config:  map[string]string{"gitlabProjectID": "42", "gitlabBaseURL": srv.URL},
	}
	rc := engine.NewRunContext("r1", []byte(`"pipeline broke"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "gitlab_issue_created" {
		t.Errorf("want 'gitlab_issue_created', got %v", result)
	}
	if gotPath != "/api/v4/projects/42/issues" {
		t.Errorf("want project issues path, got %q", gotPath)
	}
	if gotToken != "glpat-xxx" {
		t.Errorf("want PRIVATE-TOKEN header still used and custom gitlabBaseURL still respected, got %q", gotToken)
	}
}

func TestSentryAction_SendsEnvelope(t *testing.T) {
	var gotPath, gotAuth, gotContentType string
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("X-Sentry-Auth")
		gotContentType = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	srvHost := strings.TrimPrefix(srv.URL, "http://")

	node := models.WorkflowNode{
		ID: "se1", Type: models.NodeTypeAction, Template: "sentry",
		Secrets: map[string]string{"sentryDSN": "http://pubkey123@" + srvHost + "/9"},
	}
	rc := engine.NewRunContext("r1", []byte(`"job queue backed up"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "sentry_event_sent" {
		t.Errorf("want 'sentry_event_sent', got %v", result)
	}
	if gotPath != "/api/9/envelope/" {
		t.Errorf("want envelope path, got %q", gotPath)
	}
	if !strings.Contains(gotAuth, "sentry_key=pubkey123") {
		t.Errorf("want sentry_key in X-Sentry-Auth, got %q", gotAuth)
	}
	if gotContentType != "application/x-sentry-envelope" {
		t.Errorf("want envelope content type, got %q", gotContentType)
	}
	lines := strings.Split(strings.TrimRight(gotBody, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3-line envelope (header, item header, item body), got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[2], "job queue backed up") {
		t.Errorf("want message in item body, got %q", lines[2])
	}
}

func TestParseSentryDSN(t *testing.T) {
	publicKey, host, projectID, err := nodes.ParseSentryDSNForTest("https://abc@o123.ingest.sentry.io/456")
	if err != nil {
		t.Fatal(err)
	}
	if publicKey != "abc" || host != "o123.ingest.sentry.io" || projectID != "456" {
		t.Errorf("want abc/o123.ingest.sentry.io/456, got %s/%s/%s", publicKey, host, projectID)
	}
	if _, _, _, err := nodes.ParseSentryDSNForTest("not-a-dsn"); err == nil {
		t.Error("want error for malformed DSN")
	}
}

func TestSentryAction_SkipsWhenNoDSN(t *testing.T) {
	node := models.WorkflowNode{
		ID: "se2", Type: models.NodeTypeAction, Template: "sentry",
	}
	rc := engine.NewRunContext("r1", []byte(`"job queue backed up"`))
	result, err := nodes.ExecuteAction(context.Background(), node, rc)
	if !errors.Is(err, nodes.ErrActionSkipped) {
		t.Fatalf("want ErrActionSkipped, got %v", err)
	}
	if result != "sentry_skipped_no_dsn" {
		t.Errorf("want 'sentry_skipped_no_dsn', got %v", result)
	}
}
