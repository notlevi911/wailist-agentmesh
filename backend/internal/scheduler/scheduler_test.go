package scheduler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentmesh/backend/internal/db"
	"github.com/agentmesh/backend/internal/engine"
	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/sse"
)

// TestMain sets the permissive URL validator once for this whole test
// binary, mirroring the identical override in internal/engine's own
// TestMain (debit_test.go) and internal/engine/nodes' (tool402_test.go).
// Without it, the SSRF guard in nodes.ExecuteTool blocks every
// httptest.NewServer target (127.0.0.1) with "requests to private/internal
// addresses are not allowed" -- this package's tests need a real HTTP node
// to actually reach the test server to prove the scheduler fired it.
func TestMain(m *testing.M) {
	nodes.SetURLValidatorForTest(func(string) error { return nil })
	os.Exit(m.Run())
}

type noopSigner struct{}

func (n *noopSigner) SignAndSendPayment(_ context.Context, _, _ string, _ uint64) (string, error) {
	return "", nil
}

func testStore(t *testing.T) *db.Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	store, err := db.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	return store
}

// TestTickFiresDueScheduleAndAdvancesIt is the scheduler's own end-to-end
// check: a deployed workflow with a past-due schedule gets a real run
// started (the trigger -> http -> end graph below actually hits a test
// server) when tick runs, and isn't claimed again on a second tick right
// after.
func TestTickFiresDueScheduleAndAdvancesIt(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	email := fmt.Sprintf("scheduler-tick-test-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(ctx, email, "hash")
	if err != nil {
		t.Fatal(err)
	}
	// The workflow's http Tool node is billable (nodes.BillableFlatFee);
	// preflightCheck fails outright for a user with no credit_balance row
	// at all before the node ever runs, so fund one here.
	orderID := fmt.Sprintf("order_scheduler_test_%d", time.Now().UnixNano())
	if _, err := store.CreateCreditTransaction(ctx, user.ID, orderID, 50000, 0.012); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CompleteCreditTransaction(ctx, "cashfree", orderID, "pay_scheduler_test"); err != nil {
		t.Fatal(err)
	}

	wf, err := store.CreateWorkflow(ctx, "Scheduler Tick Test", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })

	graph := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{
			{ID: "n1", Type: models.NodeTypeTrigger},
			{ID: "n2", Type: models.NodeTypeTool, Template: "http", URL: srv.URL, Method: "GET"},
			{ID: "n3", Type: models.NodeTypeEnd},
		},
		Edges: []models.WorkflowEdge{
			{ID: "e1", From: "n1", To: "n2", Kind: models.EdgeKindFlow},
			{ID: "e2", From: "n2", To: "n3", Kind: models.EdgeKindFlow},
		},
	}
	wf, _ = store.UpdateWorkflow(ctx, wf.ID, wf.Name, graph)

	if err := store.SetWorkflowDeployed(ctx, wf.ID, "https://example.com/run", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.SetWorkflowSchedule(ctx, wf.ID, "* * * * *", time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}

	broker := sse.NewBroker()
	runner := engine.NewRunner(store, broker, &noopSigner{}, "http://localhost:8080", "", "", engine.X402Config{USDCAssetID: 10458941})
	sched := New(store, runner, broker, "")

	sched.tick(ctx)

	// The run is started in a goroutine (engine.Runner.Start); give it a
	// moment to actually hit the test server rather than racing it.
	deadline := time.Now().Add(3 * time.Second)
	for atomic.LoadInt32(&requests) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("server saw %d requests after tick, want 1 (the scheduled run's http node)", got)
	}

	// A second tick immediately after must not fire it again -- the first
	// tick's ClaimDueSchedules call already advanced schedule_next_run_at
	// into the future (next minute boundary).
	sched.tick(ctx)
	time.Sleep(100 * time.Millisecond)
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("server saw %d requests after a second tick, want still 1 (must not double-fire)", got)
	}
}

// TestTickSkipsWorkflowWithRunAlreadyInFlight covers the overlap case
// TestTickFiresDueScheduleAndAdvancesIt doesn't: a workflow whose *own*
// schedule is due again (e.g. a very tight cron, or a slow run that's still
// mid-flight when the next tick lands) while its previous scheduled run
// hasn't finished yet. engine.Runner.Start's registry always supersedes
// (cancels) any previous run for the same workflow ID -- without the
// scheduler's own IsRunning check, this second tick would silently cancel
// the still-running first attempt mid-node instead of skipping.
func TestTickSkipsWorkflowWithRunAlreadyInFlight(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	release := make(chan struct{})
	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		<-release
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	email := fmt.Sprintf("scheduler-overlap-test-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(ctx, email, "hash")
	if err != nil {
		t.Fatal(err)
	}
	orderID := fmt.Sprintf("order_scheduler_overlap_test_%d", time.Now().UnixNano())
	if _, err := store.CreateCreditTransaction(ctx, user.ID, orderID, 50000, 0.012); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CompleteCreditTransaction(ctx, "cashfree", orderID, "pay_scheduler_overlap_test"); err != nil {
		t.Fatal(err)
	}

	wf, err := store.CreateWorkflow(ctx, "Scheduler Overlap Test", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })

	graph := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{
			{ID: "n1", Type: models.NodeTypeTrigger},
			{ID: "n2", Type: models.NodeTypeTool, Template: "http", URL: srv.URL, Method: "GET"},
			{ID: "n3", Type: models.NodeTypeEnd},
		},
		Edges: []models.WorkflowEdge{
			{ID: "e1", From: "n1", To: "n2", Kind: models.EdgeKindFlow},
			{ID: "e2", From: "n2", To: "n3", Kind: models.EdgeKindFlow},
		},
	}
	wf, _ = store.UpdateWorkflow(ctx, wf.ID, wf.Name, graph)

	if err := store.SetWorkflowDeployed(ctx, wf.ID, "https://example.com/run", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.SetWorkflowSchedule(ctx, wf.ID, "* * * * *", time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}

	broker := sse.NewBroker()
	runner := engine.NewRunner(store, broker, &noopSigner{}, "http://localhost:8080", "", "", engine.X402Config{USDCAssetID: 10458941})
	sched := New(store, runner, broker, "")

	sched.tick(ctx)

	// Wait for the first scheduled run to genuinely be in-flight (its http
	// node has actually made the request and is now blocked on release),
	// not merely registered.
	deadline := time.Now().Add(3 * time.Second)
	for atomic.LoadInt32(&requests) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("server saw %d requests before the overlap tick, want 1", got)
	}

	// Re-arm the schedule as due again -- simulating this same workflow's
	// next cron tick landing before the first run finished.
	if err := store.SetWorkflowSchedule(ctx, wf.ID, "* * * * *", time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}

	sched.tick(ctx)
	time.Sleep(100 * time.Millisecond)
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("server saw %d requests after the overlap tick, want still 1 (in-flight run must not be superseded)", got)
	}

	// Let the first (only) run's blocked http node finish so its goroutine
	// doesn't outlive the test.
	close(release)
	time.Sleep(200 * time.Millisecond)
}

// TestTickSkipsWorkflowRunningAccordingToDBAcrossReplicas is the
// multi-replica counterpart to TestTickSkipsWorkflowWithRunAlreadyInFlight:
// this backend runs several Railway replicas, each with its own
// process-local engine.Runner registry, so a run one replica started is
// invisible to engine.Runner.IsRunning on any other. Simulated here with a
// second Scheduler/Runner pair sharing the same store but never having
// registered the in-flight run locally -- if tick relied on IsRunning
// alone, this "other replica" would see IsRunning=false and fire a
// duplicate run despite Postgres already showing one as "running".
func TestTickSkipsWorkflowRunningAccordingToDBAcrossReplicas(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	release := make(chan struct{})
	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		<-release
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	email := fmt.Sprintf("scheduler-cross-replica-test-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(ctx, email, "hash")
	if err != nil {
		t.Fatal(err)
	}
	orderID := fmt.Sprintf("order_scheduler_cross_replica_test_%d", time.Now().UnixNano())
	if _, err := store.CreateCreditTransaction(ctx, user.ID, orderID, 50000, 0.012); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CompleteCreditTransaction(ctx, "cashfree", orderID, "pay_scheduler_cross_replica_test"); err != nil {
		t.Fatal(err)
	}

	wf, err := store.CreateWorkflow(ctx, "Scheduler Cross Replica Test", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })

	graph := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{
			{ID: "n1", Type: models.NodeTypeTrigger},
			{ID: "n2", Type: models.NodeTypeTool, Template: "http", URL: srv.URL, Method: "GET"},
			{ID: "n3", Type: models.NodeTypeEnd},
		},
		Edges: []models.WorkflowEdge{
			{ID: "e1", From: "n1", To: "n2", Kind: models.EdgeKindFlow},
			{ID: "e2", From: "n2", To: "n3", Kind: models.EdgeKindFlow},
		},
	}
	wf, _ = store.UpdateWorkflow(ctx, wf.ID, wf.Name, graph)

	if err := store.SetWorkflowDeployed(ctx, wf.ID, "https://example.com/run", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.SetWorkflowSchedule(ctx, wf.ID, "* * * * *", time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}

	broker := sse.NewBroker()
	// "Replica A": fires the first tick and starts the run.
	runnerA := engine.NewRunner(store, broker, &noopSigner{}, "http://localhost:8080", "", "", engine.X402Config{USDCAssetID: 10458941})
	schedA := New(store, runnerA, broker, "")
	schedA.tick(ctx)

	deadline := time.Now().Add(3 * time.Second)
	for atomic.LoadInt32(&requests) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("server saw %d requests before the cross-replica tick, want 1", got)
	}

	if err := store.SetWorkflowSchedule(ctx, wf.ID, "* * * * *", time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}

	// "Replica B": a completely separate Runner instance, sharing only the
	// store -- runnerB.IsRunning(wf.ID) is false here since runnerA is the
	// one that registered the in-flight run, exactly mirroring what a
	// second backend process would see.
	runnerB := engine.NewRunner(store, broker, &noopSigner{}, "http://localhost:8080", "", "", engine.X402Config{USDCAssetID: 10458941})
	schedB := New(store, runnerB, broker, "")
	if runnerB.IsRunning(wf.ID) {
		t.Fatal("runnerB should have no local record of the run runnerA started -- this test only proves anything if that's true")
	}
	schedB.tick(ctx)
	time.Sleep(100 * time.Millisecond)
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("server saw %d requests after the cross-replica tick, want still 1 (DB-visible in-flight run must not be superseded even when unregistered locally)", got)
	}

	close(release)
	time.Sleep(200 * time.Millisecond)
}
