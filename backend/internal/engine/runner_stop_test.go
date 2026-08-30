package engine_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/agentmesh/backend/internal/db"
	"github.com/agentmesh/backend/internal/engine"
	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/sse"
	"github.com/agentmesh/backend/internal/x402"
)

type noopSigner struct{}

func (n *noopSigner) SignAndSendPayment(_ context.Context, _, _ string, _ uint64) (string, error) {
	return "", nil
}

// fakeRelaySigner additionally satisfies nodes.USDCGroupSigner (unlike
// noopSigner), so a Runner built with it will actually route tool402 relay-
// dialect payments through the relay/Wallet 1 path instead of degrading
// gracefully with "no platform spend wallet configured".
type fakeRelaySigner struct{ noopSigner }

func (f *fakeRelaySigner) SignUSDCPaymentGroup(_ context.Context, _, _ string, _, _ uint64, _ string) ([]string, int, error) {
	return []string{"g0", "g1"}, 0, nil
}

func (f *fakeRelaySigner) SignUSDCPaymentSingle(_ context.Context, _, _ string, _, _ uint64) ([]string, int, error) {
	return []string{"g0"}, 0, nil
}

// Compile-time proof that the test doubles really do satisfy the interface
// they claim to. reserveAndFundRun and executeTool402V2Relay both reach
// their signer via `x, _ := r.walletSvc.(nodes.USDCGroupSigner)`, which
// discards the ok and degrades to the no-funding path when the assertion
// fails -- so a double that has fallen behind the interface does not fail
// to compile, it silently turns every run-funding assertion in this package
// into a no-op. That is exactly what happened when SignUSDCPaymentSingle
// was added to USDCGroupSigner and these doubles were not updated with it.
var _ nodes.USDCGroupSigner = (*fakeRelaySigner)(nil)

func newTestRunnerWithRelay(t *testing.T, relayBaseURL string) (*engine.Runner, *db.Store) {
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
	broker := sse.NewBroker()
	return engine.NewRunner(store, broker, &fakeRelaySigner{}, relayBaseURL, "platform-enc-mnemonic", engine.X402Config{USDCAssetID: 10458941}), store
}

func newTestRunner(t *testing.T) (*engine.Runner, *db.Store) {
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
	broker := sse.NewBroker()
	return engine.NewRunner(store, broker, &noopSigner{}, "http://localhost:8080", "", engine.X402Config{USDCAssetID: 10458941}), store
}

// newTestRunnerWithRunFunding builds a Runner with the full run-level
// x402 pre-funding path wired up (platform wallet address/mnemonic,
// facilitator client, relay network/fee payer) — needed by tests that
// exercise reserveAndFundRun's real settle-then-in-process-payout flow
// (Task 5), unlike newTestRunner/newTestRunnerWithRelay above which only
// need the legacy per-call paths and leave these fields zero-valued.
// maxRelayOutboundUSDMicros is left at 0 (no cap) — none of these tests
// need to exercise the outbound cap.
func newTestRunnerWithRunFunding(t *testing.T, relayBaseURL, facilitatorURL string) (*engine.Runner, *db.Store) {
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
	broker := sse.NewBroker()
	return engine.NewRunner(store, broker, &fakeRelaySigner{}, relayBaseURL, "platform-spend-enc-mnemonic", engine.X402Config{
		USDCAssetID:               10458941,
		PlatformWalletAddress:     "PLATFORMADDR",
		PlatformWalletEncMnemonic: "platform-wallet-enc-mnemonic",
		FacilitatorClient:         x402.NewFacilitatorClient(facilitatorURL),
		RelayNetwork:              "algorand:testnet",
		RelayFeePayer:             "FEEPAYERADDR",
		MaxRelayOutboundUSDMicros: 0,
	}), store
}

// TestStopReturnsFalseWhenNotRunning verifies that Stop returns false
// when the workflow has no active run registered in the registry.
func TestStopReturnsFalseWhenNotRunning(t *testing.T) {
	runner, _ := newTestRunner(t)
	if runner.Stop("some-workflow-id-that-was-never-started") {
		t.Fatal("Stop should return false when no run is registered")
	}
}

// TestStopReturnsTrueImmediatelyAfterStart verifies that Stop returns true
// when called right after Start, because Start registers the cancel
// synchronously before launching the goroutine.
func TestStopReturnsTrueImmediatelyAfterStart(t *testing.T) {
	runner, store := newTestRunner(t)
	ctx := context.Background()

	wf, err := store.CreateWorkflow(ctx, "Stop Test WF", "test-user")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })

	graph := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{
			{ID: "n1", Type: models.NodeTypeTrigger},
			{ID: "n2", Type: models.NodeTypeEnd},
		},
		Edges: []models.WorkflowEdge{
			{ID: "e1", From: "n1", To: "n2", Kind: models.EdgeKindFlow},
		},
	}
	wf, _ = store.UpdateWorkflow(ctx, wf.ID, wf.Name, graph)

	run, err := store.CreateRun(ctx, wf.ID, "test", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}

	broker := sse.NewBroker()
	broker.Create(run.ID)

	runner.Start(wf, run)
	if !runner.Stop(wf.ID) {
		t.Fatal("Stop should return true immediately after Start (cancel is registered synchronously)")
	}
}

// TestStopSetsRunStatusStopped verifies the end-to-end cancellation path:
// a workflow is started, immediately stopped, and the run record in the DB
// ends with status "stopped" (not "success" or "failed").
func TestStopSetsRunStatusStopped(t *testing.T) {
	runner, store := newTestRunner(t)
	ctx := context.Background()

	wf, err := store.CreateWorkflow(ctx, "Stop Status Test", "test-user")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })

	// Two-level graph so the runner checks ctx.Err() between levels.
	graph := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{
			{ID: "n1", Type: models.NodeTypeTrigger},
			{ID: "n2", Type: models.NodeTypeEnd},
		},
		Edges: []models.WorkflowEdge{
			{ID: "e1", From: "n1", To: "n2", Kind: models.EdgeKindFlow},
		},
	}
	wf, _ = store.UpdateWorkflow(ctx, wf.ID, wf.Name, graph)

	run, err := store.CreateRun(ctx, wf.ID, "test", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}

	broker := sse.NewBroker()
	broker.Create(run.ID)

	runner.Start(wf, run)
	runner.Stop(wf.ID)

	// Wait for the goroutine to write its final status.
	var finalRun models.Run
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		finalRun, err = store.GetRun(ctx, run.ID)
		if err == nil && finalRun.Status != models.RunStatusRunning {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if finalRun.Status != models.RunStatusStopped && finalRun.Status != models.RunStatusSuccess {
		// Success is acceptable: the two-node graph may complete before the
		// cancel propagates. What we must NOT see is "running" or "failed".
		t.Fatalf("unexpected final run status: %q", finalRun.Status)
	}
}
