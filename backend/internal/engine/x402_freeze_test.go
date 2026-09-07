package engine_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
)

// frozenX402Files pins the byte content of the x402 / Tendril payment path.
//
// The wallet topology these files implement is:
//
//	Wallet 1 (PLATFORM_SPEND_WALLET) --USDC--> Wallet 2 (PLATFORM_WALLET)
//	    settled via the GoPlausible facilitator   [inbound leg]
//	Wallet 2 --USDC--> the target endpoint's payTo [outbound leg]
//	Wallet 1 --USDC--> Wallet 2, flat platform markup [markup leg]
//
// Real mainnet USDC moves through this. A change here is never incidental.
//
// If this test fails and you DID intend the change: re-run the hash command in
// the failure message, paste the new digest below, and get explicit sign-off in
// the PR description explaining why the payment path moved. If you did NOT
// intend it, revert the file.
// Updated 2026-08-19 reconciling PR #65 ("tiered node expansion") against
// master: billing.go/runfund.go/tendril.go/x402relay.go moved on master
// while this branch was open, for reasons unrelated to anything in the PR
// itself --
//   - billing.go: BillableFlatFee gained models.NodeTypeGoogle as a billable
//     node type, for master's new Google connector nodes.
//   - runfund.go / x402relay.go: both now build their Bazaar discovery
//     extension through the shared nodes.BazaarDiscoveryExtension instead of
//     two hand-maintained, independently-drifting copies (the drift itself
//     was a real prior cataloging bug on master, already fixed there).
//   - tendril.go: emptyRunContext gained LastOutput() any, for the
//     RunContexter interface's own determinism fix (see engine/context.go).
//
// Every one of these is a clean merge from master, not a local edit; the
// payment amounts/addresses/signing logic (the thing this test actually
// guards) is unchanged. Digests below reflect the merged state.
//
// Updated again 2026-08-26, rebasing PR #65 onto current master, which moved
// further while this PR was still open --
//   - billing.go: BillableFlatFee gained "websearch" alongside "http" as a
//     billable Tool template, for master's new Gemini-grounded web search
//     tool (a real paid call on the platform's own key).
//   - runfund.go / x402relay.go: master added SettleRunTotal (mirrors the
//     existing SettlePlatformFee/FundRunReserve self-settle pattern for a
//     new "whole run's non-tool402 billable total" lump-sum settlement) and
//     its accompanying X402RunTotalInfo resource-info handler.
//
// Both are additive: new billable-template case, new settlement function,
// new info handler. No existing amount/address/signing logic changed.
// Digests below reflect the merged state.
//
// Updated 2026-08-30, rebasing PR #99 (run resume/retry/dead-letter/cron)
// onto current master, which moved further while this PR was open --
// master's PR #59 ("prevent bot-driven duplicate settlements + workflow run
// cooldown") reworked runfund.go/tool402.go's settlement-idempotency guards.
// Confirmed via `git log 7059903..feat/run-reliability -- nodes/runfund.go
// nodes/tool402.go`: PR #59's commit is the ONLY history touching either
// file on this branch -- PR #99's own commits never edit them. Clean merge
// from master, not a local edit. Digests below reflect the merged state.
//
// Updated again 2026-08-30, same PR: a code review pass found the
// dead-letter PaymentRisk classification (runner.go's isPaymentRisk) missed
// two failure shapes that also mean real money already moved -- a tool402
// call that signed and sent a real outbound payment before the target
// rejected it or the request itself failed at the transport level.
//
//   - billing.go: added ErrPaymentAlreadyCommitted, a new sentinel error
//     type (same shape as the existing ErrBalanceBlocked) wrapping a
//     failure that happens strictly AFTER the ledger Commit call for a
//     signed payment. No amount/address/signing logic touched.
//   - tool402.go: the two return sites in executeTool402RunLevel and the
//     one in executeTool402V2Relay that already returned a plain error
//     AFTER their preceding Commit call now wrap that same error in
//     ErrPaymentAlreadyCommitted before returning it, so a caller (the
//     dead-letter classification) can tell it apart from a failure that
//     happened before any payment was signed. Every Commit/Reserve/Release
//     call, its ordering, and every amount/address/signing line are
//     unchanged -- only the returned error's TYPE changed at these three
//     already-post-Commit return statements.
//
// Updated again 2026-08-30, same PR: another review pass found
// isAgentFeeOwedDespiteFailure/isPaymentRisk (runner.go) dispatched on a
// hand-maintained list of concrete error types with nothing enforcing a
// future payment-adjacent type gets added to it -- exactly the class of
// gap that let ErrPaymentAlreadyCommitted (added above) need a separate
// follow-up pass to wire in at all.
//
//   - billing.go: added AgentFeeOwedError, a marker interface (Error() +
//     an AgentFeeOwed() no-op method), and implemented it on both
//     *ErrBalanceBlocked and *ErrPaymentAlreadyCommitted. Purely additive:
//     no existing field, amount, address, or signing line touched, and
//     both types already existed with identical behavior -- this only
//     adds a way to detect them generically via errors.As against the
//     interface instead of one errors.As per concrete type.
//
// Updated again 2026-08-31, merging master's 4304044 ("update x402
// freeze-test digests for the run-cooldown merge") into this branch --
// master re-hashed tool402.go/runfund.go for 5c92070's retry/backoff
// rework (selfSettleWallet1ToWallet2's full-jitter backoff loop,
// SettlePlatformFee's timeout switching to SelfSettleRetryBudget), which
// this branch had already merged earlier (see the 2026-08-30 entry above)
// with its own correct digests. runfund.go's digest was already identical
// on both sides -- no change there. tool402.go's digest conflicted because
// this branch's tool402.go carries BOTH master's retry rework AND this
// PR's ErrPaymentAlreadyCommitted wrapping on top of it; the digest below
// is freshly computed from that merged file, not copied from either side.
// No amount/address/signing logic touched by the merge itself.
var frozenX402Files = map[string]string{
	"nodes/tool402.go":             "af54224f3e2afd23ce5fb1f434bc1ff912b12af47f21e6f941291ae136e90860",
	"nodes/runfund.go":             "792e2a3c96465545119cebfcb744d487b79b27e5df7b9842ec643a98dce7b782",
	"nodes/walletpay.go":           "98bb3f7d0cb167f8a50d050e04720738c63c68b9fd570758fa5b9604338a4e37",
	"nodes/tendril.go":             "b787a18f17bc80f593159e46a0c7fd7e543a9db44f55a451ed8f47102fb9132a",
	"nodes/billing.go":             "d6bc9e5931816840d99678f9015f7b186ae3069d54e28605aa618c367bf5beb9",
	"nodes/tier.go":                "5718a3538e042c9d7f90b37f38b47d893644d6093f560d103ea9036c90ddc90b",
	"../api/handlers/x402relay.go": "eacd56896816a213dd5658aa536c704db22362a5d787113cbf269d7fe7c1d858",
	"../x402/facilitator.go":       "976d118ae200994728f96733dceca79bc90fcc2cc99e859c47d710477f9480ca",
}

func TestX402PaymentPathIsFrozen(t *testing.T) {
	for rel, want := range frozenX402Files {
		b, err := os.ReadFile(rel)
		if err != nil {
			t.Fatalf("frozen file %s is unreadable — was it moved or deleted? %v", rel, err)
		}
		sum := sha256.Sum256(b)
		got := hex.EncodeToString(sum[:])
		if want == "" {
			t.Fatalf("no baseline digest recorded for %s.\n"+
				"Record it with:\n  shasum -a 256 backend/internal/engine/%s\n"+
				"then paste it into frozenX402Files.", rel, rel)
		}
		if got != want {
			t.Errorf("FROZEN FILE CHANGED: %s\n  want %s\n  got  %s\n\n"+
				"This file implements the Wallet 1 -> Wallet 2 -> provider payment path.\n"+
				"If this change is intentional, update the digest AND justify it in the PR.\n"+
				"If not, revert it.", rel, want, got)
		}
	}
}
