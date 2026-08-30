package engine_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentmesh/backend/internal/db"
	"github.com/agentmesh/backend/internal/models"
)

// fundedWorkflow creates a real user with credits and a workflow owned by
// them. An HTTP tool node is billable, so it is blocked by preflightCheck
// before it ever makes its request if the owner has no balance — these
// tests are about field expansion, not billing, so they need a funded
// owner to reach the outbound call at all.
func fundedWorkflow(t *testing.T, store *db.Store, name string) models.Workflow {
	t.Helper()
	ctx := context.Background()

	email := fmt.Sprintf("state-expand-%s-%d@example.com", name, time.Now().UnixNano())
	user, err := store.CreateUser(ctx, email, "hash")
	if err != nil {
		t.Fatal(err)
	}
	fundUser(t, store, user.ID, 1000000) // $1, far more than a few flat fees

	wf, err := store.CreateWorkflow(ctx, name, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(ctx, wf.ID) })
	wf.UserID = user.ID
	return wf
}

// A tool node's URL containing {{state.x}} is resolved from the workflow's
// persisted variables before the request goes out.
func TestStateExpandsInToolURL(t *testing.T) {
	runner, store := newTestRunner(t)
	ctx := context.Background()

	gotPath := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case gotPath <- r.URL.RequestURI():
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)

	wf := fundedWorkflow(t, store, "StateExpand")

	if err := store.SetWorkflowVariable(ctx, wf.ID, "cursor", []byte(`"abc123"`)); err != nil {
		t.Fatal(err)
	}

	wf.Nodes = []models.WorkflowNode{
		{ID: "t1", Type: models.NodeTypeTrigger},
		{ID: "h1", Type: models.NodeTypeTool, Template: "http",
			URL: srv.URL + "/items?after={{state.cursor}}", Method: http.MethodGet},
	}
	wf.Edges = []models.WorkflowEdge{
		{ID: "x1", From: "t1", To: "h1", Kind: models.EdgeKindFlow},
	}

	run, _ := store.CreateRun(ctx, wf.ID, "manual", []byte(`{"message":"go"}`))
	runner.Run(ctx, wf, run)

	select {
	case p := <-gotPath:
		if p != "/items?after=abc123" {
			t.Fatalf("state was not expanded into the URL: got %q", p)
		}
	default:
		t.Fatal("the tool node never called the server")
	}
}

// A workflow with no state and no placeholders must produce a byte-identical
// request — the regression guard for every workflow that exists today.
func TestNoStateLeavesFieldsUntouched(t *testing.T) {
	runner, store := newTestRunner(t)
	ctx := context.Background()

	gotPath := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case gotPath <- r.URL.RequestURI():
		default:
		}
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	wf := fundedWorkflow(t, store, "NoState")

	wf.Nodes = []models.WorkflowNode{
		{ID: "t1", Type: models.NodeTypeTrigger},
		{ID: "h1", Type: models.NodeTypeTool, Template: "http",
			URL: srv.URL + "/items?q={{not.state}}", Method: http.MethodGet},
	}
	wf.Edges = []models.WorkflowEdge{
		{ID: "x1", From: "t1", To: "h1", Kind: models.EdgeKindFlow},
	}

	run, _ := store.CreateRun(ctx, wf.ID, "manual", []byte(`{"message":"go"}`))
	runner.Run(ctx, wf, run)

	select {
	case p := <-gotPath:
		if p != "/items?q={{not.state}}" {
			t.Fatalf("a non-state placeholder must pass through verbatim: got %q", p)
		}
	default:
		t.Fatal("the tool node never called the server")
	}
}

// Expanding a node must not mutate the workflow graph: a second run reads
// the original template, not the first run's substituted value.
func TestStateExpansionDoesNotMutateTheGraph(t *testing.T) {
	runner, store := newTestRunner(t)
	ctx := context.Background()

	paths := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case paths <- r.URL.RequestURI():
		default:
		}
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	wf := fundedWorkflow(t, store, "StateNoMutate")

	if err := store.SetWorkflowVariable(ctx, wf.ID, "cursor", []byte(`"first"`)); err != nil {
		t.Fatal(err)
	}
	wf.Nodes = []models.WorkflowNode{
		{ID: "t1", Type: models.NodeTypeTrigger},
		{ID: "h1", Type: models.NodeTypeTool, Template: "http",
			URL: srv.URL + "/x?c={{state.cursor}}", Method: http.MethodGet},
	}
	wf.Edges = []models.WorkflowEdge{
		{ID: "x1", From: "t1", To: "h1", Kind: models.EdgeKindFlow},
	}

	run1, _ := store.CreateRun(ctx, wf.ID, "manual", []byte(`{"message":"go"}`))
	runner.Run(ctx, wf, run1)

	if err := store.SetWorkflowVariable(ctx, wf.ID, "cursor", []byte(`"second"`)); err != nil {
		t.Fatal(err)
	}
	run2, _ := store.CreateRun(ctx, wf.ID, "manual", []byte(`{"message":"go"}`))
	runner.Run(ctx, wf, run2)

	first := <-paths
	second := <-paths
	if first != "/x?c=first" {
		t.Fatalf("run 1: got %q", first)
	}
	if second != "/x?c=second" {
		t.Fatalf("run 2 reused run 1's substituted URL instead of the template: got %q", second)
	}
}

// Credentials must never be assembled from mutable state.
func TestStateIsNotExpandedIntoCredentials(t *testing.T) {
	runner, store := newTestRunner(t)
	ctx := context.Background()

	gotAuth := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case gotAuth <- r.Header.Get("Authorization"):
		default:
		}
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	wf := fundedWorkflow(t, store, "StateNoCreds")

	if err := store.SetWorkflowVariable(ctx, wf.ID, "secret", []byte(`"leaked"`)); err != nil {
		t.Fatal(err)
	}
	wf.Nodes = []models.WorkflowNode{
		{ID: "t1", Type: models.NodeTypeTrigger},
		{ID: "h1", Type: models.NodeTypeTool, Template: "http",
			URL: srv.URL, Method: http.MethodGet, APIKey: "{{state.secret}}"},
	}
	wf.Edges = []models.WorkflowEdge{
		{ID: "x1", From: "t1", To: "h1", Kind: models.EdgeKindFlow},
	}

	run, _ := store.CreateRun(ctx, wf.ID, "manual", []byte(`{"message":"go"}`))
	runner.Run(ctx, wf, run)

	select {
	case auth := <-gotAuth:
		if auth == "leaked" || auth == "Bearer leaked" {
			t.Fatalf("APIKey must never be expanded from state, got %q", auth)
		}
	default:
		t.Fatal("the tool node never called the server")
	}
}
