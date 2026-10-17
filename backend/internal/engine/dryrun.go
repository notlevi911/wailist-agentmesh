package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"unicode/utf8"

	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
)

// dryRunOutputShown bounds each step's output in a dry-run result: enough for
// the builder to read an answer or spot an empty object, not a whole page.
const dryRunOutputShown = 700

// DryRunOptions is what a dry run starts from, beyond the graph itself.
type DryRunOptions struct {
	// Input is the chat or webhook message to start from ("" for a manual run).
	Input string
	// State is the workflow's stored variables, loaded as a run loads them,
	// so {{state.x}} resolves to what a real run would see.
	State map[string]any
	// PlatformKeys is the provider key map a platform-key agent needs.
	PlatformKeys map[string]string
	// CheckBalance, when set, gates each platform-key agent on its real fee
	// exactly as a run's preflight does. A test run that could call any
	// agent regardless would let a user with no credits run them for free.
	CheckBalance func(ctx context.Context, amountUSDMicros int64) error
	// ChargeAgent, when set, is called after a platform-key agent call
	// succeeds, with the same fee a run would charge. A test run costs the
	// platform exactly what a run does, so checking the balance without ever
	// debiting it would make those calls free for anyone who keeps a single
	// credit on the account. A failure to charge is logged, not surfaced:
	// the call has already happened.
	ChargeAgent func(ctx context.Context, nodeID string, amountUSDMicros int64, model string) error
}

// unverifiable is a step a dry run could not check -- not a failure of the
// workflow, so the builder is not told to fix it.
type unverifiable struct{ reason string }

func (u unverifiable) Error() string { return u.reason }

// DryRun executes a workflow graph the way a run does -- same topological
// order, same attach rules, same variables and {{state.x}} expansion, the
// real executors -- except that every step nodes.DryRunExecutes rejects is
// simulated rather than performed, so nothing is sent, paid for, rented or
// written. Nothing is persisted and nothing is billed. The chat builder calls
// it to check a workflow actually produces an answer before it tells the user
// the workflow is done.
func DryRun(ctx context.Context, graph models.WorkflowGraph, opts DryRunOptions) nodes.DryRunResult {
	res := nodes.DryRunResult{Steps: []nodes.DryRunStep{}}
	levels, err := TopologicalSort(graph.Nodes, graph.Edges)
	if err != nil {
		res.Failed, res.Error = true, err.Error()
		return res
	}
	attachMap := BuildAttachMap(graph.Nodes, graph.Edges)
	// Nodes attached to an agent are its resources, not steps -- the runner
	// skips them the same way.
	attached := map[string]bool{}
	next := map[string][]string{}
	for _, e := range graph.Edges {
		switch e.Kind {
		case models.EdgeKindAttach:
			attached[e.From] = true
		case models.EdgeKindFlow:
			next[e.From] = append(next[e.From], e.To)
		}
	}
	// fedBySimulated maps a step to the simulated step whose placeholder
	// output flows into it. What such a step does with a placeholder says
	// nothing about the workflow: a json_extract on it fails, an agent
	// handed it says there is no data. Neither is a fault to fix.
	fedBySimulated := map[string]string{}

	var inputJSON []byte
	if opts.Input != "" {
		inputJSON, _ = json.Marshal(map[string]string{"message": opts.Input})
	}
	rc := NewRunContext("dry-run", inputJSON)
	// Same per-node message resolution as a run (see nodeRunContext), so a
	// test run checks what each step would really receive.
	msgPreds := messagePredecessors(levels, graph.Edges)
	if opts.State != nil {
		rc.SetState(opts.State)
	}

	lastFedBySimulated := false
	// wasSimulated marks every simulated step; carries holds what one would
	// have sent, for only the steps that send something (see simulatedCarry).
	// The end of the run reads both when it finished on a simulated step.
	wasSimulated := map[string]bool{}
	carries := map[string]carry{}
	typeOf := make(map[string]models.NodeType, len(graph.Nodes))
	for _, n := range graph.Nodes {
		typeOf[n.ID] = n.Type
	}
	for _, level := range levels {
		for _, n := range level {
			if attached[n.ID] {
				continue
			}
			if ctx.Err() != nil {
				res.Failed, res.Error = true, "the test run ran out of time"
				return res
			}
			step := nodes.DryRunStep{NodeID: n.ID, Name: n.Name, Type: string(n.Type), Template: n.Template}
			source, tainted := fedBySimulated[n.ID]
			nrc := rc.forNode(msgPreds[n.ID])
			out, reason, err := dryRunNode(ctx, n, attachMap[n.ID], nrc, opts)
			var u unverifiable
			switch {
			case errors.As(err, &u):
				step.Status, step.Reason = "unverified", u.reason
				res.Steps = append(res.Steps, step)
				res.Unverified = true
				return res
			case err != nil && credentialReason(n, err) != "":
				// Not a fault to fix: the builder cannot set credentials, so
				// a 401 on a node that takes one means the node is waiting for
				// the user, not that the workflow is wrong.
				step.Status, step.Reason = "unverified", credentialReason(n, err)
				step.Error = nodes.SanitizeRunError(err.Error())
				res.Steps = append(res.Steps, step)
				res.Unverified = true
				return res
			case err != nil && tainted:
				step.Status, step.Error = "unverified", nodes.SanitizeRunError(err.Error())
				step.Reason = fmt.Sprintf("its input comes from %q, which a test run only simulates, so it cannot be checked", source)
				res.Steps = append(res.Steps, step)
				res.Unverified = true
				return res
			case err != nil && nodes.IsDegradable(n):
				// A real run does not stop here: a read step's failure is
				// handed downstream as an error payload and the run carries
				// on (engine.Runner). Stopping would judge the workflow by
				// behaviour it no longer has, and would never show the
				// builder the answer the user will actually get.
				//
				// Still Failed, though. This runs while the workflow is being
				// built, where a failing source is usually a wrong id, a wrong
				// path or a dead API that the builder can fix -- and the test
				// gate is what sends it back to fix them.
				step.Status, step.Error = "failed", nodes.SanitizeRunError(err.Error())
				res.Steps = append(res.Steps, step)
				res.Failed = true
				res.Degraded = true
				rc.Set(n.ID, map[string]any{
					"error":    nodes.SanitizeRunError(err.Error()),
					"degraded": true,
					"node":     n.Name,
				})
				continue
			case err != nil:
				step.Status, step.Error = "failed", nodes.SanitizeRunError(err.Error())
				res.Steps = append(res.Steps, step)
				res.Failed = true
				return res // an action's failure ends a real run, and this one
			}
			// What a simulated step would have sent, worked out before its own
			// placeholder output is Set: a body or message template reads
			// {{ result }}, which must still be the step's input, exactly as
			// it is when the real connector runs.
			var pending carry
			var sends bool
			if reason != "" {
				pending, sends = simulatedCarry(n, out, nrc)
			}
			rc.Set(n.ID, out)
			step.Output = clipOutput(out)
			isStep := n.Type != models.NodeTypeTrigger && n.Type != models.NodeTypeEnd
			switch {
			case reason != "":
				step.Status, step.Reason = "simulated", reason
				wasSimulated[n.ID] = true
				if sends {
					carries[n.ID] = pending
				}
				source = n.Name
				if source == "" {
					source = n.ID
				}
			case tainted && isStep:
				step.Status = "unverified"
				step.Reason = fmt.Sprintf("its input comes from %q, which a test run only simulates, so its output is not real", source)
				res.Unverified = true
			case isStep && nodes.IsEmptyOutput(out):
				step.Status = "empty"
				res.Empty = true
			default:
				step.Status = "ran"
			}
			if reason != "" || tainted {
				for _, to := range next[n.ID] {
					if _, seen := fedBySimulated[to]; !seen {
						fedBySimulated[to] = source
					}
				}
			}
			lastFedBySimulated = reason != "" || tainted
			if n.Type == models.NodeTypeAgent {
				if m, ok := out.(map[string]any); ok {
					if s, ok := m["message"].(string); ok {
						res.Answer = s
					}
				}
			}
			res.Steps = append(res.Steps, step)
		}
	}
	final := rc.Message()
	res.FinalOutput = clipText(final)
	// Which step the run really ended on: the last output Set, skipping the
	// end node (which only passes its input along) and the trigger. This is
	// the same order Message() reads, not the level-by-level order of Steps,
	// which interleaves parallel branches. If that step was simulated, its
	// wouldSend is exactly what a real run would have sent from it.
	order := rc.OutputOrder()
	for i := len(order) - 1; i >= 0; i-- {
		if t := typeOf[order[i]]; t == models.NodeTypeEnd || t == models.NodeTypeTrigger {
			continue
		}
		if wasSimulated[order[i]] {
			res.FinalSimulated = true
			if c, ok := carries[order[i]]; ok {
				res.WouldSend = clipText(c.text)
				// The run's final output is only a placeholder here, so the
				// ordinary empty-output check above cannot see this. Only an
				// explicit template that resolved to nothing -- a field that
				// does not exist -- is flagged: that is unambiguously a send
				// that would post nothing. An input that happens to be empty
				// (a manual trigger) is not, since the step may never use it.
				if c.templated && nodes.IsEmptyOutput(c.text) {
					res.Empty = true
					if res.Error == "" {
						res.Error = fmt.Sprintf("%q would have sent an empty message", stepLabel(graph, order[i]))
					}
				}
			}
		}
		break
	}
	if nodes.IsEmptyOutput(final) && !lastFedBySimulated {
		res.Empty = true
	}
	return res
}

// dryRunNode runs or simulates one node. A non-empty reason means it was
// simulated.
func dryRunNode(ctx context.Context, n models.WorkflowNode, attach models.AttachConfig, rc *nodeRunContext, opts DryRunOptions) (any, string, error) {
	if ok, reason := nodes.DryRunExecutes(n); !ok {
		sim := map[string]any{"simulated": true, "reason": reason}
		if n.Type == models.NodeTypeAction || n.Type == models.NodeTypeGoogle {
			sim["wouldSend"] = nodes.ResolveMessageForTest(n, rc)
		}
		return sim, reason, nil
	}
	n = expandNodeState(n, rc.State())

	switch n.Type {
	case models.NodeTypeTrigger:
		return rc.input, "", nil
	case models.NodeTypeEnd:
		return rc.Message(), "", nil
	case models.NodeTypeProvider:
		return rc.Message(), "", nil
	case models.NodeTypeTool:
		out, err := nodes.ExecuteTool(ctx, n, rc)
		return out, "", err
	case models.NodeTypeAction:
		out, err := nodes.ExecuteAction(ctx, n, rc)
		if errors.Is(err, nodes.ErrActionSkipped) {
			// A connector skips itself when something it needs is missing.
			// In a run that is a step doing nothing; in a test it means this
			// step was never checked, which must not read as success. A
			// missing credential is the user's to add; any other skip (no
			// coin ids, no query, no city) is a setting the builder left out,
			// so it goes back for repair as a failure.
			if nodes.IsCredentialSkip(out) {
				return out, "", unverifiable{nodes.MissingCredentialReason}
			}
			return out, "", fmt.Errorf("%s did not run: a setting it needs is missing (%v)", n.Template, out)
		}
		return out, "", err
	case models.NodeTypeAgent:
		// The same preflight a run applies (Runner.executeNode): a
		// platform-key agent runs only if the user could pay for it -- and,
		// below, it is charged for it.
		var platformFee int64
		var platformModel string
		if p := attach.Provider; p != nil && p.KeyMode == "platform" {
			platformModel = nodes.ResolveModel(p.Template, p.Model)
			platformFee = nodes.PlatformKeyFeeUSDMicros(nodes.ModelTier(p.Template, platformModel))
			if opts.CheckBalance != nil {
				if err := opts.CheckBalance(ctx, platformFee); err != nil {
					return nil, "", unverifiable{"this agent runs on AgentMesh credits and the account does not have enough credits to test it"}
				}
			}
		}
		// Only tools a dry run may execute are handed to the agent: a paid
		// x402 tool, or an HTTP call that could change something, is left
		// out rather than invoked.
		safe := attach
		safe.Tools = nil
		var withheld []string
		for _, t := range attach.Tools {
			if ok, _ := nodes.DryRunExecutes(t); ok {
				safe.Tools = append(safe.Tools, t)
				continue
			}
			name := t.Name
			if name == "" {
				name = t.ID
			}
			withheld = append(withheld, name)
		}
		out, err := nodes.ExecuteAgent(ctx, n, safe, models.AgentWallet{}, nil, rc, nil, opts.PlatformKeys, nodes.X402RelayConfig{})
		// Charged as soon as the call returns, before anything classifies the
		// result: the model has been paid for whatever we decide the output
		// means, and the branches below return early. A failed charge is
		// logged rather than turned into a step failure.
		//
		// Detached from ctx, with its own timeout, as the runner's ledger
		// writes are (ledgerCompensationTimeout): ctx ends at the build's time
		// budget, and a call that succeeded just before it must still be
		// charged.
		if err == nil && platformFee > 0 && opts.ChargeAgent != nil {
			cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ledgerCompensationTimeout)
			if cerr := opts.ChargeAgent(cctx, n.ID, platformFee, platformModel); cerr != nil {
				log.Printf("dry run: charge agent %s (%d micros): %v", n.ID, platformFee, cerr)
			}
			cancel()
		}
		// A model call's 401 surfaces on the agent, but the key that was
		// refused belongs to the attached provider. On BYOK that key is the
		// user's own, pasted in the Inspector, so this is theirs to fix and
		// not a workflow the builder should be sent to repair. A platform key
		// is nobody's to paste, so it stays a plain failure.
		if err != nil && nodes.IsAuthRejection(err.Error()) {
			if p := attach.Provider; p != nil && p.KeyMode != "platform" {
				return nil, "", unverifiable{nodes.CredentialRejectedOrMissing(*p)}
			}
		}
		// A tool this run may not call is the test's limit, not the
		// workflow's fault -- reported as a failure it sent the builder to
		// repair a correct workflow. Only when the agent had nothing to say,
		// though: a call that errored produces no output either, so without
		// the err check a 429 or a bad model name was excused too.
		if len(withheld) > 0 &&
			((err == nil && nodes.IsEmptyOutput(out)) || errors.Is(err, nodes.ErrNoModelText)) {
			return nil, "", unverifiable{fmt.Sprintf(
				"a test run never calls %s, so this agent had nothing to work from and its answer cannot be checked",
				strings.Join(withheld, ", "))}
		}
		return out, "", err
	}
	return nil, "", fmt.Errorf("a %s step cannot be test-run", n.Type)
}

// carry is what a simulated step would have sent, and whether it came from an
// explicit template (and so can be judged empty).
type carry struct {
	text      string
	templated bool
}

// actionsWithoutMessage are simulated connectors that never send the run's
// message: they read (calendly, telegram_get_updates), send their own query
// (graphql), or build an email from their own fields. Taken from the
// connectors that do not call resolveMessage; the read-only connectors a test
// run executes for real (coingecko, hackernews, rss) are never simulated.
var actionsWithoutMessage = map[string]bool{
	"calendly": true, "telegram_get_updates": true, "graphql": true,
	"email": true, "resend": true, "sendgrid": true, "postmark": true, "brevo": true,
}

// simulatedCarry says what a simulated step would have sent, if it sends
// anything at all. http and Google ask the connector's own body/summary
// decision (nodes.HTTPRequestBodyForDryRun, nodes.GoogleOutgoingForDryRun),
// so this cannot drift from what the real connector does. A step that only
// reads, writes state, or rents compute has no carry: judging it by its input
// would flag a correct workflow as an empty send.
//
// Must be called before the step's own output is Set on rc.
func simulatedCarry(n models.WorkflowNode, out any, rc *nodeRunContext) (carry, bool) {
	switch n.Type {
	case models.NodeTypeAction:
		if actionsWithoutMessage[n.Template] {
			return carry{}, false
		}
		m, _ := out.(map[string]any)
		msg, ok := m["wouldSend"].(string)
		if !ok {
			return carry{}, false
		}
		return carry{text: msg, templated: n.Config["messageTemplate"] != ""}, true
	case models.NodeTypeGoogle:
		text, sends, templated := nodes.GoogleOutgoingForDryRun(n, rc)
		return carry{text: text, templated: templated}, sends
	case models.NodeTypeTool:
		if n.Template != "http" {
			return carry{}, false
		}
		body, sends, templated := nodes.HTTPRequestBodyForDryRun(n, rc)
		return carry{text: body, templated: templated}, sends
	}
	return carry{}, false
}

// stepLabel is a node's name, or its id when it has none.
func stepLabel(graph models.WorkflowGraph, id string) string {
	for _, n := range graph.Nodes {
		if n.ID == id && n.Name != "" {
			return n.Name
		}
	}
	return id
}

// credentialReason is nodes.CredentialProblem's reason, or "" when the
// failure is not about a credential the user supplies.
func credentialReason(n models.WorkflowNode, err error) string {
	reason, _ := nodes.CredentialProblem(n, err.Error())
	return reason
}

func clipOutput(v any) string {
	if s, ok := v.(string); ok {
		return clipText(s)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return clipText(fmt.Sprint(v))
	}
	return clipText(string(b))
}

// clipText bounds a value at dryRunOutputShown bytes without splitting a
// rune: the result is marshalled into the payload sent to the model and
// shown in chat, and half a rune is invalid UTF-8 in both.
func clipText(s string) string {
	if len(s) <= dryRunOutputShown {
		return s
	}
	cut := dryRunOutputShown
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
