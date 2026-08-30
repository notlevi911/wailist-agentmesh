package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/agentmesh/backend/internal/db"
	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/sse"
	"github.com/agentmesh/backend/internal/x402"
)

// TestPaymentLedgerCommitAndReleaseSurviveCancelledContext is a regression
// test for a phantom-deduction bug: newPaymentLedger's Commit/Release
// closures used to run on the caller's own cctx. Commit and Release are
// compensating actions for money that has already moved (or a reservation
// that must be undone) -- if the triggering request's context was already
// cancelled or past its deadline (e.g. Runner.Stop firing mid-payment, or
// the outbound HTTP call to the relay timing out) by the time either ran,
// the resulting DB call would be a no-op: it neither writes the
// debit_ledger row (Commit) nor restores the reserved balance (Release),
// silently stranding the reservation as a permanent, unledgered credit
// loss. Both must now run on context.WithoutCancel so they complete
// regardless of the caller's context state.
func TestPaymentLedgerCommitAndReleaseSurviveCancelledContext(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	store, err := db.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)

	email := fmt.Sprintf("ledger-cancelled-ctx-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(context.Background(), email, "hash")
	if err != nil {
		t.Fatal(err)
	}
	orderID := fmt.Sprintf("fund_%s_%d", user.ID, time.Now().UnixNano())
	if _, err := store.CreateCreditTransaction(context.Background(), user.ID, orderID, 100, 1.0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CompleteCreditTransaction(context.Background(), "cashfree", orderID, "pay_"+orderID); err != nil {
		t.Fatal(err)
	}

	wf, err := store.CreateWorkflow(context.Background(), "Ledger Cancelled Ctx Test", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })
	wf.UserID = user.ID
	run, err := store.CreateRun(context.Background(), wf.ID, "test", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}

	r := NewRunner(store, sse.NewBroker(), nil, "", "", X402Config{USDCAssetID: 10458941})
	ledger := r.newPaymentLedger(wf, run)

	if err := ledger.Reserve(context.Background(), 250_000); err != nil {
		t.Fatalf("unexpected error reserving: %v", err)
	}
	balance, err := store.GetCreditBalance(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if balance != 750_000 {
		t.Fatalf("want balance 750000 after reserving 250000 from 1000000, got %d", balance)
	}

	// Simulate the run's context already being cancelled by the time
	// Release runs -- e.g. Runner.Stop fired, or the outbound HTTP call's
	// context deadline was exceeded, mid-payment.
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	ledger.Release(cancelledCtx, 250_000)

	balance, err = store.GetCreditBalance(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if balance != 1_000_000 {
		t.Fatalf("want balance restored to 1000000 after Release, even with an already-cancelled context, got %d -- reservation was stranded", balance)
	}

	// Same for Commit: reserve again, then commit with a cancelled context,
	// and confirm the debit_ledger row actually gets written.
	if err := ledger.Reserve(context.Background(), 250_000); err != nil {
		t.Fatalf("unexpected error reserving: %v", err)
	}
	cancelledCtx2, cancel2 := context.WithCancel(context.Background())
	cancel2()
	ledger.Commit(cancelledCtx2, "x1", 250_000, models.DebitKindX402RelayCost)

	entries, err := store.ListDebitLedger(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("want exactly 1 debit_ledger row written despite the cancelled context, got %d", len(entries))
	}
	if entries[0].AmountUSDMicros != 250_000 || entries[0].Kind != models.DebitKindX402RelayCost {
		t.Fatalf("want a 250000 x402_relay_cost entry, got %+v", entries[0])
	}
}

// TestRecordRunFundedSettlementSurvivesCancelledContext is a regression test
// for the same phantom-audit-loss bug TestPaymentLedgerCommitAndRelease...
// covers for newPaymentLedger's Commit/Release: the RecordSettlement
// closure wired into X402RelayConfig by executeNode's NodeTypeAgent case
// used to run on the caller's own cctx, so a StopWorkflow call arriving
// right after a real run-funded payment signed and sent would cancel the
// context before the audit row (x402_relay_settlements) could be written --
// silently losing the record of a real, already-committed payment. It now
// runs on context.WithoutCancel, matching every sibling Commit/Release
// closure in this file.
func TestRecordRunFundedSettlementSurvivesCancelledContext(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	store, err := db.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)

	email := fmt.Sprintf("record-settlement-cancelled-ctx-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(context.Background(), email, "hash")
	if err != nil {
		t.Fatal(err)
	}
	wf, err := store.CreateWorkflow(context.Background(), "Record Settlement Cancelled Ctx Test", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })
	wf.UserID = user.ID
	run, err := store.CreateRun(context.Background(), wf.ID, "test", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}

	inboundTxID := fmt.Sprintf("RECORD-SETTLE-CANCEL-%d", time.Now().UnixNano())
	funding, err := store.RecordRunFunding(context.Background(), run.ID, inboundTxID, 300000)
	if err != nil {
		t.Fatal(err)
	}

	r := NewRunner(store, sse.NewBroker(), &fakeUSDCSignerForLedgerTest{}, "http://localhost:65535", "platform-spend-enc-mnemonic", X402Config{USDCAssetID: 10458941})

	recordSettlement := r.newRecordSettlement(wf, run, funding.ID)

	cctx, cancel := context.WithCancel(context.Background())
	cancel() // simulate StopWorkflow firing before RecordSettlement runs

	if err := recordSettlement(cctx, "https://target.example/tool", 100000, true); err != nil {
		t.Fatalf("want RecordSettlement to succeed despite a cancelled input context, got %v", err)
	}

	rows, err := store.ListX402RelaySettlementsByRunFunding(context.Background(), funding.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("want exactly 1 settlement row written despite the cancelled context, got %d", len(rows))
	}
}

// fakeUSDCSignerForLedgerTest satisfies nodes.USDCGroupSigner (and
// nodes.WalletSigner) so a Runner built with it actually attempts the
// run-level pre-fund path in reserveAndFundRun instead of degrading
// gracefully for a missing signer.
type fakeUSDCSignerForLedgerTest struct{}

func (f *fakeUSDCSignerForLedgerTest) SignAndSendPayment(_ context.Context, _, _ string, _ uint64) (string, error) {
	return "", nil
}

func (f *fakeUSDCSignerForLedgerTest) SignUSDCPaymentGroup(_ context.Context, _, _ string, _, _ uint64, _ string) ([]string, int, error) {
	return []string{"g0", "g1"}, 0, nil
}

func (f *fakeUSDCSignerForLedgerTest) SignUSDCPaymentSingle(_ context.Context, _, _ string, _, _ uint64) ([]string, int, error) {
	return []string{"g0"}, 0, nil
}

// Compile-time proof that this double really does satisfy the interface its
// doc comment claims. reserveAndFundRun reaches its signer via
// `x, _ := r.walletSvc.(nodes.USDCGroupSigner)`, which discards the ok and
// degrades to the no-funding path when the assertion fails -- so a double
// that has fallen behind the interface does not fail to compile, it
// silently turns every run-funding assertion here into a no-op. That is
// exactly what happened when SignUSDCPaymentSingle was added to
// USDCGroupSigner and this double was not updated with it.
var _ nodes.USDCGroupSigner = (*fakeUSDCSignerForLedgerTest)(nil)

// TestReserveAndFundRunFailsRatherThanSilentlyDegradingWhenRecordRunFundingFails
// is a white-box regression test for the exact bug this branch's final
// review found: reserveAndFundRun used to return funding.ID's zero value
// ("") whenever RecordRunFunding failed after a successful FundRunReserve --
// the SAME sentinel ExecuteTool402V2 reads as "no run-level pre-fund
// happened for this run", silently routing every subsequent v2 tool402 call
// onto the OLD per-call public-relay path, which performs its own full
// inbound settle per call. Since a real bulk inbound settlement had already
// just happened moments earlier via FundRunReserve, that meant Wallet 1
// would pay twice for the same run -- exactly the double-settle bug this
// whole branch exists to eliminate. reserveAndFundRun must instead return a
// non-nil error, and must NOT release the DB credit reservation (real money
// already moved on-chain; releasing would let the user re-spend credits
// that already funded a real settlement).
//
// The RecordRunFunding failure is provoked without a mock store: the fake
// facilitator below always reports settlement under the SAME fixed
// inbound_tx_id, and a row under that exact tx id is pre-inserted (attached
// to an unrelated run) before reserveAndFundRun ever runs -- so its own
// real RecordRunFunding call collides with inbound_tx_id's UNIQUE
// constraint, simulating "the on-chain settle genuinely happened but the DB
// write recording it failed".
func TestReserveAndFundRunFailsRatherThanSilentlyDegradingWhenRecordRunFundingFails(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	store, err := db.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"TARGETADDR","asset":"10458941","maxAmountRequired":"300000"}]}`))
	}))
	defer target.Close()

	inboundTxID := fmt.Sprintf("LEDGER-INTERNAL-RUNFUND-DUP-%d", time.Now().UnixNano())
	facilitator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/verify" {
			json.NewEncoder(w).Encode(map[string]any{"isValid": true})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "transaction": inboundTxID})
	}))
	defer facilitator.Close()

	email := fmt.Sprintf("ledger-internal-runfund-dup-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(context.Background(), email, "hash")
	if err != nil {
		t.Fatal(err)
	}
	orderID := fmt.Sprintf("fund_%s_%d", user.ID, time.Now().UnixNano())
	if _, err := store.CreateCreditTransaction(context.Background(), user.ID, orderID, 100, 1.0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CompleteCreditTransaction(context.Background(), "cashfree", orderID, "pay_"+orderID); err != nil {
		t.Fatal(err)
	}

	wf, err := store.CreateWorkflow(context.Background(), "Ledger Internal Runfund Dup Test", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })
	wf.UserID = user.ID
	run, err := store.CreateRun(context.Background(), wf.ID, "test", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.RecordRunFunding(context.Background(), "unrelated-run-id", inboundTxID, 1); err != nil {
		t.Fatal(err)
	}

	r := NewRunner(store, sse.NewBroker(), &fakeUSDCSignerForLedgerTest{}, "http://localhost:65535", "platform-spend-enc-mnemonic", X402Config{
		USDCAssetID:               10458941,
		PlatformWalletAddress:     "PLATFORMADDR",
		PlatformWalletEncMnemonic: "platform-wallet-enc-mnemonic",
		FacilitatorClient:         x402.NewFacilitatorClient(facilitator.URL),
		RelayNetwork:              "algorand:testnet",
		RelayFeePayer:             "FEEPAYERADDR",
	})

	attach := models.AttachConfig{
		Tools: []models.WorkflowNode{
			{ID: "x1", Type: models.NodeTypeTool402, Endpoint: target.URL},
		},
	}

	if _, err := r.reserveAndFundRun(context.Background(), wf, run, attach); err == nil {
		t.Fatal("want reserveAndFundRun to return a non-nil error when RecordRunFunding fails after a successful FundRunReserve")
	}

	balance, err := store.GetCreditBalance(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if balance != 700000 {
		t.Fatalf("want the 300000 reservation to remain deducted, NOT released (real money already settled on-chain), got balance %d (started at 1000000)", balance)
	}
}

// TestRunLevelLedgerExhaustsPoolNotDBBalance verifies that an in-memory
// run-level ledger correctly enforces a fixed credit pool independent of the
// DB balance, and that Release restores the in-memory pool (but not the DB,
// since the DB balance was never touched).
func TestRunLevelLedgerExhaustsPoolNotDBBalance(t *testing.T) {
	wf := models.Workflow{
		ID:     "wf-1",
		UserID: "user-1",
	}
	run := models.Run{
		ID: "run-1",
	}

	// Create a run-level ledger with a fixed pool of 500000 micros.
	ledger, getRemaining := newRunLevelLedger(500000, wf, run, nil)

	ctx := context.Background()

	// Reserve 300000 — should succeed, leaving 200000.
	err := ledger.Reserve(ctx, 300000)
	if err != nil {
		t.Fatalf("first reserve failed: %v", err)
	}
	if remaining := getRemaining(); remaining != 200000 {
		t.Errorf("after first reserve: want 200000 remaining, got %d", remaining)
	}

	// Reserve another 300000 — should fail (only 200000 left).
	// Must assert that the error wraps db.ErrInsufficientCredits.
	err = ledger.Reserve(ctx, 300000)
	if err == nil {
		t.Fatal("second reserve should have failed but didn't")
	}
	if !errors.Is(err, db.ErrInsufficientCredits) {
		t.Errorf("want error wrapping ErrInsufficientCredits, got: %v", err)
	}
	// Pool should not have changed after the failed reserve.
	if remaining := getRemaining(); remaining != 200000 {
		t.Errorf("after failed reserve: want 200000 remaining, got %d", remaining)
	}

	// Reserve 200000 — should succeed, pool now empty.
	err = ledger.Reserve(ctx, 200000)
	if err != nil {
		t.Fatalf("third reserve failed: %v", err)
	}
	if remaining := getRemaining(); remaining != 0 {
		t.Errorf("after third reserve: want 0 remaining, got %d", remaining)
	}

	// Release 100000 back to the pool.
	ledger.Release(ctx, 100000)
	if remaining := getRemaining(); remaining != 100000 {
		t.Errorf("after release: want 100000 remaining, got %d", remaining)
	}

	// Reserve 100000 — should succeed now (exactly fills the released space).
	err = ledger.Reserve(ctx, 100000)
	if err != nil {
		t.Fatalf("reserve after release failed: %v", err)
	}
	if remaining := getRemaining(); remaining != 0 {
		t.Errorf("after reserve after release: want 0 remaining, got %d", remaining)
	}
}

// TestReserveAndFundRunRejectsOutOfRangeQuote is a regression test for an
// integer-overflow bug: reserveAndFundRun used to sum every attached v2
// tool402 node's live quote into an int64 estimate with no ceiling, so an
// adversarial or compromised target's huge quote (or several large quotes
// summing past int64's range) could wrap the estimate negative --
// store.ReserveCredits(ctx, userID, negativeEstimate) then INCREASES the
// user's balance instead of decrementing it. A per-quote ceiling rejects
// the quote outright before it's ever summed.
func TestReserveAndFundRunRejectsOutOfRangeQuote(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	store, err := db.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"TARGETADDR","asset":"10458941","maxAmountRequired":"2000000000"}]}`))
	}))
	defer target.Close()

	facilitatorHit := false
	facilitator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		facilitatorHit = true
	}))
	defer facilitator.Close()

	email := fmt.Sprintf("ledger-internal-overflow-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(context.Background(), email, "hash")
	if err != nil {
		t.Fatal(err)
	}
	orderID := fmt.Sprintf("fund_%s_%d", user.ID, time.Now().UnixNano())
	if _, err := store.CreateCreditTransaction(context.Background(), user.ID, orderID, 100, 1.0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CompleteCreditTransaction(context.Background(), "cashfree", orderID, "pay_"+orderID); err != nil {
		t.Fatal(err)
	}

	wf, err := store.CreateWorkflow(context.Background(), "Ledger Internal Overflow Test", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })
	wf.UserID = user.ID
	run, err := store.CreateRun(context.Background(), wf.ID, "test", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}

	r := NewRunner(store, sse.NewBroker(), &fakeUSDCSignerForLedgerTest{}, "http://localhost:65535", "platform-spend-enc-mnemonic", X402Config{
		USDCAssetID:               10458941,
		PlatformWalletAddress:     "PLATFORMADDR",
		PlatformWalletEncMnemonic: "platform-wallet-enc-mnemonic",
		FacilitatorClient:         x402.NewFacilitatorClient(facilitator.URL),
		RelayNetwork:              "algorand:testnet",
		RelayFeePayer:             "FEEPAYERADDR",
	})

	attach := models.AttachConfig{
		Tools: []models.WorkflowNode{
			{ID: "x1", Type: models.NodeTypeTool402, Endpoint: target.URL},
		},
	}

	if _, err := r.reserveAndFundRun(context.Background(), wf, run, attach); err == nil {
		t.Fatal("want reserveAndFundRun to reject a quote above the per-call ceiling")
	}

	balance, err := store.GetCreditBalance(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if balance != 1000000 {
		t.Fatalf("want balance untouched at 1000000 (rejected before ReserveCredits), got %d", balance)
	}
	if facilitatorHit {
		t.Fatal("want the facilitator never called for a rejected quote")
	}
}

// TestReserveAndFundRunRejectsOutOfRangeQuoteNoDatabase is a regression
// test for the same integer-overflow bug as TestReserveAndFundRunRejectsOutOfRangeQuote,
// but DB-free: the ceiling guard happens before any r.store dereference, so
// this test verifies the rejection path doesn't require TEST_DATABASE_URL to be set.
func TestReserveAndFundRunRejectsOutOfRangeQuoteNoDatabase(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"TARGETADDR","asset":"10458941","maxAmountRequired":"2000000000"}]}`))
	}))
	defer target.Close()

	facilitatorHit := false
	facilitator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		facilitatorHit = true
	}))
	defer facilitator.Close()

	r := NewRunner(nil, sse.NewBroker(), &fakeUSDCSignerForLedgerTest{}, "http://localhost:65535", "platform-spend-enc-mnemonic", X402Config{
		USDCAssetID:               10458941,
		PlatformWalletAddress:     "PLATFORMADDR",
		PlatformWalletEncMnemonic: "platform-wallet-enc-mnemonic",
		FacilitatorClient:         x402.NewFacilitatorClient(facilitator.URL),
		RelayNetwork:              "algorand:testnet",
		RelayFeePayer:             "FEEPAYERADDR",
	})

	wf := models.Workflow{
		ID:     "wf-no-db-test",
		UserID: "user-no-db-test",
	}
	run := models.Run{
		ID:         "run-no-db-test",
		WorkflowID: wf.ID,
	}

	attach := models.AttachConfig{
		Tools: []models.WorkflowNode{
			{ID: "x1", Type: models.NodeTypeTool402, Endpoint: target.URL},
		},
	}

	_, err := r.reserveAndFundRun(context.Background(), wf, run, attach)
	if err == nil {
		t.Fatal("want reserveAndFundRun to reject a quote above the per-call ceiling")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "exceeding the") || !strings.Contains(errMsg, "ceiling") {
		t.Fatalf("want error mentioning ceiling, got: %v", err)
	}

	if facilitatorHit {
		t.Fatal("want the facilitator never called for a rejected quote")
	}
}

// TestReserveAndFundRunHoldsReservationOnIndeterminateSettle is a regression
// test for the same class of bug TestReserveAndFundRunFailsRatherThan...
// covers one step later in the same function: if FundRunReserve's settle
// call returns ErrSettlementIndeterminate (response lost, payment's fate
// unknown), reserveAndFundRun must NOT release the DB reservation -- doing
// so risks refunding a user for money that already left Wallet 1.
func TestReserveAndFundRunHoldsReservationOnIndeterminateSettle(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	store, err := db.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"TARGETADDR","asset":"10458941","maxAmountRequired":"300000"}]}`))
	}))
	defer target.Close()

	facilitator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/verify":
			json.NewEncoder(w).Encode(map[string]any{"isValid": true})
		case "/settle":
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer facilitator.Close()

	email := fmt.Sprintf("ledger-internal-indeterminate-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(context.Background(), email, "hash")
	if err != nil {
		t.Fatal(err)
	}
	orderID := fmt.Sprintf("fund_%s_%d", user.ID, time.Now().UnixNano())
	if _, err := store.CreateCreditTransaction(context.Background(), user.ID, orderID, 100, 1.0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CompleteCreditTransaction(context.Background(), "cashfree", orderID, "pay_"+orderID); err != nil {
		t.Fatal(err)
	}

	wf, err := store.CreateWorkflow(context.Background(), "Ledger Internal Indeterminate Test", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })
	wf.UserID = user.ID
	run, err := store.CreateRun(context.Background(), wf.ID, "test", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}

	r := NewRunner(store, sse.NewBroker(), &fakeUSDCSignerForLedgerTest{}, "http://localhost:65535", "platform-spend-enc-mnemonic", X402Config{
		USDCAssetID:               10458941,
		PlatformWalletAddress:     "PLATFORMADDR",
		PlatformWalletEncMnemonic: "platform-wallet-enc-mnemonic",
		FacilitatorClient:         x402.NewFacilitatorClient(facilitator.URL),
		RelayNetwork:              "algorand:testnet",
		RelayFeePayer:             "FEEPAYERADDR",
	})

	attach := models.AttachConfig{
		Tools: []models.WorkflowNode{
			{ID: "x1", Type: models.NodeTypeTool402, Endpoint: target.URL},
		},
	}

	if _, err := r.reserveAndFundRun(context.Background(), wf, run, attach); err == nil {
		t.Fatal("want reserveAndFundRun to fail when settlement is indeterminate")
	}

	balance, err := store.GetCreditBalance(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if balance != 700000 {
		t.Fatalf("want the 300000 reservation to remain deducted, NOT released (settlement's fate is unknown, not confirmed failed), got balance %d (started at 1000000)", balance)
	}
}

// TestReserveAndFundRunDegradesGracefullyWithNilFacilitator is a regression
// test for a gap in the graceful-degradation guard: it checked only
// platformSpendEncMnemonic and usdcSigner before calling FundRunReserve,
// but FundRunReserve also unconditionally dereferences cfg.Facilitator and
// uses cfg.PlatformWalletAddress -- neither checked. Unreachable in
// production today (main.go log.Fatal-gates both), but a future test
// double or alternate entrypoint leaving Facilitator nil while the other
// two fields are set must degrade to the no-fund path, not panic after
// ReserveCredits has already run.
func TestReserveAndFundRunDegradesGracefullyWithNilFacilitator(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	store, err := db.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"TARGETADDR","asset":"10458941","maxAmountRequired":"300000"}]}`))
	}))
	defer target.Close()

	email := fmt.Sprintf("ledger-internal-nil-facilitator-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(context.Background(), email, "hash")
	if err != nil {
		t.Fatal(err)
	}
	orderID := fmt.Sprintf("fund_%s_%d", user.ID, time.Now().UnixNano())
	if _, err := store.CreateCreditTransaction(context.Background(), user.ID, orderID, 100, 1.0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CompleteCreditTransaction(context.Background(), "cashfree", orderID, "pay_"+orderID); err != nil {
		t.Fatal(err)
	}

	wf, err := store.CreateWorkflow(context.Background(), "Ledger Internal Nil Facilitator Test", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })
	wf.UserID = user.ID
	run, err := store.CreateRun(context.Background(), wf.ID, "test", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}

	// PlatformSpendEncMnemonic set and a valid USDCGroupSigner supplied
	// (both already-checked fields), but FacilitatorClient left nil and
	// PlatformWalletAddress left empty -- the two fields the existing guard
	// doesn't check.
	r := NewRunner(store, sse.NewBroker(), &fakeUSDCSignerForLedgerTest{}, "http://localhost:65535", "platform-spend-enc-mnemonic", X402Config{
		USDCAssetID: 10458941,
	})

	attach := models.AttachConfig{
		Tools: []models.WorkflowNode{
			{ID: "x1", Type: models.NodeTypeTool402, Endpoint: target.URL},
		},
	}

	result, err := r.reserveAndFundRun(context.Background(), wf, run, attach)
	if err != nil {
		t.Fatalf("want graceful degradation (nil error), not a failure, got %v", err)
	}
	if result.FundingID != "" {
		t.Fatalf("want empty FundingID (no-fund path), got %q", result.FundingID)
	}

	balance, err := store.GetCreditBalance(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if balance != 1000000 {
		t.Fatalf("want balance untouched at 1000000 (degraded before ReserveCredits), got %d", balance)
	}
}
