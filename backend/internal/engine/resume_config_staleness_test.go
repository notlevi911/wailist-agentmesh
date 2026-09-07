package engine

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/agentmesh/backend/internal/db"
	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/sse"
)

// newConfigStalenessTestRunner is a package-local twin of engine_test's
// newTestRunner (unreachable from here, a white-box test file) -- same
// setup, just usable from package engine so nodeConfigHash/
// nodeMayHaveRealSideEffect (both unexported) can be called directly.
func newConfigStalenessTestRunner(t *testing.T) (*Runner, *db.Store) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	store, err := db.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	broker := sse.NewBroker()
	return NewRunner(store, broker, &fakeUSDCSignerForLedgerTest{}, "http://localhost:8080", "", "", X402Config{USDCAssetID: 10458941}), store
}

// TestResumeReExecutesPureComputeNodeWhenConfigChanged is a regression test
// for a review finding: Resume used to skip ANY already-succeeded node
// unconditionally, even one whose configuration had since been edited --
// silently re-feeding downstream steps its OLD, now-stale output instead of
// recomputing it under the new config. For a node type with no real side
// effect (Tool "calc" here), editing it between a dead-letter and a Resume
// must make it re-execute with the new config, not replay the old result.
func TestResumeReExecutesPureComputeNodeWhenConfigChanged(t *testing.T) {
	runner, store := newConfigStalenessTestRunner(t)
	ctx := context.Background()

	wf, err := store.CreateWorkflow(ctx, "Config Staleness Recompute Test", fmt.Sprintf("user-%d", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })

	n2Old := models.WorkflowNode{ID: "n2", Type: models.NodeTypeTool, Template: "calc", URL: "1+1"}
	graphOld := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{
			{ID: "n1", Type: models.NodeTypeTrigger},
			n2Old,
			{ID: "n3", Type: models.NodeTypeEnd},
		},
		Edges: []models.WorkflowEdge{
			{ID: "e1", From: "n1", To: "n2", Kind: models.EdgeKindFlow},
			{ID: "e2", From: "n2", To: "n3", Kind: models.EdgeKindFlow},
		},
	}
	wf, _ = store.UpdateWorkflow(ctx, wf.ID, wf.Name, graphOld)

	run, err := store.CreateRun(ctx, wf.ID, "test", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}

	// Seed n2 as already succeeded under the OLD config ("1+1" -> 2), with
	// its real config hash from that attempt -- exactly what a genuine
	// prior success would have recorded.
	seedEntry, err := store.InsertRunLog(ctx, models.RunLog{RunID: run.ID, NodeID: "n2", NodeType: models.NodeTypeTool, Status: models.LogStatusRunning})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateRunLog(ctx, seedEntry.ID, models.LogStatusSuccess, []byte("2"), 1, nodeConfigHash(n2Old)); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishRun(ctx, run.ID, models.RunStatusFailed); err != nil {
		t.Fatal(err)
	}

	// The user edits n2's expression before resuming.
	n2New := models.WorkflowNode{ID: "n2", Type: models.NodeTypeTool, Template: "calc", URL: "2+2"}
	graphNew := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{
			{ID: "n1", Type: models.NodeTypeTrigger},
			n2New,
			{ID: "n3", Type: models.NodeTypeEnd},
		},
		Edges: graphOld.Edges,
	}
	wf, err = store.UpdateWorkflow(ctx, wf.ID, wf.Name, graphNew)
	if err != nil {
		t.Fatal(err)
	}

	broker := sse.NewBroker()
	broker.Create(run.ID)
	runner.Resume(ctx, wf, run, 0, false)

	finalRun, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finalRun.Status != models.RunStatusSuccess {
		t.Fatalf("run status = %s, want success", finalRun.Status)
	}

	logs, err := store.GetRunLogs(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var n2Count int
	var n2LatestOutput any
	for _, l := range logs {
		if l.NodeID == "n2" {
			n2Count++
			n2LatestOutput = l.Output
		}
	}
	if n2Count != 2 {
		t.Fatalf("n2 has %d run_logs rows, want exactly 2 (the stale seed + a real re-execution under the new config) -- config change was not detected", n2Count)
	}
	// calc's real result for "2+2" is 4 -- confirms n2 was actually
	// recomputed under the NEW config, not merely re-logged with the old
	// (2) value.
	if fmt.Sprint(n2LatestOutput) != "4" {
		t.Fatalf("n2's latest output = %v, want 4 (the real result of the edited \"2+2\" expression)", n2LatestOutput)
	}
}

// TestResumeSkipsSideEffectNodeEvenWhenConfigChanged is the other half of
// the same fix: a node type that can move real money or trigger a real
// external effect (an Action/connector node here) must keep being skipped
// unconditionally on resume, even if its config changed since its prior
// success -- re-executing it under new config could otherwise repeat a
// real send under different settings. A user who wants an edited
// payment-adjacent node to actually take effect needs a fresh Run.
func TestResumeSkipsSideEffectNodeEvenWhenConfigChanged(t *testing.T) {
	runner, store := newConfigStalenessTestRunner(t)
	ctx := context.Background()

	wf, err := store.CreateWorkflow(ctx, "Config Staleness SideEffect Test", fmt.Sprintf("user-%d", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })

	n2Old := models.WorkflowNode{ID: "n2", Type: models.NodeTypeAction, Template: "webhook", URL: "https://old.example.com/hook"}
	if !nodeMayHaveRealSideEffect(n2Old.Type, n2Old.Template, n2Old.Method) {
		t.Fatal("test fixture assumption broken: NodeTypeAction must count as a real-side-effect type")
	}
	graphOld := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{
			{ID: "n1", Type: models.NodeTypeTrigger},
			n2Old,
			{ID: "n3", Type: models.NodeTypeEnd},
		},
		Edges: []models.WorkflowEdge{
			{ID: "e1", From: "n1", To: "n2", Kind: models.EdgeKindFlow},
			{ID: "e2", From: "n2", To: "n3", Kind: models.EdgeKindFlow},
		},
	}
	wf, _ = store.UpdateWorkflow(ctx, wf.ID, wf.Name, graphOld)

	run, err := store.CreateRun(ctx, wf.ID, "test", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}

	seedEntry, err := store.InsertRunLog(ctx, models.RunLog{RunID: run.ID, NodeID: "n2", NodeType: models.NodeTypeAction, Status: models.LogStatusRunning})
	if err != nil {
		t.Fatal(err)
	}
	seedOutput := []byte(`{"status":200}`)
	if err := store.UpdateRunLog(ctx, seedEntry.ID, models.LogStatusSuccess, seedOutput, 1, nodeConfigHash(n2Old)); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishRun(ctx, run.ID, models.RunStatusFailed); err != nil {
		t.Fatal(err)
	}

	// Edit n2's webhook URL -- if this were re-executed, it would try to
	// hit a URL that doesn't exist in this test (and, in a real deploy,
	// could send a duplicate notification to a DIFFERENT endpoint).
	n2New := models.WorkflowNode{ID: "n2", Type: models.NodeTypeAction, Template: "webhook", URL: "https://new.example.com/hook"}
	graphNew := models.WorkflowGraph{
		Nodes: []models.WorkflowNode{
			{ID: "n1", Type: models.NodeTypeTrigger},
			n2New,
			{ID: "n3", Type: models.NodeTypeEnd},
		},
		Edges: graphOld.Edges,
	}
	wf, err = store.UpdateWorkflow(ctx, wf.ID, wf.Name, graphNew)
	if err != nil {
		t.Fatal(err)
	}

	broker := sse.NewBroker()
	broker.Create(run.ID)
	runner.Resume(ctx, wf, run, 0, false)

	finalRun, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finalRun.Status != models.RunStatusSuccess {
		t.Fatalf("run status = %s, want success", finalRun.Status)
	}

	logs, err := store.GetRunLogs(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var n2Count int
	for _, l := range logs {
		if l.NodeID == "n2" {
			n2Count++
		}
	}
	if n2Count != 1 {
		t.Fatalf("n2 has %d run_logs rows, want exactly 1 (the seed) -- a side-effect node must never re-execute on resume, config change or not", n2Count)
	}
}

// nodeConfigHashExcludedFields lists every models.WorkflowNode field
// nodeConfigHash deliberately leaves OUT of its hash, with the reason --
// see nodeConfigHash's own doc comment for the full explanation of each
// category. Kept here, not just implicit in nodeConfigHash's hand-copied
// field list, specifically so TestNodeConfigHashAccountsForEveryField
// below can enforce that EVERY WorkflowNode field is accounted for one
// way or the other, catching the exact class of gap a review flagged:
// nodeConfigHash's allowlist has no compiler or test-enforced link to
// WorkflowNode's actual field list, so a future field added to one
// without the other fails silently -- a config edit to it would never be
// detected as a change, and Resume would keep reusing stale output
// forever with no error.
var nodeConfigHashExcludedFields = map[string]string{
	"ID":                "identity, not config",
	"X":                 "canvas position, cosmetic",
	"Y":                 "canvas position, cosmetic",
	"Name":              "cosmetic label",
	"Label":             "cosmetic label",
	"Icon":              "cosmetic",
	"Description":       "cosmetic",
	"APIKey":            "encrypted at rest with a fresh nonce per save -- ciphertext changes every save regardless of plaintext",
	"EmailAPIKey":       "encrypted at rest with a fresh nonce per save -- ciphertext changes every save regardless of plaintext",
	"Secrets":           "encrypted at rest with a fresh nonce per save -- ciphertext changes every save regardless of plaintext",
	"TendrilLeaseToken": "never persisted (json:\"-\"), always zero-value when loaded from the DB",
	"MaxRetries":        "retry POLICY, not what the node does",
	"RetryBackoffMs":    "retry POLICY, not what the node does",
}

// TestNodeConfigHashAccountsForEveryField is a regression test for a
// review finding: nodeConfigHash hashes a hand-picked subset of
// WorkflowNode's fields with nothing enforcing that list stays in sync
// with the struct. Reflects over every field models.WorkflowNode actually
// has and fails loudly if one is neither in nodeConfigHash's own included
// set (confirmed by asserting changing it changes the hash) nor in
// nodeConfigHashExcludedFields above -- so a future field added to
// WorkflowNode without a matching decision here breaks this test instead
// of silently never being checked for staleness.
func TestNodeConfigHashAccountsForEveryField(t *testing.T) {
	base := models.WorkflowNode{ID: "n1", Type: models.NodeTypeTool, Template: "http"}
	baseHash := nodeConfigHash(base)

	nodeType := reflect.TypeOf(models.WorkflowNode{})
	for i := 0; i < nodeType.NumField(); i++ {
		field := nodeType.Field(i)
		name := field.Name
		if reason, excluded := nodeConfigHashExcludedFields[name]; excluded {
			t.Logf("%s: excluded from nodeConfigHash (%s)", name, reason)
			continue
		}

		// Not excluded -- prove it's actually INCLUDED by changing it and
		// confirming the hash changes. Uses reflection to set a
		// distinguishable non-zero value generically across every
		// remaining field's type (string, float64, map[string]string,
		// []models.ParamDef, []models.CustomParam, models.NodeType) rather
		// than hand-writing one assertion per field.
		modified := base
		mv := reflect.ValueOf(&modified).Elem().FieldByName(name)
		switch mv.Kind() {
		case reflect.String:
			mv.SetString("changed-value-for-" + name)
		case reflect.Float64:
			mv.SetFloat(12345)
		case reflect.Map:
			mv.Set(reflect.ValueOf(map[string]string{"k": "v"}))
		case reflect.Slice:
			switch name {
			case "DiscoveredParams":
				mv.Set(reflect.ValueOf([]models.ParamDef{{Name: "p"}}))
			case "CustomParams":
				mv.Set(reflect.ValueOf([]models.CustomParam{{Name: "p"}}))
			default:
				t.Fatalf("field %s: unhandled slice type %s -- add a case for it here, then decide whether nodeConfigHash should include or exclude it", name, field.Type)
			}
		default:
			t.Fatalf("field %s: unhandled kind %s -- add a case for it here, then decide whether nodeConfigHash should include or exclude it", name, mv.Kind())
		}

		if got := nodeConfigHash(modified); got == baseHash {
			t.Errorf("field %s: changing it did not change nodeConfigHash's result -- it's missing from the hashed field list, so an edit to it will never be detected as a config change on Resume", name)
		}
	}
}
