package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/agentmesh/backend/internal/db"
	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
)

func flatFeeFixture(t *testing.T, balance int64) (*Runner, *db.Store, models.Workflow, models.Run) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	store, err := db.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	user, err := store.CreateUser(ctx, fmt.Sprintf("flat-fee-%d@example.com", time.Now().UnixNano()), "hash")
	if err != nil {
		t.Fatal(err)
	}
	if balance > 0 {
		order := "fund_" + user.ID
		if _, err := store.CreateCreditTransaction(ctx, user.ID, order, 100, float64(balance)/1e6); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.CompleteCreditTransaction(ctx, "cashfree", order, "pay_"+order); err != nil {
			t.Fatal(err)
		}
	}
	wf, err := store.CreateWorkflow(ctx, "Flat fee lifecycle", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })
	run, err := store.CreateRun(ctx, wf.ID, "test", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	return &Runner{store: store}, store, wf, run
}

func TestReservedFlatFeeLifecycle(t *testing.T) {
	for _, mode := range []string{"success", "failure", "skip", "panic", "cancel", "unbillable", "insufficient", "reservation_error"} {
		t.Run(mode, func(t *testing.T) {
			balance := models.ByokFlatFeeUSDMicros
			if mode == "insufficient" || mode == "unbillable" {
				balance = 0
			}
			runner, store, wf, run := flatFeeFixture(t, balance)
			owner := wf.UserID
			if mode == "reservation_error" {
				wf.UserID = "00000000-0000-0000-0000-000000000001"
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			called := false
			var err error
			var caught any
			func() {
				defer func() { caught = recover() }()
				_, err = runner.withReservedFlatFee(ctx, wf, run, "node", mode != "unbillable", func() (any, error) {
					called = true
					remaining, readErr := store.GetCreditBalance(ctx, owner)
					if readErr != nil || remaining != 0 {
						t.Errorf("balance during execution = %d, error %v; want 0", remaining, readErr)
					}
					switch mode {
					case "failure":
						return nil, errors.New("request failed")
					case "skip":
						return "skipped", nodes.ErrActionSkipped
					case "panic":
						cancel()
						panic("execution panicked")
					case "cancel":
						cancel()
						return nil, ctx.Err()
					default:
						return "ok", nil
					}
				})
			}()
			wantCall := mode != "insufficient" && mode != "reservation_error"
			if called != wantCall {
				t.Fatalf("executed = %v, want %v", called, wantCall)
			}
			if mode == "panic" {
				if caught != "execution panicked" {
					t.Fatalf("panic = %v", caught)
				}
			} else if caught != nil {
				t.Fatalf("unexpected panic: %v", caught)
			}
			switch mode {
			case "success", "unbillable", "panic":
				if err != nil {
					t.Fatal(err)
				}
			case "insufficient":
				if !errors.Is(err, db.ErrInsufficientCredits) || !isBillingRefusal(err) {
					t.Fatalf("insufficient balance was not a billing refusal: %v", err)
				}
			case "reservation_error":
				if !errors.Is(err, errBillingCheckFailed) || !isBillingRefusal(err) {
					t.Fatalf("reservation failure was not a billing refusal: %v", err)
				}
			default:
				if err == nil {
					t.Fatal("expected execution error")
				}
			}
			wantEntries := 0
			if mode == "success" {
				balance = 0
				wantEntries = 1
			}
			got, readErr := store.GetCreditBalance(context.Background(), owner)
			if readErr != nil || got != balance {
				t.Fatalf("final balance = %d, error %v; want %d", got, readErr, balance)
			}
			entries, readErr := store.ListDebitLedger(context.Background(), run.ID)
			if readErr != nil || len(entries) != wantEntries {
				t.Fatalf("ledger = %+v, error %v; want %d entries", entries, readErr, wantEntries)
			}
			if wantEntries == 1 && (entries[0].AmountUSDMicros != models.ByokFlatFeeUSDMicros || entries[0].Kind != models.DebitKindByokFlatFee || entries[0].NodeID != "node") {
				t.Fatalf("unexpected debit: %+v", entries[0])
			}
		})
	}
}
