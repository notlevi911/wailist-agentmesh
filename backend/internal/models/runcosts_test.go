package models_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/agentmesh/backend/internal/models"
)

func TestSummarizeRunCostsGroupsByStepInFirstChargeOrder(t *testing.T) {
	entries := []models.DebitEntry{
		{NodeID: "agent1", Kind: models.DebitKindPlatformKeyLLMFee, AmountUSDMicros: 90_000},
		{NodeID: "x402", Kind: models.DebitKindX402RelayCost, AmountUSDMicros: 400_000},
		{NodeID: "tool1", Kind: models.DebitKindByokFlatFee, AmountUSDMicros: 500_000},
		// A paid x402 call's second row, written after tool1's charge.
		{NodeID: "x402", Kind: models.DebitKindX402PlatformFee, AmountUSDMicros: 1_500_000},
	}

	got := models.SummarizeRunCosts(entries)

	if got.TotalUSDMicros != 2_490_000 {
		t.Fatalf("TotalUSDMicros = %d, want 2490000", got.TotalUSDMicros)
	}
	want := []models.StepCost{
		{NodeID: "agent1", TotalUSDMicros: 90_000, ByKind: map[string]int64{models.DebitKindPlatformKeyLLMFee: 90_000}},
		{NodeID: "x402", TotalUSDMicros: 1_900_000, ByKind: map[string]int64{
			models.DebitKindX402RelayCost:   400_000,
			models.DebitKindX402PlatformFee: 1_500_000,
		}},
		{NodeID: "tool1", TotalUSDMicros: 500_000, ByKind: map[string]int64{models.DebitKindByokFlatFee: 500_000}},
	}
	if !reflect.DeepEqual(got.Steps, want) {
		t.Fatalf("Steps = %+v\nwant   %+v", got.Steps, want)
	}
}

func TestSummarizeRunCostsSumsRepeatedChargesOfOneKind(t *testing.T) {
	// An agent calling the same attached tool twice in one turn.
	got := models.SummarizeRunCosts([]models.DebitEntry{
		{NodeID: "tool1", Kind: models.DebitKindByokFlatFee, AmountUSDMicros: 500_000},
		{NodeID: "tool1", Kind: models.DebitKindByokFlatFee, AmountUSDMicros: 500_000},
	})
	if len(got.Steps) != 1 || got.Steps[0].ByKind[models.DebitKindByokFlatFee] != 1_000_000 || got.TotalUSDMicros != 1_000_000 {
		t.Fatalf("got %+v, want one step charged 1000000 of byok_flat_fee", got)
	}
}

// A run charged nothing must encode steps as [], not null, so a client can
// iterate it without a nil check.
func TestSummarizeRunCostsEmptyRunEncodesEmptySteps(t *testing.T) {
	b, err := json.Marshal(models.SummarizeRunCosts(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"totalUsdMicros":0,"steps":[]}` {
		t.Fatalf("encoded %s", b)
	}
}
