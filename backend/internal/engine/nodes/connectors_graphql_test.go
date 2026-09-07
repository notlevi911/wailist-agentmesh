package nodes_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentmesh/backend/internal/engine"
	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
)

func TestGraphQLAction_PostsQueryAndReturnsData(t *testing.T) {
	var gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"viewer":{"login":"octocat"}}}`))
	}))
	defer srv.Close()

	node := models.WorkflowNode{
		ID: "gq1", Type: models.NodeTypeAction, Template: "graphql",
		Secrets: map[string]string{"graphqlAuthHeader": "Bearer ghp_xxx"},
		Config: map[string]string{
			"graphqlEndpoint":  srv.URL,
			"graphqlQuery":     "query { viewer { login } }",
			"graphqlVariables": `{"first":10,"search":"{{ input }}"}`,
		},
	}
	rc := engine.NewRunContext("r1", []byte(`"octo"`))

	out, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer ghp_xxx" {
		t.Errorf("auth header should be sent verbatim, got %q", gotAuth)
	}
	if gotBody["query"] != "query { viewer { login } }" {
		t.Errorf("query: got %v", gotBody["query"])
	}
	vars := gotBody["variables"].(map[string]any)
	if vars["first"] != float64(10) {
		t.Errorf("non-string variables must survive unchanged, got %#v", vars["first"])
	}
	if vars["search"] != "octo" {
		t.Errorf("string variables should resolve templates, got %v", vars["search"])
	}
	// The decoded response is the node's output, not a sentinel.
	res, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("want the decoded response, got %T", out)
	}
	data := res["data"].(map[string]any)
	if data["viewer"].(map[string]any)["login"] != "octocat" {
		t.Errorf("got %#v", res)
	}
}

// A GraphQL server returns 200 with an "errors" array rather than an HTTP
// error status. Silently succeeding on that would be the db-node bug again.
func TestGraphQLAction_SurfacesGraphQLErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"errors":[{"message":"Field 'nope' doesn't exist"}]}`))
	}))
	defer srv.Close()

	node := models.WorkflowNode{
		ID: "gq1", Type: models.NodeTypeAction, Template: "graphql",
		Config: map[string]string{"graphqlEndpoint": srv.URL, "graphqlQuery": "query { nope }"},
	}
	rc := engine.NewRunContext("r1", nil)
	_, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err == nil {
		t.Fatal("want an error when the response carries a GraphQL errors array, got nil")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error should quote the server's message, got %v", err)
	}
}

func TestGraphQLAction_SkipsWhenUnconfigured(t *testing.T) {
	rc := engine.NewRunContext("r1", nil)
	noEndpoint := models.WorkflowNode{Type: models.NodeTypeAction, Template: "graphql",
		Config: map[string]string{"graphqlQuery": "query { x }"}}
	if got, _ := nodes.ExecuteAction(context.Background(), noEndpoint, rc); got != "graphql_skipped_no_endpoint" {
		t.Errorf("want skip sentinel, got %v", got)
	}
	noQuery := models.WorkflowNode{Type: models.NodeTypeAction, Template: "graphql",
		Config: map[string]string{"graphqlEndpoint": "https://api.example.com/graphql"}}
	if got, _ := nodes.ExecuteAction(context.Background(), noQuery, rc); got != "graphql_skipped_no_query" {
		t.Errorf("want skip sentinel, got %v", got)
	}
}

func TestGraphQLAction_RejectsMalformedVariables(t *testing.T) {
	node := models.WorkflowNode{
		Type: models.NodeTypeAction, Template: "graphql",
		Config: map[string]string{
			"graphqlEndpoint":  "https://api.example.com/graphql",
			"graphqlQuery":     "query { x }",
			"graphqlVariables": `{"broken": }`,
		},
	}
	rc := engine.NewRunContext("r1", nil)
	if _, err := nodes.ExecuteAction(context.Background(), node, rc); err == nil {
		t.Error("want an error for malformed graphqlVariables, got nil")
	}
}
