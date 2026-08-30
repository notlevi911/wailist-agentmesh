package db_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agentmesh/backend/internal/db"
)

func TestCreditTransactionLifecycle(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	email := fmt.Sprintf("credit-test-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(ctx, email, "hash")
	if err != nil {
		t.Fatal(err)
	}

	orderID := fmt.Sprintf("order_test_%d", time.Now().UnixNano())
	txn, err := store.CreateCreditTransaction(ctx, user.ID, orderID, 50000, 0.012)
	if err != nil {
		t.Fatal(err)
	}
	if txn.Status != "pending" {
		t.Fatalf("want pending got %s", txn.Status)
	}
	wantMicros := int64(50000.0 / 100.0 * 0.012 * 1e6)
	if txn.CreditUSDMicros != wantMicros {
		t.Fatalf("want %d got %d", wantMicros, txn.CreditUSDMicros)
	}

	credited, applied, err := store.CompleteCreditTransaction(ctx, "cashfree", orderID, "pay_test_1")
	if err != nil {
		t.Fatal(err)
	}
	if credited != wantMicros {
		t.Fatalf("want %d got %d", wantMicros, credited)
	}
	if !applied {
		t.Fatal("want applied=true for a fresh completion")
	}

	balance, err := store.GetCreditBalance(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if balance != wantMicros {
		t.Fatalf("want balance %d got %d", wantMicros, balance)
	}

	// Replay must not double-credit, and must report applied=false.
	credited2, applied2, err := store.CompleteCreditTransaction(ctx, "cashfree", orderID, "pay_test_1")
	if err != nil {
		t.Fatal(err)
	}
	if credited2 != wantMicros {
		t.Fatalf("replay: want %d got %d", wantMicros, credited2)
	}
	if applied2 {
		t.Fatal("want applied=false on replay")
	}
	balance2, err := store.GetCreditBalance(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if balance2 != wantMicros {
		t.Fatalf("replay must not double-credit: want %d got %d", wantMicros, balance2)
	}
}

func TestRefundCreditTransactionFullRefundReversesBalance(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	email := fmt.Sprintf("credit-refund-test-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(ctx, email, "hash")
	if err != nil {
		t.Fatal(err)
	}

	orderID := fmt.Sprintf("order_refund_%d", time.Now().UnixNano())
	if _, err := store.CreateCreditTransaction(ctx, user.ID, orderID, 50000, 0.012); err != nil {
		t.Fatal(err)
	}
	wantMicros := int64(50000.0 / 100.0 * 0.012 * 1e6)
	if _, _, err := store.CompleteCreditTransaction(ctx, "cashfree", orderID, "pay_refund_test"); err != nil {
		t.Fatal(err)
	}

	reversed, applied, err := store.RefundCreditTransaction(ctx, orderID, 50000)
	if err != nil {
		t.Fatal(err)
	}
	if reversed != wantMicros {
		t.Fatalf("want reversed %d got %d", wantMicros, reversed)
	}
	if !applied {
		t.Fatal("want applied=true for a fresh refund")
	}

	balance, err := store.GetCreditBalance(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if balance != 0 {
		t.Fatalf("want balance 0 after full refund, got %d", balance)
	}

	// Replay of the same cumulative refund amount must not double-reverse.
	reversed2, applied2, err := store.RefundCreditTransaction(ctx, orderID, 50000)
	if err != nil {
		t.Fatal(err)
	}
	if reversed2 != 0 {
		t.Fatalf("want 0 on replay, got %d", reversed2)
	}
	if applied2 {
		t.Fatal("want applied=false on replay")
	}
	balance2, err := store.GetCreditBalance(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if balance2 != 0 {
		t.Fatalf("replay must not double-reverse: want 0 got %d", balance2)
	}
}

// TestCompleteCreditTransactionCannotDoubleDipAfterRefund guards against replaying a
// completion after a refund: Razorpay signatures don't expire, so a captured verify
// payload (or a duplicate payment.captured webhook delivery) can arrive again after the
// order has already been fully refunded. Gating on status == "completed" alone would miss
// this, since RefundCreditTransaction moves status to "refunded" — completed_at is the
// guard that must hold regardless of what status becomes afterward.
func TestCompleteCreditTransactionCannotDoubleDipAfterRefund(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	email := fmt.Sprintf("credit-doubledip-test-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(ctx, email, "hash")
	if err != nil {
		t.Fatal(err)
	}

	orderID := fmt.Sprintf("order_doubledip_%d", time.Now().UnixNano())
	if _, err := store.CreateCreditTransaction(ctx, user.ID, orderID, 50000, 0.012); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CompleteCreditTransaction(ctx, "cashfree", orderID, "pay_doubledip_test"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.RefundCreditTransaction(ctx, orderID, 50000); err != nil {
		t.Fatal(err)
	}

	balanceAfterRefund, err := store.GetCreditBalance(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if balanceAfterRefund != 0 {
		t.Fatalf("want balance 0 after refund, got %d", balanceAfterRefund)
	}

	// Replaying the same completion (e.g. a re-delivered webhook, or the signed verify
	// payload replayed by an attacker) must not re-credit — the user already got their
	// money back via the refund.
	_, applied, err := store.CompleteCreditTransaction(ctx, "cashfree", orderID, "pay_doubledip_test")
	if err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Fatal("want applied=false — replaying completion after a refund must not re-credit")
	}

	balanceAfterReplay, err := store.GetCreditBalance(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if balanceAfterReplay != 0 {
		t.Fatalf("double-dip: want balance 0 after replayed completion, got %d", balanceAfterReplay)
	}
}

func TestRefundCreditTransactionPartialRefundReversesProportionally(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	email := fmt.Sprintf("credit-partial-refund-test-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(ctx, email, "hash")
	if err != nil {
		t.Fatal(err)
	}

	orderID := fmt.Sprintf("order_partial_refund_%d", time.Now().UnixNano())
	if _, err := store.CreateCreditTransaction(ctx, user.ID, orderID, 100000, 0.012); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CompleteCreditTransaction(ctx, "cashfree", orderID, "pay_partial_refund_test"); err != nil {
		t.Fatal(err)
	}

	// Refund half (50000 of 100000 paise).
	wantReversed := int64(50000.0 / 100.0 * 0.012 * 1e6)
	reversed, applied, err := store.RefundCreditTransaction(ctx, orderID, 50000)
	if err != nil {
		t.Fatal(err)
	}
	if reversed != wantReversed {
		t.Fatalf("want reversed %d got %d", wantReversed, reversed)
	}
	if !applied {
		t.Fatal("want applied=true for a fresh partial refund")
	}

	balance, err := store.GetCreditBalance(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantBalance := int64(100000.0/100.0*0.012*1e6) - wantReversed
	if balance != wantBalance {
		t.Fatalf("want balance %d got %d", wantBalance, balance)
	}
}

func TestRefundCreditTransactionNeverCompletedSkipsBalanceReversal(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	email := fmt.Sprintf("credit-neverdone-refund-test-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(ctx, email, "hash")
	if err != nil {
		t.Fatal(err)
	}

	// Order created but never completed — no credit was ever granted.
	orderID := fmt.Sprintf("order_neverdone_%d", time.Now().UnixNano())
	if _, err := store.CreateCreditTransaction(ctx, user.ID, orderID, 50000, 0.012); err != nil {
		t.Fatal(err)
	}

	reversed, applied, err := store.RefundCreditTransaction(ctx, orderID, 50000)
	if err != nil {
		t.Fatal(err)
	}
	if reversed != 0 {
		t.Fatalf("want 0 reversed for a never-completed order, got %d", reversed)
	}
	if !applied {
		t.Fatal("want applied=true — this is still a new refund event, just with nothing to reverse")
	}

	balance, err := store.GetCreditBalance(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if balance != 0 {
		t.Fatalf("want balance untouched at 0, got %d", balance)
	}
}

func TestRefundCreditTransactionUnknownOrder(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	_, _, err := store.RefundCreditTransaction(ctx, "order_does_not_exist_xyz", 100)
	if !errors.Is(err, db.ErrCreditTransactionNotFound) {
		t.Fatalf("want ErrCreditTransactionNotFound, got %v", err)
	}
}

func TestExpireStalePendingTransactions(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	email := fmt.Sprintf("credit-expire-test-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(ctx, email, "hash")
	if err != nil {
		t.Fatal(err)
	}

	// A unique, test-only provider, for the same reason
	// TestExpireStalePendingTransactionsScopesToProvider uses one: these
	// sweeps are scoped only by provider, not by user or row, and every
	// package's tests share one database. Sweeping the real "cashfree"
	// expired other packages' in-flight pending rows and counted their
	// concurrently-created ones, so the exact-count assertions below raced
	// whatever else happened to be funding a user at that moment.
	sweepProvider := fmt.Sprintf("cashfree-expiretest-%d", time.Now().UnixNano())

	orderID := fmt.Sprintf("order_expire_%d", time.Now().UnixNano())
	if _, err := store.CreateCreditTransactionForProvider(ctx, sweepProvider, user.ID, orderID, 10000, 0.012); err != nil {
		t.Fatal(err)
	}

	// Row is only a few milliseconds old — a 24h threshold must not touch it.
	n, err := store.ExpireStalePendingTransactions(ctx, sweepProvider, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("want 0 rows expired (too fresh), got %d", n)
	}

	// A zero threshold (cutoff = the database's own now) makes the row
	// qualify as stale without racing a fixed small duration.
	n2, err := store.ExpireStalePendingTransactions(ctx, sweepProvider, 0)
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 1 {
		t.Fatalf("want exactly 1 row expired, got %d", n2)
	}

	// Re-running must not re-touch rows that are no longer 'pending'.
	n3, err := store.ExpireStalePendingTransactions(ctx, sweepProvider, 0)
	if err != nil {
		t.Fatal(err)
	}
	if n3 != 0 {
		t.Fatalf("want 0 rows on second sweep (already expired), got %d", n3)
	}
}

func TestExpireStalePendingTransactionsScopesToProvider(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	// The shared test database accumulates pending rows across test runs, so this test
	// verifies its own rows by provider_order_id rather than asserting on global affected
	// row counts (which would be flaky against that pre-existing data).
	url := os.Getenv("TEST_DATABASE_URL")
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	email := fmt.Sprintf("credit-expire-scope-test-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(ctx, email, "hash")
	if err != nil {
		t.Fatal(err)
	}

	// Use a unique, test-only provider for the swept row. This test's zero-threshold
	// (expire-everything) sweep must only ever touch the row THIS test created — using the
	// real "nowpayments" here would expire every other package's concurrent nowpayments
	// pending rows against the shared test DB, flaking those tests (e.g. the handlers
	// package's TestCreateCryptoInvoiceLeavesOrphanedPendingRowOnInvoiceFailure).
	sweepProvider := fmt.Sprintf("nowpayments-scopetest-%d", time.Now().UnixNano())

	cashfreeOrderID := fmt.Sprintf("order_expire_cashfree_%d", time.Now().UnixNano())
	if _, err := store.CreateCreditTransaction(ctx, user.ID, cashfreeOrderID, 10000, 0.012); err != nil {
		t.Fatal(err)
	}

	cryptoOrderID := fmt.Sprintf("order_expire_crypto_%d", time.Now().UnixNano())
	if _, err := store.CreateCryptoCreditTransaction(ctx, user.ID, sweepProvider, cryptoOrderID, 1999); err != nil {
		t.Fatal(err)
	}

	// A zero threshold (cutoff = now) reliably makes a row created moments ago qualify as
	// stale without a timing race against a fixed small duration like 1ms. Scoping to the
	// unique test provider must only ever touch that provider's row.
	if _, err := store.ExpireStalePendingTransactions(ctx, sweepProvider, 0); err != nil {
		t.Fatal(err)
	}

	var cryptoStatus string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM credit_ledger WHERE provider_order_id = $1 AND provider = $2`,
		cryptoOrderID, sweepProvider,
	).Scan(&cryptoStatus); err != nil {
		t.Fatal(err)
	}
	if cryptoStatus != "expired" {
		t.Fatalf("want swept-provider row expired, got status %q", cryptoStatus)
	}

	var cashfreeStatus string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM credit_ledger WHERE provider_order_id = $1 AND provider = 'cashfree'`,
		cashfreeOrderID,
	).Scan(&cashfreeStatus); err != nil {
		t.Fatal(err)
	}
	if cashfreeStatus != "pending" {
		t.Fatalf("want cashfree row untouched by a nowpayments-scoped sweep, got status %q", cashfreeStatus)
	}
}
