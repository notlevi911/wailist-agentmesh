package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/agentmesh/backend/internal/db"
	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/respond"
)

// ledgerCompensationTimeout bounds Commit/Release once they're detached from
// the triggering request's context — same rationale as runner.go's constant
// of the same name, which this is a copy of (see newConsolePaymentLedger).
const ledgerCompensationTimeout = 10 * time.Second

// newConsolePaymentLedger is runner.go's newPaymentLedger without a
// *Runner: the console executes one Tendril node directly (runTendrilAction
// below), bypassing the graph engine entirely, so there is never a Runner
// to call that method on. Without this, X402RelayConfig.PerCallLedger
// stayed nil — and PaymentLedger's own doc comment says a nil field means
// "unconditionally allowed", so every reserve/commit hook inside
// executeTool402V2Relay silently no-opped. SignUSDCPaymentGroup still ran
// unconditionally, so the console's "run" action paid Tendril for real out
// of the platform wallet on every call while never debiting the caller's
// AgentMesh credit balance at all — this exact regression was reintroduced
// by the pr-50-tendril merge (master's PerCallLedger rename left this and
// runner.go's Tendril case populating only Ledger/LegacyLedger, both dead
// on this code path) and fixed 2026-08-05.
func newConsolePaymentLedger(store *db.Store, userID, workflowID, runID string) nodes.PaymentLedger {
	return nodes.PaymentLedger{
		Reserve: func(cctx context.Context, amountUSDMicros int64) error {
			return store.ReserveCredits(cctx, userID, amountUSDMicros)
		},
		Commit: func(cctx context.Context, nodeID string, amountUSDMicros int64, kind string) {
			bctx, cancel := context.WithTimeout(context.WithoutCancel(cctx), ledgerCompensationTimeout)
			defer cancel()
			if err := store.CommitReservedDebit(bctx, userID, amountUSDMicros, kind, workflowID, runID, nodeID); err != nil {
				log.Printf("CRITICAL: tendril console commit reserved debit failed (balance already decremented, no ledger row written): user=%s workflow=%s run=%s node=%s kind=%s amount=%d err=%v",
					userID, workflowID, runID, nodeID, kind, amountUSDMicros, err)
			}
		},
		Release: func(cctx context.Context, amountUSDMicros int64) {
			bctx, cancel := context.WithTimeout(context.WithoutCancel(cctx), ledgerCompensationTimeout)
			defer cancel()
			if err := store.ReleaseReservedCredits(bctx, userID, amountUSDMicros); err != nil {
				log.Printf("CRITICAL: tendril console release reserved credits failed (balance permanently stranded): user=%s amount=%d err=%v",
					userID, amountUSDMicros, err)
			}
		},
	}
}

// tendrilConsoleWorkflowName backs the Tendril console's direct-action
// endpoints (buy credit / rent / run) with one real, hidden workflow per
// user — there's no canvas UI for it, but the engine's node executors and
// the FK constraints on rows like tendril_leases (workflow_id, run_id) both
// need one to exist. GetOrCreateSystemWorkflow finds-or-creates it lazily on
// first use.
const tendrilConsoleWorkflowName = "Tendril Console (managed — do not edit)"

// TendrilConsoleWorkflow returns (creating on first call) the one hidden
// workflow that backs this user's Tendril console — so "Load Tendril
// workflow" in the workflow list opens the SAME row every time (never a
// fresh duplicate) and the canvas route can recognize it by id and render
// the console UI in place of the graph editor.
func (d *Deps) TendrilConsoleWorkflow(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(CtxUserID).(string)
	wf, err := d.Store.GetOrCreateSystemWorkflow(r.Context(), userID, tendrilConsoleWorkflowName)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "failed to open the tendril console")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"workflowId": wf.ID})
}

// TendrilConsoleWorkflowExists is TendrilConsoleWorkflow's read-only
// counterpart: reports whether this user's console workflow already exists,
// WITHOUT creating one. WorkflowRoute calls this (not the endpoint above)
// to decide whether the workflow id it's rendering is the Tendril console --
// every workflow-page visit hitting the create-on-first-call endpoint above
// instead would mint a hidden "Tendril Console" row for every user the
// instant they open any of their OWN, entirely unrelated workflows.
func (d *Deps) TendrilConsoleWorkflowExists(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(CtxUserID).(string)
	wf, found, err := d.Store.FindSystemWorkflow(r.Context(), userID, tendrilConsoleWorkflowName)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "failed to check the tendril console")
		return
	}
	if !found {
		respond.JSON(w, http.StatusOK, map[string]any{"exists": false})
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"exists": true, "workflowId": wf.ID})
}

// consoleRunContext satisfies nodes.RunContexter for the console's
// synthesized action nodes, whose input is always request-body fields, never
// a run's free-text trigger message — Message/UserInput only ever answer
// with what the caller explicitly passed in (the "run" action's payload).
type consoleRunContext struct{ message string }

func (c consoleRunContext) Message() string             { return c.message }
func (c consoleRunContext) LastOutput() any             { return c.message }
func (c consoleRunContext) UserInput() string           { return c.message }
func (c consoleRunContext) ToolOutputs() map[string]any { return nil }
func (c consoleRunContext) Set(string, any)             {}
func (c consoleRunContext) Get(string) (any, bool)      { return nil, false }
func (c consoleRunContext) OutputOrder() []string       { return nil }

// runTendrilAction executes exactly one Tendril node, bypassing the graph
// engine (TopologicalSort/Run) entirely — the console has no trigger, no
// chain, no "run the whole workflow": each button press is one direct call.
// A fresh Run row is created every call so LatestActiveLeaseForRun stays
// meaningful for the rent step that opens a lease; run/release steps that
// don't share that run_id fall back to LatestActiveLeaseForUser (see
// resolveLease in engine/nodes/tendril.go).
func (d *Deps) runTendrilAction(ctx context.Context, userID string, node models.WorkflowNode, message string) (any, error) {
	wf, err := d.Store.GetOrCreateSystemWorkflow(ctx, userID, tendrilConsoleWorkflowName)
	if err != nil {
		return nil, err
	}
	run, err := d.Store.CreateRun(ctx, wf.ID, "tendril-console", []byte("{}"))
	if err != nil {
		return nil, err
	}
	ledger := newConsolePaymentLedger(d.Store, userID, wf.ID, run.ID)
	cfg := nodes.TendrilConfig{
		Client:     d.TendrilClient,
		Session:    d.TendrilSession,
		Store:      d.Store,
		EncryptKey: d.EncryptionKey,
		UserID:     userID,
		WorkflowID: wf.ID,
		RunID:      run.ID,
		Relay: nodes.X402RelayConfig{
			USDCSigner:               d.USDCSigner,
			PlatformSpendEncMnemonic: d.PlatformSpendWalletEncMnemonic,
			ExpectedAssetID:          d.USDCAssetID,
			RelayBaseURL:             d.RelayBaseURL,
			Facilitator:              d.FacilitatorClient,
			PlatformWalletAddress:    d.PlatformWalletAddress,
			RelayNetwork:             d.RelayNetwork,
			RelayFeePayer:            d.RelayFeePayer,
			FrontendURL:              d.FrontendURL,
			Ledger:                   nodes.RunLedger(ledger),
			LegacyLedger:             nodes.CallLedger(ledger),
			PerCallLedger:            nodes.CallLedger(ledger),
		},
	}
	result, err := nodes.ExecuteTendril(ctx, node, consoleRunContext{message: message}, cfg)
	status := models.RunStatusSuccess
	if err != nil {
		status = models.RunStatusFailed
	}
	d.Store.FinishRun(ctx, run.ID, status)
	return result, err
}

// TendrilConsoleTopup buys Tendril credit directly — no workflow chain, no
// re-buying on every job you run.
func (d *Deps) TendrilConsoleTopup(w http.ResponseWriter, r *http.Request) {
	if d.TendrilClient == nil || d.TendrilSession == nil {
		respond.Error(w, http.StatusServiceUnavailable, "tendril is not configured on this server")
		return
	}
	userID, _ := r.Context().Value(CtxUserID).(string)
	var body struct {
		AmountUsd string `json:"amountUsd"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	node := models.WorkflowNode{ID: "tendril-console-topup", Type: models.NodeTypeTendril,
		TendrilAction: "topup", TendrilAmount: body.AmountUsd}
	result, err := d.runTendrilAction(r.Context(), userID, node, "")
	if err != nil {
		respond.Error(w, http.StatusBadGateway, err.Error())
		return
	}
	respond.JSON(w, http.StatusOK, result)
}

// TendrilConsoleRent opens a metered lease directly on a machine the caller
// picked from GET /tendril/machines.
func (d *Deps) TendrilConsoleRent(w http.ResponseWriter, r *http.Request) {
	if d.TendrilClient == nil || d.TendrilSession == nil {
		respond.Error(w, http.StatusServiceUnavailable, "tendril is not configured on this server")
		return
	}
	userID, _ := r.Context().Value(CtxUserID).(string)
	var body struct {
		NodeID string `json:"nodeId"`
		Hours  string `json:"hours"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// Empty NodeID/Hours are meaningful defaults on the canvas node path
	// (empty hours = 1 hour, empty NodeID = cheapest machine — see
	// executeTendrilRent), which is correct there but a footgun here: a
	// malformed console request would otherwise silently rent whatever
	// machine happens to be cheapest instead of the one the caller picked.
	if body.NodeID == "" || body.Hours == "" {
		respond.Error(w, http.StatusBadRequest, "nodeId and hours are required")
		return
	}

	node := models.WorkflowNode{ID: "tendril-console-rent", Type: models.NodeTypeTendril,
		TendrilAction: "rent", TendrilNodeID: body.NodeID, TendrilHours: body.Hours}
	result, err := d.runTendrilAction(r.Context(), userID, node, "")
	if err != nil {
		respond.Error(w, http.StatusBadGateway, err.Error())
		return
	}
	respond.JSON(w, http.StatusOK, result)
}

// TendrilConsoleRun executes a job on whichever machine this user currently
// has open (resolveLease's LatestActiveLeaseForUser fallback — the console
// has no single "run" that a Rent step shares a run_id with).
func (d *Deps) TendrilConsoleRun(w http.ResponseWriter, r *http.Request) {
	if d.TendrilClient == nil || d.TendrilSession == nil {
		respond.Error(w, http.StatusServiceUnavailable, "tendril is not configured on this server")
		return
	}
	userID, _ := r.Context().Value(CtxUserID).(string)
	var body struct {
		Payload string `json:"payload"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	node := models.WorkflowNode{ID: "tendril-console-run", Type: models.NodeTypeTendril,
		TendrilAction: "run"}
	result, err := d.runTendrilAction(r.Context(), userID, node, body.Payload)
	if err != nil {
		respond.Error(w, http.StatusBadGateway, err.Error())
		return
	}
	respond.JSON(w, http.StatusOK, result)
}
