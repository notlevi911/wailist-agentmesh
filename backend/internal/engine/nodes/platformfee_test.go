package nodes_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/agentmesh/backend/internal/engine"
	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/x402"
)

// facilitatorSettleCapture is the subset of the /settle request body this
// file's tests need to assert against -- decoded straight off the wire
// (not trusted from whatever the caller believes it sent), so a bug that
// settles the wrong amount or pays the wrong address fails these tests
// even though "a settlement happened" alone would look fine.
type facilitatorSettleCapture struct {
	Amount string `json:"amount"`
	PayTo  string `json:"payTo"`
}

// fakeFacilitatorServer answers /verify and /settle with a fresh, always-
// valid, always-successful response, counts settle calls, and records the
// paymentRequirements of the most recent /settle request.
func fakeFacilitatorServer(t *testing.T, settleCount *int32) (*httptest.Server, func() facilitatorSettleCapture) {
	t.Helper()
	var mu sync.Mutex
	var lastSettle facilitatorSettleCapture
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/verify":
			json.NewEncoder(w).Encode(map[string]any{"isValid": true})
		case "/settle":
			var body struct {
				PaymentRequirements facilitatorSettleCapture `json:"paymentRequirements"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			lastSettle = body.PaymentRequirements
			mu.Unlock()
			atomic.AddInt32(settleCount, 1)
			json.NewEncoder(w).Encode(map[string]any{"success": true, "transaction": "FEE-SETTLE-TX"})
		default:
			t.Fatalf("unexpected facilitator request: %s", r.URL.Path)
		}
	}))
	return srv, func() facilitatorSettleCapture {
		mu.Lock()
		defer mu.Unlock()
		return lastSettle
	}
}

// TestX402V2RelaySettlesPlatformFeeOnChain verifies the fix for the
// per-call relay path silently never moving the platform's own markup as
// real money: once cfg.Facilitator is configured, a successful vendor-cost
// settlement (X-Inbound-Settled: true) must ALSO trigger a second, real
// Wallet 1 -> Wallet 2 settlement for models.X402PlatformFeeUSDMicros, on
// top of (not instead of) the existing internal credit-ledger commit, and
// that settlement must genuinely be for the fee amount, paid to the
// platform's own wallet -- not just "some settlement happened".
func TestX402V2RelaySettlesPlatformFeeOnChain(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"TARGETADDR","asset":"10458941","maxAmountRequired":"100000"}]}`))
	}))
	defer target.Close()

	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Payment") != "" {
			w.Header().Set("X-Inbound-Settled", "true")
			w.Write([]byte(`{"data":"relayed paid response"}`))
			return
		}
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"PLATFORMADDR","asset":"10458941","maxAmountRequired":"100000"}]}`))
	}))
	defer relay.Close()

	var settleCount int32
	facilitator, lastSettle := fakeFacilitatorServer(t, &settleCount)
	defer facilitator.Close()

	node := models.WorkflowNode{ID: "x1", Type: models.NodeTypeTool402, Endpoint: target.URL}
	rc := engine.NewRunContext("r1", nil)
	aw := models.AgentWallet{}
	usdcSigner := &mockUSDCGroupSigner{group: []string{"g0", "g1"}, idx: 0}

	var committedKinds []string
	var committedAmounts []int64
	ledger := nodes.PaymentLedger{
		Reserve: func(context.Context, int64) error { return nil },
		Commit: func(_ context.Context, _ string, amount int64, kind string) {
			committedKinds = append(committedKinds, kind)
			committedAmounts = append(committedAmounts, amount)
		},
		Release: func(context.Context, int64) {},
	}

	relayCfg := nodes.X402RelayConfig{
		USDCSigner:               usdcSigner,
		PlatformSpendEncMnemonic: "platform-enc-mnemonic",
		ExpectedAssetID:          uint64(10458941),
		RelayBaseURL:             relay.URL,
		Facilitator:              x402.NewFacilitatorClient(facilitator.URL),
		PlatformWalletAddress:    "PLATFORMADDR",
		RelayNetwork:             "algorand:testnet",
		RelayFeePayer:            "FEEPAYERADDR",
		FrontendURL:              "https://agentmesh.example",
		PerCallLedger:            nodes.CallLedger(ledger),
	}

	result, err := nodes.ExecuteTool402V2(context.Background(), node, rc, aw, nil, relayCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := atomic.LoadInt32(&settleCount); got != 1 {
		t.Fatalf("want exactly 1 facilitator /settle call for the platform fee, got %d", got)
	}
	if result.PlatformFeeTxID != "FEE-SETTLE-TX" {
		t.Fatalf("want the fee settlement's real tx id surfaced on the result, got %q", result.PlatformFeeTxID)
	}

	// The settlement actually seen by the facilitator must be for the flat
	// markup, paid to the platform's own wallet -- not the vendor amount,
	// not an empty/wrong address. This is the check that would catch a
	// mixed-up amount/PlatformWalletAddress wiring bug that "a settlement
	// happened" alone cannot.
	settle := lastSettle()
	if settle.Amount != "1500000" {
		t.Fatalf("want the fee settlement's amount to be the flat markup (1500000), got %q", settle.Amount)
	}
	if settle.PayTo != "PLATFORMADDR" {
		t.Fatalf("want the fee settlement paid to the platform wallet, got %q", settle.PayTo)
	}

	// The internal credit-ledger commits are unaffected by the new on-chain
	// leg -- the caller's AgentMesh credit balance must still show two rows,
	// vendor cost and platform fee, exactly as before this change.
	wantKinds := []string{models.DebitKindX402RelayCost, models.DebitKindX402PlatformFee}
	wantAmounts := []int64{100000, models.X402PlatformFeeUSDMicros}
	if len(committedKinds) != 2 || committedKinds[0] != wantKinds[0] || committedKinds[1] != wantKinds[1] {
		t.Fatalf("want ledger commits %v, got %v", wantKinds, committedKinds)
	}
	if len(committedAmounts) != 2 || committedAmounts[0] != wantAmounts[0] || committedAmounts[1] != wantAmounts[1] {
		t.Fatalf("want ledger commit amounts %v, got %v", wantAmounts, committedAmounts)
	}
}

// TestX402V2RelayFeeSettlementFailureDoesNotFailTheCall verifies the fee
// settlement is best-effort: the vendor has already been paid and the
// caller's credit balance already reflects the full charge by the time the
// fee settlement runs, so a facilitator failure on THAT leg must not turn an
// otherwise-successful tool call into an error.
func TestX402V2RelayFeeSettlementFailureDoesNotFailTheCall(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"TARGETADDR","asset":"10458941","maxAmountRequired":"100000"}]}`))
	}))
	defer target.Close()

	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Payment") != "" {
			w.Header().Set("X-Inbound-Settled", "true")
			w.Write([]byte(`{"data":"relayed paid response"}`))
			return
		}
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"PLATFORMADDR","asset":"10458941","maxAmountRequired":"100000"}]}`))
	}))
	defer relay.Close()

	// A facilitator that always 500s -- the fee settlement can never
	// succeed, but the tool call itself must still report success.
	brokenFacilitator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer brokenFacilitator.Close()

	node := models.WorkflowNode{ID: "x1", Type: models.NodeTypeTool402, Endpoint: target.URL}
	rc := engine.NewRunContext("r1", nil)
	usdcSigner := &mockUSDCGroupSigner{group: []string{"g0", "g1"}, idx: 0}

	relayCfg := nodes.X402RelayConfig{
		USDCSigner:               usdcSigner,
		PlatformSpendEncMnemonic: "platform-enc-mnemonic",
		ExpectedAssetID:          uint64(10458941),
		RelayBaseURL:             relay.URL,
		Facilitator:              x402.NewFacilitatorClient(brokenFacilitator.URL),
		PlatformWalletAddress:    "PLATFORMADDR",
		RelayNetwork:             "algorand:testnet",
		RelayFeePayer:            "FEEPAYERADDR",
		FrontendURL:              "https://agentmesh.example",
		PerCallLedger:            nodes.CallLedger(noopLedger()),
	}

	result, err := nodes.ExecuteTool402V2(context.Background(), node, rc, models.AgentWallet{}, nil, relayCfg)
	if err != nil {
		t.Fatalf("want the call to still succeed despite the fee settlement failing, got %v", err)
	}
	if result.PlatformFeeTxID != "" {
		t.Fatalf("want no fee tx id when settlement failed, got %q", result.PlatformFeeTxID)
	}
	if result.SettledUSDMicros != 100000 {
		t.Fatalf("want the vendor-cost settlement to be reported normally regardless of the fee settlement's fate, got %d", result.SettledUSDMicros)
	}
}

// TestX402RunLevelNeverSettlesPlatformFeeSeparately pins the invariant the
// two-transaction design depends on: a run-funded call (dispatched into
// executeTool402RunLevel, not executeTool402V2Relay) must NEVER trigger its
// own SettlePlatformFee, even when cfg.Facilitator is fully configured --
// its markup is already covered by reserveAndFundRun's single up-front
// creditReserve settlement (see runner.go). A future edit that added a fee
// settlement here too would silently double-settle the markup on every
// agent-attached call; this test fails immediately if that happens.
func TestX402RunLevelNeverSettlesPlatformFeeSeparately(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Payment") != "" {
			w.Write([]byte(`{"data":"paid"}`))
			return
		}
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"TARGETADDR","asset":"10458941","maxAmountRequired":"100000"}]}`))
	}))
	defer target.Close()

	var settleCount int32
	facilitator, _ := fakeFacilitatorServer(t, &settleCount)
	defer facilitator.Close()

	node := models.WorkflowNode{ID: "x1", Type: models.NodeTypeTool402, Endpoint: target.URL}
	rc := engine.NewRunContext("r1", nil)
	usdcSigner := &mockUSDCGroupSigner{group: []string{"g0", "g1"}, idx: 0}

	relayCfg := nodes.X402RelayConfig{
		RunFundingID:          "test-run-funding-no-separate-fee",
		RunFundedToolIDs:      map[string]bool{"x1": true},
		Ledger:                nodes.RunLedger(noopLedger()),
		MarkupLedger:          nodes.RunLedger(noopLedger()),
		Facilitator:           x402.NewFacilitatorClient(facilitator.URL),
		PlatformWalletAddress: "PLATFORMADDR",
		RelayNetwork:          "algorand:testnet",
		RelayFeePayer:         "FEEPAYERADDR",
		FrontendURL:           "https://agentmesh.example",
		Wallet2: nodes.Wallet2PayConfig{
			USDCSigner:                usdcSigner,
			PlatformWalletEncMnemonic: "platform-wallet-enc-mnemonic",
			USDCAssetID:               uint64(10458941),
			RelayNetwork:              "algorand:testnet",
		},
	}

	result, err := nodes.ExecuteTool402V2(context.Background(), node, rc, models.AgentWallet{}, nil, relayCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := atomic.LoadInt32(&settleCount); got != 0 {
		t.Fatalf("want ZERO facilitator /settle calls on the run-funded path (markup already covered by the run's up-front settlement), got %d", got)
	}
	if result.PlatformFeeTxID != "" {
		t.Fatalf("want no PlatformFeeTxID on the run-funded path, got %q", result.PlatformFeeTxID)
	}
}
