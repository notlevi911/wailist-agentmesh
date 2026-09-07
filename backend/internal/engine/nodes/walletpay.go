package nodes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
)

// Wallet2PayConfig carries what's needed to sign and send a real outbound
// x402 payment from the platform's Wallet 2 settlement wallet to a real
// target. Used by BOTH the public relay handler (x402relay.go's
// payTargetAndRespond, for genuine external x402 clients) and the run-level
// in-process path (Task 5) — there is exactly one function that signs from
// Wallet 2's mnemonic; both callers hold their own copy of the same
// already-in-memory secret (threaded from main.go) and reach the same code.
type Wallet2PayConfig struct {
	USDCSigner                USDCGroupSigner
	PlatformWalletEncMnemonic string
	USDCAssetID               uint64
	RelayNetwork              string
	MaxRelayOutboundUSDMicros int64
	// ContentType is what the outbound body is encoded as. Empty means the
	// historical default, application/json. A multipart body must carry the
	// exact content type it was generated with, boundary included, or the
	// target cannot parse a single field of it.
	ContentType string
	// Authorization is a bearer the TARGET requires (Tendril's lease token,
	// for example) — carried here rather than in paymentHeaders below since
	// it authenticates against the target's own business logic, not the
	// x402 payment scheme.
	Authorization string
}

// TargetQuote is defined in tool402.go (used by ProbeX402Quote) — walletpay.go
// uses it, doesn't own it.

// Wallet2PayError carries the exact HTTP status the original inline
// handler used per failure kind, so payTargetAndRespond's refactored
// wrapper can reproduce identical response codes.
type Wallet2PayError struct {
	StatusCode int
	Msg        string
}

func (e *Wallet2PayError) Error() string { return e.Msg }

// Wallet2PayResult mirrors what payTargetAndRespond needs to reconstruct
// its existing HTTP response exactly. Signed becomes true the instant
// SignUSDCPaymentGroup succeeds — the exact moment the public handler sets
// X-Inbound-Settled today — and stays true regardless of what happens next
// (a target-request failure still counts as "money already committed",
// matching the existing billing philosophy documented in x402relay.go).
type Wallet2PayResult struct {
	Signed       bool
	StatusCode   int
	ResponseBody []byte
	Settled      bool // true only when target's own response was 2xx
	// OutboundTxID is the target's OWN settlement transaction id for this
	// specific payment (Wallet 2 -> target), best-effort extracted from its
	// Payment-Response/X-Payment-Response header when the target returns
	// one -- confirmed live 2026-08-01: canix402-api.compx.io returns
	// {"transaction":"..."} base64-encoded under this exact header. Empty
	// when the target doesn't return one (not every target does), which is
	// not itself an error -- Settled/StatusCode already say whether the
	// payment worked.
	OutboundTxID string
}

// outboundTxIDFromHeader best-effort extracts a settlement transaction id
// from a target's own Payment-Response header -- tolerant of both a raw
// JSON value and the base64-encoded form (matching Payment-Required's own
// encoding), and of the handful of field names real targets use for this.
func outboundTxIDFromHeader(h http.Header) string {
	raw := h.Get("Payment-Response")
	if raw == "" {
		raw = h.Get("X-Payment-Response")
	}
	if raw == "" {
		return ""
	}
	body := []byte(raw)
	if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil {
		body = decoded
	}
	var parsed map[string]any
	if json.Unmarshal(body, &parsed) != nil {
		return ""
	}
	for _, key := range []string{"transaction", "txId", "txid", "tx"} {
		if v, ok := parsed[key].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// PayTargetFromWallet2 signs and sends a real outbound x402 payment from
// Wallet 2 to target, using quote's payTo/asset/amount, then relays target's
// raw response back to the caller. This is the sole place that signs from
// Wallet 2's mnemonic — reached both by the public relay handler
// (x402relay.go's payTargetAndRespond) and, later, engine's own trusted,
// in-process, in-memory-pool-gated run-level path — never from any other,
// independently-maintained copy of this logic.
//
// method/body are the HTTP call actually made to target for the paid
// retry — empty method defaults to GET (unchanged default), body only ever
// sent when method isn't GET. Real x402 endpoints are not guaranteed to be
// GET-compatible (e.g. a POST-only resource 404s a bare GET before payment
// state is even considered), so this can't stay hardcoded to GET.
func PayTargetFromWallet2(ctx context.Context, cfg Wallet2PayConfig, target, method string, body []byte, quote TargetQuote) (Wallet2PayResult, error) {
	assetID, assetErr := strconv.ParseUint(quote.Asset, 10, 64)
	amount, amountErr := strconv.ParseUint(quote.MaxAmountRequired, 10, 64)
	if assetErr != nil || amountErr != nil {
		return Wallet2PayResult{}, &Wallet2PayError{StatusCode: http.StatusBadGateway, Msg: "target quote had an unparseable asset or amount"}
	}

	if assetID != cfg.USDCAssetID {
		log.Printf("wallet2 pay asset mismatch: quote.Asset=%q assetID=%d want=%d", quote.Asset, assetID, cfg.USDCAssetID)
		return Wallet2PayResult{}, &Wallet2PayError{StatusCode: http.StatusBadGateway, Msg: "target quoted an unexpected asset id"}
	}
	if cfg.MaxRelayOutboundUSDMicros > 0 && amount > uint64(cfg.MaxRelayOutboundUSDMicros) {
		return Wallet2PayResult{}, &Wallet2PayError{StatusCode: http.StatusBadGateway, Msg: "target quoted an amount exceeding the relay's per-call cap"}
	}

	// The target's OWN quote decides the signing scheme, not a blanket
	// assumption: a target that declares accepts[0].extra.feePayer is
	// asking for a fee-pooled 2-txn group cosigned by that exact shared
	// facilitator address (confirmed live against two independent real
	// mainnet targets -- arbsignal-production.up.railway.app and Prism --
	// both naming the identical shared GoPlausible feePayer our own relay
	// already uses: ZMFK2OI7ZBD2U27ISERZC4S6LKM6WMFJPZQ4MYNJDZ2VNBNMBA67RA22AA.
	// GoPlausible is a neutral facilitator every side already trusts, not
	// something specific to us, so nothing about a third party needing to
	// "cosign" a stub it never agreed to ever applies -- the facilitator
	// does that, exactly as for our own inbound settlement). A target with
	// no declared feePayer wants a plain, self-fee-paying single
	// transaction instead; the official @x402/avm client dispatches on
	// exactly this signal, per the Algorand Foundation's own reference
	// implementation. An earlier version of this function always used
	// SignUSDCPaymentSingle unconditionally, which is why every fee-
	// pooling-expecting target kept re-402'ing a payment that had, in fact,
	// already left the wallet.
	var group []string
	var idx int
	var err error
	if quote.FeePayer != "" {
		group, idx, err = cfg.USDCSigner.SignUSDCPaymentGroup(ctx, cfg.PlatformWalletEncMnemonic, quote.PayTo, assetID, amount, quote.FeePayer)
	} else {
		group, idx, err = cfg.USDCSigner.SignUSDCPaymentSingle(ctx, cfg.PlatformWalletEncMnemonic, quote.PayTo, assetID, amount)
	}
	if err != nil {
		return Wallet2PayResult{}, &Wallet2PayError{StatusCode: http.StatusInternalServerError, Msg: "failed to sign outbound payment: " + err.Error()}
	}

	paymentHeaders := buildPaymentHeaders(target, cfg.RelayNetwork, group, idx, quote)
	if method == "" {
		method = http.MethodGet
	}
	var bodyReader io.Reader
	if method != http.MethodGet && len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}
	payReq, err := http.NewRequestWithContext(ctx, method, target, bodyReader)
	if err != nil {
		// Signed==true: SignUSDCPaymentGroup above already succeeded, so a
		// real signed group exists even though the paid request to target
		// was never sent -- same "money already committed" accounting as
		// the transport-failure case below, just failing before the
		// request even goes out instead of after.
		return Wallet2PayResult{Signed: true}, &Wallet2PayError{StatusCode: http.StatusBadGateway, Msg: "failed to build paid request to target: " + err.Error()}
	}
	if bodyReader != nil {
		contentType := cfg.ContentType
		if contentType == "" {
			contentType = "application/json"
		}
		payReq.Header.Set("Content-Type", contentType)
	}
	if cfg.Authorization != "" {
		payReq.Header.Set("Authorization", "Bearer "+cfg.Authorization)
	}
	for name, value := range paymentHeaders {
		payReq.Header.Set(name, value)
	}
	payResp, err := SafeOutboundPayHTTPClient().Do(payReq)
	if err != nil {
		// Signed==true is deliberate here: a real signed group already
		// exists by this point, matching the pre-existing billing
		// philosophy in x402relay.go — a network failure reaching the
		// target doesn't unwind a payment that's already committed.
		return Wallet2PayResult{Signed: true}, &Wallet2PayError{StatusCode: http.StatusBadGateway, Msg: "paid request to target failed: " + err.Error()}
	}
	defer payResp.Body.Close()
	finalBody, _ := io.ReadAll(io.LimitReader(payResp.Body, 5<<20))

	if payResp.StatusCode < 200 || payResp.StatusCode >= 300 {
		// target and its response are externally controlled (the workflow's
		// configured endpoint, and whatever that endpoint returns) -- quote
		// and cap both before logging so neither can inject control
		// characters/forge log lines or blow up log volume.
		const logSnippetLimit = 512
		bodySnippet := finalBody
		if len(bodySnippet) > logSnippetLimit {
			bodySnippet = bodySnippet[:logSnippetLimit]
		}
		headerSnippet := payResp.Header.Get("Payment-Required")
		if len(headerSnippet) > logSnippetLimit {
			headerSnippet = headerSnippet[:logSnippetLimit]
		}
		// Never log a payment header's value itself -- it carries the signed
		// transaction bytes, and those must not enter application logs even
		// though the underlying payment is headed for a public ledger:
		// anyone with log access could submit/replay it ahead of us. A
		// fingerprint (not reversible to the signed bytes) is enough to
		// correlate this rejection with what was actually sent, without
		// ever writing the signed payload itself anywhere.
		// Fingerprints the spec header, which every non-canix402 target also
		// gets a raw-JSON twin of: same bytes, so one hash identifies both.
		fingerprint := sha256.Sum256([]byte(paymentHeaders["Payment-Signature"]))
		log.Printf("wallet2 outbound pay %s %s rejected: status=%d body=%s payment-required-header=%s paymentIndex=%d xPaymentFingerprint=%s", method, strconv.Quote(target), payResp.StatusCode, strconv.Quote(string(bodySnippet)), strconv.Quote(headerSnippet), idx, hex.EncodeToString(fingerprint[:4]))
	}

	return Wallet2PayResult{
		Signed:       true,
		StatusCode:   payResp.StatusCode,
		ResponseBody: finalBody,
		Settled:      payResp.StatusCode >= 200 && payResp.StatusCode < 300,
		OutboundTxID: outboundTxIDFromHeader(payResp.Header),
	}, nil
}

// buildPaymentHeaders builds the outbound payment headers for target.
// quote.RawAccept/RawChallenge (the target's own challenge, kept verbatim
// from the probe that fetched it) are echoed back onto the payload -- a real
// v2 client copies these from the challenge it received, and it's what lets
// a spec-compliant target catalog OUR platform wallet's payment the same way
// we catalog payments made to us (see x402relay.go's resourceInfo/
// bazaarDiscoveryExtension doc comments for the mechanism this mirrors).
//
// Every target gets the same two headers, and a payload assembled entirely
// from what that target itself declared -- no per-host branching. Which
// dialect a target speaks is not something we can know from its hostname,
// and a payment path that has to be taught about each merchant by name
// cannot serve the arbitrary endpoint a user pastes into a node.
//
//   - Payment-Signature: base64(JSON) -- what x402 v2 actually specifies, and
//     the only header a server built on @x402/core looks at. Its extractPayment
//     reads "payment-signature"/"PAYMENT-SIGNATURE" and nothing else, so a
//     target running the official middleware could not see our payment at all
//     before this, no matter how correct the payment itself was. Confirmed the
//     expensive way against api.scrape402.site (2026-08-03): five real runs,
//     each settling the inbound leg for $0.10 of the user's credits, each
//     answered with the target's unchanged 402 challenge and an empty body,
//     outbound_tx_id never set on a single one.
//   - X-Payment: raw JSON -- this codebase's own historical dialect, kept so
//     any target that was working before keeps working. Deliberately NOT
//     base64: X-PAYMENT in the real spec is the v1 header and is base64 there,
//     so re-encoding this one would break the raw-JSON readers it exists for
//     without satisfying a v1 reader we have never actually met.
//
// Both carry the same signed group, so at most one can ever settle: a second
// submission of the same transaction is rejected as a duplicate on-chain.
func buildPaymentHeaders(target, relayNetwork string, group []string, idx int, quote TargetQuote) map[string]string {
	// The target's own accept option decides the network id, falling back to
	// our configured one. Echoing what it declared can only match its own
	// equality check; sending our spelling of the same chain (both CAIP-2
	// genesis hashes today, but that is config, not a guarantee) can fail it.
	// This is what replaced a hardcoded host->network map: canix402 was sent
	// the short form "algorand-mainnet" by name, yet its live challenge
	// declares the CAIP-2 id (re-checked 2026-08-03), so echoing is both more
	// correct and self-healing when a merchant changes what it advertises.
	network := relayNetwork
	if quote.RawAccept != nil {
		if n, ok := quote.RawAccept["network"].(string); ok && n != "" {
			network = n
		}
	}
	fields := map[string]any{
		"x402Version": 2, "scheme": "exact", "network": network,
		"payload": map[string]any{"paymentGroup": group, "paymentIndex": idx},
	}
	// accepted is an exact echo of the requirements this payment was created
	// against. PaymentPayloadV2Schema requires it with no .optional(), so a
	// schema-validating consumer rejects the whole payload without it -- the
	// same field whose absence kept our settle calls out of the Bazaar
	// catalog (see the facilitator.go/CLAUDE.md notes on PaymentPayload.Accepted).
	if quote.RawAccept != nil {
		fields["accepted"] = quote.RawAccept
	}
	if quote.RawChallenge != nil {
		if res, ok := quote.RawChallenge["resource"]; ok {
			fields["resource"] = res
		}
		if ext, ok := quote.RawChallenge["extensions"]; ok {
			fields["extensions"] = ext
		}
		// The target's entire original challenge, echoed back. Only canix402
		// is known to require this, and it used to get it by hostname -- but
		// sending it to everyone is safe rather than merely convenient: not a
		// single schema in @x402/core 2.20 is .strict() (grepped, zero
		// occurrences), and a non-strict zod object ignores fields it does not
		// declare. So a spec-only target drops it and a canix402-like one
		// finds what it needs, with nobody's hostname written down here.
		fields["paymentRequired"] = quote.RawChallenge
	}
	encoded, _ := json.Marshal(fields)
	return map[string]string{
		"Payment-Signature": base64.StdEncoding.EncodeToString(encoded),
		"X-Payment":         string(encoded),
	}
}
