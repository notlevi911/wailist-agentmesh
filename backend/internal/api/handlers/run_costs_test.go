package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentmesh/backend/internal/api/handlers"
	"github.com/agentmesh/backend/internal/db"
	"github.com/agentmesh/backend/internal/models"
)

// fundForRunCosts tops a user up to micros through the real top-up path, the
// same way the engine and db tests fund a user before debiting.
func fundForRunCosts(t *testing.T, store *db.Store, userID string, micros int64) {
	t.Helper()
	ctx := context.Background()
	orderID := fmt.Sprintf("fund_runcosts_%s_%d", userID, time.Now().UnixNano())
	if _, err := store.CreateCreditTransaction(ctx, userID, orderID, 100, float64(micros)/1e6); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CompleteCreditTransaction(ctx, "cashfree", orderID, "pay_"+orderID); err != nil {
		t.Fatal(err)
	}
}

func getRunReq(d *handlers.Deps, runID, userID string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := withURLParam(httptest.NewRequest(http.MethodGet, "/runs/"+runID, nil), "runId", runID)
	d.GetRun(rec, withUser(req, userID))
	return rec
}

// TestGetRunReturnsCostsFromDebitLedger is #111's backend half: GET
// /runs/{runId} carries what the run was charged, per step and in total,
// straight from debit_ledger -- including non-x402 charges that no step
// output ever reported.
func TestGetRunReturnsCostsFromDebitLedger(t *testing.T) {
	d := testDeps(t)
	ctx := context.Background()

	user, err := d.Store.CreateUser(ctx, "run-costs-"+randSuffix(t)+"@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	fundForRunCosts(t, d.Store, user.ID, 5_000_000)
	wf, err := d.Store.CreateWorkflow(ctx, "Run Costs", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Store.DeleteWorkflow(context.Background(), wf.ID) })
	run, err := d.Store.CreateRun(ctx, wf.ID, "test", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}

	charges := []struct {
		node, kind string
		amount     int64
	}{
		{"agent1", models.DebitKindPlatformKeyLLMFee, 90_000},
		{"x402", models.DebitKindX402RelayCost, 400_000},
		{"x402", models.DebitKindX402PlatformFee, 1_500_000},
		{"tool1", models.DebitKindByokFlatFee, 500_000},
	}
	for _, c := range charges {
		if err := d.Store.DebitCredits(ctx, user.ID, c.amount, c.kind, wf.ID, run.ID, c.node); err != nil {
			t.Fatal(err)
		}
	}

	rec := getRunReq(d, run.ID, user.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET run got %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Costs models.RunCosts `json:"costs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}

	if body.Costs.TotalUSDMicros != 2_490_000 {
		t.Fatalf("totalUsdMicros = %d, want 2490000", body.Costs.TotalUSDMicros)
	}
	if len(body.Costs.Steps) != 3 {
		t.Fatalf("got %d steps, want 3: %+v", len(body.Costs.Steps), body.Costs.Steps)
	}
	x402 := body.Costs.Steps[1]
	if x402.NodeID != "x402" || x402.TotalUSDMicros != 1_900_000 ||
		x402.ByKind[models.DebitKindX402RelayCost] != 400_000 || x402.ByKind[models.DebitKindX402PlatformFee] != 1_500_000 {
		t.Fatalf("x402 step = %+v, want 1900000 split into relay cost and platform fee", x402)
	}
	if agent := body.Costs.Steps[0]; agent.NodeID != "agent1" || agent.TotalUSDMicros != 90_000 {
		t.Fatalf("agent step = %+v, want agent1 charged 90000", agent)
	}
}

// A run that was charged nothing still carries a costs object, with a zero
// total and an empty (not null) step list.
func TestGetRunCostsForUnchargedRun(t *testing.T) {
	d := testDeps(t)
	ctx := context.Background()

	user, err := d.Store.CreateUser(ctx, "run-costs-free-"+randSuffix(t)+"@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	wf, err := d.Store.CreateWorkflow(ctx, "Free Run", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Store.DeleteWorkflow(context.Background(), wf.ID) })
	run, err := d.Store.CreateRun(ctx, wf.ID, "test", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}

	rec := getRunReq(d, run.ID, user.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET run got %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if got := string(body["costs"]); got != `{"totalUsdMicros":0,"steps":[]}` {
		t.Fatalf("costs = %s, want a zero total and empty steps", got)
	}
}
