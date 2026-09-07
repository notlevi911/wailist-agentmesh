package nodes_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/x402"
)

// fakeRunFundUSDCSigner is a minimal USDCGroupSigner double for
// FundRunReserve tests — it doesn't need to produce a real signed group,
// just something FundRunReserve's payload can carry through to the fake
// facilitator below.
type fakeRunFundUSDCSigner struct {
	group  []string
	idx    int
	err    error
	called bool
}

func (f *fakeRunFundUSDCSigner) SignUSDCPaymentGroup(_ context.Context, _, _ string, _, _ uint64, _ string) ([]string, int, error) {
	f.called = true
	if f.err != nil {
		return nil, 0, f.err
	}
	return f.group, f.idx, nil
}

// SignUSDCPaymentSingle is never exercised by FundRunReserve (it always uses
// the fee-pooled SignUSDCPaymentGroup) — this stub only satisfies the
// interface.
func (f *fakeRunFundUSDCSigner) SignUSDCPaymentSingle(_ context.Context, _, _ string, _, _ uint64) ([]string, int, error) {
	return f.group, f.idx, f.err
}

func TestFundRunReserveNoopOnNonPositiveAmount(t *testing.T) {
	facilitatorHit := false
	facilitator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		facilitatorHit = true
	}))
	defer facilitator.Close()

	signer := &fakeRunFundUSDCSigner{group: []string{"g0", "g1"}, idx: 0}
	cfg := nodes.RunPreFundConfig{
		USDCSigner:               signer,
		PlatformSpendEncMnemonic: "platform-enc-mnemonic",
		Facilitator:              x402.NewFacilitatorClient(facilitator.URL),
		PlatformWalletAddress:    "PLATFORMADDR",
		RelayNetwork:             "algorand:testnet",
		RelayFeePayer:            "FEEPAYERADDR",
		ExpectedAssetID:          10458941,
		FrontendURL:              "https://example.test",
	}

	for _, amount := range []int64{0, -1, -100} {
		txID, err := nodes.FundRunReserve(context.Background(), cfg, "run-1", amount)
		if err != nil {
			t.Fatalf("amount %d: unexpected error: %v", amount, err)
		}
		if txID != "" {
			t.Fatalf("amount %d: want empty txID, got %q", amount, txID)
		}
	}

	if signer.called {
		t.Fatal("want signer never called for a non-positive amount")
	}
	if facilitatorHit {
		t.Fatal("want facilitator never called for a non-positive amount")
	}
}

func TestFundRunReserveSuccess(t *testing.T) {
	const wantTxID = "RUNFUND-TX-123"

	var verifyReqs, settleReqs struct {
		PaymentRequirements x402.PaymentRequirements `json:"paymentRequirements"`
	}

	facilitator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/verify":
			json.Unmarshal(body, &verifyReqs)
			json.NewEncoder(w).Encode(map[string]any{"isValid": true})
		case "/settle":
			json.Unmarshal(body, &settleReqs)
			json.NewEncoder(w).Encode(map[string]any{"success": true, "transaction": wantTxID})
		default:
			t.Fatalf("unexpected facilitator path: %s", r.URL.Path)
		}
	}))
	defer facilitator.Close()

	signer := &fakeRunFundUSDCSigner{group: []string{"g0", "g1"}, idx: 0}
	cfg := nodes.RunPreFundConfig{
		USDCSigner:               signer,
		PlatformSpendEncMnemonic: "platform-enc-mnemonic",
		Facilitator:              x402.NewFacilitatorClient(facilitator.URL),
		PlatformWalletAddress:    "PLATFORMADDR",
		RelayNetwork:             "algorand:testnet",
		RelayFeePayer:            "FEEPAYERADDR",
		ExpectedAssetID:          10458941,
		FrontendURL:              "https://example.test",
	}

	txID, err := nodes.FundRunReserve(context.Background(), cfg, "run-1", 500000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if txID != wantTxID {
		t.Fatalf("want txID %q, got %q", wantTxID, txID)
	}
	if !signer.called {
		t.Fatal("want signer to have been called")
	}

	if verifyReqs.PaymentRequirements.PayTo != "PLATFORMADDR" {
		t.Fatalf("want verify PayTo=PLATFORMADDR (same payTo as every per-call relay settlement), got %q", verifyReqs.PaymentRequirements.PayTo)
	}
	if verifyReqs.PaymentRequirements.Extra["tag"] != "x402-global-challenge" {
		t.Fatalf("want verify Extra.tag=x402-global-challenge, got %v", verifyReqs.PaymentRequirements.Extra["tag"])
	}
	if settleReqs.PaymentRequirements.PayTo != "PLATFORMADDR" {
		t.Fatalf("want settle PayTo=PLATFORMADDR, got %q", settleReqs.PaymentRequirements.PayTo)
	}
	if settleReqs.PaymentRequirements.Extra["tag"] != "x402-global-challenge" {
		t.Fatalf("want settle Extra.tag=x402-global-challenge, got %v", settleReqs.PaymentRequirements.Extra["tag"])
	}
	// Resource is cfg.BaseURL + the real /x402/relay/run-funding path, not a
	// per-run runId-specific URL -- matches every actually-cataloged real
	// resource's convention of a real API endpoint on the resource server's
	// own domain, not an opaque identifier or a separate marketing domain.
	if want := "https://example.test/api/x402/relay/run-funding"; settleReqs.PaymentRequirements.Resource != want {
		t.Fatalf("want Resource=%q, got %q", want, settleReqs.PaymentRequirements.Resource)
	}
}

func TestFundRunReserveVerifyInvalidSurfacesError(t *testing.T) {
	facilitator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/verify":
			json.NewEncoder(w).Encode(map[string]any{"isValid": false, "invalidReason": "insufficient funds"})
		case "/settle":
			t.Fatal("want settle never called when verify says invalid")
		}
	}))
	defer facilitator.Close()

	signer := &fakeRunFundUSDCSigner{group: []string{"g0", "g1"}, idx: 0}
	cfg := nodes.RunPreFundConfig{
		USDCSigner:               signer,
		PlatformSpendEncMnemonic: "platform-enc-mnemonic",
		Facilitator:              x402.NewFacilitatorClient(facilitator.URL),
		PlatformWalletAddress:    "PLATFORMADDR",
		RelayNetwork:             "algorand:testnet",
		RelayFeePayer:            "FEEPAYERADDR",
		ExpectedAssetID:          10458941,
		FrontendURL:              "https://example.test",
	}

	txID, err := nodes.FundRunReserve(context.Background(), cfg, "run-1", 500000)
	if err == nil {
		t.Fatal("want error when verify reports invalid")
	}
	if txID != "" {
		t.Fatalf("want empty txID on error, got %q", txID)
	}
	if !strings.Contains(err.Error(), "insufficient funds") {
		t.Fatalf("want error to surface facilitator's invalid reason, got %v", err)
	}
}

func TestFundRunReserveSettleFailureSurfacesError(t *testing.T) {
	facilitator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/verify":
			json.NewEncoder(w).Encode(map[string]any{"isValid": true})
		case "/settle":
			json.NewEncoder(w).Encode(map[string]any{"success": false, "errorReason": "settlement rejected"})
		}
	}))
	defer facilitator.Close()

	signer := &fakeRunFundUSDCSigner{group: []string{"g0", "g1"}, idx: 0}
	cfg := nodes.RunPreFundConfig{
		USDCSigner:               signer,
		PlatformSpendEncMnemonic: "platform-enc-mnemonic",
		Facilitator:              x402.NewFacilitatorClient(facilitator.URL),
		PlatformWalletAddress:    "PLATFORMADDR",
		RelayNetwork:             "algorand:testnet",
		RelayFeePayer:            "FEEPAYERADDR",
		ExpectedAssetID:          10458941,
		FrontendURL:              "https://example.test",
	}

	txID, err := nodes.FundRunReserve(context.Background(), cfg, "run-1", 500000)
	if err == nil {
		t.Fatal("want error when settle reports failure")
	}
	if txID != "" {
		t.Fatalf("want empty txID on error, got %q", txID)
	}
	if !strings.Contains(err.Error(), "settlement rejected") {
		t.Fatalf("want error to surface facilitator's error reason, got %v", err)
	}
}

func TestFundRunReserveSettleTransportErrorIsIndeterminate(t *testing.T) {
	facilitator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/verify":
			json.NewEncoder(w).Encode(map[string]any{"isValid": true})
		case "/settle":
			// No decodable response body -- simulates the response being
			// lost after the facilitator accepted the request (timeout,
			// connection reset), not a definitive rejection.
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer facilitator.Close()

	signer := &fakeRunFundUSDCSigner{group: []string{"g0", "g1"}, idx: 0}
	cfg := nodes.RunPreFundConfig{
		USDCSigner:               signer,
		PlatformSpendEncMnemonic: "platform-enc-mnemonic",
		Facilitator:              x402.NewFacilitatorClient(facilitator.URL),
		PlatformWalletAddress:    "PLATFORMADDR",
		RelayNetwork:             "algorand:testnet",
		RelayFeePayer:            "FEEPAYERADDR",
		ExpectedAssetID:          10458941,
		FrontendURL:              "https://example.test",
	}

	txID, err := nodes.FundRunReserve(context.Background(), cfg, "run-1", 500000)
	if err == nil {
		t.Fatal("want error when the settle response is lost")
	}
	if txID != "" {
		t.Fatalf("want empty txID on error, got %q", txID)
	}
	if !errors.Is(err, nodes.ErrSettlementIndeterminate) {
		t.Fatalf("want error to wrap ErrSettlementIndeterminate (fate of the payment unknown), got %v", err)
	}
}

// TestFundRunReserveRetriesDefinitiveSettleFailure guards the fix for the
// elevated platform-fee settle-failure rate: a definitive (received, not
// lost) settle rejection means nothing was broadcast, so it's safe to
// retry with a freshly-signed group -- and the retry must actually happen,
// not just be theoretically safe.
func TestFundRunReserveRetriesDefinitiveSettleFailure(t *testing.T) {
	const wantTxID = "RETRY-SUCCESS-TX"
	var settleAttempts int

	facilitator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/verify":
			json.NewEncoder(w).Encode(map[string]any{"isValid": true})
		case "/settle":
			settleAttempts++
			if settleAttempts < 2 {
				json.NewEncoder(w).Encode(map[string]any{"success": false, "errorReason": "transient rejection"})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"success": true, "transaction": wantTxID})
		}
	}))
	defer facilitator.Close()

	signer := &fakeRunFundUSDCSigner{group: []string{"g0", "g1"}, idx: 0}
	cfg := nodes.RunPreFundConfig{
		USDCSigner:               signer,
		PlatformSpendEncMnemonic: "platform-enc-mnemonic",
		Facilitator:              x402.NewFacilitatorClient(facilitator.URL),
		PlatformWalletAddress:    "PLATFORMADDR",
		RelayNetwork:             "algorand:testnet",
		RelayFeePayer:            "FEEPAYERADDR",
		ExpectedAssetID:          10458941,
		FrontendURL:              "https://example.test",
	}

	txID, err := nodes.FundRunReserve(context.Background(), cfg, "run-1", 500000)
	if err != nil {
		t.Fatalf("want the retry to succeed on the second attempt, got error: %v", err)
	}
	if txID != wantTxID {
		t.Fatalf("want txID %q, got %q", wantTxID, txID)
	}
	if settleAttempts != 2 {
		t.Fatalf("want exactly 2 settle attempts (1 failure + 1 success), got %d", settleAttempts)
	}
}

// TestFundRunReserveNeverRetriesIndeterminateSettle guards the other half
// of the same fix: retrying an indeterminate settle (response lost, fate
// unknown) risks double-paying, so it must stop after exactly one attempt
// regardless of the retry budget.
func TestFundRunReserveNeverRetriesIndeterminateSettle(t *testing.T) {
	var settleAttempts int

	facilitator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/verify":
			json.NewEncoder(w).Encode(map[string]any{"isValid": true})
		case "/settle":
			settleAttempts++
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer facilitator.Close()

	signer := &fakeRunFundUSDCSigner{group: []string{"g0", "g1"}, idx: 0}
	cfg := nodes.RunPreFundConfig{
		USDCSigner:               signer,
		PlatformSpendEncMnemonic: "platform-enc-mnemonic",
		Facilitator:              x402.NewFacilitatorClient(facilitator.URL),
		PlatformWalletAddress:    "PLATFORMADDR",
		RelayNetwork:             "algorand:testnet",
		RelayFeePayer:            "FEEPAYERADDR",
		ExpectedAssetID:          10458941,
		FrontendURL:              "https://example.test",
	}

	_, err := nodes.FundRunReserve(context.Background(), cfg, "run-1", 500000)
	if !errors.Is(err, nodes.ErrSettlementIndeterminate) {
		t.Fatalf("want ErrSettlementIndeterminate, got %v", err)
	}
	if settleAttempts != 1 {
		t.Fatalf("want exactly 1 settle attempt (must not retry an indeterminate outcome -- could double-pay), got %d", settleAttempts)
	}
}

// TestFundRunReserveExhaustedRetriesPreserveEveryAttemptsError guards
// against collapsing distinct per-attempt failure reasons into just the
// last one: an intermittent failure on an early attempt and a different,
// persistent failure on a later one need to both stay visible in the
// final error to tell "transient" apart from "persistent" when diagnosing
// a real elevated settle-failure rate.
func TestFundRunReserveExhaustedRetriesPreserveEveryAttemptsError(t *testing.T) {
	var settleAttempts int

	facilitator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/verify":
			json.NewEncoder(w).Encode(map[string]any{"isValid": true})
		case "/settle":
			settleAttempts++
			json.NewEncoder(w).Encode(map[string]any{
				"success":     false,
				"errorReason": fmt.Sprintf("distinct failure reason %d", settleAttempts),
			})
		}
	}))
	defer facilitator.Close()

	signer := &fakeRunFundUSDCSigner{group: []string{"g0", "g1"}, idx: 0}
	cfg := nodes.RunPreFundConfig{
		USDCSigner:               signer,
		PlatformSpendEncMnemonic: "platform-enc-mnemonic",
		Facilitator:              x402.NewFacilitatorClient(facilitator.URL),
		PlatformWalletAddress:    "PLATFORMADDR",
		RelayNetwork:             "algorand:testnet",
		RelayFeePayer:            "FEEPAYERADDR",
		ExpectedAssetID:          10458941,
		FrontendURL:              "https://example.test",
	}

	_, err := nodes.FundRunReserve(context.Background(), cfg, "run-1", 500000)
	if err == nil {
		t.Fatal("want an error once every attempt has failed")
	}
	if settleAttempts != 3 {
		t.Fatalf("want all 3 attempts made, got %d", settleAttempts)
	}
	for i := 1; i <= 3; i++ {
		want := fmt.Sprintf("distinct failure reason %d", i)
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("want final error to mention attempt %d's own failure (%q), got: %v", i, want, err)
		}
	}
}

// TestFundRunReserveHonorsCancellationDuringVerify guards the narrowed
// protection window: a cancellation landing while Verify is in flight (no
// money broadcast yet) must be honored promptly, not swallowed by a
// blanket detach of the whole call the way an earlier version of this fix
// did.
func TestFundRunReserveHonorsCancellationDuringVerify(t *testing.T) {
	unblockVerify := make(chan struct{})
	facilitator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/verify":
			<-unblockVerify // hangs until the test lets it go, well past cancellation
			json.NewEncoder(w).Encode(map[string]any{"isValid": true})
		case "/settle":
			t.Error("want settle never reached -- the call should have been canceled during verify")
		}
	}))
	defer facilitator.Close()
	defer close(unblockVerify)

	signer := &fakeRunFundUSDCSigner{group: []string{"g0", "g1"}, idx: 0}
	cfg := nodes.RunPreFundConfig{
		USDCSigner:               signer,
		PlatformSpendEncMnemonic: "platform-enc-mnemonic",
		Facilitator:              x402.NewFacilitatorClient(facilitator.URL),
		PlatformWalletAddress:    "PLATFORMADDR",
		RelayNetwork:             "algorand:testnet",
		RelayFeePayer:            "FEEPAYERADDR",
		ExpectedAssetID:          10458941,
		FrontendURL:              "https://example.test",
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := nodes.FundRunReserve(ctx, cfg, "run-1", 500000)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("want an error once the context is canceled")
	}
	// Generous upper bound: this should return almost immediately after
	// cancel() fires (~50ms), not hang for anywhere close to a full
	// facilitator http.Client timeout (20s) or SelfSettleRetryBudget.
	if elapsed > 2*time.Second {
		t.Fatalf("want cancellation honored promptly during Verify, took %v", elapsed)
	}
}

// TestFundRunReserveSignBudgetBoundsAHungSign guards signCallBudget
// actually being applied via context.WithTimeout to the Sign call, not
// just declared and left unused: a signer that hangs forever must still
// return within signCallBudget, not run out the clock on
// SelfSettleRetryBudget (or hang forever) instead. Uses
// SetSelfSettleCallBudgetsForTest to shrink the budget rather than waiting
// out the real 20s, so this stays a fast, deterministic unit test.
func TestFundRunReserveSignBudgetBoundsAHungSign(t *testing.T) {
	const shrunkBudget = 50 * time.Millisecond
	nodes.SetSelfSettleCallBudgetsForTest(shrunkBudget)
	defer nodes.SetSelfSettleCallBudgetsForTest(0)

	facilitator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("want the facilitator never reached -- Sign should time out first")
	}))
	defer facilitator.Close()

	signer := &hangingUSDCSigner{unblock: make(chan struct{})}
	defer close(signer.unblock)
	cfg := nodes.RunPreFundConfig{
		USDCSigner:               signer,
		PlatformSpendEncMnemonic: "platform-enc-mnemonic",
		Facilitator:              x402.NewFacilitatorClient(facilitator.URL),
		PlatformWalletAddress:    "PLATFORMADDR",
		RelayNetwork:             "algorand:testnet",
		RelayFeePayer:            "FEEPAYERADDR",
		ExpectedAssetID:          10458941,
		FrontendURL:              "https://example.test",
	}

	start := time.Now()
	_, err := nodes.FundRunReserve(context.Background(), cfg, "run-1", 500000)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("want an error once every retry attempt's Sign call times out")
	}
	// Every attempt's Sign call bounded by the shrunk budget, plus the real
	// worst-case inter-attempt backoff, plus scheduling slack. Both real
	// terms come from the production values themselves (see
	// runfund_export_test.go) rather than hand-copied literals, so a change
	// to selfSettleMaxAttempts or the backoff schedule can't silently make
	// this too tight (flaky) or too loose.
	//
	// The slack is deliberately generous relative to the ~1.65s of real
	// work: five timer events have to fire here (3 sign timeouts, 2 backoff
	// sleeps), and each can overshoot under CI load. What actually has to
	// hold for this test to mean anything is the assertion below -- that
	// the whole bound stays well under the real signCallBudget default --
	// not tightness for its own sake.
	upperBound := nodes.SelfSettleMaxAttemptsForTest*shrunkBudget +
		nodes.WorstCaseBackoffTotalForTest() + 3*time.Second
	if elapsed > upperBound {
		t.Fatalf("want Sign bounded by the (shrunk) signCallBudget on every attempt, took %v (want <= %v)", elapsed, upperBound)
	}
	// Without this, a bound that crept past the production default would
	// still pass even if the shrink were ignored entirely -- i.e. the test
	// would stop testing anything. Compares against the real exported
	// default, so lowering defaultSignCallBudget in production tightens
	// this automatically instead of silently invalidating it.
	if minSlack := nodes.SelfSettleMaxAttemptsForTest * nodes.DefaultSignCallBudgetForTest; upperBound >= minSlack {
		t.Fatalf("bound %v is too loose to prove the shrunk budget fired (unshrunk sign time alone would be %v)", upperBound, minSlack)
	}
}

// hangingUSDCSigner never returns from SignUSDCPaymentGroup until its ctx
// is done or the test closes unblock -- used to prove signCallBudget
// actually bounds the call rather than being wired to a no-op timeout.
type hangingUSDCSigner struct{ unblock chan struct{} }

func (s *hangingUSDCSigner) SignUSDCPaymentGroup(ctx context.Context, _, _ string, _, _ uint64, _ string) ([]string, int, error) {
	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	case <-s.unblock:
		return nil, 0, errors.New("unblocked without a real result")
	}
}

func (s *hangingUSDCSigner) SignUSDCPaymentSingle(ctx context.Context, _, _ string, _, _ uint64) ([]string, int, error) {
	return s.SignUSDCPaymentGroup(ctx, "", "", 0, 0, "")
}

// TestFundRunReserveDoesNotInterruptInFlightSettle guards the other half:
// once Settle has actually been dispatched, a cancellation landing mid-call
// must NOT cut it off -- doing so would risk treating a real,
// possibly-already-broadcast payment as fate-unknown purely because our
// own cancellation raced the network response.
func TestFundRunReserveDoesNotInterruptInFlightSettle(t *testing.T) {
	const wantTxID = "SLOW-SETTLE-STILL-COMPLETES"
	var settleStarted = make(chan struct{})

	facilitator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/verify":
			json.NewEncoder(w).Encode(map[string]any{"isValid": true})
		case "/settle":
			close(settleStarted)
			time.Sleep(200 * time.Millisecond) // outlives the cancellation below
			json.NewEncoder(w).Encode(map[string]any{"success": true, "transaction": wantTxID})
		}
	}))
	defer facilitator.Close()

	signer := &fakeRunFundUSDCSigner{group: []string{"g0", "g1"}, idx: 0}
	cfg := nodes.RunPreFundConfig{
		USDCSigner:               signer,
		PlatformSpendEncMnemonic: "platform-enc-mnemonic",
		Facilitator:              x402.NewFacilitatorClient(facilitator.URL),
		PlatformWalletAddress:    "PLATFORMADDR",
		RelayNetwork:             "algorand:testnet",
		RelayFeePayer:            "FEEPAYERADDR",
		ExpectedAssetID:          10458941,
		FrontendURL:              "https://example.test",
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-settleStarted
		cancel() // fires while /settle is still sleeping
	}()

	txID, err := nodes.FundRunReserve(ctx, cfg, "run-1", 500000)
	if err != nil {
		t.Fatalf("want the in-flight settle to complete despite the caller's context being canceled, got: %v", err)
	}
	if txID != wantTxID {
		t.Fatalf("want txID %q, got %q", wantTxID, txID)
	}
}
