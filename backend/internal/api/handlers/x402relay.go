package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"

	"github.com/agentmesh/backend/internal/db"
	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/respond"
	"github.com/agentmesh/backend/internal/x402"
)

// maxRelayTargetBodyBytes bounds the body a caller may ask the relay to
// forward. Sized above what this platform's own nodes can produce -- a
// workflow's file params are capped at 2 MiB each (nodes.maxParamFileBytes)
// and several can share one multipart body -- while still refusing to buffer
// an unbounded upload on a public, unauthenticated route.
const maxRelayTargetBodyBytes int64 = 16 << 20 // 16 MiB

// X402Relay is the orchestrator's own paid endpoint. It has no fixed price:
// the price it charges the caller is whatever the target endpoint (given via
// ?target=) actually charges. This is what makes the relay generic across
// every x402 endpoint in the GoPlausible marketplace, not just a fixed set.
//
// Flow: no X-Payment header -> fetch target's real 402, mirror it back as our
// own v2/USDC/tagged challenge (payTo = platform wallet). X-Payment present ->
// verify+settle the inbound payment via the facilitator (credited to us),
// then pay the target from the platform wallet (credited to them), then
// relay the target's paid response back to the caller.
func (d *Deps) X402Relay(w http.ResponseWriter, r *http.Request) {
	xPayment, hasPayment, err := incomingPaymentJSON(r)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid Payment-Signature header: "+err.Error())
		return
	}

	target := r.URL.Query().Get("target")
	if target == "" {
		// No ?target= at all -- previously a flat 400, meaning there was no
		// stable, real, always-answering 402 challenge anywhere on this
		// route for a Bazaar crawler to ever find and catalog (every real
		// challenge this handler otherwise issues varies per ?target=, so
		// there's no single fixed "resource" for the crawler to settle
		// against). This branch is that fixed listing -- same price, same
		// description, every single time. Real dynamic usage (every actual
		// workflow call) always supplies ?target= and is completely
		// unaffected by this branch existing.
		if hasPayment {
			d.relaySelfSettle(w, r, xPayment)
			return
		}
		d.relaySelfChallenge(w, r)
		return
	}
	// target is caller-supplied and this route is public/unauthenticated —
	// without this check, callers could make the relay fetch or pay
	// arbitrary internal/private addresses (SSRF). Same guard applied to
	// every tool402 node's target before Task 6 wires it through here.
	if err := nodes.ValidateURL(target); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid target: "+err.Error())
		return
	}

	// X-Relay-Method tells the relay what to send to TARGET (not how the
	// caller reaches the relay itself -- that is this route's own method and
	// carries no meaning for the target). Real x402 endpoints are not
	// guaranteed to be GET-compatible (a POST-only resource can 404 a bare
	// GET before it ever considers payment state), so the relay needs to know.
	targetMethod := r.Header.Get("X-Relay-Method")
	if targetMethod == "" {
		targetMethod = http.MethodGet
	}
	switch targetMethod {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch:
	default:
		respond.Error(w, http.StatusBadRequest, "unsupported X-Relay-Method: "+targetMethod)
		return
	}
	// The body destined for the target arrives as this request's own body.
	// It used to arrive base64-encoded in X-Relay-Body, on the reasoning that
	// net/http allows 1MB of headers -- but our own server's limit is not the
	// binding one. Every proxy in front of it caps headers far lower, so a
	// file param (multipart, easily >100KB) never made it here at all: the
	// connection was dropped upstream before this handler ran (confirmed live
	// 2026-08-03, a 138KB PDF to prism-99h2.onrender.com). The header is
	// still read as a fallback so a caller that predates this change keeps
	// working for the small bodies it could actually deliver.
	var targetBody []byte
	if r.Body != nil {
		decoded, err := io.ReadAll(io.LimitReader(r.Body, maxRelayTargetBodyBytes+1))
		if err != nil {
			respond.Error(w, http.StatusBadRequest, "could not read the target request body: "+err.Error())
			return
		}
		if int64(len(decoded)) > maxRelayTargetBodyBytes {
			respond.Error(w, http.StatusRequestEntityTooLarge, "target request body exceeds the relay's limit")
			return
		}
		targetBody = decoded
	}
	if len(targetBody) == 0 {
		if b64 := r.Header.Get("X-Relay-Body"); b64 != "" {
			decoded, err := base64.StdEncoding.DecodeString(b64)
			if err != nil {
				respond.Error(w, http.StatusBadRequest, "invalid X-Relay-Body: "+err.Error())
				return
			}
			targetBody = decoded
		}
	}
	// How targetBody is encoded. Only the caller knows — a multipart body's
	// content type carries the boundary token generated when it was built,
	// which cannot be reconstructed here.
	targetContentType := r.Header.Get("X-Relay-Content-Type")
	// A bearer the TARGET requires (Tendril's lease token, for example).
	// Named X-Relay-Auth rather than Authorization so it can never be
	// confused with auth for the relay itself, which is unauthenticated and
	// a wholly different trust boundary.
	targetAuth := r.Header.Get("X-Relay-Auth")

	if !hasPayment {
		d.relayInboundChallenge(w, r, target, targetMethod, targetBody, targetContentType, targetAuth)
		return
	}
	d.relaySettleAndForward(w, r, target, xPayment, targetMethod, targetBody, targetContentType, targetAuth)
}

// incomingPaymentJSON reads the caller's payment off whichever header they
// actually used, normalized to a raw JSON string (relaySelfSettle/
// relaySettleAndForward's existing json.Unmarshal is untouched either way).
//
// Payment-Signature is the real x402 v2 spec's canonical header name --
// base64-encoded JSON, matching Payment-Required's own encoding (see
// @x402/core's encodePaymentSignatureHeader/decodePaymentSignatureHeader,
// and the reference resource-server implementation's own readPayment, which
// checks this header first). A real v2-compliant client -- the official SDK,
// or GoPlausible's own Bazaar crawler if it ever pays to sample/catalog a
// resource -- sends this by default and never X-Payment. Before this fix,
// this relay only ever read X-Payment, meaning no real external payer using
// a standard client could ever reach it at all -- only this codebase's own
// internal callers (tool402.go, the throwaway verification scripts used to
// test this endpoint), which all happen to use X-Payment.
//
// X-Payment is kept exactly as it always was here -- raw, unencoded JSON,
// checked second -- for zero risk to every already-verified real settlement
// this session performed through it; this is purely additive.
func incomingPaymentJSON(r *http.Request) (payloadJSON string, ok bool, err error) {
	if b64 := r.Header.Get("Payment-Signature"); b64 != "" {
		decoded, decErr := base64.StdEncoding.DecodeString(b64)
		if decErr != nil {
			return "", false, decErr
		}
		return string(decoded), true, nil
	}
	if raw := r.Header.Get("X-Payment"); raw != "" {
		return raw, true, nil
	}
	return "", false, nil
}

// relaySelfServiceUSDMicros/relaySelfDescription describe the relay itself
// as a fixed, always-payable resource -- unlike every other challenge this
// handler issues (all priced/described dynamically off whatever ?target=
// the caller supplied), this one never changes. Nominal price, matching the
// cheapest real tier already confirmed working this session (CANIX402's
// $0.01) -- this exists purely to be a genuinely payable, catalogable
// resource, not free metadata.
const relaySelfServiceUSDMicros = "10000" // $0.01

// relayPublicPath is this route as reached from our own branded domain,
// via the /api/:path* rewrite already configured in frontend/next.config.ts
// (it proxies to BACKEND_URL, so FRONTEND_URL + this path serves the exact
// same real 402 this handler produces -- verified live, HTTP 402 with the
// full challenge body). Appended to d.FrontendURL, never d.BaseURL.
//
// This is the identity every resource.url in this file declares, and it is
// the only form that satisfies all four constraints at once, where both
// earlier attempts each broke one:
//
//   - Real path, not a bare root. Of 748 entries in /discovery/resources,
//     ZERO have a root path; FRONTEND_URL alone normalized to
//     "https://www.agent-mesh.app/" and could never have been cataloged.
//   - Genuinely serves a 402 at that exact URL, so the declaration is true
//     rather than a pointer at unrelated marketing HTML.
//   - Resolves the leaderboard's label/logo, which the facilitator crawls
//     from the resource URL's host. BASE_URL (the bare Railway host) is
//     real and pathful but serves JSON, so it strips the branding our
//     merchant row currently shows.
//   - Keeps one root domain per payTo, which the challenge rules call out
//     as IMPORTANT three separate times. BASE_URL made railway.app a second
//     root domain under the same merchant.
const relayPublicPath = "/api/x402/relay"
const relaySelfDescription = "AgentMesh x402 relay — pays real x402 endpoints on your behalf. Append ?target=<url> to route a specific payment through this relay."

// relaySelfChallenge issues the fixed, always-identical 402 challenge for a
// bare /x402/relay request (no ?target=). See X402Relay's doc comment for
// why this exists: a Bazaar crawler visiting the bare route previously got
// a 400, never a real 402 to catalog.
func (d *Deps) relaySelfChallenge(w http.ResponseWriter, r *http.Request) {
	// See relayPublicPath. Two earlier attempts at this identity each broke
	// one constraint: FRONTEND_URL alone was a bare root path (0 of 748
	// cataloged resources have one), and BASE_URL + "/x402/relay" was a real
	// path but on a second root domain that serves JSON, stripping the
	// leaderboard branding our merchant row currently resolves.
	selfResourceURL := d.FrontendURL + relayPublicPath
	challenge := map[string]any{
		"x402Version": 2,
		// Top-level, spec-shaped ResourceInfo -- see resourceInfo's doc
		// comment for why this (not accepts[0].resource below, which is only
		// kept for the v1-dialect stragglers that still read it there) is
		// what actually makes this endpoint catalogable.
		"resource": resourceInfo(selfResourceURL, relaySelfDescription),
		"accepts": []map[string]any{{
			"scheme":            "exact",
			"network":           d.RelayNetwork,
			"amount":            relaySelfServiceUSDMicros,
			"resource":          selfResourceURL,
			"description":       relaySelfDescription,
			"payTo":             d.PlatformWalletAddress,
			"maxTimeoutSeconds": 300,
			"asset":             strconv.FormatUint(d.USDCAssetID, 10),
			"extra": map[string]any{
				"asset":    strconv.FormatUint(d.USDCAssetID, 10),
				"feePayer": d.RelayFeePayer,
				"tag":      "x402-global-challenge",
				"decimals": 6,
			},
		}},
		"extensions": bazaarDiscoveryExtension(""),
	}
	challengeJSON, err := json.Marshal(challenge)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "failed to encode challenge")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	// Bazaar's discovery crawler reads the challenge off this header (base64
	// JSON), not just the body — same convention relayInboundChallenge uses.
	w.Header().Set("Payment-Required", base64.StdEncoding.EncodeToString(challengeJSON))
	w.WriteHeader(http.StatusPaymentRequired)
	w.Write(challengeJSON)
}

// relaySelfSettle verifies+settles a real payment against the fixed
// self-challenge above. No target to fetch or forward to -- this IS the
// product being paid for, not a proxy for a downstream one. Real,
// GoPlausible-settled, same facilitator calls every other settlement this
// handler performs uses.
func (d *Deps) relaySelfSettle(w http.ResponseWriter, r *http.Request, xPaymentHeader string) {
	ctx := r.Context()

	var payload x402.PaymentPayload
	if err := json.Unmarshal([]byte(xPaymentHeader), &payload); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid X-Payment payload")
		return
	}

	amountAssetMicros, err := strconv.ParseInt(relaySelfServiceUSDMicros, 10, 64)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "internal error parsing fixed price")
		return
	}

	selfResourceURL := d.FrontendURL + relayPublicPath
	reqs := x402.PaymentRequirements{
		Scheme:            "exact",
		Network:           d.RelayNetwork,
		PayTo:             d.PlatformWalletAddress,
		Asset:             strconv.FormatUint(d.USDCAssetID, 10),
		MaxAmountRequired: relaySelfServiceUSDMicros,
		Resource:          selfResourceURL,
		Description:       relaySelfDescription,
		MimeType:          "application/json",
		// Was omitted entirely here, so the struct actually POSTed to
		// /verify and /settle carried maxTimeoutSeconds: 0 (Go's zero value,
		// no omitempty on the field) while the public challenge above
		// advertised 300. The real v2 requirements schema types this as
		// z.number().positive() -- zero is a hard validation failure, not a
		// missing-value default -- so every settlement from this path was
		// schema-invalid to any consumer that parses it, even though the AVM
		// settle mechanism itself never reads the field and happily moved
		// real money regardless. 300 mirrors the challenge exactly.
		MaxTimeoutSeconds: 300,
		Extra: map[string]any{
			"asset":    strconv.FormatUint(d.USDCAssetID, 10),
			"feePayer": d.RelayFeePayer,
			"tag":      "x402-global-challenge",
			"decimals": 6,
		},
		// Both siblings (relaySettleAndForward, runfund.go) already set this
		// on the struct POSTed to /verify and /settle; this path was the only
		// one that declared a bazaar extension in its public challenge and
		// then dropped it before the facilitator ever saw it. Same argument
		// relaySelfChallenge passes, so the declaration the caller was shown
		// and the one sent at settle time are byte-identical.
		Extensions: bazaarDiscoveryExtension(""),
	}

	// Set authoritatively, server-side, regardless of what the caller's own
	// payload carried -- see PaymentPayload.Resource's doc comment. This is
	// what actually makes a self-listing settlement catalogable: WE are the
	// resource server for this route, so we already know exactly what this
	// payment is for, and correctness here must not depend on every caller
	// (including this codebase's own internal engine self-call in
	// tool402.go) remembering to echo the challenge's resource/extensions
	// back onto its own payload.
	payload.Resource = resourceInfo(selfResourceURL, relaySelfDescription)
	payload.Extensions = bazaarDiscoveryExtension("")
	// Required field of the v2 payload schema that this codebase has never
	// sent -- see PaymentPayload.Accepted's doc comment. Set from reqs, so
	// the echo is guaranteed to match what we're actually charging rather
	// than being assembled a second time and drifting.
	payload.Accepted = reqs.AcceptedV2()
	// x402Version comes off the caller's own header and is only ever read,
	// never set, here. The facilitator's discovery extraction switches hard
	// on it (=== 2 / === 1 / else return null), so a caller that omitted it
	// (decoding to 0) silently opted this settlement out of cataloging
	// entirely. We are the resource server and this route is v2 by
	// construction, so assert it rather than inherit it.
	payload.X402Version = 2

	verifyResult, err := d.FacilitatorClient.Verify(ctx, payload, reqs)
	if err != nil {
		respond.Error(w, http.StatusBadGateway, "facilitator verify failed: "+err.Error())
		return
	}
	if !verifyResult.IsValid {
		respond.Error(w, http.StatusPaymentRequired, "payment invalid: "+verifyResult.Invalid)
		return
	}

	settleResult, err := d.FacilitatorClient.Settle(ctx, payload, reqs)
	if err != nil {
		respond.Error(w, http.StatusBadGateway, "facilitator settle failed: "+err.Error())
		return
	}
	if !settleResult.Success {
		respond.Error(w, http.StatusPaymentRequired, "settlement failed: "+settleResult.Error)
		return
	}

	// "self" -- a fixed, non-URL label, distinct from every other row this
	// table records (all real per-target URLs) -- target_url isn't the
	// dedup key (inbound_tx_id is, real and unique per settlement), so a
	// constant label across many real self-listing settlements is safe.
	if _, err := d.Store.RecordInboundSettlement(ctx, "self", settleResult.TxID, amountAssetMicros); err != nil {
		if err == db.ErrDuplicateSettlement {
			respond.Error(w, http.StatusConflict, "payment already processed")
			return
		}
		log.Printf("x402 relay: failed to record self-listing settlement: %v", err)
		respond.Error(w, http.StatusInternalServerError, "internal error recording settlement")
		return
	}

	respond.JSON(w, http.StatusOK, map[string]any{
		"service":     "AgentMesh x402 relay",
		"description": relaySelfDescription,
		"docs":        d.FrontendURL,
		"txId":        settleResult.TxID,
	})
}

// X402RunFundingInfo is the static, informational resource FundRunReserve's
// PaymentRequirements.Resource points at — a real, reachable route on our own
// domain rather than an opaque identifier string. No payment logic, no auth:
// purely informational, matching what a real Bazaar-catalog crawler would
// expect to find at a `resource` URL.
func (d *Deps) X402RunFundingInfo(w http.ResponseWriter, r *http.Request) {
	respond.JSON(w, http.StatusOK, map[string]string{
		"description": "AgentMesh workflow run funding pool — internal pre-settlement for downstream x402 tool calls, not directly payable via this route",
	})
}

// X402PlatformFeeInfo is X402RunFundingInfo's counterpart for
// nodes.SettlePlatformFee's PaymentRequirements.Resource — same rationale,
// a real reachable route rather than an opaque identifier, registered at
// nodes.platformFeePublicPath.
func (d *Deps) X402PlatformFeeInfo(w http.ResponseWriter, r *http.Request) {
	respond.JSON(w, http.StatusOK, map[string]string{
		"description": "AgentMesh platform fee — internal settlement of the flat per-call markup, not directly payable via this route",
	})
}

// X402RunTotalInfo is X402RunFundingInfo's counterpart for
// nodes.SettleRunTotal's PaymentRequirements.Resource — same rationale, a
// real reachable route rather than an opaque identifier, registered at
// nodes.runTotalPublicPath.
func (d *Deps) X402RunTotalInfo(w http.ResponseWriter, r *http.Request) {
	respond.JSON(w, http.StatusOK, map[string]string{
		"description": "AgentMesh workflow run total — internal lump-sum settlement of a run's platform-billed work, not directly payable via this route",
	})
}

// targetPriceQuote is the subset of a target's x402 402 response the relay
// cares about.
type targetPriceQuote struct {
	PayTo             string
	Asset             string
	MaxAmountRequired string
	// FeePayer, when the target's own quote declares accepts[0].extra.feePayer,
	// names the shared facilitator address expected to cosign a fee-pooled
	// stub for THIS payment. Its presence/absence -- not any constant of
	// ours -- selects which signing scheme payTargetAndRespond uses (see
	// nodes.PayTargetFromWallet2): a target with no declared feePayer wants
	// a plain self-funded single transaction instead.
	FeePayer string
	// RawAccept and RawChallenge are the exact accepts[0] entry and the
	// exact full challenge object the target returned, verbatim -- see
	// nodes.TargetQuote's matching fields.
	RawAccept    map[string]any
	RawChallenge map[string]any
}

// fetchTargetPriceQuote issues an unauthenticated GET to the caller-supplied
// target (via the SSRF-safe shared client, which also enforces a 10s dial+
// request timeout — see nodes.toolHTTPClient) and parses its x402 402
// challenge.
//
// This is called independently from two places: relayInboundChallenge (to
// mirror the price to the caller on the no-payment leg) and
// relaySettleAndForward (to learn the authoritative price to enforce and
// record before settling the inbound payment). Those two calls are separate,
// unrelated HTTP requests (no-payment challenge vs. with-payment settle) with
// no shared state between them, so the price genuinely has to be re-fetched
// across that boundary rather than trusted from the earlier call.
//
// Deliberately NOT called a second time from payTargetAndRespond: target is
// caller-supplied and this route is public/unauthenticated, so re-fetching a
// second, independent quote at pay-time would let a malicious target answer
// cheaply the first time and expensively (and/or to a different payTo) the
// second time, draining the platform wallet for more than was ever collected
// from the caller. relaySettleAndForward fetches the quote exactly once per
// relay cycle and passes that same value into payTargetAndRespond.
func fetchTargetPriceQuote(ctx context.Context, target, method string, body []byte, contentType, targetAuth string) (targetPriceQuote, error) {
	var bodyReader io.Reader
	if method != http.MethodGet && len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bodyReader)
	if err != nil {
		return targetPriceQuote{}, err
	}
	if bodyReader != nil {
		if contentType == "" {
			contentType = "application/json"
		}
		req.Header.Set("Content-Type", contentType)
	}
	if targetAuth != "" {
		req.Header.Set("Authorization", "Bearer "+targetAuth)
	}
	resp, err := nodes.SafeHTTPClient().Do(req)
	if err != nil {
		return targetPriceQuote{}, err
	}
	defer resp.Body.Close()

	var rawChallenge map[string]any
	var parsed struct {
		Accepts []map[string]any `json:"accepts"`
	}
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	json.Unmarshal(respBody, &rawChallenge)
	json.Unmarshal(respBody, &parsed)
	if len(parsed.Accepts) == 0 {
		// Body carried no accepts[] — some real targets (Prism's live
		// endpoint confirmed 2026-07-31) put the full challenge in the
		// Payment-Required header instead and leave the body empty/minimal.
		parsed.Accepts = nodes.ChallengeAcceptsFromHeader(resp.Header)
		if len(parsed.Accepts) > 0 {
			rawChallenge = nodes.ChallengeFromHeader(resp.Header)
		}
	}
	if len(parsed.Accepts) == 0 {
		return targetPriceQuote{}, fmt.Errorf("target did not return a valid x402 challenge")
	}
	accept := parsed.Accepts[0]
	payTo, _ := accept["payTo"].(string)
	asset, _ := accept["asset"].(string)
	var feePayer string
	if extra, ok := accept["extra"].(map[string]any); ok {
		feePayer, _ = extra["feePayer"].(string)
	}
	// Accept both the usual JSON-string encoding and a JSON-number encoding
	// of maxAmountRequired — some real targets encode this field as a
	// number rather than a string, which a string-only type assertion would
	// otherwise reject as an empty/invalid quote (see
	// nodes.ParseMaxAmountRequiredAsMicros). Also accept `amount` as a
	// fallback field name when `maxAmountRequired` is absent/unparseable —
	// the CURRENT real-world x402 dialect (Prism's live endpoint, the
	// official @x402/core v2.20 SDK, confirmed live 2026-07-31) uses `amount`
	// instead; `maxAmountRequired` is only checked first because it's this
	// codebase's own historical convention, not because it's more correct.
	// A parse failure on both still yields an empty MaxAmountRequired, which
	// the callers below already turn into an error via strconv.ParseInt.
	var amount string
	micros, ok := nodes.ParseMaxAmountRequiredAsMicros(accept["maxAmountRequired"])
	if !ok {
		micros, ok = nodes.ParseMaxAmountRequiredAsMicros(accept["amount"])
	}
	if ok {
		amount = strconv.FormatInt(micros, 10)
	}
	return targetPriceQuote{PayTo: payTo, Asset: asset, MaxAmountRequired: amount, FeePayer: feePayer, RawAccept: accept, RawChallenge: rawChallenge}, nil
}

// resourceInfo builds the v2 `resource` object per the official @x402/core
// v2.20 SDK's ResourceInfo type ({url, description?, mimeType?, serviceName?,
// tags?, iconUrl?}) — decompiled and confirmed against the real published
// npm package, not inferred from prose docs. This MUST be a top-level field
// on the PaymentRequired body, never nested inside accepts[0] — that's a
// different field (PaymentRequirementsV1.resource: string) on the old v1
// dialect's type, which the v2 client code path never reads. A real v2
// client's own createPaymentPayload does `resource: paymentRequired.resource`
// — a verbatim top-level copy onto the outgoing payment payload — and the
// facilitator's own discovery extraction (extractDiscoveryInfo in
// @x402/extensions) reads paymentPayload.resource.url to build a Bazaar
// catalog entry. Omit the top-level field, as this handler did before this
// fix, and no client — however spec-compliant, however many times it pays —
// has anything to echo back, so nothing here can ever be cataloged.
// relayTargetDescription is the shared description string for a
// target-mirroring challenge -- used both in the public 402 shown to the
// caller (relayInboundChallenge) and in the payload.Resource this handler
// sets server-side before Verify/Settle (relaySettleAndForward). Kept as one
// function so the two never drift apart: a catalog entry should describe
// exactly what the paying caller actually saw in its own challenge.
func relayTargetDescription(target string) string {
	return "AgentMesh x402 relay — settles the inbound leg and forwards payment to " + target
}

func resourceInfo(url, description string) map[string]any {
	return map[string]any{
		"url":         url,
		"description": description,
		"mimeType":    "application/json",
		"serviceName": "AgentMesh",
		"tags":        []string{"x402-global-challenge"},
	}
}

// bazaarDiscoveryExtension fills in the relay's own route-specific half of a
// Bazaar discovery declaration and hands the rest to
// nodes.BazaarDiscoveryExtension, which owns the schema skeleton and the
// validator's hard requirements ({info, schema} both present, info.input.type
// set, the method enum agreeing with the declared method). See that function
// for why each of those matters — both are failures the facilitator reports
// as success while silently declining to catalog anything.
//
// Describes the relay's own pass-through shape (any downstream target URL
// in, that target's own response out) since this endpoint has no fixed
// schema of its own. target == "" describes the fixed self-listing (no
// query params at all). Shared between the public 402 challenge
// (relayInboundChallenge/relaySelfChallenge) and the PaymentRequirements
// actually POSTed to /verify and /settle (relaySettleAndForward) — the
// facilitator only catalogs a route once it sees this on a real settlement,
// not from the informational challenge alone.
func bazaarDiscoveryExtension(target string) map[string]any {
	// queryParams and outputExample are the only two things that vary
	// between the self-listing and a ?target= relay; the method, the schema
	// skeleton, and the enum that has to agree with the method all come from
	// nodes.BazaarDiscoveryExtension. outputExample itself is optional per
	// the real spec (only `input` is in the schema's own `required` list) --
	// sent anyway because every live, genuinely-cataloged entry pulled from
	// the real facilitator (facilitator.goplausible.xyz/discovery/resources)
	// has one, so this closes the one structural gap left between our
	// declaration and a real working example.
	decl := nodes.BazaarDeclaration{
		// MUST stay the public path (relayPublicPath), not this backend's
		// own internal route. routeTemplate exists so the facilitator can
		// canonicalize the resource as origin+routeTemplate instead of
		// origin+request-pathname -- so a stale value here doesn't just
		// mislabel, it names a URL that does not exist. Hardcoding
		// "/x402/relay" while resource.url moved to the /api proxy
		// produced exactly that: origin+routeTemplate resolved to
		// https://www.agent-mesh.app/x402/relay, a confirmed 404, since
		// Vercel only rewrites /api/*. Derived from the same constant as
		// the URL itself so the two cannot drift again.
		RouteTemplate: relayPublicPath,
		OutputExample: map[string]any{"service": "AgentMesh x402 relay", "docs": true, "txId": "..."},
	}
	if target != "" {
		decl.QueryParams = map[string]any{"target": target}
		decl.OutputExample = map[string]any{"ok": true, "note": "the target endpoint's own paid response, forwarded unmodified"}
	}
	return nodes.BazaarDiscoveryExtension(decl)
}

// relayInboundChallenge fetches the target's real 402 price and mirrors it
// back as our own v2 challenge, tagged for the challenge and paid to our
// platform wallet instead of the target's.
func (d *Deps) relayInboundChallenge(w http.ResponseWriter, r *http.Request, target, targetMethod string, targetBody []byte, targetContentType, targetAuth string) {
	quote, err := fetchTargetPriceQuote(r.Context(), target, targetMethod, targetBody, targetContentType, targetAuth)
	if err != nil {
		respond.Error(w, http.StatusBadGateway, "target fetch failed: "+err.Error())
		return
	}
	// fetchTargetPriceQuote returns MaxAmountRequired == "" when neither the
	// maxAmountRequired nor amount field parsed -- relaySettleAndForward
	// catches this later via strconv.ParseInt, but this path (the public,
	// unauthenticated challenge preview -- what a Bazaar crawler actually
	// hits) had no equivalent check, so it would happily emit a 402 with an
	// empty price instead of a clear error.
	if quote.MaxAmountRequired == "" {
		respond.Error(w, http.StatusBadGateway, "target returned an unparseable or missing price quote")
		return
	}

	description := relayTargetDescription(target)
	// Our own branded proxy path, NOT target: see relayPublicPath. Using
	// target here was tried earlier and re-branded our own leaderboard row
	// as whichever downstream target was paid through us most recently
	// (confirmed live -- one CANIX402 settlement relabeled us as "CANIX402",
	// complete with their logo). This keeps branding stable AND uses a real
	// path that genuinely answers 402 --
	// bazaarDiscoveryExtension(target)'s queryParams + routeTemplate already
	// model every ?target= value as one parameterized resource, not N
	// per-target ones, so collapsing them under this one URL costs no
	// precision.
	targetResourceURL := d.FrontendURL + relayPublicPath
	challenge := map[string]any{
		"x402Version": 2,
		// Top-level, spec-shaped ResourceInfo -- see resourceInfo's doc
		// comment. Without this, no real v2 client has anything to echo
		// back onto its payment payload, so this route (despite varying
		// price per ?target=) can never catalog any of its settlements.
		"resource": resourceInfo(targetResourceURL, description),
		"accepts": []map[string]any{{
			"scheme":  "exact",
			"network": d.RelayNetwork,
			// GoPlausible's facilitator reads this key as "amount", not
			// "maxAmountRequired" — matches PaymentRequirements' wire tag
			// in facilitator.go. Callers parsing our challenge
			// (ChallengeAcceptsFromHeader etc.) already accept both names,
			// so this is safe to change unilaterally.
			"amount":            quote.MaxAmountRequired,
			"resource":          target,
			"description":       description,
			"payTo":             d.PlatformWalletAddress,
			"maxTimeoutSeconds": 300,
			"asset":             strconv.FormatUint(d.USDCAssetID, 10),
			"extra": map[string]any{
				"asset":    strconv.FormatUint(d.USDCAssetID, 10),
				"feePayer": d.RelayFeePayer,
				"tag":      "x402-global-challenge",
				"decimals": 6,
			},
		}},
		// Bazaar discovery metadata: describes the relay's pass-through
		// shape (any downstream target URL in, that target's own response
		// out) since this endpoint has no fixed schema of its own — see
		// GoPlausible's bazaar-integration reference server.
		"extensions": bazaarDiscoveryExtension(target),
	}

	challengeJSON, err := json.Marshal(challenge)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "failed to encode challenge")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	// Bazaar's discovery crawler reads the challenge off this header (base64
	// JSON), not just the body — see GoPlausible's bazaar-integration
	// reference server verification step. Cheap to set both.
	w.Header().Set("Payment-Required", base64.StdEncoding.EncodeToString(challengeJSON))
	w.WriteHeader(http.StatusPaymentRequired)
	w.Write(challengeJSON)
}

// relaySettleAndForward verifies+settles the caller's inbound payment, then
// pays the real target from the platform wallet, then relays the target's
// paid response back. Both settlements are real, GoPlausible-facilitated,
// mainnet payments — this is what earns orchestrator-entry attribution.
func (d *Deps) relaySettleAndForward(w http.ResponseWriter, r *http.Request, target, xPaymentHeader, targetMethod string, targetBody []byte, targetContentType, targetAuth string) {
	ctx := r.Context()

	var payload x402.PaymentPayload
	if err := json.Unmarshal([]byte(xPaymentHeader), &payload); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid X-Payment payload")
		return
	}

	// Re-fetch the target's own 402 to learn the authoritative current
	// price. This is what lets us set MaxAmountRequired below (so the
	// facilitator actually enforces the quoted price instead of trusting
	// whatever the caller's payment payload claims) and what lets us record
	// the real settled amount in the ledger instead of a hardcoded 0.
	quote, err := fetchTargetPriceQuote(ctx, target, targetMethod, targetBody, targetContentType, targetAuth)
	if err != nil {
		respond.Error(w, http.StatusBadGateway, "target fetch failed: "+err.Error())
		return
	}
	amountAssetMicros, err := strconv.ParseInt(quote.MaxAmountRequired, 10, 64)
	if err != nil {
		respond.Error(w, http.StatusBadGateway, "target returned invalid maxAmountRequired: "+err.Error())
		return
	}

	// quote.Asset comes straight from the target's own (caller-supplied,
	// unauthenticated) 402 response and is only ever used later, for the
	// OUTBOUND leg to target — the inbound leg below always settles in
	// d.USDCAssetID regardless of what the target claims. Checked here,
	// before ever verifying/settling the inbound payment, so a target that
	// can never be paid (wrong/unsupported asset) is rejected before the
	// caller's inbound payment is collected at all — settling inbound first
	// and only discovering this in payTargetAndRespond would already have
	// set X-Inbound-Settled, billing the caller in full for a call that was
	// never going to reach the target.
	if quoteAssetID, err := strconv.ParseUint(quote.Asset, 10, 64); err != nil || quoteAssetID != d.USDCAssetID {
		log.Printf("x402 relay asset mismatch: quote.Asset=%q parseErr=%v quoteAssetID=%d want=%d", quote.Asset, err, quoteAssetID, d.USDCAssetID)
		respond.Error(w, http.StatusBadGateway, "target quoted an unexpected asset id")
		return
	}

	targetResourceURL := d.FrontendURL + relayPublicPath
	reqs := x402.PaymentRequirements{
		Scheme:            "exact",
		Network:           d.RelayNetwork,
		PayTo:             d.PlatformWalletAddress,
		Asset:             strconv.FormatUint(d.USDCAssetID, 10),
		MaxAmountRequired: quote.MaxAmountRequired,
		// d.BaseURL + real path, matching relayInboundChallenge's challenge
		// above and every actually-cataloged mainnet resource (Amarok,
		// Syra) -- see targetResourceURL's doc comment there. FrontendURL
		// bought a nicer label/logo at the cost of never once appearing in
		// /discovery/resources (confirmed live 2026-08-01: schema-perfect
		// declaration, real settlement, still zero catalog entries) --
		// matching the working examples' convention takes priority.
		Resource:    targetResourceURL,
		Description: "AgentMesh — give your AI agents a wallet, let them pay their own way",
		MimeType:    "application/json",
		// See the matching comment in relaySelfSettle: omitted here too, so
		// this path also POSTed a schema-invalid maxTimeoutSeconds: 0 while
		// advertising 300 in the challenge relayInboundChallenge showed the
		// same caller moments earlier.
		MaxTimeoutSeconds: 300,
		// Without extra.feePayer the facilitator can't locate the fee-pooled
		// stub txn in the payment group and throws server-side (confirmed
		// live 2026-07-31: "Cannot convert undefined to a BigInt") — see the
		// identical, already-working Extra block in runfund.go's reqs.
		Extra: map[string]any{
			"asset":    strconv.FormatUint(d.USDCAssetID, 10),
			"feePayer": d.RelayFeePayer,
			"tag":      "x402-global-challenge",
			"decimals": 6,
		},
		// Same discovery declaration sent in the public 402 challenge above,
		// now also on the struct actually POSTed to /verify and /settle —
		// that's the leg the facilitator uses to register a catalog entry,
		// so this must ride along with the real settlement, not just the
		// informational challenge shown to the caller.
		Extensions: bazaarDiscoveryExtension(target),
	}

	// Set authoritatively, server-side, regardless of what the caller's own
	// payload carried -- see PaymentPayload.Resource's doc comment. Mirrors
	// exactly what relayInboundChallenge showed this same caller a moment
	// earlier: same targetResourceURL identity as reqs.Resource above, same
	// relayTargetDescription for the per-request specifics.
	payload.Resource = resourceInfo(targetResourceURL, relayTargetDescription(target))
	payload.Extensions = bazaarDiscoveryExtension(target)
	// See relaySelfSettle for both of these -- same reasoning, same required
	// v2 fields, and this is the higher-volume of the two settle paths.
	payload.Accepted = reqs.AcceptedV2()
	payload.X402Version = 2

	verifyResult, err := d.FacilitatorClient.Verify(ctx, payload, reqs)
	if err != nil {
		respond.Error(w, http.StatusBadGateway, "facilitator verify failed: "+err.Error())
		return
	}
	if !verifyResult.IsValid {
		respond.Error(w, http.StatusPaymentRequired, "payment invalid: "+verifyResult.Invalid)
		return
	}

	settleResult, err := d.FacilitatorClient.Settle(ctx, payload, reqs)
	if err != nil {
		respond.Error(w, http.StatusBadGateway, "facilitator settle failed: "+err.Error())
		return
	}
	if !settleResult.Success {
		respond.Error(w, http.StatusPaymentRequired, "settlement failed: "+settleResult.Error)
		return
	}

	ledgerRow, err := d.Store.RecordInboundSettlement(ctx, target, settleResult.TxID, amountAssetMicros)
	if err == db.ErrDuplicateSettlement {
		respond.Error(w, http.StatusConflict, "payment already processed")
		return
	}
	if err != nil {
		log.Printf("x402 relay: failed to record inbound settlement: %v", err)
		respond.Error(w, http.StatusInternalServerError, "internal error recording settlement")
		return
	}

	d.payTargetAndRespond(w, r, target, ledgerRow.ID, settleResult.TxID, quote, targetMethod, targetBody, targetContentType, targetAuth)
}

// payTargetAndRespond pays the real target from the platform wallet via the
// facilitator, then relays the target's paid response back to the caller.
// No refund path on failure: x402 has no chargeback primitive, and the
// inbound leg's attribution to us already landed regardless of this outcome.
//
// It takes the already-fetched targetPriceQuote from relaySettleAndForward
// (its only caller) rather than re-fetching the caller-supplied target
// itself. This is deliberate: target is arbitrary and attacker-controlled on
// this public, unauthenticated route, so re-fetching it a second time here
// would let a malicious target answer the first (enforcement/recording)
// fetch cheaply and this second (pay-time) fetch with an inflated amount
// and/or a different payTo — causing the platform wallet to sign and
// broadcast a payment for more than was ever collected from the caller, to
// an address the caller chose. One quote, fetched once per relay cycle, used
// for both enforcement/recording and the actual outbound payment closes that
// gap.
//
// Outbound tx id: paying the target here goes over the target's own
// X-Payment header directly, not through our own FacilitatorClient, so there
// is no SettleResult (and therefore no facilitator-issued transaction id) on
// this leg — the target's paid HTTP response carries no standardized txid
// reference either. RecordOutboundSettlement is called with an empty
// outbound tx id below; that is a real gap in observability given the
// current architecture (the relay pays the target directly rather than via
// a second facilitator round-trip from our side), not an oversight, and not
// something to paper over with a fabricated id.
func (d *Deps) payTargetAndRespond(w http.ResponseWriter, r *http.Request, target, ledgerID, inboundTxID string, quote targetPriceQuote, targetMethod string, targetBody []byte, targetContentType, targetAuth string) {
	ctx := r.Context()

	cfg := nodes.Wallet2PayConfig{
		USDCSigner:                d.USDCSigner,
		PlatformWalletEncMnemonic: d.PlatformWalletEncMnemonic,
		USDCAssetID:               d.USDCAssetID,
		RelayNetwork:              d.RelayNetwork,
		MaxRelayOutboundUSDMicros: d.MaxRelayOutboundUSDMicros,
		ContentType:               targetContentType,
		Authorization:             targetAuth,
	}
	result, err := nodes.PayTargetFromWallet2(ctx, cfg, target, targetMethod, targetBody, nodes.TargetQuote{
		PayTo: quote.PayTo, Asset: quote.Asset, MaxAmountRequired: quote.MaxAmountRequired, FeePayer: quote.FeePayer,
		RawAccept: quote.RawAccept, RawChallenge: quote.RawChallenge,
	})

	// Signals to the orchestrator's own tool402 caller (tool402.go) that the
	// inbound leg (Wallet 1 -> Wallet 2, via the facilitator in
	// relaySettleAndForward) has irreversibly settled AND a real signed
	// outbound payment group now exists, independent of whatever the
	// target's HTTP response says. result.Signed becomes true the instant
	// PayTargetFromWallet2's SignUSDCPaymentGroup call succeeds -- the exact
	// moment this handler used to set the header inline, before its own
	// paid request to target. A signing failure (bad payTo, algod outage,
	// ...) means the target receives nothing at all -- billing the caller
	// in that case would be a real over-charge, not the "money already
	// moved so it's fair to bill" case this header exists to represent.
	// Once a group is signed, it's a submittable claim regardless of what
	// the target's HTTP response says, which is what makes billing on this
	// flag (not on the target's status code) safe: a target that accepts
	// payment and then deliberately returns non-2xx must not be able to
	// dodge billing while still being paid. Must be set before any
	// WriteHeader call below.
	if result.Signed {
		w.Header().Set("X-Inbound-Settled", "true")
		// Carries the real, already-irreversible inbound facilitator tx id
		// out to the caller (tool402.go's executeTool402V2Relay) purely for
		// display -- e.g. surfacing it in a workflow run's console log. Set
		// alongside X-Inbound-Settled, under the same "money already moved"
		// condition, since that's the only point at which this id is
		// meaningful to show.
		w.Header().Set("X-Settlement-TxId", inboundTxID)
	}
	// The OUTBOUND leg's own settlement id (Wallet 2 -> target), when the
	// target returned one -- separate from X-Settlement-TxId above, which
	// is only ever the inbound leg. Both, together, are what let a
	// workflow's console log show the full real payment chain: caller ->
	// Wallet 2 (X-Settlement-TxId) and Wallet 2 -> target
	// (X-Outbound-Settlement-TxId).
	if result.OutboundTxID != "" {
		w.Header().Set("X-Outbound-Settlement-TxId", result.OutboundTxID)
	}

	if err != nil {
		d.Store.RecordOutboundSettlement(ctx, ledgerID, "", "failed")
		status := http.StatusBadGateway
		var payErr *nodes.Wallet2PayError
		if errors.As(err, &payErr) {
			status = payErr.StatusCode
		}
		respond.Error(w, status, err.Error())
		return
	}

	// The target's paid response must actually succeed for the outbound leg
	// to count as settled — a 402/4xx/5xx here means the platform wallet's
	// payment was rejected (or the target errored) despite the inbound leg
	// already being collected. Relay the target's real status/body back to
	// the caller either way (no refund path), but only record "settled"
	// when the target actually accepted the payment.
	//
	// See the empty outbound-tx-id note in the function doc comment above:
	// there is no facilitator-issued outbound transaction id available at
	// this call site with the current design.
	status := "settled"
	if !result.Settled {
		status = "failed"
	}
	d.Store.RecordOutboundSettlement(ctx, ledgerID, "", status)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(result.StatusCode)
	w.Write(result.ResponseBody)
}
