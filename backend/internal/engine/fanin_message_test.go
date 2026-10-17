package engine_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/sse"
)

// runMessageRoutingWorkflow runs the graph build returns for a fresh, funded
// user and returns the body the "sink" node POSTed: with no body template, an
// http POST sends rc.Message(), so this is exactly what the sink's
// Message() resolved to. "left" and "right" are GET nodes whose servers
// answer after the given delays, so a test can force either one to finish
// (and Set its output) last.
func runMessageRoutingWorkflow(t *testing.T, name string, leftDelay, rightDelay time.Duration, build func(leftURL, rightURL, sinkURL string) models.WorkflowGraph) string {
	t.Helper()
	runner, store := newTestRunner(t)
	ctx := context.Background()

	upstream := func(from string, delay time.Duration) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(delay)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"from":%q}`, from)
		}))
	}
	left := upstream("left", leftDelay)
	defer left.Close()
	right := upstream("right", rightDelay)
	defer right.Close()

	var mu sync.Mutex
	var sinkBody string
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		sinkBody = string(b)
		mu.Unlock()
		w.Write([]byte(`{"ok":true}`))
	}))
	defer sink.Close()

	email := fmt.Sprintf("message-routing-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(ctx, email, "hash")
	if err != nil {
		t.Fatal(err)
	}
	// Three billable http nodes (left, right, sink), plus headroom.
	fundUser(t, store, user.ID, 3*models.ByokFlatFeeUSDMicros+200_000)

	wf, err := store.CreateWorkflow(ctx, name, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })
	wf, _ = store.UpdateWorkflow(ctx, wf.ID, wf.Name, build(left.URL, right.URL, sink.URL))

	run, err := store.CreateRun(ctx, wf.ID, "test", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	broker := sse.NewBroker()
	broker.Create(run.ID)

	runner.Start(wf, run)
	final := waitForRunDone(t, store, run.ID)
	if final.Status != models.RunStatusSuccess {
		t.Fatalf("want success got %s", final.Status)
	}

	mu.Lock()
	defer mu.Unlock()
	return sinkBody
}

func assertFrom(t *testing.T, body, want string) {
	t.Helper()
	if !strings.Contains(body, `"from":"`+want+`"`) {
		t.Fatalf("sink received %q, want the %q branch's output", body, want)
	}
}

// TestMessageFollowsFlowEdgeNotSiblingBranch is part of #68: a node with a
// single flow predecessor used to receive whichever node in the previous
// level happened to Set its output last -- including a node on a completely
// unrelated branch. Here sink hangs off left only, and right (a sibling of
// left on another branch) is made to finish last; sink must still get left's
// output.
func TestMessageFollowsFlowEdgeNotSiblingBranch(t *testing.T) {
	body := runMessageRoutingWorkflow(t, "Message Sibling Branch Test", 0, 150*time.Millisecond,
		func(leftURL, rightURL, sinkURL string) models.WorkflowGraph {
			return models.WorkflowGraph{
				Nodes: []models.WorkflowNode{
					{ID: "trigger", Type: models.NodeTypeTrigger},
					{ID: "left", Type: models.NodeTypeTool, Template: "http", URL: leftURL, Method: "GET"},
					{ID: "right", Type: models.NodeTypeTool, Template: "http", URL: rightURL, Method: "GET"},
					{ID: "sink", Type: models.NodeTypeTool, Template: "http", URL: sinkURL, Method: "POST"},
					{ID: "end", Type: models.NodeTypeEnd},
				},
				Edges: []models.WorkflowEdge{
					{ID: "e1", From: "trigger", To: "left", Kind: models.EdgeKindFlow},
					{ID: "e2", From: "trigger", To: "right", Kind: models.EdgeKindFlow},
					{ID: "e3", From: "left", To: "sink", Kind: models.EdgeKindFlow},
					{ID: "e4", From: "sink", To: "end", Kind: models.EdgeKindFlow},
					{ID: "e5", From: "right", To: "end", Kind: models.EdgeKindFlow},
				},
			}
		})
	assertFrom(t, body, "left")
}

// TestFanInMessageIsDeterministic is the core of #68: when two predecessors
// in the same level both feed one node, which output it received depended on
// goroutine scheduling. It must now be the same every run -- the predecessor
// whose flow edge comes later in the saved graph (here left, e4) -- whichever
// branch actually finishes last.
func TestFanInMessageIsDeterministic(t *testing.T) {
	fanIn := func(leftURL, rightURL, sinkURL string) models.WorkflowGraph {
		return models.WorkflowGraph{
			Nodes: []models.WorkflowNode{
				{ID: "trigger", Type: models.NodeTypeTrigger},
				{ID: "left", Type: models.NodeTypeTool, Template: "http", URL: leftURL, Method: "GET"},
				{ID: "right", Type: models.NodeTypeTool, Template: "http", URL: rightURL, Method: "GET"},
				{ID: "sink", Type: models.NodeTypeTool, Template: "http", URL: sinkURL, Method: "POST"},
				{ID: "end", Type: models.NodeTypeEnd},
			},
			Edges: []models.WorkflowEdge{
				{ID: "e1", From: "trigger", To: "left", Kind: models.EdgeKindFlow},
				{ID: "e2", From: "trigger", To: "right", Kind: models.EdgeKindFlow},
				{ID: "e3", From: "right", To: "sink", Kind: models.EdgeKindFlow},
				{ID: "e4", From: "left", To: "sink", Kind: models.EdgeKindFlow},
				{ID: "e5", From: "sink", To: "end", Kind: models.EdgeKindFlow},
			},
		}
	}

	t.Run("right finishes last", func(t *testing.T) {
		assertFrom(t, runMessageRoutingWorkflow(t, "Fan-in Right Last", 0, 150*time.Millisecond, fanIn), "left")
	})
	t.Run("left finishes last", func(t *testing.T) {
		assertFrom(t, runMessageRoutingWorkflow(t, "Fan-in Left Last", 150*time.Millisecond, 0, fanIn), "left")
	})
}
