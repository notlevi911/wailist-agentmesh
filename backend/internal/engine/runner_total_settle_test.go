package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentmesh/backend/internal/db"
	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/sse"
)

// newFakeFacilitator builds a minimal GoPlausible-shaped facilitator that
// always verifies and settles successfully, returning a fresh, unique txID
// per settle call -- needed here (unlike the single-txID fakes elsewhere in
// this package) because a run can now produce more than one real settlement
// (the existing tool402 pre-fund, plus the new run-total settlement), and
// the tests below need to tell them apart by their distinct tx ids. Seeded
// with time.Now().UnixNano(), not just a per-server counter reset to zero:
// x402_run_fundings.inbound_tx_id is UNIQUE, and every other test in this
// package that fabricates a tx id does the same, precisely so re-running
// the suite against the same persistent throwaway Postgres container never
// collides with a row a previous run already committed.
func newFakeFacilitator(t *testing.T, prefix string) *httptest.Server {
	t.Helper()
	seed := time.Now().UnixNano()
	var n int
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/verify" {
			json.NewEncoder(w).Encode(map[string]any{"isValid": true})
			return
		}
		n++
		json.NewEncoder(w).Encode(map[string]any{"success": true, "transaction": fmt.Sprintf("%s-%d-%d", prefix, seed, n)})
	}))
}

// TestRunTotalSettlesNonX402BillingAtEndOfRun is the base case: a run with
// only a standalone, non-tool402 billable node (a Tool/http node, byok flat
// fee) produces exactly one x402_run_fundings row once the run finishes,
// sized to that node's flat fee -- the exact amount that, before this
// change, only ever moved inside the internal credit ledger and never
// touched the chain at all.
func TestRunTotalSettlesNonX402BillingAtEndOfRun(t *testing.T) {
	ctx := context.Background()

	facilitator := newFakeFacilitator(t, "RUNTOTAL")
	defer facilitator.Close()

	toolSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	defer toolSrv.Close()

	runner, store := newTestRunnerWithRunFunding(t, "http://localhost:65535", facilitator.URL)

	email := fmt.Sprintf("run-total-basic-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(ctx, email, "hash")
	if err != nil {
		t.Fatal(err)
	}
	fundUser(t, store, user.ID, models.ByokFlatFeeUSDMicros+200_000)

	wf, err := store.CreateWorkflow(ctx, "Run Total Basic Test", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })

	graph := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{
			{ID: "n1", Type: models.NodeTypeTrigger},
			{ID: "n2", Type: models.NodeTypeTool, Template: "http", URL: toolSrv.URL, Method: "GET"},
			{ID: "n3", Type: models.NodeTypeEnd},
		},
		Edges: []models.WorkflowEdge{
			{ID: "e1", From: "n1", To: "n2", Kind: models.EdgeKindFlow},
			{ID: "e2", From: "n2", To: "n3", Kind: models.EdgeKindFlow},
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
	final := waitForRunDone(t, store, run.ID)
	if final.Status != models.RunStatusSuccess {
		t.Fatalf("want success got %s", final.Status)
	}

	// The DB-side charge is unchanged by any of this -- still exactly the
	// flat fee, still an internal credit debit, same as before this change.
	balance, err := store.GetCreditBalance(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if balance != 200_000 {
		t.Fatalf("want balance 200000 got %d", balance)
	}

	fundings, err := waitForRunFundings(t, store, run.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if fundings[0].AmountAssetMicros != models.ByokFlatFeeUSDMicros {
		t.Fatalf("want run-total settlement of %d, got %d", models.ByokFlatFeeUSDMicros, fundings[0].AmountAssetMicros)
	}
}

// TestRunTotalSettlementExcludesToolAssetSpendAlreadySettledOnChain proves
// the two settlement rails never double-settle the same money: a workflow
// with BOTH a run-funded tool402 agent AND a standalone byok-flat-fee tool
// node ends with two x402_run_fundings rows -- the pre-existing per-agent
// pre-fund (unchanged amount: vendor cost + platform markup) and the new
// end-of-run settlement -- and the new one's amount is exactly the standalone
// tool's flat fee, never the tool402 agent's relay cost or markup.
func TestRunTotalSettlementExcludesToolAssetSpendAlreadySettledOnChain(t *testing.T) {
	ctx := context.Background()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Payment") != "" {
			w.Write([]byte(`{"ok":true}`))
			return
		}
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"TARGETADDR","asset":"10458941","maxAmountRequired":"250000"}]}`))
	}))
	defer target.Close()

	facilitator := newFakeFacilitator(t, "DEDUP")
	defer facilitator.Close()

	toolSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	defer toolSrv.Close()

	llmCallCount := 0
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llmCallCount++
		w.Header().Set("Content-Type", "application/json")
		if llmCallCount == 1 {
			json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"message": map[string]any{
					"role": "assistant",
					"tool_calls": []map[string]any{{
						"id":       "call_1",
						"type":     "function",
						"function": map[string]any{"name": "paid_tool", "arguments": "{}"},
					}},
				}}},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "done"}}},
		})
	}))
	defer llmSrv.Close()

	runner, store := newTestRunnerWithRunFunding(t, "http://localhost:65535", facilitator.URL)
	nodes.SetOpenAIBaseURL(llmSrv.URL)
	defer nodes.SetOpenAIBaseURL("https://api.openai.com")

	email := fmt.Sprintf("run-total-dedup-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(ctx, email, "hash")
	if err != nil {
		t.Fatal(err)
	}
	fundUser(t, store, user.ID, 3_000_000+models.ByokFlatFeeUSDMicros)

	wf, err := store.CreateWorkflow(ctx, "Run Total Dedup Test", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })

	graph := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{
			{ID: "n1", Type: models.NodeTypeTrigger},
			{ID: "p1", Type: models.NodeTypeProvider, Template: "openai", APIKey: "test-key", Model: "gpt-4o"},
			{ID: "a1", Type: models.NodeTypeAgent},
			{ID: "x1", Type: models.NodeTypeTool402, Name: "paid_tool", Endpoint: target.URL},
			{ID: "n3", Type: models.NodeTypeEnd},
			// A parallel, unrelated branch off the same trigger so this run
			// also owes a plain byok flat fee alongside the tool402 spend.
			{ID: "n2", Type: models.NodeTypeTool, Template: "http", URL: toolSrv.URL, Method: "GET"},
			{ID: "n4", Type: models.NodeTypeEnd},
		},
		Edges: []models.WorkflowEdge{
			{ID: "e1", From: "n1", To: "a1", Kind: models.EdgeKindFlow},
			{ID: "e2", From: "a1", To: "n3", Kind: models.EdgeKindFlow},
			{ID: "e3", From: "p1", To: "a1", Kind: models.EdgeKindAttach, ToPort: "model"},
			{ID: "e4", From: "x1", To: "a1", Kind: models.EdgeKindAttach, ToPort: "tools"},
			{ID: "e5", From: "n1", To: "n2", Kind: models.EdgeKindFlow},
			{ID: "e6", From: "n2", To: "n4", Kind: models.EdgeKindFlow},
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
	final := waitForRunDone(t, store, run.ID)
	if final.Status != models.RunStatusSuccess {
		t.Fatalf("want success got %s", final.Status)
	}

	fundings, err := waitForRunFundings(t, store, run.ID, 2)
	if err != nil {
		t.Fatal(err)
	}

	wantPreFund := int64(250_000 + models.X402PlatformFeeUSDMicros)
	var sawPreFund, sawRunTotal bool
	for _, f := range fundings {
		switch f.AmountAssetMicros {
		case wantPreFund:
			sawPreFund = true
		case models.ByokFlatFeeUSDMicros:
			sawRunTotal = true
		default:
			t.Fatalf("unexpected x402_run_fundings amount %d (want either the pre-fund %d or the run-total %d)", f.AmountAssetMicros, wantPreFund, models.ByokFlatFeeUSDMicros)
		}
	}
	if !sawPreFund {
		t.Fatalf("want a pre-fund row of %d, got %+v", wantPreFund, fundings)
	}
	if !sawRunTotal {
		t.Fatalf("want a run-total row of exactly the byok flat fee (%d), not inflated by tool402 spend, got %+v", models.ByokFlatFeeUSDMicros, fundings)
	}
}

// TestRunTotalSettlementNoOpWithoutPlatformWallet confirms a Runner with no
// platform wallet configured (the normal shape for most existing test
// harnesses, and any deployment that hasn't set up self-settlement) behaves
// exactly as before this change: billing still happens in the DB, but no
// settlement is attempted and no x402_run_fundings row appears.
func TestRunTotalSettlementNoOpWithoutPlatformWallet(t *testing.T) {
	ctx := context.Background()
	runner, store := newTestRunner(t)

	toolSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	defer toolSrv.Close()

	email := fmt.Sprintf("run-total-nowallet-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(ctx, email, "hash")
	if err != nil {
		t.Fatal(err)
	}
	fundUser(t, store, user.ID, models.ByokFlatFeeUSDMicros+200_000)

	wf, err := store.CreateWorkflow(ctx, "Run Total No Wallet Test", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })

	graph := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{
			{ID: "n1", Type: models.NodeTypeTrigger},
			{ID: "n2", Type: models.NodeTypeTool, Template: "http", URL: toolSrv.URL, Method: "GET"},
			{ID: "n3", Type: models.NodeTypeEnd},
		},
		Edges: []models.WorkflowEdge{
			{ID: "e1", From: "n1", To: "n2", Kind: models.EdgeKindFlow},
			{ID: "e2", From: "n2", To: "n3", Kind: models.EdgeKindFlow},
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
	final := waitForRunDone(t, store, run.ID)
	if final.Status != models.RunStatusSuccess {
		t.Fatalf("want success got %s", final.Status)
	}

	// Give any (wrongly-fired) settlement attempt a moment, same wait budget
	// as waitForRunFundings uses elsewhere in this file, then assert zero.
	time.Sleep(150 * time.Millisecond)
	fundings, err := store.ListX402RunFundingsByRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(fundings) != 0 {
		t.Fatalf("want zero x402_run_fundings rows (no platform wallet configured), got %d: %+v", len(fundings), fundings)
	}
}

// TestRunTotalSettlementSkippedForNoBillableWork confirms a trigger-only
// workflow (nothing billable ran) settles nothing, even with a platform
// wallet fully configured -- amount <= 0 stays a no-op, same as
// selfSettleWallet1ToWallet2's own existing contract.
func TestRunTotalSettlementSkippedForNoBillableWork(t *testing.T) {
	ctx := context.Background()

	facilitator := newFakeFacilitator(t, "EMPTY")
	defer facilitator.Close()

	runner, store := newTestRunnerWithRunFunding(t, "http://localhost:65535", facilitator.URL)

	email := fmt.Sprintf("run-total-empty-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(ctx, email, "hash")
	if err != nil {
		t.Fatal(err)
	}
	fundUser(t, store, user.ID, 200_000)

	wf, err := store.CreateWorkflow(ctx, "Run Total Empty Test", user.ID)
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
	final := waitForRunDone(t, store, run.ID)
	if final.Status != models.RunStatusSuccess {
		t.Fatalf("want success got %s", final.Status)
	}

	time.Sleep(150 * time.Millisecond)
	fundings, err := store.ListX402RunFundingsByRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(fundings) != 0 {
		t.Fatalf("want zero x402_run_fundings rows (nothing billable ran), got %d: %+v", len(fundings), fundings)
	}
}

// TestRunTotalSettlementFailureDoesNotFailRun confirms a facilitator that
// rejects the run-total settlement (money never moves) does not affect the
// run's own already-recorded status or the user's already-correct DB
// balance -- this settlement is purely an additive on-chain receipt, not
// part of the run's success/failure determination, exactly as documented on
// settleRunTotal.
func TestRunTotalSettlementFailureDoesNotFailRun(t *testing.T) {
	ctx := context.Background()

	failingFacilitator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/verify" {
			json.NewEncoder(w).Encode(map[string]any{"isValid": true})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "settle rejected"})
	}))
	defer failingFacilitator.Close()

	toolSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	defer toolSrv.Close()

	runner, store := newTestRunnerWithRunFunding(t, "http://localhost:65535", failingFacilitator.URL)

	email := fmt.Sprintf("run-total-facilitatorfail-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(ctx, email, "hash")
	if err != nil {
		t.Fatal(err)
	}
	fundUser(t, store, user.ID, models.ByokFlatFeeUSDMicros+200_000)

	wf, err := store.CreateWorkflow(ctx, "Run Total Facilitator Fail Test", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })

	graph := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{
			{ID: "n1", Type: models.NodeTypeTrigger},
			{ID: "n2", Type: models.NodeTypeTool, Template: "http", URL: toolSrv.URL, Method: "GET"},
			{ID: "n3", Type: models.NodeTypeEnd},
		},
		Edges: []models.WorkflowEdge{
			{ID: "e1", From: "n1", To: "n2", Kind: models.EdgeKindFlow},
			{ID: "e2", From: "n2", To: "n3", Kind: models.EdgeKindFlow},
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
	final := waitForRunDone(t, store, run.ID)
	if final.Status != models.RunStatusSuccess {
		t.Fatalf("want success (settlement failure must not fail the run) got %s", final.Status)
	}

	balance, err := store.GetCreditBalance(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if balance != 200_000 {
		t.Fatalf("want balance 200000 (DB billing unaffected by on-chain settlement failure) got %d", balance)
	}

	time.Sleep(150 * time.Millisecond)
	fundings, err := store.ListX402RunFundingsByRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(fundings) != 0 {
		t.Fatalf("want zero x402_run_fundings rows (settlement failed before RecordRunFunding), got %d: %+v", len(fundings), fundings)
	}
}

// waitForRunFundings polls ListX402RunFundingsByRun until it sees exactly
// want rows or a short deadline expires -- the run-total settlement fires
// from a defer after waitForRunDone's caller already observes the run's
// terminal status, so there's a small window where the DB write hasn't
// landed yet.
func waitForRunFundings(t *testing.T, store *db.Store, runID string, want int) ([]models.X402RunFunding, error) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var fundings []models.X402RunFunding
	var err error
	for time.Now().Before(deadline) {
		fundings, err = store.ListX402RunFundingsByRun(context.Background(), runID)
		if err != nil {
			return nil, err
		}
		if len(fundings) == want {
			return fundings, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil, fmt.Errorf("timed out waiting for %d x402_run_fundings rows, got %d: %+v", want, len(fundings), fundings)
}
