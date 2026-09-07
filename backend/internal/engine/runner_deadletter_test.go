package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/sse"
)

// TestDeadLetterWrittenAfterRetriesExhausted verifies a node that never
// succeeds gets exactly one dead_letter_runs row once its retry budget is
// spent, with the attempt count covering every executeNode call made
// (1 initial + MaxRetries).
func TestDeadLetterWrittenAfterRetriesExhausted(t *testing.T) {
	runner, store := newTestRunner(t)
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	wf, err := store.CreateWorkflow(ctx, "Dead Letter Exhausted Test", fundedTestUser(t, store))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })

	graph := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{
			{ID: "n1", Type: models.NodeTypeTrigger},
			{ID: "n2", Type: models.NodeTypeTool, Template: "http", URL: srv.URL, Method: "GET", MaxRetries: 2},
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

	runner.Run(ctx, wf, run, 0)

	finalRun, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finalRun.Status != models.RunStatusFailed {
		t.Fatalf("run status = %s, want failed", finalRun.Status)
	}

	dls, err := store.GetDeadLetterRuns(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(dls) != 1 {
		t.Fatalf("got %d dead_letter_runs rows, want 1", len(dls))
	}
	if dls[0].NodeID != "n2" {
		t.Errorf("dead letter node = %s, want n2", dls[0].NodeID)
	}
	if dls[0].AttemptCount != 3 {
		t.Errorf("attempt count = %d, want 3 (1 initial + 2 retries)", dls[0].AttemptCount)
	}
	if !strings.Contains(dls[0].Error, "500") {
		t.Errorf("dead letter error = %q, want it to mention the 500 status", dls[0].Error)
	}
}

// TestDeadLetterWrittenImmediatelyForNonRetryableError verifies a
// non-retryable failure (POST + 500) dead-letters after exactly one
// attempt even though the node asked for retries.
func TestDeadLetterWrittenImmediatelyForNonRetryableError(t *testing.T) {
	runner, store := newTestRunner(t)
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	wf, err := store.CreateWorkflow(ctx, "Dead Letter Non-Retryable Test", fundedTestUser(t, store))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })

	graph := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{
			{ID: "n1", Type: models.NodeTypeTrigger},
			{ID: "n2", Type: models.NodeTypeTool, Template: "http", URL: srv.URL, Method: "POST", MaxRetries: 3},
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

	runner.Run(ctx, wf, run, 0)

	dls, err := store.GetDeadLetterRuns(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(dls) != 1 {
		t.Fatalf("got %d dead_letter_runs rows, want 1", len(dls))
	}
	if dls[0].AttemptCount != 1 {
		t.Errorf("attempt count = %d, want 1 (non-retryable, no retry attempted)", dls[0].AttemptCount)
	}
}

// TestResumeAfterDeadLetterSkipsSucceededUpstreamNode is the end-to-end
// dead-letter -> resume path: a run fails at n3 with n2 already succeeded,
// then (after the transient cause clears) Resume finishes the run without
// re-executing n2 -- proving dead-letter recovery really does build on
// Runner.Resume rather than restarting from scratch.
func TestResumeAfterDeadLetterSkipsSucceededUpstreamNode(t *testing.T) {
	runner, store := newTestRunner(t)
	ctx := context.Background()

	var failN3 atomic.Bool
	failN3.Store(true)
	var n3Requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n3Requests, 1)
		if failN3.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	wf, err := store.CreateWorkflow(ctx, "Resume After Dead Letter Test", fundedTestUser(t, store))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })

	graph := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{
			{ID: "n1", Type: models.NodeTypeTrigger},
			{ID: "n2", Type: models.NodeTypeTool, Template: "calc", URL: "1+1"},
			{ID: "n3", Type: models.NodeTypeTool, Template: "http", URL: srv.URL, Method: "GET"},
			{ID: "n4", Type: models.NodeTypeEnd},
		},
		Edges: []models.WorkflowEdge{
			{ID: "e1", From: "n1", To: "n2", Kind: models.EdgeKindFlow},
			{ID: "e2", From: "n2", To: "n3", Kind: models.EdgeKindFlow},
			{ID: "e3", From: "n3", To: "n4", Kind: models.EdgeKindFlow},
		},
	}
	wf, _ = store.UpdateWorkflow(ctx, wf.ID, wf.Name, graph)

	run, err := store.CreateRun(ctx, wf.ID, "test", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	broker := sse.NewBroker()
	broker.Create(run.ID)

	runner.Run(ctx, wf, run, 0)

	failedRun, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failedRun.Status != models.RunStatusFailed {
		t.Fatalf("run status after first attempt = %s, want failed", failedRun.Status)
	}
	if got := atomic.LoadInt32(&n3Requests); got != 1 {
		t.Fatalf("n3 saw %d requests before resume, want 1", got)
	}

	// The transient cause clears; resume should now let n3 succeed without
	// n2 (calc) running again.
	failN3.Store(false)
	broker.Create(run.ID) // Resume calls broker.Close(run.ID) again on exit; needs a live stream to close.
	runner.Resume(ctx, wf, run, 0, false)

	finalRun, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finalRun.Status != models.RunStatusSuccess {
		t.Fatalf("run status after resume = %s, want success", finalRun.Status)
	}

	logs, err := store.GetRunLogs(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var n2Count int
	for _, l := range logs {
		if l.NodeID == "n2" {
			n2Count++
		}
	}
	if n2Count != 1 {
		t.Errorf("n2 has %d run_logs rows after resume, want exactly 1 (must not re-execute)", n2Count)
	}
	if got := atomic.LoadInt32(&n3Requests); got != 2 {
		t.Errorf("n3 saw %d requests total, want 2 (1 failed + 1 succeeded on resume)", got)
	}
}

// TestPaymentRiskDeadLetterRefusesResumeWithoutForce reproduces a real
// *nodes.ErrBalanceBlocked failure (agent's own LLM turn completes and its
// fee is charged, then its attached x402 call is blocked by the pre-call
// floor guard) and verifies: the resulting dead-letter row is flagged
// PaymentRisk, Resume refuses outright without force (no new LLM call, run
// stays failed), and Resume proceeds -- attempting the node again -- once
// force is passed.
func TestPaymentRiskDeadLetterRefusesResumeWithoutForce(t *testing.T) {
	runner, store := newTestRunner(t)
	ctx := context.Background()
	runner.SetPlatformKeys(map[string]string{"openai": "platform-secret"})

	var x402Hits int32
	x402Srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&x402Hits, 1)
		w.Header().Set("X-Payment-Required", `{"price":"0.001","unit":"call","network":"algorand-testnet","recipient":"ALGO123"}`)
		w.WriteHeader(http.StatusPaymentRequired)
	}))
	defer x402Srv.Close()

	var llmHits int32
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&llmHits, 1)
		w.Header().Set("Content-Type", "application/json")
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
	}))
	defer llmSrv.Close()

	email := fmt.Sprintf("payment-risk-resume-test-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(ctx, email, "hash")
	if err != nil {
		t.Fatal(err)
	}
	// Exactly enough for the agent's own economy-tier fee, below the
	// attached tool402 call's pre-call floor guard -- same fixture as
	// TestAgentBlocksAttachedX402CallWhenBalanceInsufficientForFee.
	fundUser(t, store, user.ID, models.PlatformKeyEconomyFeeUSDMicros)

	wf, err := store.CreateWorkflow(ctx, "Payment Risk Resume Test", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })

	nodes.SetOpenAIBaseURL(llmSrv.URL)

	graph := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{
			{ID: "n1", Type: models.NodeTypeTrigger},
			{ID: "p1", Type: models.NodeTypeProvider, Template: "openai", KeyMode: "platform", Model: "gpt-4o-mini"},
			{ID: "a1", Type: models.NodeTypeAgent},
			{ID: "x1", Type: models.NodeTypeTool402, Name: "paid_tool", Endpoint: x402Srv.URL},
			{ID: "n3", Type: models.NodeTypeEnd},
		},
		Edges: []models.WorkflowEdge{
			{ID: "e1", From: "n1", To: "a1", Kind: models.EdgeKindFlow},
			{ID: "e2", From: "a1", To: "n3", Kind: models.EdgeKindFlow},
			{ID: "e3", From: "p1", To: "a1", Kind: models.EdgeKindAttach, ToPort: "model"},
			{ID: "e4", From: "x1", To: "a1", Kind: models.EdgeKindAttach, ToPort: "tools"},
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
	if final.Status != models.RunStatusFailed {
		t.Fatalf("want failed got %s", final.Status)
	}

	deadLetters, err := store.GetDeadLetterRuns(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(deadLetters) != 1 || deadLetters[0].NodeID != "a1" {
		t.Fatalf("want exactly 1 dead-letter row for node a1, got %+v", deadLetters)
	}
	if !deadLetters[0].PaymentRisk {
		t.Fatalf("want PaymentRisk=true on the dead-letter row (ErrBalanceBlocked means the agent's fee was already charged), got false")
	}

	llmHitsBeforeResume := atomic.LoadInt32(&llmHits)

	// Without force: refused outright, no new LLM call, run stays failed.
	broker.Create(run.ID) // Resume calls broker.Close(run.ID) on exit; needs a live stream to close.
	runner.Resume(ctx, wf, run, 0, false)
	if got := atomic.LoadInt32(&llmHits); got != llmHitsBeforeResume {
		t.Fatalf("resume without force made %d new LLM calls, want 0 (must refuse before executing anything)", got-llmHitsBeforeResume)
	}
	refusedRun, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if refusedRun.Status != models.RunStatusFailed {
		t.Fatalf("run status after refused resume = %s, want still failed", refusedRun.Status)
	}

	// With force: actually attempts the node again. Balance is 0 after the
	// first attempt's fee debit, so top up first -- the point of this
	// assertion is that force lets execution reach the LLM at all (it isn't
	// refused outright), not to re-litigate the insufficient-balance path
	// already covered by TestAgentBlocksAttachedX402CallWhenBalanceInsufficientForFee.
	fundUser(t, store, user.ID, models.PlatformKeyEconomyFeeUSDMicros)
	broker.Create(run.ID)
	runner.Resume(ctx, wf, run, 0, true)
	if got := atomic.LoadInt32(&llmHits); got <= llmHitsBeforeResume {
		t.Fatalf("resume with force made %d new LLM calls, want at least 1 (must actually attempt the node)", got-llmHitsBeforeResume)
	}
}

// TestDeadLetterRowClearedOnceNodeLaterSucceeds is a regression test for a
// review finding: dead_letter_runs rows were never deleted anywhere, so a
// node that dead-lettered once (even for an ordinary transient reason, let
// alone a payment-risk one) stayed in GetDeadLetterRuns' result forever --
// permanently requiring ?force=true to resume this run ever again, even
// long after that exact node succeeded on a prior resume. Reuses
// TestResumeAfterDeadLetterSkipsSucceededUpstreamNode's fixture (n3 fails
// once, resume succeeds) and adds the actual assertion that finding is
// about: the dead-letter row for n3 must be gone after it succeeds.
func TestDeadLetterRowClearedOnceNodeLaterSucceeds(t *testing.T) {
	runner, store := newTestRunner(t)
	ctx := context.Background()

	var failN3 atomic.Bool
	failN3.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failN3.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	wf, err := store.CreateWorkflow(ctx, "Dead Letter Clear Test", fundedTestUser(t, store))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })

	graph := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{
			{ID: "n1", Type: models.NodeTypeTrigger},
			{ID: "n3", Type: models.NodeTypeTool, Template: "http", URL: srv.URL, Method: "GET"},
			{ID: "n4", Type: models.NodeTypeEnd},
		},
		Edges: []models.WorkflowEdge{
			{ID: "e1", From: "n1", To: "n3", Kind: models.EdgeKindFlow},
			{ID: "e2", From: "n3", To: "n4", Kind: models.EdgeKindFlow},
		},
	}
	wf, _ = store.UpdateWorkflow(ctx, wf.ID, wf.Name, graph)

	run, err := store.CreateRun(ctx, wf.ID, "test", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	broker := sse.NewBroker()
	broker.Create(run.ID)

	runner.Run(ctx, wf, run, 0)

	deadLettersBeforeResume, err := store.GetDeadLetterRuns(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(deadLettersBeforeResume) != 1 || deadLettersBeforeResume[0].NodeID != "n3" {
		t.Fatalf("want exactly 1 dead-letter row for n3 before resume, got %+v", deadLettersBeforeResume)
	}

	failN3.Store(false)
	broker.Create(run.ID)
	runner.Resume(ctx, wf, run, 0, false)

	finalRun, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finalRun.Status != models.RunStatusSuccess {
		t.Fatalf("run status after resume = %s, want success", finalRun.Status)
	}

	deadLettersAfterResume, err := store.GetDeadLetterRuns(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(deadLettersAfterResume) != 0 {
		t.Fatalf("want 0 dead-letter rows once n3 succeeded, got %+v -- a stale row here would permanently require force to resume this run again, even for an unrelated later failure", deadLettersAfterResume)
	}
}

// TestResumeRefusesNodeStuckRunningWithoutForce is a regression test for a
// review finding: a node whose LATEST logged state is "running" -- a prior
// attempt started it and the process crashed before ever writing a
// terminal status -- used to fall through Resume's skip-on-success check
// (running != success) straight into unconditional re-execution, same as
// any ordinary failed node. Whether that node's real side effect (a
// payment, an email, a webhook post) actually fired before the crash is
// genuinely unknown, so Resume must refuse outright without force, the
// same fail-closed stance already taken for a payment-risk dead-letter
// row.
func TestResumeRefusesNodeStuckRunningWithoutForce(t *testing.T) {
	runner, store := newTestRunner(t)
	ctx := context.Background()

	var n2Hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n2Hits, 1)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	wf, err := store.CreateWorkflow(ctx, "Resume Stuck Running Test", fundedTestUser(t, store))
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

	run, err := store.CreateRun(ctx, wf.ID, "test", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a crash mid-node: n2's row was inserted (LogStatusRunning)
	// but the process died before ever writing a terminal status -- no
	// UpdateRunLog call at all, exactly what a real crash would leave
	// behind.
	if _, err := store.InsertRunLog(ctx, models.RunLog{RunID: run.ID, NodeID: "n2", NodeType: models.NodeTypeTool, Status: models.LogStatusRunning}); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishRun(ctx, run.ID, models.RunStatusFailed); err != nil {
		t.Fatal(err)
	}

	broker := sse.NewBroker()

	// Without force: refused outright, n2 never actually invoked again.
	broker.Create(run.ID)
	runner.Resume(ctx, wf, run, 0, false)
	if got := atomic.LoadInt32(&n2Hits); got != 0 {
		t.Fatalf("resume without force made %d requests to n2, want 0 (must refuse before executing anything)", got)
	}
	refusedRun, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if refusedRun.Status != models.RunStatusFailed {
		t.Fatalf("run status after refused resume = %s, want failed", refusedRun.Status)
	}

	// With force: actually attempts the node again.
	broker.Create(run.ID)
	runner.Resume(ctx, wf, run, 0, true)
	if got := atomic.LoadInt32(&n2Hits); got == 0 {
		t.Fatal("resume with force made 0 requests to n2, want at least 1 (must actually attempt the stuck node)")
	}
}
