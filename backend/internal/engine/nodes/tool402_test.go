package nodes_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/agentmesh/backend/internal/engine"
	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
)

// TestMain sets the permissive URL validator once for the whole nodes_test
// binary, so every test that dials an httptest.NewServer target works
// regardless of file/test execution order. No test in this package exercises
// the real SSRF-blocking validator, so there's nothing to preserve by
// toggling it per-test.
func TestMain(m *testing.M) {
	nodes.SetURLValidatorForTest(func(string) error { return nil })
	os.Exit(m.Run())
}

type mockSigner struct {
	txID string
	err  error
}

func (m *mockSigner) SignAndSendPayment(_ context.Context, _, _ string, _ uint64) (string, error) {
	return m.txID, m.err
}

func TestX402FreeEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":"free response"}`))
	}))
	defer srv.Close()
	node := models.WorkflowNode{ID: "x1", Type: models.NodeTypeTool402, Endpoint: srv.URL}
	rc := engine.NewRunContext("r1", nil)
	result, err := nodes.ExecuteTool402(context.Background(), node, rc, models.AgentWallet{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := result.(map[string]any)
	if !ok || m["data"] != "free response" {
		t.Fatalf("unexpected result: %v", result)
	}
}

func TestX402ParseQuote(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Payment-Required", `{"price":"0.001","unit":"call","network":"algorand-testnet","recipient":"ALGO123"}`)
		w.WriteHeader(http.StatusPaymentRequired)
	}))
	defer srv.Close()
	price, err := nodes.QuoteX402(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if price["price"] != "0.001" {
		t.Fatalf("want price 0.001 got %v", price["price"])
	}
}

// TestX402ProbeQuoteValid verifies that ProbeX402Quote (and by extension
// ProbeX402Price) correctly parses a well-formed real v2 challenge's
// payTo/asset/maxAmountRequired, exercising probeTool402Endpoint directly
// rather than only through ExecuteTool402V2 (which never surfaces the parsed
// quote to its caller).
func TestX402ProbeQuoteValid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"TARGETADDR","asset":"10458941","maxAmountRequired":"250000"}]}`))
	}))
	defer srv.Close()

	isV2, quote, err := nodes.ProbeX402Quote(context.Background(), srv.URL, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isV2 {
		t.Fatal("want isV2=true for a real accepts[] challenge")
	}
	if quote.PayTo != "TARGETADDR" {
		t.Fatalf("want payTo TARGETADDR, got %q", quote.PayTo)
	}
	if quote.Asset != "10458941" {
		t.Fatalf("want asset 10458941, got %q", quote.Asset)
	}
	if quote.MaxAmountRequired != "250000" {
		t.Fatalf("want maxAmountRequired 250000, got %q", quote.MaxAmountRequired)
	}

	priceIsV2, amount, err := nodes.ProbeX402Price(context.Background(), srv.URL, "")
	if err != nil {
		t.Fatalf("unexpected error from ProbeX402Price: %v", err)
	}
	if !priceIsV2 {
		t.Fatal("want isV2=true from ProbeX402Price")
	}
	if amount != 250000 {
		t.Fatalf("want amount 250000, got %d", amount)
	}
}

// TestX402ProbeQuoteMalformedAmount verifies that a real v2 challenge
// (accepts[] present) whose maxAmountRequired is non-numeric produces an
// error rather than silently parsing to amount=0 — a silent 0 would be
// indistinguishable from a genuinely free tool, and Task 5 sizes a real
// credit reservation and on-chain payment off this value.
func TestX402ProbeQuoteMalformedAmount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"TARGETADDR","asset":"10458941","maxAmountRequired":"not-a-number"}]}`))
	}))
	defer srv.Close()

	_, _, err := nodes.ProbeX402Quote(context.Background(), srv.URL, "")
	if err == nil {
		t.Fatal("want an error for a malformed (non-numeric) maxAmountRequired, got nil")
	}

	_, amount, err := nodes.ProbeX402Price(context.Background(), srv.URL, "")
	if err == nil {
		t.Fatal("want an error from ProbeX402Price for a malformed maxAmountRequired, got nil")
	}
	if amount != 0 {
		t.Fatalf("want zero-valued amount alongside the error, got %d", amount)
	}
}

// TestX402ProbeQuoteMissingAmount verifies that a real v2 challenge with
// maxAmountRequired missing entirely (not merely malformed) is treated the
// same as a malformed one: an error, not a silent zero. There's no reason a
// missing field should be trusted more than a garbled one — both are
// evidence of a broken/non-compliant challenge, and either way Task 5 must
// not size a reservation or payment off an unverified zero.
func TestX402ProbeQuoteMissingAmount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"TARGETADDR","asset":"10458941"}]}`))
	}))
	defer srv.Close()

	isV2, _, err := nodes.ProbeX402Quote(context.Background(), srv.URL, "")
	if err == nil {
		t.Fatal("want an error for a missing maxAmountRequired, got nil")
	}
	if !isV2 {
		t.Fatal("want isV2=true still reported — it IS a real v2 challenge, just malformed")
	}
}

// TestX402ProbePostOnlyEndpointUnreachableViaGet is a reproduce step for the
// real bug found live against Prism
// (https://prism-99h2.onrender.com/resume-screen-accurate) on 2026-07-31: a
// POST-only x402 endpoint 404s a bare GET before it ever looks at payment
// state. Confirms the OLD default (empty method -> GET) genuinely can't see
// this endpoint's real v2 challenge at all -- notPaymentRequired comes back
// true (a 404, not a 402), exactly the failure mode that made the whole
// tool402 dispatch dead-end before probing with the right method existed.
func TestX402ProbePostOnlyEndpointUnreachableViaGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"TARGETADDR","asset":"10458941","maxAmountRequired":"250000"}]}`))
	}))
	defer srv.Close()

	isV2, quote, err := nodes.ProbeX402Quote(context.Background(), srv.URL, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isV2 || quote.PayTo != "" {
		t.Fatalf("want the OLD get-only probe to see a 404, not the real v2 challenge (isV2=%v quote=%+v)", isV2, quote)
	}
}

// TestX402ProbePostOnlyEndpointReachableViaConfiguredMethod is the fix half
// of the pair above: passing method=POST reaches the same endpoint's real
// v2 challenge correctly.
func TestX402ProbePostOnlyEndpointReachableViaConfiguredMethod(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"TARGETADDR","asset":"10458941","maxAmountRequired":"250000"}]}`))
	}))
	defer srv.Close()

	isV2, quote, err := nodes.ProbeX402Quote(context.Background(), srv.URL, http.MethodPost)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isV2 {
		t.Fatal("want isV2=true once probed with the endpoint's actual required method")
	}
	if quote.PayTo != "TARGETADDR" || quote.MaxAmountRequired != "250000" {
		t.Fatalf("want the real quote parsed, got %+v", quote)
	}

	priceIsV2, amount, err := nodes.ProbeX402Price(context.Background(), srv.URL, http.MethodPost)
	if err != nil {
		t.Fatalf("unexpected error from ProbeX402Price: %v", err)
	}
	if !priceIsV2 || amount != 250000 {
		t.Fatalf("want isV2=true amount=250000, got isV2=%v amount=%d", priceIsV2, amount)
	}
}

// TestX402ProbeAcceptsAmountFieldDialect is a reproduce-then-fix regression
// test for the real bug found live against our own Prism-schema demo
// merchant (backend/cmd/x402demo) on 2026-07-31: its challenge uses `amount`
// (the current real-world x402 dialect — confirmed against Prism's own live
// endpoint and the official @x402/core v2.20 SDK), not `maxAmountRequired`
// (this codebase's own historical convention). Before this fix, a real,
// live workflow run against that merchant failed with "invalid or missing
// maxAmountRequired" despite the challenge being entirely well-formed.
func TestX402ProbeAcceptsAmountFieldDialect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"TARGETADDR","asset":"10458941","amount":"250000"}]}`))
	}))
	defer srv.Close()

	isV2, quote, err := nodes.ProbeX402Quote(context.Background(), srv.URL, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isV2 {
		t.Fatal("want isV2=true for a real accepts[] challenge using the `amount` field")
	}
	if quote.MaxAmountRequired != "250000" {
		t.Fatalf("want amount parsed via the `amount` fallback field, got %q", quote.MaxAmountRequired)
	}
}

// TestX402PaymentSigned verifies the full sign-and-retry flow: the runner
// receives a 402, calls the signer, and retries with X-Payment-Txid.
func TestX402PaymentSigned(t *testing.T) {
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h := r.Header.Get("X-Payment-Txid"); h != "" {
			gotHeader = h
			w.Write([]byte(`{"ok":true}`))
			return
		}
		w.Header().Set("X-Payment-Required", `{"price":"0.001","unit":"call","network":"algorand-testnet","recipient":"ALGO123"}`)
		w.WriteHeader(http.StatusPaymentRequired)
	}))
	defer srv.Close()

	node := models.WorkflowNode{ID: "x1", Type: models.NodeTypeTool402, Endpoint: srv.URL}
	rc := engine.NewRunContext("r1", nil)
	signer := &mockSigner{txID: "TX-SIGNED-123"}
	aw := models.AgentWallet{AgentNodeID: "a1", EncryptedMnemonic: "enc-mnemonic"}

	result, err := nodes.ExecuteTool402(context.Background(), node, rc, aw, signer)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type %T: %v", result, result)
	}
	if m["txId"] != "TX-SIGNED-123" {
		t.Fatalf("want txId TX-SIGNED-123, got %v", m["txId"])
	}
	if gotHeader != "TX-SIGNED-123" {
		t.Fatalf("retry request missing X-Payment-Txid header, got %q", gotHeader)
	}
	resp, _ := m["response"].(map[string]any)
	if resp == nil || resp["ok"] != true {
		t.Fatalf("want response.ok=true, got %v", m["response"])
	}
}

// TestX402NoWallet verifies that a 402 response with no wallet configured
// returns a graceful error map (not a Go error that would fail the run).
func TestX402NoWallet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Payment-Required", `{"price":"0.001","unit":"call","network":"algorand-testnet","recipient":"ALGO123"}`)
		w.WriteHeader(http.StatusPaymentRequired)
	}))
	defer srv.Close()

	node := models.WorkflowNode{ID: "x1", Type: models.NodeTypeTool402, Endpoint: srv.URL}
	rc := engine.NewRunContext("r1", nil)

	result, err := nodes.ExecuteTool402(context.Background(), node, rc, models.AgentWallet{}, nil)
	if err != nil {
		t.Fatalf("want nil Go error (graceful degradation), got %v", err)
	}
	m, ok := result.(map[string]any)
	if !ok || m["error"] == nil {
		t.Fatalf("want error key in result map, got %v", result)
	}
	if !strings.Contains(m["error"].(string), "no agent wallet") {
		t.Fatalf("want 'no agent wallet' in error message, got %v", m["error"])
	}
}

// TestX402SignerError verifies that a signer failure (e.g. insufficient funds)
// propagates as a Go error so the run log marks the node as failed.
func TestX402SignerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Payment-Required", `{"price":"0.001","unit":"call","network":"algorand-testnet","recipient":"ALGO123"}`)
		w.WriteHeader(http.StatusPaymentRequired)
	}))
	defer srv.Close()

	node := models.WorkflowNode{ID: "x1", Type: models.NodeTypeTool402, Endpoint: srv.URL}
	rc := engine.NewRunContext("r1", nil)
	signer := &mockSigner{err: errors.New("insufficient balance")}
	aw := models.AgentWallet{AgentNodeID: "a1", EncryptedMnemonic: "enc-mnemonic"}

	_, err := nodes.ExecuteTool402(context.Background(), node, rc, aw, signer)
	if err == nil {
		t.Fatal("want error from signer failure, got nil")
	}
	if !strings.Contains(err.Error(), "insufficient balance") {
		t.Fatalf("want 'insufficient balance' in error, got %v", err)
	}
}

type mockUSDCGroupSigner struct {
	group []string
	idx   int
}

func (m *mockUSDCGroupSigner) SignUSDCPaymentGroup(_ context.Context, _, _ string, _, _ uint64, _ string) ([]string, int, error) {
	return m.group, m.idx, nil
}

func (m *mockUSDCGroupSigner) SignUSDCPaymentSingle(_ context.Context, _, _ string, _, _ uint64) ([]string, int, error) {
	return m.group, m.idx, nil
}

// noopLedger is a permissive PaymentLedger for tests that need a real
// payment to go through without asserting anything about reserve/commit/
// release themselves.
func noopLedger() nodes.PaymentLedger {
	return nodes.PaymentLedger{
		Reserve: func(context.Context, int64) error { return nil },
		Commit:  func(context.Context, string, int64, string) {},
		Release: func(context.Context, int64) {},
	}
}

// TestX402V2TargetRoutesThroughRelay verifies that a target advertising the
// real x402 v2 shape (accepts[]) is never paid directly — the agent pays the
// relay instead, which is what earns orchestrator-entry attribution.
func TestX402V2TargetRoutesThroughRelay(t *testing.T) {
	var targetHit, relayHit bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHit = true
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"TARGETADDR","asset":"10458941","maxAmountRequired":"100000"}]}`))
	}))
	defer target.Close()

	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relayHit = true
		if r.Header.Get("X-Payment") != "" {
			w.Header().Set("X-Inbound-Settled", "true")
			w.Write([]byte(`{"data":"relayed paid response"}`))
			return
		}
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"PLATFORMADDR","asset":"10458941","maxAmountRequired":"100000"}]}`))
	}))
	defer relay.Close()

	node := models.WorkflowNode{ID: "x1", Type: models.NodeTypeTool402, Endpoint: target.URL}
	rc := engine.NewRunContext("r1", nil)
	aw := models.AgentWallet{AgentNodeID: "a1", EncryptedMnemonic: "enc-mnemonic"}
	signer := &mockSigner{txID: "unused-legacy-path"}
	usdcSigner := &mockUSDCGroupSigner{group: []string{"g0", "g1"}, idx: 0}

	relayCfg := nodes.X402RelayConfig{USDCSigner: usdcSigner, PlatformSpendEncMnemonic: "platform-enc-mnemonic", ExpectedAssetID: uint64(10458941), RelayBaseURL: relay.URL, PerCallLedger: nodes.CallLedger(noopLedger())}
	paymentResult, err := nodes.ExecuteTool402V2(context.Background(), node, rc, aw, signer, relayCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !targetHit {
		t.Fatal("want relay to have queried target's real price first")
	}
	if !relayHit {
		t.Fatal("want relay to have been called")
	}
	if paymentResult.SettledUSDMicros != 100000 {
		t.Fatalf("want settled amount 100000 (matches maxAmountRequired), got %d", paymentResult.SettledUSDMicros)
	}
	if paymentResult.DebitKind != models.DebitKindX402RelayCost {
		t.Fatalf("want debit kind %q, got %q", models.DebitKindX402RelayCost, paymentResult.DebitKind)
	}
	m, ok := paymentResult.Response.(map[string]any)
	if !ok || m["data"] != "relayed paid response" {
		t.Fatalf("want relayed response, got %v", paymentResult.Response)
	}
}

// TestX402LegacyTargetBypassesRelay verifies the existing flat-quote dialect
// (no accepts[]) still pays the target directly — unchanged behavior.
func TestX402LegacyTargetBypassesRelay(t *testing.T) {
	var relayHit bool
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relayHit = true
	}))
	defer relay.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h := r.Header.Get("X-Payment-Txid"); h != "" {
			w.Write([]byte(`{"ok":true}`))
			return
		}
		w.Header().Set("X-Payment-Required", `{"price":"0.001","unit":"call","network":"algorand-testnet","recipient":"ALGO123"}`)
		w.WriteHeader(http.StatusPaymentRequired)
	}))
	defer srv.Close()

	node := models.WorkflowNode{ID: "x1", Type: models.NodeTypeTool402, Endpoint: srv.URL}
	rc := engine.NewRunContext("r1", nil)
	aw := models.AgentWallet{AgentNodeID: "a1", EncryptedMnemonic: "enc-mnemonic"}
	signer := &mockSigner{txID: "TX-SIGNED-123"}
	usdcSigner := &mockUSDCGroupSigner{}

	relayCfg := nodes.X402RelayConfig{USDCSigner: usdcSigner, PlatformSpendEncMnemonic: "platform-enc-mnemonic", ExpectedAssetID: uint64(10458941), RelayBaseURL: relay.URL, PerCallLedger: nodes.CallLedger(noopLedger())}
	paymentResult, err := nodes.ExecuteTool402V2(context.Background(), node, rc, aw, signer, relayCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if relayHit {
		t.Fatal("legacy target must bypass the relay entirely")
	}
	if paymentResult.SettledUSDMicros != models.X402PlatformFeeUSDMicros {
		t.Fatalf("want flat fee %d, got %d", models.X402PlatformFeeUSDMicros, paymentResult.SettledUSDMicros)
	}
	if paymentResult.DebitKind != models.DebitKindX402PlatformFee {
		t.Fatalf("want debit kind %q, got %q", models.DebitKindX402PlatformFee, paymentResult.DebitKind)
	}
	m := paymentResult.Response.(map[string]any)
	if m["txId"] != "TX-SIGNED-123" {
		t.Fatalf("want legacy direct-pay path unchanged, got %v", m)
	}
}

// TestX402V2TargetWithAmpersandInQueryString verifies that endpoint URLs
// containing & (e.g. model=gpt4&format=json) are properly URL-encoded when
// passed to the relay, so the relay's parsing of the target parameter receives
// the full original URL, not a truncated prefix at the first &.
func TestX402V2TargetWithAmpersandInQueryString(t *testing.T) {
	var capturedTargetParam string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"TARGETADDR","asset":"10458941","maxAmountRequired":"100000"}]}`))
	}))
	defer target.Close()

	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedTargetParam = r.URL.Query().Get("target")
		if r.Header.Get("X-Payment") != "" {
			w.Header().Set("X-Inbound-Settled", "true")
			w.Write([]byte(`{"data":"relayed paid response"}`))
			return
		}
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"PLATFORMADDR","asset":"10458941","maxAmountRequired":"100000"}]}`))
	}))
	defer relay.Close()

	// Create an endpoint URL with & in the query string
	endpointWithQuery := target.URL + "?model=gpt4&format=json"
	node := models.WorkflowNode{ID: "x1", Type: models.NodeTypeTool402, Endpoint: endpointWithQuery}
	rc := engine.NewRunContext("r1", nil)
	aw := models.AgentWallet{AgentNodeID: "a1", EncryptedMnemonic: "enc-mnemonic"}
	signer := &mockSigner{txID: "unused-legacy-path"}
	usdcSigner := &mockUSDCGroupSigner{group: []string{"g0", "g1"}, idx: 0}

	relayCfg := nodes.X402RelayConfig{USDCSigner: usdcSigner, PlatformSpendEncMnemonic: "platform-enc-mnemonic", ExpectedAssetID: uint64(10458941), RelayBaseURL: relay.URL, PerCallLedger: nodes.CallLedger(noopLedger())}
	paymentResult, err := nodes.ExecuteTool402V2(context.Background(), node, rc, aw, signer, relayCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the relay received the full endpoint URL, not truncated at &
	if capturedTargetParam != endpointWithQuery {
		t.Fatalf("want target param %q, got %q (was truncated at &)", endpointWithQuery, capturedTargetParam)
	}

	m, ok := paymentResult.Response.(map[string]any)
	if !ok || m["data"] != "relayed paid response" {
		t.Fatalf("want relayed response, got %v", paymentResult.Response)
	}
}

// TestX402V2RelayPreflightUsesRealAmount verifies the balance check gates on
// the relay's real maxAmountRequired, not the flat platform fee — a
// checkBalance that only tolerates the flat fee must reject a costlier call.
func TestX402V2RelayPreflightUsesRealAmount(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"TARGETADDR","asset":"10458941","maxAmountRequired":"100000"}]}`))
	}))
	defer target.Close()

	var paid bool
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Payment") != "" {
			paid = true
			w.Header().Set("X-Inbound-Settled", "true")
			w.Write([]byte(`{"data":"relayed paid response"}`))
			return
		}
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"PLATFORMADDR","asset":"10458941","maxAmountRequired":"100000"}]}`))
	}))
	defer relay.Close()

	node := models.WorkflowNode{ID: "x1", Type: models.NodeTypeTool402, Endpoint: target.URL}
	rc := engine.NewRunContext("r1", nil)
	aw := models.AgentWallet{}
	usdcSigner := &mockUSDCGroupSigner{group: []string{"g0", "g1"}, idx: 0}

	// wantTotal is the real vendor amount (100000) plus the platform's flat
	// markup -- every real x402 call reserves/commits vendor cost + markup
	// together, not the vendor cost alone. See executeTool402V2Relay's
	// total/amount split.
	wantTotal := int64(100000) + models.X402PlatformFeeUSDMicros

	var checkedAmount int64
	ledger := nodes.PaymentLedger{
		Reserve: func(_ context.Context, amount int64) error {
			checkedAmount = amount
			if amount > wantTotal {
				return fmt.Errorf("insufficient credits")
			}
			return nil
		},
		Commit:  func(context.Context, string, int64, string) {},
		Release: func(context.Context, int64) {},
	}

	// maxAmountRequired (100000) plus the flat markup is under the ceiling
	// this ledger.Reserve allows, so this call should succeed and pay.
	relayCfg := nodes.X402RelayConfig{USDCSigner: usdcSigner, PlatformSpendEncMnemonic: "platform-enc-mnemonic", ExpectedAssetID: uint64(10458941), RelayBaseURL: relay.URL, PerCallLedger: nodes.CallLedger(ledger)}
	_, err := nodes.ExecuteTool402V2(context.Background(), node, rc, aw, nil, relayCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if checkedAmount != wantTotal {
		t.Fatalf("want ledger.Reserve called with real amount + markup (%d), got %d", wantTotal, checkedAmount)
	}
	if !paid {
		t.Fatal("want relay to have been paid")
	}

	// Now make Reserve reject anything over 50000 — well below the real
	// 100000 vendor cost, let alone vendor cost + markup — and verify the
	// payment never happens.
	paid = false
	strictLedger := nodes.PaymentLedger{
		Reserve: func(_ context.Context, amount int64) error {
			if amount > 50_000 {
				return fmt.Errorf("insufficient credits")
			}
			return nil
		},
		Commit:  func(context.Context, string, int64, string) {},
		Release: func(context.Context, int64) {},
	}
	strictRelayCfg := nodes.X402RelayConfig{USDCSigner: usdcSigner, PlatformSpendEncMnemonic: "platform-enc-mnemonic", ExpectedAssetID: uint64(10458941), RelayBaseURL: relay.URL, PerCallLedger: nodes.CallLedger(strictLedger)}
	_, err = nodes.ExecuteTool402V2(context.Background(), node, rc, aw, nil, strictRelayCfg)
	if err == nil {
		t.Fatal("want insufficient-credits error when real amount exceeds balance")
	}
	if paid {
		t.Fatal("want no payment sent when preflight rejects the real amount")
	}
}

// TestX402V2RelayRejectsPayment verifies that when the relay rejects a payment
// (returns non-2xx status despite X-Payment being present, e.g. expired/invalid
// payment or verification failure), the result is returned to the caller but
// nothing is billed — SettledUSDMicros remains 0 and DebitKind remains empty.
func TestX402V2RelayRejectsPayment(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"TARGETADDR","asset":"10458941","maxAmountRequired":"100000"}]}`))
	}))
	defer target.Close()

	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Payment") != "" {
			// Relay rejects the payment (expired/invalid/verification failed)
			w.WriteHeader(http.StatusPaymentRequired)
			w.Write([]byte(`{"error":"payment verification failed"}`))
			return
		}
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"PLATFORMADDR","asset":"10458941","maxAmountRequired":"100000"}]}`))
	}))
	defer relay.Close()

	node := models.WorkflowNode{ID: "x1", Type: models.NodeTypeTool402, Endpoint: target.URL}
	rc := engine.NewRunContext("r1", nil)
	aw := models.AgentWallet{AgentNodeID: "a1", EncryptedMnemonic: "enc-mnemonic"}
	signer := &mockSigner{txID: "unused"}
	usdcSigner := &mockUSDCGroupSigner{group: []string{"g0", "g1"}, idx: 0}

	var reserved, released bool
	var committed bool
	ledger := nodes.PaymentLedger{
		Reserve: func(context.Context, int64) error { reserved = true; return nil },
		Commit:  func(context.Context, string, int64, string) { committed = true },
		Release: func(context.Context, int64) { released = true },
	}
	relayCfg := nodes.X402RelayConfig{USDCSigner: usdcSigner, PlatformSpendEncMnemonic: "platform-enc-mnemonic", ExpectedAssetID: uint64(10458941), RelayBaseURL: relay.URL, PerCallLedger: nodes.CallLedger(ledger)}
	paymentResult, err := nodes.ExecuteTool402V2(context.Background(), node, rc, aw, signer, relayCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The response should be returned to the caller (the error message)
	m, ok := paymentResult.Response.(map[string]any)
	if !ok {
		t.Fatalf("want response to be parsed JSON, got %T: %v", paymentResult.Response, paymentResult.Response)
	}
	if m["error"] != "payment verification failed" {
		t.Fatalf("want error message in response, got %v", m)
	}

	// But nothing should be billed — the payment was rejected, so nothing
	// actually settled on-chain.
	if paymentResult.SettledUSDMicros != 0 {
		t.Fatalf("want SettledUSDMicros=0 (payment rejected), got %d", paymentResult.SettledUSDMicros)
	}
	if paymentResult.DebitKind != "" {
		t.Fatalf("want DebitKind empty (payment rejected), got %q", paymentResult.DebitKind)
	}

	// The reservation must have been taken (before signing) and then
	// released (no X-Inbound-Settled on the relay's response), never
	// committed to a permanent charge.
	if !reserved {
		t.Fatal("want the balance to have been reserved before signing")
	}
	if !released {
		t.Fatal("want the reservation to have been released since nothing settled")
	}
	if committed {
		t.Fatal("want no commit — the payment was rejected, nothing settled")
	}
}

// TestX402V2RelayReportsFailureWhenTargetRejectsAfterInboundSettles
// reproduces a real bug found via live mainnet testing 2026-08-01: the
// inbound leg (caller -> our Wallet 2) settles and gets billed, a real
// signed outbound payment group goes out, but the actual target rejects it
// (still 402s, e.g. a scheme/verification mismatch on its end) -- the relay
// honestly forwards target's real non-2xx status and body, but this
// function used to only branch on X-Inbound-Settled, never on that status,
// so the node reported "success" with target's own un-paid challenge body
// relayed back as if it were real data. Billing must stay unchanged (money
// already moved); the node must now report failure.
func TestX402V2RelayReportsFailureWhenTargetRejectsAfterInboundSettles(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"TARGETADDR","asset":"10458941","maxAmountRequired":"100000"}]}`))
	}))
	defer target.Close()

	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Payment") != "" {
			// Inbound leg settled (billing already happened on the real
			// relay handler's side) but the outbound leg to target was
			// itself rejected -- relay honestly forwards target's real
			// status/body, same as x402relay.go's payTargetAndRespond.
			w.Header().Set("X-Inbound-Settled", "true")
			w.Header().Set("X-Settlement-TxId", "REALTXID123")
			w.WriteHeader(http.StatusPaymentRequired)
			w.Write([]byte(`{"service":"Target Service","message":"Pay $0.001 USDC to get data."}`))
			return
		}
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"PLATFORMADDR","asset":"10458941","maxAmountRequired":"100000"}]}`))
	}))
	defer relay.Close()

	node := models.WorkflowNode{ID: "x1", Type: models.NodeTypeTool402, Endpoint: target.URL}
	rc := engine.NewRunContext("r1", nil)
	aw := models.AgentWallet{AgentNodeID: "a1", EncryptedMnemonic: "enc-mnemonic"}
	signer := &mockSigner{txID: "unused"}
	usdcSigner := &mockUSDCGroupSigner{group: []string{"g0", "g1"}, idx: 0}

	var committed, released bool
	ledger := nodes.PaymentLedger{
		Reserve: func(context.Context, int64) error { return nil },
		Commit:  func(context.Context, string, int64, string) { committed = true },
		Release: func(context.Context, int64) { released = true },
	}
	relayCfg := nodes.X402RelayConfig{USDCSigner: usdcSigner, PlatformSpendEncMnemonic: "platform-enc-mnemonic", ExpectedAssetID: uint64(10458941), RelayBaseURL: relay.URL, PerCallLedger: nodes.CallLedger(ledger)}
	_, err := nodes.ExecuteTool402V2(context.Background(), node, rc, aw, signer, relayCfg)

	if err == nil {
		t.Fatal("want an error — target rejected the paid request, the node must not report success")
	}
	if !committed {
		t.Fatal("want the inbound leg still committed/billed — money already moved regardless of what target did")
	}
	if released {
		t.Fatal("want no release — once billed via X-Inbound-Settled, this is not a release path")
	}
}

type panickingUSDCGroupSigner struct{}

func (p *panickingUSDCGroupSigner) SignUSDCPaymentGroup(_ context.Context, _, _ string, _, _ uint64, _ string) ([]string, int, error) {
	panic("simulated panic mid-payment")
}

func (p *panickingUSDCGroupSigner) SignUSDCPaymentSingle(_ context.Context, _, _ string, _, _ uint64) ([]string, int, error) {
	panic("simulated panic mid-payment")
}

// TestX402V2RelayReleasesReservationOnPanic is a regression test: a
// reservation taken by Reserve is a real balance decrement with no durable
// record of its own (no debit_ledger row exists until Commit runs). If a
// panic unwinds through executeTool402V2Relay after Reserve succeeds but
// before Commit or Release runs -- here simulated via a signer whose
// SignUSDCPaymentGroup panics instead of erroring -- the reservation must
// still be released via a deferred cleanup, or the user's credits are
// permanently and silently stranded with nothing to reconcile against.
func TestX402V2RelayReleasesReservationOnPanic(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"TARGETADDR","asset":"10458941","maxAmountRequired":"100000"}]}`))
	}))
	defer target.Close()

	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"PLATFORMADDR","asset":"10458941","maxAmountRequired":"100000"}]}`))
	}))
	defer relay.Close()

	node := models.WorkflowNode{ID: "x1", Type: models.NodeTypeTool402, Endpoint: target.URL}
	rc := engine.NewRunContext("r1", nil)
	aw := models.AgentWallet{}
	usdcSigner := &panickingUSDCGroupSigner{}

	var reserved, released, committed bool
	ledger := nodes.PaymentLedger{
		Reserve: func(context.Context, int64) error { reserved = true; return nil },
		Commit:  func(context.Context, string, int64, string) { committed = true },
		Release: func(context.Context, int64) { released = true },
	}

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("want the panic to propagate out of ExecuteTool402V2, got none")
			}
		}()
		relayCfg := nodes.X402RelayConfig{USDCSigner: usdcSigner, PlatformSpendEncMnemonic: "platform-enc-mnemonic", ExpectedAssetID: uint64(10458941), RelayBaseURL: relay.URL, PerCallLedger: nodes.CallLedger(ledger)}
		nodes.ExecuteTool402V2(context.Background(), node, rc, aw, nil, relayCfg)
		t.Fatal("unreachable: ExecuteTool402V2 should have panicked")
	}()

	if !reserved {
		t.Fatal("want the balance to have been reserved before the panic")
	}
	if !released {
		t.Fatal("want the reservation released via deferred cleanup despite the panic — otherwise the balance is permanently stranded")
	}
	if committed {
		t.Fatal("want no commit — the payment never completed")
	}
}

// TestX402RunLevelCommitsNotReleasesOnTargetNetworkFailure is a regression
// test for the run-level path (RunFundingID != "", dispatched by
// ExecuteTool402V2 into executeTool402RunLevel): PayTargetFromWallet2's
// Signed field becomes true the instant a real payment group is signed and
// submitted from Wallet 2 -- real money has already moved -- and stays true
// even when the subsequent HTTP call to the target fails at the network
// level (see Wallet2PayResult's doc comment in walletpay.go). Before this
// fix, executeTool402RunLevel released the ledger reservation whenever
// payErr != nil, regardless of Signed, which would understate real spend:
// a later call in the same agent turn could Reserve phantom headroom this
// pool doesn't actually have. This test hijacks and closes the connection
// on the paid request specifically (the quote probe beforehand must still
// succeed normally) to simulate a genuine transport failure after signing.
func TestX402RunLevelCommitsNotReleasesOnTargetNetworkFailure(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Payment") != "" {
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("test server does not support hijacking")
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Fatal(err)
			}
			conn.Close()
			return
		}
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"TARGETADDR","asset":"10458941","maxAmountRequired":"100000"}]}`))
	}))
	defer target.Close()

	node := models.WorkflowNode{ID: "x1", Type: models.NodeTypeTool402, Endpoint: target.URL}
	rc := engine.NewRunContext("r1", nil)
	aw := models.AgentWallet{}
	usdcSigner := &mockUSDCGroupSigner{group: []string{"g0", "g1"}, idx: 0}

	var reservedVendor, reservedMarkup int64
	var released bool
	// Separate commit trackers per pool -- not one shared slice -- so a
	// regression that commits the markup against cfg.Ledger instead of
	// cfg.MarkupLedger (or vice versa) fails this test even though the
	// (amount, kind) pair alone would still look correct.
	var vendorCommits, markupCommits []struct {
		amount int64
		kind   string
	}
	vendorLedger := nodes.PaymentLedger{
		Reserve: func(_ context.Context, amount int64) error { reservedVendor = amount; return nil },
		Commit: func(_ context.Context, _ string, amount int64, kind string) {
			vendorCommits = append(vendorCommits, struct {
				amount int64
				kind   string
			}{amount, kind})
		},
		Release: func(context.Context, int64) { released = true },
	}
	markupLedger := nodes.PaymentLedger{
		Reserve: func(_ context.Context, amount int64) error { reservedMarkup = amount; return nil },
		Commit: func(_ context.Context, _ string, amount int64, kind string) {
			markupCommits = append(markupCommits, struct {
				amount int64
				kind   string
			}{amount, kind})
		},
		Release: func(context.Context, int64) { released = true },
	}

	var recordSettlementCalled bool
	relayCfg := nodes.X402RelayConfig{
		RunFundingID:     "test-run-funding-1",
		RunFundedToolIDs: map[string]bool{"x1": true},
		Ledger:           nodes.RunLedger(vendorLedger),
		MarkupLedger:     nodes.RunLedger(markupLedger),
		Wallet2: nodes.Wallet2PayConfig{
			USDCSigner:                usdcSigner,
			PlatformWalletEncMnemonic: "platform-wallet-enc-mnemonic",
			USDCAssetID:               uint64(10458941),
			RelayNetwork:              "algorand:testnet",
		},
		RecordSettlement: func(_ context.Context, _ string, _ int64, _ bool) error {
			recordSettlementCalled = true
			return nil
		},
	}

	_, err := nodes.ExecuteTool402V2(context.Background(), node, rc, aw, nil, relayCfg)
	if err == nil {
		t.Fatal("want an error surfaced for the network failure reaching the target")
	}
	if reservedVendor != 100000 {
		t.Fatalf("want 100000 reserved from the vendor-cost pool, got %d", reservedVendor)
	}
	if reservedMarkup != models.X402PlatformFeeUSDMicros {
		t.Fatalf("want %d reserved from the markup pool, got %d", models.X402PlatformFeeUSDMicros, reservedMarkup)
	}
	if !recordSettlementCalled {
		t.Fatal("want RecordSettlement to still be called even though the target request failed at the network level")
	}
	if released {
		t.Fatal("want neither reservation released -- money already left Wallet 2 once the payment group was signed")
	}
	// One commit per pool, each attributed to the pool it was reserved
	// from: the real vendor cost against cfg.Ledger, the platform's markup
	// against cfg.MarkupLedger -- never the other way around.
	if len(vendorCommits) != 1 || vendorCommits[0].kind != models.DebitKindX402RelayCost || vendorCommits[0].amount != 100000 {
		t.Fatalf("want exactly 1 vendor-pool commit (100000, %s), got %+v", models.DebitKindX402RelayCost, vendorCommits)
	}
	if len(markupCommits) != 1 || markupCommits[0].kind != models.DebitKindX402PlatformFee || markupCommits[0].amount != models.X402PlatformFeeUSDMicros {
		t.Fatalf("want exactly 1 markup-pool commit (%d, %s), got %+v", models.X402PlatformFeeUSDMicros, models.DebitKindX402PlatformFee, markupCommits)
	}
}

// TestX402RunLevelReserveFailureIsErrBalanceBlocked is a regression test for
// executeTool402RunLevel's pool-exhaustion path: unlike its two siblings
// (the legacy-dialect and per-call v2 reserve failures a few lines above and
// below it in tool402.go), a failed cfg.Ledger.Reserve here used to return a
// plain error instead of &ErrBalanceBlocked{}. provider.go's agent loop only
// hard-stops on errors.As(execErr, &ErrBalanceBlocked{}) -- a plain error
// gets fed back to the LLM as a retryable tool result, so pool exhaustion
// would silently turn into repeated real outbound probes against the target
// (up to the loop's iteration cap) instead of stopping the run once.
func TestX402RunLevelReserveFailureIsErrBalanceBlocked(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"TARGETADDR","asset":"10458941","maxAmountRequired":"100000"}]}`))
	}))
	defer target.Close()

	node := models.WorkflowNode{ID: "x1", Type: models.NodeTypeTool402, Endpoint: target.URL}
	rc := engine.NewRunContext("r1", nil)
	aw := models.AgentWallet{}

	reserveErr := errors.New("pool exhausted: need 100000, 0 left of 500000")
	ledger := nodes.PaymentLedger{
		Reserve: func(context.Context, int64) error { return reserveErr },
	}
	relayCfg := nodes.X402RelayConfig{
		RunFundingID:     "test-run-funding-1",
		RunFundedToolIDs: map[string]bool{"x1": true},
		Ledger:           nodes.RunLedger(ledger),
	}

	_, err := nodes.ExecuteTool402V2(context.Background(), node, rc, aw, nil, relayCfg)
	if err == nil {
		t.Fatal("want an error surfaced for the exhausted pool")
	}
	var blocked *nodes.ErrBalanceBlocked
	if !errors.As(err, &blocked) {
		t.Fatalf("want *ErrBalanceBlocked so the agent loop hard-stops instead of retrying, got %T: %v", err, err)
	}
}

// TestX402RunLevelCommitsNotReleasesOnRecordSettlementFailure is the second
// half of the same regression: when the outbound payment succeeds (the
// target responds 2xx) but the audit-write call (RecordSettlement, e.g.
// RecordRunFundedSettlement/RecordOutboundSettlement in production) fails
// for an unrelated reason (DB blip), the ledger reservation must still be
// Committed, never Released -- this is a bookkeeping failure, not a payment
// failure, matching the identical distinction reserveAndFundRun already
// makes when RecordRunFunding fails after a successful FundRunReserve.
func TestX402RunLevelCommitsNotReleasesOnRecordSettlementFailure(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Payment") != "" {
			w.Write([]byte(`{"data":"paid tool response"}`))
			return
		}
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"TARGETADDR","asset":"10458941","maxAmountRequired":"100000"}]}`))
	}))
	defer target.Close()

	node := models.WorkflowNode{ID: "x1", Type: models.NodeTypeTool402, Endpoint: target.URL}
	rc := engine.NewRunContext("r1", nil)
	aw := models.AgentWallet{}
	usdcSigner := &mockUSDCGroupSigner{group: []string{"g0", "g1"}, idx: 0}

	var released bool
	// Separate commit trackers per pool -- see the identical rationale in
	// TestX402RunLevelCommitsNotReleasesOnTargetNetworkFailure above.
	var vendorCommits, markupCommits []struct {
		amount int64
		kind   string
	}
	vendorLedger := nodes.PaymentLedger{
		Reserve: func(context.Context, int64) error { return nil },
		Commit: func(_ context.Context, _ string, amount int64, kind string) {
			vendorCommits = append(vendorCommits, struct {
				amount int64
				kind   string
			}{amount, kind})
		},
		Release: func(context.Context, int64) { released = true },
	}
	markupLedger := nodes.PaymentLedger{
		Reserve: func(context.Context, int64) error { return nil },
		Commit: func(_ context.Context, _ string, amount int64, kind string) {
			markupCommits = append(markupCommits, struct {
				amount int64
				kind   string
			}{amount, kind})
		},
		Release: func(context.Context, int64) { released = true },
	}

	relayCfg := nodes.X402RelayConfig{
		RunFundingID:     "test-run-funding-2",
		RunFundedToolIDs: map[string]bool{"x1": true},
		Ledger:           nodes.RunLedger(vendorLedger),
		MarkupLedger:     nodes.RunLedger(markupLedger),
		Wallet2: nodes.Wallet2PayConfig{
			USDCSigner:                usdcSigner,
			PlatformWalletEncMnemonic: "platform-wallet-enc-mnemonic",
			USDCAssetID:               uint64(10458941),
			RelayNetwork:              "algorand:testnet",
		},
		RecordSettlement: func(context.Context, string, int64, bool) error {
			return errors.New("db unavailable")
		},
	}

	paymentResult, err := nodes.ExecuteTool402V2(context.Background(), node, rc, aw, nil, relayCfg)
	if err != nil {
		t.Fatalf("want nil error -- a failed audit write doesn't undo a real settled payment, got %v", err)
	}
	if released {
		t.Fatal("want the reservation NEVER released -- the payment settled, only the audit write failed")
	}
	if len(vendorCommits) != 1 || vendorCommits[0].kind != models.DebitKindX402RelayCost || vendorCommits[0].amount != 100000 {
		t.Fatalf("want exactly 1 vendor-pool commit (100000, %s) despite the RecordSettlement failure, got %+v", models.DebitKindX402RelayCost, vendorCommits)
	}
	if len(markupCommits) != 1 || markupCommits[0].kind != models.DebitKindX402PlatformFee || markupCommits[0].amount != models.X402PlatformFeeUSDMicros {
		t.Fatalf("want exactly 1 markup-pool commit (%d, %s) despite the RecordSettlement failure, got %+v", models.X402PlatformFeeUSDMicros, models.DebitKindX402PlatformFee, markupCommits)
	}
	if paymentResult.SettledUSDMicros != 100000 {
		t.Fatalf("want SettledUSDMicros 100000 (real vendor cost only), got %d", paymentResult.SettledUSDMicros)
	}
}

// TestX402RunLevelVendorPoolBoundsRealSpendIndependentlyOfMarkupHeadroom is
// the regression test for the fix that split cfg.Ledger and cfg.MarkupLedger
// into two separately-sized pools. Before that fix, a single pool sized
// estimate+markupTotal let one call's real vendor amount exceed `estimate`
// (the exact sum reserveAndFundRun actually moved on-chain Wallet 1 ->
// Wallet 2) by borrowing unused markup headroom left over from other
// funded-but-uncalled tools -- letting PayTargetFromWallet2 pay out real
// USDC this run never funded on-chain. Here the vendor pool is sized 200000
// (this run's on-chain leg) and the markup pool 3000000 (comfortably wide
// -- 2 funded tools' worth), and the target quotes 250000: amount alone
// (250000) exceeds the vendor pool, even though amount+markup (1750000)
// would easily fit a combined pool of 3200000. The call must be blocked at
// the vendor pool, never reaching PayTargetFromWallet2.
func TestX402RunLevelVendorPoolBoundsRealSpendIndependentlyOfMarkupHeadroom(t *testing.T) {
	var targetHits int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&targetHits, 1)
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"TARGETADDR","asset":"10458941","maxAmountRequired":"250000"}]}`))
	}))
	defer target.Close()

	node := models.WorkflowNode{ID: "x1", Type: models.NodeTypeTool402, Endpoint: target.URL}
	rc := engine.NewRunContext("r1", nil)
	aw := models.AgentWallet{}
	usdcSigner := &mockUSDCGroupSigner{group: []string{"g0", "g1"}, idx: 0}

	// Minimal stand-ins for newRunLevelLedger's real semantics (reject if
	// amount exceeds what remains, otherwise decrement) -- runner.go's own
	// pool implementation isn't exported to this package's tests.
	newFakePool := func(budget int64) nodes.PaymentLedger {
		remaining := budget
		return nodes.PaymentLedger{
			Reserve: func(_ context.Context, amount int64) error {
				if amount > remaining {
					return fmt.Errorf("pool exhausted: need %d, %d left of %d", amount, remaining, budget)
				}
				remaining -= amount
				return nil
			},
			Commit:  func(context.Context, string, int64, string) {},
			Release: func(_ context.Context, amount int64) { remaining += amount },
		}
	}
	vendorPool := newFakePool(200000)
	markupPool := newFakePool(3000000)

	relayCfg := nodes.X402RelayConfig{
		RunFundingID:     "test-run-funding-solvency",
		RunFundedToolIDs: map[string]bool{"x1": true},
		Ledger:           nodes.RunLedger(vendorPool),
		MarkupLedger:     nodes.RunLedger(markupPool),
		Wallet2: nodes.Wallet2PayConfig{
			USDCSigner:                usdcSigner,
			PlatformWalletEncMnemonic: "platform-wallet-enc-mnemonic",
			USDCAssetID:               uint64(10458941),
			RelayNetwork:              "algorand:testnet",
		},
		RecordSettlement: func(context.Context, string, int64, bool) error { return nil },
	}

	_, err := nodes.ExecuteTool402V2(context.Background(), node, rc, aw, nil, relayCfg)
	var blocked *nodes.ErrBalanceBlocked
	if !errors.As(err, &blocked) {
		t.Fatalf("want *ErrBalanceBlocked from the undersized vendor pool, got %T: %v", err, err)
	}
	if got := atomic.LoadInt32(&targetHits); got != 1 {
		t.Fatalf("want exactly 1 hit to target (the unauthenticated probe only) -- no real payment should have been attempted, got %d", got)
	}
}

// TestX402RunLevelNilRecordSettlementDoesNotPanic is a regression test for
// a nil-func-call panic: executeTool402RunLevel called cfg.RecordSettlement
// unconditionally, contradicting the "nil-safe like every other ledger call
// site in this file" comment a few lines above it (which describes exactly
// this kind of guard applied to cfg.Ledger.Reserve, but not to
// RecordSettlement). A caller that sets RunFundingID/RunFundedToolIDs
// without also wiring RecordSettlement must not panic after a real payment
// has already been signed and sent -- it should alert loudly instead.
func TestX402RunLevelNilRecordSettlementDoesNotPanic(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Payment") != "" {
			w.Write([]byte(`{"data":"paid tool response"}`))
			return
		}
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"TARGETADDR","asset":"10458941","maxAmountRequired":"100000"}]}`))
	}))
	defer target.Close()

	node := models.WorkflowNode{ID: "x1", Type: models.NodeTypeTool402, Endpoint: target.URL}
	rc := engine.NewRunContext("r1", nil)
	aw := models.AgentWallet{}
	usdcSigner := &mockUSDCGroupSigner{group: []string{"g0", "g1"}, idx: 0}

	var committed bool
	ledger := nodes.PaymentLedger{
		Reserve: func(context.Context, int64) error { return nil },
		Commit:  func(context.Context, string, int64, string) { committed = true },
		Release: func(context.Context, int64) {},
	}

	relayCfg := nodes.X402RelayConfig{
		RunFundingID:     "test-run-funding-nil-record",
		RunFundedToolIDs: map[string]bool{"x1": true},
		Ledger:           nodes.RunLedger(ledger),
		Wallet2: nodes.Wallet2PayConfig{
			USDCSigner:                usdcSigner,
			PlatformWalletEncMnemonic: "platform-wallet-enc-mnemonic",
			USDCAssetID:               uint64(10458941),
			RelayNetwork:              "algorand:testnet",
		},
		RecordSettlement: nil,
	}

	paymentResult, err := nodes.ExecuteTool402V2(context.Background(), node, rc, aw, nil, relayCfg)
	if err != nil {
		t.Fatalf("want nil error -- a missing RecordSettlement doesn't undo a real settled payment, got %v", err)
	}
	if !committed {
		t.Fatal("want the reservation committed despite RecordSettlement being nil")
	}
	if paymentResult.SettledUSDMicros != 100000 {
		t.Fatalf("want SettledUSDMicros 100000, got %d", paymentResult.SettledUSDMicros)
	}
}

// TestX402RunFundedAgentWithUnfundedToolUsesPerCallPath is the direct
// regression test for the predicate mismatch bug: ExecuteTool402V2's
// dispatch used to route ANY v2 call into executeTool402RunLevel purely
// because RunFundingID was non-empty, even for a tool the run's up-front
// estimate never covered (e.g. its probe failed during reserveAndFundRun,
// so it's absent from RunFundedToolIDs). That drew the tool's real cost
// from an in-memory pool sized for OTHER tools. It must instead fall
// through to the existing per-call relay path, which reserves/pays/settles
// against the live DB balance independently.
func TestX402RunFundedAgentWithUnfundedToolUsesPerCallPath(t *testing.T) {
	relayHit := false
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relayHit = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer relay.Close()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"TARGETADDR","asset":"10458941","maxAmountRequired":"100000"}]}`))
	}))
	defer target.Close()

	node := models.WorkflowNode{ID: "unfunded-tool", Type: models.NodeTypeTool402, Endpoint: target.URL}
	rc := engine.NewRunContext("r1", nil)
	aw := models.AgentWallet{}
	usdcSigner := &mockUSDCGroupSigner{group: []string{"g0", "g1"}, idx: 0}

	relayCfg := nodes.X402RelayConfig{
		USDCSigner:               usdcSigner,
		PlatformSpendEncMnemonic: "platform-enc-mnemonic",
		ExpectedAssetID:          uint64(10458941),
		RelayBaseURL:             relay.URL,
		// RunFundingID set (this agent's run WAS funded), but this specific
		// tool's ID is absent from RunFundedToolIDs -- its probe failed
		// during estimation, so reserveAndFundRun never folded it in.
		RunFundingID:     "test-run-funding-mismatch",
		RunFundedToolIDs: map[string]bool{"some-other-tool": true},
		Ledger:           nodes.RunLedger{},
	}

	// The per-call relay path (executeTool402V2Relay) needs a configured
	// USDCSigner/PlatformSpendEncMnemonic to ever dial relay.URL (it
	// degrades to a no-op error response otherwise) -- aw/signer being
	// empty just means the unrelated legacy-dialect branch (never reached
	// here, this is a v2 target) has nothing to pay with. The point of
	// this test is which CODE PATH is taken, not whether the payment
	// against the relay ultimately succeeds (the relay returns 500 above).
	_, _ = nodes.ExecuteTool402V2(context.Background(), node, rc, aw, nil, relayCfg)

	if !relayHit {
		t.Fatal("want the unfunded tool to route through the per-call relay path (executeTool402V2Relay), not the run-level pool")
	}
}

// TestParseMaxAmountRequiredAsMicrosRejectsOversizedFloat reproduces a
// money-correctness bug: a target's 402 challenge encoding maxAmountRequired
// as a JSON number (not string) at or beyond int64's range used to convert
// via a bare int64(t) cast, which is implementation-defined for
// out-of-range floats -- in practice yielding a large negative int64 that
// still reported ok=true. That negative "amount" would sail past
// reserveAndFundRun's ceiling check (amount > ceiling is false for a
// negative number) and reach store.ReserveCredits as a negative
// reservation, which reads as a credit INCREASE rather than a decrease.
func TestParseMaxAmountRequiredAsMicrosRejectsOversizedFloat(t *testing.T) {
	if _, ok := nodes.ParseMaxAmountRequiredAsMicros(1e20); ok {
		t.Fatal("want ok=false for a float far beyond int64's range, got ok=true")
	}
	if _, ok := nodes.ParseMaxAmountRequiredAsMicros(math.Pow(2, 63)); ok {
		t.Fatal("want ok=false for a float at int64's overflow boundary, got ok=true")
	}
	// Sanity: a large but legitimate whole-number quote still parses.
	micros, ok := nodes.ParseMaxAmountRequiredAsMicros(float64(500_000_000)) // $500
	if !ok || micros != 500_000_000 {
		t.Fatalf("want ok=true micros=500000000 for a legitimate large quote, got ok=%v micros=%d", ok, micros)
	}
}

// runFundedTargetServer is a real 402-then-200 target for the run-funded
// path: it quotes on an unpaid request and returns body on a paid one.
func runFundedTargetServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Payment") != "" || r.Header.Get("Payment-Signature") != "" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(body))
			return
		}
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[{"scheme":"exact","payTo":"TARGETADDR","asset":"10458941","maxAmountRequired":"100000"}]}`))
	}))
}

// TestX402RunLevelReportsRunFundingTxID pins the fix for a paid run being
// completely unauditable from the UI: a run-funded tool call settles no
// inbound payment of its own (the run's single up-front funding settlement
// already covered it), so the receipt it produced carried no tx id at all —
// the console showed "paid" with no explorer link, and the usage page's
// settlements list stayed permanently empty. The run funding's own tx id is
// that call's inbound leg and must be reported as such.
func TestX402RunLevelReportsRunFundingTxID(t *testing.T) {
	target := runFundedTargetServer(t, `{"data":"real result"}`)
	defer target.Close()

	node := models.WorkflowNode{ID: "x1", Type: models.NodeTypeTool402, Endpoint: target.URL}
	relayCfg := nodes.X402RelayConfig{
		RunFundingID:     "test-run-funding-1",
		RunFundingTxID:   "FUNDINGTXID123",
		RunFundedToolIDs: map[string]bool{"x1": true},
		ExpectedAssetID:  uint64(10458941),
		Ledger: nodes.RunLedger(nodes.PaymentLedger{
			Reserve: func(context.Context, int64) error { return nil },
			Commit:  func(context.Context, string, int64, string) {},
			Release: func(context.Context, int64) {},
		}),
		Wallet2: nodes.Wallet2PayConfig{
			USDCSigner:                &mockUSDCGroupSigner{group: []string{"g0", "g1"}, idx: 0},
			PlatformWalletEncMnemonic: "platform-wallet-enc-mnemonic",
			USDCAssetID:               uint64(10458941),
			RelayNetwork:              "algorand:testnet",
		},
		RecordSettlement: func(context.Context, string, int64, bool) error { return nil },
	}

	res, err := nodes.ExecuteTool402V2(context.Background(), node, engine.NewRunContext("r1", nil), models.AgentWallet{}, nil, relayCfg)
	if err != nil {
		t.Fatalf("want a successful run-funded call, got %v", err)
	}
	if res.TxID != "FUNDINGTXID123" {
		t.Fatalf("want the run funding tx id reported as this call's inbound leg, got %q", res.TxID)
	}
	wantURL := "https://lora.algokit.io/testnet/transaction/FUNDINGTXID123"
	if res.ExplorerURL != wantURL {
		t.Fatalf("want explorer URL %q, got %q", wantURL, res.ExplorerURL)
	}
	// Merged into the response body too, for the standalone-node console row
	// that renders the raw response map rather than a payment receipt.
	m, ok := res.Response.(map[string]any)
	if !ok {
		t.Fatalf("want a JSON object response, got %T", res.Response)
	}
	if m["txId"] != "FUNDINGTXID123" || m["explorerURL"] != wantURL {
		t.Fatalf("want txId/explorerURL merged into the response map, got %v", m)
	}
	// Decimal USDC, not raw micros: the console parseFloat's this field, so
	// micros rendered a one-cent call as "10000.000000 paid".
	if m["amount"] != "0.100000" {
		t.Fatalf("want amount as decimal USDC %q, got %v", "0.100000", m["amount"])
	}
}

// TestX402RunLevelReportsTxIDForNonObjectResponse covers the case the
// response-map merge alone can never handle: a target answering with a bare
// JSON array has nowhere to carry sibling fields, so the settlement id has
// to travel on the result struct or it is lost — which is exactly what the
// agent-attached path (provider.go's paymentReceipt) reads.
func TestX402RunLevelReportsTxIDForNonObjectResponse(t *testing.T) {
	target := runFundedTargetServer(t, `[{"symbol":"ALGO"}]`)
	defer target.Close()

	node := models.WorkflowNode{ID: "x1", Type: models.NodeTypeTool402, Endpoint: target.URL}
	relayCfg := nodes.X402RelayConfig{
		RunFundingID:     "test-run-funding-1",
		RunFundingTxID:   "FUNDINGTXID456",
		RunFundedToolIDs: map[string]bool{"x1": true},
		ExpectedAssetID:  uint64(31566704), // mainnet USDC
		Ledger: nodes.RunLedger(nodes.PaymentLedger{
			Reserve: func(context.Context, int64) error { return nil },
			Commit:  func(context.Context, string, int64, string) {},
			Release: func(context.Context, int64) {},
		}),
		Wallet2: nodes.Wallet2PayConfig{
			USDCSigner:                &mockUSDCGroupSigner{group: []string{"g0", "g1"}, idx: 0},
			PlatformWalletEncMnemonic: "platform-wallet-enc-mnemonic",
			USDCAssetID:               uint64(10458941),
			RelayNetwork:              "algorand:testnet",
		},
		RecordSettlement: func(context.Context, string, int64, bool) error { return nil },
	}

	res, err := nodes.ExecuteTool402V2(context.Background(), node, engine.NewRunContext("r1", nil), models.AgentWallet{}, nil, relayCfg)
	if err != nil {
		t.Fatalf("want a successful run-funded call, got %v", err)
	}
	if res.TxID != "FUNDINGTXID456" {
		t.Fatalf("want the run funding tx id reported even for a non-object response, got %q", res.TxID)
	}
	if res.ExplorerURL != "https://lora.algokit.io/mainnet/transaction/FUNDINGTXID456" {
		t.Fatalf("want a mainnet explorer URL for the mainnet USDC asset id, got %q", res.ExplorerURL)
	}
}

// TestTool402NonPaymentEndpointReceivesConfiguredParams pins the fix for a
// tool402 node pointed at an endpoint that turns out NOT to require payment.
// The probe used to send no body at all, and since a non-402 answer makes
// that probe the node's only request (its response becomes the node's
// result), every configured param was silently dropped and the target's
// complaint about the empty request was reported as a successful step.
// Confirmed live 2026-08-02 against api.scrape402.site/faucet/algo, which
// answered {"error":"Unexpected end of JSON input"} — its reaction to a
// bodyless POST, not to anything the node was configured with.
func TestTool402NonPaymentEndpointReceivesConfiguredParams(t *testing.T) {
	var gotBody, gotContentType string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody, gotContentType = string(b), r.Header.Get("Content-Type")
		if len(b) == 0 {
			// Exactly how the real endpoint behaved: parse an empty body,
			// fail, and report that instead of doing the work.
			w.Write([]byte(`{"error":"Unexpected end of JSON input"}`))
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer target.Close()

	node := models.WorkflowNode{
		ID: "x1", Type: models.NodeTypeTool402, Endpoint: target.URL, Method: http.MethodPost,
		CustomParams: []models.CustomParam{{Kind: "text", Name: "url", Value: "https://www.agent-mesh.app/"}},
	}

	res, err := nodes.ExecuteTool402V2(context.Background(), node, engine.NewRunContext("r1", nil), models.AgentWallet{}, nil, nodes.X402RelayConfig{})
	if err != nil {
		t.Fatalf("want the unpaid call to succeed, got %v", err)
	}
	if gotBody != `{"url":"https://www.agent-mesh.app/"}` {
		t.Fatalf("want the configured params sent as the request body, got %q", gotBody)
	}
	if gotContentType != "application/json" {
		t.Fatalf("want a JSON content type alongside the body, got %q", gotContentType)
	}
	m, ok := res.Response.(map[string]any)
	if !ok || m["ok"] != true {
		t.Fatalf("want the endpoint's real response, not its empty-request complaint, got %#v", res.Response)
	}
}

// TestTool402NonPaymentEndpointSendsMultipartContentType covers the same
// path for a file param: a multipart body carries its boundary in the
// content type, so the probe hardcoding application/json left the receiver
// unable to parse a single field.
func TestTool402NonPaymentEndpointSendsMultipartContentType(t *testing.T) {
	var gotFile, gotField string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, "unparseable multipart: "+err.Error(), http.StatusBadRequest)
			return
		}
		gotField = r.FormValue("caption")
		if f, hdr, err := r.FormFile("doc"); err == nil {
			defer f.Close()
			gotFile = hdr.Filename
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer target.Close()

	node := models.WorkflowNode{
		ID: "x1", Type: models.NodeTypeTool402, Endpoint: target.URL, Method: http.MethodPost,
		CustomParams: []models.CustomParam{
			{Kind: "text", Name: "caption", Value: "hello"},
			{Kind: "file", Name: "doc", FileName: "report.txt", Value: base64.StdEncoding.EncodeToString([]byte("file contents"))},
		},
	}

	res, err := nodes.ExecuteTool402V2(context.Background(), node, engine.NewRunContext("r1", nil), models.AgentWallet{}, nil, nodes.X402RelayConfig{})
	if err != nil {
		t.Fatalf("want the unpaid call to succeed, got %v", err)
	}
	if gotField != "hello" || gotFile != "report.txt" {
		t.Fatalf("want the multipart body parseable by the receiver, got field=%q file=%q", gotField, gotFile)
	}
	if m, ok := res.Response.(map[string]any); !ok || m["ok"] != true {
		t.Fatalf("want the endpoint's real response, got %#v", res.Response)
	}
}
