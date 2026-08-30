package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/agentmesh/backend/internal/alert"
	"github.com/agentmesh/backend/internal/db"
	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/sse"
	"github.com/agentmesh/backend/internal/x402"
)

// X402Config bundles the platform-wallet/facilitator identity engine.Runner
// needs for run-level pre-funding (Task 5) — grouped into one struct rather
// than appended as more same-typed positional NewRunner params, so a future
// caller can't silently swap e.g. RelayNetwork and RelayFeePayer (both
// strings) without the compiler catching it.
type X402Config struct {
	PlatformWalletEncMnemonic string
	USDCAssetID               uint64
	FacilitatorClient         *x402.FacilitatorClient
	PlatformWalletAddress     string
	RelayNetwork              string
	RelayFeePayer             string
	MaxRelayOutboundUSDMicros int64
	// FrontendURL is our own branded origin, used (with the /api proxy
	// path) as the run-funding settlement's declared resource -- see
	// nodes.RunPreFundConfig.FrontendURL. Distinct from Runner.relayBaseURL,
	// which is the bare backend origin the engine actually dials.
	FrontendURL string
}

type Runner struct {
	store                    *db.Store
	broker                   *sse.Broker
	walletSvc                nodes.WalletSigner
	registry                 *runRegistry
	relayBaseURL             string
	platformSpendEncMnemonic string
	x402                     X402Config
	platformKeys             map[string]string
}

func NewRunner(
	store *db.Store,
	broker *sse.Broker,
	walletSvc nodes.WalletSigner,
	relayBaseURL string,
	platformSpendEncMnemonic string,
	x402Cfg X402Config,
) *Runner {
	return &Runner{
		store:                    store,
		broker:                   broker,
		walletSvc:                walletSvc,
		registry:                 newRunRegistry(),
		relayBaseURL:             relayBaseURL,
		platformSpendEncMnemonic: platformSpendEncMnemonic,
		x402:                     x402Cfg,
	}
}

// SetPlatformKeys installs AgentMesh's own provider API keys, used by
// Provider nodes with KeyMode == "platform". Optional — a Runner with no
// platform keys set simply errors (via resolveAPIKey) if a workflow tries
// to use platform-key mode, which is the correct behavior for every test
// harness and any deployment that hasn't configured PLATFORM_*_API_KEY.
func (r *Runner) SetPlatformKeys(keys map[string]string) {
	r.platformKeys = keys
}

// preflightCheck fails a node before it runs if wf.UserID can't cover
// amountUSDMicros. Blocks outright — no soft overage — matching the
// prepaid-only model already used for credit top-ups.
func (r *Runner) preflightCheck(ctx context.Context, wf models.Workflow, amountUSDMicros int64) error {
	balance, err := r.store.GetCreditBalance(ctx, wf.UserID)
	if err != nil {
		return err
	}
	if balance < amountUSDMicros {
		return fmt.Errorf("insufficient credits: balance %d micros, need %d micros", balance, amountUSDMicros)
	}
	return nil
}

// debitOrLog charges amountUSDMicros against wf.UserID for nodeID and just
// logs on failure rather than failing the node — the node already ran
// successfully by the time this is called, so there's nothing left to roll
// back (x402 payments in particular can't be undone once sent on-chain).
func (r *Runner) debitOrLog(ctx context.Context, wf models.Workflow, run models.Run, nodeID string, amountUSDMicros int64, kind string) {
	if err := r.store.DebitCredits(ctx, wf.UserID, amountUSDMicros, kind, wf.ID, run.ID, nodeID); err != nil {
		log.Printf("debit failed: user=%s workflow=%s run=%s node=%s kind=%s amount=%d: %v",
			wf.UserID, wf.ID, run.ID, nodeID, kind, amountUSDMicros, err)
	}
}

// ledgerCompensationTimeout bounds Commit/Release calls once they're
// detached from the triggering request's context (see newPaymentLedger) —
// long enough for a single locked UPDATE, short enough not to hang a
// terminating process indefinitely.
const ledgerCompensationTimeout = 10 * time.Second

// newPaymentLedger builds the reserve/commit/release closures a real
// on-chain tool402 payment (either dialect, standalone or agent-attached)
// uses to atomically decrement the user's balance at the moment a payment
// is committed to, before it's attempted — instead of checking balance and
// only debiting afterward, which would let multiple calls within the same
// node execution (an agent's sequential tool loop, or concurrent standalone
// tool402 nodes in the same topology level) all pass a check against the
// same stale balance and collectively overspend past what the user can
// cover. See nodes.PaymentLedger.
//
// Commit and Release are compensating actions for money that has already
// moved (or a reservation that must be undone) — they run with
// context.WithoutCancel, not the caller's cctx. If they inherited a
// cancelled/deadline-exceeded context (e.g. Runner.Stop firing mid-payment,
// or the outbound HTTP call timing out), the resulting DB call would be a
// no-op that neither writes the debit_ledger row nor restores the reserved
// balance, silently stranding the reservation as a permanent, unledgered
// credit loss. UpdateRunLog already establishes this same
// context.Background()-after-cancellation convention elsewhere in Run.
func (r *Runner) newPaymentLedger(wf models.Workflow, run models.Run) nodes.PaymentLedger {
	return nodes.PaymentLedger{
		Reserve: func(cctx context.Context, amountUSDMicros int64) error {
			return r.store.ReserveCredits(cctx, wf.UserID, amountUSDMicros)
		},
		Commit: func(cctx context.Context, nodeID string, amountUSDMicros int64, kind string) {
			bctx, cancel := context.WithTimeout(context.WithoutCancel(cctx), ledgerCompensationTimeout)
			defer cancel()
			if err := r.store.CommitReservedDebit(bctx, wf.UserID, amountUSDMicros, kind, wf.ID, run.ID, nodeID); err != nil {
				criticalAlert(wf, run, "commit reserved debit failed (balance already decremented, no ledger row written)", err, "node", nodeID, "kind", kind, "amount", amountUSDMicros)
			}
		},
		Release: func(cctx context.Context, amountUSDMicros int64) {
			bctx, cancel := context.WithTimeout(context.WithoutCancel(cctx), ledgerCompensationTimeout)
			defer cancel()
			if err := r.store.ReleaseReservedCredits(bctx, wf.UserID, amountUSDMicros); err != nil {
				criticalAlert(wf, run, "release reserved credits failed (balance permanently stranded)", err, "amount", amountUSDMicros)
			}
		},
	}
}

// criticalAlert logs and fires a CRITICAL payments alert with a consistent
// shape -- extracted from 6 near-identical hand-rolled fmt.Sprintf +
// log.Print + alert.Notify triplets scattered across this file (see
// newPaymentLedger's Commit/Release, newRunLevelLedger's Commit, and
// reserveAndFundRun's failure branches). fields are alternating key/value
// pairs (e.g. "amount", amountUSDMicros, "node", nodeID) appended to the
// message in order.
//
// A plain function, not a *Runner method: it never touches Runner state,
// and newRunLevelLedger (a free function, not a Runner method) needs to
// call it too.
func criticalAlert(wf models.Workflow, run models.Run, label string, err error, fields ...any) {
	parts := []string{fmt.Sprintf("CRITICAL: %s: user=%s workflow=%s run=%s", label, wf.UserID, wf.ID, run.ID)}
	for i := 0; i < len(fields); i += 2 {
		if i+1 < len(fields) {
			parts = append(parts, fmt.Sprintf("%v=%v", fields[i], fields[i+1]))
		} else {
			parts = append(parts, fmt.Sprintf("%v=<missing value>", fields[i]))
		}
	}
	if err != nil {
		parts = append(parts, fmt.Sprintf("err=%v", err))
	}
	msg := strings.Join(parts, " ")
	log.Print(msg)
	go alert.Notify(context.Background(), alert.ChannelPayments, msg)
}

// newRunLevelLedger builds an in-memory credit pool for a single run,
// atomically tracking reservations against a fixed budget instead of hitting
// the DB per-call. Reserve decrements the pool; Commit writes the permanent
// audit row (DB-backed, same as newPaymentLedger); Release credits back the
// in-memory balance (unlike newPaymentLedger, which also calls the DB). See
// nodes.PaymentLedger for the full contract.
func newRunLevelLedger(pool int64, wf models.Workflow, run models.Run, store *db.Store) (nodes.PaymentLedger, func() int64) {
	var mu sync.Mutex
	remaining := pool

	ledger := nodes.PaymentLedger{
		Reserve: func(_ context.Context, amountUSDMicros int64) error {
			mu.Lock()
			defer mu.Unlock()
			if amountUSDMicros > remaining {
				return fmt.Errorf("run pre-fund pool exhausted: need %d, %d left of %d reserved for this run: %w",
					amountUSDMicros, remaining, pool, db.ErrInsufficientCredits)
			}
			remaining -= amountUSDMicros
			return nil
		},
		Commit: func(cctx context.Context, nodeID string, amountUSDMicros int64, kind string) {
			bctx, cancel := context.WithTimeout(context.WithoutCancel(cctx), ledgerCompensationTimeout)
			defer cancel()
			if err := store.CommitReservedDebit(bctx, wf.UserID, amountUSDMicros, kind, wf.ID, run.ID, nodeID); err != nil {
				criticalAlert(wf, run, "commit reserved debit failed (run pre-fund pool already decremented, no ledger row written)", err, "node", nodeID, "kind", kind, "amount", amountUSDMicros)
			}
		},
		Release: func(_ context.Context, amountUSDMicros int64) {
			mu.Lock()
			defer mu.Unlock()
			remaining += amountUSDMicros
		},
	}
	return ledger, func() int64 {
		mu.Lock()
		defer mu.Unlock()
		return remaining
	}
}

// newRecordSettlement builds the RecordSettlement callback a run-funded
// agent's X402RelayConfig uses to audit each per-call outbound settlement.
// Runs on context.WithoutCancel, like every other compensating-write
// closure in this file (newPaymentLedger's Commit/Release,
// newRunLevelLedger's Commit) -- a real, already-signed Wallet 2 payment
// must have its audit row written even if the caller's context (e.g. the
// run's own ctx, cancelled by StopWorkflow) is already done by the time
// this runs.
func (r *Runner) newRecordSettlement(wf models.Workflow, run models.Run, fundingID string) func(ctx context.Context, target string, amountUSDMicros int64, settled bool) error {
	return func(cctx context.Context, target string, amountUSDMicros int64, settled bool) error {
		bctx, cancel := context.WithTimeout(context.WithoutCancel(cctx), ledgerCompensationTimeout)
		defer cancel()
		row, err := r.store.RecordRunFundedSettlement(bctx, fundingID, target, amountUSDMicros)
		if err != nil {
			return err
		}
		status := "failed"
		if settled {
			status = "settled"
		}
		return r.store.RecordOutboundSettlement(bctx, row.ID, "", status)
	}
}

// runFundResult bundles what reserveAndFundRun computes for one agent node:
// the ledger v2 tool402 dispatch should use, the run funding id ("" if no
// run-level pre-fund happened), the set of attached tool402 node IDs that
// were confirmed real v2 targets and folded into that pre-fund's estimate
// (so a legacy-dialect tool attached to the same run-funded agent can still
// be told apart — see X402RelayConfig.RunFundedToolIDs), and a cleanup func
// that releases whatever's left of the pool back to the DB balance at the
// end of the agent's turn.
type runFundResult struct {
	Ledger    nodes.PaymentLedger
	FundingID string
	// FundingTxID and FundedUSDMicros describe the real on-chain inbound
	// settlement FundingID refers to (Wallet 1 -> Wallet 2, the whole run's
	// tool budget in one payment). Both zero when no pre-fund happened.
	// They exist so the run's paid work is auditable from the UI: this is
	// the ONLY inbound settlement a run-funded agent makes, so nothing
	// downstream can reconstruct it per call.
	FundingTxID     string
	FundedUSDMicros int64
	FundedToolIDs   map[string]bool
	Cleanup         func(context.Context)
}

// reserveAndFundRun sizes and reserves a single run-level credit hold for
// agentNode's attached tool402 tools, then settles that exact amount as one
// real inbound x402 payment (Wallet 1 -> Wallet 2) before the agent's
// tool-calling loop starts. Size = sum of REAL, freshly-fetched quotes for
// each attached v2 tool402 node — never padded.
//
// An agent with no attached tool402 nodes, or only legacy-dialect ones,
// gets estimate=0 — a no-op returning the existing per-call
// newPaymentLedger and an empty runFundingID, so ExecuteAgent's tool402
// calls take the completely unmodified per-call public-relay path (the
// isV2 dispatch in ExecuteTool402V2 gates on runFundingID == "").
func (r *Runner) reserveAndFundRun(ctx context.Context, wf models.Workflow, run models.Run, attach models.AttachConfig) (runFundResult, error) {
	noFund := runFundResult{Ledger: r.newPaymentLedger(wf, run), Cleanup: func(context.Context) {}}

	var estimate int64
	fundedToolIDs := make(map[string]bool)
	for _, tool := range attach.Tools {
		if tool.Type != models.NodeTypeTool402 {
			continue
		}
		isV2, amount, err := nodes.ProbeX402Price(ctx, tool.Endpoint, tool.Method)
		if err != nil || !isV2 {
			continue // unreachable/legacy-dialect tools stay on their existing billing path
		}
		// Reject outright rather than silently excluding the tool: a quote
		// this far out of range is independent evidence something
		// adversarial is happening, and estimate += amount below would risk
		// overflowing an int64 negative, which store.ReserveCredits would
		// then read as a credit INCREASE instead of a decrease.
		if amount > models.MaxSingleX402QuoteUSDMicros {
			return runFundResult{}, fmt.Errorf("x402 run funding: tool %s quoted %d, exceeding the %d ceiling", tool.ID, amount, models.MaxSingleX402QuoteUSDMicros)
		}
		if estimate > math.MaxInt64-amount {
			return runFundResult{}, fmt.Errorf("x402 run funding: estimate overflow summing tool %s", tool.ID)
		}
		estimate += amount
		fundedToolIDs[tool.ID] = true
	}

	if estimate == 0 {
		return noFund, nil
	}

	// Same two-condition check executeTool402V2Relay (the old per-call relay
	// path) already makes before attempting anything: without a platform
	// spend wallet configured, neither FundRunReserve nor
	// PayTargetFromWallet2 can do anything real. Checked here, after sizing
	// the estimate above (an agent with only legacy-dialect/unreachable
	// tools attached needs no wallet at all — that path must keep probing
	// regardless of wallet config, exactly as it always has) but,
	// critically, before ReserveCredits — r.walletSvc's dynamic type not
	// satisfying USDCGroupSigner (a real, valid configuration: e.g. a
	// noopSigner test double, or a WalletSigner-only production wiring)
	// makes the type assertion below yield a nil usdcSigner, and calling a
	// method on it later would panic with no recover() in the run
	// goroutine — after ReserveCredits already ran, stranding credits on
	// top of the crash. Degrading gracefully here instead matches an agent
	// with no attached tool402 nodes at all.
	usdcSigner, _ := r.walletSvc.(nodes.USDCGroupSigner)
	if r.platformSpendEncMnemonic == "" || usdcSigner == nil || r.x402.FacilitatorClient == nil || r.x402.PlatformWalletAddress == "" {
		return noFund, nil
	}

	if err := r.store.ReserveCredits(ctx, wf.UserID, estimate); err != nil {
		return runFundResult{}, err
	}

	fundCfg := nodes.RunPreFundConfig{
		USDCSigner:               usdcSigner,
		PlatformSpendEncMnemonic: r.platformSpendEncMnemonic,
		Facilitator:              r.x402.FacilitatorClient,
		PlatformWalletAddress:    r.x402.PlatformWalletAddress,
		RelayNetwork:             r.x402.RelayNetwork,
		RelayFeePayer:            r.x402.RelayFeePayer,
		ExpectedAssetID:          r.x402.USDCAssetID,
		FrontendURL:              r.x402.FrontendURL,
	}
	txID, err := nodes.FundRunReserve(ctx, fundCfg, run.ID, estimate)
	if err != nil {
		if errors.Is(err, nodes.ErrSettlementIndeterminate) {
			// The settle response was lost -- we don't know whether the
			// payment actually went through. Releasing the reservation
			// here could refund a user for money that already left Wallet
			// 1. Hold it, alert, and fail the run so an operator can
			// reconcile by hand, matching the same "money might have
			// already moved" caution the RecordRunFunding-failure branch
			// below already applies at the next step in this flow.
			criticalAlert(wf, run, "run pre-fund settle response lost, fate unknown, reservation held", err, "amount", estimate)
			return runFundResult{}, fmt.Errorf("x402 run funding: settlement indeterminate, failing rather than risking a refund for money already sent: %w", err)
		}
		bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ledgerCompensationTimeout)
		defer cancel()
		if relErr := r.store.ReleaseReservedCredits(bctx, wf.UserID, estimate); relErr != nil {
			criticalAlert(wf, run, "run pre-fund failed AND release failed (balance stranded)", relErr, "amount", estimate, "fundErr", err)
		}
		return runFundResult{}, fmt.Errorf("x402 run funding failed: %w", err)
	}

	funding, err := r.store.RecordRunFunding(ctx, run.ID, txID, estimate)
	if err != nil {
		// Real money already moved on-chain — this is a bookkeeping failure,
		// not a payment failure. Do NOT release the DB reservation (the
		// on-chain settle genuinely happened); alert so an operator can
		// reconcile the missing audit row by hand. Do NOT fall back to
		// funding.ID's zero value ("") either -- that's the exact same
		// sentinel ExecuteTool402V2 reads as "no run-level pre-fund
		// happened for this run", which would silently route every
		// subsequent v2 tool402 call for this agent onto the OLD per-call
		// public-relay path. That path performs its own FULL inbound settle
		// per call, and a real bulk inbound settlement already just
		// happened above via FundRunReserve -- so Wallet 1 would pay twice
		// for the same run, exactly the double-settle bug this whole branch
		// exists to eliminate. Failing the node instead is safe: the money
		// is sitting in Wallet 2, our own wallet -- a state we can
		// reconcile by hand, unlike a silent double-spend.
		criticalAlert(wf, run, "run funding settled on-chain but RecordRunFunding failed", err, "txID", txID)
		return runFundResult{}, fmt.Errorf("run funding settled on-chain (tx %s) but recording it failed, failing the run rather than risking a double-settle on the old per-call path: %w", txID, err)
	}

	ledger, remaining := newRunLevelLedger(estimate, wf, run, r.store)
	cleanup := func(cctx context.Context) {
		unused := remaining()
		if unused <= 0 {
			return
		}
		bctx, cancel := context.WithTimeout(context.WithoutCancel(cctx), ledgerCompensationTimeout)
		defer cancel()
		if err := r.store.ReleaseReservedCredits(bctx, wf.UserID, unused); err != nil {
			criticalAlert(wf, run, "run-level release failed (balance permanently stranded)", err, "amount", unused)
		}
	}
	return runFundResult{
		Ledger:          ledger,
		FundingID:       funding.ID,
		FundingTxID:     funding.InboundTxID,
		FundedUSDMicros: estimate,
		FundedToolIDs:   fundedToolIDs,
		Cleanup:         cleanup,
	}, nil
}

// prependRunFundingReceipt folds the run's up-front funding settlement into
// an agent result's x402Payments list as its first entry, so it gets its own
// console row and DB log row through the same publish loop in Run() that
// every per-call receipt already goes through. No-op when the run was never
// pre-funded.
//
// The list order matters: this row carries the full amount that really
// settled on-chain, while the per-call receipts that follow repeat its tx id
// (their only inbound leg) with just their own slice of that amount — so a
// consumer de-duplicating by tx id keeps the accurate one by keeping the
// first.
func prependRunFundingReceipt(result map[string]any, rf runFundResult, node models.WorkflowNode, usdcAssetID uint64) {
	if rf.FundingTxID == "" {
		return
	}
	funded := map[string]any{
		"nodeId":           node.ID,
		"nodeName":         "run funding · " + node.Name,
		"settledUsdMicros": rf.FundedUSDMicros,
		"debitKind":        models.DebitKindX402RelayCost,
		"txId":             rf.FundingTxID,
		"amount":           fmt.Sprintf("%.6f", float64(rf.FundedUSDMicros)/1e6),
		"explorerURL":      nodes.ExplorerURLForAsset(usdcAssetID, rf.FundingTxID),
	}
	// []map[string]any is the concrete type Run()'s publish loop asserts on;
	// anything else there would silently drop every payment row.
	existing, _ := result["x402Payments"].([]map[string]any)
	result["x402Payments"] = append([]map[string]any{funded}, existing...)
}

// debitAgentFee charges the agent node's own LLM-call fee — the flat BYOK
// convenience fee, or the platform-key tier fee with usage recorded — and
// logs on failure rather than failing the node, same rationale as
// debitOrLog: the call already happened, there's nothing left to roll back.
// debitAgentFee charges an agent step. BYOK is free: the user is paying their
// own provider directly with their own key, so AgentMesh incurs no cost to
// pass on and takes no cut. Credits exist to cover what the platform actually
// spends on the user's behalf — platform-key LLM calls, and real x402
// settlements paid out of the platform wallets. Charging for BYOK billed
// users for compute they had already bought themselves.
func (r *Runner) debitAgentFee(ctx context.Context, wf models.Workflow, run models.Run, nodeID string, amountUSDMicros int64, platformMode bool, model string, tokensIn, tokensOut int) {
	if !platformMode {
		return
	}
	if err := r.store.DebitCreditsForPlatformLLM(ctx, wf.UserID, amountUSDMicros, wf.ID, run.ID, nodeID, model, tokensIn, tokensOut); err != nil {
		log.Printf("platform-key debit failed: user=%s workflow=%s run=%s node=%s model=%s amount=%d: %v",
			wf.UserID, wf.ID, run.ID, nodeID, model, amountUSDMicros, err)
	}
}

// Start creates a cancellable context for the run, registers it, and launches
// Run in a goroutine. Replaces the previous pattern of calling Run directly.
func (r *Runner) Start(wf models.Workflow, run models.Run) {
	ctx, cancel := context.WithCancel(context.Background())
	r.registry.register(wf.ID, cancel)
	go r.Run(ctx, wf, run)
}

// Stop cancels the active run for the given workflow ID. Returns false if no
// run was registered (i.e. the workflow is not currently running).
func (r *Runner) Stop(workflowID string) bool {
	return r.registry.cancel(workflowID)
}

// finishRun records the run's terminal status and fires a workflow-run audit-log
// notification. Centralized here so every terminal path (success, failed, stopped)
// reports to the same Discord channel with the same message shape.
func (r *Runner) finishRun(wf models.Workflow, run models.Run, status models.RunStatus) {
	r.store.FinishRun(context.Background(), run.ID, status)
	go alert.Notify(context.Background(), alert.ChannelWorkflows, fmt.Sprintf("workflow %q run %s finished: %s", wf.Name, run.ID, status))
}

// Run executes a workflow. Call via Start rather than directly.
func (r *Runner) Run(ctx context.Context, wf models.Workflow, run models.Run) {
	defer r.broker.Close(run.ID)
	defer r.registry.deregister(wf.ID)

	go alert.Notify(context.Background(), alert.ChannelWorkflows, fmt.Sprintf("workflow %q run %s started", wf.Name, run.ID))

	attachMap := BuildAttachMap(wf.Nodes, wf.Edges)
	levels, err := TopologicalSort(wf.Nodes, wf.Edges)
	if err != nil {
		r.finishRun(wf, run, models.RunStatusFailed)
		return
	}

	// Nodes attached to an agent are its resources, not steps of their own:
	// a tool is invoked by the agent's LLM via function calling, and a
	// provider is the model that agent runs on. Neither is a workflow step.
	//
	// This used to match only ToPort == "tools", which left a provider
	// attached to the "model" port executing as a standalone topology step —
	// and NodeTypeProvider's executeNode case simply returns rc.Message(), so
	// it surfaced in the console as a step that echoed the run's input back
	// verbatim (confirmed live 2026-08-02). Matching every attach edge fixes
	// that for both ports.
	agentToolIDs := make(map[string]bool)
	for _, e := range wf.Edges {
		if e.Kind == models.EdgeKindAttach {
			agentToolIDs[e.From] = true
		}
	}

	// Pre-load all agent wallets for this workflow so tool402 nodes can resolve
	// their parent agent's wallet without hitting the DB per-node.
	walletByAgent := make(map[string]models.AgentWallet)
	if wallets, err := r.store.ListAgentWallets(ctx, run.WorkflowID); err == nil {
		for _, w := range wallets {
			walletByAgent[w.AgentNodeID] = w
		}
	}

	var inputJSON []byte
	if run.InputContext != nil {
		inputJSON, _ = json.Marshal(run.InputContext)
	}
	rc := NewRunContext(run.ID, inputJSON)

	// Workflow variables are loaded once per run. A workflow with none
	// gets an empty map, and ExpandState is a no-op on every field, so
	// this costs one query and changes nothing else.
	if vars, err := r.store.GetWorkflowVariables(ctx, wf.ID); err == nil {
		rc.SetState(vars)
	} else {
		log.Printf("load workflow variables: workflow=%s run=%s: %v", wf.ID, run.ID, err)
	}

	var failed int32

	for stepIdx, level := range levels {
		// Check for cancellation between levels.
		if ctx.Err() != nil {
			r.finishRun(wf, run, models.RunStatusStopped)
			return
		}

		var wg sync.WaitGroup
		for _, node := range level {
			wg.Add(1)
			go func(n models.WorkflowNode, idx int) {
				defer wg.Done()
				// Skip attached tools — the agent invokes them via function calling.
				if agentToolIDs[n.ID] {
					return
				}
				if atomic.LoadInt32(&failed) != 0 {
					return
				}

				start := time.Now()
				logEntry, _ := r.store.InsertRunLog(ctx, models.RunLog{
					RunID:     run.ID,
					StepIndex: idx,
					NodeID:    n.ID,
					NodeType:  n.Type,
					Status:    models.LogStatusRunning,
				})

				result, execErr := r.executeNode(ctx, n, attachMap, walletByAgent, rc, run, wf)
				dur := int(time.Since(start).Milliseconds())

				if execErr != nil {
					atomic.StoreInt32(&failed, 1)
					outJSON, _ := json.Marshal(execErr.Error())
					r.store.UpdateRunLog(context.Background(), logEntry.ID, models.LogStatusFailed, outJSON, dur)
					r.broker.Publish(run.ID, models.LogEvent{
						StepIndex:  idx,
						NodeID:     n.ID,
						NodeType:   n.Type,
						Status:     models.LogStatusFailed,
						Output:     execErr.Error(),
						DurationMs: dur,
						Ts:         time.Now(),
					})
					return
				}

				rc.Set(n.ID, result)
				outJSON, _ := json.Marshal(result)
				r.store.UpdateRunLog(context.Background(), logEntry.ID, models.LogStatusSuccess, outJSON, dur)
				r.broker.Publish(run.ID, models.LogEvent{
					StepIndex:  idx,
					NodeID:     n.ID,
					NodeType:   n.Type,
					Status:     models.LogStatusSuccess,
					Output:     result,
					DurationMs: dur,
					Ts:         time.Now(),
				})
				// One log entry per x402 payment made inside the agent loop, so
				// each settlement's tx ids are visible as their own console
				// row. These are written to the DB as well as published:
				// broadcast-only events exist for as long as a live stream is
				// attached and no longer, so a dropped stream used to lose the
				// on-chain receipts for money that had really moved — the one
				// record a user most needs to audit a paid run.
				if m, ok := result.(map[string]any); ok {
					if payments, ok := m["x402Payments"].([]map[string]any); ok {
						for _, p := range payments {
							nodeID, _ := p["nodeId"].(string)
							ev := models.LogEvent{
								StepIndex:  idx,
								NodeID:     nodeID,
								NodeType:   models.NodeTypeTool402,
								Status:     models.LogStatusSuccess,
								Output:     p,
								DurationMs: 0,
								Ts:         time.Now(),
							}
							payJSON, _ := json.Marshal(p)
							if entry, err := r.store.InsertRunLog(ctx, models.RunLog{
								RunID:     run.ID,
								StepIndex: idx,
								NodeID:    nodeID,
								NodeType:  models.NodeTypeTool402,
								Status:    models.LogStatusRunning,
							}); err == nil {
								r.store.UpdateRunLog(context.Background(), entry.ID, models.LogStatusSuccess, payJSON, 0)
							}
							r.broker.Publish(run.ID, ev)
						}
					}
				}
			}(node, stepIdx)
		}
		wg.Wait()

		if atomic.LoadInt32(&failed) != 0 {
			r.finishRun(wf, run, models.RunStatusFailed)
			return
		}
	}

	r.finishRun(wf, run, models.RunStatusSuccess)
}

func (r *Runner) executeNode(
	ctx context.Context,
	node models.WorkflowNode,
	attachMap map[string]models.AttachConfig,
	walletByAgent map[string]models.AgentWallet,
	rc *RunContext,
	run models.Run,
	wf models.Workflow,
) (any, error) {
	// Resolve {{state.x}} references against this run's snapshot of the
	// workflow's persisted variables. Applied to a fixed, explicit set of
	// user-authored fields; ExpandState is the identity function on any
	// string without a literal "{{state." in it, so a workflow that does
	// not use state produces byte-identical requests to before.
	//
	// Deliberately NOT expanded: APIKey, EmailAPIKey, Secrets and Config —
	// a credential must never be assembled out of mutable state.
	//
	// node is a value copy (executeNode takes models.WorkflowNode by
	// value), so mutating it here cannot affect wf.Nodes or any other
	// node's view of the graph.
	if state := rc.State(); len(state) > 0 {
		node.URL = nodes.ExpandState(node.URL, state)
		node.Endpoint = nodes.ExpandState(node.Endpoint, state)
		node.SystemPrompt = nodes.ExpandState(node.SystemPrompt, state)
		node.BodyTemplate = nodes.ExpandState(node.BodyTemplate, state)
		node.EmailTo = nodes.ExpandState(node.EmailTo, state)
		node.EmailSubject = nodes.ExpandState(node.EmailSubject, state)
		node.EmailBody = nodes.ExpandState(node.EmailBody, state)
		if len(node.ParamDefaults) > 0 {
			expanded := make(map[string]string, len(node.ParamDefaults))
			for k, v := range node.ParamDefaults {
				expanded[k] = nodes.ExpandState(v, state)
			}
			node.ParamDefaults = expanded
		}
	}

	switch node.Type {
	case models.NodeTypeTrigger:
		return rc.input, nil
	case models.NodeTypeEnd:
		return rc.Message(), nil
	case models.NodeTypeState:
		result, err := nodes.ExecuteState(ctx, node, wf.ID, r.store, rc, rc.State())
		if err != nil {
			return nil, err
		}
		// Reflect the write into this run's snapshot so a later node in
		// the same run reads what this one just stored, without a second
		// round-trip to the database.
		if sr, ok := result.(nodes.StateResult); ok {
			if sr.Op == "delete" {
				rc.setStateKey(sr.Key, nil)
			} else {
				rc.setStateKey(sr.Key, sr.Value)
			}
		}
		return result, nil
	case models.NodeTypeAgent:
		provider := attachMap[node.ID].Provider
		platformMode := provider != nil && provider.KeyMode == "platform"

		// BYOK costs the platform nothing, so it is neither gated on credits
		// nor charged (see debitAgentFee). A zero preflight amount always
		// passes, which is the point: a user running purely on their own API
		// key should never be blocked by an empty balance.
		var agentFeeUSDMicros int64
		var resolvedModel string
		if platformMode {
			resolvedModel = nodes.ResolveModel(provider.Template, provider.Model)
			agentFeeUSDMicros = nodes.PlatformKeyFeeUSDMicros(nodes.ModelTier(provider.Template, resolvedModel))
		}

		if err := r.preflightCheck(ctx, wf, agentFeeUSDMicros); err != nil {
			return nil, err
		}
		aw := walletByAgent[node.ID]
		checkBalance := func(cctx context.Context, amount int64) error {
			return r.preflightCheck(cctx, wf, amount)
		}
		attach := attachMap[node.ID]
		rf, err := r.reserveAndFundRun(ctx, wf, run, attach)
		if err != nil {
			return nil, err
		}
		defer rf.Cleanup(ctx)

		// r.walletSvc's dynamic type (*wallet.Service) also satisfies
		// USDCGroupSigner (same nil-safe assertion as the NodeTypeTool402
		// case below) — an agent-attached tool402 call routes through the
		// same relay/Wallet 1 path as a standalone one.
		usdcSigner, _ := r.walletSvc.(nodes.USDCGroupSigner)
		relayCfg := nodes.X402RelayConfig{
			USDCSigner:               usdcSigner,
			PlatformSpendEncMnemonic: r.platformSpendEncMnemonic,
			ExpectedAssetID:          r.x402.USDCAssetID,
			RelayBaseURL:             r.relayBaseURL,
			Ledger:                   nodes.RunLedger(rf.Ledger),
			// LegacyLedger is always the original per-call, DB-backed
			// ledger — never rf.Ledger, which is the run-level in-memory
			// pool once the agent is run-funded. Legacy-dialect billing
			// must be identical whether or not this same agent also has a
			// run-funded v2 tool attached (see X402RelayConfig.LegacyLedger).
			LegacyLedger:     nodes.CallLedger(r.newPaymentLedger(wf, run)),
			RunFundingID:     rf.FundingID, // "" => existing unmodified per-call public-relay path
			RunFundingTxID:   rf.FundingTxID,
			RunFundedToolIDs: rf.FundedToolIDs,
			Wallet2: nodes.Wallet2PayConfig{
				USDCSigner:                usdcSigner,
				PlatformWalletEncMnemonic: r.x402.PlatformWalletEncMnemonic,
				USDCAssetID:               r.x402.USDCAssetID,
				RelayNetwork:              r.x402.RelayNetwork,
				MaxRelayOutboundUSDMicros: r.x402.MaxRelayOutboundUSDMicros,
			},
			RecordSettlement: r.newRecordSettlement(wf, run, rf.FundingID),
		}
		result, err := nodes.ExecuteAgent(ctx, node, attach, aw, r.walletSvc, rc, checkBalance, r.platformKeys, relayCfg)
		if err != nil {
			// A *nodes.ErrBalanceBlocked failure means the agent's own LLM
			// turn already completed and only ran into insufficient balance
			// when it tried an attached call — the agent's own flat fee is
			// still owed. Any other error (e.g. LLM connectivity failure)
			// means the agent turn itself never completed, so nothing is
			// billed, matching the pre-existing behavior for those failures.
			var blocked *nodes.ErrBalanceBlocked
			if errors.As(err, &blocked) {
				r.debitAgentFee(ctx, wf, run, node.ID, agentFeeUSDMicros, platformMode, resolvedModel, 0, 0)
			}
			return nil, err
		}
		var tokensIn, tokensOut int
		if m, ok := result.(map[string]any); ok {
			if usage, ok := m["platformKeyUsage"].(map[string]any); ok {
				tokensIn, _ = usage["tokensIn"].(int)
				tokensOut, _ = usage["tokensOut"].(int)
			}
		}
		r.debitAgentFee(ctx, wf, run, node.ID, agentFeeUSDMicros, platformMode, resolvedModel, tokensIn, tokensOut)
		if m, ok := result.(map[string]any); ok {
			// x402Payments entries are already reserved+committed via
			// relayCfg.Ledger from inside ExecuteAgent's tool-calling loop, at
			// the moment each payment settled — not batched here. Batching the
			// debit until after the whole agent turn completes would let every
			// iteration of the loop check the same stale balance and
			// collectively overspend past what the user can cover; see
			// newPaymentLedger. This entry is retained in the result only so
			// Run() can still publish a log/SSE event per payment below.
			if nodeIDs, ok := m["billedFlatFeeNodeIds"].([]string); ok {
				for _, nodeID := range nodeIDs {
					r.debitOrLog(ctx, wf, run, nodeID, models.ByokFlatFeeUSDMicros, models.DebitKindByokFlatFee)
				}
			}
			// The run-level pre-fund is a real inbound settlement of its own
			// (Wallet 1 -> Wallet 2, the run's whole tool budget in one
			// payment) with no node of its own to report it. Folding it in as
			// the FIRST x402Payments entry gives it its own console row and DB
			// log row through the existing publish loop in Run(), carrying the
			// amount that genuinely moved on-chain. The per-call receipts
			// repeat this same tx id (it is their only inbound leg) but each
			// carries just its own slice of the amount, so this row is the one
			// a settlements view should key on.
			//
			// Purely a reporting entry: unlike the per-call receipts it is NOT
			// billed here or anywhere below. reserveAndFundRun already
			// reserved the full amount against the user's balance before this
			// agent ran, and rf.Cleanup releases whatever went unspent.
			prependRunFundingReceipt(m, rf, node, r.x402.USDCAssetID)
		} else if rf.FundingTxID != "" {
			// A run-funded agent whose own result is not a map has no
			// x402Payments field to attach to (it never actually paid for a
			// tool — rf.Cleanup releases the unspent pre-fund). The
			// settlement itself is still recorded in x402_run_fundings; only
			// the console row is skipped.
			log.Printf("x402: run %s funded on-chain (tx %s) but agent node %s returned a non-map result, so no console row was emitted",
				run.ID, rf.FundingTxID, node.ID)
		}
		return result, nil
	case models.NodeTypeProvider:
		return rc.Message(), nil
	case models.NodeTypeTool:
		billable := nodes.BillableFlatFee(node.Type, node.Template)
		if billable {
			if err := r.preflightCheck(ctx, wf, models.ByokFlatFeeUSDMicros); err != nil {
				return nil, err
			}
		}
		result, err := nodes.ExecuteTool(ctx, node, rc)
		if err != nil {
			return nil, err
		}
		if billable {
			r.debitOrLog(ctx, wf, run, node.ID, models.ByokFlatFeeUSDMicros, models.DebitKindByokFlatFee)
		}
		return result, nil
	case models.NodeTypeTool402:
		// Find the agent that has this tool attached and use its wallet (only
		// the legacy direct-pay dialect still needs this; the relay dialect
		// pays from the platform's own Wallet 1 spend wallet instead).
		var aw models.AgentWallet
		for agentID, cfg := range attachMap {
			for _, t := range cfg.Tools {
				if t.ID == node.ID {
					aw = walletByAgent[agentID]
				}
			}
		}
		// r.walletSvc's dynamic type (*wallet.Service) also satisfies
		// USDCGroupSigner (Task 3); the assertion is nil-safe if a test double
		// only implements WalletSigner, and ExecuteTool402V2 falls back to a
		// graceful "no wallet configured" result rather than paying via relay.
		usdcSigner, _ := r.walletSvc.(nodes.USDCGroupSigner)
		// Cheap, conservative guard before any network call to node.Endpoint —
		// see the matching comment in provider.go's executeFunctionCall. The
		// real, exact-amount reservation happens inside ExecuteTool402V2 via
		// ledger below.
		if err := r.preflightCheck(ctx, wf, models.X402PlatformFeeUSDMicros); err != nil {
			return nil, err
		}
		// A standalone tool402 node is never run-funded (that only ever
		// applies to an agent's attached tools), so Ledger and LegacyLedger
		// are the same DB-backed, per-call ledger here — both fields are
		// still populated so ExecuteTool402V2's legacy-dialect branch (which
		// only ever reads LegacyLedger) works identically to the v2 branch
		// (which only ever reads Ledger).
		standaloneLedger := r.newPaymentLedger(wf, run)
		relayCfg := nodes.X402RelayConfig{
			USDCSigner:               usdcSigner,
			PlatformSpendEncMnemonic: r.platformSpendEncMnemonic,
			ExpectedAssetID:          r.x402.USDCAssetID,
			RelayBaseURL:             r.relayBaseURL,
			Ledger:                   nodes.RunLedger(standaloneLedger),
			LegacyLedger:             nodes.CallLedger(standaloneLedger),
		}
		paymentResult, err := nodes.ExecuteTool402V2(ctx, node, rc, aw, r.walletSvc, relayCfg)
		if err != nil {
			return nil, err
		}
		// Already reserved+committed via ledger inside ExecuteTool402V2, at
		// the moment the payment settled — see newPaymentLedger.
		return paymentResult.Response, nil
	case models.NodeTypeAction:
		billable := nodes.BillableFlatFee(node.Type, node.Template)
		if billable {
			if err := r.preflightCheck(ctx, wf, models.ByokFlatFeeUSDMicros); err != nil {
				return nil, err
			}
		}
		result, err := nodes.ExecuteAction(ctx, node, rc)
		if err != nil {
			if errors.Is(err, nodes.ErrActionSkipped) {
				return result, nil
			}
			return nil, err
		}
		if billable {
			r.debitOrLog(ctx, wf, run, node.ID, models.ByokFlatFeeUSDMicros, models.DebitKindByokFlatFee)
		}
		return result, nil
	default:
		return nil, nil
	}
}
