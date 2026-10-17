package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/agentmesh/backend/internal/models"
)

// Build progress: what the chat shows while the builder works, instead of a
// bare spinner -- "Searched the web for ...", "Added HTTP Request ...",
// "Couldn't add ... (HTTP 404)", one line per tool call, like a coding
// agent's transcript. Labels are written for the person watching, not for
// the model.

// BuildStep is one finished tool call.
type BuildStep struct {
	// Kind groups steps for display: search, check, node, edge, x402, look.
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Status string `json:"status"` // "done" or "error"
	Detail string `json:"detail,omitempty"`
}

// BuildProgress is a snapshot: every step finished so far, and what is
// happening right now ("" when nothing is in flight).
type BuildProgress struct {
	Steps   []BuildStep `json:"steps"`
	Current string      `json:"current,omitempty"`
}

type progressTracker struct {
	on      func(BuildProgress)
	steps   []BuildStep
	current string
}

func (p *progressTracker) working(label string) {
	p.current = label
	p.emit()
}

func (p *progressTracker) finished(s BuildStep) {
	p.steps = append(p.steps, s)
	p.current = ""
	p.emit()
}

func (p *progressTracker) idle() {
	p.current = ""
	p.emit()
}

func (p *progressTracker) emit() {
	if p.on == nil {
		return
	}
	// A copy: the receiver may hold on to the snapshot while steps grows.
	p.on(BuildProgress{Steps: append([]BuildStep(nil), p.steps...), Current: p.current})
}

// runBuildCall executes one tool call from the model and returns the
// functionResponse payload for it. Every tool's failure comes back as data
// ({"result": "error: ..."} or {"error": ...}), never as a Go error, because
// the model needs to see it and try something else.
// graphMutations are the calls that change the graph, and so invalidate the
// last test run.
var graphMutations = map[string]bool{
	"add_node": true, "update_node": true, "remove_node": true,
	"add_edge": true, "remove_edge": true, "add_x402_node": true,
}

// maxTestRuns bounds the test runs one build may make, however often the
// model asks: each one really calls the workflow's agents and sources.
const maxTestRuns = 5

// testTracker remembers whether the graph, as it now stands, has been
// test-run and what that run produced.
type testTracker struct {
	run func(ctx context.Context, graph models.WorkflowGraph, input string) DryRunResult
	// dirty is set when this build changes the graph and cleared by a test
	// run. It starts false: a turn that changes nothing (a question, a
	// clarification) has nothing new to test.
	dirty bool
	last  *DryRunResult
	// rounds counts the times the gate sent the model back; runs counts
	// the test runs made.
	rounds int
	runs   int
}

func runBuildCall(ctx context.Context, graph *models.WorkflowGraph, c geminiFuncCall, apiKey string, x402 *x402Session, probed map[string]string, resolved map[string]bool, schedule *builderSchedule, tester *testTracker) map[string]any {
	response := dispatchBuildCall(ctx, graph, c, apiKey, x402, probed, resolved, schedule, tester)
	if graphMutations[c.name] {
		if text, _ := response["result"].(string); !strings.HasPrefix(text, "error: ") {
			tester.dirty = true
		}
	}
	return response
}

// resolved is the build's set of CoinGecko ids that resolve_coin returned;
// resolve_coin adds to it and add_node / update_node are checked against it.
func dispatchBuildCall(ctx context.Context, graph *models.WorkflowGraph, c geminiFuncCall, apiKey string, x402 *x402Session, probed map[string]string, resolved map[string]bool, schedule *builderSchedule, tester *testTracker) map[string]any {
	switch c.name {
	case "set_schedule":
		if schedule == nil {
			return map[string]any{"result": "error: schedules cannot be set here"}
		}
		out, err := schedule.set(c.args, time.Now())
		if err != nil {
			return map[string]any{"result": "error: " + err.Error()}
		}
		return map[string]any{"result": out}

	case "test_run":
		if tester.run == nil {
			return map[string]any{"result": "error: test runs are not available here"}
		}
		if tester.runs >= maxTestRuns {
			return map[string]any{"result": fmt.Sprintf("error: this build has already used its %d test runs. Reply to the user now, and say plainly whether the last test produced the answer they asked for.", maxTestRuns)}
		}
		tester.runs++
		input := argString(c.args, "input")
		manualNote := ""
		// A manual trigger carries no message, so a test started with one
		// proves nothing about a real run.
		if strings.TrimSpace(input) != "" && startsManually(*graph) {
			input = ""
			manualNote = " This workflow starts from a manual trigger, which carries no message, so your input was ignored and it was tested exactly as a real run starts -- with none. A step that needs something to work on must get it from a node in the workflow, not from the test input."
		}
		res := tester.run(ctx, *graph, input)
		tester.last, tester.dirty = &res, false
		out, _ := json.Marshal(res)
		note := manualNote + " -- Steps marked simulated were not performed (they would send, pay or write). "
		switch {
		// Before the plain Failed case, which this one is also in: a degraded
		// read sets both. The distinction matters to the model. A hard
		// failure means the workflow produced nothing; a degraded read means
		// it answered, without that source. Told only "the run FAILED", the
		// model rebuilt steps that were fine, or replied as though there were
		// no answer to quote.
		case res.Degraded:
			note += "A step that READS a source failed, and the run carried on past it the way a real run does -- so this workflow DOES answer, but with that source missing. Check that step's own settings first: at build time a wrong id, a wrong path or a dead endpoint is the usual cause, and shipping it unfixed means answering \"the source failed\" on every run from now on. If the source really is just unavailable, leave the workflow alone and tell the user which source was missing."
		case res.Failed:
			note += "The run FAILED: fix the step that failed and test again."
		case res.Empty:
			note += "Something returned NOTHING: find out why (a wrong id, a wrong path, a source with no data), fix it and test again."
		case res.Unverified:
			note += "Steps marked unverified could not be checked in a test (their input is simulated, or they need credits) -- that is not a fault, do not change the workflow because of it. Tell the user which steps went unchecked."
		default:
			note += "Check the answer really is what the user asked for before you reply, and quote it in your reply."
		}
		return map[string]any{"result": string(out) + note}

	case "web_search":
		// Not a graph mutation: it reads the world, and its answer + sources
		// shape is richer than a graph op's one-line result.
		out, err := webSearch(ctx, argString(c.args, "query"), apiKey)
		if err != nil {
			// The model needs to know the search failed, but not how:
			// webSearch's error wraps postLLMJSON's, which carries Gemini's raw
			// response body -- exactly what BuildWorkflow keeps out of the
			// chat. Logged here in full, reported as a plain failure.
			log.Printf("builder web_search failed: %v", err)
			return map[string]any{"error": "web search is unavailable right now"}
		}
		return map[string]any{"result": out}

	case "resolve_coin":
		matches, err := resolveCoin(ctx, argString(c.args, "query"))
		if err != nil {
			return map[string]any{"result": "error: " + SanitizeRunError(err.Error())}
		}
		if len(matches) == 0 {
			return map[string]any{"result": fmt.Sprintf(
				"not_listed: CoinGecko has no coin matching %q. It cannot be tracked with a coingecko node. Tell the user plainly that this token is not listed, and do not use a different asset with a similar name or fall back to a web search for its price.",
				argString(c.args, "query"))}
		}
		if resolved != nil {
			for _, m := range matches {
				resolved[m.ID] = true
			}
		}
		out, _ := json.Marshal(matches)
		return map[string]any{"result": string(out) + " -- use the id field, not the name or the symbol."}

	case "search_x402", "add_x402_node":
		// These need the request's catalog loader. add_x402_node edits the
		// graph, but only ever from a catalog entry -- never from model-typed
		// URL or price fields.
		var out string
		var err error
		if c.name == "search_x402" {
			out, err = x402.search(ctx, c.args)
		} else {
			out, err = x402.add(ctx, graph, c.args)
		}
		if err != nil {
			out = "error: " + err.Error()
		}
		return map[string]any{"result": out}

	case "fetch_url":
		u := strings.TrimSpace(argString(c.args, "url"))
		r, ok := probed[u]
		if !ok {
			r = fetchURL(ctx, u)
			probed[u] = r
		}
		return map[string]any{"result": r}

	case "describe_node":
		out, err := describeNode(argString(c.args, "type"), argString(c.args, "template"))
		if err != nil {
			out = "error: " + err.Error()
		}
		return map[string]any{"result": out}
	}

	// An http node's url is checked before the node is added: a live build
	// wired an address it had never fetched and the run 404'd. Asking the
	// model to verify first was not enough, so the builder calls the url
	// itself -- through the same client a run uses -- and refuses one a run
	// would fail on.
	probeNote := ""
	if u := httpNodeURLChange(graph, c.name, c.args); u != "" {
		probe, seen := probed[u]
		if !seen {
			probe = fetchURL(ctx, u)
			probed[u] = probe
		}
		refuse, note := judgeProbe(u, probe)
		if refuse != "" {
			return map[string]any{"result": "error: " + refuse + noNodeCreatedNote(c.name, nil)}
		}
		probeNote = note
	}
	result, err := applyGraphOpResolved(graph, c.name, c.args, resolved)
	if err != nil {
		return map[string]any{"result": "error: " + err.Error() + noNodeCreatedNote(c.name, err)}
	}
	return map[string]any{"result": result + probeNote}
}

// startsManually reports whether every trigger is manual, so a run never
// carries an incoming message.
func startsManually(graph models.WorkflowGraph) bool {
	found := false
	for _, n := range graph.Nodes {
		if n.Type != models.NodeTypeTrigger {
			continue
		}
		if n.Template != "manual" {
			return false
		}
		found = true
	}
	return found
}

// runningLabel says what a call is about to do.
func runningLabel(graph *models.WorkflowGraph, name string, args map[string]any) string {
	switch name {
	case "web_search":
		return fmt.Sprintf("Searching the web for “%s”", clip(argString(args, "query"), 80))
	case "fetch_url":
		return "Checking " + shortURL(argString(args, "url"))
	case "resolve_coin":
		return fmt.Sprintf("Looking up “%s” on CoinGecko", clip(argString(args, "query"), 60))
	case "set_schedule":
		return "Setting the schedule"
	case "search_x402":
		return fmt.Sprintf("Searching the x402 Bazaar for “%s”", clip(argString(args, "query"), 60))
	case "add_x402_node":
		return "Adding an x402 endpoint"
	case "describe_node":
		return fmt.Sprintf("Reading the %s settings", templateTitle(argString(args, "type"), argString(args, "template")))
	case "add_node":
		label := "Adding " + namedTemplate(argString(args, "type"), argString(args, "template"), argString(args, "name"))
		if httpNodeURLChange(graph, name, args) != "" {
			label += " and checking its URL"
		}
		return label
	case "update_node":
		return fmt.Sprintf("Updating “%s”", nodeName(graph, argString(args, "id")))
	case "remove_node":
		return fmt.Sprintf("Removing “%s”", nodeName(graph, argString(args, "id")))
	case "add_edge":
		return fmt.Sprintf("Connecting “%s” → “%s”", nodeName(graph, argString(args, "from")), nodeName(graph, argString(args, "to")))
	case "remove_edge":
		return "Removing a connection"
	case "test_run":
		return "Test-running the workflow"
	}
	return "Working"
}

// finishedStep describes a call once its response is known. graph is the
// graph AFTER the call, so a node it just added can be named.
func finishedStep(graph *models.WorkflowGraph, name string, args map[string]any, response map[string]any) BuildStep {
	text, _ := response["result"].(string)
	errText, _ := response["error"].(string)
	if strings.HasPrefix(text, "error: ") {
		errText = strings.TrimPrefix(text, "error: ")
	}
	failed := errText != ""
	step := BuildStep{Status: "done"}
	if failed {
		step.Status = "error"
		step.Detail = clip(firstSentence(errText), 180)
	}

	switch name {
	case "web_search":
		step.Kind = "search"
		step.Label = fmt.Sprintf("Searched the web for “%s”", clip(argString(args, "query"), 80))
		if failed {
			step.Label = fmt.Sprintf("Web search for “%s” failed", clip(argString(args, "query"), 80))
			// Never the error text: it can carry a raw upstream body.
			step.Detail = "the search service returned an error"
		}
	case "set_schedule":
		step.Kind = "edit"
		step.Label = scheduleLabel(args)
		if failed {
			step.Label = "Couldn't set the schedule"
		}
	case "resolve_coin":
		step.Kind = "search"
		step.Label = fmt.Sprintf("Looked up “%s” on CoinGecko", clip(argString(args, "query"), 60))
		if strings.HasPrefix(text, "not_listed:") {
			step.Status = "error"
			step.Label = fmt.Sprintf("“%s” is not listed on CoinGecko", clip(argString(args, "query"), 60))
		}
	case "fetch_url":
		step.Kind = "check"
		var p struct {
			Status int    `json:"status"`
			Error  string `json:"error"`
		}
		_ = json.Unmarshal([]byte(text), &p)
		u := shortURL(argString(args, "url"))
		switch {
		case p.Error != "":
			step.Status, step.Label, step.Detail = "error", "Couldn't reach "+u, clip(firstSentence(p.Error), 180)
		case p.Status >= 200 && p.Status < 300:
			step.Label = fmt.Sprintf("Checked %s → HTTP %d", u, p.Status)
		default:
			step.Status, step.Label = "error", fmt.Sprintf("Checked %s → HTTP %d", u, p.Status)
		}
	case "search_x402":
		step.Kind = "x402"
		q := clip(argString(args, "query"), 60)
		step.Label = fmt.Sprintf("Searched the x402 Bazaar for “%s”", q)
		var r struct {
			Results []any `json:"results"`
		}
		if !failed && json.Unmarshal([]byte(text), &r) == nil {
			step.Label += fmt.Sprintf(" → %d found", len(r.Results))
		}
	case "add_x402_node":
		step.Kind = "x402"
		if failed {
			step.Label = "Couldn't add the x402 endpoint"
		} else if n := lastNode(graph); n != nil {
			step.Label = fmt.Sprintf("Added x402 endpoint “%s”", n.Name)
			if i := strings.Index(text, "costs "); i >= 0 {
				step.Detail = clip(firstSentence(text[i:]), 120)
			}
		}
	case "describe_node":
		step.Kind = "look"
		step.Label = fmt.Sprintf("Read the %s settings", templateTitle(argString(args, "type"), argString(args, "template")))
	case "add_node":
		step.Kind = "node"
		what := namedTemplate(argString(args, "type"), argString(args, "template"), argString(args, "name"))
		if failed {
			step.Label = "Couldn't add " + what
		} else {
			step.Label = "Added " + what
		}
	case "update_node":
		step.Kind = "node"
		n := nodeName(graph, argString(args, "id"))
		step.Label = fmt.Sprintf("Updated “%s”", n)
		if failed {
			step.Label = fmt.Sprintf("Couldn't update “%s”", n)
		}
	case "remove_node":
		step.Kind = "node"
		step.Label = "Removed a step"
		if failed {
			step.Label = "Couldn't remove a step"
		}
	case "add_edge":
		step.Kind = "edge"
		from, to := nodeName(graph, argString(args, "from")), nodeName(graph, argString(args, "to"))
		switch {
		case failed:
			step.Label = fmt.Sprintf("Couldn't connect “%s” → “%s”", from, to)
		case argString(args, "kind") == "attach":
			step.Label = fmt.Sprintf("Attached “%s” to “%s”", from, to)
		default:
			step.Label = fmt.Sprintf("Connected “%s” → “%s”", from, to)
		}
	case "remove_edge":
		step.Kind = "edge"
		step.Label = "Removed a connection"
		if failed {
			step.Label = "Couldn't remove a connection"
		}
	case "test_run":
		step.Kind = "check"
		var r DryRunResult
		body := text
		if i := strings.Index(body, " -- Steps marked simulated"); i >= 0 {
			body = body[:i]
		}
		if failed || json.Unmarshal([]byte(body), &r) != nil {
			step.Label = "Couldn't test-run the workflow"
			break
		}
		switch {
		case r.Failed:
			step.Status = "error"
			step.Label = "Test run failed"
			for _, s := range r.Steps {
				if s.Status == "failed" {
					step.Label = fmt.Sprintf("Test run failed at “%s”", s.Name)
					step.Detail = clip(firstSentence(s.Error), 180)
				}
			}
			if step.Detail == "" {
				step.Detail = clip(firstSentence(r.Error), 180)
			}
		case r.Empty:
			step.Status = "error"
			step.Label = "Test run: a step returned nothing"
			for _, s := range r.Steps {
				if s.Status == "empty" {
					step.Label = fmt.Sprintf("Test run: “%s” returned nothing", s.Name)
					break
				}
			}
		case r.Unverified:
			step.Label = "Test run: part of the workflow can't be checked in a test"
			if names := unverifiedSteps(r); names != "" {
				step.Detail = clip(names, 180)
			}
		default:
			answer := r.Answer
			if answer == "" {
				answer = r.FinalOutput
			}
			step.Label = fmt.Sprintf("Test-ran the workflow → “%s”", clip(answer, 110))
		}
	default:
		step.Kind = "look"
		step.Label = name
	}
	return step
}

func lastNode(graph *models.WorkflowGraph) *models.WorkflowNode {
	if len(graph.Nodes) == 0 {
		return nil
	}
	return &graph.Nodes[len(graph.Nodes)-1]
}

// nodeName is how a node is shown to the user: its own name, else its
// template's catalog name, else its id.
func nodeName(graph *models.WorkflowGraph, id string) string {
	n, ok := findGraphNode(graph, id)
	if !ok {
		return id
	}
	if n.Name != "" {
		return n.Name
	}
	return templateTitle(string(n.Type), n.Template)
}

// templateTitle is a template's catalog display name ("HTTP Request").
func templateTitle(nodeType, template string) string {
	if nodeType == "tool402" {
		return "x402 endpoint"
	}
	if t, ok := catalogTemplate(nodeType, template); ok {
		return t.Name
	}
	if template == "" {
		return nodeType
	}
	return nodeType + "/" + template
}

// namedTemplate is `HTTP Request “Fetch Nifty”`, or just the template title.
func namedTemplate(nodeType, template, name string) string {
	title := templateTitle(nodeType, template)
	if name == "" {
		return title
	}
	return fmt.Sprintf("%s “%s”", title, name)
}

func shortURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return clip(raw, 60)
	}
	return clip(u.Host+u.Path, 60)
}

// firstSentence keeps an error readable in one line: the model-facing
// guidance after the first sentence is for the model, not the user.
func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	for _, sep := range []string{". ", " -- "} {
		if i := strings.Index(s, sep); i > 0 {
			s = s[:i]
		}
	}
	return strings.TrimSuffix(s, ".")
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// noNodeCreatedNote is the note appended to a rejected add_node: nothing was
// created, so there is no id to refer to. Without it a live build invented
// one ("n_1789376858057591417") and spent two rounds trying to update and
// connect a node that never existed.
//
// Not for the rejections that name a node already on the graph (they tell the
// model to use update_node on it, which this would contradict), and not for
// any other tool.
func noNodeCreatedNote(toolName string, err error) string {
	if toolName != "add_node" {
		return ""
	}
	var exists nodeExistsError
	if err != nil && errors.As(err, &exists) {
		return ""
	}
	return " -- no node was created, so there is no id for it yet; fix the call and add it again"
}

// scheduleLabel says what set_schedule did, in the user's terms.
func scheduleLabel(args map[string]any) string {
	cadence := strings.ToLower(argString(args, "cadence"))
	at := argString(args, "time")
	switch cadence {
	case "off":
		return "Removed the schedule"
	case "weekly":
		return fmt.Sprintf("Scheduled every %s at %s", argString(args, "day"), at)
	case "monthly":
		n, _ := argInt(args, "dayOfMonth")
		return fmt.Sprintf("Scheduled on day %d of every month at %s", n, at)
	default:
		return "Scheduled every day at " + at
	}
}
