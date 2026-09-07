package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/agentmesh/backend/internal/tendril"
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
	encryptionKey            string
	x402                     X402Config
	platformKeys             map[string]string
	tendrilClient            *tendril.Client
	tendrilSession           *tendril.Session
	googleClientID           string
	googleClientSecret       string
	// runBilling accumulates each run's non-tool402 billable total (run.ID
	// -> *int64, in USD micros) so it can be settled as one lump-sum x402
	// payment at the end of Run -- see addRunBilling and settleRunTotal.
	// Real tool402 spend is deliberately excluded (see addRunBilling's own
	// filtering): it already gets its own on-chain settlement.
	runBilling sync.Map
}

func NewRunner(
	store *db.Store,
	broker *sse.Broker,
	walletSvc nodes.WalletSigner,
	relayBaseURL string,
	platformSpendEncMnemonic string,
	encryptionKey string,
	x402Cfg X402Config,
) *Runner {
	return &Runner{
		store:                    store,
		broker:                   broker,
		walletSvc:                walletSvc,
		registry:                 newRunRegistry(),
		relayBaseURL:             relayBaseURL,
		platformSpendEncMnemonic: platformSpendEncMnemonic,
		encryptionKey:            encryptionKey,
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
	nodes.SetPlatformKeys(keys)
}

// SetTendril supplies the Tendril registry client and the Wallet 2 session
// that reads the shared credit pool. Left nil when TENDRIL_REGISTRY_URL is
// unset, in which case tendril nodes fail closed.
func (r *Runner) SetTendril(client *tendril.Client, session *tendril.Session) {
	r.tendrilClient = client
	r.tendrilSession = session
}

// SetGoogleOAuth supplies the same GOOGLE_CLIENT_ID/SECRET already used for
// the sign-in-with-Google login flow (handlers/oauth.go) -- reused
// deliberately, not a separate app registration, so a deployment that
// already has Google login configured gets Gmail/Sheets/Calendar/Drive
// nodes "for free" once those APIs and scopes are enabled on that same
// Google Cloud project. Left unset ("", ""), Google connector nodes fail
// closed with a clear error, the same pattern as SetTendril above.
func (r *Runner) SetGoogleOAuth(clientID, clientSecret string) {
	r.googleClientID = clientID
	r.googleClientSecret = clientSecret
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
		return // the DB was never actually charged -- don't settle it on-chain either
	}
	// Always a BYOK flat fee (the only kind debitOrLog is ever called with),
	// never a tool402 kind -- safe to accumulate unconditionally.
	r.addRunBilling(run.ID, amountUSDMicros)
}

// addRunBilling folds amountUSDMicros into the running total settleRunTotal
// will settle on-chain once, at the end of the run -- see runBilling's own
// doc comment. A no-op when runID isn't currently tracked (e.g. a unit test
// calling this helper's callers directly without going through Run()).
func (r *Runner) addRunBilling(runID string, amountUSDMicros int64) {
	if v, ok := r.runBilling.Load(runID); ok {
		atomic.AddInt64(v.(*int64), amountUSDMicros)
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
				return // the DB ledger row was never actually written -- don't settle it on-chain either
			}
			// Real tool402 spend (standalone or agent-attached, either
			// dialect, including Tendril's own gate fee -- which is routed
			// through this same closure via ExecuteTool402V2) already gets
			// its own on-chain settlement -- the run-level pre-fund or the
			// per-call relay/legacy path -- so accumulating it here too
			// would double-settle the same money. Everything else this
			// shared ledger commits (agent-attached http/action flat fees)
			// has no on-chain leg of its own yet, so it belongs in the
			// run-total settlement. Tendril's own lease/rent cost never
			// reaches this closure at all -- it's charged against a wholly
			// separate Tendril-credit pool via Store.ChargeTendrilCredit,
			// not this one.
			if kind != models.DebitKindX402RelayCost && kind != models.DebitKindX402PlatformFee {
				r.addRunBilling(run.ID, amountUSDMicros)
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

// isAgentFeeOwedDespiteFailure reports whether err means the agent's own
// LLM turn already completed -- so its flat fee is still owed -- before
// the node's overall execution failed. True for a failure that happens
// DURING that turn's tool-calling loop, wherever it implements
// nodes.AgentFeeOwedError (currently *nodes.ErrBalanceBlocked, an attached
// call blocked by insufficient balance before it could run, and
// *nodes.ErrPaymentAlreadyCommitted, one that already signed and sent a
// real payment before a downstream failure) -- dispatched via errors.As
// against that interface rather than a hand-maintained list of concrete
// types, so a FUTURE payment-adjacent error occurring mid-agent-turn is
// picked up correctly just by implementing the interface, not by also
// remembering to add a branch here (see AgentFeeOwedError's own doc
// comment in billing.go for why that's worth avoiding). False for
// anything meaning the turn itself never ran -- an LLM connectivity error,
// or the run-level pre-fund step (reserveAndFundRun) failing before the
// agent's loop ever started, which is a real payment-risk case too but
// not one where an LLM turn happened to bill for.
func isAgentFeeOwedDespiteFailure(err error) bool {
	var feeOwed nodes.AgentFeeOwedError
	return errors.As(err, &feeOwed)
}

// isPaymentRisk reports whether err means real money may already have
// moved for this node's attempt, regardless of whether an agent's LLM turn
// was involved -- used to flag a dead-letter row (DeadLetterRun.PaymentRisk)
// so Resume refuses to silently retry (and potentially re-pay) it without
// an explicit force. Broader than isAgentFeeOwedDespiteFailure: also true
// for nodes.ErrSettlementIndeterminate, the run-level pre-fund settle
// response being lost before any node (agent or otherwise) even started.
func isPaymentRisk(err error) bool {
	return isAgentFeeOwedDespiteFailure(err) || errors.Is(err, nodes.ErrSettlementIndeterminate)
}

// nodeMayHaveRealSideEffect reports whether a node with this type/
// template/method is one whose execution can move real money, or trigger
// some other real external effect (an email, a Slack/webhook POST, an
// agent's LLM call), that must never be silently repeated.
// execute()'s skip-on-prior-success check (below) uses this to decide
// whether a node's config-staleness check even applies: for one of these,
// an already-succeeded node is skipped unconditionally on resume,
// regardless of whether its config has changed since -- re-executing one
// under new config could otherwise repeat a real payment/send under
// different settings without the kind of explicit review this codebase's
// payment-risk `force` gate already requires for a comparable decision
// elsewhere in this file. A user who wants a payment-adjacent node's
// edited config to actually take effect needs a fresh Run, not a Resume.
//
// Mirrors nodes.BillableFlatFee's own node-type/template cases (the same
// types that move money or trigger a real external effect), plus
// NodeTypeTool402 and NodeTypeTendril, which BillableFlatFee deliberately
// excludes for its own (billing-mechanism) reasons documented on it, but
// which absolutely must not be re-executed here either -- both charge
// real money at runtime.
//
// A NodeTypeTool node is billable (hence flagged by BillableFlatFee) for
// exactly two templates, "http" and "websearch", but that's a BILLING
// distinction, not a SIDE-EFFECT one -- confirmed as a real gap in
// review: an "http" GET node (or "websearch", a read-only paid search
// call) is idempotent, so re-executing it under a corrected URL doesn't
// repeat any real external effect, it just runs the CORRECTED request
// that should have run the first time -- exactly what config-staleness
// detection exists to let happen. Only re-executing a genuinely
// non-idempotent "http" method (POST/PATCH) risks repeating a real write
// under new config, so "http" is exempted here ONLY when method is
// non-idempotent; "websearch" (no method of its own, always a read) is
// never exempted on that basis alone.
func nodeMayHaveRealSideEffect(nodeType models.NodeType, template, method string) bool {
	if nodeType == models.NodeTypeTool && template == "http" {
		return !nodes.IsIdempotentHTTPMethod(method)
	}
	return nodes.BillableFlatFee(nodeType, template) ||
		nodeType == models.NodeTypeTool402 ||
		nodeType == models.NodeTypeTendril
}

// nodeConfigHash returns a stable hash of node's functionally-relevant
// configuration, used by execute()'s skip-on-prior-success check (above)
// to detect a config edit made between a node's prior success and a
// later Resume of the same run -- without this, that check reused a
// node's stale prior output unconditionally, even when its config had
// since changed to something that would produce different output or call
// a different endpoint entirely (confirmed as a real gap in review: an
// upstream node silently re-fed downstream steps its OLD output after
// being edited, with no error).
//
// Deliberately hashes a hand-picked SUBSET of WorkflowNode's fields, not
// the whole struct marshaled as-is, excluding:
//   - ID, X, Y, Name, Label, Icon, Description: identity/canvas-position/
//     cosmetic fields that never affect what running the node does
//   - APIKey, EmailAPIKey, Secrets: independently encrypted at rest with
//     a fresh nonce on every save (see encryptNodes/maskNodes/decryptNodes
//     in handlers/workflows.go) -- their CIPHERTEXT changes on every
//     ordinary autosave even when the underlying plaintext doesn't, so
//     including them would make this hash change on every save regardless
//     of any real edit, defeating the whole point of it
//   - MaxRetries, RetryBackoffMs: retry POLICY, not what the node DOES --
//     irrelevant to whether its output would differ
//
// Every field that actually determines what a node calls or computes is
// included. json.Marshal on a struct with fixed field order (not a map)
// is deterministic, and Go's encoding/json already sorts map keys
// (ParamDefaults/Config) alphabetically, so this hash is stable across
// repeated calls for identical input regardless of in-memory map
// iteration order.
func nodeConfigHash(n models.WorkflowNode) string {
	relevant := struct {
		Type             models.NodeType
		Template         string
		SystemPrompt     string
		Wallet           string
		Balance          string
		Model            string
		KeyMode          string
		URL              string
		Method           string
		Endpoint         string
		Price            string
		Unit             string
		Provider         string
		Source           string
		EmailTo          string
		EmailFrom        string
		EmailSubject     string
		EmailBody        string
		EmailProvider    string
		DiscoveredParams []models.ParamDef
		ParamDefaults    map[string]string
		CustomParams     []models.CustomParam
		BodyMode         string
		BodyTemplate     string
		Config           map[string]string
		TendrilAction    string
		TendrilNodeID    string
		TendrilHours     string
		TendrilAmount    string
		StateOp          string
		StateKey         string
		StateValue       string
	}{
		Type: n.Type, Template: n.Template, SystemPrompt: n.SystemPrompt,
		Wallet: n.Wallet, Balance: n.Balance, Model: n.Model, KeyMode: n.KeyMode,
		URL: n.URL, Method: n.Method, Endpoint: n.Endpoint, Price: n.Price,
		Unit: n.Unit, Provider: n.Provider, Source: n.Source,
		EmailTo: n.EmailTo, EmailFrom: n.EmailFrom, EmailSubject: n.EmailSubject,
		EmailBody: n.EmailBody, EmailProvider: n.EmailProvider,
		DiscoveredParams: n.DiscoveredParams, ParamDefaults: n.ParamDefaults,
		CustomParams: n.CustomParams, BodyMode: n.BodyMode, BodyTemplate: n.BodyTemplate,
		Config: n.Config, TendrilAction: n.TendrilAction, TendrilNodeID: n.TendrilNodeID,
		TendrilHours: n.TendrilHours, TendrilAmount: n.TendrilAmount,
		StateOp: n.StateOp, StateKey: n.StateKey, StateValue: n.StateValue,
	}
	b, err := json.Marshal(relevant)
	if err != nil {
		// Every field above is a string, a slice of a plain struct, or a
		// map[string]string -- json.Marshal cannot fail on this shape in
		// practice. If it somehow did, "" is the SAFE direction: the skip
		// check above treats an empty ConfigHash as "no signal, assume
		// unchanged" (matching a legacy pre-migration row), never as
		// "definitely changed" -- an unexpected marshal failure must not
		// itself force every node in every run to needlessly re-execute.
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
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
// selfSettleConfig reports whether this Runner can settle a real Wallet 1 ->
// Wallet 2 x402 payment at all, and if so builds the config both
// reserveAndFundRun (tool402 pre-fund) and settleRunTotal (end-of-run lump
// sum) need to do it. Shared so the two call sites can't drift on the
// four-condition check a real settlement depends on. ok is false when any
// piece is missing -- no platform spend wallet configured, r.walletSvc's
// dynamic type not satisfying USDCGroupSigner (a real, valid configuration:
// e.g. a noopSigner test double, or a WalletSigner-only production wiring),
// no facilitator client, or no platform wallet address -- in which case
// callers should degrade to a no-op rather than attempt a settlement that
// can't succeed.
func (r *Runner) selfSettleConfig() (cfg nodes.RunPreFundConfig, usdcSigner nodes.USDCGroupSigner, ok bool) {
	usdcSigner, _ = r.walletSvc.(nodes.USDCGroupSigner)
	if r.platformSpendEncMnemonic == "" || usdcSigner == nil || r.x402.FacilitatorClient == nil || r.x402.PlatformWalletAddress == "" {
		return nodes.RunPreFundConfig{}, nil, false
	}
	return nodes.RunPreFundConfig{
		USDCSigner:               usdcSigner,
		PlatformSpendEncMnemonic: r.platformSpendEncMnemonic,
		Facilitator:              r.x402.FacilitatorClient,
		PlatformWalletAddress:    r.x402.PlatformWalletAddress,
		RelayNetwork:             r.x402.RelayNetwork,
		RelayFeePayer:            r.x402.RelayFeePayer,
		ExpectedAssetID:          r.x402.USDCAssetID,
		FrontendURL:              r.x402.FrontendURL,
	}, usdcSigner, true
}

// settleRunTotal fires once per run (see Run's deferred call), settling
// whatever addRunBilling accumulated for this run -- every non-tool402
// billable amount committed during the run -- as one real, additive Wallet 1
// -> Wallet 2 x402 payment. Called after the run has already finished and
// finishRun has already recorded its status, so a failure here never fails
// the run: the user's credit balance was already correctly debited per node
// as it ran, and this is purely a missing on-chain receipt, not a billing
// error. total <= 0 (no billable non-tool402 work happened, e.g. a
// trigger-only workflow, or a run with only tool402 nodes) is a no-op, same
// as no platform wallet being configured.
func (r *Runner) settleRunTotal(ctx context.Context, wf models.Workflow, run models.Run, total *int64) {
	amount := atomic.LoadInt64(total)
	if amount <= 0 {
		return
	}
	fundCfg, _, ok := r.selfSettleConfig()
	if !ok {
		return
	}
	txID, err := nodes.SettleRunTotal(ctx, fundCfg, amount)
	if err != nil {
		criticalAlert(wf, run, "run total settlement failed (run already finished, DB billing already correct -- this is a missing on-chain receipt only)", err, "amount", amount)
		return
	}
	// Detached with its own budget, like every other compensating write in
	// this file. The settle above has already moved real money on-chain, but
	// ctx here is bounded by SelfSettleRetryBudget -- which a worst-case
	// retry sequence can consume in full, leaving this write no time at all
	// and producing an on-chain payment with no DB record of it. Inheriting
	// ctx only ever appeared to work because the budget used to be
	// over-provisioned past its own worst case; that slack was accidental,
	// not a guarantee, and is exactly what a compensating write must not
	// depend on.
	bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ledgerCompensationTimeout)
	defer cancel()
	if _, err := r.store.RecordRunFunding(bctx, run.ID, txID, amount); err != nil {
		criticalAlert(wf, run, "run total settled on-chain but RecordRunFunding failed", err, "txID", txID, "amount", amount)
	}
}

type runFundResult struct {
	Ledger nodes.PaymentLedger
	// MarkupLedger is a second, separately-sized in-memory pool for the
	// platform's flat per-call markup — see X402RelayConfig.MarkupLedger
	// for why this must stay a distinct pool from Ledger rather than one
	// pool sized estimate+markupTotal.
	MarkupLedger nodes.PaymentLedger
	FundingID    string
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
// tool-calling loop starts. estimate = sum of REAL, freshly-fetched vendor
// quotes for each attached v2 tool402 node — never padded. creditReserve =
// estimate plus one flat platform markup (models.X402PlatformFeeUSDMicros)
// per funded tool, and is what's held against the user's CREDIT balance AND
// what FundRunReserve actually settles on-chain: the whole run's margin
// lands in Wallet 2 in this one up-front payment, same as every real vendor
// cost, so nothing here is a pure ledger fiction with no backing transfer.
// executeTool402RunLevel's own per-call spend still only ever draws
// `estimate` worth out to vendors (vendorLedger below), never touching the
// markup portion sitting in Wallet 2.
//
// An agent with no attached tool402 nodes, or only legacy-dialect ones,
// gets estimate=0 — a no-op returning the existing per-call
// newPaymentLedger and an empty runFundingID, so ExecuteAgent's tool402
// calls take the completely unmodified per-call public-relay path (the
// isV2 dispatch in ExecuteTool402V2 gates on runFundingID == "").
func (r *Runner) reserveAndFundRun(ctx context.Context, wf models.Workflow, run models.Run, attach models.AttachConfig) (runFundResult, error) {
	noFund := runFundResult{Ledger: r.newPaymentLedger(wf, run), Cleanup: func(context.Context) {}}

	var estimate int64
	var fundedToolCount int64
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
		fundedToolCount++
		fundedToolIDs[tool.ID] = true
	}

	if estimate == 0 {
		return noFund, nil
	}

	// creditReserve is what's actually held against the user's CREDIT
	// balance and settled on-chain -- estimate (real vendor cost) plus one
	// flat markup per funded tool, matching the total executeTool402RunLevel
	// reserves/commits per call (see its own total/amount split).
	// FundRunReserve below settles this FULL amount, not estimate alone: the
	// whole point is that Wallet 2 actually receives everything the user was
	// charged in one real transaction, not just the vendor-cost portion --
	// see SettlePlatformFee's doc comment for why the per-call (non-run-
	// funded) path needs a second dedicated transaction to reach the same
	// end state, which this single up-front settlement gets for free.
	markupTotal := fundedToolCount * models.X402PlatformFeeUSDMicros
	if markupTotal/models.X402PlatformFeeUSDMicros != fundedToolCount || estimate > math.MaxInt64-markupTotal {
		return runFundResult{}, fmt.Errorf("x402 run funding: credit reserve overflow (estimate %d, %d funded tools)", estimate, fundedToolCount)
	}
	creditReserve := estimate + markupTotal

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
	fundCfg, _, ok := r.selfSettleConfig()
	if !ok {
		return noFund, nil
	}

	if err := r.store.ReserveCredits(ctx, wf.UserID, creditReserve); err != nil {
		return runFundResult{}, err
	}

	// NOT detached from ctx (no WithoutCancel) -- FundRunReserve only
	// shields the one narrow, actually-unsafe-to-interrupt moment (the
	// Settle HTTP call itself) internally, via its own detached
	// sub-context -- see selfSettleWallet1ToWallet2/attemptSelfSettle's doc
	// comments. A StopWorkflow can still land promptly during signing,
	// Verify, or between retry attempts; SelfSettleRetryBudget below is
	// just a backstop ceiling against an unbounded hang, not what makes
	// this call safe to cancel. fundCfg comes from selfSettleConfig() above,
	// not rebuilt here.
	fctx, fcancel := context.WithTimeout(ctx, nodes.SelfSettleRetryBudget)
	txID, err := nodes.FundRunReserve(fctx, fundCfg, run.ID, creditReserve)
	fcancel()
	if err != nil {
		if errors.Is(err, nodes.ErrSettlementIndeterminate) {
			// The settle response was lost -- we don't know whether the
			// payment actually went through. Releasing the reservation
			// here could refund a user for money that already left Wallet
			// 1. Hold it, alert, and fail the run so an operator can
			// reconcile by hand, matching the same "money might have
			// already moved" caution the RecordRunFunding-failure branch
			// below already applies at the next step in this flow.
			criticalAlert(wf, run, "run pre-fund settle response lost, fate unknown, reservation held", err, "amount", creditReserve)
			return runFundResult{}, fmt.Errorf("x402 run funding: settlement indeterminate, failing rather than risking a refund for money already sent: %w", err)
		}
		bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ledgerCompensationTimeout)
		defer cancel()
		if relErr := r.store.ReleaseReservedCredits(bctx, wf.UserID, creditReserve); relErr != nil {
			criticalAlert(wf, run, "run pre-fund failed AND release failed (balance stranded)", relErr, "amount", creditReserve, "fundErr", err)
		}
		return runFundResult{}, fmt.Errorf("x402 run funding failed: %w", err)
	}

	// Detached, but for a different reason than settleRunTotal's own
	// RecordRunFunding call: this one never inherited the settle budget
	// (fctx above is cancelled immediately after FundRunReserve returns, and
	// this ran on the plain ctx), so it was never at risk of being starved
	// by a worst-case retry sequence. What it WAS exposed to is
	// cancellation: FundRunReserve has already moved real money on-chain by
	// this point, so a StopWorkflow landing here would fail this audit write
	// and send us into the branch below -- failing the run over a
	// bookkeeping gap for a payment that genuinely happened. WithoutCancel
	// makes the record survive that, matching every other compensating
	// write in this file.
	//
	// recCtx, not a second fctx: reusing that name here would silently
	// reassign the one declared above rather than introduce a new binding.
	recCtx, recCancel := context.WithTimeout(context.WithoutCancel(ctx), ledgerCompensationTimeout)
	defer recCancel()
	funding, err := r.store.RecordRunFunding(recCtx, run.ID, txID, creditReserve)
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

	// Two separate pools, not one sized creditReserve: vendorLedger is
	// bounded by estimate -- the exact amount executeTool402RunLevel's real
	// PayTargetFromWallet2 is allowed to pay OUT to vendors -- so a single
	// call can never drain more than this run's own real vendor-cost
	// budget, even though the on-chain settlement above moved creditReserve
	// (estimate + markupTotal) into Wallet 2 in one lump sum. markupLedger
	// is bounded by markupTotal, the flat-fee counterpart -- both are pure
	// credits-side bookkeeping pools (Reserve/Release never touch the DB
	// balance — see newRunLevelLedger); the single real DB decrement already
	// happened above via ReserveCredits(creditReserve), so splitting the
	// in-memory budget here doesn't double-reserve anything.
	vendorLedger, vendorRemaining := newRunLevelLedger(estimate, wf, run, r.store)
	markupLedger, markupRemaining := newRunLevelLedger(markupTotal, wf, run, r.store)
	cleanup := func(cctx context.Context) {
		unused := vendorRemaining() + markupRemaining()
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
		Ledger:          vendorLedger,
		MarkupLedger:    markupLedger,
		FundingID:       funding.ID,
		FundingTxID:     funding.InboundTxID,
		FundedUSDMicros: creditReserve,
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
//
// isFundingReceipt marks this row as bookkeeping, not a tool invocation: a
// single funding transaction can cover many real per-call receipts that
// follow, so a consumer counting "how many tools ran" must skip this row
// specifically rather than deduplicating by tx id (that would collapse the
// real, distinct calls that share its tx id down to one).
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
		"isFundingReceipt": true,
	}
	// []map[string]any is the concrete type Run()'s publish loop asserts on;
	// anything else there would silently drop every payment row.
	existing, _ := result["x402Payments"].([]map[string]any)
	result["x402Payments"] = append([]map[string]any{funded}, existing...)
}

// debitAgentFee charges an agent step. BYOK is free: the user is paying their
// own provider directly with their own key, so AgentMesh incurs no cost to
// pass on and takes no cut. Credits exist to cover what the platform actually
// spends on the user's behalf — platform-key LLM calls, and real x402
// settlements paid out of the platform wallets. Charging for BYOK billed
// users for compute they had already bought themselves. Logs on failure
// rather than failing the node, same rationale as debitOrLog: the call
// already happened, there's nothing left to roll back.
func (r *Runner) debitAgentFee(ctx context.Context, wf models.Workflow, run models.Run, nodeID string, amountUSDMicros int64, platformMode bool, model string, tokensIn, tokensOut int) {
	if !platformMode {
		return
	}
	if err := r.store.DebitCreditsForPlatformLLM(ctx, wf.UserID, amountUSDMicros, wf.ID, run.ID, nodeID, model, tokensIn, tokensOut); err != nil {
		log.Printf("platform-key debit failed: user=%s workflow=%s run=%s node=%s model=%s amount=%d: %v",
			wf.UserID, wf.ID, run.ID, nodeID, model, amountUSDMicros, err)
		return // the DB was never actually charged -- don't settle it on-chain either
	}
	r.addRunBilling(run.ID, amountUSDMicros)
}

// Start creates a cancellable context for the run, registers it, and launches
// Run in a goroutine. Replaces the previous pattern of calling Run directly.
//
// register() unconditionally cancels any run already registered for
// wf.ID -- correct here, since Start is only ever called for a user (or a
// webhook) deliberately triggering their own workflow, which should
// supersede whatever else was running. The scheduler must NOT get this
// behavior -- see StartIfNotRunning below.
func (r *Runner) Start(wf models.Workflow, run models.Run) {
	ctx, cancel := context.WithCancel(context.Background())
	gen := r.registry.register(wf.ID, cancel)
	go r.Run(ctx, wf, run, gen)
}

// StartIfNotRunning is Start's non-superseding counterpart, for the
// scheduler only. Its own overlap guard (Scheduler.tick's IsRunning +
// HasRunningRun checks) is check-then-act: read shared state, decide, then
// call this. That leaves a window between the read and this call where a
// manual trigger or resume for the same workflow could register in
// between -- if this used Start's register() (unconditional supersede),
// closing that window would mean silently cancelling a run the user just
// started, possibly mid-payment, which is exactly the class of bug
// StartResume's own claim-before-register ordering exists to prevent for
// resume. registerIfAbsent makes the FINAL registration step itself
// atomic and non-destructive: if anything is already registered for wf.ID
// by the time this runs -- including one that raced in during the
// scheduler's own check-then-act window -- this refuses (false) instead
// of cancelling it. Returns false without starting anything in that case;
// the caller (scheduler) must not have already committed anything
// unrecoverable (e.g. a run row) that this leaves orphaned.
//
// r.broker.Create(run.ID) happens here, only on the success path, for the
// same two reasons as StartResume's identical ordering: doing it in the
// caller before this call would leak a hub for a run that turns out to
// never execute (registerIfAbsent refused), and doing it in the caller
// AFTER this call would race the goroutine below actually publishing.
func (r *Runner) StartIfNotRunning(wf models.Workflow, run models.Run) bool {
	ctx, cancel := context.WithCancel(context.Background())
	gen, ok := r.registry.registerIfAbsent(wf.ID, cancel)
	if !ok {
		cancel()
		return false
	}
	r.broker.Create(run.ID)
	go r.Run(ctx, wf, run, gen)
	return true
}

// StartResume is Start's counterpart for continuing a run that already has
// some nodes logged as success (e.g. a dead-lettered run, retried by hand).
// force must be explicit to resume a run with a payment-risk dead-letter
// row -- see Resume's doc comment.
//
// The admission claim (store.MarkRunRunning) happens synchronously HERE,
// before registry.register() -- deliberately not inside Resume() itself.
// registry.register() unconditionally cancels whatever run is currently
// registered for this workflow ID; if the claim happened after it (as it
// used to), a losing concurrent StartResume call would already have
// cancelled a legitimately in-flight (possibly mid-payment) resume before
// ever finding out it lost the claim. Claiming first means a losing call
// returns false here and never touches the registry at all -- the winner's
// run is never cancelled out from under it. The claim itself is a plain
// conditional UPDATE, not registry state, so it's also correct across
// Railway's multiple replicas: two different processes calling this
// concurrently both hit the same row, and Postgres's row lock lets exactly
// one UPDATE succeed regardless of which process or in-process registry
// either is running under.
//
// Returns false (no error) if the run wasn't in a resumable state -- the
// caller must treat that as "already resumed elsewhere" or "already
// finished", not start executing anything.
//
// r.broker.Create(run.ID) happens HERE, synchronously, after the claim
// succeeds -- not in the handler before calling this. Broker.Create
// unconditionally overwrites any existing hub for run.ID with a fresh,
// empty one (no compare-and-swap); if the caller created it beforehand,
// two concurrent resume requests for the same run.ID would race that
// overwrite, and whichever hub a client's GET /stream subscribed to could
// end up orphaned -- silently never receiving events -- regardless of
// which request actually won the claim below. Creating it only after the
// claim succeeds means only the sole winner ever creates a hub for this
// resume attempt, and it does so before the goroutine below can publish
// anything into it.
func (r *Runner) StartResume(ctx context.Context, wf models.Workflow, run models.Run, force bool) (bool, error) {
	claimed, err := r.store.MarkRunRunning(ctx, run.ID)
	if err != nil || !claimed {
		return false, err
	}
	rctx, cancel := context.WithCancel(context.Background())
	// registerIfAbsent, NOT register: run.ID is an OLD failed/stopped run
	// being resumed, which says nothing about whether wf.ID has some OTHER
	// run currently in flight (a manual trigger, a schedule, or another
	// resume already executing). register() unconditionally cancels
	// whatever's registered for wf.ID -- that would mean resuming this old
	// run silently cancels a completely unrelated in-flight run for the
	// same workflow, possibly mid-payment, exactly the class of bug
	// registerIfAbsent/StartIfNotRunning already closed for the scheduler.
	// If something's already running for this workflow, this resume must
	// not claim it -- and having already flipped run.ID to 'running' via
	// MarkRunRunning above, that claim must be undone so the run row
	// doesn't get stuck reading "running" forever with nothing executing
	// it.
	gen, ok := r.registry.registerIfAbsent(wf.ID, cancel)
	if !ok {
		cancel()
		// Revert to run's OWN status as the caller passed it in -- NOT a
		// hardcoded RunStatusFailed. MarkRunRunning only ever claims a row
		// already sitting at 'failed' or 'stopped' (its own WHERE clause),
		// so run.Status here already carries whichever of those two it
		// actually was before this call flipped it to 'running'. Hardcoding
		// Failed would silently relabel a user-initiated Stop as a
		// Failed run just because an unrelated concurrent run raced in.
		revertStatus := run.Status
		if revertStatus != models.RunStatusFailed && revertStatus != models.RunStatusStopped {
			// Defensive: MarkRunRunning's own WHERE clause guarantees this
			// never happens (it only claims rows already 'failed' or
			// 'stopped'), but fail toward the safer, more visible label if
			// it somehow did rather than silently reverting to something
			// that isn't even a valid terminal status this run row started
			// from.
			revertStatus = models.RunStatusFailed
		}
		if revertErr := r.store.FinishRun(context.Background(), run.ID, revertStatus); revertErr != nil {
			log.Printf("resume: workflow %s already has a run in flight, AND reverting run %s's claim failed: %v", wf.ID, run.ID, revertErr)
		}
		return false, nil
	}
	r.broker.Create(run.ID)
	go r.Resume(rctx, wf, run, gen, force)
	return true, nil
}

// Stop cancels the active run for the given workflow ID. Returns false if no
// run was registered (i.e. the workflow is not currently running).
func (r *Runner) Stop(workflowID string) bool {
	return r.registry.cancel(workflowID)
}

// IsRunning reports whether workflowID currently has an in-flight run
// registered, without affecting it. Unlike Start -- which always supersedes
// (cancels) any previous run for the same workflow ID, since that's the
// right behavior for a user re-triggering their own workflow -- StartResume
// and StartIfNotRunning both refuse instead of superseding (see their own
// doc comments for why); the scheduler additionally uses this read-only
// check as a cheap first pass before StartIfNotRunning.
func (r *Runner) IsRunning(workflowID string) bool {
	return r.registry.isActive(workflowID)
}

// finishRun records the run's terminal status and fires a workflow-run audit-log
// notification. Centralized here so every terminal path (success, failed, stopped)
// reports to the same Discord channel with the same message shape.
func (r *Runner) finishRun(wf models.Workflow, run models.Run, status models.RunStatus) {
	r.store.FinishRun(context.Background(), run.ID, status)
	go alert.Notify(context.Background(), alert.ChannelWorkflows, fmt.Sprintf("workflow %q run %s finished: %s", wf.Name, run.ID, status))
}

// Run executes a workflow from scratch. Call via Start rather than directly.
func (r *Runner) Run(ctx context.Context, wf models.Workflow, run models.Run, gen uint64) {
	defer r.broker.Close(run.ID)
	defer r.registry.deregister(wf.ID, gen)

	// Tracks this run's non-tool402 billable total (see addRunBilling) so it
	// can be settled as one lump-sum x402 payment once the run is done.
	// Registered as a defer, not called at each of the explicit finishRun
	// return sites below, so it fires unconditionally on every exit path --
	// including the early TopologicalSort failure -- without those sites
	// needing to know about it. context.WithoutCancel + a bounded timeout
	// matches the same compensating-write convention used elsewhere in this
	// file (e.g. reserveAndFundRun's Cleanup), so the settlement still runs
	// even if Stop() already cancelled ctx. Uses nodes.SelfSettleRetryBudget,
	// not a locally-hardcoded timeout: settleRunTotal's underlying retry
	// loop (selfSettleWallet1ToWallet2) can make up to 3 real sign+verify+settle
	// attempts, same as FundRunReserve/SettlePlatformFee, and needs the same
	// ceiling those get -- a tighter one here would spuriously cut off a
	// later retry attempt under nothing worse than ordinary facilitator
	// latency.
	runTotal := new(int64)
	r.runBilling.Store(run.ID, runTotal)
	defer func() {
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), nodes.SelfSettleRetryBudget)
		defer cancel()
		r.settleRunTotal(sctx, wf, run, runTotal)
		r.runBilling.Delete(run.ID)
	}()

	go alert.Notify(context.Background(), alert.ChannelWorkflows, fmt.Sprintf("workflow %q run %s started", wf.Name, run.ID))

	r.execute(ctx, wf, run, nil)
}

// Resume continues run.ID from wherever it left off: nodes already logged as
// success in run_logs are skipped (their prior output is fed to downstream
// nodes unchanged) rather than re-executed, so nothing that already
// side-effected -- an x402 payment, a debited flat fee, a sent email -- runs
// twice. Call via StartResume rather than directly.
//
// Only a node's own prior success is checked here; a level upstream of the
// failed node that fully succeeded falls through immediately with nothing
// left to do, which is what makes "resume" cheaper than "restart" for
// workflows with real side-effecting steps earlier in the graph.
//
// force must be true to resume a run that has any payment-risk dead-letter
// row (see DeadLetterRun.PaymentRisk) -- otherwise this refuses outright.
// Retrying one of those nodes would re-run its LLM turn and re-debit its
// flat fee on top of the one already charged for the failed attempt, so the
// default has to be "don't", with an operator's explicit force required to
// accept that known double-charge risk.
func (r *Runner) Resume(ctx context.Context, wf models.Workflow, run models.Run, gen uint64, force bool) {
	defer r.broker.Close(run.ID)
	defer r.registry.deregister(wf.ID, gen)

	if !force {
		deadLetters, err := r.store.GetDeadLetterRuns(ctx, run.ID)
		if err != nil {
			log.Printf("resume: loading dead letters failed, run=%s: %v", run.ID, err)
			r.finishRun(wf, run, models.RunStatusFailed)
			return
		}
		for _, dl := range deadLetters {
			if dl.PaymentRisk {
				criticalAlert(wf, run, "resume refused: payment-risk dead-letter row present, force required", nil, "nodeId", dl.NodeID)
				r.finishRun(wf, run, models.RunStatusFailed)
				return
			}
		}
	}

	// Same run-total billing registration Run() does (see its own doc
	// comment) -- a resumed run can still execute new billable non-tool402
	// work (whatever wasn't already logged success), and without this,
	// addRunBilling's Load(run.ID) would miss and silently drop it: the
	// user's credit balance still gets debited correctly per node, but the
	// matching on-chain settlement would just never happen.
	runTotal := new(int64)
	r.runBilling.Store(run.ID, runTotal)
	defer func() {
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), nodes.SelfSettleRetryBudget)
		defer cancel()
		r.settleRunTotal(sctx, wf, run, runTotal)
		r.runBilling.Delete(run.ID)
	}()

	states, err := r.store.GetLatestNodeStates(ctx, run.ID)
	if err != nil {
		log.Printf("resume: loading prior node state failed, run=%s: %v", run.ID, err)
		r.finishRun(wf, run, models.RunStatusFailed)
		return
	}

	if !force {
		// A node whose LATEST logged state is still "running" means a
		// prior attempt started executing it and the process crashed (or
		// was killed) before ever writing a terminal status -- neither
		// "success" (skip, safe) nor "failed" (re-execute, already the
		// accepted risk for a plain failure). Whether that node's real
		// side effect (a payment, an email, a webhook post) actually fired
		// before the crash is genuinely unknown -- the exact same
		// "fate unknown" ambiguity ErrSettlementIndeterminate exists to
		// name elsewhere in this file, and the same fail-closed answer
		// applies: refuse rather than guess. Execution below treats
		// anything not LogStatusSuccess as not-yet-done and re-executes
		// it, so left unguarded this would silently re-fire that same
		// side effect a second time. force=true is the operator
		// explicitly accepting that risk, same as the payment-risk gate
		// above.
		for nodeID, l := range states {
			if l.Status == models.LogStatusRunning {
				criticalAlert(wf, run, "resume refused: node stuck mid-execution from a prior crash, force required", nil, "nodeId", nodeID)
				r.finishRun(wf, run, models.RunStatusFailed)
				return
			}
		}
	}

	// The run row was already flipped to "running" by StartResume's
	// MarkRunRunning claim before this goroutine was ever spawned -- that
	// claim is Resume's actual admission gate (see StartResume's doc
	// comment for why it has to happen there and not here).

	go alert.Notify(context.Background(), alert.ChannelWorkflows, fmt.Sprintf("workflow %q run %s resumed", wf.Name, run.ID))

	r.execute(ctx, wf, run, states)
}

// execute is Run and Resume's shared body. seed is nil for a fresh run, or
// the prior run_logs state for a resumed one -- every node whose seed entry
// has LogStatusSuccess is skipped rather than re-executed.
func (r *Runner) execute(ctx context.Context, wf models.Workflow, run models.Run, seed map[string]models.RunLog) {
	attachMap := BuildAttachMap(wf.Nodes, wf.Edges)
	levels, err := TopologicalSort(wf.Nodes, wf.Edges)
	if err != nil {
		// Surfaced, not swallowed: before this, a workflow that suddenly
		// failed here (e.g. a real or implicit-edge cycle) showed the user
		// only "Failed" with zero indication why -- no log row, no SSE
		// event, nothing. There's no single node to blame it on, so this
		// logs a run-level entry the same shape a node failure would get.
		log.Printf("workflow %q run %s: topological sort failed: %v", wf.Name, run.ID, err)
		outJSON, _ := json.Marshal(err.Error())
		if entry, insErr := r.store.InsertRunLog(context.Background(), models.RunLog{
			RunID:    run.ID,
			NodeType: models.NodeTypeTrigger,
			Status:   models.LogStatusRunning,
		}); insErr == nil {
			r.store.UpdateRunLog(context.Background(), entry.ID, models.LogStatusFailed, outJSON, 0, "")
		}
		r.broker.Publish(run.ID, models.LogEvent{
			NodeType: models.NodeTypeTrigger,
			Status:   models.LogStatusFailed,
			Output:   err.Error(),
			Ts:       time.Now(),
		})
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
				// Resume path: this node already reached success on a prior
				// attempt (real payments/debits included) — reuse its logged
				// output rather than re-executing and risking a double
				// side-effect. seed is nil for a fresh Run.
				//
				// For a node type with a real side effect (nodeMayHaveRealSideEffect),
				// this is unconditional -- skip regardless of whether the
				// node's config has since changed, exactly matching this
				// codebase's existing fail-closed stance on anything
				// payment-adjacent: re-executing one of these under a
				// changed config could repeat a real payment, email, or
				// connector send, which is a worse outcome than reusing
				// stale (but already-paid-for) output. A user who wants a
				// payment-adjacent node's new config to actually take
				// effect needs a fresh Run, not a Resume.
				//
				// For every other node type (pure computation: Tool
				// "calc"/"datetime", Trigger, End), prior.ConfigHash must
				// also match the node's CURRENT config -- see
				// nodeConfigHash's own doc comment for exactly which
				// fields that covers and why. An empty prior.ConfigHash
				// (a success logged before this check existed) is treated
				// as a match, preserving the always-skip behavior every
				// run resumed before this shipped already relied on.
				if prior, ok := seed[n.ID]; ok && prior.Status == models.LogStatusSuccess {
					if nodeMayHaveRealSideEffect(n.Type, n.Template, n.Method) ||
						prior.ConfigHash == "" || prior.ConfigHash == nodeConfigHash(n) {
						rc.Set(n.ID, prior.Output)
						return
					}
				}

				start := time.Now()
				logEntry, _ := r.store.InsertRunLog(ctx, models.RunLog{
					RunID:     run.ID,
					StepIndex: idx,
					NodeID:    n.ID,
					NodeType:  n.Type,
					Status:    models.LogStatusRunning,
				})

				// Retry only on an error the node explicitly marked safe
				// (nodes.Retryable) — e.g. a transport failure on an
				// idempotent HTTP method. Anything unclassified defaults to
				// NOT retryable, the same fail-closed stance the payment
				// paths in this file already take, since a node this loop
				// doesn't understand may have already side-effected.
				var result any
				var execErr error
				attempts := 0
				for {
					attempts++
					result, execErr = r.executeNode(ctx, n, attachMap, walletByAgent, rc, run, wf)
					// A sibling in this same parallel level already failed the
					// run (atomic `failed` below) -- no point sleeping through
					// a backoff and making another live outbound call for a
					// run that's already doomed to fail.
					if execErr == nil || ctx.Err() != nil || attempts > n.MaxRetries || !nodes.IsRetryable(execErr) || atomic.LoadInt32(&failed) != 0 {
						break
					}
					if n.RetryBackoffMs > 0 {
						timer := time.NewTimer(time.Duration(n.RetryBackoffMs) * time.Millisecond)
						select {
						case <-timer.C:
						case <-ctx.Done():
							timer.Stop()
						}
					}
					// Rechecked immediately on waking, before looping back to
					// executeNode -- ctx can be cancelled (Stop) or `failed`
					// can flip (a sibling in this level failed) at any point
					// during the sleep above, and the loop's own condition at
					// the top only catches that on its NEXT iteration's
					// result, one live outbound call too late. Breaking here
					// instead means a doomed or cancelled run never makes
					// that extra call.
					if ctx.Err() != nil || atomic.LoadInt32(&failed) != 0 {
						break
					}
				}
				dur := int(time.Since(start).Milliseconds())

				if execErr != nil {
					atomic.StoreInt32(&failed, 1)
					outJSON, _ := json.Marshal(execErr.Error())
					r.store.UpdateRunLog(context.Background(), logEntry.ID, models.LogStatusFailed, outJSON, dur, "")
					// A run cancelled mid-node (Stop) surfaces as execErr too,
					// but that's not a permanent node failure worth
					// dead-lettering -- the finishRun path below already
					// reports it as "stopped", not "failed".
					if ctx.Err() == nil {
						// isPaymentRisk covers every shape of "real money may
						// already have moved for this attempt": an attached
						// call blocked by insufficient balance after the
						// agent's own LLM turn (and its fee) already
						// completed, an attached tool402 call that signed and
						// sent a real payment before a downstream failure, or
						// the run-level pre-fund settle response getting lost
						// before any node started. Flag it so Resume refuses
						// to blindly retry (and potentially re-pay) this node
						// without an explicit force.
						paymentRisk := isPaymentRisk(execErr)
						if dlErr := r.store.InsertDeadLetterRun(context.Background(), models.DeadLetterRun{
							RunID:        run.ID,
							NodeID:       n.ID,
							Error:        execErr.Error(),
							AttemptCount: attempts,
							PaymentRisk:  paymentRisk,
						}); dlErr != nil {
							// This row is the only record of paymentRisk --
							// Resume's force-required guard reads it back via
							// GetDeadLetterRuns, so a failed write here would
							// silently let a resume re-execute (and re-bill)
							// a node whose fee was already charged. Loud
							// enough to page an operator, not just a log line.
							criticalAlert(wf, run, "dead-letter insert failed, payment-risk flag may be lost", dlErr, "nodeId", n.ID, "paymentRisk", paymentRisk)
						}
					}
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
				r.store.UpdateRunLog(context.Background(), logEntry.ID, models.LogStatusSuccess, outJSON, dur, nodeConfigHash(n))
				// This node just succeeded -- any dead-letter row an EARLIER
				// attempt at it left behind (this run reached here via
				// Resume) no longer reflects this node's current state, and
				// must not keep permanently requiring force to resume this
				// run in the future. See DeleteDeadLettersForNode's doc
				// comment. A fresh (non-resumed) run has no such rows to
				// begin with, so this is a no-op there.
				if delErr := r.store.DeleteDeadLettersForNode(context.Background(), run.ID, n.ID); delErr != nil {
					log.Printf("resume: clearing dead-letter rows for node %s, run=%s failed: %v", n.ID, run.ID, delErr)
				}
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
								// "" for configHash: this synthetic row is keyed by
								// the ATTACHED tool402 node's ID, not the agent
								// node actually running as a topology step --
								// execute()'s own skip-on-prior-success check
								// never looks this row up (attached tools are
								// excluded from that loop entirely, see
								// agentToolIDs above), so no hash is meaningful
								// here; it's purely an audit record.
								r.store.UpdateRunLog(context.Background(), entry.ID, models.LogStatusSuccess, payJSON, 0, "")
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
	//
	// Always runs, even when this run has no state loaded (an empty/nil
	// map): a workflow's very first run is exactly that case, and an
	// unresolved {{state.x}} reference must expand to empty there too, not
	// leak the literal placeholder into a real request -- ExpandState's own
	// fast path (no "{{" in the string) already makes this a no-op for
	// every field on a workflow that doesn't reference state at all.
	state := rc.State()
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
			Facilitator:              r.x402.FacilitatorClient,
			PlatformWalletAddress:    r.x402.PlatformWalletAddress,
			RelayNetwork:             r.x402.RelayNetwork,
			RelayFeePayer:            r.x402.RelayFeePayer,
			FrontendURL:              r.x402.FrontendURL,
			Ledger:                   nodes.RunLedger(rf.Ledger),
			MarkupLedger:             nodes.RunLedger(rf.MarkupLedger),
			// PerCallLedger backs any v2 dispatch not covered by this run's
			// funding -- either the whole agent has no run-level pre-fund
			// (rf.Ledger degrades to r.newPaymentLedger itself in that case,
			// via reserveAndFundRun's noFund branch) or this specific tool's
			// probe failed during estimation and toolIsRunFunded is false
			// for it. Always DB-backed, never the in-memory pool -- see
			// X402RelayConfig.PerCallLedger.
			PerCallLedger: nodes.CallLedger(r.newPaymentLedger(wf, run)),
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
			// FlatFeeLedger reserves/commits/releases each attached billable
			// flat-fee call (http Tool or Action/connector node) atomically,
			// the moment it happens inside ExecuteAgent's tool-calling loop —
			// same DB-backed, per-call ledger as LegacyLedger above, reused
			// here since both need the identical atomic-decrement semantics.
			// NOT batched until the turn ends: see FlatFeeLedger's doc
			// comment for why that would let every loop iteration check the
			// same stale balance and collectively overspend.
			FlatFeeLedger: nodes.CallLedger(r.newPaymentLedger(wf, run)),
		}
		result, err := nodes.ExecuteAgent(ctx, node, attach, aw, r.walletSvc, rc, checkBalance, r.platformKeys, relayCfg)
		if err != nil {
			// isAgentFeeOwedDespiteFailure means the agent's own LLM turn
			// already completed -- either an attached call was blocked by
			// insufficient balance before it could run, or an attached
			// tool402 call signed and sent a real payment before a
			// downstream failure -- either way the agent's own flat fee is
			// still owed. Any other error (e.g. LLM connectivity failure)
			// means the agent turn itself never completed, so nothing is
			// billed, matching the pre-existing behavior for those failures.
			if isAgentFeeOwedDespiteFailure(err) {
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
		// Attached x402Payments entries (relay markup included) and attached
		// flat-fee tool/action calls are already reserved+committed from
		// inside ExecuteAgent's tool-calling loop, atomically per call, at
		// the moment each payment/call settled — not batched here. See
		// relayCfg.Ledger/MarkupLedger/FlatFeeLedger and their doc comments
		// for why batching until the whole agent turn completes would let
		// every loop iteration check the same stale balance and collectively
		// overspend past what the user can cover.
		if m, ok := result.(map[string]any); ok {
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
		if err := r.preflightCheck(ctx, wf, models.X402ProbeFloorUSDMicros); err != nil {
			return nil, err
		}
		// A standalone tool402 node is never run-funded (that only ever
		// applies to an agent's attached tools, RunFundingID stays "" here),
		// so toolIsRunFunded is always false and v2 dispatch always takes
		// the PerCallLedger path -- Ledger is never actually read for a
		// standalone node, but PerCallLedger and LegacyLedger are the same
		// DB-backed, per-call ledger, matching the identical
		// legacy-dialect/v2-dialect split ExecuteTool402V2 already makes.
		standaloneLedger := r.newPaymentLedger(wf, run)
		relayCfg := nodes.X402RelayConfig{
			USDCSigner:               usdcSigner,
			PlatformSpendEncMnemonic: r.platformSpendEncMnemonic,
			ExpectedAssetID:          r.x402.USDCAssetID,
			RelayBaseURL:             r.relayBaseURL,
			Facilitator:              r.x402.FacilitatorClient,
			PlatformWalletAddress:    r.x402.PlatformWalletAddress,
			RelayNetwork:             r.x402.RelayNetwork,
			RelayFeePayer:            r.x402.RelayFeePayer,
			FrontendURL:              r.x402.FrontendURL,
			PerCallLedger:            nodes.CallLedger(standaloneLedger),
			LegacyLedger:             nodes.CallLedger(standaloneLedger),
		}
		paymentResult, err := nodes.ExecuteTool402V2(ctx, node, rc, aw, r.walletSvc, relayCfg)
		if err != nil {
			return nil, err
		}
		// Already reserved+committed via ledger inside ExecuteTool402V2, at
		// the moment the payment settled — see newPaymentLedger.
		return paymentResult.Response, nil
	case models.NodeTypeTendril:
		if r.tendrilClient == nil || r.tendrilSession == nil {
			return nil, fmt.Errorf("tendril: TENDRIL_REGISTRY_URL is not configured on this server")
		}
		// Same conservative pre-flight as tool402: one cheap balance check
		// before any network call that could spend money.
		if err := r.preflightCheck(ctx, wf, models.X402PlatformFeeUSDMicros); err != nil {
			return nil, err
		}
		usdcSigner, _ := r.walletSvc.(nodes.USDCGroupSigner)
		ledger := r.newPaymentLedger(wf, run)
		return nodes.ExecuteTendril(ctx, node, rc, nodes.TendrilConfig{
			Client:     r.tendrilClient,
			Session:    r.tendrilSession,
			Store:      r.store,
			EncryptKey: r.encryptionKey,
			UserID:     wf.UserID,
			WorkflowID: wf.ID,
			RunID:      run.ID,
			Relay: nodes.X402RelayConfig{
				USDCSigner:               usdcSigner,
				PlatformSpendEncMnemonic: r.platformSpendEncMnemonic,
				ExpectedAssetID:          r.x402.USDCAssetID,
				RelayBaseURL:             r.relayBaseURL,
				Facilitator:              r.x402.FacilitatorClient,
				PlatformWalletAddress:    r.x402.PlatformWalletAddress,
				RelayNetwork:             r.x402.RelayNetwork,
				RelayFeePayer:            r.x402.RelayFeePayer,
				FrontendURL:              r.x402.FrontendURL,
				Ledger:                   nodes.RunLedger(ledger),
				LegacyLedger:             nodes.CallLedger(ledger),
				PerCallLedger:            nodes.CallLedger(ledger),
			},
		})
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
	case models.NodeTypeGoogle:
		if r.googleClientID == "" || r.googleClientSecret == "" {
			return nil, fmt.Errorf("google: GOOGLE_CLIENT_ID/GOOGLE_CLIENT_SECRET are not configured on this server")
		}
		// Routed through BillableFlatFee, same as NodeTypeAction above,
		// instead of hardcoding "always billable" here -- BillableFlatFee is
		// the single source of truth for which template/nodeType pairs are
		// billable (NodeTypeTool already varies by template: template ==
		// "http"), so calling it here means a future template-conditional
		// change there (e.g. a free Google read-only op) takes effect
		// automatically instead of silently being ignored by this branch.
		billable := nodes.BillableFlatFee(node.Type, node.Template)
		if billable {
			if err := r.preflightCheck(ctx, wf, models.ByokFlatFeeUSDMicros); err != nil {
				return nil, err
			}
		}
		result, err := nodes.ExecuteGoogle(ctx, node, rc, nodes.GoogleConfig{
			Store:        r.store,
			EncryptKey:   r.encryptionKey,
			ClientID:     r.googleClientID,
			ClientSecret: r.googleClientSecret,
			UserID:       wf.UserID,
		})
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
