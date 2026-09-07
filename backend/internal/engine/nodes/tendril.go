package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/agentmesh/backend/internal/alert"
	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/sshkeys"
	"github.com/agentmesh/backend/internal/tendril"
	"github.com/agentmesh/backend/internal/wallet"
)

// ReaperInterval is how often the lease reaper ticks (cmd/server/main.go's
// StartLeaseReaper call uses this, not a separately hardcoded duration, so
// it and ReleaseLease's overrun-alert tolerance below can never drift apart).
const ReaperInterval = time.Minute

// tendrilRentGateFeeAtomic is Tendril's flat charge to open a lease, confirmed
// live 2026-08-04 in the /x402/rent challenge (amount "10000", 6 decimals).
// Renting does NOT buy time — time meters from the paying address's credit
// balance, which is why RequiredCreditAtomic adds hours on top.
//
// This is a REAL on-chain payment, made through payTendril -> the x402
// relay -> Wallet 1 -> Wallet 2 -> Tendril, exactly like every other paid
// Tendril call -- the relay's own Reserve/Commit already bills it (plus
// models.X402PlatformFeeUSDMicros markup) against the user's AgentMesh
// credit. It must NOT also be folded into RequiredCreditAtomic's
// Tendril-credit reservation below:
// doing so double-charged the user for the same $0.01 in two different
// ledgers. RequiredCreditAtomic now only reserves the metered hourly time,
// which is the one thing genuinely drawn from the user's own Tendril credit.
const tendrilRentGateFeeAtomic int64 = 10_000

// maxTendrilHours caps a single rent. At $6/hr a fat-fingered "100" would
// commit $600 of real mainnet USDC in one click.
const maxTendrilHours = 24.0

// RequiredCreditAtomic is how much of THIS USER's Tendril credit a rent
// reserves for metered hourly time. Not the pool's balance — the pool is a
// shared custodial float that holds every user's topups at once, so it can
// never be the thing a rent is authorized against. Does NOT include
// tendrilRentGateFeeAtomic -- see that constant's doc comment for why the
// gate fee is billed separately, in AgentMesh credit, by the relay.
func RequiredCreditAtomic(rateUSDMicrosPerHour int64, hours float64) int64 {
	return int64(float64(rateUSDMicrosPerHour)*hours + 0.5)
}

func parseHours(raw string) (float64, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 1, nil
	}
	h, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("tendril: hours %q is not a number", raw)
	}
	if h <= 0 {
		return 0, fmt.Errorf("tendril: hours must be positive, got %v", h)
	}
	if h > maxTendrilHours {
		return 0, fmt.Errorf("tendril: hours must be at most %v, got %v", maxTendrilHours, h)
	}
	return h, nil
}

func parseTopupUSD(raw string) (float64, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, fmt.Errorf("tendril: set a topup amount in USD")
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("tendril: topup amount %q is not a number", raw)
	}
	if v <= 0 {
		return 0, fmt.Errorf("tendril: topup amount must be positive, got %v", v)
	}
	return v, nil
}

// TendrilStore is the slice of *db.Store this package needs, as an interface
// so tests can drive the executor without a database.
type TendrilStore interface {
	InsertTendrilLease(ctx context.Context, l models.TendrilLease) (models.TendrilLease, error)
	GetTendrilLease(ctx context.Context, id string) (models.TendrilLease, error)
	// MarkTendrilLeaseReleased's bool reports whether THIS call performed a
	// genuine active->released transition -- see the Store implementation's
	// doc comment for why ReleaseLease's refund logic depends on it.
	MarkTendrilLeaseReleased(ctx context.Context, id string, usedSeconds, chargedUSDMicros int64) (bool, error)
	LatestActiveLeaseForRun(ctx context.Context, runID string) (models.TendrilLease, error)
	// LatestActiveLeaseForUser is resolveLease's fallback for the Tendril
	// console's direct-action endpoints, where run/release each get a fresh
	// run_id of their own — the same-run lookup above never matches — so the
	// only thing left to resolve against is "whichever machine this user
	// currently has open."
	LatestActiveLeaseForUser(ctx context.Context, userID string) (models.TendrilLease, error)
	// Credit sub-ledger (Task 6) — the authority on what THIS user may spend.
	TendrilCreditBalance(ctx context.Context, userID string) (int64, error)
	CreditBalance(ctx context.Context, userID string) (int64, error)
	ConvertCreditsToTendril(ctx context.Context, userID string, amountUSDMicros int64, txID string) (int64, error)
	ChargeTendrilCredit(ctx context.Context, userID, leaseID, kind string, amountUSDMicros int64) error
}

type TendrilConfig struct {
	Client     *tendril.Client
	Session    *tendril.Session
	Store      TendrilStore
	EncryptKey string
	Relay      X402RelayConfig
	UserID     string
	WorkflowID string
	RunID      string
}

func ExecuteTendril(ctx context.Context, node models.WorkflowNode, rc RunContexter, cfg TendrilConfig) (any, error) {
	switch node.TendrilAction {
	case "", "rent":
		return executeTendrilRent(ctx, node, cfg)
	case "topup":
		return executeTendrilTopup(ctx, node, cfg)
	case "run":
		return executeTendrilRun(ctx, node, rc, cfg)
	case "release":
		return executeTendrilRelease(ctx, node, cfg)
	default:
		return nil, fmt.Errorf("tendril: unknown action %q", node.TendrilAction)
	}
}

// executeTendrilTopup buys Tendril credit for THIS user. It settles a real
// USDC payment into the shared Wallet 2 pool and, in the same breath, moves
// the same value from the user's AgentMesh credits to their Tendril credits.
//
// The pool is universal — every user's topups accumulate in it — but what a
// user may spend is only ever their own converted balance. That is why the
// conversion is not optional bookkeeping: it IS the spending authority.
func executeTendrilTopup(ctx context.Context, node models.WorkflowNode, cfg TendrilConfig) (any, error) {
	amountUSD, err := parseTopupUSD(node.TendrilAmount)
	if err != nil {
		return nil, err
	}
	atomic := int64(amountUSD*1e6 + 0.5)

	// Tendril's own bounds, read live rather than hardcoded.
	platform, err := cfg.Client.Platform(ctx)
	if err != nil {
		return nil, fmt.Errorf("tendril: platform: %w", err)
	}
	if platform.MinTopUpAtomic > 0 && atomic < platform.MinTopUpAtomic {
		return nil, fmt.Errorf("tendril: minimum topup is %s", formatUSDCAmount(platform.MinTopUpAtomic))
	}
	if platform.MaxTopUpAtomic > 0 && atomic > platform.MaxTopUpAtomic {
		return nil, fmt.Errorf("tendril: maximum topup is %s", formatUSDCAmount(platform.MaxTopUpAtomic))
	}

	// Refuse before paying if the user cannot afford it. Settling first and
	// discovering the shortfall afterwards would put real USDC in the pool
	// with no user entitled to spend it. executeTool402V2Relay reserves
	// amount + models.X402PlatformFeeUSDMicros regardless of caller (see
	// its `total :=` line), so the console path is billed the same markup
	// as the main graph engine's standalone tool402 dispatch.
	realCost := atomic + models.X402PlatformFeeUSDMicros
	balance, err := cfg.Store.TendrilCreditBalance(ctx, cfg.UserID)
	if err != nil {
		return nil, err
	}
	agentMeshBalance, err := cfg.Store.CreditBalance(ctx, cfg.UserID)
	if err != nil {
		return nil, err
	}
	if agentMeshBalance < realCost {
		return nil, fmt.Errorf("tendril: topup of %s needs %s in AgentMesh credits, you have %s",
			formatUSDCAmount(atomic), formatUSDCAmount(realCost), formatUSDCAmount(agentMeshBalance))
	}

	receipt, err := payTendril(ctx, cfg, fmt.Sprintf("/topup?amount=%d", atomic), nil, "")
	if err != nil {
		return nil, err
	}
	// ExecuteTool402V2 returns a "successful" result with ZERO money moved
	// whenever the target answers with anything other than a 402 (a
	// Tendril-side outage, maintenance page, or misconfigured platform spend
	// wallet all take this path) -- require positive proof a real payment
	// settled before minting any Tendril credit. See payTendril's doc
	// comment for the unbacked-credit bug this closes.
	if receipt.SettledUSDMicros <= 0 {
		return nil, fmt.Errorf("tendril: topup did not settle (no payment confirmed) -- nothing was charged, try again")
	}

	txID := receipt.TxID
	if txID == "" {
		if m, ok := receipt.Response.(map[string]any); ok {
			txID, _ = m["txId"].(string)
		}
	}
	newBalance, err := cfg.Store.ConvertCreditsToTendril(ctx, cfg.UserID, atomic, txID)
	if err != nil {
		// The USDC really moved into the pool. Surface that loudly: the pool
		// is now larger than the sum of user entitlements, which is the one
		// direction of drift that is safe but must still be reconciled.
		return nil, fmt.Errorf("tendril: topup settled on-chain (tx %s) but crediting your balance failed — contact support with that tx id: %w", txID, err)
	}

	out := map[string]any{
		"toppedUp":             formatUSDCAmount(atomic),
		"tendrilCreditBalance": formatUSDCAmount(newBalance),
		"previousBalance":      formatUSDCAmount(balance),
		"note":                 "Tendril credit is separate from your AgentMesh credits and can only be spent on Tendril machine time.",
	}
	if m, ok := receipt.Response.(map[string]any); ok {
		for _, k := range []string{"txId", "explorerURL", "outboundTxId", "outboundExplorerURL"} {
			if v, ok := m[k]; ok {
				out[k] = v
			}
		}
	}
	return out, nil
}

func executeTendrilRent(ctx context.Context, node models.WorkflowNode, cfg TendrilConfig) (any, error) {
	hours, err := parseHours(node.TendrilHours)
	if err != nil {
		return nil, err
	}

	machines, err := cfg.Client.OnlineNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("tendril: market: %w", err)
	}
	if len(machines) == 0 {
		return nil, fmt.Errorf("tendril: no machines are online right now")
	}
	machine := machines[0] // cheapest, per OnlineNodes' ordering
	if node.TendrilNodeID != "" {
		found := false
		for _, m := range machines {
			if m.ID == node.TendrilNodeID {
				machine, found = m, true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("tendril: machine %q is not online", node.TendrilNodeID)
		}
	}

	// Reserve the hours against THIS user's Tendril credit. The shared Wallet 2
	// pool is deliberately not consulted: it holds every user's topups at once,
	// so checking it would let one user rent on hours somebody else bought.
	// Their own balance is the only authority.
	need := RequiredCreditAtomic(machine.RateUSDMicrosPerHour(), hours)
	userCredit, err := cfg.Store.TendrilCreditBalance(ctx, cfg.UserID)
	if err != nil {
		return nil, err
	}
	if userCredit < need {
		return nil, fmt.Errorf(
			"tendril: %v hour(s) on %s costs %s but your Tendril credit is %s — add more Tendril credit first",
			hours, machine.ID, formatUSDCAmount(need), formatUSDCAmount(userCredit))
	}
	// The gate fee below is a separate real charge, billed in AgentMesh
	// credit by the relay -- refuse before paying if the user can't cover
	// it, same rationale as executeTendrilTopup's pre-check, including the
	// same models.X402PlatformFeeUSDMicros markup executeTool402V2Relay
	// always reserves on top of the target's quoted amount.
	gateFeeRealCost := tendrilRentGateFeeAtomic + models.X402PlatformFeeUSDMicros
	agentMeshBalance, err := cfg.Store.CreditBalance(ctx, cfg.UserID)
	if err != nil {
		return nil, err
	}
	if agentMeshBalance < gateFeeRealCost {
		return nil, fmt.Errorf(
			"tendril: opening a lease needs %s in AgentMesh credits for the gate fee, you have %s",
			formatUSDCAmount(gateFeeRealCost), formatUSDCAmount(agentMeshBalance))
	}
	if err := cfg.Store.ChargeTendrilCredit(ctx, cfg.UserID, "", "charge", need); err != nil {
		return nil, fmt.Errorf("tendril: reserve credit: %w", err)
	}
	// From here on the user has paid; any failure below must hand the
	// reservation back rather than silently keeping it.
	reserved := true
	defer func() {
		if reserved {
			if err := cfg.Store.ChargeTendrilCredit(context.Background(), cfg.UserID, "", "refund", need); err != nil {
				log.Printf("tendril: FAILED to refund %d micros to user %s after a failed rent: %v", need, cfg.UserID, err)
			}
		}
	}()

	// A sanity check on the custodial float, not an authorization check. If the
	// pool cannot cover what users have collectively bought, the invariant has
	// been violated upstream and renting would silently fail at Tendril's end.
	if poolBalance, perr := cfg.Session.Balance(ctx); perr == nil && poolBalance < need {
		return nil, fmt.Errorf("tendril: the platform pool is short (%s available, %s needed) — this is a platform-side problem, not yours; no credit was spent",
			formatUSDCAmount(poolBalance), formatUSDCAmount(need))
	}

	sshPub, sshPriv, err := sshkeys.Generate()
	if err != nil {
		return nil, fmt.Errorf("tendril: ssh keygen: %w", err)
	}
	body, _ := json.Marshal(map[string]string{"sshPubKey": sshPub})
	raw, err := payTendril(ctx, cfg, "/x402/rent?nodeId="+machine.ID, body, "")
	if err != nil {
		return nil, err
	}
	// Same proof-of-settlement requirement as executeTendrilTopup: a
	// non-402 response from Tendril (outage, maintenance page, proxy
	// error) otherwise looks like a successful call with nothing paid --
	// refuse to persist a lease and spend the reservation on one.
	if raw.SettledUSDMicros <= 0 {
		return nil, fmt.Errorf("tendril: rent did not settle (no payment confirmed) -- nothing was charged, try again")
	}

	lease, err := decodeRentResponse(raw.Response)
	if err != nil {
		return nil, err
	}

	// From here on we hold a real, known lease id and token: the machine is
	// live and metering at Tendril regardless of what happens in the rest
	// of this function. A failure below (encrypting a credential, or
	// persisting the row) used to just return an error -- the deferred
	// refund above hands the user's Tendril credit back, but the machine
	// itself keeps running with no local row for the reaper to ever find,
	// metering against the shared pool indefinitely with nobody watching.
	// persisted tracks whether InsertTendrilLease below actually succeeded;
	// if it didn't, attempt a compensating Release before returning so the
	// meter actually stops, and alert if even that fails -- matching this
	// codebase's convention for other "real money moved, cleanup itself
	// failed" situations (see reserveAndFundRun's own CRITICAL alerts).
	persisted := false
	defer func() {
		if persisted {
			return
		}
		if _, relErr := cfg.Client.Release(context.Background(), lease.LeaseID, lease.LeaseToken); relErr != nil {
			msg := fmt.Sprintf("CRITICAL: tendril rent for user %s paid and opened lease %s, but persisting it locally failed and the compensating Release ALSO failed (%v) -- machine is live and metering with no local row, reconcile by hand",
				cfg.UserID, lease.LeaseID, relErr)
			log.Print(msg)
			go alert.Notify(context.Background(), alert.ChannelPayments, msg)
		}
	}()

	tokenEnc, err := wallet.Encrypt(lease.LeaseToken, cfg.EncryptKey)
	if err != nil {
		return nil, fmt.Errorf("tendril: encrypt lease token: %w", err)
	}
	keyEnc, err := wallet.Encrypt(sshPriv, cfg.EncryptKey)
	if err != nil {
		return nil, fmt.Errorf("tendril: encrypt ssh key: %w", err)
	}
	passwordEnc := ""
	if lease.SSH.Password != "" {
		if passwordEnc, err = wallet.Encrypt(lease.SSH.Password, cfg.EncryptKey); err != nil {
			return nil, fmt.Errorf("tendril: encrypt ssh password: %w", err)
		}
	}

	fundedUntil, err := time.Parse(time.RFC3339, lease.FundedUntil)
	if err != nil {
		fundedUntil = time.Now().Add(time.Duration(hours * float64(time.Hour)))
	}

	saved, err := cfg.Store.InsertTendrilLease(ctx, models.TendrilLease{
		UserID: cfg.UserID, WorkflowID: cfg.WorkflowID, RunID: cfg.RunID, NodeID: node.ID,
		LeaseID: lease.LeaseID, LeaseTokenEnc: tokenEnc,
		TendrilNodeID: machine.ID, TendrilNodeLabel: machine.Label,
		SSHHost: lease.SSH.Host, SSHPort: lease.SSH.Port, SSHUsername: lease.SSH.Username,
		SSHCommand: lease.SSH.Command, SSHPublicKey: sshPub,
		SSHPrivateKeyEnc: keyEnc, SSHPasswordEnc: passwordEnc,
		RateUSDMicrosPerHour: machine.RateUSDMicrosPerHour(),
		HoursPurchased:       hours,
		ReservedUSDMicros:    need,
		FundedUntil:          fundedUntil,
	})
	if err != nil {
		return nil, fmt.Errorf("tendril: persist lease: %w", err)
	}
	// The lease is durably recorded -- stop the compensating Release above
	// from firing, and stop the deferred refund from clawing back a
	// reservation that's now legitimately spent.
	persisted = true
	reserved = false

	remaining, _ := cfg.Store.TendrilCreditBalance(ctx, cfg.UserID)

	// The lease token never leaves the server. Everything here is safe to show
	// in the console and to cache in localStorage with the run transcript.
	out := map[string]any{
		"agentMeshLeaseId": saved.ID,
		"leaseId":          lease.LeaseID,
		"machine":          map[string]any{"id": machine.ID, "label": machine.Label, "cpuCores": machine.CPUCores, "ramMb": machine.RAMMb, "pricePerHourUsd": machine.PricePerHourUSD},
		"hours":            hours,
		"ssh":              map[string]any{"host": lease.SSH.Host, "port": lease.SSH.Port, "username": lease.SSH.Username, "command": lease.SSH.Command},
		"fundedUntil":      fundedUntil.Format(time.RFC3339),
		"reservedUsd":      formatUSDCAmount(need),
		// What the user has left to spend on Tendril — the number the canvas
		// shows them, and the only balance that governs what they may rent.
		"tendrilCreditBalance": formatUSDCAmount(remaining),
	}
	if m, ok := raw.Response.(map[string]any); ok {
		for _, k := range []string{"txId", "explorerURL", "outboundTxId", "outboundExplorerURL"} {
			if v, ok := m[k]; ok {
				out[k] = v
			}
		}
	}
	return out, nil
}

type rentResponse struct {
	LeaseID     string `json:"leaseId"`
	LeaseToken  string `json:"leaseToken"`
	FundedUntil string `json:"fundedUntil"`
	SSH         struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Username string `json:"username"`
		Command  string `json:"command"`
		Password string `json:"password"`
	} `json:"ssh"`
}

func decodeRentResponse(raw any) (rentResponse, error) {
	var lease rentResponse
	blob, err := json.Marshal(raw)
	if err != nil {
		return lease, fmt.Errorf("tendril: rent response: %w", err)
	}
	if err := json.Unmarshal(blob, &lease); err != nil {
		return lease, fmt.Errorf("tendril: rent response: %w", err)
	}
	if lease.LeaseID == "" || lease.LeaseToken == "" {
		// Money has already moved at this point, so say so loudly rather than
		// returning a lease-shaped zero value.
		return lease, fmt.Errorf("tendril: rent settled but returned no lease: %s", truncateJSON(blob))
	}
	return lease, nil
}

func truncateJSON(b []byte) string {
	if len(b) > 400 {
		return string(b[:400])
	}
	return string(b)
}

// payTendril runs one paid Tendril call through the EXISTING relay path by
// synthesizing a tool402 node for it. Nothing about payment is reimplemented
// here: ExecuteTool402V2 probes the 402, picks the group-vs-single signer off
// extra.feePayer, settles through Wallet 1 -> Wallet 2 -> Tendril, and bills
// the user's credit balance through cfg.Relay's ledger exactly as a normal
// x402 tool call does.
//
// bearer, when set, is the Tendril lease token the TARGET needs (for /x402/run
// against a machine the user already holds). It is not auth for our own relay
// — see the X-Relay-Auth passthrough added in Task 10.
//
// Returns the full Tool402PaymentResult, not just its Response body:
// ExecuteTool402V2 returns (response, nil) -- a "successful" call with ZERO
// money moved -- whenever the target answers with anything other than a 402
// (a Tendril-side outage, maintenance page, proxy error, or misconfigured
// platform spend wallet all take this path). A caller that credits the user
// based on the response body alone, without checking SettledUSDMicros, mints
// value backed by nothing. Callers that mutate a balance based on this call
// MUST check SettledUSDMicros > 0 (proof a real payment settled) before
// doing so -- see executeTendrilTopup and executeTendrilRent.
func payTendril(ctx context.Context, cfg TendrilConfig, path string, body []byte, bearer string) (Tool402PaymentResult, error) {
	node := models.WorkflowNode{
		ID:       "tendril:" + path,
		Type:     models.NodeTypeTool402,
		Endpoint: strings.TrimRight(cfg.Client.BaseURL(), "/") + path,
		Method:   http.MethodPost,
	}
	if len(body) > 0 {
		node.BodyMode = models.BodyModeJSON
		node.BodyTemplate = string(body)
	}
	if bearer != "" {
		node.TendrilLeaseToken = bearer
	}
	res, err := ExecuteTool402V2(ctx, node, emptyRunContext{}, models.AgentWallet{}, nil, cfg.Relay)
	if err != nil {
		return Tool402PaymentResult{}, fmt.Errorf("tendril %s: %w", path, err)
	}
	return res, nil
}

// emptyRunContext satisfies RunContexter for the synthesized nodes above,
// whose bodies are fully specified by BodyTemplate and must never pick up the
// run's free-text trigger message.
type emptyRunContext struct{}

func (emptyRunContext) Message() string             { return "" }
func (emptyRunContext) LastOutput() any             { return nil }
func (emptyRunContext) UserInput() string           { return "" }
func (emptyRunContext) ToolOutputs() map[string]any { return nil }
func (emptyRunContext) Set(string, any)             {}
func (emptyRunContext) Get(string) (any, bool)      { return nil, false }

// ReleaseLease stops the meter and records what Tendril actually charged.
// Shared by the release node, the REST endpoint, and the reaper so all three
// bill identically.
//
// The int64 return is what was ACTUALLY refunded to lease.UserID's Tendril
// credit, in micros -- always 0 except on the one path below that calls
// ChargeTendrilCredit with kind "refund". Callers must report THIS value to
// the user, not independently recompute reservedUSDMicros-charged: that
// recomputation is wrong on the 404 path (no real charge data exists to
// subtract from) and on the not-transitioned path (this call didn't refund
// anything -- some other caller already did, or didn't).
func ReleaseLease(ctx context.Context, cfg TendrilConfig, lease models.TendrilLease) (tendril.ReleaseResult, int64, error) {
	token, err := wallet.Decrypt(lease.LeaseTokenEnc, cfg.EncryptKey)
	if err != nil {
		return tendril.ReleaseResult{}, 0, fmt.Errorf("tendril: decrypt lease token: %w", err)
	}
	res, err := cfg.Client.Release(ctx, lease.LeaseID, token)
	if err != nil {
		// Tendril's own watchdog reaps abandoned leases, so a lease it no
		// longer knows about is already stopped and already billed. Treating
		// that as a failure would leave our row 'active' forever and have the
		// reaper retry it on every tick.
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found") {
			transitioned, merr := cfg.Store.MarkTendrilLeaseReleased(ctx, lease.ID, 0, 0)
			if merr != nil {
				return tendril.ReleaseResult{}, 0, merr
			}
			// A 404 carries no charged amount for us to reconcile against --
			// unlike a normal release, there's no reservation math to do
			// here at all. If THIS call is the one that closed the row
			// locally (transitioned == true), the lease's real fate at
			// Tendril is unknown: it may have used the full reservation, or
			// none of it. Refunding anything here -- as this used to do
			// unconditionally -- reported a phantom "fully refunded" for
			// money whose real disposition nobody actually confirmed.
			// Alert so it gets reconciled by hand instead of guessing.
			if transitioned {
				msg := fmt.Sprintf("tendril lease %s (user %s, reserved %d micros) closed via 404 from Tendril (its own watchdog beat us to it) -- real usage unknown, nothing refunded, reconcile by hand",
					lease.LeaseID, lease.UserID, lease.ReservedUSDMicros)
				log.Print(msg)
				go alert.Notify(context.Background(), alert.ChannelPayments, msg)
			}
			// !transitioned means some other caller (a concurrent release
			// click, or the reaper) already closed this row -- that caller
			// owns whatever reconciliation was possible; doing it again
			// here would double up.
			return tendril.ReleaseResult{}, 0, nil
		}
		return tendril.ReleaseResult{}, 0, fmt.Errorf("tendril: release: %w", err)
	}
	charged := int64(res.ChargedAtomic)
	transitioned, err := cfg.Store.MarkTendrilLeaseReleased(ctx, lease.ID, res.UsedSeconds, charged)
	if err != nil {
		return res, 0, err
	}
	// Tendril's own DELETE just succeeded with real charge data, but our row
	// was already 'released' by someone else (a concurrent release click, or
	// the reaper beating this call to it) -- that caller already ran the
	// refund/overrun accounting below once; running it again here on the
	// same underlying charge would double-refund or double-alert.
	if !transitioned {
		return res, 0, nil
	}

	var refunded int64
	// Return the unused part of the reservation as Tendril credit — not as
	// AgentMesh credit. The user bought hours; releasing early means they
	// still hold those hours, just not on this machine. Refunding to AgentMesh
	// credits instead would let a user cycle rent/release to convert Tendril
	// credit back into general platform credit, which the pool cannot honour
	// (the USDC is already sitting at Tendril).
	if unused := lease.ReservedUSDMicros - charged; unused > 0 {
		if err := cfg.Store.ChargeTendrilCredit(ctx, lease.UserID, lease.ID, "refund", unused); err != nil {
			// The lease is already closed and billed; a failed refund is a
			// reconciliation problem, not a reason to report the release as
			// failed and have the reaper retry a DELETE that already ran.
			log.Printf("tendril: lease %s released but refunding %d micros to user %s failed: %v",
				lease.LeaseID, unused, lease.UserID, err)
		} else {
			refunded = unused
		}
	} else if overrun := charged - lease.ReservedUSDMicros; overrun > 0 {
		// Tendril charged MORE than this lease's own upfront reservation --
		// a platform-cost overrun (the reap window closing later than the
		// user's paid time, a rate mismatch, or a bug at Tendril's end).
		// Silently doing nothing here would absorb the difference: the
		// user's Tendril credit was never debited for it, so the platform
		// ate the cost with no record of it having happened. Not
		// auto-charging the user more (that could fail if they lack the
		// credit, or surprise them for compute they didn't ask to keep
		// paying for) -- alert loudly instead so it gets reconciled by
		// hand, matching this codebase's convention for other "real money
		// moved, the accounting doesn't line up" situations.
		//
		// tolerance absorbs the ROUTINE case, not just the exceptional one:
		// the reaper ticks once a minute (cmd/server/main.go's
		// StartLeaseReaper interval), so a lease that runs its full
		// purchased window is expected to be reaped up to ~60s late, and
		// ReservedUSDMicros no longer carries the old $0.01 gate-fee
		// buffer (RequiredCreditAtomic reserves metered time only -- see
		// its own doc comment). Without a tolerance, ordinary reaper lag on
		// nearly every full-term lease would page this CRITICAL alert,
		// which is exactly how a real overrun later gets ignored. Two full
		// reap intervals' worth of this lease's own rate is a deliberately
		// generous margin -- log, don't page, below it.
		tolerance := lease.RateUSDMicrosPerHour * 2 * int64(ReaperInterval/time.Second) / 3600
		if overrun <= tolerance {
			log.Printf("tendril: lease %s (user %s) charged %d micros against a %d micro reservation -- %d micro overrun within reaper-lag tolerance (%d), not alerting",
				lease.LeaseID, lease.UserID, charged, lease.ReservedUSDMicros, overrun, tolerance)
		} else {
			msg := fmt.Sprintf("CRITICAL: tendril lease %s (user %s) charged %d micros against a %d micro reservation -- %d micro overrun (tolerance %d) absorbed by the platform, not billed to the user",
				lease.LeaseID, lease.UserID, charged, lease.ReservedUSDMicros, overrun, tolerance)
			log.Print(msg)
			go alert.Notify(context.Background(), alert.ChannelPayments, msg)
		}
	}
	return res, refunded, nil
}

func executeTendrilRelease(ctx context.Context, node models.WorkflowNode, cfg TendrilConfig) (any, error) {
	lease, err := resolveLease(ctx, node, cfg)
	if err != nil {
		return nil, err
	}
	// Same guard as the REST /leases/:id/release handler and for the same
	// reason: an already-released lease falls into ReleaseLease's 404
	// fallback (a zero-valued result, no error), which would otherwise
	// report the full reservation as "refunded" a second time — a phantom
	// refund for money that was already accounted for (or, if Tendril's own
	// watchdog closed the lease independently, never refunded at all).
	if lease.Status != "active" {
		return nil, fmt.Errorf("tendril: lease %s is already released", lease.LeaseID)
	}
	res, refunded, err := ReleaseLease(ctx, cfg, lease)
	if err != nil {
		return nil, err
	}
	remaining, _ := cfg.Store.TendrilCreditBalance(ctx, lease.UserID)
	return map[string]any{
		"agentMeshLeaseId": lease.ID,
		"leaseId":          lease.LeaseID,
		"usedSeconds":      res.UsedSeconds,
		"charged":          formatUSDCAmount(int64(res.ChargedAtomic)),
		// The amount ReleaseLease ACTUALLY refunded, not
		// reservedUSDMicros-charged recomputed here -- that recomputation
		// is wrong on the 404 (watchdog beat us to it, no real charge data)
		// and not-transitioned (some other caller already handled it, or
		// didn't) paths, both of which refund nothing regardless of what
		// this stale subtraction would suggest.
		"refunded": formatUSDCAmount(refunded),
		// Deliberately NOT res.Balance: that is the shared pool, which is
		// every user's money and must never be shown to one of them.
		"tendrilCreditBalance": formatUSDCAmount(remaining),
	}, nil
}

// resolveLease finds which lease a run/release node acts on. A node may name
// one explicitly via TendrilNodeID; otherwise it takes the lease this run
// opened, which is what the trigger -> rent -> run -> release workflow wants.
func resolveLease(ctx context.Context, node models.WorkflowNode, cfg TendrilConfig) (models.TendrilLease, error) {
	if node.TendrilNodeID != "" {
		lease, err := cfg.Store.GetTendrilLease(ctx, node.TendrilNodeID)
		// Ownership check, matching every REST handler in handlers/leases.go
		// (GetTendrilLease itself is a bare WHERE id = $1, no user scoping).
		// node.TendrilNodeID here is an AgentMesh lease row id set via
		// PUT /workflows/{id}, accepted verbatim -- not restricted to values
		// the Inspector's own machine picker would ever produce. Without
		// this check, a run/release node pointing at another user's lease
		// id would decrypt and act on that user's SSH machine. 404, not
		// 403, so this can't be used to confirm another user's lease id is
		// valid.
		if err != nil || lease.UserID != cfg.UserID {
			return models.TendrilLease{}, fmt.Errorf("tendril: lease %q not found", node.TendrilNodeID)
		}
		return lease, nil
	}
	if lease, err := cfg.Store.LatestActiveLeaseForRun(ctx, cfg.RunID); err == nil {
		return lease, nil
	}
	// The Tendril console's Run/Release actions each execute under their own
	// fresh run_id (they are not steps of the workflow that opened the
	// lease), so the run-scoped lookup above never matches there — fall back
	// to "whichever machine this user currently has open." Still scoped to
	// one user, never the shared pool.
	lease, err := cfg.Store.LatestActiveLeaseForUser(ctx, cfg.UserID)
	if err != nil {
		return models.TendrilLease{}, fmt.Errorf("tendril: no lease to act on — rent a machine first: %w", err)
	}
	return lease, nil
}

func executeTendrilRun(ctx context.Context, node models.WorkflowNode, rc RunContexter, cfg TendrilConfig) (any, error) {
	payload := strings.TrimSpace(rc.Message())
	for _, p := range node.CustomParams {
		if p.Name == "payload" && strings.TrimSpace(p.Value) != "" {
			payload = p.Value
		}
	}
	if payload == "" {
		return nil, fmt.Errorf("tendril: run needs a payload — set one on the node or pass it as the run's input")
	}

	// A lease is optional: with one, the job runs inside the machine the user
	// is already paying for; without one, Tendril picks an idle machine and
	// bills the seconds. Both are the same paid endpoint.
	body, _ := json.Marshal(map[string]string{"payload": payload})
	var leaseToken string
	if lease, err := resolveLease(ctx, node, cfg); err == nil && lease.LeaseTokenEnc != "" {
		if tok, derr := wallet.Decrypt(lease.LeaseTokenEnc, cfg.EncryptKey); derr == nil {
			leaseToken = tok
		}
	}
	res, err := payTendril(ctx, cfg, "/x402/run", body, leaseToken)
	if err != nil {
		return nil, err
	}
	return res.Response, nil
}
