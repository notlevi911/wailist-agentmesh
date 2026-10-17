package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/agentmesh/backend/internal/db"
	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
	"github.com/google/uuid"
)

func consoleLedgerFixture(t *testing.T, balance int64, limit *int64) (*db.Store, string, string, string) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	store, err := db.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	user, err := store.CreateUser(t.Context(), uuid.NewString()+"@example.test", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReleaseReservedCredits(t.Context(), user.ID, balance); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertUserSettings(t.Context(), user.ID, models.UserSettingsPatch{SetMaxCallSpend: true, MaxCallSpendUSDMicros: limit}); err != nil {
		t.Fatal(err)
	}
	wf, err := store.CreateWorkflow(t.Context(), "Console limit test", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateRun(t.Context(), wf.ID, "console-limit-test", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	return store, user.ID, wf.ID, run.ID
}

func assertConsoleBalance(t *testing.T, store *db.Store, owner string, want int64) {
	t.Helper()
	got, err := store.GetCreditBalance(t.Context(), owner)
	if err != nil || got != want {
		t.Fatalf("balance=%d want=%d err=%v", got, want, err)
	}
}

func TestConsoleSpendLimitLedgerChecks(t *testing.T) {
	for _, tc := range []struct {
		name   string
		amount int64
		want   error
	}{
		{"negative", -1, errConsoleSpendCheck},
		{"zero", 0, nil},
		{"exact", 100_000, nil},
		{"over", 100_001, errConsoleSpendLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			limit := int64(100_000)
			store, owner, wf, run := consoleLedgerFixture(t, 1_000_000, &limit)
			ledger := newConsolePaymentLedger(store, owner, wf, run)
			if err := ledger.Reserve(t.Context(), tc.amount); !errors.Is(err, tc.want) {
				t.Fatalf("reserve=%v want=%v", err, tc.want)
			}
			want := int64(1_000_000)
			if tc.want == nil {
				want -= tc.amount
			}
			assertConsoleBalance(t, store, owner, want)
		})
	}
}

func TestConsoleSpendLimitReadsBeforeEveryCall(t *testing.T) {
	limit := int64(100_000)
	store, owner, wf, run := consoleLedgerFixture(t, 1_000_000, &limit)
	ledger := newConsolePaymentLedger(store, owner, wf, run)
	for range 2 {
		if err := ledger.Reserve(t.Context(), 100_000); err != nil {
			t.Fatal(err)
		}
	}
	limit = 50_000
	if _, err := store.UpsertUserSettings(t.Context(), owner, models.UserSettingsPatch{SetMaxCallSpend: true, MaxCallSpendUSDMicros: &limit}); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Reserve(t.Context(), 100_000); !errors.Is(err, errConsoleSpendLimit) {
		t.Fatalf("changed ceiling ignored: %v", err)
	}
	assertConsoleBalance(t, store, owner, 800_000)
}

func TestConsoleSpendLimitReadFailures(t *testing.T) {
	for _, failure := range []string{"missing owner", "closed store", "cancelled request"} {
		t.Run(failure, func(t *testing.T) {
			limit := int64(100_000)
			store, owner, wf, run := consoleLedgerFixture(t, 1_000_000, &limit)
			ctx := t.Context()
			ledgerOwner := owner
			switch failure {
			case "missing owner":
				ledgerOwner = uuid.NewString()
			case "closed store":
				store.Close()
			case "cancelled request":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			ledger := newConsolePaymentLedger(store, ledgerOwner, wf, run)
			if err := ledger.Reserve(ctx, 100_000); !errors.Is(err, errConsoleSpendCheck) {
				t.Fatalf("unsafe read failure: %v", err)
			}
			if failure != "closed store" {
				assertConsoleBalance(t, store, owner, 1_000_000)
			}
		})
	}
}

func TestConsoleSpendLimitBatchCompensation(t *testing.T) {
	limit := int64(100_000)
	store, owner, wf, run := consoleLedgerFixture(t, 1_000_000, &limit)
	ledger := newConsoleBatchPaymentLedger(store, owner, wf, run)
	if err := ledger.Reserve(t.Context(), 100_000); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	ledger.Release(ctx, 100_000)
	assertConsoleBalance(t, store, owner, 1_000_000)
	if err := ledger.Reserve(t.Context(), 100_000); err != nil {
		t.Fatalf("release did not restore budget: %v", err)
	}
	ledger.Commit(ctx, "test", 40_000, models.DebitKindX402RelayCost)
	ledger.Commit(ctx, "test", 60_000, models.DebitKindX402PlatformFee)
	if err := ledger.Reserve(t.Context(), 1); !errors.Is(err, errConsoleSpendLimit) {
		t.Fatalf("committed spend lost from budget: %v", err)
	}
	assertConsoleBalance(t, store, owner, 900_000)
	rows, err := store.ListDebitLedger(t.Context(), run)
	if err != nil || len(rows) != 2 || rows[0].AmountUSDMicros+rows[1].AmountUSDMicros != 100_000 {
		t.Fatalf("cancelled commit rows=%v err=%v", rows, err)
	}
}

func TestConsoleSpendLimitFailedReservePreservesBudget(t *testing.T) {
	limit := int64(100_000)
	store, owner, wf, run := consoleLedgerFixture(t, 0, &limit)
	ledger := newConsoleBatchPaymentLedger(store, owner, wf, run)
	if err := ledger.Reserve(t.Context(), 100_000); !errors.Is(err, db.ErrInsufficientCredits) {
		t.Fatalf("reserve=%v", err)
	}
	if err := store.ReleaseReservedCredits(t.Context(), owner, 100_000); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Reserve(t.Context(), 100_000); err != nil {
		t.Fatalf("failed reservation consumed budget: %v", err)
	}
	assertConsoleBalance(t, store, owner, 0)
}

func TestConsoleSpendLimitBatchOverflow(t *testing.T) {
	store, owner, wf, run := consoleLedgerFixture(t, math.MaxInt64, nil)
	ledger := newConsoleBatchPaymentLedger(store, owner, wf, run)
	if err := ledger.Reserve(t.Context(), math.MaxInt64-10); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Reserve(t.Context(), 11); !errors.Is(err, errConsoleSpendCheck) {
		t.Fatalf("overflow=%v", err)
	}
	assertConsoleBalance(t, store, owner, 10)
}

func TestConsoleSpendLimitConcurrentBatch(t *testing.T) {
	limit := int64(150_000)
	store, owner, wf, run := consoleLedgerFixture(t, 1_000_000, &limit)
	ledger := newConsoleBatchPaymentLedger(store, owner, wf, run)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			<-start
			results <- ledger.Reserve(t.Context(), 100_000)
		})
	}
	close(start)
	wg.Wait()
	close(results)
	accepted, blocked := 0, 0
	for err := range results {
		if err == nil {
			accepted++
		} else if errors.Is(err, errConsoleSpendLimit) {
			blocked++
		} else {
			t.Fatal(err)
		}
	}
	if accepted != 1 || blocked != 1 {
		t.Fatalf("accepted=%d blocked=%d", accepted, blocked)
	}
	assertConsoleBalance(t, store, owner, 900_000)
}

func TestConsolePaymentRefusalClassification(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code string
		http int
	}{
		{"limit", errConsoleSpendLimit, "spending_limit", 402},
		{"wrapped limit", &nodes.ErrBalanceBlocked{Err: fmt.Errorf("reserve: %w", errConsoleSpendLimit)}, "spending_limit", 402},
		{"balance", &nodes.ErrBalanceBlocked{Err: db.ErrInsufficientCredits}, "insufficient_credits", 402},
		{"settings failure", errConsoleSpendCheck, "payment_check_failed", 503},
		{"unknown reservation failure", &nodes.ErrBalanceBlocked{Err: errors.New("private database detail")}, "payment_check_failed", 503},
		{"provider failure", errors.New("provider unavailable"), "", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			if handled := respondConsolePaymentRefusal(rec, tc.err); handled != (tc.code != "") {
				t.Fatalf("handled=%v", handled)
			}
			if rec.Code != tc.http {
				t.Fatalf("status=%d want=%d", rec.Code, tc.http)
			}
			if tc.code == "" {
				if rec.Body.Len() != 0 {
					t.Fatal("unrelated error wrote a response")
				}
				return
			}
			var body map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["code"] != tc.code {
				t.Fatalf("response=%s error=%v", rec.Body, err)
			}
			if tc.http == 503 && body["error"] != "Could not check payment eligibility. Nothing was charged; try again later." {
				t.Fatalf("unsafe infrastructure response: %s", rec.Body)
			}
		})
	}
}
