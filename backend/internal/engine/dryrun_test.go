package engine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
)

func dn(id string, t models.NodeType, template string) models.WorkflowNode {
	return models.WorkflowNode{ID: id, Type: t, Template: template, Name: id}
}

func flow(from, to string) models.WorkflowEdge {
	return models.WorkflowEdge{ID: from + "-" + to, From: from, To: to, Kind: models.EdgeKindFlow, ToPort: "in"}
}

func dataServer(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/empty" {
			io.WriteString(w, `{}`)
			return
		}
		io.WriteString(w, `{"data":{"price":1.5}}`)
	}))
	t.Cleanup(srv.Close)
	nodes.SetURLValidatorForTest(func(string) error { return nil })
	t.Cleanup(func() { nodes.SetURLValidatorForTest(func(string) error { return nil }) })
	return srv
}

func TestDryRunRunsReadOnlyStepsForReal(t *testing.T) {
	srv := dataServer(t)
	fetch := dn("fetch", models.NodeTypeTool, "http")
	fetch.URL = srv.URL + "/quote"
	extract := dn("extract", models.NodeTypeTool, "json_extract")
	extract.Config = map[string]string{"jsonPath": "data.price"}
	g := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{dn("t", models.NodeTypeTrigger, "manual"), fetch, extract, dn("e", models.NodeTypeEnd, "done")},
		Edges: []models.WorkflowEdge{flow("t", "fetch"), flow("fetch", "extract"), flow("extract", "e")},
	}
	res := DryRun(context.Background(), g, DryRunOptions{})
	if res.Failed || res.Empty || res.Error != "" {
		t.Fatalf("want a clean run, got %+v", res)
	}
	if res.FinalOutput != "1.5" {
		t.Fatalf("want the extracted price as the final output, got %q", res.FinalOutput)
	}
}

// The user's case: a lookup that "succeeds" with {}.
func TestDryRunFlagsAStepThatReturnsNothing(t *testing.T) {
	srv := dataServer(t)
	fetch := dn("fetch", models.NodeTypeTool, "http")
	fetch.URL = srv.URL + "/empty"
	g := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{dn("t", models.NodeTypeTrigger, "manual"), fetch, dn("e", models.NodeTypeEnd, "done")},
		Edges: []models.WorkflowEdge{flow("t", "fetch"), flow("fetch", "e")},
	}
	res := DryRun(context.Background(), g, DryRunOptions{})
	if !res.Empty {
		t.Fatalf("an empty {} result must be flagged, got %+v", res)
	}
	var fetchStep nodes.DryRunStep
	for _, s := range res.Steps {
		if s.NodeID == "fetch" {
			fetchStep = s
		}
	}
	if fetchStep.Status != "empty" {
		t.Fatalf("the step that returned {} must be marked empty, got %+v", fetchStep)
	}
}

func TestDryRunSimulatesStepsWithSideEffects(t *testing.T) {
	slack := dn("slack", models.NodeTypeAction, "slack")
	// A real webhook would be called if this were executed; the test server
	// fails the test if anything reaches it.
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer srv.Close()
	slack.Secrets = map[string]string{"slackWebhookURL": srv.URL}
	slack.Config = map[string]string{"messageTemplate": "Price: {{ input }}"}
	g := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{dn("t", models.NodeTypeTrigger, "chat"), slack, dn("e", models.NodeTypeEnd, "done")},
		Edges: []models.WorkflowEdge{flow("t", "slack"), flow("slack", "e")},
	}
	res := DryRun(context.Background(), g, DryRunOptions{Input: "hello"})
	if hit {
		t.Fatal("a dry run must never send the Slack message")
	}
	for _, s := range res.Steps {
		if s.NodeID == "slack" {
			if s.Status != "simulated" || s.Reason == "" {
				t.Fatalf("slack must be simulated with a reason, got %+v", s)
			}
			if !strings.Contains(s.Output, "Price: hello") {
				t.Fatalf("a simulated send should show what it would send, got %q", s.Output)
			}
			return
		}
	}
	t.Fatal("no slack step in the result")
}

func TestDryRunReportsTheAgentsAnswer(t *testing.T) {
	gem := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"MYRAD is $0.00021."}]}}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}`)
	}))
	defer gem.Close()
	nodes.SetGeminiBaseURL(gem.URL)
	defer nodes.SetGeminiBaseURL("https://generativelanguage.googleapis.com")

	provider := dn("p", models.NodeTypeProvider, "gemini")
	provider.KeyMode = "platform"
	g := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{dn("t", models.NodeTypeTrigger, "manual"), dn("a", models.NodeTypeAgent, "agent"), provider, dn("e", models.NodeTypeEnd, "done")},
		Edges: []models.WorkflowEdge{flow("t", "a"), flow("a", "e"),
			{ID: "pa", From: "p", To: "a", Kind: models.EdgeKindAttach, ToPort: "model"}},
	}
	res := DryRun(context.Background(), g, DryRunOptions{PlatformKeys: map[string]string{"gemini": "k"}})
	if res.Failed {
		t.Fatalf("unexpected failure: %+v", res)
	}
	if res.Answer != "MYRAD is $0.00021." {
		t.Fatalf("want the agent's reply as the answer, got %q", res.Answer)
	}
}

func TestDryRunReportsALoop(t *testing.T) {
	g := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{dn("t", models.NodeTypeTrigger, "manual"), dn("a", models.NodeTypeTool, "calc"), dn("b", models.NodeTypeTool, "calc")},
		Edges: []models.WorkflowEdge{flow("t", "a"), flow("a", "b"), flow("b", "a")},
	}
	if res := DryRun(context.Background(), g, DryRunOptions{}); res.Error == "" || !res.Failed {
		t.Fatalf("a loop must fail the dry run, got %+v", res)
	}
}

func stepOf(res nodes.DryRunResult, id string) nodes.DryRunStep {
	for _, s := range res.Steps {
		if s.NodeID == id {
			return s
		}
	}
	return nodes.DryRunStep{}
}

// Review finding: a simulated step hands its placeholder to the step after
// it, and a json_extract on that placeholder failed -- a correct workflow
// reported as broken, which sent the builder off to "fix" it.
func TestDryRunDoesNotBlameAStepFedBySimulatedData(t *testing.T) {
	paid := dn("paid", models.NodeTypeTool402, "x402")
	extract := dn("extract", models.NodeTypeTool, "json_extract")
	extract.Config = map[string]string{"jsonPath": "data.price"}
	g := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{dn("t", models.NodeTypeTrigger, "manual"), paid, extract, dn("e", models.NodeTypeEnd, "done")},
		Edges: []models.WorkflowEdge{flow("t", "paid"), flow("paid", "extract"), flow("extract", "e")},
	}
	res := DryRun(context.Background(), g, DryRunOptions{})
	if res.Failed || res.Empty {
		t.Fatalf("a step fed only by simulated data must not fail the test run, got %+v", res)
	}
	if !res.Unverified {
		t.Fatalf("the run must say it could not be checked end to end, got %+v", res)
	}
	if s := stepOf(res, "extract"); s.Status != "unverified" || s.Reason == "" {
		t.Fatalf("the extract step must be marked unverified with a reason, got %+v", s)
	}
}

// Review finding: a real run loads the workflow's variables and expands
// {{state.x}} in the endpoint too; a test run that did neither called the
// wrong URL and reported a working workflow as broken.
func TestDryRunExpandsWorkflowVariablesLikeARun(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/quote/btc" {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"error":"not found"}`)
			return
		}
		io.WriteString(w, `{"price":1.5}`)
	}))
	defer srv.Close()
	nodes.SetURLValidatorForTest(func(string) error { return nil })
	defer nodes.SetURLValidatorForTest(func(string) error { return nil })

	fetch := dn("fetch", models.NodeTypeTool, "http")
	fetch.URL = srv.URL + "/quote/{{state.coin}}"
	g := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{dn("t", models.NodeTypeTrigger, "manual"), fetch, dn("e", models.NodeTypeEnd, "done")},
		Edges: []models.WorkflowEdge{flow("t", "fetch"), flow("fetch", "e")},
	}
	res := DryRun(context.Background(), g, DryRunOptions{State: map[string]any{"coin": "btc"}})
	if res.Failed || res.Empty {
		t.Fatalf("want a clean run with the variable expanded, got %+v (paths %v)", res, paths)
	}
	if len(paths) != 1 || paths[0] != "/quote/btc" {
		t.Fatalf("want exactly one call to /quote/btc, got %v", paths)
	}
}

// Review finding: a test run called platform-key agents with no credit
// check, so a user with no credits could run them for free just by chatting.
func TestDryRunDoesNotRunAPlatformAgentTheUserCannotPayFor(t *testing.T) {
	called := false
	gem := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"hi"}]}}]}`)
	}))
	defer gem.Close()
	nodes.SetGeminiBaseURL(gem.URL)
	defer nodes.SetGeminiBaseURL("https://generativelanguage.googleapis.com")

	provider := dn("p", models.NodeTypeProvider, "gemini")
	provider.KeyMode = "platform"
	g := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{dn("t", models.NodeTypeTrigger, "manual"), dn("a", models.NodeTypeAgent, "agent"), provider, dn("e", models.NodeTypeEnd, "done")},
		Edges: []models.WorkflowEdge{flow("t", "a"), flow("a", "e"),
			{ID: "pa", From: "p", To: "a", Kind: models.EdgeKindAttach, ToPort: "model"}},
	}
	var asked int64
	res := DryRun(context.Background(), g, DryRunOptions{
		PlatformKeys: map[string]string{"gemini": "k"},
		CheckBalance: func(ctx context.Context, amount int64) error {
			asked = amount
			return errors.New("insufficient credits")
		},
	})
	if called {
		t.Fatal("an agent the user cannot pay for must not be called")
	}
	if asked <= 0 {
		t.Fatalf("the check must ask for the agent's real fee, asked for %d", asked)
	}
	if res.Failed || !res.Unverified {
		t.Fatalf("no credits is not a broken workflow: want unverified, got %+v", res)
	}
	if s := stepOf(res, "a"); s.Status != "unverified" || !strings.Contains(s.Reason, "credits") {
		t.Fatalf("the agent step must say it needs credits, got %+v", s)
	}
}

// Review finding: a 401 from a node whose key the user has not pasted yet was
// reported as a failed workflow, so the gate sent the builder round the repair
// loop for a workflow that is correct -- and the builder cannot set the key
// anyway.
func TestDryRunTreatsAMissingCredentialAsUncheckable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"message":"missing api key sk-live-should-not-be-echoed"}`)
	}))
	defer srv.Close()
	nodes.SetURLValidatorForTest(func(string) error { return nil })
	fetch := dn("fetch", models.NodeTypeTool, "http")
	fetch.URL = srv.URL + "/quote"
	g := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{dn("t", models.NodeTypeTrigger, "manual"), fetch, dn("e", models.NodeTypeEnd, "done")},
		Edges: []models.WorkflowEdge{flow("t", "fetch"), flow("fetch", "e")},
	}
	res := DryRun(context.Background(), g, DryRunOptions{})
	if res.Failed {
		t.Fatalf("a missing credential is not a broken workflow: %+v", res)
	}
	if !res.Unverified {
		t.Fatalf("the run must report itself unchecked, got %+v", res)
	}
	s := stepOf(res, "fetch")
	if s.Status != "unverified" || !strings.Contains(s.Reason, "credential") {
		t.Fatalf("want the step marked unverified with a credential reason, got %+v", s)
	}
	if strings.Contains(s.Error, "sk-live") {
		t.Fatalf("the upstream body must not be carried into the result: %q", s.Error)
	}
}

// A read-only connector skips itself when its credential is missing. That is
// not a step that ran: reporting it as one let the builder claim a workflow
// was checked when the only thing it proved was that nothing happened.
func TestDryRunDoesNotCountASkippedConnectorAsRan(t *testing.T) {
	g := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{dn("t", models.NodeTypeTrigger, "manual"), dn("w", models.NodeTypeAction, "openweathermap"), dn("e", models.NodeTypeEnd, "done")},
		Edges: []models.WorkflowEdge{flow("t", "w"), flow("w", "e")},
	}
	res := DryRun(context.Background(), g, DryRunOptions{})
	if res.Failed || !res.Unverified {
		t.Fatalf("want an unverified result, got %+v", res)
	}
	if s := stepOf(res, "w"); s.Status != "unverified" {
		t.Fatalf("want the skipped connector marked unverified, got %+v", s)
	}
}

// Review finding: the balance was checked and never debited, so a single
// credit bought unlimited platform-key agent calls, one build message at a
// time.
func TestDryRunChargesForAPlatformKeyAgent(t *testing.T) {
	gem := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"hi"}]}}]}`)
	}))
	defer gem.Close()
	nodes.SetGeminiBaseURL(gem.URL)
	defer nodes.SetGeminiBaseURL("https://generativelanguage.googleapis.com")

	provider := dn("p", models.NodeTypeProvider, "gemini")
	provider.KeyMode = "platform"
	g := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{dn("t", models.NodeTypeTrigger, "manual"), dn("a", models.NodeTypeAgent, "agent"), provider, dn("e", models.NodeTypeEnd, "done")},
		Edges: []models.WorkflowEdge{flow("t", "a"), flow("a", "e"),
			{ID: "pa", From: "p", To: "a", Kind: models.EdgeKindAttach, ToPort: "model"}},
	}
	var charged int64
	var chargedNode, chargedModel string
	res := DryRun(context.Background(), g, DryRunOptions{
		PlatformKeys: map[string]string{"gemini": "k"},
		CheckBalance: func(ctx context.Context, amount int64) error { return nil },
		ChargeAgent: func(ctx context.Context, nodeID string, amount int64, model string) error {
			charged, chargedNode, chargedModel = amount, nodeID, model
			return nil
		},
	})
	if res.Failed {
		t.Fatalf("unexpected failure: %+v", res)
	}
	if charged <= 0 || chargedNode != "a" || chargedModel == "" {
		t.Fatalf("the agent call must be charged its real fee: amount=%d node=%q model=%q", charged, chargedNode, chargedModel)
	}
}

// A BYOK agent costs the platform nothing, so a test run must not charge for
// one -- the same rule a real run applies.
func TestDryRunDoesNotChargeForAByokAgent(t *testing.T) {
	gem := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"hi"}]}}]}`)
	}))
	defer gem.Close()
	nodes.SetGeminiBaseURL(gem.URL)
	defer nodes.SetGeminiBaseURL("https://generativelanguage.googleapis.com")

	provider := dn("p", models.NodeTypeProvider, "gemini")
	provider.APIKey = "user-key"
	g := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{dn("t", models.NodeTypeTrigger, "manual"), dn("a", models.NodeTypeAgent, "agent"), provider, dn("e", models.NodeTypeEnd, "done")},
		Edges: []models.WorkflowEdge{flow("t", "a"), flow("a", "e"),
			{ID: "pa", From: "p", To: "a", Kind: models.EdgeKindAttach, ToPort: "model"}},
	}
	charged := false
	DryRun(context.Background(), g, DryRunOptions{
		ChargeAgent: func(ctx context.Context, nodeID string, amount int64, model string) error {
			charged = true
			return nil
		},
	})
	if charged {
		t.Fatal("a BYOK agent must not be charged")
	}
}

// Review finding: clipText cut on a byte index, which splits a multi-byte
// rune -- invalid UTF-8 in the payload sent to the model and in the chat.
func TestClipTextKeepsValidUTF8(t *testing.T) {
	long := strings.Repeat("é", dryRunOutputShown)
	got := clipText(long)
	if !utf8.ValidString(got) {
		t.Fatalf("clipped text is not valid UTF-8: %q", got[len(got)-8:])
	}
	if len(got) > dryRunOutputShown+len("…") {
		t.Fatalf("clipped text is longer than the limit: %d bytes", len(got))
	}
}

// Review finding (7374b817): every skipped connector was reported as needing a
// credential. coingecko_skipped_no_ids is a missing setting the builder can
// fill in, and CoinGecko has no key -- it must go back for repair.
func TestDryRunSendsAMissingSettingBackForRepair(t *testing.T) {
	g := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{dn("t", models.NodeTypeTrigger, "manual"), dn("cg", models.NodeTypeAction, "coingecko"), dn("e", models.NodeTypeEnd, "done")},
		Edges: []models.WorkflowEdge{flow("t", "cg"), flow("cg", "e")},
	}
	res := DryRun(context.Background(), g, DryRunOptions{})
	if !res.Failed || res.Unverified {
		t.Fatalf("a missing setting is a fault the builder can fix: want failed, got %+v", res)
	}
	if s := stepOf(res, "cg"); s.Status != "failed" || strings.Contains(s.Reason+s.Error, "credential") {
		t.Fatalf("want a failed step that does not blame a credential, got %+v", s)
	}
}

// Review finding: the credential check ran on the raw error, so a 500 whose
// body happened to mention "API 403" read as a missing key and was hidden
// from repair.
func TestDryRunDoesNotHideARealFailureBehindAnAuthWord(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `upstream API 403 forbidden`)
	}))
	defer srv.Close()
	nodes.SetURLValidatorForTest(func(string) error { return nil })
	fetch := dn("fetch", models.NodeTypeTool, "http")
	fetch.URL = srv.URL
	g := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{dn("t", models.NodeTypeTrigger, "manual"), fetch, dn("e", models.NodeTypeEnd, "done")},
		Edges: []models.WorkflowEdge{flow("t", "fetch"), flow("fetch", "e")},
	}
	res := DryRun(context.Background(), g, DryRunOptions{})
	if !res.Failed || res.Unverified {
		t.Fatalf("a 500 is a real failure whatever its body says: got %+v", res)
	}
}

// Review finding: a revoked platform key surfaced as "the user adds it in the
// Inspector", which the user cannot do. An agent has no credential of its own
// to add, so a 401 from its model call is a real failure.
func TestDryRunReportsAModelKeyRejectionAsAFailure(t *testing.T) {
	gem := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"API key not valid"}}`)
	}))
	defer gem.Close()
	nodes.SetGeminiBaseURL(gem.URL)
	defer nodes.SetGeminiBaseURL("https://generativelanguage.googleapis.com")

	provider := dn("p", models.NodeTypeProvider, "gemini")
	provider.KeyMode = "platform"
	g := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{dn("t", models.NodeTypeTrigger, "manual"), dn("a", models.NodeTypeAgent, "agent"), provider, dn("e", models.NodeTypeEnd, "done")},
		Edges: []models.WorkflowEdge{flow("t", "a"), flow("a", "e"),
			{ID: "pa", From: "p", To: "a", Kind: models.EdgeKindAttach, ToPort: "model"}},
	}
	res := DryRun(context.Background(), g, DryRunOptions{PlatformKeys: map[string]string{"gemini": "revoked"}})
	if !res.Failed {
		t.Fatalf("a rejected model key is not something the user adds in the Inspector: want failed, got %+v", res)
	}
}

// An http node that already carries the user's credential and still gets 401
// is not waiting for a key to be added -- the one it has was rejected. Still
// not the builder's to fix, but it must say so rather than "add a credential".
func TestDryRunNamesARejectedStoredCredential(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	nodes.SetURLValidatorForTest(func(string) error { return nil })
	fetch := dn("fetch", models.NodeTypeTool, "http")
	fetch.URL = srv.URL
	fetch.Secrets = map[string]string{"httpHeadersJSON": `{"Authorization":"Bearer expired"}`}
	g := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{dn("t", models.NodeTypeTrigger, "manual"), fetch, dn("e", models.NodeTypeEnd, "done")},
		Edges: []models.WorkflowEdge{flow("t", "fetch"), flow("fetch", "e")},
	}
	res := DryRun(context.Background(), g, DryRunOptions{})
	if res.Failed || !res.Unverified {
		t.Fatalf("want unverified, got %+v", res)
	}
	if s := stepOf(res, "fetch"); !strings.Contains(s.Reason, "rejected") {
		t.Fatalf("want the reason to say the stored credential was rejected, got %q", s.Reason)
	}
}

// Review finding: the charge ran on the build's own context, which ends at
// the build's time budget. A call that succeeded just before it lost its
// debit to the cancellation -- the model was paid for, the user was not
// charged. The charge must outlive the build's context, as the runner's
// ledger writes do.
func TestDryRunChargeSurvivesTheBuildContextEnding(t *testing.T) {
	gem := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"hi"}]}}]}`)
	}))
	defer gem.Close()
	nodes.SetGeminiBaseURL(gem.URL)
	defer nodes.SetGeminiBaseURL("https://generativelanguage.googleapis.com")

	provider := dn("p", models.NodeTypeProvider, "gemini")
	provider.KeyMode = "platform"
	g := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{dn("t", models.NodeTypeTrigger, "manual"), dn("a", models.NodeTypeAgent, "agent"), provider, dn("e", models.NodeTypeEnd, "done")},
		Edges: []models.WorkflowEdge{flow("t", "a"), flow("a", "e"),
			{ID: "pa", From: "p", To: "a", Kind: models.EdgeKindAttach, ToPort: "model"}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var chargeCtxErr error
	charged := false
	DryRun(ctx, g, DryRunOptions{
		PlatformKeys: map[string]string{"gemini": "k"},
		ChargeAgent: func(cctx context.Context, nodeID string, amount int64, model string) error {
			charged = true
			cancel() // the build's budget runs out right as the charge starts
			chargeCtxErr = cctx.Err()
			return nil
		},
	})
	if !charged {
		t.Fatal("setup: the agent call should have been charged")
	}
	if chargeCtxErr != nil {
		t.Fatalf("the charge must not be cancelled with the build: %v", chargeCtxErr)
	}
}

// Review finding (b797e3c2): a model-call 401 surfaces on the agent, but the
// key lives on the attached provider. For BYOK that key is the user's, pasted
// in the Inspector, so it is exactly the case a test run must report as
// uncheckable rather than send the builder off to "fix" the workflow.
func TestDryRunTreatsARevokedByokKeyAsTheUsersToFix(t *testing.T) {
	gem := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"API key not valid"}}`)
	}))
	defer gem.Close()
	nodes.SetGeminiBaseURL(gem.URL)
	defer nodes.SetGeminiBaseURL("https://generativelanguage.googleapis.com")

	provider := dn("p", models.NodeTypeProvider, "gemini")
	provider.APIKey = "user-key-revoked"
	g := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{dn("t", models.NodeTypeTrigger, "manual"), dn("a", models.NodeTypeAgent, "agent"), provider, dn("e", models.NodeTypeEnd, "done")},
		Edges: []models.WorkflowEdge{flow("t", "a"), flow("a", "e"),
			{ID: "pa", From: "p", To: "a", Kind: models.EdgeKindAttach, ToPort: "model"}},
	}
	res := DryRun(context.Background(), g, DryRunOptions{})
	if res.Failed || !res.Unverified {
		t.Fatalf("the user's own rejected key is not a workflow to fix: want unverified, got %+v", res)
	}
	if s := stepOf(res, "a"); !strings.Contains(s.Reason, "rejected") {
		t.Fatalf("want the step to say the credential was rejected, got %+v", s)
	}
}

// Review finding (eeadb492): for a platform-key agent -> slack the answer was
// read from the agent step's Output, its whole node output as JSON. What the
// send would really carry is the agent's sentence, and DryRun now records
// exactly that.
func TestDryRunRecordsWhatASimulatedSendWouldCarry(t *testing.T) {
	gem := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"BTC is 60000 dollars."}]}}],"usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":8}}`)
	}))
	defer gem.Close()
	nodes.SetGeminiBaseURL(gem.URL)
	defer nodes.SetGeminiBaseURL("https://generativelanguage.googleapis.com")

	provider := dn("p", models.NodeTypeProvider, "gemini")
	provider.KeyMode = "platform"
	g := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{dn("t", models.NodeTypeTrigger, "manual"), dn("a", models.NodeTypeAgent, "agent"), provider, dn("s", models.NodeTypeAction, "slack"), dn("e", models.NodeTypeEnd, "done")},
		Edges: []models.WorkflowEdge{flow("t", "a"), flow("a", "s"), flow("s", "e"),
			{ID: "pa", From: "p", To: "a", Kind: models.EdgeKindAttach, ToPort: "model"}},
	}
	res := DryRun(context.Background(), g, DryRunOptions{PlatformKeys: map[string]string{"gemini": "k"}})
	if !res.FinalSimulated {
		t.Fatalf("the run ended on a simulated send: %+v", res)
	}
	if res.WouldSend != "BTC is 60000 dollars." {
		t.Fatalf("want the agent's sentence as what slack would send, got %q", res.WouldSend)
	}
}

// Review finding (eeadb492): the "last real step" was picked from the Steps
// list, which interleaves branches and ignores message templates. The send's
// own resolved message is the truth: here it pulls one field from a fetch that
// is not the step just before it.
func TestDryRunWouldSendFollowsTheSendsOwnTemplate(t *testing.T) {
	srv := dataServer(t)
	fetch := dn("fetch", models.NodeTypeTool, "http")
	fetch.URL = srv.URL + "/quote"
	note := dn("note", models.NodeTypeTool, "json_extract")
	note.Config = map[string]string{"jsonPath": "data"}
	send := dn("s", models.NodeTypeAction, "slack")
	send.Config = map[string]string{"messageTemplate": "Price: {{ node.fetch.data.price }}"}
	g := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{dn("t", models.NodeTypeTrigger, "manual"), fetch, note, send, dn("e", models.NodeTypeEnd, "done")},
		Edges: []models.WorkflowEdge{flow("t", "fetch"), flow("fetch", "note"), flow("note", "s"), flow("s", "e")},
	}
	res := DryRun(context.Background(), g, DryRunOptions{})
	if res.Failed {
		t.Fatalf("unexpected failure: %+v", res)
	}
	if res.WouldSend != "Price: 1.5" {
		t.Fatalf("want the send's own resolved message, got %q", res.WouldSend)
	}
}

// A run that ends on a simulated step with no message of its own -- a state
// write -- has only a placeholder as its final output. It carries nothing: a
// state write stores a value, it does not send the fetched data anywhere.
func TestDryRunFlagsAFinalSimulatedStepWithNothingToSend(t *testing.T) {
	srv := dataServer(t)
	fetch := dn("fetch", models.NodeTypeTool, "http")
	fetch.URL = srv.URL + "/quote"
	g := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{dn("t", models.NodeTypeTrigger, "manual"), fetch, dn("st", models.NodeTypeState, "set"), dn("e", models.NodeTypeEnd, "done")},
		Edges: []models.WorkflowEdge{flow("t", "fetch"), flow("fetch", "st"), flow("st", "e")},
	}
	res := DryRun(context.Background(), g, DryRunOptions{})
	if !res.FinalSimulated || res.WouldSend != "" || res.Empty {
		t.Fatalf("want FinalSimulated with no carry and no empty flag, got FinalSimulated=%v WouldSend=%q Empty=%v", res.FinalSimulated, res.WouldSend, res.Empty)
	}
}

// Review finding (e6622c49): a send whose template resolves to nothing -- here
// a field the fetched data does not have -- recorded WouldSend "" and the run
// was not flagged, so the builder called a workflow that posts an empty
// message a working one.
func TestDryRunFlagsASendThatWouldCarryNothing(t *testing.T) {
	srv := dataServer(t)
	fetch := dn("fetch", models.NodeTypeTool, "http")
	fetch.URL = srv.URL + "/quote"
	send := dn("s", models.NodeTypeAction, "slack")
	send.Config = map[string]string{"messageTemplate": "{{ result.summary }}"}
	g := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{dn("t", models.NodeTypeTrigger, "manual"), fetch, send, dn("e", models.NodeTypeEnd, "done")},
		Edges: []models.WorkflowEdge{flow("t", "fetch"), flow("fetch", "s"), flow("s", "e")},
	}
	res := DryRun(context.Background(), g, DryRunOptions{})
	if !res.FinalSimulated {
		t.Fatalf("setup: the run should end on the simulated send: %+v", res)
	}
	if !res.Empty {
		t.Fatalf("a send that would carry an empty message must flag the run as empty, got WouldSend=%q Empty=%v", res.WouldSend, res.Empty)
	}
}

// Review finding (e6622c49): a simulated step that sends no message of its own
// (an HTTP POST, x402, Tendril, a state write) recorded nothing, and the
// builder fell back to the agent's reply from anywhere in the run. What such a
// step would carry is its input -- here the extracted JSON the POST would send.
func TestDryRunRecordsWhatAFinalNonMessageStepWouldReceive(t *testing.T) {
	srv := dataServer(t)
	fetch := dn("fetch", models.NodeTypeTool, "http")
	fetch.URL = srv.URL + "/quote"
	extract := dn("x", models.NodeTypeTool, "json_extract")
	extract.Config = map[string]string{"jsonPath": "data"}
	post := dn("post", models.NodeTypeTool, "http")
	post.Method = "POST"
	post.URL = srv.URL + "/hook"
	g := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{dn("t", models.NodeTypeTrigger, "manual"), fetch, extract, post, dn("e", models.NodeTypeEnd, "done")},
		Edges: []models.WorkflowEdge{flow("t", "fetch"), flow("fetch", "x"), flow("x", "post"), flow("post", "e")},
	}
	res := DryRun(context.Background(), g, DryRunOptions{})
	if !res.FinalSimulated {
		t.Fatalf("setup: the run should end on the simulated POST: %+v", res)
	}
	if !strings.Contains(res.WouldSend, `"price":1.5`) {
		t.Fatalf("want what the POST would receive (the extracted JSON), got %q", res.WouldSend)
	}
}

// Review finding (89c67c1a): every simulated step was judged by its input, so
// a correct workflow ending on a step that never uses its input was flagged
// "would have sent an empty message" and sent back for repair. With a manual
// trigger that input is empty. None of these is a broken workflow.
func TestDryRunDoesNotFlagStepsThatNeverSendTheirInput(t *testing.T) {
	put := dn("put", models.NodeTypeTool, "http")
	put.Method = "PUT"
	put.URL = "https://example.invalid/resource"
	cases := []struct {
		name string
		node models.WorkflowNode
	}{
		{"state get", dn("st", models.NodeTypeState, "get")},
		{"google sheets_read", dn("g", models.NodeTypeGoogle, "sheets_read")},
		{"http PUT with no body template", put},
		{"calendly read", dn("c", models.NodeTypeAction, "calendly")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := models.WorkflowGraph{
				Nodes: []models.WorkflowNode{dn("t", models.NodeTypeTrigger, "manual"), c.node, dn("e", models.NodeTypeEnd, "done")},
				Edges: []models.WorkflowEdge{flow("t", c.node.ID), flow(c.node.ID, "e")},
			}
			res := DryRun(context.Background(), g, DryRunOptions{})
			if !res.FinalSimulated {
				t.Fatalf("setup: the run should end on the simulated step: %+v", res)
			}
			if res.Empty || res.Failed {
				t.Fatalf("a step that never sends its input is not an empty send: Empty=%v Failed=%v Error=%q", res.Empty, res.Failed, res.Error)
			}
		})
	}
}

// Review findings (eeadb492..3fad251d): what a simulated step "would carry"
// was worked out by hand and drifted from the real connectors. Each case is a
// correct workflow and the exact thing the real connector would send.
func TestDryRunCarryMatchesWhatTheRealConnectorSends(t *testing.T) {
	srv := dataServer(t)
	fetch := func() models.WorkflowNode {
		f := dn("fetch", models.NodeTypeTool, "http")
		f.URL = srv.URL + "/quote"
		return f
	}
	extract := dn("x", models.NodeTypeTool, "json_extract")
	extract.Config = map[string]string{"jsonPath": "data"}

	// A body template reads the step's input, not its own placeholder.
	postTmpl := dn("post", models.NodeTypeTool, "http")
	postTmpl.Method = "POST"
	postTmpl.URL = srv.URL + "/hook"
	postTmpl.Config = map[string]string{"httpBodyTemplate": "{{ result.price }}"}

	// callHTTP compares the no-template POST case-sensitively: "post" sends no body.
	lowerPost := dn("post", models.NodeTypeTool, "http")
	lowerPost.Method = "post"
	lowerPost.URL = srv.URL + "/hook"

	// HEAD never carries a body, whatever template is left on the node.
	head := dn("post", models.NodeTypeTool, "http")
	head.Method = "HEAD"
	head.URL = srv.URL + "/hook"
	head.Config = map[string]string{"httpBodyTemplate": "{{ result.missing }}"}

	// calendar_create uses calendarSummary when it is set, not the message.
	cal := dn("post", models.NodeTypeGoogle, "calendar_create")
	cal.Config = map[string]string{"calendarSummary": "Standup", "messageTemplate": "{{ result.missing }}"}

	cases := []struct {
		name      string
		steps     []models.WorkflowNode
		wouldSend string
	}{
		{"POST body template reads the upstream output", []models.WorkflowNode{fetch(), extract, postTmpl}, "1.5"},
		{"lowercase post with no template sends nothing", []models.WorkflowNode{fetch(), lowerPost}, ""},
		{"HEAD ignores a leftover body template", []models.WorkflowNode{fetch(), head}, ""},
		{"calendar_create sends its summary", []models.WorkflowNode{fetch(), cal}, "Standup"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			nodesIn := append([]models.WorkflowNode{dn("t", models.NodeTypeTrigger, "manual")}, c.steps...)
			nodesIn = append(nodesIn, dn("e", models.NodeTypeEnd, "done"))
			var edges []models.WorkflowEdge
			for i := 0; i+1 < len(nodesIn); i++ {
				edges = append(edges, flow(nodesIn[i].ID, nodesIn[i+1].ID))
			}
			res := DryRun(context.Background(), models.WorkflowGraph{Nodes: nodesIn, Edges: edges}, DryRunOptions{})
			if !res.FinalSimulated {
				t.Fatalf("setup: the run should end on the simulated step: %+v", res)
			}
			if res.Empty || res.Failed {
				t.Fatalf("a correct workflow must not be flagged: Empty=%v Failed=%v Error=%q", res.Empty, res.Failed, res.Error)
			}
			if res.WouldSend != c.wouldSend {
				t.Fatalf("want WouldSend %q (what the real connector sends), got %q", c.wouldSend, res.WouldSend)
			}
		})
	}
}

// An agent fed only by a paid tool has nothing to answer with in a test.
// Reported as failed, it sent the builder to "fix" a correct workflow.
func TestDryRunDoesNotBlameAnAgentForAWithheldPaidTool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{
				{"finishReason": "STOP", "content": map[string]any{"role": "model"}},
			},
		})
	}))
	defer srv.Close()
	nodes.SetGeminiBaseURL(srv.URL)
	defer nodes.SetGeminiBaseURL("https://generativelanguage.googleapis.com")

	graph := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{
			{ID: "t", Type: models.NodeTypeTrigger, Template: "manual", Name: "Start"},
			{ID: "a", Type: models.NodeTypeAgent, Template: "agent", Name: "Explain Price"},
			{ID: "p", Type: models.NodeTypeProvider, Template: "gemini", Name: "Gemini", KeyMode: "platform"},
			{ID: "x", Type: models.NodeTypeTool402, Name: "Paid Prices", Endpoint: "https://api.example.com/x402/prices"},
		},
		Edges: []models.WorkflowEdge{
			{ID: "e1", From: "t", To: "a", Kind: models.EdgeKindFlow, ToPort: "in"},
			{ID: "e2", From: "p", To: "a", Kind: models.EdgeKindAttach, ToPort: "model"},
			{ID: "e3", From: "x", To: "a", Kind: models.EdgeKindAttach, ToPort: "tools"},
		},
	}
	res := DryRun(context.Background(), graph, DryRunOptions{
		PlatformKeys: map[string]string{"gemini": "k"},
	})
	if res.Failed {
		t.Errorf("the workflow is not at fault: %+v", res.Steps)
	}
	if !res.Unverified {
		t.Error("an agent that could not call its paid tool must read as unverified")
	}
	var agent nodes.DryRunStep
	for _, s := range res.Steps {
		if s.NodeID == "a" {
			agent = s
		}
	}
	if agent.Status != "unverified" {
		t.Errorf("agent step status = %q, want unverified (%s)", agent.Status, agent.Reason)
	}
	if !strings.Contains(agent.Reason, "Paid Prices") {
		t.Errorf("the reason should name the tool that was withheld, got %q", agent.Reason)
	}
}

// A withheld tool excuses an agent that had nothing to say -- not an agent
// whose model call was refused. IsEmptyOutput is true on every error path,
// so without the err check a 429, a bad model name or a rejected platform
// key all came back "unverified" and the builder never repaired them.
func TestDryRunStillBlamesARealAgentFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"quota exceeded"}}`))
	}))
	defer srv.Close()
	nodes.SetGeminiBaseURL(srv.URL)
	defer nodes.SetGeminiBaseURL("https://generativelanguage.googleapis.com")

	graph := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{
			{ID: "t", Type: models.NodeTypeTrigger, Template: "manual", Name: "Start"},
			{ID: "a", Type: models.NodeTypeAgent, Template: "agent", Name: "Explain"},
			{ID: "p", Type: models.NodeTypeProvider, Template: "gemini", Name: "Gemini", KeyMode: "platform"},
			{ID: "x", Type: models.NodeTypeTool402, Name: "Paid Prices", Endpoint: "https://api.example.com/x402/prices"},
		},
		Edges: []models.WorkflowEdge{
			{ID: "e1", From: "t", To: "a", Kind: models.EdgeKindFlow, ToPort: "in"},
			{ID: "e2", From: "p", To: "a", Kind: models.EdgeKindAttach, ToPort: "model"},
			{ID: "e3", From: "x", To: "a", Kind: models.EdgeKindAttach, ToPort: "tools"},
		},
	}
	res := DryRun(context.Background(), graph, DryRunOptions{
		PlatformKeys: map[string]string{"gemini": "k"},
	})
	if !res.Failed {
		t.Fatalf("a refused model call is the workflow's problem to fix: %+v", res.Steps)
	}
}

// The model call is billed to the platform key the moment it returns, so it
// must be charged however the result is later classified. A withheld tool
// used to return before the charge, making every test run of such a
// workflow free.
func TestDryRunChargesAnEmptyAnswerWithAWithheldTool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{
				{"content": map[string]any{"parts": []map[string]any{{"text": ""}}}},
			},
		})
	}))
	defer srv.Close()
	nodes.SetGeminiBaseURL(srv.URL)
	defer nodes.SetGeminiBaseURL("https://generativelanguage.googleapis.com")

	graph := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{
			{ID: "t", Type: models.NodeTypeTrigger, Template: "manual", Name: "Start"},
			{ID: "a", Type: models.NodeTypeAgent, Template: "agent", Name: "Explain"},
			{ID: "p", Type: models.NodeTypeProvider, Template: "gemini", Name: "Gemini", KeyMode: "platform"},
			{ID: "x", Type: models.NodeTypeTool402, Name: "Paid Prices", Endpoint: "https://api.example.com/x402/prices"},
		},
		Edges: []models.WorkflowEdge{
			{ID: "e1", From: "t", To: "a", Kind: models.EdgeKindFlow, ToPort: "in"},
			{ID: "e2", From: "p", To: "a", Kind: models.EdgeKindAttach, ToPort: "model"},
			{ID: "e3", From: "x", To: "a", Kind: models.EdgeKindAttach, ToPort: "tools"},
		},
	}
	var charged int64
	res := DryRun(context.Background(), graph, DryRunOptions{
		PlatformKeys: map[string]string{"gemini": "k"},
		ChargeAgent: func(ctx context.Context, nodeID string, amount int64, model string) error {
			charged += amount
			return nil
		},
	})
	if !res.Unverified {
		t.Fatalf("the withheld tool still explains the empty answer: %+v", res.Steps)
	}
	if charged == 0 {
		t.Error("the model call happened and was billed, so it must be charged")
	}
}

// TestDryRunContinuesPastADegradableFailure: at run time a failed read step
// degrades and the run carries on (nodes.IsDegradable), so a test run that
// stopped at the first one would judge the workflow by behaviour a real run
// no longer has -- and would never show the builder the answer the user
// actually gets.
//
// It still counts as a fault. A test run happens while the workflow is being
// built, where a failing source is usually a wrong id, a wrong path or a dead
// API that the builder can fix; degrading silently there would ship exactly
// the broken workflow the test gate exists to catch.
func TestDryRunContinuesPastADegradableFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	g := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{
			dn("t", models.NodeTypeTrigger, "manual"),
			{ID: "h", Type: models.NodeTypeTool, Template: "http", Name: "h", URL: srv.URL, Method: "GET"},
			dn("e", models.NodeTypeEnd, "done"),
		},
		Edges: []models.WorkflowEdge{flow("t", "h"), flow("h", "e")},
	}
	res := DryRun(context.Background(), g, DryRunOptions{})

	if !res.Degraded {
		t.Errorf("Degraded = false, want true: a read step failed and the run carried on")
	}
	if !res.Failed {
		t.Errorf("Failed = false, want true: a failing source during a build is a fault to fix")
	}
	if s := stepOf(res, "h"); s.Status != "failed" {
		t.Errorf("step status = %q, want %q", s.Status, "failed")
	}
	if s := stepOf(res, "e"); s.Status == "" {
		t.Error("the test run stopped at the failed step instead of carrying on to the end")
	}
	// The other side of the contract -- a failure that still ends a test run
	// -- is TestDryRunReportsAModelKeyRejectionAsAFailure above: a dry run
	// simulates every real action, so an agent's is the failure left that can
	// end one.
}
