package db_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRecordRunFundingThenRunFundedSettlement(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	wf, err := store.CreateWorkflow(ctx, "Funding", "dev")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })
	run, err := store.CreateRun(ctx, wf.ID, "test", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}

	inboundTxID := fmt.Sprintf("RUNFUND-TX-%d", time.Now().UnixNano())
	funding, err := store.RecordRunFunding(ctx, run.ID, inboundTxID, 500000)
	if err != nil {
		t.Fatal(err)
	}
	if funding.RunID != run.ID {
		t.Fatalf("want %s, got %s", run.ID, funding.RunID)
	}
	if funding.InboundTxID != inboundTxID {
		t.Fatalf("want %s, got %s", inboundTxID, funding.InboundTxID)
	}
	if funding.AmountAssetMicros != 500000 {
		t.Fatalf("want 500000, got %d", funding.AmountAssetMicros)
	}

	row, err := store.RecordRunFundedSettlement(ctx, funding.ID, "https://example.com/target", 25000)
	if err != nil {
		t.Fatal(err)
	}
	if row.InboundTxID != nil {
		t.Fatalf("want nil InboundTxID for a run-funded row, got %v", *row.InboundTxID)
	}
	if row.Status != "pending_outbound" {
		t.Fatalf("want default status pending_outbound, got %s", row.Status)
	}
	if row.AmountAssetMicros != 25000 {
		t.Fatalf("want 25000, got %d", row.AmountAssetMicros)
	}

	if err := store.RecordOutboundSettlement(ctx, row.ID, "OUTBOUND-TX-RUNFUND-1", "settled"); err != nil {
		t.Fatal(err)
	}
}

// TestX402RunFundingConstraintRejectsNeitherIDPresent is a DB-level test:
// x402_relay_settlements' funding_check CHECK constraint requires either a
// per-call inbound_tx_id (the normal, unmodified public relay path) or a
// run_funding_id (a run-level bulk settlement, Task 5) -- a row with
// neither is not a real audit record of anything and must be rejected by
// the database itself, not just application code.
func TestX402RunFundingConstraintRejectsNeitherIDPresent(t *testing.T) {
	testStore(t) // ensures migrations (including 000010) have run

	url := os.Getenv("TEST_DATABASE_URL")
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	targetURL := fmt.Sprintf("https://example.com/funding-check-%d", time.Now().UnixNano())
	_, err = pool.Exec(context.Background(), `
		INSERT INTO x402_relay_settlements (target_url, amount_asset_micros)
		VALUES ($1, $2)
	`, targetURL, int64(1000))
	if err == nil {
		t.Fatal("want the insert to be rejected: neither inbound_tx_id nor run_funding_id is set")
	}
	if !strings.Contains(err.Error(), "x402_relay_settlements_funding_check") {
		t.Fatalf("want the funding_check constraint to be the reason for rejection, got: %v", err)
	}
}
