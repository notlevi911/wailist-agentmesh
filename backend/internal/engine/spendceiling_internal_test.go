package engine

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentmesh/backend/internal/models"
)

func TestCallSpendLimitBeforeReservationAndExecution(t *testing.T) {
	for _, mode := range []string{"blocked", "allowed", "unset", "settings_error"} {
		t.Run(mode, func(t *testing.T) {
			runner, store, wf, run := flatFeeFixture(t, 5_000_000)
			owner := wf.UserID
			ceiling := models.X402ProbeFloorUSDMicros
			if mode == "allowed" {
				ceiling = models.ByokFlatFeeUSDMicros
			}
			if mode != "unset" {
				if _, err := store.UpsertUserSettings(t.Context(), owner, models.UserSettingsPatch{SetMaxCallSpend: true, MaxCallSpendUSDMicros: &ceiling}); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "settings_error" {
				wf.UserID = "missing-settings-owner"
			}
			called := false
			_, err := runner.withReservedFlatFee(t.Context(), wf, run, "node", true, func() (any, error) { called = true; return "ok", nil })
			want := mode == "allowed" || mode == "unset"
			if called != want {
				t.Fatalf("execution=%v want %v error=%v", called, want, err)
			}
			if want && err != nil {
				t.Fatal(err)
			}
			if !want && !isBillingRefusal(err) {
				t.Fatalf("refusal can degrade: %v", err)
			}
			balance, readErr := store.GetCreditBalance(t.Context(), owner)
			expected := int64(5_000_000)
			if want {
				expected -= models.ByokFlatFeeUSDMicros
			}
			if readErr != nil || balance != expected {
				t.Fatalf("balance=%d want %d error=%v", balance, expected, readErr)
			}
			entries, readErr := store.ListDebitLedger(t.Context(), run.ID)
			if readErr != nil || len(entries) != map[bool]int{true: 1, false: 0}[want] {
				t.Fatalf("ledger=%+v error=%v", entries, readErr)
			}
		})
	}
}

func TestCallSpendLimitCacheOwnerAndRunLifetime(t *testing.T) {
	runner, store, wf, run := flatFeeFixture(t, 5_000_000)
	ceiling := int64(100_000)
	save := func() {
		t.Helper()
		if _, err := store.UpsertUserSettings(t.Context(), wf.UserID, models.UserSettingsPatch{SetMaxCallSpend: true, MaxCallSpendUSDMicros: &ceiling}); err != nil {
			t.Fatal(err)
		}
	}
	save()
	finish := runner.trackSpendLimit(wf, run)
	if err := runner.checkCallSpend(t.Context(), wf, run, 100_000); err != nil {
		t.Fatal(err)
	}
	ceiling = 50_000
	save()
	if err := runner.checkCallSpend(t.Context(), wf, run, 100_000); err != nil {
		t.Fatal("in-progress run lost its snapshot", err)
	}
	other := wf
	other.UserID = "different-owner"
	if err := runner.checkCallSpend(t.Context(), other, run, 100_000); !errors.Is(err, errBillingCheckFailed) {
		t.Fatalf("cache crossed owner: %v", err)
	}
	finish()
	if err := runner.checkCallSpend(t.Context(), wf, run, 100_000); !errors.Is(err, errCallSpendLimit) {
		t.Fatalf("later run used stale settings: %v", err)
	}
}

func TestCallSpendLimitDoesNotCacheReadFailure(t *testing.T) {
	runner, _, wf, run := flatFeeFixture(t, 5_000_000)
	defer runner.trackSpendLimit(wf, run)()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := runner.checkCallSpend(ctx, wf, run, 100_000); !errors.Is(err, errBillingCheckFailed) {
		t.Fatalf("cancellation bypassed guard: %v", err)
	}
	if err := runner.checkCallSpend(t.Context(), wf, run, 100_000); err != nil {
		t.Fatalf("failure poisoned later check: %v", err)
	}
}

func TestFundedCallLimitIncludesMarkupAndLaterQuote(t *testing.T) {
	runner, store, wf, run := flatFeeFixture(t, 5_000_000)
	ceiling := models.X402PlatformFeeUSDMicros + 200_000
	if _, err := store.UpsertUserSettings(t.Context(), wf.UserID, models.UserSettingsPatch{SetMaxCallSpend: true, MaxCallSpendUSDMicros: &ceiling}); err != nil {
		t.Fatal(err)
	}
	base, remaining := newRunLevelLedger(1_000_000, wf, run, store)
	ledger := runner.limitFundedLedger(wf, run, base)
	for _, amount := range []int64{200_001, math.MaxInt64, -1} {
		if err := ledger.Reserve(t.Context(), amount); err == nil {
			t.Fatalf("accepted charge %d", amount)
		}
		if remaining() != 1_000_000 {
			t.Fatal("rejected quote changed pool")
		}
	}
	if err := ledger.Reserve(t.Context(), 200_000); err != nil {
		t.Fatal(err)
	}
	if remaining() != 800_000 {
		t.Fatal("allowed quote did not reserve vendor cost")
	}
	ledger.Release(t.Context(), 200_000)
	if remaining() != 1_000_000 {
		t.Fatal("release changed accounting")
	}
}

func TestPrefundRejectsQuoteBeforeFunding(t *testing.T) {
	runner, store, wf, run := flatFeeFixture(t, 5_000_000)
	ceiling := models.X402PlatformFeeUSDMicros + 100_000
	if _, err := store.UpsertUserSettings(t.Context(), wf.UserID, models.UserSettingsPatch{SetMaxCallSpend: true, MaxCallSpendUSDMicros: &ceiling}); err != nil {
		t.Fatal(err)
	}
	probes := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probes++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(402)
		_, _ = w.Write([]byte(`{"x402Version":2,"accepts":[{"scheme":"exact","network":"algorand:testnet","asset":"10458941","payTo":"recipient","amount":"200000","maxAmountRequired":"200000"}]}`))
	}))
	defer target.Close()
	_, err := runner.reserveAndFundRun(t.Context(), wf, run, models.AttachConfig{Tools: []models.WorkflowNode{{ID: "paid", Type: models.NodeTypeTool402, Endpoint: target.URL, Method: "GET"}}})
	if !errors.Is(err, errCallSpendLimit) || probes != 1 {
		t.Fatalf("quote not blocked: probes=%d error=%v", probes, err)
	}
	balance, err := store.GetCreditBalance(t.Context(), wf.UserID)
	if err != nil || balance != 5_000_000 {
		t.Fatalf("prefund debited balance: %d %v", balance, err)
	}
	fundings, err := store.ListX402RunFundingsByRun(t.Context(), run.ID)
	if err != nil || len(fundings) != 0 {
		t.Fatalf("prefund recorded funding: %+v %v", fundings, err)
	}
}
