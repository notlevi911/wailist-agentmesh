package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode"

	"github.com/agentmesh/backend/internal/models"
)

var openAIBaseURL = "https://api.openai.com"
var groqBaseURL = "https://api.groq.com/openai"
var mistralBaseURL = "https://api.mistral.ai"
var geminiBaseURL = "https://generativelanguage.googleapis.com"

func SetOpenAIBaseURL(u string) { openAIBaseURL = u }

// SetGeminiBaseURL overrides the Gemini API host. Test-only seam, mirroring
// SetOpenAIBaseURL, so callGemini can be pointed at an httptest server.
func SetGeminiBaseURL(u string) { geminiBaseURL = u }

// ExecuteAgent runs the agent node: calls the attached LLM with function calling
// support so the agent decides whether and how to invoke its attached tools.
// platformKeys maps provider template ("openai", "gemini", ...) to AgentMesh's
// own API key for that provider, used only when the Provider node's KeyMode is
// "platform". Never empty-checked against BYOK nodes — resolveAPIKey ignores it
// entirely unless KeyMode == "platform".
func ExecuteAgent(ctx context.Context, node models.WorkflowNode, attach models.AttachConfig, aw models.AgentWallet, signer WalletSigner, rc RunContexter, checkBalance BalanceChecker, platformKeys map[string]string, relayCfg X402RelayConfig) (any, error) {
	if attach.Provider == nil {
		return rc.UserInput(), nil
	}
	p := attach.Provider
	switch p.Template {
	case "openai", "groq", "mistral":
		return callOpenAICompat(ctx, node, *p, attach.Tools, aw, signer, rc, checkBalance, platformKeys, relayCfg)
	case "anthropic":
		return callAnthropic(ctx, node, *p, rc, platformKeys)
	case "gemini":
		return callGemini(ctx, node, *p, attach.Tools, aw, signer, rc, checkBalance, platformKeys, relayCfg)
	default:
		return callOpenAICompat(ctx, node, *p, attach.Tools, aw, signer, rc, checkBalance, platformKeys, relayCfg)
	}
}

// resolveAPIKey returns the API key a Provider node's call should use: its
// own APIKey for BYOK (KeyMode != "platform"), or AgentMesh's platform key
// for its Template when KeyMode == "platform". Errors rather than silently
// falling back to an empty key when platform mode is selected but no key is
// configured for that template — an empty Authorization header would just
// surface as a confusing 401 from the upstream provider instead.
func resolveAPIKey(provider models.WorkflowNode, platformKeys map[string]string) (string, error) {
	if provider.KeyMode != "platform" {
		return provider.APIKey, nil
	}
	key, ok := platformKeys[provider.Template]
	if !ok || key == "" {
		return "", fmt.Errorf("platform key not configured for provider %q", provider.Template)
	}
	return key, nil
}

// defaultModel returns the model each provider call falls back to when a
// Provider node's Model field is empty. Centralized here (not duplicated in
// each call* function) so the billing tier computed before the call
// (runner.go's preflight) and the tier reported after the call
// (platformKeyUsageResult) can never disagree about which model actually ran.
func defaultModel(template string) string {
	switch template {
	case "gemini":
		return "gemini-2.5-flash"
	case "anthropic":
		return "claude-sonnet-4-6"
	default:
		return "gpt-4o"
	}
}

// ResolveModel returns the model a call actually runs on: the Provider
// node's own Model if set, else its template's default. Exported so
// runner.go can compute the same billing tier before the call that
// provider.go computes for the call itself.
func ResolveModel(template, model string) string {
	if model != "" {
		return model
	}
	return defaultModel(template)
}

// platformKeyUsageResult wraps a final agent answer with tier/usage metadata
// when the call ran on a platform key, so the Runner can bill the right
// tier fee and record usage on the debit_ledger row. BYOK calls never go
// through this — their return shape is unchanged from before this feature.
func platformKeyUsageResult(provider models.WorkflowNode, resolvedModel string, tokensIn, tokensOut int, extra map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range extra {
		out[k] = v
	}
	out["platformKeyUsage"] = map[string]any{
		"tier":      ModelTier(provider.Template, resolvedModel),
		"model":     resolvedModel,
		"tokensIn":  tokensIn,
		"tokensOut": tokensOut,
	}
	return out
}

// ─── function declaration helpers ────────────────────────────────────────────

type funcDecl struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// sanitizeFuncName converts any string into a valid LLM function name.
// Rules: starts with [a-zA-Z_], contains only [a-zA-Z0-9_-], max 64 chars.
func sanitizeFuncName(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsLetter(r) || r == '_' {
			b.WriteRune(r)
		} else if i > 0 && (unicode.IsDigit(r) || r == '-') {
			b.WriteRune(r)
		} else if b.Len() > 0 {
			b.WriteRune('_')
		}
	}
	result := b.String()
	// Trim trailing underscores
	result = strings.TrimRight(result, "_")
	if len(result) > 64 {
		result = result[:64]
	}
	if result == "" {
		result = "tool"
	}
	return result
}

func toolFuncName(t models.WorkflowNode) string {
	name := sanitizeFuncName(t.Name)
	if name == "" || name == "tool" {
		name = sanitizeFuncName(t.ID)
	}
	return name
}

func buildFuncDecls(tools []models.WorkflowNode) []funcDecl {
	decls := make([]funcDecl, 0, len(tools))
	for _, t := range tools {
		name := toolFuncName(t)
		desc := t.Description
		if desc == "" {
			desc = "External tool: " + t.Name
			if t.Endpoint != "" {
				desc += ". Endpoint: " + t.Endpoint
			}
		}

		properties := map[string]any{}
		required := []string{}

		for _, p := range t.DiscoveredParams {
			pType := "string"
			switch strings.ToLower(p.Type) {
			case "number", "integer", "float", "int":
				pType = "number"
			case "boolean", "bool":
				pType = "boolean"
			}
			prop := map[string]any{"type": pType, "description": p.Description}
			if p.Default != "" {
				prop["default"] = p.Default
			}
			properties[p.Name] = prop
			if p.Required {
				required = append(required, p.Name)
			}
		}

		// Hand-added fields are part of this tool's real signature too — an
		// endpoint that publishes no Bazaar schema has nothing in
		// DiscoveredParams, so without this the model would be handed a
		// no-argument tool and could never vary the one input that matters.
		// File params are deliberately excluded: their value is base64 bytes
		// the user attached, not something a model can author.
		for _, p := range t.CustomParams {
			if p.Name == "" || p.Kind == "file" {
				continue
			}
			prop := map[string]any{
				"type":        "string",
				"description": "Caller-defined parameter for " + t.Name,
			}
			if p.Value != "" {
				prop["default"] = p.Value
			}
			properties[p.Name] = prop
		}

		params := map[string]any{"type": "OBJECT", "properties": properties}
		if len(required) > 0 {
			params["required"] = required
		}

		decls = append(decls, funcDecl{Name: name, Description: desc, Parameters: params})
	}
	return decls
}

// ─── tool execution helper ────────────────────────────────────────────────────

func executeFunctionCall(ctx context.Context, funcName string, args map[string]any, tools []models.WorkflowNode, aw models.AgentWallet, signer WalletSigner, rc RunContexter, checkBalance BalanceChecker, relayCfg X402RelayConfig) (any, models.WorkflowNode, *ToolPaymentInfo, error) {
	for _, t := range tools {
		if toolFuncName(t) != funcName {
			continue
		}
		toolNode := t
		// billableFlatFee nodes (an attached "http" Tool, or any Action/
		// connector node) are billed via an atomic per-call Reserve/Commit/
		// Release below, NOT the read-only checkBalance floor guard tool402
		// nodes use -- see X402RelayConfig.FlatFeeLedger's doc comment for
		// why: checkBalance alone would let every iteration of the agent's
		// tool-calling loop pass against the same stale, undecremented
		// balance and collectively overspend.
		billableFlatFee := toolNode.Type != models.NodeTypeTool402 && BillableFlatFee(toolNode.Type, toolNode.Template)
		if checkBalance != nil && toolNode.Type == models.NodeTypeTool402 {
			// Cheap, conservative guard before any network call to the tool's
			// endpoint: reject outright if the balance can't even cover the
			// worst-case floor, so an unfunded caller can't drive unbounded
			// outbound HTTP requests (SSRF/DoS-amplification risk) merely by
			// attaching tool nodes it can never pay for. The real, exact-
			// amount gate for tool402 relay-dialect payments still runs
			// separately inside ExecuteTool402V2 once the true cost is known
			// (which can be less than this floor) — this is a floor, not the
			// final word.
			//
			// The run-level path (toolIsRunFunded true for THIS tool)
			// already reserved this run's full estimated tool402 cost from
			// the live DB balance up front, in reserveAndFundRun's
			// ReserveCredits call, before the agent's loop ever started --
			// and gates each attached call's real amount against the
			// run-level in-memory pool inside executeTool402RunLevel via
			// cfg.Ledger.Reserve. Re-checking the live DB balance here would
			// double-count that up-front reservation. A tool NOT covered by
			// the up-front estimate (its probe failed during
			// reserveAndFundRun, or it's a legacy-dialect tool attached to
			// the same run-funded agent) still needs this floor check
			// exactly as before -- it bills against the live DB balance via
			// the per-call path (relayCfg.PerCallLedger or
			// relayCfg.LegacyLedger, depending on dialect) inside
			// ExecuteTool402V2, so skipping this check for it would let an
			// unfunded caller drive unbounded outbound requests through it.
			if !relayCfg.toolIsRunFunded(toolNode.ID) {
				if err := checkBalance(ctx, models.X402ProbeFloorUSDMicros); err != nil {
					return nil, toolNode, nil, &ErrBalanceBlocked{Err: err}
				}
			}
		}
		if billableFlatFee {
			if reserve := relayCfg.FlatFeeLedger.Reserve; reserve != nil {
				if err := reserve(ctx, models.ByokFlatFeeUSDMicros); err != nil {
					return nil, toolNode, nil, &ErrBalanceBlocked{Err: err}
				}
			}
		}
		// Append LLM-chosen args as query params onto the endpoint URL
		if len(args) > 0 && toolNode.Endpoint != "" {
			u, err := url.Parse(toolNode.Endpoint)
			if err == nil {
				q := u.Query()
				for k, v := range args {
					q.Set(k, fmt.Sprintf("%v", v))
				}
				u.RawQuery = q.Encode()
				toolNode.Endpoint = u.String()
			}
		}
		if t.Type == models.NodeTypeTool402 {
			paymentResult, err := ExecuteTool402V2(ctx, toolNode, rc, aw, signer, relayCfg)
			if err != nil {
				return nil, toolNode, nil, err
			}
			var payment *ToolPaymentInfo
			if paymentResult.SettledUSDMicros > 0 {
				payment = &ToolPaymentInfo{
					NodeID: toolNode.ID, NodeName: toolNode.Name,
					SettledUSDMicros:       paymentResult.SettledUSDMicros,
					DebitKind:              paymentResult.DebitKind,
					PlatformFeeUSDMicros:   paymentResult.PlatformFeeUSDMicros,
					TxID:                   paymentResult.TxID,
					ExplorerURL:            paymentResult.ExplorerURL,
					OutboundTxID:           paymentResult.OutboundTxID,
					OutboundExplorerURL:    paymentResult.OutboundExplorerURL,
					PlatformFeeTxID:        paymentResult.PlatformFeeTxID,
					PlatformFeeExplorerURL: paymentResult.PlatformFeeExplorerURL,
				}
				// The legacy direct-pay dialect never populates the result's
				// own settlement fields (ExecuteTool402 predates them and
				// only ever returns its map), so fall back to the response
				// map for that path alone.
				if payment.TxID == "" {
					if m, ok := paymentResult.Response.(map[string]any); ok {
						payment.TxID, _ = m["txId"].(string)
						payment.ExplorerURL, _ = m["explorerURL"].(string)
					}
				}
			}
			return paymentResult.Response, toolNode, payment, nil
		}
		result, err := ExecuteToolWithArgs(ctx, toolNode, rc, args)
		if billableFlatFee {
			if err != nil {
				if release := relayCfg.FlatFeeLedger.Release; release != nil {
					release(ctx, models.ByokFlatFeeUSDMicros)
				}
			} else if commit := relayCfg.FlatFeeLedger.Commit; commit != nil {
				commit(ctx, toolNode.ID, models.ByokFlatFeeUSDMicros, models.DebitKindByokFlatFee)
			}
		}
		return result, toolNode, nil, err
	}
	return nil, models.WorkflowNode{}, nil, fmt.Errorf("tool %q not found in attached tools", funcName)
}

// ─── low-level HTTP helper ────────────────────────────────────────────────────

// postLLMJSON posts a JSON payload to an LLM provider API and decodes the response body.
// Distinct from the connector-oriented postJSON in connector_helpers.go, which returns a
// caller-supplied sentinel instead of the decoded body.
func postLLMJSON(ctx context.Context, apiURL string, headers map[string]string, payload any) (map[string]any, error) {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("LLM API %d: %s", resp.StatusCode, string(b))
	}
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	return result, nil
}

// ─── Gemini ───────────────────────────────────────────────────────────────────

const maxToolIterations = 15

// ToolPaymentInfo carries what an agent-attached tool402 call actually
// charged, so the agent loop can bill the real settled amount (relay
// dialect) or the flat fee (legacy dialect) instead of assuming a fixed
// amount from the tool result's shape. SettledUSDMicros + PlatformFeeUSDMicros
// is the TOTAL debited from the user's credits for a v2 call -- neither
// field alone is the total charge (see Tool402PaymentResult's doc comment).
type ToolPaymentInfo struct {
	NodeID               string
	NodeName             string
	SettledUSDMicros     int64
	DebitKind            string
	PlatformFeeUSDMicros int64
	// TxID/ExplorerURL are the payment's INBOUND settlement leg — the
	// legacy direct-pay dialect's own transaction, the relay dialect's
	// per-call facilitator settlement, or (for a run-funded tool) the run's
	// single up-front funding settlement, which is the only inbound leg
	// that path has. OutboundTxID/OutboundExplorerURL are the Wallet 2 ->
	// target leg, when the target returned one. All four can be empty; a
	// payment is real and billed regardless of whether its ids reached us.
	TxID                string
	ExplorerURL         string
	OutboundTxID        string
	OutboundExplorerURL string
	// PlatformFeeTxID/PlatformFeeExplorerURL identify the dedicated Wallet 1
	// -> Wallet 2 settlement for PlatformFeeUSDMicros -- see
	// Tool402PaymentResult.PlatformFeeTxID's doc comment. Both empty for the
	// legacy dialect, a run-funded call (its markup already settled as part
	// of the run's single up-front funding), or a fee settlement that failed
	// (logged/alerted elsewhere, never blocks the call itself).
	PlatformFeeTxID        string
	PlatformFeeExplorerURL string
}

// paymentReceipt turns a ToolPaymentInfo into the map shape surfaced in an
// agent's "x402Payments" output field, consumed only for the run console's
// SSE display (runner.go) -- never re-billed from here, billing already
// happened via the ledger Commit calls inside ExecuteTool402V2. txId/
// explorerURL keys are included only when populated (legacy dialect).
// platformFeeUsdMicros is a NEW additive key (zero/omitted for legacy
// calls, which have no separate markup component): settledUsdMicros alone
// is NOT the total charged for a v2 call, see ToolPaymentInfo's doc
// comment -- a consumer that wants the real total must add both fields.
func paymentReceipt(p *ToolPaymentInfo) map[string]any {
	receipt := map[string]any{
		"nodeId":           p.NodeID,
		"nodeName":         p.NodeName,
		"settledUsdMicros": p.SettledUSDMicros,
		"debitKind":        p.DebitKind,
	}
	if p.PlatformFeeUSDMicros > 0 {
		receipt["platformFeeUsdMicros"] = p.PlatformFeeUSDMicros
	}
	if p.TxID != "" {
		receipt["txId"] = p.TxID
	}
	if p.ExplorerURL != "" {
		receipt["explorerURL"] = p.ExplorerURL
	}
	if p.OutboundTxID != "" {
		receipt["outboundTxId"] = p.OutboundTxID
	}
	if p.OutboundExplorerURL != "" {
		receipt["outboundExplorerURL"] = p.OutboundExplorerURL
	}
	if p.PlatformFeeTxID != "" {
		receipt["platformFeeTxId"] = p.PlatformFeeTxID
	}
	if p.PlatformFeeExplorerURL != "" {
		receipt["platformFeeExplorerURL"] = p.PlatformFeeExplorerURL
	}
	if p.SettledUSDMicros > 0 {
		// Same decimal shape the non-agent-attached paths put in their
		// response map, so LogDrawer's OutputCell shows the real amount
		// paid instead of a bare "paid" with no number.
		receipt["amount"] = formatUSDCAmount(p.SettledUSDMicros)
	}
	return receipt
}

func callGemini(ctx context.Context, agent models.WorkflowNode, provider models.WorkflowNode, tools []models.WorkflowNode, aw models.AgentWallet, signer WalletSigner, rc RunContexter, checkBalance BalanceChecker, platformKeys map[string]string, relayCfg X402RelayConfig) (any, error) {
	model := ResolveModel(provider.Template, provider.Model)

	apiKey, err := resolveAPIKey(provider, platformKeys)
	if err != nil {
		return nil, err
	}

	apiURL := fmt.Sprintf("%s/v1beta/models/%s:generateContent", geminiBaseURL, model)
	apiHeaders := map[string]string{"x-goog-api-key": apiKey}

	contents := []map[string]any{
		{"role": "user", "parts": []map[string]any{{"text": rc.UserInput()}}},
	}

	payload := map[string]any{"contents": contents}
	if agent.SystemPrompt != "" {
		payload["systemInstruction"] = map[string]any{
			"parts": []map[string]string{{"text": agent.SystemPrompt}},
		}
	}
	decls := buildFuncDecls(tools)
	if len(decls) > 0 {
		payload["tools"] = []map[string]any{{"functionDeclarations": decls}}
	}

	var x402Payments []map[string]any
	var tokensIn, tokensOut int

	// Agentic loop — keep calling until the model returns text (no function call).
	for iter := 0; iter < maxToolIterations; iter++ {
		resp, err := postLLMJSON(ctx, apiURL, apiHeaders, payload)
		if err != nil {
			return nil, err
		}
		if usage, ok := resp["usageMetadata"].(map[string]any); ok {
			if v, ok := usage["promptTokenCount"].(float64); ok {
				tokensIn += int(v)
			}
			if v, ok := usage["candidatesTokenCount"].(float64); ok {
				tokensOut += int(v)
			}
		}

		// Collect all function calls the model wants to make in this turn
		// (Gemini can return multiple functionCall parts at once for parallel use).
		calls := extractGeminiFunctionCalls(resp)
		if len(calls) == 0 {
			text, textErr := extractGeminiText(resp)
			if textErr != nil {
				return nil, textErr
			}
			if provider.KeyMode == "platform" {
				extra := map[string]any{"message": text}
				if len(x402Payments) > 0 {
					extra["x402Payments"] = x402Payments
				}
				return platformKeyUsageResult(provider, model, tokensIn, tokensOut, extra), nil
			}
			if len(x402Payments) > 0 {
				return map[string]any{"message": text, "x402Payments": x402Payments}, nil
			}
			return text, nil
		}

		// Build the model turn (all function calls in one "model" message)
		modelParts := make([]map[string]any, len(calls))
		for i, c := range calls {
			modelParts[i] = map[string]any{"functionCall": map[string]any{"name": c.name, "args": c.args}}
		}
		contents = append(contents, map[string]any{"role": "model", "parts": modelParts})

		// Execute every requested function call and collect responses.
		// Billable flat-fee nodes are reserved/committed/released atomically,
		// per call, inside executeFunctionCall itself -- not batched here.
		// See X402RelayConfig.FlatFeeLedger's doc comment.
		responseParts := make([]map[string]any, 0, len(calls))
		for _, c := range calls {
			result, _, payment, execErr := executeFunctionCall(ctx, c.name, c.args, tools, aw, signer, rc, checkBalance, relayCfg)
			if execErr != nil {
				var blocked *ErrBalanceBlocked
				if errors.As(execErr, &blocked) {
					return nil, execErr
				}
				// KNOWN GAP: any other tool error -- including
				// *ErrPaymentAlreadyCommitted, a tool402 call that signed
				// and sent a real payment before the target rejected it or
				// the request failed -- falls through below and gets fed
				// back to the LLM as a soft "error: ..." string, discarding
				// its type. If the loop later fails anyway (maxToolIterations,
				// an LLM API error), the error runner.go's dead-letter
				// classification sees carries no trace of the earlier
				// payment. Closing this needs a payment-risk flag threaded
				// through this whole loop (and its OpenAI-compatible/
				// Anthropic counterparts), not just a wrapped error type at
				// the origin -- not done here.
			}
			resultStr := ""
			if execErr != nil {
				resultStr = "error: " + execErr.Error()
			} else {
				if payment != nil {
					x402Payments = append(x402Payments, paymentReceipt(payment))
				}
				b, _ := json.Marshal(result)
				resultStr = string(b)
			}
			responseParts = append(responseParts, map[string]any{
				"functionResponse": map[string]any{
					"name":     c.name,
					"response": map[string]any{"result": resultStr},
				},
			})
		}

		// Feed all results back in one "user" turn
		contents = append(contents, map[string]any{"role": "user", "parts": responseParts})
		payload["contents"] = contents
	}

	return nil, fmt.Errorf("agent exceeded maximum tool call iterations (%d)", maxToolIterations)
}

func extractGeminiText(resp map[string]any) (string, error) {
	candidates, _ := resp["candidates"].([]any)
	if len(candidates) == 0 {
		return "", fmt.Errorf("empty response from Gemini")
	}
	content, _ := candidates[0].(map[string]any)["content"].(map[string]any)
	parts, _ := content["parts"].([]any)
	for _, p := range parts {
		part, _ := p.(map[string]any)
		if text, ok := part["text"].(string); ok {
			return text, nil
		}
	}
	return "", fmt.Errorf("no text part in Gemini response")
}

type geminiFuncCall struct {
	name string
	args map[string]any
}

// extractGeminiFunctionCalls returns ALL functionCall parts in the first candidate.
// Gemini may request multiple parallel tool calls in a single turn.
func extractGeminiFunctionCalls(resp map[string]any) []geminiFuncCall {
	candidates, _ := resp["candidates"].([]any)
	if len(candidates) == 0 {
		return nil
	}
	content, _ := candidates[0].(map[string]any)["content"].(map[string]any)
	parts, _ := content["parts"].([]any)
	var calls []geminiFuncCall
	for _, p := range parts {
		part, _ := p.(map[string]any)
		if fc, ok := part["functionCall"].(map[string]any); ok {
			name, _ := fc["name"].(string)
			args, _ := fc["args"].(map[string]any)
			calls = append(calls, geminiFuncCall{name: name, args: args})
		}
	}
	return calls
}

// ─── OpenAI / Groq / Mistral ──────────────────────────────────────────────────

func callOpenAICompat(ctx context.Context, agent models.WorkflowNode, provider models.WorkflowNode, tools []models.WorkflowNode, aw models.AgentWallet, signer WalletSigner, rc RunContexter, checkBalance BalanceChecker, platformKeys map[string]string, relayCfg X402RelayConfig) (any, error) {
	baseURL := openAIBaseURL
	switch provider.Template {
	case "groq":
		baseURL = groqBaseURL
	case "mistral":
		baseURL = mistralBaseURL
	}
	model := ResolveModel(provider.Template, provider.Model)

	apiKey, err := resolveAPIKey(provider, platformKeys)
	if err != nil {
		return nil, err
	}

	messages := []map[string]any{}
	if agent.SystemPrompt != "" {
		messages = append(messages, map[string]any{"role": "system", "content": agent.SystemPrompt})
	}
	messages = append(messages, map[string]any{"role": "user", "content": rc.UserInput()})

	payload := map[string]any{"model": model, "messages": messages}

	decls := buildFuncDecls(tools)
	if len(decls) > 0 {
		oaiTools := make([]map[string]any, len(decls))
		for i, d := range decls {
			oaiTools[i] = map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        d.Name,
					"description": d.Description,
					"parameters":  d.Parameters,
				},
			}
		}
		payload["tools"] = oaiTools
	}

	headers := map[string]string{"Authorization": "Bearer " + apiKey}

	var x402Payments []map[string]any
	var tokensIn, tokensOut int

	// Agentic loop — repeat until the model returns content with no tool calls.
	for iter := 0; iter < maxToolIterations; iter++ {
		payload["messages"] = messages
		resp, err := postLLMJSON(ctx, baseURL+"/v1/chat/completions", headers, payload)
		if err != nil {
			return nil, err
		}
		if usage, ok := resp["usage"].(map[string]any); ok {
			if v, ok := usage["prompt_tokens"].(float64); ok {
				tokensIn += int(v)
			}
			if v, ok := usage["completion_tokens"].(float64); ok {
				tokensOut += int(v)
			}
		}

		choices, _ := resp["choices"].([]any)
		if len(choices) == 0 {
			return nil, fmt.Errorf("empty choices from LLM")
		}
		choice, _ := choices[0].(map[string]any)
		msg, _ := choice["message"].(map[string]any)

		toolCalls, _ := msg["tool_calls"].([]any)
		if len(toolCalls) == 0 {
			// No tool calls — return the final answer
			content, _ := msg["content"].(string)
			if provider.KeyMode == "platform" {
				extra := map[string]any{"message": content}
				if len(x402Payments) > 0 {
					extra["x402Payments"] = x402Payments
				}
				return platformKeyUsageResult(provider, model, tokensIn, tokensOut, extra), nil
			}
			if len(x402Payments) > 0 {
				return map[string]any{"message": content, "x402Payments": x402Payments}, nil
			}
			return content, nil
		}

		// Build the assistant message with all tool calls
		assistantMsg := map[string]any{"role": "assistant", "tool_calls": toolCalls}
		if content, _ := msg["content"].(string); content != "" {
			assistantMsg["content"] = content
		}
		messages = append(messages, assistantMsg)

		// Execute every tool call and append results. Billable flat-fee nodes
		// are reserved/committed/released atomically, per call, inside
		// executeFunctionCall itself -- not batched here. See
		// X402RelayConfig.FlatFeeLedger's doc comment.
		for _, raw := range toolCalls {
			tc, _ := raw.(map[string]any)
			tcFunc, _ := tc["function"].(map[string]any)
			tcName, _ := tcFunc["name"].(string)
			tcArgsStr, _ := tcFunc["arguments"].(string)
			tcID, _ := tc["id"].(string)

			var tcArgs map[string]any
			json.Unmarshal([]byte(tcArgsStr), &tcArgs)

			toolResult, _, payment, toolErr := executeFunctionCall(ctx, tcName, tcArgs, tools, aw, signer, rc, checkBalance, relayCfg)
			if toolErr != nil {
				var blocked *ErrBalanceBlocked
				if errors.As(toolErr, &blocked) {
					return nil, toolErr
				}
				// See the identical KNOWN GAP note in the Gemini loop above
				// -- any other tool error, *ErrPaymentAlreadyCommitted
				// included, gets fed back to the LLM as a string below and
				// loses its type if the loop later fails for an unrelated
				// reason.
			}
			resultStr := ""
			if toolErr != nil {
				resultStr = "error: " + toolErr.Error()
			} else {
				if payment != nil {
					x402Payments = append(x402Payments, paymentReceipt(payment))
				}
				b, _ := json.Marshal(toolResult)
				resultStr = string(b)
			}

			messages = append(messages, map[string]any{
				"role":         "tool",
				"tool_call_id": tcID,
				"content":      resultStr,
			})
		}
	}

	return nil, fmt.Errorf("agent exceeded maximum tool call iterations (%d)", maxToolIterations)
}

// ─── Anthropic ────────────────────────────────────────────────────────────────

func callAnthropic(ctx context.Context, agent models.WorkflowNode, provider models.WorkflowNode, rc RunContexter, platformKeys map[string]string) (any, error) {
	model := ResolveModel(provider.Template, provider.Model)

	apiKey, err := resolveAPIKey(provider, platformKeys)
	if err != nil {
		return nil, err
	}

	type anthMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	payload := map[string]any{
		"model":      model,
		"max_tokens": 4096,
		"messages":   []anthMsg{{Role: "user", Content: rc.UserInput()}},
	}
	if agent.SystemPrompt != "" {
		payload["system"] = agent.SystemPrompt
	}

	headers := map[string]string{
		"x-api-key":         apiKey,
		"anthropic-version": "2023-06-01",
	}
	resp, err := postLLMJSON(ctx, "https://api.anthropic.com/v1/messages", headers, payload)
	if err != nil {
		return nil, err
	}

	var tokensIn, tokensOut int
	if usage, ok := resp["usage"].(map[string]any); ok {
		if v, ok := usage["input_tokens"].(float64); ok {
			tokensIn = int(v)
		}
		if v, ok := usage["output_tokens"].(float64); ok {
			tokensOut = int(v)
		}
	}

	contentArr, _ := resp["content"].([]any)
	for _, c := range contentArr {
		cm, _ := c.(map[string]any)
		if cm["type"] == "text" {
			text, _ := cm["text"].(string)
			if provider.KeyMode == "platform" {
				return platformKeyUsageResult(provider, model, tokensIn, tokensOut, map[string]any{"message": text}), nil
			}
			return text, nil
		}
	}
	return nil, fmt.Errorf("no text block in Anthropic response")
}
