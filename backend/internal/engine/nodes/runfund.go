package nodes

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"time"

	"github.com/agentmesh/backend/internal/x402"
)

// runFundingPublicPath is this route as reached through our own branded
// domain's /api proxy (frontend/next.config.ts rewrites /api/:path* to the
// backend). Used for both the declared resource URL and the bazaar
// routeTemplate, which must agree -- see x402relay.go's relayPublicPath.
const runFundingPublicPath = "/api/x402/relay/run-funding"

// platformFeePublicPath is SettlePlatformFee's resource identity, same
// rationale as runFundingPublicPath -- a real, non-root, genuinely
// 402-answering path under our own branded origin, distinct from
// runFundingPublicPath so the two settlement kinds attribute separately in
// any per-resource reporting even though both share one payTo.
const platformFeePublicPath = "/api/x402/relay/platform-fee"

// runTotalPublicPath is SettleRunTotal's resource identity, same rationale
// as runFundingPublicPath/platformFeePublicPath -- a real, non-root,
// genuinely 402-answering path under our own branded origin, distinct from
// the other two so this settlement kind attributes separately.
const runTotalPublicPath = "/api/x402/relay/run-total"

// RunPreFundConfig carries what's needed to settle a single lump-sum inbound
// x402 payment (Wallet 1 -> Wallet 2) before an agent node's tool-calling
// loop starts. Distinct from Wallet2PayConfig (Task 3), which drives the
// per-call OUTBOUND leg — this only ever settles the INBOUND leg, once per
// run.
type RunPreFundConfig struct {
	USDCSigner               USDCGroupSigner
	PlatformSpendEncMnemonic string
	Facilitator              *x402.FacilitatorClient
	PlatformWalletAddress    string
	RelayNetwork             string
	RelayFeePayer            string
	ExpectedAssetID          uint64
	// FrontendURL is our own branded origin (FRONTEND_URL), combined below
	// with the /api/... proxy path that frontend/next.config.ts already
	// rewrites to this backend. Same identity x402relay.go declares -- see
	// its relayPublicPath doc comment for why the origin has to be this one
	// and the path has to be a real, non-root, genuinely-402-answering one.
	FrontendURL string
}

// FundRunReserve settles one real GoPlausible payment for amountUSDMicros
// from the platform's Wallet 1 spend wallet into Wallet 2
// (cfg.PlatformWalletAddress) — same payTo as every per-call relay
// settlement, so leaderboard attribution (keyed on payTo, not resource) is
// unaffected. Resource points at the real run-funding route as reached
// through our own branded domain's /api proxy, rather than an opaque
// identifier string or a bare backend host.
// amountUSDMicros <= 0 is a no-op (an agent with no attached tool402 nodes,
// or all-legacy-dialect ones, needs no pre-fund at all).
func FundRunReserve(ctx context.Context, cfg RunPreFundConfig, runID string, amountUSDMicros int64) (string, error) {
	return selfSettleWallet1ToWallet2(ctx, cfg, runFundingPublicPath,
		"AgentMesh workflow run funding — pre-settled pool for this run's downstream x402 tool calls",
		"run pre-fund", amountUSDMicros)
}

// SettlePlatformFee settles the platform's own flat per-call markup
// (models.X402PlatformFeeUSDMicros) as one more real, direct Wallet 1 ->
// Wallet 2 payment, same mechanism as FundRunReserve but for the fee alone
// rather than a whole run's estimated vendor spend.
//
// Exists so the fee actually lands in cfg.PlatformWalletAddress on-chain
// instead of being a pure internal credit-ledger bookkeeping entry with no
// backing asset movement -- before this, the per-call relay path's
// executeTool402V2Relay committed the fee against the caller's AgentMesh
// credit balance and stopped there: real USDC only ever moved for the
// vendor's own real ask (see that function's `total := amount + fee` split,
// where only `amount` is ever signed/settled). Called right alongside that
// vendor-cost settlement, not baked into it, because the vendor leg's price
// is set by the vendor's own external 402 challenge -- inflating it by our
// own markup would overpay whatever the vendor actually quoted and break
// protocol compatibility for any other caller of our public relay endpoint
// (x402relay.go's X402Relay is unauthenticated and bazaar-cataloged, used by
// more than just this platform's own engine).
func SettlePlatformFee(ctx context.Context, cfg RunPreFundConfig, amountUSDMicros int64) (string, error) {
	return selfSettleWallet1ToWallet2(ctx, cfg, platformFeePublicPath,
		"AgentMesh platform fee — flat per-call markup, settled from the platform spend wallet to the platform revenue wallet",
		"platform fee settle", amountUSDMicros)
}

// SettleRunTotal settles the whole run's non-tool402 billable total (agent
// platform-key LLM fees, action/google/http BYOK flat fees -- everything
// that otherwise only ever moves money inside the internal credit ledger)
// as one more real, direct Wallet 1 -> Wallet 2 payment, same mechanism as
// FundRunReserve/SettlePlatformFee. Tendril lease/rent cost is NOT included
// here -- it's charged against a wholly separate Tendril-credit pool
// (Store.ChargeTendrilCredit), never the AgentMesh credit ledger this
// settlement mirrors; only Tendril's own small gate fee is, and that
// already flows through the tool402 relay path (excluded here the same way
// as any other real tool402 spend). Called once
// per run, after all node-level billing has already been committed to the
// DB ledger -- this is an additive on-chain receipt for that already-final
// total, not a second charge: the user's credit balance was already
// decremented by the normal per-node debit calls. Real tool402 spend is
// excluded (it already gets its own real settlement via FundRunReserve /
// the per-call relay path) to avoid double-settling the same money on-chain.
func SettleRunTotal(ctx context.Context, cfg RunPreFundConfig, amountUSDMicros int64) (string, error) {
	return selfSettleWallet1ToWallet2(ctx, cfg, runTotalPublicPath,
		"AgentMesh workflow run total — lump-sum settlement of this run's platform-billed work",
		"run total settle", amountUSDMicros)
}

// selfSettleWallet1ToWallet2 signs, verifies, and settles one real GoPlausible
// payment for amountUSDMicros from the platform's own Wallet 1 spend wallet
// into Wallet 2 (cfg.PlatformWalletAddress) -- no external target involved,
// the platform is both payer and resource server. publicPath/description
// give the settlement its own distinct, genuinely-402-answering resource
// identity (see runFundingPublicPath's doc comment for why that has to be a
// real path under our own branded origin, not an opaque identifier).
// amountUSDMicros <= 0 is a no-op.
//
// Retries up to selfSettleMaxAttempts times on any failure EXCEPT
// ErrSettlementIndeterminate: a signing error, a verify rejection, or a
// definitive (received) settle failure all mean nothing was broadcast or
// confirmed, so a retry -- with a fresh SignUSDCPaymentGroup call, and
// therefore a fresh uniqueNote nonce and SuggestedParams -- is safe and
// cannot double-pay. An indeterminate settle response (the request may
// have already been broadcast and confirmed, we just never heard back)
// stops retrying immediately instead, exactly as before this change --
// resubmitting there risks paying twice. This is what actually closes the
// gap: before wallet/algorand.go's uniqueNote fix, a same-round retry of
// the flat-amount platform fee would have produced the exact same
// collision it was retrying to escape; now every attempt is guaranteed
// distinct regardless of amount or timing.
//
// ctx.Err() is checked before every attempt, including the first: without
// this, a caller whose context is already canceled/expired when this is
// entered would still burn up to selfSettleMaxAttempts real sign+verify
// round trips before giving up. Because only the Settle sub-call itself is
// shielded from cancellation (see attemptSelfSettle's doc comment), ctx
// stays the caller's real, cancelable context throughout -- a StopWorkflow
// firing between attempts, or during signing/Verify of the current one,
// is honored promptly; only the narrow window where a Settle call is
// actually in flight (Wallet 1 -> Wallet 2, possibly already broadcast)
// is protected, matching this file's existing "money in motion must not
// be interrupted" rule everywhere else.
//
// All non-indeterminate attempt errors are accumulated (errors.Join), not
// just the last one: an intermittent facilitator 5xx on attempt 1
// followed by a persistent signing misconfiguration on attempt 2 used to
// collapse into one final message with the first failure's cause
// discarded -- exactly the signal needed to tell "transient, would
// probably succeed on a true retry later" apart from "persistent,
// retrying again won't help" when diagnosing a real elevated
// settle-failure rate like the one this PR was written to fix.
func selfSettleWallet1ToWallet2(ctx context.Context, cfg RunPreFundConfig, publicPath, description, errPrefix string, amountUSDMicros int64) (string, error) {
	if amountUSDMicros <= 0 {
		return "", nil
	}

	var errs []error
	for attempt := 1; attempt <= selfSettleMaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			errs = append(errs, fmt.Errorf("attempt %d: context done: %w", attempt, err))
			break
		}
		if attempt > 1 {
			if err := sleepWithBackoff(ctx, attempt); err != nil {
				errs = append(errs, fmt.Errorf("attempt %d: context done during backoff: %w", attempt, err))
				break
			}
		}
		txID, err := attemptSelfSettle(ctx, cfg, publicPath, description, errPrefix, amountUSDMicros)
		if err == nil {
			return txID, nil
		}
		if errors.Is(err, ErrSettlementIndeterminate) {
			return "", err
		}
		errs = append(errs, fmt.Errorf("attempt %d: %w", attempt, err))
	}
	return "", fmt.Errorf("%s: all attempts failed: %w", errPrefix, errors.Join(errs...))
}

// selfSettleRetryBackoffBase/Max bound the delay selfSettleWallet1ToWallet2
// waits before each retry (not before the first attempt). Full-jitter
// exponential backoff: during a real facilitator outage -- the scenario
// this whole retry mechanism is meant to survive -- every in-flight run
// across the platform hits the same failure at roughly the same time, so
// retrying instantly with no delay would multiply the load on an already
// struggling facilitator by selfSettleMaxAttempts instead of easing off it.
const (
	selfSettleRetryBackoffBase = 500 * time.Millisecond
	selfSettleRetryBackoffMax  = 5 * time.Second
)

// backoffCapForAttempt is the full-jitter ceiling for the gap before
// attempt N (N>1): exponential from selfSettleRetryBackoffBase, clamped to
// selfSettleRetryBackoffMax. The delay actually slept is a random value
// strictly below this, so summing it across attempts gives a true worst
// case (see worstCaseBackoffTotal).
//
// Split out of sleepWithBackoff so the retry budget below, and the tests
// asserting on real elapsed time, derive from this one schedule rather
// than each re-deriving (or hand-mirroring) it and drifting apart.
func backoffCapForAttempt(attempt int) time.Duration {
	c := selfSettleRetryBackoffBase * time.Duration(uint64(1)<<uint(attempt-2))
	if c > selfSettleRetryBackoffMax || c <= 0 {
		c = selfSettleRetryBackoffMax
	}
	return c
}

// worstCaseBackoffTotal is the most time a full retry sequence can spend
// sleeping between attempts -- the sum of every gap's cap.
//
// Deliberately the real per-attempt schedule rather than
// (selfSettleMaxAttempts-1) * selfSettleRetryBackoffMax: at
// selfSettleMaxAttempts=3 the two gaps cap at 500ms and 1s and never reach
// the 5s max at all, so the flat-max version over-provisioned the budget
// below by ~8.5s of time no run can actually spend.
func worstCaseBackoffTotal() time.Duration {
	var total time.Duration
	for attempt := 2; attempt <= selfSettleMaxAttempts; attempt++ {
		total += backoffCapForAttempt(attempt)
	}
	return total
}

// sleepWithBackoff waits before retry attempt N (N>1), honoring ctx
// cancellation -- this gap is one of the safe-to-interrupt windows
// selfSettleWallet1ToWallet2's doc comment describes, so a StopWorkflow
// landing here returns promptly instead of waiting out the full delay.
func sleepWithBackoff(ctx context.Context, attempt int) error {
	delay := time.Duration(rand.Int63n(int64(backoffCapForAttempt(attempt))))
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// selfSettleMaxAttempts bounds selfSettleWallet1ToWallet2's retry loop. Not
// unbounded: a real, persistent misconfiguration (bad mnemonic, algod down,
// facilitator down) should fail loudly and quickly, not loop for minutes.
const selfSettleMaxAttempts = 3

// defaultSettleCallBudget/defaultVerifyCallBudget/defaultSignCallBudget are
// the real per-call budgets -- named consts so SetSelfSettleCallBudgetsForTest's
// reset branch can reference them instead of duplicating the literals a
// second time (which previously risked silently drifting out of sync with
// the vars' own initializers below).
const (
	defaultSettleCallBudget = 60 * time.Second
	defaultVerifyCallBudget = 20 * time.Second
	defaultSignCallBudget   = 20 * time.Second
)

// settleCallBudget bounds the detached sub-context attemptSelfSettle gives
// ONLY the Facilitator.Settle call (see its doc comment) -- generous
// headroom over x402.FacilitatorClient's own 20s http.Client timeout,
// which is what actually bounds how long that call can block; this is a
// backstop, not the real limiter, so it never needs to be tight.
//
// A var, not a const, like signCallBudget/verifyCallBudget below -- see
// SetSelfSettleCallBudgetsForTest's doc comment for why.
var settleCallBudget = defaultSettleCallBudget

// verifyCallBudget is generous headroom for attemptSelfSettle's Verify call,
// same rationale as settleCallBudget: x402.FacilitatorClient's own http.Client
// has a 20s timeout, which is what actually bounds Verify; this just needs to
// not be tighter than that.
var verifyCallBudget = defaultVerifyCallBudget

// signCallBudget covers the signing step of a self-settle attempt, which --
// unlike Verify/Settle above -- makes a real algod SuggestedParams network
// round trip (wallet.SignUSDCPaymentGroup/Single) with no timeout of its
// own from the algod client. So this is a real bound, not just a backstop.
var signCallBudget = defaultSignCallBudget

// SetSelfSettleCallBudgetsForTest overrides signCallBudget/verifyCallBudget/
// settleCallBudget together and recomputes SelfSettleRetryBudget to match.
// Call only from tests. Pass 0 to reset all three to their real defaults.
//
// These are vars rather than consts specifically so a test can shrink them
// to prove attemptSelfSettle's context.WithTimeout wrapping actually bounds
// a hung Sign/Verify/Settle call, instead of only being able to test the
// wiring indirectly (a fast transport-level error, or waiting out the real
// 20s/60s budgets, which no existing test does either). Safety of mutating
// these package-level vars with no lock rests entirely on Go's default
// sequential (non-t.Parallel) test execution within this package -- the
// same assumption every other SetXForTest override here already makes
// (e.g. action.go's SetResendAPIBaseForTest, connectors_data.go's
// SetHubSpotAPIBaseForTest). Not reachable from any production code path;
// if a future test in this package ever adds t.Parallel(), this function
// must not be called concurrently with anything reading these vars.
//
// Note this shrinks only the three per-call budgets, NOT the
// inter-attempt backoff -- so under a small override SelfSettleRetryBudget
// is dominated by worstCaseBackoffTotal() rather than by d, and is not
// proportional to d. Tests asserting on real elapsed time should derive
// their bound from both terms (see runfund_export_test.go).
func SetSelfSettleCallBudgetsForTest(d time.Duration) {
	if d <= 0 {
		signCallBudget = defaultSignCallBudget
		verifyCallBudget = defaultVerifyCallBudget
		settleCallBudget = defaultSettleCallBudget
	} else {
		signCallBudget = d
		verifyCallBudget = d
		settleCallBudget = d
	}
	SelfSettleRetryBudget = computeSelfSettleRetryBudget()
}

// SelfSettleRetryBudget is a sane ceiling a caller MAY wrap around
// SettlePlatformFee/FundRunReserve with (context.WithTimeout(ctx,
// SelfSettleRetryBudget) -- deliberately NOT context.WithoutCancel: unlike
// the narrow, actually-unsafe-to-interrupt Settle call itself (shielded
// internally, see settleCallBudget), everything else in a retry sequence
// -- signing, Verify, the gaps between attempts -- is safe to cancel, so
// there's no reason to make the whole call deaf to a StopWorkflow just to
// protect the one part that needs it. This exists purely as a backstop
// against an unbounded hang if the caller's own ctx has no deadline of
// its own: selfSettleMaxAttempts attempts, each budgeted for a full
// sign+verify+settle cycle -- signCallBudget for signing, verifyCallBudget
// for Verify, and settleCallBudget for Settle.
// Derived from the three per-call budgets above, not re-hardcoded, so this
// stays correct if any one of them changes; a version of this that omitted
// any one of them could let that many attempts' worth of calls alone eat
// enough of the ceiling that a legitimate later retry gets cut short by
// ctx.Err() -- the exact reliability gap this whole retry mechanism exists
// to close. Also adds worstCaseBackoffTotal() for the same reason --
// omitting it would let the backoff sleeps themselves eat into the budget
// meant for actual sign/verify/settle work. That term is the real
// per-attempt schedule, not a flat (attempts-1) * backoffMax
// approximation: see worstCaseBackoffTotal for why the flat version
// over-provisioned.
//
// This is a ceiling on THIS call's own work and nothing else. A caller
// that also does follow-up work under the same context (e.g. recording
// the settlement in the DB) must not rely on leftover slack here --
// there is none by construction, since the budget is sized to the worst
// case. runner.go's compensating writes each take their own detached
// context for exactly that reason.
//
// A var, computed once at package init and recomputed by
// SetSelfSettleCallBudgetsForTest, rather than a const: it's derived from
// signCallBudget/verifyCallBudget/settleCallBudget, which are themselves
// vars (see there for why).
var SelfSettleRetryBudget = computeSelfSettleRetryBudget()

func computeSelfSettleRetryBudget() time.Duration {
	return selfSettleMaxAttempts*(signCallBudget+verifyCallBudget+settleCallBudget) + worstCaseBackoffTotal()
}

func attemptSelfSettle(ctx context.Context, cfg RunPreFundConfig, publicPath, description, errPrefix string, amountUSDMicros int64) (string, error) {
	resourceURL := cfg.FrontendURL + publicPath
	reqs := x402.PaymentRequirements{
		Scheme:            "exact",
		Network:           cfg.RelayNetwork,
		MaxAmountRequired: strconv.FormatInt(amountUSDMicros, 10),
		// See RunPreFundConfig.FrontendURL's doc comment.
		Resource:          resourceURL,
		Description:       description,
		MimeType:          "application/json",
		PayTo:             cfg.PlatformWalletAddress,
		MaxTimeoutSeconds: 300,
		Asset:             strconv.FormatUint(cfg.ExpectedAssetID, 10),
		Extra: map[string]any{
			"asset":    strconv.FormatUint(cfg.ExpectedAssetID, 10),
			"feePayer": cfg.RelayFeePayer,
			"tag":      "x402-global-challenge",
			"decimals": 6,
		},
		// Bazaar discovery declaration on the struct actually POSTed to
		// /verify — extra.tag alone only attributes an already-discovered
		// route's activity to the challenge, it doesn't register the route.
		// Built by the shared BazaarDiscoveryExtension rather than spelled
		// out here: this block used to be a hand-maintained second copy of
		// the one in x402relay.go, which is exactly how the two drifted into
		// declaring method "GET" against an enum of ["GET","HEAD","DELETE"]
		// and silently failed the facilitator's catalog validator on every
		// settlement. RouteTemplate is the public /api proxy path, matching
		// resourceURL above -- origin+routeTemplate has to resolve to a real
		// URL. No queryParams (both self-settle routes take none) and no
		// output example (neither route returns a payable body; they answer
		// a static informational document).
		Extensions: BazaarDiscoveryExtension(BazaarDeclaration{RouteTemplate: publicPath}),
	}

	// signCallBudget bounds this: SignUSDCPaymentGroup makes a real algod
	// SuggestedParams round trip with no timeout of its own (see
	// signCallBudget's doc comment) -- without this, a single hung attempt
	// could burn the whole outer SelfSettleRetryBudget by itself, leaving
	// no time for the retries that budget exists to make room for.
	signCtx, signCancel := context.WithTimeout(ctx, signCallBudget)
	group, idx, err := cfg.USDCSigner.SignUSDCPaymentGroup(signCtx, cfg.PlatformSpendEncMnemonic, cfg.PlatformWalletAddress, cfg.ExpectedAssetID, uint64(amountUSDMicros), cfg.RelayFeePayer)
	signCancel()
	if err != nil {
		return "", fmt.Errorf("%s: sign failed: %w", errPrefix, err)
	}
	payload := x402.PaymentPayload{
		X402Version: 2,
		Scheme:      "exact",
		Network:     cfg.RelayNetwork,
		Payload:     x402.PaymentGroup{PaymentGroup: group, PaymentIndex: idx},
		// Set authoritatively regardless of the fact that WE are both payer
		// and resource server here -- same reasoning as x402relay.go's
		// relaySelfSettle/relaySettleAndForward: the facilitator's discovery
		// extraction reads resource/extensions off the PAYLOAD, not off
		// PaymentRequirements above, so without these two fields this
		// settlement (real money, real facilitator round trip) still had
		// nothing for the catalog to key off, exactly like every other
		// settlement path did before its own matching fix today.
		Resource:   map[string]any{"url": resourceURL, "description": description, "mimeType": "application/json", "serviceName": "AgentMesh", "tags": []string{"x402-global-challenge"}},
		Extensions: reqs.Extensions,
		// Required field of the v2 payload schema -- see
		// x402.PaymentPayload.Accepted's doc comment. This path already set
		// a positive maxTimeoutSeconds on reqs (unlike x402relay.go's two
		// settle paths, which sent zero until this fix), so the projection
		// below is schema-valid as-is.
		Accepted: reqs.AcceptedV2(),
	}

	// Verify runs on the caller's real, cancelable ctx (bounded by
	// verifyCallBudget on top of it): nothing has been broadcast yet, so an
	// interruption here is always safe -- it just fails this attempt
	// cleanly, the same as any other pre-Settle error.
	verifyCtx, verifyCancel := context.WithTimeout(ctx, verifyCallBudget)
	verifyResult, err := cfg.Facilitator.Verify(verifyCtx, payload, reqs)
	verifyCancel()
	if err != nil {
		return "", fmt.Errorf("%s: facilitator verify failed: %w", errPrefix, err)
	}
	if !verifyResult.IsValid {
		return "", fmt.Errorf("%s: payment invalid: %s", errPrefix, verifyResult.Invalid)
	}

	// Settle is the one call in this whole file where a cancellation
	// landing mid-flight is genuinely dangerous: the facilitator may have
	// already broadcast and confirmed the payment by the time ctx.Done()
	// fires, and losing the response here is indistinguishable from the
	// payment simply never having happened -- see ErrSettlementIndeterminate
	// below. Detached (WithoutCancel) with its own bounded budget rather
	// than inheriting ctx directly, so a StopWorkflow racing this exact
	// instant can't turn a real, possibly-already-broadcast payment into a
	// stuck "fate unknown" reservation purely because OUR OWN cancellation
	// beat the facilitator's response back. Everything else in this
	// function (signing, Verify, and the retry loop's between-attempts
	// gaps in the caller) stays on the real ctx and remains promptly
	// cancelable -- this is the narrowest possible window of protection,
	// not a blanket detach of the whole retry sequence.
	settleCtx, settleCancel := context.WithTimeout(context.WithoutCancel(ctx), settleCallBudget)
	settleResult, err := cfg.Facilitator.Settle(settleCtx, payload, reqs)
	settleCancel()
	if err != nil {
		// Response never arrived -- settlement's fate is unknown, not
		// "failed". Wrapped so callers can tell this apart from a
		// definitive rejection and avoid releasing a reservation for money
		// that may have already moved.
		return "", fmt.Errorf("%s: facilitator settle response lost: %v: %w", errPrefix, err, ErrSettlementIndeterminate)
	}
	if !settleResult.Success {
		// A real, received response says it failed -- money definitively
		// never moved.
		return "", fmt.Errorf("%s: settlement failed: %s", errPrefix, settleResult.Error)
	}
	return settleResult.TxID, nil
}
