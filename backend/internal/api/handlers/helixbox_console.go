package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/helixbox"
	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/respond"
)

// helixboxConsoleWorkflowName backs the HelixBox console's direct-action
// endpoint with one real, hidden workflow per user. There is no canvas UI for
// it, but the engine's node executors and the run/debit-ledger rows both need a
// workflow to hang off. GetOrCreateSystemWorkflow finds-or-creates it lazily on
// first use.
//
// A sibling of prismConsoleWorkflowName and tendrilConsoleWorkflowName, not a
// generalisation of them: the three consoles share this scaffolding shape and
// almost nothing else, so they stay parallel files that are easy to diff rather
// than an abstraction over three members.
const helixboxConsoleWorkflowName = "HelixBox Console (managed, do not edit)"

// HelixboxConsoleWorkflow returns (creating on first call) the one hidden
// workflow that backs this user's HelixBox console, so every entry point into
// the console opens the SAME row rather than minting a duplicate.
func (d *Deps) HelixboxConsoleWorkflow(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(CtxUserID).(string)
	wf, err := d.Store.GetOrCreateSystemWorkflow(r.Context(), userID, helixboxConsoleWorkflowName)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "Could not open the HelixBox console. Try again in a moment.")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"workflowId": wf.ID})
}

// HelixboxConsoleWorkflowExists is the read-only counterpart: it reports
// whether this user's console workflow exists WITHOUT creating one, so a caller
// asking the question does not mint a hidden row as a side effect.
func (d *Deps) HelixboxConsoleWorkflowExists(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(CtxUserID).(string)
	wf, found, err := d.Store.FindSystemWorkflow(r.Context(), userID, helixboxConsoleWorkflowName)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "Could not open the HelixBox console. Try again in a moment.")
		return
	}
	if !found {
		respond.JSON(w, http.StatusOK, map[string]any{"exists": false})
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"exists": true, "workflowId": wf.ID})
}

// HelixboxEndpoints serves the console's plan list.
//
// BodyTemplate is deliberately excluded (`json:"-"` on the type). The frontend
// posts a plan id and has no business seeing — still less influencing — the
// body that gets paid for. The platform fee is included so the console can show
// the real total a plan costs, which for the two hourly plans is dominated by
// the fee rather than the vendor price.
func (d *Deps) HelixboxEndpoints(w http.ResponseWriter, r *http.Request) {
	respond.JSON(w, http.StatusOK, map[string]any{
		"provider":             helixbox.Provider,
		"host":                 helixbox.Host,
		"asset":                helixbox.AssetID,
		"platformFeeUsdMicros": models.X402PlatformFeeUSDMicros,
		"endpoints":            helixbox.Endpoints(),
	})
}

// helixboxRunRequest is the console's buy payload. `endpoint` is an id from
// helixbox.Endpoints(), never a URL — see helixbox.Lookup's doc comment for why
// that distinction is load-bearing.
//
// Fields carries the pairing code. An earlier version of this type had no
// fields at all, on the strength of the 402 challenge declaring an empty
// request body; that cost two paid calls before the vendor's own handler
// showed the body it really wants. See the internal/helixbox package comment.
type helixboxRunRequest struct {
	Endpoint string            `json:"endpoint"`
	Fields   map[string]string `json:"fields"`
}

// buildHelixboxNode turns a validated request into the tool402 node the engine
// executes. The URL, method and body SHAPE all come from the endpoint spec;
// only the field values come from the caller.
//
// Every check here runs BEFORE any payment is attempted, and that is the whole
// point of the function. HelixBox settles first and validates second: a body it
// rejects still costs $1.75, with no refund. Anything we can catch on this side
// of the payment is money kept.
func buildHelixboxNode(req helixboxRunRequest) (models.WorkflowNode, error) {
	e, ok := helixbox.Lookup(req.Endpoint)
	if !ok {
		return models.WorkflowNode{}, fmt.Errorf("that plan is no longer available. Refresh the page to see the current list.")
	}

	params := make([]models.CustomParam, 0, len(e.Fields))
	for _, f := range e.Fields {
		value := strings.TrimSpace(req.Fields[f.Name])
		if value == "" {
			if f.Required {
				// The exact condition that produced HelixBox's own
				// "CLI pairing code is required" 400 — after payment. Refusing
				// it here costs nothing.
				return models.WorkflowNode{}, fmt.Errorf("Enter your %s before buying.", strings.ToLower(f.Label))
			}
			continue
		}
		// A pairing code is one opaque token. Whitespace or a newline in the
		// middle means a bad paste, and HelixBox would take the money for it:
		// getOrCreateAssembleSession creates a session for any string it has
		// not seen, so a mangled code buys time on a session nobody can reach.
		if strings.ContainsAny(value, " \t\r\n") {
			return models.WorkflowNode{}, fmt.Errorf("That %s has a space in it. Paste just the code, with nothing around it.", strings.ToLower(f.Label))
		}
		params = append(params, models.CustomParam{Name: f.Name, Kind: "text", Value: value})
	}

	node := models.WorkflowNode{
		ID:       "helixbox-console-" + e.ID,
		Type:     models.NodeTypeTool402,
		Name:     e.Title,
		Endpoint: e.URL(),
		Method:   e.Method,
		// BodyModeJSON with the spec's own template. Setting the mode without
		// a template would send no body and no Content-Type at all — see
		// buildTargetRequest's first branch.
		BodyMode:     models.BodyModeJSON,
		BodyTemplate: e.BodyTemplate,
		CustomParams: params,
	}
	return node, nil
}

// HelixboxConsoleRun pays for exactly one HelixBox plan and returns the session
// credential it bought, bypassing the graph engine the way the Prism and
// Tendril consoles do: one button press is one direct call.
func (d *Deps) HelixboxConsoleRun(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(CtxUserID).(string)

	var req helixboxRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "That request could not be read. Refresh the page and try again.")
		return
	}
	node, err := buildHelixboxNode(req)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	// Looked up twice (buildHelixboxNode did it too) so the response can echo
	// back what was bought without the frontend having to trust its own idea of
	// which plan it asked for.
	plan, _ := helixbox.Lookup(req.Endpoint)

	// Preflight BEFORE anything is spent.
	//
	// HelixBox now rejects a missing or unknown code before settling, so this
	// is no longer the only thing standing between a user and a wasted $1.75.
	// It still catches the one trap they have no reason to treat as an error:
	// paidUntil moves by Math.max, so buying a plan shorter than the time
	// already on a session takes the money and changes nothing.
	//
	// Fails open: an unreachable or changed probe yields StateUnknown, which is
	// not blocked. See helixbox.Ready.
	//
	// The pairing code's field name is read from the endpoint spec rather
	// than hardcoded, so a future plan with a differently-named or optional
	// session field does not silently lose this check — it just has no single
	// field to preflight on, same as today's zero-field case.
	if len(plan.Fields) == 1 {
		if code := strings.TrimSpace(req.Fields[plan.Fields[0].Name]); code != "" {
			if state, paidUntil := helixbox.Ready(r.Context(), nil, code, plan.DurationSeconds); state.Blocked() {
				// 409, not 400: the request is well-formed and would be accepted
				// at another time. Nothing was charged, and the message says why.
				respond.Error(w, http.StatusConflict, state.Message(paidUntil))
				return
			}
		}
	}

	wf, err := d.Store.GetOrCreateSystemWorkflow(r.Context(), userID, helixboxConsoleWorkflowName)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "Could not open the HelixBox console. Try again in a moment.")
		return
	}
	run, err := d.Store.CreateRun(r.Context(), wf.ID, "helixbox-console", []byte("{}"))
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "Could not start the purchase. Nothing was charged; try again.")
		return
	}

	// PerCallLedger is what makes this call BILL — see prismRelayConfig, which
	// this reuses precisely so the wiring cannot drift between two consoles
	// that bill identically. Leaving it nil would not fail loudly: a nil hook
	// means "unconditionally allowed", so every reserve/commit would silently
	// no-op while the platform still paid HelixBox for real.
	ledger := newConsolePaymentLedger(d.Store, userID, wf.ID, run.ID)
	relay := d.prismRelayConfig(ledger)

	// An empty AgentWallet and nil signer are correct here: those are the
	// legacy-dialect direct-pay path's inputs, and a v2 target never reaches
	// it. All three HelixBox endpoints answered a v2 challenge when probed.
	result, execErr := nodes.ExecuteTool402V2(
		r.Context(), node, consoleRunContext{}, models.AgentWallet{}, nil, relay,
	)

	settled := result.SettledUSDMicros > 0
	unpayable := !settled && relayUnpayable(result.Response)

	status := models.RunStatusSuccess
	if execErr != nil || unpayable {
		status = models.RunStatusFailed
	}
	// WithoutCancel: by this point the payment has settled and the ledger row
	// is written, so the run must reach a terminal status even if the client
	// has already disconnected. A row stuck at "running" after the user was
	// charged is the worst of the outcomes here.
	d.Store.FinishRun(context.WithoutCancel(r.Context()), run.ID, status)

	if execErr != nil {
		if respondConsolePaymentRefusal(w, execErr) {
			return
		}
		respond.Error(w, http.StatusBadGateway, execErr.Error())
		return
	}

	// A call that never settled is not a success story, even with a response
	// body attached. Two very different things land here:
	//
	//  1. The target answered the probe with something other than a 402, so
	//     there was nothing to pay. The response is really theirs.
	//  2. WE could not pay: no platform spend wallet or USDC signer configured
	//     on this server. executeTool402V2Relay's first guard returns that as a
	//     response body with a NIL error, so it arrives here looking exactly
	//     like a success.
	//
	// relayUnpayable separates the two, so each gets its own status and message.
	if unpayable {
		log.Printf("CRITICAL: helixbox console could not pay (user=%s run=%s target=%s): the relay has no platform spend wallet or USDC signer configured",
			userID, run.ID, node.Endpoint)
		respond.Error(w, http.StatusServiceUnavailable,
			"Payments are not set up on this server, so nothing was bought. You were not charged.")
		return
	}
	if !settled {
		log.Printf("helixbox console: %s answered the probe without a payment challenge (user=%s run=%s) — nothing was billed",
			node.Endpoint, userID, run.ID)
	}

	respond.JSON(w, http.StatusOK, map[string]any{
		"endpoint":               req.Endpoint,
		"title":                  plan.Title,
		"accessLevel":            plan.AccessLevel,
		"durationSeconds":        plan.DurationSeconds,
		"response":               result.Response,
		"settled":                settled,
		"settledUsdMicros":       result.SettledUSDMicros,
		"platformFeeUsdMicros":   result.PlatformFeeUSDMicros,
		"totalUsdMicros":         result.SettledUSDMicros + result.PlatformFeeUSDMicros,
		"txId":                   result.TxID,
		"explorerURL":            result.ExplorerURL,
		"platformFeeTxId":        result.PlatformFeeTxID,
		"platformFeeExplorerURL": result.PlatformFeeExplorerURL,
	})
}
