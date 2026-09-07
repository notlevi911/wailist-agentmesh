package handlers_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/agentmesh/backend/internal/api/handlers"
	"github.com/agentmesh/backend/internal/db"
	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/x402"
)

// TestMain relaxes the SSRF guard for this package's tests, mirroring the
// identical override in internal/engine/nodes and internal/engine's own
// TestMain — without it, the relay's SSRF check (added specifically because
// this route is public and unauthenticated) blocks every httptest.NewServer
// target (127.0.0.1), which is exactly what these tests use as fake
// downstream targets and a fake facilitator. No test in this package
// exercises the real SSRF-blocking validator.
func TestMain(m *testing.M) {
	nodes.SetURLValidatorForTest(func(string) error { return nil })
	os.Exit(m.Run())
}

func TestX402RelayNoPaymentMirrorsTargetPriceAsChallengeTag(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Payment") != "" {
			w.Write([]byte(`{"data":"paid response"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		json.NewEncoder(w).Encode(map[string]any{
			"x402Version": 2,
			"accepts": []map[string]any{{
				"scheme":            "exact",
				"network":           "algorand:SGO1GKSzyE7IEPItTxCByw9x8FmnrCDexi9/cOUJOiI=",
				"maxAmountRequired": "100000",
				"payTo":             "TARGETADDR",
				"asset":             "10458941",
			}},
		})
	}))
	defer target.Close()

	d := &handlers.Deps{
		PlatformWalletAddress: "PLATFORMADDR",
		USDCAssetID:           10458941,
		RelayNetwork:          "algorand:SGO1GKSzyE7IEPItTxCByw9x8FmnrCDexi9/cOUJOiI=",
		RelayFeePayer:         "FEEPAYERADDR",
	}
	req := httptest.NewRequest(http.MethodGet, "/x402/relay?target="+target.URL, nil)
	w := httptest.NewRecorder()

	d.X402Relay(w, req)

	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("want 402, got %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Accepts []map[string]any `json:"accepts"`
	}
	json.Unmarshal(w.Body.Bytes(), &body)
	if len(body.Accepts) != 1 {
		t.Fatalf("want 1 accepts entry, got %d", len(body.Accepts))
	}
	extra, _ := body.Accepts[0]["extra"].(map[string]any)
	if extra["tag"] != "x402-global-challenge" {
		t.Fatalf("want tag x402-global-challenge in extra, got %v", extra)
	}
	if body.Accepts[0]["payTo"] != "PLATFORMADDR" {
		t.Fatalf("want payTo=PLATFORMADDR (our own wallet, not the target's), got %v", body.Accepts[0]["payTo"])
	}
	if body.Accepts[0]["amount"] != "100000" {
		t.Fatalf("want price mirrored from target (100000), got %v", body.Accepts[0]["amount"])
	}
	_ = x402.PaymentPayload{} // referenced so import stays used once payment-path test is added below
}

// TestX402RelayWithNoTargetIssuesFixedSelfChallenge is a reproduce-then-fix
// regression test: a bare /x402/relay request (no ?target= at all) used to
// return a flat 400 "target query param required" -- meaning there was no
// stable, real, always-answering 402 challenge anywhere on this route for a
// Bazaar crawler to ever find and catalog (every other challenge this
// handler issues varies per-call, keyed off whatever ?target= was given).
// Confirms the bare route now issues a real, fixed 402 challenge instead.
func TestX402RelayWithNoTargetIssuesFixedSelfChallenge(t *testing.T) {
	d := &handlers.Deps{
		PlatformWalletAddress: "PLATFORMADDR",
		USDCAssetID:           10458941,
		RelayNetwork:          "algorand:SGO1GKSzyE7IEPItTxCByw9x8FmnrCDexi9/cOUJOiI=",
		RelayFeePayer:         "FEEPAYERADDR",
		FrontendURL:           "https://api.example.test",
	}
	req := httptest.NewRequest(http.MethodGet, "/x402/relay", nil)
	w := httptest.NewRecorder()

	d.X402Relay(w, req)

	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("want 402, got %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Accepts []map[string]any `json:"accepts"`
	}
	json.Unmarshal(w.Body.Bytes(), &body)
	if len(body.Accepts) != 1 {
		t.Fatalf("want 1 accepts entry, got %d", len(body.Accepts))
	}
	if body.Accepts[0]["payTo"] != "PLATFORMADDR" {
		t.Fatalf("want payTo=PLATFORMADDR, got %v", body.Accepts[0]["payTo"])
	}
	if body.Accepts[0]["amount"] != "10000" {
		t.Fatalf("want the fixed $0.01 self-listing price, got %v", body.Accepts[0]["amount"])
	}
	if body.Accepts[0]["resource"] != "https://api.example.test/api/x402/relay" {
		t.Fatalf("want resource=BaseURL+real path (matches every actually-cataloged mainnet resource's own-domain convention), got %v", body.Accepts[0]["resource"])
	}
	extra, _ := body.Accepts[0]["extra"].(map[string]any)
	if extra["tag"] != "x402-global-challenge" {
		t.Fatalf("want tag x402-global-challenge in extra, got %v", extra)
	}
	if w.Header().Get("Payment-Required") == "" {
		t.Fatal("want a Payment-Required header, same as every other challenge this handler issues")
	}
}

// TestX402RelayChallengeResourceIsTopLevelSpecShape is a reproduce-then-fix
// regression test for the actual Bazaar-cataloging root cause: the official
// @x402/core v2.20 SDK's PaymentRequired type has `resource` as a top-level
// ResourceInfo object ({url, description, ...}), not a string nested inside
// accepts[0] (that's the older, different v1 dialect's field). A real v2
// client's createPaymentPayload does `resource: paymentRequired.resource` --
// a verbatim top-level copy onto the outgoing payment payload -- and the
// facilitator's discovery extraction reads paymentPayload.resource.url to
// build a catalog entry. Before this fix, this handler never set the
// top-level field at all, so no compliant client had anything to echo back,
// meaning no settlement against this endpoint could ever be cataloged no
// matter how many times it was paid. Checked on both challenge branches
// (self-listing and target-mirroring) since both had the same gap.
func TestX402RelayChallengeResourceIsTopLevelSpecShape(t *testing.T) {
	d := &handlers.Deps{
		PlatformWalletAddress: "PLATFORMADDR",
		USDCAssetID:           10458941,
		RelayNetwork:          "algorand:SGO1GKSzyE7IEPItTxCByw9x8FmnrCDexi9/cOUJOiI=",
		RelayFeePayer:         "FEEPAYERADDR",
		FrontendURL:           "https://api.example.test",
	}
	req := httptest.NewRequest(http.MethodGet, "/x402/relay", nil)
	w := httptest.NewRecorder()

	d.X402Relay(w, req)

	var body struct {
		Resource map[string]any `json:"resource"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode challenge body: %v", err)
	}
	if body.Resource == nil {
		t.Fatal("want a top-level `resource` object on the challenge body (ResourceInfo shape), got none")
	}
	if body.Resource["url"] != "https://api.example.test/api/x402/relay" {
		t.Fatalf("want resource.url=BaseURL+real path, got %v", body.Resource["url"])
	}
	if body.Resource["description"] == nil || body.Resource["description"] == "" {
		t.Fatalf("want a non-empty resource.description, got %v", body.Resource["description"])
	}
	tags, _ := body.Resource["tags"].([]any)
	if len(tags) == 0 || tags[0] != "x402-global-challenge" {
		t.Fatalf("want resource.tags to include x402-global-challenge, got %v", body.Resource["tags"])
	}
}

// TestX402RelayBazaarExtensionIsSchemaValid is a reproduce-then-fix
// regression test: @x402/extensions v2.20's own discovery-extension
// validator (validateDiscoveryExtensionSpec) requires an `extensions.bazaar`
// declaration to carry BOTH `info` and `schema`, with `info.input.type` set
// to "http" or "mcp". Before this fix, this handler emitted `bazaar.info`
// with no `schema` sibling and no `input.type` at all -- which fails that
// validation unconditionally, so even a payment that correctly echoed back
// a top-level `resource` would still never catalog, because the facilitator
// rejects the malformed extension before ever building a catalog entry.
func TestX402RelayBazaarExtensionIsSchemaValid(t *testing.T) {
	d := &handlers.Deps{
		PlatformWalletAddress: "PLATFORMADDR",
		USDCAssetID:           10458941,
		RelayNetwork:          "algorand:SGO1GKSzyE7IEPItTxCByw9x8FmnrCDexi9/cOUJOiI=",
		RelayFeePayer:         "FEEPAYERADDR",
		FrontendURL:           "https://www.agent-mesh.app",
	}
	req := httptest.NewRequest(http.MethodGet, "/x402/relay", nil)
	w := httptest.NewRecorder()

	d.X402Relay(w, req)

	var body struct {
		Extensions struct {
			Bazaar struct {
				Info   map[string]any `json:"info"`
				Schema map[string]any `json:"schema"`
			} `json:"bazaar"`
		} `json:"extensions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode challenge body: %v", err)
	}
	if body.Extensions.Bazaar.Schema == nil {
		t.Fatal("want extensions.bazaar.schema to be present (required by validateDiscoveryExtensionSpec), got none")
	}
	input, _ := body.Extensions.Bazaar.Info["input"].(map[string]any)
	if input == nil || input["type"] != "http" {
		t.Fatalf("want extensions.bazaar.info.input.type=\"http\", got %v", input)
	}
	// Optional per the real spec's own schema validator (only `input` is in
	// `required`), but every genuinely-cataloged entry pulled live from
	// facilitator.goplausible.xyz/discovery/resources has one -- confirms
	// this handler emits it too, closing that gap.
	output, _ := body.Extensions.Bazaar.Info["output"].(map[string]any)
	if output == nil || output["type"] != "json" || output["example"] == nil {
		t.Fatalf("want extensions.bazaar.info.output={type:\"json\",example:{...}}, got %v", output)
	}

	// The validator's other exact-match requirement, asserted here on the
	// bytes this route really serves rather than only on the shared builder
	// (nodes.BazaarDiscoveryExtension, covered in that package's own tests):
	// the schema's method enum has to hold exactly the method info declares,
	// not a superset of it. Declaring "GET" against enum
	// ["GET","HEAD","DELETE"] is what the x402 Doctor reported as
	// "bazaar.schema method enum must match the declared method", and it cost
	// every settlement its catalog entry while verify and settle both
	// reported success.
	schemaProps, _ := body.Extensions.Bazaar.Schema["properties"].(map[string]any)
	inputSchema, _ := schemaProps["input"].(map[string]any)
	inputProps, _ := inputSchema["properties"].(map[string]any)
	methodSchema, _ := inputProps["method"].(map[string]any)
	enum, _ := methodSchema["enum"].([]any)
	if len(enum) != 1 || enum[0] != input["method"] {
		t.Fatalf("want the schema method enum to hold exactly the declared method %v, got %v", input["method"], methodSchema["enum"])
	}
}

// TestX402RelayAcceptsPaymentSignatureHeader is a reproduce-then-fix
// regression test: this relay previously only ever read the X-Payment
// header (raw, unencoded JSON). The real x402 v2 spec's canonical header is
// Payment-Signature, base64-encoded JSON -- what the official @x402/core
// client sends by default, and what reference resource-server
// implementations (e.g. the Tendril example server) check first. Without
// accepting it, no real external payer using a standard v2 client could
// ever reach this relay at all -- only this codebase's own internal callers
// (which all happen to use X-Payment) could pay it. Confirms a
// Payment-Signature-only request (no X-Payment at all) now settles
// identically to the pre-existing X-Payment path.
func TestX402RelayAcceptsPaymentSignatureHeader(t *testing.T) {
	store := newTestStoreForHandlers(t)
	inboundTxID := fmt.Sprintf("PAYMENT-SIGNATURE-TX-%d", time.Now().UnixNano())

	facilitator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/verify" {
			json.NewEncoder(w).Encode(map[string]any{"isValid": true})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "transaction": inboundTxID})
	}))
	defer facilitator.Close()

	d := &handlers.Deps{
		Store:                     store,
		PlatformWalletAddress:     "PLATFORMADDR",
		PlatformWalletEncMnemonic: "enc-mnemonic",
		FacilitatorClient:         x402.NewFacilitatorClient(facilitator.URL),
		USDCAssetID:               10458941,
		RelayNetwork:              "algorand:testnet",
		RelayFeePayer:             "FEEPAYERADDR",
		FrontendURL:               "https://www.agent-mesh.app",
	}

	payloadJSON, _ := json.Marshal(map[string]any{"x402Version": 2, "scheme": "exact", "network": "algorand:testnet"})
	req := httptest.NewRequest(http.MethodGet, "/x402/relay", nil)
	// Payment-Signature only -- deliberately no X-Payment at all, matching
	// what a real v2-compliant client actually sends.
	req.Header.Set("Payment-Signature", base64.StdEncoding.EncodeToString(payloadJSON))
	w := httptest.NewRecorder()

	d.X402Relay(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 via Payment-Signature header alone, got %d: %s", w.Code, w.Body.String())
	}
}

// TestX402RelayRejectsMalformedPaymentSignatureHeader confirms invalid
// base64 in Payment-Signature fails loudly (400) rather than silently
// falling through to the no-payment challenge path, which would mask a
// real caller's broken payment as if they'd never tried to pay at all.
func TestX402RelayRejectsMalformedPaymentSignatureHeader(t *testing.T) {
	d := &handlers.Deps{FrontendURL: "https://www.agent-mesh.app"}
	req := httptest.NewRequest(http.MethodGet, "/x402/relay", nil)
	req.Header.Set("Payment-Signature", "not-valid-base64!!!")
	w := httptest.NewRecorder()

	d.X402Relay(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for malformed Payment-Signature, got %d: %s", w.Code, w.Body.String())
	}
}

// TestX402RelaySelfSettleSetsResourceOnPayloadRegardlessOfCaller is a
// reproduce-then-fix regression test for the deepest layer of the Bazaar
// cataloging bug: even after the challenge itself carried a correct
// top-level `resource` (see TestX402RelayChallengeResourceIsTopLevelSpecShape),
// the facilitator's discovery extraction reads resource/extensions off the
// PAYMENT PAYLOAD the caller sends -- not off anything the resource server
// declares in PaymentRequirements. x402.PaymentPayload had no Resource/
// Extensions fields at all before this fix, so even a caller that DID send
// them got them silently dropped on decode (Go ignores unknown JSON
// fields), and this codebase's own internal engine self-call (tool402.go)
// never sent them in the first place. Fixed by having the relay set them
// authoritatively, server-side, regardless of what the caller's payload
// carried -- this test sends a payload with neither field set (the exact
// shape tool402.go sent before its own matching fix) and confirms the
// facilitator still receives a fully-formed resource/extensions pair.
func TestX402RelaySelfSettleSetsResourceOnPayloadRegardlessOfCaller(t *testing.T) {
	// Unique per run (like every sibling settlement test in this file) --
	// RecordInboundSettlement dedups on this txid, so a hardcoded value
	// would 409 on any second run against the same persistent test DB.
	inboundTxID := fmt.Sprintf("SELF-RESOURCE-TX-%d", time.Now().UnixNano())
	var capturedPayload map[string]any
	facilitator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			PaymentPayload map[string]any `json:"paymentPayload"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path == "/verify" {
			capturedPayload = body.PaymentPayload
			json.NewEncoder(w).Encode(map[string]any{"isValid": true})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "transaction": inboundTxID})
	}))
	defer facilitator.Close()

	store := newTestStoreForHandlers(t) // TEST_DATABASE_URL-gated, see helper below
	d := &handlers.Deps{
		PlatformWalletAddress: "PLATFORMADDR",
		USDCAssetID:           10458941,
		RelayNetwork:          "algorand:SGO1GKSzyE7IEPItTxCByw9x8FmnrCDexi9/cOUJOiI=",
		RelayFeePayer:         "FEEPAYERADDR",
		FrontendURL:           "https://api.example.test",
		FacilitatorClient:     x402.NewFacilitatorClient(facilitator.URL),
		Store:                 store,
	}

	// Deliberately the exact minimal shape tool402.go's engine self-call
	// sent before its own matching fix -- no resource, no extensions.
	minimalPayload := `{"x402Version":2,"scheme":"exact","network":"algorand:SGO1GKSzyE7IEPItTxCByw9x8FmnrCDexi9/cOUJOiI=","payload":{"paymentGroup":["AAAA"],"paymentIndex":0}}`
	req := httptest.NewRequest(http.MethodGet, "/x402/relay", nil)
	req.Header.Set("X-Payment", minimalPayload)
	w := httptest.NewRecorder()

	d.X402Relay(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if capturedPayload == nil {
		t.Fatal("facilitator never received a paymentPayload")
	}
	resource, _ := capturedPayload["resource"].(map[string]any)
	if resource == nil || resource["url"] != "https://api.example.test/api/x402/relay" {
		t.Fatalf("want the facilitator to receive payload.resource despite the caller never sending one, got %v", capturedPayload["resource"])
	}
	extensions, _ := capturedPayload["extensions"].(map[string]any)
	if extensions == nil || extensions["bazaar"] == nil {
		t.Fatalf("want the facilitator to receive payload.extensions.bazaar despite the caller never sending one, got %v", capturedPayload["extensions"])
	}
}

// TestX402RelaySelfSettleSendsSchemaValidV2Payload pins the two fields that
// made every settlement this codebase ever performed invalid against the real
// published v2 schemas, while leaving the settlement itself working (the AVM
// scheme mechanism reads neither field, so real money moved every time):
//
//   - paymentPayload.accepted -- a REQUIRED field of @x402-avm/core's
//     PaymentPayloadV2Schema, never sent at all before this fix.
//   - paymentRequirements.maxTimeoutSeconds -- typed z.number().positive(),
//     but omitted from this handler's requirements literal, so Go's zero
//     value went out on the wire while the public challenge advertised 300.
//
// Both are exactly the kind of defect a consumer that parses the payload
// (the Bazaar discovery catalog being the known one) rejects silently.
func TestX402RelaySelfSettleSendsSchemaValidV2Payload(t *testing.T) {
	inboundTxID := fmt.Sprintf("SELF-SCHEMA-TX-%d", time.Now().UnixNano())
	var captured struct {
		PaymentPayload      map[string]any `json:"paymentPayload"`
		PaymentRequirements map[string]any `json:"paymentRequirements"`
	}
	facilitator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/verify" {
			json.NewDecoder(r.Body).Decode(&captured)
			json.NewEncoder(w).Encode(map[string]any{"isValid": true})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "transaction": inboundTxID})
	}))
	defer facilitator.Close()

	store := newTestStoreForHandlers(t)
	d := &handlers.Deps{
		PlatformWalletAddress: "PLATFORMADDR",
		USDCAssetID:           10458941,
		RelayNetwork:          "algorand:SGO1GKSzyE7IEPItTxCByw9x8FmnrCDexi9/cOUJOiI=",
		RelayFeePayer:         "FEEPAYERADDR",
		FrontendURL:           "https://www.agent-mesh.app",
		FacilitatorClient:     x402.NewFacilitatorClient(facilitator.URL),
		Store:                 store,
	}

	// Same minimal caller payload as the sibling test above: no accepted, no
	// resource, no extensions. The resource server must supply all of them.
	minimalPayload := `{"x402Version":2,"scheme":"exact","network":"algorand:SGO1GKSzyE7IEPItTxCByw9x8FmnrCDexi9/cOUJOiI=","payload":{"paymentGroup":["AAAA"],"paymentIndex":0}}`
	req := httptest.NewRequest(http.MethodGet, "/x402/relay", nil)
	req.Header.Set("X-Payment", minimalPayload)
	w := httptest.NewRecorder()

	d.X402Relay(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	accepted, _ := captured.PaymentPayload["accepted"].(map[string]any)
	if accepted == nil {
		t.Fatalf("want payload.accepted (required by PaymentPayloadV2Schema), got %v", captured.PaymentPayload["accepted"])
	}
	for _, field := range []string{"scheme", "network", "amount", "asset", "payTo"} {
		if s, _ := accepted[field].(string); s == "" {
			t.Fatalf("want payload.accepted.%s to be a non-empty string, got %v", field, accepted[field])
		}
	}
	// z.number().positive() -- zero is a validation failure, not a default.
	if secs, _ := accepted["maxTimeoutSeconds"].(float64); secs <= 0 {
		t.Fatalf("want payload.accepted.maxTimeoutSeconds > 0, got %v", accepted["maxTimeoutSeconds"])
	}
	if secs, _ := captured.PaymentRequirements["maxTimeoutSeconds"].(float64); secs <= 0 {
		t.Fatalf("want paymentRequirements.maxTimeoutSeconds > 0, got %v", captured.PaymentRequirements["maxTimeoutSeconds"])
	}
	// The v2 requirements schema has no resource/description/mimeType of its
	// own -- those live on the payload's ResourceInfo. accepted must not
	// smuggle the v1 fields back in, or it stops being the shape it claims.
	for _, v1Only := range []string{"resource", "description", "mimeType", "extensions", "maxAmountRequired"} {
		if _, present := accepted[v1Only]; present {
			t.Fatalf("want payload.accepted to carry only PaymentRequirementsV2 fields, found v1-only %q", v1Only)
		}
	}
}

// TestX402RelaySelfSettleRecordsRealSettlement confirms a real payment
// against the bare-route fixed listing actually settles through the
// facilitator (same Verify/Settle calls every other settlement uses) and
// gets recorded, rather than being a no-op or an error.
func TestX402RelaySelfSettleRecordsRealSettlement(t *testing.T) {
	store := newTestStoreForHandlers(t)

	inboundTxID := fmt.Sprintf("SELF-TX-%d", time.Now().UnixNano())

	var verifyReqs, settleReqs struct {
		PaymentRequirements struct {
			MaxAmountRequired string `json:"amount"`
			Resource          string `json:"resource"`
		} `json:"paymentRequirements"`
	}
	facilitator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path == "/verify" {
			json.Unmarshal(body, &verifyReqs)
			json.NewEncoder(w).Encode(map[string]any{"isValid": true})
			return
		}
		json.Unmarshal(body, &settleReqs)
		json.NewEncoder(w).Encode(map[string]any{"success": true, "transaction": inboundTxID})
	}))
	defer facilitator.Close()

	d := &handlers.Deps{
		Store:                     store,
		PlatformWalletAddress:     "PLATFORMADDR",
		PlatformWalletEncMnemonic: "enc-mnemonic",
		FacilitatorClient:         x402.NewFacilitatorClient(facilitator.URL),
		USDCAssetID:               10458941,
		RelayNetwork:              "algorand:testnet",
		RelayFeePayer:             "FEEPAYERADDR",
		FrontendURL:               "https://api.example.test",
	}

	payload, _ := json.Marshal(map[string]any{"x402Version": 2, "scheme": "exact", "network": "algorand:testnet"})
	req := httptest.NewRequest(http.MethodGet, "/x402/relay", nil)
	req.Header.Set("X-Payment", string(payload))
	w := httptest.NewRecorder()

	d.X402Relay(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if verifyReqs.PaymentRequirements.MaxAmountRequired != "10000" {
		t.Fatalf("want facilitator Verify called with the fixed $0.01 price, got %q", verifyReqs.PaymentRequirements.MaxAmountRequired)
	}
	if settleReqs.PaymentRequirements.MaxAmountRequired != "10000" {
		t.Fatalf("want facilitator Settle called with the fixed $0.01 price, got %q", settleReqs.PaymentRequirements.MaxAmountRequired)
	}
	if settleReqs.PaymentRequirements.Resource != "https://api.example.test/api/x402/relay" {
		t.Fatalf("want Resource=BaseURL+real path sent to the facilitator (not just the challenge), got %q", settleReqs.PaymentRequirements.Resource)
	}

	row, err := store.GetX402RelaySettlementByInboundTx(context.Background(), inboundTxID)
	if err != nil {
		t.Fatalf("want to find the recorded ledger row: %v", err)
	}
	if row.AmountAssetMicros != 10000 {
		t.Fatalf("want ledger row to record the fixed amount (10000), got %d", row.AmountAssetMicros)
	}
	if row.TargetURL != "self" {
		t.Fatalf("want target_url=\"self\" (fixed label, distinct from real per-target rows), got %q", row.TargetURL)
	}
}

// TestX402RelayAcceptsTargetAmountFieldDialect is a reproduce-then-fix
// regression test for the real bug found live against our own Prism-schema
// demo merchant (backend/cmd/x402demo) on 2026-07-31: its challenge uses
// `amount` (the current real-world x402 dialect — confirmed against Prism's
// own live endpoint and the official @x402/core v2.20 SDK), not
// `maxAmountRequired` (this codebase's own historical convention). The
// relay's own outward-facing challenge now emits `amount` too, matching
// GoPlausible's facilitator wire format. fetchTargetPriceQuote must still
// correctly parse a target
// that only sends `amount`.
func TestX402RelayAcceptsTargetAmountFieldDialect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		json.NewEncoder(w).Encode(map[string]any{
			"x402Version": 2,
			"accepts": []map[string]any{{
				"scheme":  "exact",
				"network": "algorand:SGO1GKSzyE7IEPItTxCByw9x8FmnrCDexi9/cOUJOiI=",
				"amount":  "250000",
				"payTo":   "TARGETADDR",
				"asset":   "10458941",
			}},
		})
	}))
	defer target.Close()

	d := &handlers.Deps{
		PlatformWalletAddress: "PLATFORMADDR",
		USDCAssetID:           10458941,
		RelayNetwork:          "algorand:SGO1GKSzyE7IEPItTxCByw9x8FmnrCDexi9/cOUJOiI=",
		RelayFeePayer:         "FEEPAYERADDR",
	}
	req := httptest.NewRequest(http.MethodGet, "/x402/relay?target="+target.URL, nil)
	w := httptest.NewRecorder()

	d.X402Relay(w, req)

	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("want 402, got %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Accepts []map[string]any `json:"accepts"`
	}
	json.Unmarshal(w.Body.Bytes(), &body)
	if len(body.Accepts) != 1 || body.Accepts[0]["amount"] != "250000" {
		t.Fatalf("want price parsed from target's `amount` field and mirrored as our own amount=250000, got %+v", body.Accepts)
	}
}

// TestX402RunFundingInfoReturnsStaticJSON pins the contract of the static,
// informational route FundRunReserve's PaymentRequirements.Resource points
// at (runfund.go: cfg.PublicBaseURL + "/x402/relay/run-funding") — a real,
// reachable route on our own domain, matching what a real Bazaar-catalog
// crawler would expect to find there. The exact path is load-bearing (it's
// embedded in every run-level pre-fund's on-chain payment requirements), so
// a 200 with the expected static shape is worth pinning even though there's
// no payment logic behind it.
func TestX402RunFundingInfoReturnsStaticJSON(t *testing.T) {
	d := &handlers.Deps{}
	req := httptest.NewRequest(http.MethodGet, "/x402/relay/run-funding", nil)
	w := httptest.NewRecorder()

	d.X402RunFundingInfo(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("want valid JSON body: %v", err)
	}
	if body["description"] == "" {
		t.Fatalf("want a non-empty description field, got %v", body)
	}
}

func newTestStoreForHandlers(t *testing.T) *db.Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	store, err := db.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	return store
}

// signCall captures one invocation of fakeUSDCSigner.SignUSDCPaymentSingle
// (the outbound-to-third-party leg's signer), so tests can assert on exactly
// what the relay actually signed and broadcast for the outbound
// platform-wallet payment.
type signCall struct {
	payTo        string
	assetID      uint64
	amountMicros uint64
}

type fakeUSDCSigner struct {
	group []string
	idx   int
	err   error

	calls []signCall
}

func (f *fakeUSDCSigner) SignUSDCPaymentGroup(_ context.Context, _, payTo string, assetID, amountMicros uint64, _ string) ([]string, int, error) {
	f.calls = append(f.calls, signCall{payTo: payTo, assetID: assetID, amountMicros: amountMicros})
	return f.group, f.idx, f.err
}

func (f *fakeUSDCSigner) SignUSDCPaymentSingle(_ context.Context, _, payTo string, assetID, amountMicros uint64) ([]string, int, error) {
	f.calls = append(f.calls, signCall{payTo: payTo, assetID: assetID, amountMicros: amountMicros})
	return f.group, f.idx, f.err
}

func TestX402RelayPaysTargetFromPlatformWalletAfterInboundSettles(t *testing.T) {
	var targetGotXPayment string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h := r.Header.Get("X-Payment"); h != "" {
			targetGotXPayment = h
			w.Write([]byte(`{"data":"paid response from target"}`))
			return
		}
		w.WriteHeader(http.StatusPaymentRequired)
		json.NewEncoder(w).Encode(map[string]any{
			"accepts": []map[string]any{{"payTo": "TARGETADDR", "asset": "10458941", "maxAmountRequired": "50000"}},
		})
	}))
	defer target.Close()

	store := newTestStoreForHandlers(t) // TEST_DATABASE_URL-gated, see helper below

	// inbound_tx_id has a uniqueness constraint; target.URL's ephemeral port
	// can be reused across test runs against a long-lived Postgres, so a
	// txid keyed off it alone is not collision-proof over time -- suffix
	// with a monotonic timestamp (matches the fix applied to
	// x402_orchestrator_integration_test.go).
	inboundTxID := fmt.Sprintf("INBOUND-TX-%s-%d", target.URL, time.Now().UnixNano())

	// Captures the paymentRequirements the relay sent on /verify and /settle
	// so the test can assert the real target-quoted amount (50000, from the
	// fake target above) was actually threaded through and enforced, rather
	// than the previous hardcoded-0/no-enforcement behavior.
	var verifyReqs, settleReqs struct {
		PaymentRequirements struct {
			MaxAmountRequired string `json:"amount"`
		} `json:"paymentRequirements"`
	}
	facilitator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path == "/verify" {
			json.Unmarshal(body, &verifyReqs)
			json.NewEncoder(w).Encode(map[string]any{"isValid": true})
			return
		}
		json.Unmarshal(body, &settleReqs)
		json.NewEncoder(w).Encode(map[string]any{"success": true, "transaction": inboundTxID})
	}))
	defer facilitator.Close()

	d := &handlers.Deps{
		Store:                     store,
		PlatformWalletAddress:     "PLATFORMADDR",
		PlatformWalletEncMnemonic: "enc-mnemonic",
		FacilitatorClient:         x402.NewFacilitatorClient(facilitator.URL),
		USDCAssetID:               10458941,
		RelayNetwork:              "algorand:testnet",
		RelayFeePayer:             "FEEPAYERADDR",
		USDCSigner:                &fakeUSDCSigner{group: []string{"g0", "g1"}, idx: 0},
	}

	payload, _ := json.Marshal(map[string]any{"x402Version": 2, "scheme": "exact", "network": "algorand:testnet"})
	req := httptest.NewRequest(http.MethodGet, "/x402/relay?target="+target.URL, nil)
	req.Header.Set("X-Payment", string(payload))
	w := httptest.NewRecorder()

	d.X402Relay(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if targetGotXPayment == "" {
		t.Fatal("want relay to have paid the target with its own X-Payment header")
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("paid response from target")) {
		t.Fatalf("want target's response relayed back, got %s", w.Body.String())
	}

	if verifyReqs.PaymentRequirements.MaxAmountRequired != "50000" {
		t.Fatalf("want facilitator Verify called with MaxAmountRequired=50000 (the target's real quote, for price enforcement), got %q", verifyReqs.PaymentRequirements.MaxAmountRequired)
	}
	if settleReqs.PaymentRequirements.MaxAmountRequired != "50000" {
		t.Fatalf("want facilitator Settle called with MaxAmountRequired=50000, got %q", settleReqs.PaymentRequirements.MaxAmountRequired)
	}

	row, err := store.GetX402RelaySettlementByInboundTx(context.Background(), inboundTxID)
	if err != nil {
		t.Fatalf("want to find the recorded ledger row: %v", err)
	}
	if row.AmountAssetMicros != 50000 {
		t.Fatalf("want ledger row to record the real settled amount (50000), got %d", row.AmountAssetMicros)
	}
}

// TestX402RelayForwardsConfiguredTargetMethodAndBody is a reproduce-then-fix
// regression test for the real bug found live against Prism
// (https://prism-99h2.onrender.com/resume-screen-accurate) on 2026-07-31: a
// POST-only x402 target 404s a bare GET before it ever considers payment
// state, so the relay's pre-existing hardcoded-GET/nil-body target fetch
// could never reach it at all. The fake target below mimics that shape (404
// on GET, real handling on POST+body) and asserts BOTH the unauthenticated
// challenge-mirror leg and the paid leg actually reach target as POST with
// the caller-supplied body -- confirming X-Relay-Method/X-Relay-Body (set
// once per relay call, mirroring how X-Payment already works) are threaded
// all the way through relayInboundChallenge, relaySettleAndForward, and
// payTargetAndRespond, not just one of the three.
func TestX402RelayForwardsConfiguredTargetMethodAndBody(t *testing.T) {
	const wantBody = `{"task_description":"test","files":[]}`
	var gotChallengeBody, gotPaidBody string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		b, _ := io.ReadAll(r.Body)
		if h := r.Header.Get("X-Payment"); h != "" {
			gotPaidBody = string(b)
			w.Write([]byte(`{"data":"paid response from target"}`))
			return
		}
		gotChallengeBody = string(b)
		w.WriteHeader(http.StatusPaymentRequired)
		json.NewEncoder(w).Encode(map[string]any{
			"accepts": []map[string]any{{"payTo": "TARGETADDR", "asset": "10458941", "maxAmountRequired": "50000"}},
		})
	}))
	defer target.Close()

	store := newTestStoreForHandlers(t)
	inboundTxID := fmt.Sprintf("INBOUND-TX-METHODBODY-%s-%d", target.URL, time.Now().UnixNano())

	facilitator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/verify" {
			json.NewEncoder(w).Encode(map[string]any{"isValid": true})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "transaction": inboundTxID})
	}))
	defer facilitator.Close()

	d := &handlers.Deps{
		Store:                     store,
		PlatformWalletAddress:     "PLATFORMADDR",
		PlatformWalletEncMnemonic: "enc-mnemonic",
		FacilitatorClient:         x402.NewFacilitatorClient(facilitator.URL),
		USDCAssetID:               10458941,
		RelayNetwork:              "algorand:testnet",
		RelayFeePayer:             "FEEPAYERADDR",
		USDCSigner:                &fakeUSDCSigner{group: []string{"g0", "g1"}, idx: 0},
	}

	// Unauthenticated leg: relayInboundChallenge must also use the
	// configured method/body to even see a real 402 back from a POST-only
	// target, not a 404.
	challengeReq := httptest.NewRequest(http.MethodGet, "/x402/relay?target="+target.URL, nil)
	challengeReq.Header.Set("X-Relay-Method", http.MethodPost)
	challengeReq.Header.Set("X-Relay-Body", base64.StdEncoding.EncodeToString([]byte(wantBody)))
	challengeW := httptest.NewRecorder()
	d.X402Relay(challengeW, challengeReq)

	if challengeW.Code != http.StatusPaymentRequired {
		t.Fatalf("want 402 mirrored from the target's real POST-only challenge, got %d: %s", challengeW.Code, challengeW.Body.String())
	}
	if gotChallengeBody != wantBody {
		t.Fatalf("want target to receive the configured body on the unauthenticated leg, got %q", gotChallengeBody)
	}

	// Paid leg: same X-Relay-Method/X-Relay-Body, now alongside X-Payment.
	payload, _ := json.Marshal(map[string]any{"x402Version": 2, "scheme": "exact", "network": "algorand:testnet"})
	paidReq := httptest.NewRequest(http.MethodGet, "/x402/relay?target="+target.URL, nil)
	paidReq.Header.Set("X-Payment", string(payload))
	paidReq.Header.Set("X-Relay-Method", http.MethodPost)
	paidReq.Header.Set("X-Relay-Body", base64.StdEncoding.EncodeToString([]byte(wantBody)))
	paidW := httptest.NewRecorder()
	d.X402Relay(paidW, paidReq)

	if paidW.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", paidW.Code, paidW.Body.String())
	}
	if gotPaidBody != wantBody {
		t.Fatalf("want target to receive the configured body on the paid leg, got %q", gotPaidBody)
	}
	if !bytes.Contains(paidW.Body.Bytes(), []byte("paid response from target")) {
		t.Fatalf("want target's response relayed back, got %s", paidW.Body.String())
	}
}

// TestX402RelayUsesSingleQuoteForBothSettlementAndOutboundPayment is a
// regression test for a fund-drain vector: relaySettleAndForward used to
// fetch the target's price quote once (to enforce/record it), and
// payTargetAndRespond then independently re-fetched the SAME caller-supplied
// target URL a second time to learn what to actually sign and pay. Since
// `target` is arbitrary and attacker-controlled on this public,
// unauthenticated route, a malicious target could answer the first
// (price-enforcement) fetch with a cheap price and the second (pay-time)
// fetch with an inflated amount and/or a different payTo — causing the
// platform wallet to sign and broadcast a payment for more than was ever
// collected from the caller, to an address the caller chose.
//
// The fake target below tracks how many unauthenticated (no X-Payment)
// requests it has received and answers the first one cheaply
// (TARGETADDR-CHEAP / 50000) and every subsequent one expensively
// (ATTACKERADDR / 999000000). Under the old buggy code this is exactly two
// independent fetches — one in relaySettleAndForward, one in
// payTargetAndRespond — so the outbound payment would be signed for
// 999000000 to ATTACKERADDR while the ledger recorded only 50000. Under the
// fixed code there is only one fetch, reused for both purposes, so the
// signed outbound payment must match the cheap, recorded amount and address.
func TestX402RelayUsesSingleQuoteForBothSettlementAndOutboundPayment(t *testing.T) {
	var quoteFetches int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Payment") != "" {
			w.Write([]byte(`{"data":"paid response from target"}`))
			return
		}
		quoteFetches++
		w.WriteHeader(http.StatusPaymentRequired)
		if quoteFetches == 1 {
			json.NewEncoder(w).Encode(map[string]any{
				"accepts": []map[string]any{{"payTo": "TARGETADDR-CHEAP", "asset": "10458941", "maxAmountRequired": "50000"}},
			})
			return
		}
		// A malicious target would only do this on a second, pay-time-only
		// fetch that should never happen under the fix.
		json.NewEncoder(w).Encode(map[string]any{
			"accepts": []map[string]any{{"payTo": "ATTACKERADDR", "asset": "10458941", "maxAmountRequired": "999000000"}},
		})
	}))
	defer target.Close()

	store := newTestStoreForHandlers(t)

	inboundTxID := fmt.Sprintf("INBOUND-TX-SINGLEQUOTE-%s-%d", target.URL, time.Now().UnixNano())
	facilitator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path == "/verify" {
			_ = body
			json.NewEncoder(w).Encode(map[string]any{"isValid": true})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "transaction": inboundTxID})
	}))
	defer facilitator.Close()

	signer := &fakeUSDCSigner{group: []string{"g0", "g1"}, idx: 0}
	d := &handlers.Deps{
		Store:                     store,
		PlatformWalletAddress:     "PLATFORMADDR",
		PlatformWalletEncMnemonic: "enc-mnemonic",
		FacilitatorClient:         x402.NewFacilitatorClient(facilitator.URL),
		USDCAssetID:               10458941,
		RelayNetwork:              "algorand:testnet",
		RelayFeePayer:             "FEEPAYERADDR",
		USDCSigner:                signer,
	}

	payload, _ := json.Marshal(map[string]any{"x402Version": 2, "scheme": "exact", "network": "algorand:testnet"})
	req := httptest.NewRequest(http.MethodGet, "/x402/relay?target="+target.URL, nil)
	req.Header.Set("X-Payment", string(payload))
	w := httptest.NewRecorder()

	d.X402Relay(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	row, err := store.GetX402RelaySettlementByInboundTx(context.Background(), inboundTxID)
	if err != nil {
		t.Fatalf("want to find the recorded ledger row: %v", err)
	}
	if row.AmountAssetMicros != 50000 {
		t.Fatalf("want ledger row to record the cheap quoted amount (50000), got %d", row.AmountAssetMicros)
	}

	if quoteFetches != 1 {
		t.Fatalf("want exactly one price-quote fetch of the caller-supplied target per relay cycle, got %d — a second independent fetch is exactly the fund-drain gap this test guards against", quoteFetches)
	}

	if len(signer.calls) != 1 {
		t.Fatalf("want exactly one outbound sign call, got %d", len(signer.calls))
	}
	got := signer.calls[0]
	if uint64(row.AmountAssetMicros) != got.amountMicros {
		t.Fatalf("want the amount actually signed for the outbound payment (%d) to match the amount recorded in the ledger (%d) — a mismatch means the relay paid a different amount than it collected/recorded", got.amountMicros, row.AmountAssetMicros)
	}
	if got.payTo != "TARGETADDR-CHEAP" {
		t.Fatalf("want outbound payment signed to the same payTo used for the recorded settlement (TARGETADDR-CHEAP), got %q — this is the attacker-address-substitution half of the fund-drain vector", got.payTo)
	}
}

// TestX402RelayRejectsOutboundPaymentInWrongAsset is a regression test for a
// gap where payTargetAndRespond blindly trusted quote.Asset (parsed straight
// out of the target's own, caller-supplied, unauthenticated 402 response) as
// the asset id to sign and broadcast the outbound platform-wallet payment in
// — with no check that it matches d.USDCAssetID, the platform's own
// designated USDC asset id (already anchored and enforced on the inbound
// settlement side via reqs.Asset in relaySettleAndForward). A malicious
// target could consistently, in its single quote response, claim some other
// asset id it controls (or one that doesn't exist/has no value) and the
// relay would still sign and send a real payment in that asset.
//
// The fake target here claims asset "99999999" (not d.USDCAssetID,
// 10458941) consistently on every unauthenticated fetch — so this is not the
// two-different-fetches fund-drain vector from the test above, just a single
// quote that names the wrong asset throughout.
//
// The check now runs in relaySettleAndForward, before the facilitator's
// Verify/Settle are ever called — not just before paying the target in
// payTargetAndRespond. Checking it only in payTargetAndRespond (after
// RecordInboundSettlement) would have set X-Inbound-Settled on the response
// and let tool402.go bill the caller in full for a call that was never
// going to reach the target; catching it earlier means neither the inbound
// leg nor an outbound-settlement row exist at all — there is nothing to
// bill and nothing to reconcile.
func TestX402RelayRejectsOutboundPaymentInWrongAsset(t *testing.T) {
	var targetGotPaidRequest bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Payment") != "" {
			targetGotPaidRequest = true
			w.Write([]byte(`{"data":"paid response from target"}`))
			return
		}
		w.WriteHeader(http.StatusPaymentRequired)
		json.NewEncoder(w).Encode(map[string]any{
			"accepts": []map[string]any{{"payTo": "TARGETADDR", "asset": "99999999", "maxAmountRequired": "50000"}},
		})
	}))
	defer target.Close()

	store := newTestStoreForHandlers(t)

	inboundTxID := fmt.Sprintf("INBOUND-TX-WRONGASSET-%s-%d", target.URL, time.Now().UnixNano())
	var facilitatorHitPaths []string
	facilitator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		facilitatorHitPaths = append(facilitatorHitPaths, r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path == "/verify" {
			_ = body
			json.NewEncoder(w).Encode(map[string]any{"isValid": true})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "transaction": inboundTxID})
	}))
	defer facilitator.Close()

	signer := &fakeUSDCSigner{group: []string{"g0", "g1"}, idx: 0}
	d := &handlers.Deps{
		Store:                     store,
		PlatformWalletAddress:     "PLATFORMADDR",
		PlatformWalletEncMnemonic: "enc-mnemonic",
		FacilitatorClient:         x402.NewFacilitatorClient(facilitator.URL),
		USDCAssetID:               10458941, // does NOT match the target's claimed asset (99999999)
		RelayNetwork:              "algorand:testnet",
		RelayFeePayer:             "FEEPAYERADDR",
		USDCSigner:                signer,
	}

	payload, _ := json.Marshal(map[string]any{"x402Version": 2, "scheme": "exact", "network": "algorand:testnet"})
	req := httptest.NewRequest(http.MethodGet, "/x402/relay?target="+target.URL, nil)
	req.Header.Set("X-Payment", string(payload))
	w := httptest.NewRecorder()

	d.X402Relay(w, req)

	if w.Code == http.StatusOK {
		t.Fatalf("want relay to refuse to pay the target in a non-USDC asset, got 200: %s", w.Body.String())
	}
	if w.Header().Get("X-Inbound-Settled") == "true" {
		t.Fatal("want X-Inbound-Settled unset -- nothing settled, the caller must not be billed")
	}

	if len(facilitatorHitPaths) != 0 {
		t.Fatalf("want the facilitator (verify/settle) to never be called when the target's quoted asset is rejected up front, got calls to %v", facilitatorHitPaths)
	}
	if len(signer.calls) != 0 {
		t.Fatalf("want the USDC signer to never be called when the target's quoted asset does not match d.USDCAssetID, got %d call(s): %+v", len(signer.calls), signer.calls)
	}
	if targetGotPaidRequest {
		t.Fatal("want the relay to never send a paid request to the target when the quoted asset does not match d.USDCAssetID")
	}

	if _, err := store.GetX402RelaySettlementByInboundTx(context.Background(), inboundTxID); err == nil {
		t.Fatal("want no settlement row at all -- the inbound leg was never settled, so there is nothing to record")
	}
}

// TestX402RelayDoesNotBillWhenOutboundSigningFails is a regression test: the
// relay used to set X-Inbound-Settled as soon as the inbound leg settled,
// before the outbound payment to target was ever signed. If signing then
// failed (bad payTo, an algod outage, ...) the target received nothing at
// all, but the header was already set -- tool402.go would still commit the
// full amount, over-billing the caller for a call where no money reached
// the target and no submittable claim against the platform wallet was ever
// created. The header must only be set after SignUSDCPaymentGroup succeeds.
func TestX402RelayDoesNotBillWhenOutboundSigningFails(t *testing.T) {
	var targetGotPaidRequest bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Payment") != "" {
			targetGotPaidRequest = true
			w.Write([]byte(`{"data":"paid response from target"}`))
			return
		}
		w.WriteHeader(http.StatusPaymentRequired)
		json.NewEncoder(w).Encode(map[string]any{
			"accepts": []map[string]any{{"payTo": "TARGETADDR", "asset": "10458941", "maxAmountRequired": "50000"}},
		})
	}))
	defer target.Close()

	store := newTestStoreForHandlers(t)

	inboundTxID := fmt.Sprintf("INBOUND-TX-SIGNFAIL-%s-%d", target.URL, time.Now().UnixNano())
	facilitator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/verify" {
			json.NewEncoder(w).Encode(map[string]any{"isValid": true})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "transaction": inboundTxID})
	}))
	defer facilitator.Close()

	signer := &fakeUSDCSigner{err: fmt.Errorf("invalid receiver address")}
	d := &handlers.Deps{
		Store:                     store,
		PlatformWalletAddress:     "PLATFORMADDR",
		PlatformWalletEncMnemonic: "enc-mnemonic",
		FacilitatorClient:         x402.NewFacilitatorClient(facilitator.URL),
		USDCAssetID:               10458941,
		RelayNetwork:              "algorand:testnet",
		RelayFeePayer:             "FEEPAYERADDR",
		USDCSigner:                signer,
	}

	payload, _ := json.Marshal(map[string]any{"x402Version": 2, "scheme": "exact", "network": "algorand:testnet"})
	req := httptest.NewRequest(http.MethodGet, "/x402/relay?target="+target.URL, nil)
	req.Header.Set("X-Payment", string(payload))
	w := httptest.NewRecorder()

	d.X402Relay(w, req)

	if w.Code == http.StatusOK {
		t.Fatalf("want relay to report the signing failure, got 200: %s", w.Body.String())
	}
	if w.Header().Get("X-Inbound-Settled") == "true" {
		t.Fatal("want X-Inbound-Settled unset when outbound signing fails -- no signed payment group exists, so the caller must not be billed")
	}
	if targetGotPaidRequest {
		t.Fatal("want the relay to never send a paid request to the target when signing the outbound payment failed")
	}

	row, err := store.GetX402RelaySettlementByInboundTx(context.Background(), inboundTxID)
	if err != nil {
		t.Fatalf("want the inbound settlement row to still exist (that leg genuinely settled): %v", err)
	}
	if row.Status != "failed" {
		t.Fatalf("want the outbound leg recorded as failed, got status %q", row.Status)
	}
}

// TestX402RelayRecordsFailedWhenTargetRejectsOutboundPayment is a regression
// test: payTargetAndRespond used to record the outbound leg as "settled" as
// soon as the paid HTTP request to the target completed without a transport
// error, without ever checking the target's actual status code. A target
// that rejects the platform wallet's payment (e.g. it still returns 402, or
// errors with a 5xx) would then be recorded as a successful settlement even
// though the downstream leg the whole Orchestrator entry depends on never
// actually happened. The fake target below always answers a paid
// (X-Payment-bearing) request with 402 — simulating a rejected outbound
// payment — and the test asserts the ledger row ends up "failed", and that
// the relay surfaces the target's real (non-200) status code to the caller
// rather than claiming success.
func TestX402RelayRecordsFailedWhenTargetRejectsOutboundPayment(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		json.NewEncoder(w).Encode(map[string]any{
			"accepts": []map[string]any{{"payTo": "TARGETADDR", "asset": "10458941", "maxAmountRequired": "50000"}},
		})
	}))
	defer target.Close()

	store := newTestStoreForHandlers(t)

	inboundTxID := fmt.Sprintf("INBOUND-TX-REJECTED-%s-%d", target.URL, time.Now().UnixNano())
	facilitator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/verify" {
			json.NewEncoder(w).Encode(map[string]any{"isValid": true})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "transaction": inboundTxID})
	}))
	defer facilitator.Close()

	d := &handlers.Deps{
		Store:                     store,
		PlatformWalletAddress:     "PLATFORMADDR",
		PlatformWalletEncMnemonic: "enc-mnemonic",
		FacilitatorClient:         x402.NewFacilitatorClient(facilitator.URL),
		USDCAssetID:               10458941,
		RelayNetwork:              "algorand:testnet",
		RelayFeePayer:             "FEEPAYERADDR",
		USDCSigner:                &fakeUSDCSigner{group: []string{"g0", "g1"}, idx: 0},
	}

	payload, _ := json.Marshal(map[string]any{"x402Version": 2, "scheme": "exact", "network": "algorand:testnet"})
	req := httptest.NewRequest(http.MethodGet, "/x402/relay?target="+target.URL, nil)
	req.Header.Set("X-Payment", string(payload))
	w := httptest.NewRecorder()

	d.X402Relay(w, req)

	if w.Code == http.StatusOK {
		t.Fatalf("want the relay to surface the target's real rejection status, not claim success, got %d: %s", w.Code, w.Body.String())
	}

	row, err := store.GetX402RelaySettlementByInboundTx(context.Background(), inboundTxID)
	if err != nil {
		t.Fatalf("want to find the recorded ledger row: %v", err)
	}
	if row.Status != "failed" {
		t.Fatalf("want the outbound settlement recorded as failed when the target rejects the payment, got status %q", row.Status)
	}
}

// TestX402RelayForwardsTargetAuthHeader confirms a bearer the TARGET needs
// (Tendril's lease token, carried via X-Relay-Auth) reaches the target as a
// real Authorization header, and that its absence sets no Authorization
// header at all — this exercises the unauthenticated challenge-probe leg
// (relayInboundChallenge -> fetchTargetPriceQuote), which needs no
// facilitator or wallet signer to reach.
func TestX402RelayForwardsTargetAuthHeader(t *testing.T) {
	var gotAuth string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		json.NewEncoder(w).Encode(map[string]any{
			"x402Version": 2,
			"accepts": []map[string]any{{
				"scheme":            "exact",
				"network":           "algorand:SGO1GKSzyE7IEPItTxCByw9x8FmnrCDexi9/cOUJOiI=",
				"maxAmountRequired": "100000",
				"payTo":             "TARGETADDR",
				"asset":             "10458941",
			}},
		})
	}))
	defer target.Close()

	d := &handlers.Deps{
		PlatformWalletAddress: "PLATFORMADDR",
		USDCAssetID:           10458941,
		RelayNetwork:          "algorand:SGO1GKSzyE7IEPItTxCByw9x8FmnrCDexi9/cOUJOiI=",
		RelayFeePayer:         "FEEPAYERADDR",
	}

	req := httptest.NewRequest(http.MethodGet, "/x402/relay?target="+target.URL, nil)
	req.Header.Set("X-Relay-Auth", "tok")
	w := httptest.NewRecorder()
	d.X402Relay(w, req)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("want 402, got %d: %s", w.Code, w.Body.String())
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("target Authorization = %q, want %q", gotAuth, "Bearer tok")
	}

	req2 := httptest.NewRequest(http.MethodGet, "/x402/relay?target="+target.URL, nil)
	w2 := httptest.NewRecorder()
	d.X402Relay(w2, req2)
	if w2.Code != http.StatusPaymentRequired {
		t.Fatalf("want 402, got %d: %s", w2.Code, w2.Body.String())
	}
	if gotAuth != "" {
		t.Errorf("target Authorization = %q, want none without X-Relay-Auth", gotAuth)
	}
}
