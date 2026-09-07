package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/prism"
	"github.com/agentmesh/backend/internal/respond"
)

// prismConsoleWorkflowName backs the Prism console's direct-action endpoint
// with one real, hidden workflow per user. There is no canvas UI for it, but
// the engine's node executors and the run/debit-ledger rows both need a
// workflow to hang off. GetOrCreateSystemWorkflow finds-or-creates it lazily
// on first use.
//
// A sibling of tendrilConsoleWorkflowName, not a generalisation of it: the two
// consoles share this scaffolding shape and almost nothing else (leases and a
// metered credit balance on one side, stateless paid calls on the other), so
// they are kept as parallel files that are easy to diff rather than folded
// into an abstraction over two members.
const prismConsoleWorkflowName = "Prism Console (managed, do not edit)"

// maxPrismFileBytes bounds a single uploaded file's decoded size. Mirrors the
// frontend's MAX_PARAM_FILE_BYTES. Enforced here as well because the client's
// check is a courtesy to the user, not a control: this handler base64-decodes
// whatever it is handed and hands it to the relay, which has its own body
// limits, and a request rejected for size AFTER payment would be the exact
// charge-then-fail outcome this console is built to avoid.
const maxPrismFileBytes = 2 * 1024 * 1024

// PrismConsoleWorkflow returns (creating on first call) the one hidden
// workflow that backs this user's Prism console, so every entry point into the
// console opens the SAME row rather than minting a duplicate.
func (d *Deps) PrismConsoleWorkflow(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(CtxUserID).(string)
	wf, err := d.Store.GetOrCreateSystemWorkflow(r.Context(), userID, prismConsoleWorkflowName)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "Could not open the Prism console. Try again in a moment.")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"workflowId": wf.ID})
}

// PrismConsoleWorkflowExists is PrismConsoleWorkflow's read-only counterpart:
// it reports whether this user's console workflow exists WITHOUT creating one.
//
// WorkflowRoute calls this on every workflow-page visit to decide whether the
// id it is rendering is a console. Calling the creating variant there would
// mint a hidden "Prism Console" row for every user the instant they opened any
// of their own, entirely unrelated workflows — the same hazard
// TendrilConsoleWorkflowExists exists to avoid, for the same reason.
func (d *Deps) PrismConsoleWorkflowExists(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(CtxUserID).(string)
	wf, found, err := d.Store.FindSystemWorkflow(r.Context(), userID, prismConsoleWorkflowName)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "Could not open the Prism console. Try again in a moment.")
		return
	}
	if !found {
		respond.JSON(w, http.StatusOK, map[string]any{"exists": false})
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"exists": true, "workflowId": wf.ID})
}

// PrismEndpoints serves the console's form spec: the tasks, the endpoints, and
// the fields each one takes.
//
// BodyTemplate is deliberately excluded (it is `json:"-"` on the type). The
// frontend renders a form and posts field values; it has no business seeing —
// still less influencing — the shape of the body that gets paid for. The
// platform fee is included so the console can show the real total a call
// costs, which for Prism is dominated by the fee rather than the vendor price.
func (d *Deps) PrismEndpoints(w http.ResponseWriter, r *http.Request) {
	respond.JSON(w, http.StatusOK, map[string]any{
		"provider":             prism.Provider,
		"host":                 prism.Host,
		"asset":                prism.AssetID,
		"platformFeeUsdMicros": models.X402PlatformFeeUSDMicros,
		"tasks":                prism.Tasks(),
		"endpoints":            prism.Endpoints(),
		"maxFileBytes":         maxPrismFileBytes,
	})
}

// prismRunRequest is the console's run payload. `endpoint` is an id from
// prism.Endpoints(), never a URL — see prism.Lookup's doc comment for why that
// distinction is load-bearing.
type prismRunRequest struct {
	Endpoint string                   `json:"endpoint"`
	Fields   map[string]prismRunField `json:"fields"`
}

type prismRunField struct {
	Kind     string `json:"kind"`
	Value    string `json:"value"`
	FileName string `json:"fileName,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`
}

// buildPrismNode turns a validated request into the tool402 node the engine
// executes. Everything that determines what is called and how much it costs —
// URL, method, body shape — comes from the endpoint spec; only the field
// VALUES come from the caller.
//
// Returns a client-facing error for anything wrong with the request. Every one
// of these checks runs BEFORE any payment is attempted, which is the whole
// point: being charged $1.75 for a request that was already known to be
// malformed is the failure mode this console was built to prevent.
func buildPrismNode(req prismRunRequest) (models.WorkflowNode, error) {
	e, ok := prism.Lookup(req.Endpoint)
	if !ok {
		return models.WorkflowNode{}, fmt.Errorf("that task is no longer available. Refresh the page to see the current list.")
	}

	params := make([]models.CustomParam, 0, len(e.Fields))
	for _, f := range e.Fields {
		got, present := req.Fields[f.Name]
		value := strings.TrimSpace(got.Value)
		if !present || value == "" {
			if f.Required {
				return models.WorkflowNode{}, fmt.Errorf("Fill in %s before running.", strings.ToLower(f.Label))
			}
			continue
		}

		if f.Kind == prism.FieldFile {
			// The value is base64 file content. Decode it here purely to
			// validate: bad base64 or an oversized file must fail now, not
			// after the endpoint has been paid and rejects the body.
			raw, err := base64.StdEncoding.DecodeString(got.Value)
			if err != nil {
				return models.WorkflowNode{}, fmt.Errorf("That %s file could not be read. Choose it again.", strings.ToLower(f.Label))
			}
			if len(raw) > maxPrismFileBytes {
				return models.WorkflowNode{}, fmt.Errorf("That %s is %.1f MB. The limit is %d MB; try a smaller file.",
					strings.ToLower(f.Label), float64(len(raw))/1024/1024, maxPrismFileBytes/1024/1024)
			}
			if got.FileName == "" {
				return models.WorkflowNode{}, fmt.Errorf("That %s file has no name. Choose it again.", strings.ToLower(f.Label))
			}
			params = append(params, models.CustomParam{
				Name:     f.Name,
				Kind:     "file",
				Value:    got.Value,
				FileName: got.FileName,
				MIMEType: got.MIMEType,
			})
			continue
		}

		params = append(params, models.CustomParam{Name: f.Name, Kind: "text", Value: value})
	}

	node := models.WorkflowNode{
		ID:           "prism-console-" + e.ID,
		Type:         models.NodeTypeTool402,
		Name:         e.Title,
		Endpoint:     e.URL(),
		Method:       e.Method,
		CustomParams: params,
	}
	// An empty template means the fields go on the query string instead —
	// buildTargetRequest's default. Setting BodyMode only when there is a
	// template to expand keeps the two modes from being half-applied.
	if e.BodyTemplate != "" {
		node.BodyMode = models.BodyModeJSON
		node.BodyTemplate = e.BodyTemplate
	}
	return node, nil
}

// relayUnpayable reports whether a result is executeTool402V2Relay's
// "cannot pay" sentinel rather than a real answer from the target.
//
// That branch signals the condition only through the response body: it returns
// a nil error, and the graph-engine callers rely on that contract, so there is
// nothing else to key on without changing the payment path this console
// deliberately does not touch. Matching is done against the exported
// nodes.ErrMsgNoPlatformSpendWallet constant, not a local literal; tool402.go
// is byte-frozen so its own copy stays a literal, but
// nodes.TestNoPlatformSpendWalletMessageMatchesTheFrozenReturn fails if the two
// ever diverge. If that sentinel becomes a typed error, replace this with
// errors.As and delete the helper.
func relayUnpayable(response any) bool {
	m, ok := response.(map[string]any)
	if !ok {
		return false
	}
	msg, _ := m["error"].(string)
	return strings.Contains(msg, nodes.ErrMsgNoPlatformSpendWallet)
}

// PrismConsoleRun pays for and executes exactly one Prism endpoint, bypassing
// the graph engine the way the Tendril console does: one button press is one
// direct call, with no trigger, no chain, and no "run the whole workflow".
func (d *Deps) PrismConsoleRun(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(CtxUserID).(string)

	var req prismRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "That request could not be read. Refresh the page and try again.")
		return
	}
	node, err := buildPrismNode(req)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	wf, err := d.Store.GetOrCreateSystemWorkflow(r.Context(), userID, prismConsoleWorkflowName)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "Could not open the Prism console. Try again in a moment.")
		return
	}
	run, err := d.Store.CreateRun(r.Context(), wf.ID, "prism-console", []byte("{}"))
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "Could not start the run. Nothing was charged; try again.")
		return
	}

	// PerCallLedger is what makes this call BILL. Prism's endpoints are x402
	// v2, so ExecuteTool402V2 takes the per-call relay path, which reserves
	// the vendor amount plus models.X402PlatformFeeUSDMicros, commits both as
	// separate debit_ledger rows, and settles the fee on-chain — all inside
	// executeTool402V2Relay, with no fee arithmetic here.
	//
	// Leaving this nil would not fail loudly: PaymentLedger's doc comment says
	// a nil hook means "unconditionally allowed", so every reserve/commit
	// would silently no-op while the platform still paid the vendor for real.
	// That precise regression shipped once on the Tendril console (see
	// newConsolePaymentLedger) — TestPrismConsoleWiresThePerCallLedger exists
	// so it cannot happen again here.
	ledger := newConsolePaymentLedger(d.Store, userID, wf.ID, run.ID)
	relay := nodes.X402RelayConfig{
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
	}

	// An empty AgentWallet and nil signer are correct here: those are the
	// legacy-dialect direct-pay path's inputs, and a v2 target never reaches
	// it. If a Prism endpoint ever stops answering with a v2 challenge, that
	// path degrades to an unpaid call rather than erroring — which is exactly
	// why the result is checked for settlement below instead of being
	// reported as a success on the strength of a 200.
	result, execErr := nodes.ExecuteTool402V2(
		r.Context(), node, consoleRunContext{}, models.AgentWallet{}, nil, relay,
	)

	// settled / unpayable are computed here, before FinishRun, because they
	// decide the run's status as much as execErr does. A call we could not pay
	// for (relayUnpayable: no platform spend wallet on this server) did not do
	// what the user asked, so its row must not read RunStatusSuccess just
	// because executeTool402V2Relay reported the misconfiguration with a nil
	// error. A quiet probe (settled == false, but not unpayable) still counts
	// as success: the target answered, there was simply nothing to pay.
	settled := result.SettledUSDMicros > 0
	unpayable := !settled && relayUnpayable(result.Response)

	status := models.RunStatusSuccess
	if execErr != nil || unpayable {
		status = models.RunStatusFailed
	}
	// WithoutCancel: by this point the payment has settled and the ledger row
	// is written, so the run must reach a terminal status even if the client
	// has already disconnected and r.Context() is cancelled. A row stuck at
	// "running" after the user was charged is the worst of the outcomes here.
	d.Store.FinishRun(context.WithoutCancel(r.Context()), run.ID, status)

	if execErr != nil {
		// A blocked balance is the user's problem to fix (top up), not a
		// gateway failure, so it gets 402 rather than 502. lib/prism.ts turns
		// that status into a PrismRunError the console reads to show an "Add
		// credits" button instead of a bare error line.
		var blocked *nodes.ErrBalanceBlocked
		if errors.As(execErr, &blocked) {
			respond.Error(w, http.StatusPaymentRequired, execErr.Error())
			return
		}
		respond.Error(w, http.StatusBadGateway, execErr.Error())
		return
	}

	// A call that never settled is not a success story, even with a response
	// body attached: nothing was paid and nothing was billed, so reporting it
	// as a paid result would tell the user their $1.75 bought this answer.
	//
	// Two very different things land here, and they must not be conflated:
	//
	//  1. The target answered the probe with something other than a 402, so
	//     there was nothing to pay. Unusual, but the response is really theirs.
	//  2. WE could not pay: no platform spend wallet or USDC signer configured
	//     on this server. executeTool402V2Relay's first guard returns that as a
	//     response body with a NIL error, so it arrives here looking exactly
	//     like a success. It is a server misconfiguration, and telling the user
	//     "Prism answered without asking for payment" would blame the vendor for
	//     our own broken deployment.
	//
	// relayUnpayable separates the two, so each gets its own status, log line
	// and message.
	if unpayable {
		log.Printf("CRITICAL: prism console could not pay (user=%s run=%s target=%s): the relay has no platform spend wallet or USDC signer configured",
			userID, run.ID, node.Endpoint)
		respond.Error(w, http.StatusServiceUnavailable,
			"Payments are not set up on this server, so the run did not happen. You were not charged.")
		return
	}
	if !settled {
		log.Printf("prism console: %s answered the probe without a payment challenge (user=%s run=%s) — nothing was billed",
			node.Endpoint, userID, run.ID)
	}

	respond.JSON(w, http.StatusOK, map[string]any{
		"endpoint":               req.Endpoint,
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
