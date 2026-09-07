package nodes

import (
	"context"
	"errors"

	"github.com/agentmesh/backend/internal/models"
)

// BillableFlatFee reports whether executing a node performs a real
// off-platform action that should be charged the flat BYOK convenience
// fee. Every Action node (email + all connectors) is billable — in
// practice every Action node has a real, recognized template, since the
// UI only ever creates nodes from its connector list; the "logged"
// fallback in ExecuteAction's switch is a defensive no-op that doesn't
// occur in real workflows, so it isn't special-cased here. Agent nodes
// are always billable — every agent call is BYOK today (issue #25 will
// add a platform-key toggle, which is where this stops being true
// unconditionally). Tool nodes are billable only for the "http" template
// — "calc" and "datetime" are pure local computation, no different from
// Trigger/End, and stay free. Google nodes (Gmail/Sheets/Calendar/Drive)
// are billable the same as any other connector — the API call itself is
// free (the user's own Google quota, not a paid endpoint), but running it
// is still real off-platform work through AgentMesh's infra, same as
// every webhook-based connector already charges for. Tool402 is metered
// separately — relay-path payments charge the real settled amount, legacy
// direct-pay charges a flat fee, both gated on whether a payment actually
// happened at runtime, not on static config — so it always returns false
// here. "websearch" is billable alongside "http": it's a real call to
// Gemini's (paid) search-grounding API on the platform's own key, not local
// computation like "calc"/"datetime".
func BillableFlatFee(nodeType models.NodeType, template string) bool {
	switch nodeType {
	case models.NodeTypeAgent, models.NodeTypeAction, models.NodeTypeGoogle:
		return true
	case models.NodeTypeTool:
		return template == "http" || template == "websearch"
	default:
		return false
	}
}

// BalanceChecker lets the nodes package ask the caller whether a billable
// attached tool/x402 call can proceed, without giving nodes direct DB
// access. Returns a non-nil error (matching preflightCheck's error text
// convention) when the balance is insufficient.
type BalanceChecker func(ctx context.Context, amountUSDMicros int64) error

// PaymentLedger bundles the reserve/commit/release protocol a real on-chain
// x402 payment (either dialect) uses to close the gap between "balance
// checked" and "balance charged": Reserve atomically decrements the balance
// before the payment is attempted (so concurrent or sequential calls within
// one node execution can't all pass a check against the same stale
// balance); Commit writes the permanent debit_ledger audit row once the
// payment is confirmed settled; Release credits back a reservation that
// never became a real charge. A nil field is treated as a no-op — Reserve
// nil means unconditionally allowed, matching the pre-existing nil-checker
// convention elsewhere in this package. Production callers (runner.go)
// always supply all three; nil fields are for tests that don't exercise a
// billable tool402 payment at all.
type PaymentLedger struct {
	Reserve func(ctx context.Context, amountUSDMicros int64) error
	Commit  func(ctx context.Context, nodeID string, amountUSDMicros int64, kind string)
	Release func(ctx context.Context, amountUSDMicros int64)
}

// RunLedger and CallLedger both wrap PaymentLedger with the same method
// set, but are distinct Go types so X402RelayConfig.Ledger (run-level, in-
// memory pool) and .LegacyLedger (per-call, DB-backed) can never be
// accidentally read in place of each other -- a future edit that mixes them
// up now fails to compile instead of silently misbilling (see
// X402RelayConfig's field comments for why the distinction matters).
type RunLedger PaymentLedger
type CallLedger PaymentLedger

// ErrActionSkipped is returned by Action node implementations (email + all
// connectors) when required credentials/config are missing, so the node
// short-circuits before making any real network call. runner.go's
// NodeTypeAction case treats this as a successful no-op: the descriptive
// skip message is still returned to the workflow as the node's result, but
// the flat BYOK fee is not charged, since no billable work happened.
var ErrActionSkipped = errors.New("action skipped: missing required configuration")

// ErrSettlementIndeterminate wraps a Facilitator.Settle call whose response
// never arrived (network timeout, connection reset, or any other transport-
// level failure) -- unlike a definitively-decoded SettleResult{Success:
// false}, this means we genuinely don't know whether the facilitator
// broadcast and confirmed the payment before the response was lost.
// Callers must not release a reservation on this error the way they would
// for a real, received rejection -- the money may have already moved.
var ErrSettlementIndeterminate = errors.New("x402: facilitator settle response lost, payment fate unknown")

// AgentFeeOwedError is implemented by any error meaning the agent's own
// LLM turn already completed -- so its flat fee is still owed -- before
// something it then did failed with real money potentially already moved.
// engine.isAgentFeeOwedDespiteFailure/isPaymentRisk dispatch on this
// interface via errors.As rather than a hand-maintained list of concrete
// types, specifically so a FUTURE payment-adjacent error occurring mid-
// agent-turn only needs to implement this one method to be picked up
// correctly -- without it, a new type would silently classify as "no
// payment risk" until someone remembered to add a matching branch by hand
// (exactly what happened here: ErrPaymentAlreadyCommitted was added after
// ErrBalanceBlocked, in a separate pass, and had to be found and added to
// the dispatch list manually).
type AgentFeeOwedError interface {
	error
	AgentFeeOwed()
}

// ErrBalanceBlocked wraps a BalanceChecker failure so the agent loop can
// hard-stop instead of feeding the failure back to the LLM as a retryable
// tool-level error (which would just spin the loop until
// maxToolIterations, contradicting the "blocks before it runs, no soft
// overage" contract). Callers use errors.As to distinguish this from other
// ExecuteAgent failures (e.g. LLM connectivity errors): a
// *ErrBalanceBlocked failure means the agent's own LLM turn already ran
// (so its flat fee is still owed) and only the subsequent attached call
// was blocked; any other error means the agent turn itself never
// completed, so nothing should be billed.
type ErrBalanceBlocked struct {
	Err error
}

func (e *ErrBalanceBlocked) Error() string { return e.Err.Error() }
func (e *ErrBalanceBlocked) Unwrap() error { return e.Err }
func (e *ErrBalanceBlocked) AgentFeeOwed() {}

// ErrPaymentAlreadyCommitted wraps a tool402 failure that happens AFTER a
// real outbound x402 payment already signed, submitted, and got committed
// to the ledger (see PayTargetFromWallet2/Wallet2PayResult.Signed and the
// relay path's identical shape) -- the target then rejected the paid
// request, or the target request itself failed at the transport level.
// Either way the money already left Wallet 2; nothing here is safe to
// silently retry from scratch, since a retry would pay again for the same
// call rather than just recovering the one part that actually failed.
// Callers (the dead-letter PaymentRisk classification in runner.go) use
// errors.As to distinguish this from a tool402 failure that happened
// before any payment was signed, which is safe to retry freely.
type ErrPaymentAlreadyCommitted struct {
	Err error
}

func (e *ErrPaymentAlreadyCommitted) Error() string { return e.Err.Error() }
func (e *ErrPaymentAlreadyCommitted) Unwrap() error { return e.Err }
func (e *ErrPaymentAlreadyCommitted) AgentFeeOwed() {}
