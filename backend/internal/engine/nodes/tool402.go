package nodes

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agentmesh/backend/internal/alert"
	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/x402"
)

// relayHTTPClient is used only for the two calls to our own /x402/relay
// endpoint (the quote fetch and the paid retry). It shares toolHTTPClient's
// SSRF-safe dialer (dialFn, tool.go) but needs a longer timeout: the relay
// handler's own worst-case latency for a single request can exceed
// toolHTTPClient's 10s budget by itself — up to ~10s re-fetching the
// target's quote, up to 20s each for the facilitator's Verify and Settle
// calls (internal/x402/facilitator.go), plus the outbound payment request
// to target (another ~10s budget). A caller-side timeout shorter than the
// callee's own worst case means "the orchestrator gave up waiting" and
// "the inbound leg genuinely never settled" become indistinguishable from
// a transport error, which is unsafe to resolve either way: assuming
// settlement risks billing for a payment that never happened, and assuming
// no settlement risks never billing for one that did (the underlying
// vector the X-Inbound-Settled header exists to close). Sized with
// headroom above the relay's own worst case rather than exactly matching
// it, so ordinary slow-but-real responses aren't cut off right at the
// boundary.
var relayHTTPClient = &http.Client{
	Timeout: 90 * time.Second,
	Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialFn(ctx, network, addr)
		},
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if err := validateURL(req.URL.String()); err != nil {
			return err
		}
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		return nil
	},
}

// WalletSigner signs and submits an Algorand payment transaction.
// Satisfied by *wallet.Service.
type WalletSigner interface {
	SignAndSendPayment(ctx context.Context, encMnemonic, toAddress string, microAlgo uint64) (string, error)
}

func QuoteX402(ctx context.Context, rawURL string) (map[string]any, error) {
	if err := urlValidator(rawURL); err != nil {
		return nil, err
	}
	// Try the real x402 v2 challenge shape first, via the same prober
	// ExecuteTool402V2/ProbeX402Price use. The legacy parser below only
	// understands this codebase's own pre-v2 flat-quote dialect
	// ({"price": "0.002", ...}) — real v2 targets (confirmed live against
	// canix402-api.compx.io) never speak that: their amount lives at
	// accepts[0].amount/maxAmountRequired, sometimes only inside a base64
	// Payment-Required header with an empty body, which the legacy path
	// can't see at all and silently fell back to a hardcoded "0" for.
	isV2, notPaymentRequired, _, quote, err := probeTool402Endpoint(ctx, rawURL, http.MethodGet, nil, "")
	// A POST-only resource answers a bare GET with 404/405 before it ever
	// reaches its own 402 gate, so a GET-only probe would report a real,
	// payable endpoint as unpriceable. Only retried when GET produced no
	// challenge at all — never when it did, so a working GET is never
	// second-guessed.
	if notPaymentRequired || (err != nil && !isV2) {
		if v2, npr, _, q, perr := probeTool402Endpoint(ctx, rawURL, http.MethodPost, nil, ""); perr == nil && v2 && !npr {
			isV2, quote, err = true, q, nil
		}
	}
	if err == nil && isV2 {
		out := map[string]any{
			"price":     strconv.FormatFloat(float64(quote.MaxAmountRequired)/1e6, 'f', -1, 64),
			"unit":      "call",
			"asset":     assetSymbol(quote.Asset),
			"network":   "algorand",
			"recipient": quote.PayTo,
		}
		// What the target says it needs from a caller, straight out of its
		// own challenge — this is what lets the canvas show real, per-endpoint
		// input fields for an arbitrary endpoint nobody hardcoded support for.
		if params, method, in := ParamsFromChallenge(quote.RawChallenge); len(params) > 0 {
			out["params"] = params
			out["paramsIn"] = in
			if method != "" {
				out["method"] = method
			}
		}
		return out, nil
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	resp, err := toolHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// Always attempt to parse payment info from header+body regardless of status.
	// Proxies (Cloudflare tunnels) may rewrite 402 → 200/503 or strip headers.
	legacyQuote := parsePaymentHeader(resp)
	if _, hasPrice := legacyQuote["price"]; hasPrice {
		return legacyQuote, nil
	}
	// Neither dialect produced a price. Reporting "0" here (as this did until
	// 2026-08-02) is worse than useless: a free endpoint and an endpoint that
	// simply doesn't speak x402 become indistinguishable, and the canvas
	// cheerfully shows a 0 price for a URL that can never be paid or called.
	// Confirmed live against https://kepaix.com/api/x402-publisher-credits.php,
	// which answers every request shape with a flat 400 and never a 402.
	return nil, &ErrNoPaymentChallenge{Status: resp.StatusCode}
}

// ErrNoPaymentChallenge means a URL answered, but not with anything x402:
// no 402 status, no v2 accepts[], no legacy price. Distinct from a transport
// error — the endpoint is reachable, it just isn't a paid resource.
type ErrNoPaymentChallenge struct{ Status int }

func (e *ErrNoPaymentChallenge) Error() string {
	return fmt.Sprintf("this URL answered with HTTP %d and no x402 payment challenge — it may not be an x402 endpoint, or it may expect a different request shape", e.Status)
}

// testnetUSDCAssetID mirrors mainnetUSDCAssetID for the other network a
// probed target's accepts[].asset may name (main.go wires both ids as
// USDCAssetID depending on environment; this package only needs them for
// display, not for signing, so they're kept as local literals rather than
// threading a shared constant through).
const testnetUSDCAssetID = 10458941

// assetSymbol turns a v2 challenge's accepts[].asset (an Algorand ASA id, or
// empty/"0" for native ALGO) into the ticker QuoteX402 displays to the user
// — without this, the frontend has no way to know a quote is priced in USDC
// rather than ALGO.
func assetSymbol(assetID string) string {
	switch assetID {
	case "", "0":
		return "ALGO"
	case strconv.Itoa(mainnetUSDCAssetID), strconv.Itoa(testnetUSDCAssetID):
		return "USDC"
	default:
		return "ASA-" + assetID
	}
}

// mapAt walks a chain of keys through nested map[string]any values, returning
// nil the moment any hop is missing or isn't a map. Every level of a Bazaar
// extension is optional, so this keeps the extraction below readable instead
// of a type assertion per hop.
func mapAt(m map[string]any, keys ...string) map[string]any {
	for _, k := range keys {
		if m == nil {
			return nil
		}
		next, _ := m[k].(map[string]any)
		m = next
	}
	return m
}

// bazaarInputKeys are the field names a target may use under
// extensions.bazaar.info.input (and the matching schema node) to declare its
// caller-supplied parameters. "queryParams" is the one the official
// @x402/extensions package emits and the only one confirmed live
// (canix402-api.compx.io, 2026-08-02); the body-shaped names are accepted
// read-side so a POST-based target that picked a different-but-obvious
// spelling still yields usable fields instead of silently none.
var bazaarInputKeys = []string{"queryParams", "body", "bodyFields", "bodyParams"}

// ParamsFromChallenge extracts the input parameters a real x402 v2 target
// declares for itself in its challenge's Bazaar extension, so the canvas can
// show a caller exactly what an arbitrary endpoint needs without anyone
// hand-transcribing its docs. Returns the declared params, the HTTP method
// the target expects, and where the params belong on the wire ("query" or
// "body").
//
// Two sources, both optional, both used: info.input carries an example value
// per param, while schema.properties.input.properties.* carries the real
// types and — crucially — which params are required. The examples are
// placeholders, not usable values (canix402's is the Algorand zero address),
// so they land in Description rather than Default. A target declaring
// neither yields no params, which is not an error: plenty of real endpoints
// take no input at all.
func ParamsFromChallenge(challenge map[string]any) (params []models.ParamDef, method string, in string) {
	bazaar := mapAt(challenge, "extensions", "bazaar")
	if bazaar == nil {
		return nil, "", ""
	}
	input := mapAt(bazaar, "info", "input")
	method, _ = input["method"].(string)
	schemaInput := mapAt(bazaar, "schema", "properties", "input", "properties")

	for _, key := range bazaarInputKeys {
		examples, _ := input[key].(map[string]any)
		spec := mapAt(schemaInput, key)
		props := mapAt(spec, "properties")
		if len(examples) == 0 && len(props) == 0 {
			continue
		}

		required := map[string]bool{}
		if reqList, ok := spec["required"].([]any); ok {
			for _, r := range reqList {
				if name, ok := r.(string); ok {
					required[name] = true
				}
			}
		}

		seen := map[string]bool{}
		names := make([]string, 0, len(examples)+len(props))
		for _, src := range []map[string]any{props, examples} {
			for name := range src {
				if !seen[name] {
					seen[name] = true
					names = append(names, name)
				}
			}
		}
		// Stable ordering: map iteration is randomized, and these render as
		// an ordered list of form fields that must not reshuffle between
		// two probes of the same endpoint.
		sort.Strings(names)

		for _, name := range names {
			paramType := "string"
			if p := mapAt(props, name); p != nil {
				if t, ok := p["type"].(string); ok && t != "" {
					paramType = t
				}
			}
			desc := ""
			if ex, ok := examples[name]; ok {
				desc = fmt.Sprintf("example: %v", ex)
			}
			params = append(params, models.ParamDef{
				Name:        name,
				Type:        paramType,
				Required:    required[name],
				Description: desc,
			})
		}

		in = "body"
		if key == "queryParams" {
			in = "query"
		}
		return params, method, in
	}
	return nil, method, ""
}

// maxParamFileBytes bounds a single uploaded file's decoded size. Files live
// inside the workflow document, so this is really a bound on how large a
// workflow row can grow — and on how much gets re-sent on every save. The
// frontend enforces the same ceiling before encoding; this is the
// authoritative check, since the frontend's can be bypassed.
const maxParamFileBytes = 2 << 20 // 2 MiB

// buildTargetRequest turns a tool402 node's configured inputs into the actual
// request to make: the endpoint URL to hit, the body to send, and the content
// type that body is in. Three shapes, picked by what the inputs actually are:
//
//   - any file param with content -> multipart/form-data carrying every param
//     as a part (the only encoding that can carry a file at all)
//   - otherwise GET -> values appended to the query string, no body
//   - otherwise -> a flat JSON object body
//
// Values already present on the endpoint URL are never overwritten: an agent's
// LLM-chosen args (appended onto the URL by executeFunctionCall before this
// runs) are a deliberate per-call choice, while these are static config.
// Custom params take precedence over discovered ones of the same name, since
// the user typing a field by hand is the more specific intent.
// bodyPlaceholder matches a single {{kind:name}} reference in a JSON body
// template. Name runs to the closing brace, so it may contain anything but
// "}" -- endpoint field names are not restricted to identifiers.
var bodyPlaceholder = regexp.MustCompile(`\{\{(param|file|fileName|fileType):([^}]+)\}\}`)

// expandBodyTemplate fills a node's JSON body template from its configured
// params. Four forms, each naming a param:
//
//	{{param:x}}     the text param's value (or its ParamDefaults entry)
//	{{file:x}}      the file param's bytes, base64 -- what an endpoint that
//	                takes uploads inside JSON actually wants
//	{{fileName:x}}  the uploaded file's original name
//	{{fileType:x}}  its MIME type
//
// Values are JSON-escaped, so a placeholder belongs inside a string literal
// ("{{file:resume}}") and a filename containing a quote cannot break the
// document. A reference to a param that does not exist is an error rather
// than an empty string: this body is about to be sent with a real payment
// attached, and an endpoint that silently receives null where a resume
// should be still charges for the call.
func expandBodyTemplate(node models.WorkflowNode, template string) ([]byte, error) {
	byName := make(map[string]models.CustomParam, len(node.CustomParams))
	for _, p := range node.CustomParams {
		byName[p.Name] = p
	}

	var missing []string
	expanded := bodyPlaceholder.ReplaceAllStringFunc(template, func(match string) string {
		parts := bodyPlaceholder.FindStringSubmatch(match)
		kind, name := parts[1], strings.TrimSpace(parts[2])
		p, defined := byName[name]
		if !defined {
			// A text param may live in ParamDefaults instead (a value typed
			// against a schema the endpoint declared), which has no file
			// counterpart -- the other three forms genuinely need a param.
			if v, ok := node.ParamDefaults[name]; ok && kind == "param" {
				return jsonStringEscape(v)
			}
			missing = append(missing, match)
			return match
		}
		switch kind {
		case "param":
			return jsonStringEscape(p.Value)
		case "file":
			return jsonStringEscape(p.Value) // already base64 in Value
		case "fileName":
			return jsonStringEscape(p.FileName)
		case "fileType":
			return jsonStringEscape(p.MIMEType)
		}
		return match
	})

	if len(missing) > 0 {
		return nil, fmt.Errorf("request body references %s, which this node has no field for", strings.Join(missing, ", "))
	}
	if !json.Valid([]byte(expanded)) {
		return nil, fmt.Errorf("request body is not valid JSON once its fields are filled in")
	}
	return []byte(expanded), nil
}

// jsonStringEscape renders s as it must appear INSIDE a JSON string literal
// (no surrounding quotes), so substituting it into a template cannot produce
// a malformed document.
func jsonStringEscape(s string) string {
	quoted, err := json.Marshal(s)
	if err != nil || len(quoted) < 2 {
		return ""
	}
	return string(quoted[1 : len(quoted)-1])
}

func buildTargetRequest(node models.WorkflowNode, method string) (models.WorkflowNode, []byte, string, error) {
	// A hand-written JSON body replaces the field-derived request entirely:
	// the fields are still configured (and a file param still uploaded), but
	// they reach the endpoint only where the template references them.
	if node.BodyMode == models.BodyModeJSON {
		if strings.TrimSpace(node.BodyTemplate) == "" {
			return node, nil, "", nil
		}
		body, err := expandBodyTemplate(node, node.BodyTemplate)
		if err != nil {
			return node, nil, "", err
		}
		return node, body, "application/json", nil
	}

	text := map[string]string{}
	for k, v := range node.ParamDefaults {
		if v != "" {
			text[k] = v
		}
	}
	var files []models.CustomParam
	for _, p := range node.CustomParams {
		if p.Name == "" {
			continue
		}
		if p.Kind == "file" {
			if p.Value != "" {
				files = append(files, p)
			}
			// A file param never doubles as a text field: its Value is
			// base64 bytes, which would otherwise be pasted into a query
			// string or JSON field as a giant meaningless string.
			delete(text, p.Name)
			continue
		}
		if p.Value != "" {
			text[p.Name] = p.Value
		}
	}

	if len(files) > 0 {
		body, contentType, err := multipartBody(text, files)
		if err != nil {
			// Fall through to a bodyless request rather than sending a
			// half-built multipart payload: a target rejecting a request with
			// missing fields is diagnosable, one parsing a truncated body is
			// not.
			log.Printf("x402: building multipart body for node %s failed: %v", node.ID, err)
			return node, nil, "", nil
		}
		return node, body, contentType, nil
	}

	if len(text) == 0 {
		return node, nil, "", nil
	}

	if method == "" || method == http.MethodGet {
		u, err := url.Parse(node.Endpoint)
		if err != nil {
			return node, nil, "", nil
		}
		q := u.Query()
		for k, v := range text {
			if q.Get(k) != "" {
				continue
			}
			q.Set(k, v)
		}
		u.RawQuery = q.Encode()
		node.Endpoint = u.String()
		return node, nil, "", nil
	}

	encoded, err := json.Marshal(text)
	if err != nil {
		return node, nil, "", nil
	}
	return node, encoded, "application/json", nil
}

// multipartBody encodes text fields and files as a single multipart/form-data
// payload, returning it with the exact content type (including the generated
// boundary) that must accompany it — a multipart body sent under any other
// content type is unparseable by the receiver.
func multipartBody(text map[string]string, files []models.CustomParam) ([]byte, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range text {
		if err := mw.WriteField(k, v); err != nil {
			return nil, "", err
		}
	}
	for _, f := range files {
		raw, err := base64.StdEncoding.DecodeString(f.Value)
		if err != nil {
			return nil, "", fmt.Errorf("param %q: %w", f.Name, err)
		}
		if len(raw) > maxParamFileBytes {
			return nil, "", fmt.Errorf("param %q: file is %d bytes, over the %d byte limit", f.Name, len(raw), maxParamFileBytes)
		}
		name := f.FileName
		if name == "" {
			name = f.Name
		}
		part, err := mw.CreateFormFile(f.Name, name)
		if err != nil {
			return nil, "", err
		}
		if _, err := part.Write(raw); err != nil {
			return nil, "", err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), mw.FormDataContentType(), nil
}

func ExecuteTool402(ctx context.Context, node models.WorkflowNode, rc RunContexter, wallet models.AgentWallet, signer WalletSigner) (any, error) {
	if err := urlValidator(node.Endpoint); err != nil {
		return nil, err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, node.Endpoint, nil)
	resp, err := toolHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusPaymentRequired {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, httpResponseLimit))
		var result any
		if json.Unmarshal(b, &result) == nil {
			return result, nil
		}
		return string(b), nil
	}

	quote := parsePaymentHeader(resp) // reads body internally
	resp.Body.Close()

	if wallet.EncryptedMnemonic == "" || signer == nil {
		return map[string]any{"error": "payment required but no agent wallet configured", "quote": quote}, nil
	}

	priceStr, _ := quote["price"].(string)
	recipient, _ := quote["recipient"].(string)
	if recipient == "" {
		return nil, fmt.Errorf("x402: no recipient address in payment header")
	}
	priceFloat, err := strconv.ParseFloat(priceStr, 64)
	if err != nil || priceFloat <= 0 {
		return nil, fmt.Errorf("x402: invalid price %q", priceStr)
	}
	microAlgo := uint64(priceFloat * 1e6)

	txID, err := signer.SignAndSendPayment(ctx, wallet.EncryptedMnemonic, recipient, microAlgo)
	if err != nil {
		return nil, fmt.Errorf("x402 payment failed: %w", err)
	}

	algoAmount := fmt.Sprintf("%.6f", float64(microAlgo)/1e6)
	explorerURL := "https://lora.algokit.io/testnet/transaction/" + txID

	// Retry the original request with the payment proof header.
	req2, _ := http.NewRequestWithContext(ctx, http.MethodGet, node.Endpoint, nil)
	req2.Header.Set("X-Payment-Txid", txID)
	resp2, err := toolHTTPClient.Do(req2)
	if err != nil {
		return map[string]any{"status": "payment_sent", "txId": txID, "amount": algoAmount, "explorerURL": explorerURL, "error": "retry request failed: " + err.Error()}, nil
	}
	defer resp2.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp2.Body, httpResponseLimit))
	var retryResult any
	if json.Unmarshal(b, &retryResult) == nil {
		return map[string]any{"status": "payment_sent", "txId": txID, "amount": algoAmount, "explorerURL": explorerURL, "response": retryResult}, nil
	}
	return map[string]any{"status": "payment_sent", "txId": txID, "amount": algoAmount, "explorerURL": explorerURL, "response": string(b)}, nil
}

func parsePaymentHeader(resp *http.Response) map[string]any {
	// Try header first (direct connections). Cloudflare and other proxies may
	// strip non-standard response headers, so fall back to the response body.
	header := resp.Header.Get("X-Payment-Required")
	if header == "" {
		header = resp.Header.Get("WWW-Authenticate")
	}
	var result map[string]any
	if header != "" {
		if err := json.Unmarshal([]byte(header), &result); err == nil {
			return result
		}
	}
	// Body fallback: our server returns {"error":"Payment required","payment":{...}}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, httpResponseLimit))
	var envelope struct {
		Payment map[string]any `json:"payment"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Payment != nil {
		return envelope.Payment
	}
	// Last resort: try parsing body directly as the payment object
	if err := json.Unmarshal(body, &result); err == nil {
		return result
	}
	return map[string]any{"raw": header}
}

// USDCGroupSigner signs a gasless USDC atomic-payment group for the relay's
// X-Payment header. Satisfied by *wallet.Service (SignUSDCPaymentGroup).
//
// SignUSDCPaymentSingle is the other half: a plain, self-fee-paying,
// single-transaction "exact" scheme payment for PayTargetFromWallet2's
// direct-to-third-party outbound leg, which no arbitrary target's own
// facilitator can cosign a fee-pooled stub for. Both methods live on the
// same interface since exactly one concrete type (*wallet.Service) signs
// for both legs today.
type USDCGroupSigner interface {
	SignUSDCPaymentGroup(ctx context.Context, encMnemonic, payTo string, assetID, amountMicros uint64, feePayerAddr string) ([]string, int, error)
	SignUSDCPaymentSingle(ctx context.Context, encMnemonic, payTo string, assetID, amountMicros uint64) ([]string, int, error)
}

// X402RelayConfig bundles what an agent-attached tool402 call needs to route
// through the AgentMesh relay (Wallet 1) exactly the way a standalone
// tool402 node already does via ExecuteTool402V2. Threaded from Runner
// through ExecuteAgent -> callOpenAICompat/callGemini -> executeFunctionCall.
type X402RelayConfig struct {
	USDCSigner               USDCGroupSigner
	PlatformSpendEncMnemonic string
	ExpectedAssetID          uint64
	RelayBaseURL             string
	// Facilitator/PlatformWalletAddress/RelayNetwork/RelayFeePayer/FrontendURL
	// are only used by executeTool402V2Relay's SettlePlatformFee call, right
	// after the vendor-cost leg settles -- see that call site's comment. A
	// zero Facilitator (dev/test wiring that never configured one) makes the
	// fee settlement a no-op rather than a panic; the vendor payment and the
	// internal credit debit are both unaffected either way.
	Facilitator           *x402.FacilitatorClient
	PlatformWalletAddress string
	RelayNetwork          string
	RelayFeePayer         string
	FrontendURL           string
	// Ledger reserves/commits/releases credits for executeTool402RunLevel
	// only -- the in-memory run-level pool, sized to `estimate` (never
	// padded with markup, see MarkupLedger), read only when
	// toolIsRunFunded(node.ID) is true. Every other v2 dispatch path uses
	// PerCallLedger instead; the legacy-dialect branch below never reads
	// this field directly either, see LegacyLedger.
	Ledger RunLedger
	// PerCallLedger is the DB-backed, per-call ledger executeTool402V2Relay
	// reserves/commits/releases amount+markup against for any v2 dispatch
	// NOT covered by run funding for this specific tool -- both when the
	// whole agent has no run-level pre-fund (RunFundingID == "") and when
	// it does but THIS tool's own probe failed during estimation
	// (toolIsRunFunded(node.ID) == false; see that method's doc comment).
	// Deliberately never Ledger: Ledger is the in-memory run-level pool
	// once RunFundingID != "", sized only for the tools reserveAndFundRun
	// actually folded into its estimate -- reserving a not-run-funded
	// tool's amount+markup against that pool would either draw down
	// another tool's budget it was never sized for, or (now that a call
	// reserves amount+markup instead of amount alone) spuriously exhaust
	// it and hard-block the run even though the user's real DB balance is
	// untouched and sufficient. Always r.newPaymentLedger(wf, run) in
	// production, same as LegacyLedger/FlatFeeLedger.
	PerCallLedger CallLedger
	// MarkupLedger is the platform-flat-markup counterpart to Ledger, read
	// only by executeTool402RunLevel. Kept as a SEPARATE pool rather than
	// folded into Ledger's own budget: reserveAndFundRun sizes Ledger to
	// `estimate` (the ceiling on what executeTool402RunLevel may pay OUT to
	// vendors from Wallet 2) and MarkupLedger to `markupTotal` (a pure
	// credits-side accounting cap -- both pools are already backed by the
	// SAME single on-chain settlement, creditReserve = estimate+markupTotal,
	// see reserveAndFundRun). If markup were added into Ledger's own pool
	// instead, a single call's real vendor amount could exceed `estimate` by
	// borrowing unused markup headroom left over from other
	// funded-but-never-called tools, letting Wallet 2 pay out real USDC
	// beyond what this run's vendor-cost budget actually allows. Unused for the
	// per-call relay path (executeTool402V2Relay), which reserves its own
	// amount+markup total from one DB-backed ledger per call — there's no
	// upfront padded pool to protect against in that path.
	MarkupLedger RunLedger
	// LegacyLedger is the original per-call, DB-backed ledger (always
	// r.newPaymentLedger(wf, run), never the run-level in-memory pool) —
	// what the legacy flat-quote dialect's direct-pay branch reserves/
	// commits/releases its flat fee against, regardless of whether Ledger
	// above has been swapped to the run-level pool for this same agent's v2
	// tools. Legacy-dialect billing must be identical whether or not the
	// agent also happens to have a run-funded v2 tool attached — reading
	// Ledger here instead would decrement a pool sized only for v2 quotes,
	// spuriously blocking legacy calls or committing them against credits
	// that were already converted into a real on-chain settlement to
	// Wallet 2 for an unrelated call.
	LegacyLedger CallLedger

	// RunFundingID is set (non-empty) the moment the agent's run has already
	// settled a single lump-sum inbound x402 payment covering its attached
	// v2 tool402 nodes (Task 5's reserveAndFundRun) — a property of the RUN,
	// not of any one node's dialect. "" means no run-level pre-fund happened
	// (no attached v2 tools, or the estimate came back 0), so v2 calls keep
	// taking the existing per-call public-relay path unchanged.
	RunFundingID string
	// RunFundedToolIDs is the set of attached tool402 node IDs that
	// reserveAndFundRun confirmed as real v2 targets and folded into
	// RunFundingID's up-front reservation — empty/nil when RunFundingID is
	// "". A legacy-dialect tool attached to the same run-funded agent is
	// never in this set (reserveAndFundRun's estimator skips it), so
	// provider.go's pre-flight floor guard uses this, not a blanket
	// RunFundingID != "" check, to decide whether a given attached tool402
	// node's own DB balance still needs checking before its first outbound
	// HTTP call.
	RunFundedToolIDs map[string]bool
	// RunFundingTxID is the on-chain id of the single inbound settlement
	// (Wallet 1 -> Wallet 2) that RunFundingID refers to — the only inbound
	// leg a run-funded call has, since that settlement happened once, in
	// bulk, before the agent's tool-calling loop started. Surfaced on every
	// run-funded payment receipt as its txId: without it a run-funded tool
	// call produced a receipt with no tx id at all, leaving real money that
	// had moved unverifiable from the UI. Empty whenever RunFundingID is
	// "".
	RunFundingTxID string
	// Wallet2 carries what's needed to pay a real target directly from
	// Wallet 2, in-process, once RunFundingID is set. See Wallet2PayConfig.
	Wallet2 Wallet2PayConfig
	// RecordSettlement records one run-funded per-call settlement audit row
	// (x402_relay_settlements, run_funding_id-linked). amountUSDMicros must
	// be the real settled amount — RecordRunFundedSettlement takes it at
	// INSERT time since there is no later call that backfills it.
	RecordSettlement func(ctx context.Context, target string, amountUSDMicros int64, settled bool) error
	// FlatFeeLedger reserves/commits/releases credits for an agent-attached
	// billable flat-fee node (BillableFlatFee -- an attached "http" Tool or
	// any Action/connector node), atomically per call, exactly like
	// LegacyLedger does for the legacy x402 dialect. Deliberately NOT a
	// batched-at-turn-end debit: checking balance without reserving and only
	// debiting once the whole agent turn ends would let every iteration of
	// the tool-calling loop check the same stale balance and collectively
	// overspend past what the user can cover (identical hazard to the one
	// newPaymentLedger's doc comment describes for x402 payments -- see
	// runner.go). A nil Reserve/Commit/Release is a no-op, matching the
	// pre-existing nil-checker convention elsewhere in this package.
	FlatFeeLedger CallLedger
}

// toolIsRunFunded reports whether toolID's real cost is already covered by
// this run's up-front lump-sum settlement -- true only when the run has a
// funding id AND reserveAndFundRun's estimator specifically confirmed this
// tool as a real v2 target it folded into that estimate. A run-funded
// agent with a tool whose probe failed during estimation (or a
// legacy-dialect tool attached alongside a funded v2 one) must NOT be
// treated as run-funded for that specific tool -- both call sites that used
// to hand-write this condition differently now share this one definition.
func (cfg X402RelayConfig) toolIsRunFunded(toolID string) bool {
	return cfg.RunFundingID != "" && cfg.RunFundedToolIDs[toolID]
}

// Tool402PaymentResult is what ExecuteTool402V2 returns. SettledUSDMicros
// and DebitKind describe a charge that has ALREADY been committed via the
// caller-supplied PaymentLedger by the time this returns — callers report
// these for logging/audit purposes and must not debit again. Both are zero
// when no payment was sent (e.g. the endpoint didn't require one, no wallet
// was configured, or a reservation was taken but released because the
// payment never actually settled).
type Tool402PaymentResult struct {
	Response any
	// SettledUSDMicros is the real vendor/on-chain component only -- for a
	// v2 call this is strictly less than the total actually debited from
	// the user's credits, since PlatformFeeUSDMicros below is committed as
	// a second, separate debit_ledger row on top of it. Kept vendor-cost-
	// only (not the sum) so this field's meaning matches its DebitKind tag
	// and existing callers reading it for on-chain/audit purposes aren't
	// silently handed a blended number.
	SettledUSDMicros int64
	DebitKind        string
	// PlatformFeeUSDMicros is the flat markup committed alongside
	// SettledUSDMicros for a v2 call (models.X402PlatformFeeUSDMicros,
	// DebitKind models.DebitKindX402PlatformFee) -- zero for the legacy
	// dialect, whose SettledUSDMicros already IS the flat markup with no
	// separate vendor-cost component to add it to.
	PlatformFeeUSDMicros int64

	// TxID/ExplorerURL identify the INBOUND settlement leg that paid for
	// this call (caller -> Wallet 2): the per-call facilitator settlement
	// on the public-relay path, or the run's single up-front funding
	// settlement on the run-funded path (where no per-call inbound leg
	// exists at all — see executeTool402RunLevel). OutboundTxID/
	// OutboundExplorerURL are the second leg (Wallet 2 -> target), when the
	// target returned one; not every target does.
	//
	// These duplicate the txId/explorerURL/outboundTxId/outboundExplorerURL
	// keys merged into Response, and exist because that merge is only
	// possible when the target's own response happens to unmarshal as a
	// JSON object — a target answering with a bare array or string has
	// nowhere to carry sibling fields, and the agent-attached path
	// (provider.go's paymentReceipt, which reads the merged map) then had
	// no tx id to show at all.
	TxID                string
	ExplorerURL         string
	OutboundTxID        string
	OutboundExplorerURL string
	// PlatformFeeTxID/PlatformFeeExplorerURL identify the second, dedicated
	// Wallet 1 -> Wallet 2 settlement that pays PlatformFeeUSDMicros
	// on-chain -- see SettlePlatformFee's doc comment. Both empty when no
	// fee was owed (legacy dialect, whose SettledUSDMicros already IS the
	// flat markup with nothing separate to settle) or when settlement
	// failed (logged/alerted, not fatal to the call -- see the call site in
	// executeTool402V2Relay; executeTool402RunLevel never sets these, its
	// markup is already covered by reserveAndFundRun's single up-front
	// settlement).
	PlatformFeeTxID        string
	PlatformFeeExplorerURL string
}

// ChallengeAcceptsFromHeader extracts a v2 challenge's accepts[] from a
// Payment-Required response header, for targets that put the full
// base64-encoded challenge there and leave the JSON body empty/minimal —
// Prism's live endpoint does exactly this (confirmed 2026-07-31: body is
// `{}`, the real accepts[] is only in this header). This is the same header
// name and encoding this codebase's own relay emits (see
// relayInboundChallenge in handlers/x402relay.go), so it's a real,
// currently-used wire format, not a defensive guess.
func ChallengeAcceptsFromHeader(header http.Header) []map[string]any {
	challenge := ChallengeFromHeader(header)
	if challenge == nil {
		return nil
	}
	acceptsRaw, _ := challenge["accepts"].([]any)
	accepts := make([]map[string]any, 0, len(acceptsRaw))
	for _, a := range acceptsRaw {
		if m, ok := a.(map[string]any); ok {
			accepts = append(accepts, m)
		}
	}
	return accepts
}

// ChallengeFromHeader decodes a full v2 challenge object (not just its
// accepts[]) from a Payment-Required response header, when present -- some
// targets need the whole object echoed back verbatim on the paid retry
// (see TargetQuote.RawChallenge), not just the parsed fields
// ChallengeAcceptsFromHeader extracts.
func ChallengeFromHeader(header http.Header) map[string]any {
	b64 := header.Get("Payment-Required")
	if b64 == "" {
		return nil
	}
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil
	}
	var challenge map[string]any
	if json.Unmarshal(decoded, &challenge) != nil {
		return nil
	}
	return challenge
}

// x402Quote is what a real v2 challenge's accepts[0] entry carries that
// callers in this package need — payTo/asset for actually paying it,
// MaxAmountRequired (parsed to USD micros) for gating/estimating.
type x402Quote struct {
	PayTo             string
	Asset             string
	MaxAmountRequired int64 // USD micros
	// FeePayer, when the target declares one in accepts[0].extra.feePayer,
	// names the shared facilitator address that will cosign a fee-pooled
	// stub txn for this specific payment. A target with no declared
	// feePayer is signaling the opposite -- a plain, self-fee-paying single
	// transaction -- so this must never default to our own relay's feePayer
	// constant; presence/absence of this exact field on the target's OWN
	// quote is what selects the signing scheme (see PayTargetFromWallet2).
	FeePayer string
	// RawAccept and RawChallenge are the exact accepts[0] entry and the
	// exact full challenge object the target returned, kept verbatim (not
	// reconstructed) -- see TargetQuote's matching fields for why.
	RawAccept    map[string]any
	RawChallenge map[string]any
}

// probeTool402Endpoint fetches endpoint's 402 challenge (if any) and reports
// whether it speaks real x402 v2 (accepts[] present) along with its current
// quote. notPaymentRequired=true means the endpoint answered something
// other than 402 (caller treats that as "no payment needed", exactly like
// ExecuteTool402V2 does today).
//
// method/body let this reach targets that gate on HTTP method before ever
// looking at payment state (e.g. a POST-only resource 404s a bare GET
// before it gets a chance to return 402) — real x402 endpoints are not
// guaranteed to be GET-compatible. method empty defaults to GET, matching
// every caller's behavior before this parameter existed. body is only ever
// sent when method is not GET, mirroring nodes.go's callHTTP convention for
// the plain "tool" node type.
//
// contentType describes body's encoding; empty defaults to application/json,
// which is what this always assumed. It has to be explicit for a multipart
// body, whose generated boundary lives in the content type and without which
// the receiver cannot parse a single field.
func probeTool402Endpoint(ctx context.Context, endpoint, method string, body []byte, contentType string, targetAuth ...string) (isV2 bool, notPaymentRequired bool, rawResponse any, quote x402Quote, err error) {
	if err := urlValidator(endpoint); err != nil {
		return false, false, nil, x402Quote{}, err
	}
	if method == "" {
		method = http.MethodGet
	}
	var bodyReader io.Reader
	if method != http.MethodGet && len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}
	req, _ := http.NewRequestWithContext(ctx, method, endpoint, bodyReader)
	if bodyReader != nil {
		if contentType == "" {
			contentType = "application/json"
		}
		req.Header.Set("Content-Type", contentType)
	}
	// Variadic so every existing caller stays untouched -- only
	// ExecuteTool402V2 passes one, to make its pre-relay probe see the same
	// target as the relay's own fetchTargetPriceQuote does (both must agree
	// on whether the target actually requires payment).
	if len(targetAuth) > 0 && targetAuth[0] != "" {
		req.Header.Set("Authorization", "Bearer "+targetAuth[0])
	}
	resp, err := toolHTTPClient.Do(req)
	if err != nil {
		return false, false, nil, x402Quote{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPaymentRequired {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, httpResponseLimit))
		var result any
		if json.Unmarshal(b, &result) == nil {
			return false, true, result, x402Quote{}, nil
		}
		return false, true, string(b), x402Quote{}, nil
	}

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, httpResponseLimit))
	var rawChallenge map[string]any
	json.Unmarshal(respBody, &rawChallenge)
	var v2Challenge struct {
		Accepts []map[string]any `json:"accepts"`
	}
	json.Unmarshal(respBody, &v2Challenge)
	if len(v2Challenge.Accepts) == 0 {
		// Body carried no accepts[] -- some real targets (Prism's live
		// endpoint, confirmed 2026-07-31) put the full challenge in the
		// Payment-Required header instead, so the raw object has to come
		// from there too, not the (empty/minimal) body.
		v2Challenge.Accepts = ChallengeAcceptsFromHeader(resp.Header)
		if len(v2Challenge.Accepts) > 0 {
			rawChallenge = ChallengeFromHeader(resp.Header)
		}
	}
	if len(v2Challenge.Accepts) == 0 {
		return false, false, nil, x402Quote{}, nil
	}
	accept := v2Challenge.Accepts[0]
	payTo, _ := accept["payTo"].(string)
	asset, _ := accept["asset"].(string)
	var feePayer string
	if extra, ok := accept["extra"].(map[string]any); ok {
		feePayer, _ = extra["feePayer"].(string)
	}
	// `amount` is the field name the CURRENT real-world dialect uses (Prism's
	// live endpoint, the official @x402/core v2.20 SDK, confirmed live
	// 2026-07-31) — `maxAmountRequired` is checked first only because it's
	// this codebase's own historical convention, not because it's more
	// correct. Both were separately confirmed to parse fine against the real
	// facilitator, so accepting either read-side (never emitted ourselves,
	// see relayInboundChallenge in x402relay.go) is pure compatibility, not
	// a protocol judgment call.
	amount, ok := ParseMaxAmountRequiredAsMicros(accept["maxAmountRequired"])
	if !ok {
		amount, ok = ParseMaxAmountRequiredAsMicros(accept["amount"])
	}
	// This is a real v2 challenge (accepts[] present) -- a missing or
	// unparseable amount here is a malformed challenge, not a genuinely free
	// tool. Silently returning MaxAmountRequired: 0 in that case would be
	// indistinguishable from a real zero-cost quote, and Task 5 sizes both a
	// credit reservation and a real on-chain payment off this value -- a
	// silent 0 there is a money-correctness bug. Report it as an error
	// instead; isV2 still reflects reality (it IS a v2 challenge) even
	// though the quote itself is zero-valued.
	if !ok || amount <= 0 {
		return true, false, nil, x402Quote{}, fmt.Errorf("x402: invalid or missing amount/maxAmountRequired in v2 challenge (maxAmountRequired=%v amount=%v)", accept["maxAmountRequired"], accept["amount"])
	}
	return true, false, nil, x402Quote{PayTo: payTo, Asset: asset, MaxAmountRequired: amount, FeePayer: feePayer, RawAccept: accept, RawChallenge: rawChallenge}, nil
}

// ParseMaxAmountRequiredAsMicros parses a real x402 v2 challenge's
// accepts[].maxAmountRequired field, accepting either its usual JSON-string
// encoding or a JSON-number encoding — some real targets encode this field
// as a number rather than a string, and a string-only type assertion would
// otherwise reject an entirely valid quote. Returns false if neither shape
// parses to a value, or the value isn't a whole number of micros.
func ParseMaxAmountRequiredAsMicros(v any) (int64, bool) {
	switch t := v.(type) {
	case string:
		n, err := strconv.ParseInt(t, 10, 64)
		// Same 1e15-micros ($1B/call) ceiling as the float64 branch below --
		// without it a target quoting a huge numeric string (e.g. close to
		// MaxInt64) parses "successfully" and downstream callers that add
		// models.X402PlatformFeeUSDMicros to this value (executeTool402RunLevel,
		// reserveAndFundRun's markup sizing) can overflow int64 into a negative
		// amount, which store.ReserveCredits then reads as a credit INCREASE.
		if err != nil || n < 0 || n > 1e15 {
			return 0, false
		}
		return n, true
	case float64:
		// Upper-bounded well below int64's range (1e15 micros = $1B/call,
		// already absurd for a real quote) and safely representable exactly
		// as a float64 (2^53 ≈ 9e15) -- without this, a target quoting e.g.
		// 1e20 as a JSON number converts via int64(t) to an
		// implementation-defined (in practice large-negative) result that
		// still returns ok=true, which every downstream ceiling check here
		// and in reserveAndFundRun assumes can't happen for a "valid" parse.
		if t != math.Trunc(t) || t < 0 || t > 1e15 {
			return 0, false
		}
		return int64(t), true
	default:
		return 0, false
	}
}

// ProbeX402Price is the exported form the run-level estimator (Task 5) uses
// when it only needs "is this v2, and what's the current price" — not the
// full quote. Always probes with an empty body: the 402 gate on a real
// x402 endpoint fires on method/payment-header alone, before any body
// validation, so a pure price probe never needs the real call's payload —
// only its method.
func ProbeX402Price(ctx context.Context, endpoint, method string) (isV2 bool, amountUSDMicros int64, err error) {
	isV2, _, _, quote, err := probeTool402Endpoint(ctx, endpoint, method, nil, "")
	return isV2, quote.MaxAmountRequired, err
}

// TargetQuote is a real target's parsed x402 v2 quote — payTo/asset for
// actually paying it (PayTargetFromWallet2, Task 3), MaxAmountRequired kept
// as the original string since that's the wire format PayTargetFromWallet2
// and the facilitator both expect. Defined here (not in Task 3) so Task 1
// compiles standalone, in task order, before Task 3 exists.
type TargetQuote struct {
	PayTo             string
	Asset             string
	MaxAmountRequired string
	// FeePayer mirrors x402Quote.FeePayer -- see that field's doc comment
	// for why its presence/absence (not a blanket default) selects which
	// signing scheme PayTargetFromWallet2 uses.
	FeePayer string
	// RawAccept is the target's own accepts[0] entry, verbatim, and
	// RawChallenge is its full challenge object, verbatim -- some targets
	// (confirmed live 2026-08-01: canix402-api.compx.io) require their own
	// challenge echoed back inside the paid retry rather than a fresh
	// minimal payload, so PayTargetFromWallet2 needs the exact original
	// objects, not a reconstruction from the parsed fields above (which
	// would drop fields specific to that target's own schema).
	RawAccept    map[string]any
	RawChallenge map[string]any
}

// ProbeX402Quote is the exported form the run-level per-call executor
// (Task 5) uses when it needs the full quote (payTo/asset too) to actually
// pay it via PayTargetFromWallet2 (Task 3). Same empty-body reasoning as
// ProbeX402Price.
func ProbeX402Quote(ctx context.Context, endpoint, method string) (isV2 bool, quote TargetQuote, err error) {
	isV2, _, _, q, err := probeTool402Endpoint(ctx, endpoint, method, nil, "")
	return isV2, TargetQuote{
		PayTo:             q.PayTo,
		Asset:             q.Asset,
		MaxAmountRequired: strconv.FormatInt(q.MaxAmountRequired, 10),
		FeePayer:          q.FeePayer,
		RawAccept:         q.RawAccept,
		RawChallenge:      q.RawChallenge,
	}, err
}

// ExecuteTool402V2 is the entry point runner.go calls for tool402 nodes. It
// inspects the target's 402 quote shape: a real x402 v2 challenge (accepts[])
// is routed through the AgentMesh relay so both payment legs are real,
// GoPlausible-settled, and attributable to us as an orchestrator entry, paid
// from the platform's own Wallet 1 spend wallet and gated/charged against
// the triggering user's credits for the real settled amount. The legacy
// flat-quote dialect (no accepts[]) bypasses the relay entirely and keeps
// today's direct-pay-from-the-agent's-own-wallet behavior, gated/charged at
// the fixed platform fee — it was never GoPlausible-compliant and isn't
// becoming so.
func ExecuteTool402V2(ctx context.Context, node models.WorkflowNode, rc RunContexter, aw models.AgentWallet, signer WalletSigner, relayCfg X402RelayConfig) (Tool402PaymentResult, error) {
	method := node.Method
	if method == "" {
		method = http.MethodGet
	}
	// Fill in the caller-configured values for whatever this target declared
	// it needs (ParamsFromChallenge -> the canvas's param fields -> here)
	// before the endpoint is probed or paid: a target that requires a param
	// rejects the PAID retry with a validation error otherwise, having
	// already taken the money.
	node, paramBody, paramContentType, err := buildTargetRequest(node, method)
	if err != nil {
		// Before any probe or payment: a body we cannot build is a
		// configuration error, and this node is one HTTP call away from
		// spending real money on a request the endpoint would reject.
		return Tool402PaymentResult{}, fmt.Errorf("x402: %w", err)
	}
	// A body can only be sent on a request that has one, so an endpoint
	// configured with a file (multipart) or a hand-written JSON body is
	// called with POST regardless of the dropdown — GET with a body is not a
	// valid request, and the body is the whole point of both modes.
	if len(paramBody) > 0 && method == http.MethodGet {
		method = http.MethodPost
	}
	// The body this node's real call carries when method isn't GET: the run's
	// input, or the configured params when there are any — those describe the
	// endpoint's own required input shape, which the run's free-form trigger
	// message does not. Same convention nodes.go's callHTTP uses for the plain
	// "tool" node type's http template; GET requests never carry a body (their
	// params went onto the query string in buildTargetRequest above).
	var payBody []byte
	if method != http.MethodGet {
		payBody = []byte(rc.Message())
		if paramBody != nil {
			payBody = paramBody
		}
	}
	// The probe carries that same body rather than nothing. It used to send
	// nil unconditionally, being "just" a check for a 402 challenge -- but
	// when the endpoint answers with anything other than 402, the probe IS
	// the node's only request and its response IS the node's result. A
	// body-reading endpoint then received an empty request and its complaint
	// about that was surfaced as the node's output, with every configured
	// param silently dropped (confirmed live 2026-08-02: a POST tool402 node
	// returned the target's own {"error":"Unexpected end of JSON input"} as a
	// successful step). Sending it costs nothing on a real 402 target, which
	// answers 402 before reading a body, and is exactly what the paid retry
	// below sends anyway.
	// node.TendrilLeaseToken is passed through so a lease-token-gated target
	// (Tendril's /x402/run) sees the same request here as it will from the
	// relay's own fetchTargetPriceQuote -- otherwise this probe alone can
	// get a non-402 auth-error response, which notPaymentRequired below
	// would then surface as a silently "successful" node result.
	isV2, notPaymentRequired, rawResponse, quote, err := probeTool402Endpoint(ctx, node.Endpoint, method, payBody, paramContentType, node.TendrilLeaseToken)
	if err != nil {
		return Tool402PaymentResult{}, err
	}
	if notPaymentRequired {
		return Tool402PaymentResult{Response: rawResponse}, nil
	}
	if isV2 {
		// toolIsRunFunded (not a blanket RunFundingID != "" check) decides
		// this: a run can be funded while a SPECIFIC tool's probe failed
		// during estimation and was never folded into that estimate, and
		// such a tool must still take the per-call path below rather than
		// draw against a pool sized for other tools. Nested inside isV2 so
		// a legacy-dialect call attached to the same agent as a v2 one
		// still falls through, unmodified, to the direct-pay branch below
		// regardless of the run's funding state.
		if relayCfg.toolIsRunFunded(node.ID) {
			targetQuote := TargetQuote{
				PayTo:             quote.PayTo,
				Asset:             quote.Asset,
				MaxAmountRequired: strconv.FormatInt(quote.MaxAmountRequired, 10),
				FeePayer:          quote.FeePayer,
				RawAccept:         quote.RawAccept,
				RawChallenge:      quote.RawChallenge,
			}
			return executeTool402RunLevel(ctx, node, relayCfg, targetQuote, quote.MaxAmountRequired, method, payBody)
		}
		return executeTool402V2Relay(ctx, node, relayCfg, PaymentLedger(relayCfg.PerCallLedger), method, payBody, paramContentType, node.TendrilLeaseToken)
	}

	// Legacy flat-quote dialect: unchanged direct-pay path, flat-fee billing,
	// paid from the agent's own wallet (not Wallet 1). If no wallet is
	// configured, ExecuteTool402 degrades gracefully without attempting a
	// payment at all — check that first so a reservation is never taken for
	// a call that can't possibly pay.
	if aw.EncryptedMnemonic == "" || signer == nil {
		result, err := ExecuteTool402(ctx, node, rc, aw, signer)
		if err != nil {
			return Tool402PaymentResult{}, err
		}
		return Tool402PaymentResult{Response: result}, nil
	}
	if reserve := relayCfg.LegacyLedger.Reserve; reserve != nil {
		if err := reserve(ctx, models.X402PlatformFeeUSDMicros); err != nil {
			return Tool402PaymentResult{}, &ErrBalanceBlocked{Err: err}
		}
	}
	// settled tracks whether the reservation above has already been
	// resolved (via Commit or Release) by the normal control flow below. If
	// a panic unwinds through ExecuteTool402 before that happens (any
	// runtime panic, not just an explicit error return), the balance would
	// otherwise stay permanently decremented with no debit_ledger row and
	// no way to reconcile it -- releasing here on the way out (before
	// re-panicking, so the original panic/stack trace still propagates and
	// this is not mistaken for a handled error) closes that window.
	settled := false
	defer func() {
		if !settled {
			if release := relayCfg.LegacyLedger.Release; release != nil {
				release(ctx, models.X402PlatformFeeUSDMicros)
			}
		}
	}()
	result, err := ExecuteTool402(ctx, node, rc, aw, signer)
	if err != nil {
		settled = true
		if release := relayCfg.LegacyLedger.Release; release != nil {
			release(ctx, models.X402PlatformFeeUSDMicros)
		}
		return Tool402PaymentResult{}, err
	}
	out := Tool402PaymentResult{Response: result}
	if m, ok := result.(map[string]any); ok {
		if _, hasTx := m["txId"]; hasTx {
			out.SettledUSDMicros = models.X402PlatformFeeUSDMicros
			out.DebitKind = models.DebitKindX402PlatformFee
			settled = true
			if commit := relayCfg.LegacyLedger.Commit; commit != nil {
				commit(ctx, node.ID, models.X402PlatformFeeUSDMicros, models.DebitKindX402PlatformFee)
			}
			return out, nil
		}
	}
	// No payment was actually sent (e.g. the retried request came back
	// without a txId) -- release the reservation, nothing to charge for.
	settled = true
	if release := relayCfg.LegacyLedger.Release; release != nil {
		release(ctx, models.X402PlatformFeeUSDMicros)
	}
	return out, nil
}

// setRelayTargetHeaders tells our own /x402/relay handler (x402relay.go)
// what HTTP method/body to use against target, out of band from the
// relay's own always-GET calling convention (?target=... never changes
// shape). A body goes in a header, base64-encoded, rather than the query
// string: request bodies (a full resume's text, for example) can easily
// exceed URLs' practical length limits, while headers comfortably hold
// far more (net/http's default MaxHeaderBytes is 1MB). Same base64-in-
// header pattern this file/x402relay.go already use for X-Payment and
// Payment-Required.
func setRelayTargetHeaders(req *http.Request, method string, body []byte, contentType, targetAuth string) {
	if method != "" && method != http.MethodGet {
		req.Header.Set("X-Relay-Method", method)
	}
	// Only small bodies still ride in the header, and only for compatibility
	// with a relay that predates newRelayRequest sending them as a real
	// request body -- see relayHeaderBodyLimit.
	if len(body) > 0 && len(body) <= relayHeaderBodyLimit {
		req.Header.Set("X-Relay-Body", base64.StdEncoding.EncodeToString(body))
	}
	// Without this the relay would send the body as application/json — fine
	// for a JSON body, unparseable for multipart, whose boundary token only
	// exists in this header.
	if contentType != "" {
		req.Header.Set("X-Relay-Content-Type", contentType)
	}
	// A bearer the TARGET requires (Tendril's lease token). Named X-Relay-Auth
	// rather than Authorization so it can never be confused with auth for the
	// relay itself, which is a different trust boundary entirely.
	if targetAuth != "" {
		req.Header.Set("X-Relay-Auth", targetAuth)
	}
}

// relayHeaderBodyLimit bounds what may travel in the X-Relay-Body header.
// That header was originally the ONLY way to give the relay a target body,
// justified by net/http's 1MB default MaxHeaderBytes -- but our own server's
// limit is not the binding one. Every proxy in front of it caps headers far
// lower (Cloudflare ~16KB, and the tunnel/edge in between drops the
// connection outright rather than answering), so a file param turned into a
// multipart body never arrived: a 138KB PDF became a ~184KB header, and the
// request died before the relay's handler ran at all (confirmed live
// 2026-08-03 against prism-99h2.onrender.com/resume-screen-accurate, which
// failed at ~2.5s with no payment attempted; bisected between 60KB working
// and 100KB failing).
//
// 8KB stays clear of the smallest proxy limit worth worrying about. Anything
// larger goes in the request body, where it belongs.
const relayHeaderBodyLimit = 8 << 10

// newRelayRequest builds the call to our own /x402/relay. A target body is
// sent as this request's own body (a plain POST) rather than smuggled
// through a header, because that is what bodies are for and what every
// intermediary is sized for. The relay reads its request body first and
// falls back to X-Relay-Body, so a small body still works against a relay
// that has not been redeployed yet.
//
// The relay route accepts any method (router.go registers it with Handle,
// not Get), and neither side treats the method used to reach the relay as
// the method for the target -- that has always been X-Relay-Method's job.
func newRelayRequest(ctx context.Context, relayURL, targetMethod string, targetBody []byte, targetContentType, targetAuth string) (*http.Request, error) {
	if len(targetBody) == 0 {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, relayURL, nil)
		if err != nil {
			return nil, err
		}
		setRelayTargetHeaders(req, targetMethod, nil, targetContentType, targetAuth)
		return req, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, relayURL, bytes.NewReader(targetBody))
	if err != nil {
		return nil, err
	}
	// Deliberately opaque: this body is bytes destined for the target as-is,
	// not something the relay should parse. The encoding the TARGET needs
	// travels separately in X-Relay-Content-Type.
	req.Header.Set("Content-Type", "application/octet-stream")
	setRelayTargetHeaders(req, targetMethod, targetBody, targetContentType, targetAuth)
	return req, nil
}

// targetMethod/targetBody describe the call the RELAY should make to
// node.Endpoint (our own /x402/relay is always reached via a plain GET
// itself — these two headers just tell the relay handler what to do with
// target on the relay's own end, same "method/body only matter for the
// downstream target, never for talking to the relay" split PayTargetFromWallet2
// and probeTool402Endpoint already follow).
func executeTool402V2Relay(ctx context.Context, node models.WorkflowNode, cfg X402RelayConfig, ledger PaymentLedger, targetMethod string, targetBody []byte, targetContentType, targetAuth string) (Tool402PaymentResult, error) {
	usdcSigner := cfg.USDCSigner
	platformSpendEncMnemonic := cfg.PlatformSpendEncMnemonic
	expectedAssetID := cfg.ExpectedAssetID
	relayBaseURL := cfg.RelayBaseURL
	if platformSpendEncMnemonic == "" || usdcSigner == nil {
		return Tool402PaymentResult{Response: map[string]any{"error": "payment required but no platform spend wallet configured"}}, nil
	}

	relayURL := relayBaseURL + "/x402/relay?target=" + url.QueryEscape(node.Endpoint)

	quoteReq, err := newRelayRequest(ctx, relayURL, targetMethod, targetBody, targetContentType, targetAuth)
	if err != nil {
		return Tool402PaymentResult{}, fmt.Errorf("x402 relay: building the quote request failed: %w", err)
	}
	quoteResp, err := relayHTTPClient.Do(quoteReq)
	if err != nil {
		return Tool402PaymentResult{}, fmt.Errorf("x402 relay quote failed: %w", err)
	}
	quoteBody, _ := io.ReadAll(io.LimitReader(quoteResp.Body, httpResponseLimit))
	quoteResp.Body.Close()

	var relayChallenge struct {
		Accepts []map[string]any `json:"accepts"`
		// Resource/Extensions are captured here so the paid retry below can
		// echo them back verbatim onto its own payment payload -- a real v2
		// client is expected to copy these straight from the challenge it
		// received, and without them here even a correctly-signed payment
		// has nothing for the facilitator's discovery extraction to catalog
		// against (see x402relay.go's resourceInfo/bazaarDiscoveryExtension
		// doc comments). The relay itself now also sets these fields
		// server-side regardless of what this payload carries, so this is
		// belt-and-suspenders rather than load-bearing for OUR OWN relay --
		// but it's what makes this call correct/spec-compliant client
		// behavior in general, not just against our own endpoint.
		Resource   map[string]any `json:"resource"`
		Extensions map[string]any `json:"extensions"`
	}
	// A target that answers the quote GET with anything other than a real 402
	// challenge (an expired auth token, a 5xx, a plain error body) used to
	// collapse into one generic "invalid challenge response" with no way to
	// tell those apart from a genuinely malformed challenge — a dead end for
	// diagnosing e.g. an expired Tendril lease token hitting /x402/run.
	// Surface what the target actually said instead: status plus a
	// truncated body.
	if json.Unmarshal(quoteBody, &relayChallenge) != nil || len(relayChallenge.Accepts) == 0 {
		snippet := string(quoteBody)
		if len(snippet) > 300 {
			snippet = snippet[:300]
		}
		if quoteResp.StatusCode != http.StatusPaymentRequired {
			return Tool402PaymentResult{}, fmt.Errorf(
				"x402 relay: target returned %s instead of a payment challenge: %s",
				quoteResp.Status, snippet)
		}
		return Tool402PaymentResult{}, fmt.Errorf(
			"x402 relay: target's 402 challenge has no accepts[]: %s", snippet)
	}
	accept := relayChallenge.Accepts[0]
	payTo, _ := accept["payTo"].(string)
	assetStr, _ := accept["asset"].(string)
	// Our own relay emits "amount" (matching GoPlausible's facilitator wire
	// format — see relayInboundChallenge), "maxAmountRequired" kept as a
	// fallback for the historical dialect.
	amountStr, ok := accept["amount"].(string)
	if !ok {
		amountStr, _ = accept["maxAmountRequired"].(string)
	}
	network, _ := accept["network"].(string)
	var feePayer string
	if extra, ok := accept["extra"].(map[string]any); ok {
		feePayer, _ = extra["feePayer"].(string)
	}
	assetID, err := strconv.ParseUint(assetStr, 10, 64)
	if err != nil {
		return Tool402PaymentResult{}, fmt.Errorf("x402 relay: invalid asset id %q", assetStr)
	}
	if assetID != expectedAssetID {
		return Tool402PaymentResult{}, fmt.Errorf("x402 relay: unexpected asset id %d, want %d", assetID, expectedAssetID)
	}
	amount, err := strconv.ParseUint(amountStr, 10, 64)
	if err != nil || amount == 0 || amount > uint64(models.MaxSingleX402QuoteUSDMicros) {
		return Tool402PaymentResult{}, fmt.Errorf("x402 relay: invalid settlement amount %q", amountStr)
	}

	// USDC's 6 decimals match credit_balance_usd_micros' scale exactly —
	// the relay's asset base-unit amount converts to USD micros 1:1.
	//
	// total is amount (the real vendor cost, what actually leaves Wallet 2)
	// plus the platform's flat markup -- see executeTool402RunLevel's
	// identical total/amount split for the run-funded path; both real x402
	// dispatch paths bill the same way.
	total := int64(amount) + models.X402PlatformFeeUSDMicros
	//
	// Reserve (atomically decrement) the exact amount now, before signing —
	// not just check it — so a second call racing this one (another
	// iteration of the same agent's tool loop, or a concurrent standalone
	// tool402 node) can't also pass a check against the same stale balance
	// and cause the platform to pay out more than the user can cover in
	// aggregate.
	if reserve := ledger.Reserve; reserve != nil {
		if err := reserve(ctx, total); err != nil {
			return Tool402PaymentResult{}, &ErrBalanceBlocked{Err: err}
		}
	}
	// settled tracks whether the reservation above has already been
	// resolved (via Commit or Release) by the normal control flow below —
	// see the identical pattern and rationale in ExecuteTool402V2's legacy
	// branch above. Covers a panic unwinding through the signing call, the
	// relay HTTP round trip, or response parsing before Commit/Release runs.
	settled := false
	defer func() {
		if !settled {
			if release := ledger.Release; release != nil {
				release(ctx, total)
			}
		}
	}()
	releaseReservation := func() {
		settled = true
		if release := ledger.Release; release != nil {
			release(ctx, total)
		}
	}

	group, idx, err := usdcSigner.SignUSDCPaymentGroup(ctx, platformSpendEncMnemonic, payTo, assetID, amount, feePayer)
	if err != nil {
		releaseReservation()
		return Tool402PaymentResult{}, fmt.Errorf("x402 relay payment signing failed: %w", err)
	}
	xPaymentFields := map[string]any{
		"x402Version": 2, "scheme": "exact", "network": network,
		"payload": map[string]any{"paymentGroup": group, "paymentIndex": idx},
	}
	if relayChallenge.Resource != nil {
		xPaymentFields["resource"] = relayChallenge.Resource
	}
	if relayChallenge.Extensions != nil {
		xPaymentFields["extensions"] = relayChallenge.Extensions
	}
	xPayment, _ := json.Marshal(xPaymentFields)

	payReq, err := newRelayRequest(ctx, relayURL, targetMethod, targetBody, targetContentType, targetAuth)
	if err != nil {
		releaseReservation()
		return Tool402PaymentResult{}, fmt.Errorf("x402 relay: building the payment request failed: %w", err)
	}
	payReq.Header.Set("X-Payment", string(xPayment))
	payResp, err := relayHTTPClient.Do(payReq)
	if err != nil {
		releaseReservation()
		return Tool402PaymentResult{}, fmt.Errorf("x402 relay payment request failed: %w", err)
	}
	defer payResp.Body.Close()
	finalBody, _ := io.ReadAll(io.LimitReader(payResp.Body, httpResponseLimit))

	out := Tool402PaymentResult{}
	var result any
	if json.Unmarshal(finalBody, &result) == nil {
		out.Response = result
	} else {
		out.Response = string(finalBody)
	}
	// Bill based on X-Inbound-Settled, not the relay's overall HTTP status.
	// The relay only sets this header once both (a) the inbound leg (Wallet
	// 1 -> Wallet 2) has irreversibly settled via the facilitator, and (b) a
	// real signed outbound payment group now exists as a submittable claim
	// (see x402relay.go's payTargetAndRespond) — so a signing failure on the
	// platform's side never bills the caller, but once a group is signed the
	// outbound leg to the caller-controlled target can still fail afterward
	// (the target errors, or rejects) with no refund path, and that must
	// still bill: gating on the final composite status instead would let a
	// malicious target accept payment and then deliberately return a
	// non-2xx response to avoid ever being billed, while still being paid.
	if payResp.Header.Get("X-Inbound-Settled") == "true" {
		out.SettledUSDMicros = int64(amount)
		out.DebitKind = models.DebitKindX402RelayCost
		out.PlatformFeeUSDMicros = models.X402PlatformFeeUSDMicros
		settled = true
		if commit := ledger.Commit; commit != nil {
			commit(ctx, node.ID, int64(amount), models.DebitKindX402RelayCost)
			commit(ctx, node.ID, models.X402PlatformFeeUSDMicros, models.DebitKindX402PlatformFee)
		}
		// Settle the platform's own flat markup as a second, real Wallet 1
		// -> Wallet 2 payment -- see SettlePlatformFee's doc comment for why
		// this can't just be folded into the vendor-cost leg above. Best
		// effort: the vendor has already been paid and the caller's credit
		// balance already reflects the full charge via the two Commit calls
		// above, so a failure here is a treasury reconciliation problem, not
		// a reason to fail a tool call that has, in every way that matters
		// to the caller, already succeeded. cfg.Facilitator == nil (dev/test
		// wiring that never configured one) makes this a silent no-op.
		//
		// Detached from ctx (context.WithoutCancel) -- unlike FundRunReserve,
		// where a StopWorkflow landing mid-retry just means ReleaseReservedCredits
		// undoes a reservation that was never committed, the two Commit calls
		// above have ALREADY moved this fee into the caller's committed ledger
		// total by the time we get here. A StopWorkflow racing this call must
		// not be allowed to abort the retry sequence early (selfSettleWallet1ToWallet2's
		// ctx.Err() check would do exactly that on a cancelable ctx): that would
		// leave the fee permanently billed in the ledger with no on-chain
		// settlement ever attempted again, not merely delayed. SelfSettleRetryBudget
		// still bounds how long this can run.
		//
		// Runs synchronously (not backgrounded) even though it's best-effort:
		// this blocks the agent's tool-calling loop for up to
		// SelfSettleRetryBudget on a degraded facilitator, which is a real
		// cost, but backgrounding it would mean either dropping
		// PlatformFeeTxID/PlatformFeeExplorerURL from the tool result
		// entirely (LogDrawer's receipt display and platformfee_test.go both
		// depend on it being there synchronously on success) or bolting on
		// a callback/polling path to fill it in later. Given the fee is
		// already committed either way, the ceiling here bounds the
		// downside to a fixed worst case rather than an unbounded hang, and
		// the CRITICAL alert below is the actual backstop for the failure
		// case -- worth revisiting if p95 tool-call latency under a
		// degraded facilitator becomes a real product problem.
		if cfg.Facilitator != nil {
			fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), SelfSettleRetryBudget)
			feeTxID, feeErr := SettlePlatformFee(fctx, RunPreFundConfig{
				USDCSigner:               usdcSigner,
				PlatformSpendEncMnemonic: platformSpendEncMnemonic,
				Facilitator:              cfg.Facilitator,
				PlatformWalletAddress:    cfg.PlatformWalletAddress,
				RelayNetwork:             cfg.RelayNetwork,
				RelayFeePayer:            cfg.RelayFeePayer,
				ExpectedAssetID:          expectedAssetID,
				FrontendURL:              cfg.FrontendURL,
			}, models.X402PlatformFeeUSDMicros)
			cancel()
			if feeErr != nil {
				msg := fmt.Sprintf("CRITICAL: x402 platform fee failed to settle on-chain (node %s, target %s, fee %d): %v",
					node.ID, node.Endpoint, models.X402PlatformFeeUSDMicros, feeErr)
				log.Print(msg)
				go alert.Notify(context.Background(), alert.ChannelPayments, msg)
			} else {
				out.PlatformFeeTxID = feeTxID
				out.PlatformFeeExplorerURL = ExplorerURLForAsset(expectedAssetID, feeTxID)
				if m, ok := out.Response.(map[string]any); ok {
					m["platformFeeTxId"] = feeTxID
					m["platformFeeExplorerURL"] = out.PlatformFeeExplorerURL
				}
			}
		}
		// Surfaces the real, already-settled inbound tx id in the node's own
		// output (rather than only the DB audit trail) so a run's console log
		// shows it -- same txId/explorerURL shape LogDrawer already renders
		// specially for the legacy direct-pay dialect (see ExecuteTool402).
		// Only when Response unmarshaled as a JSON object: a non-object
		// response (e.g. the target returned a bare string/array) has no
		// place to attach sibling fields without changing its type.
		if txID := payResp.Header.Get("X-Settlement-TxId"); txID == "" {
			// A paid call whose settlement id never reaches the caller is
			// unauditable from the UI, so say why rather than dropping it
			// silently: either the relay did not send the header, or the
			// target's response is not a JSON object with room to carry it.
			_, isObject := out.Response.(map[string]any)
			log.Printf("x402: no X-Settlement-TxId on relay response for %s (relay headers: %v, response is JSON object: %t)",
				node.Endpoint, payResp.Header, isObject)
		} else {
			out.TxID = txID
			out.ExplorerURL = ExplorerURLForAsset(expectedAssetID, txID)
			if m, ok := out.Response.(map[string]any); ok {
				m["txId"] = txID
				m["amount"] = formatUSDCAmount(int64(amount))
				m["explorerURL"] = out.ExplorerURL
			}
		}
		// The OUTBOUND leg's own settlement id (Wallet 2 -> target) --
		// separate from txId above, which is only ever the inbound leg
		// (caller -> Wallet 2). Together they show the full real payment
		// chain in a run's console log: caller -> Wallet 2 -> target. Not
		// every target returns one (Settled/StatusCode already say whether
		// the payment worked regardless), so this is additive/best-effort.
		if outboundTxID := payResp.Header.Get("X-Outbound-Settlement-TxId"); outboundTxID != "" {
			out.OutboundTxID = outboundTxID
			out.OutboundExplorerURL = ExplorerURLForAsset(expectedAssetID, outboundTxID)
			if m, ok := out.Response.(map[string]any); ok {
				m["outboundTxId"] = outboundTxID
				m["outboundExplorerURL"] = out.OutboundExplorerURL
			}
		}
		// Billing (above) and target delivery are separate concerns by
		// design -- but once billed, a non-2xx from target must still
		// surface as a failed node, or a run silently "succeeds" with the
		// caller charged and target's own raw 402/error body relayed back
		// as if it were real data (confirmed live 2026-08-01: a target that
		// rejected the signed outbound payment still produced a "success"
		// step with its un-paid challenge merged in as the response).
		// Deliberately scoped to this branch only -- when the INBOUND leg
		// itself is rejected (below, nothing billed), returning the relay's
		// error body as a graceful non-error result is correct as-is.
		if payResp.StatusCode < 200 || payResp.StatusCode >= 300 {
			const errSnippetLimit = 512
			snippet := finalBody
			if len(snippet) > errSnippetLimit {
				snippet = snippet[:errSnippetLimit]
			}
			return out, &ErrPaymentAlreadyCommitted{Err: fmt.Errorf("x402 relay: target rejected the paid request (status %d): %s", payResp.StatusCode, snippet)}
		}
	} else {
		releaseReservation()
	}
	return out, nil
}

// ExplorerURLForAsset picks the Lora explorer network segment from the
// USDC asset id the relay was configured to expect -- the same
// testnet/mainnet asset id split main.go already uses to choose
// usdcAssetID and relayNetwork together, so the two stay consistent without
// threading a separate network string through this call chain.
func ExplorerURLForAsset(assetID uint64, txID string) string {
	network := "testnet"
	if assetID == mainnetUSDCAssetID {
		network = "mainnet"
	}
	return "https://lora.algokit.io/" + network + "/transaction/" + txID
}

const mainnetUSDCAssetID = 31566704

// executeTool402RunLevel pays a real target directly from Wallet 2,
// in-process — no HTTP round trip to our own public relay, no fresh
// inbound settle (that already happened once, in bulk, via
// reserveAndFundRun before this agent's loop started). Reserve/Commit/
// Release still go through cfg.Ledger exactly like the per-call path;
// the only difference is what's behind those calls (an in-memory pool
// instead of a DB round trip per call).
func executeTool402RunLevel(ctx context.Context, node models.WorkflowNode, cfg X402RelayConfig, quote TargetQuote, amount int64, method string, body []byte) (Tool402PaymentResult, error) {
	// quote/amount come from ExecuteTool402V2's dispatch probe, taken
	// synchronously immediately before this call -- no time gap in which
	// price could legitimately drift, unlike the once-per-run estimate in
	// reserveAndFundRun (fetched potentially minutes before any specific
	// call fires) or executeTool402V2Relay's own separate "freshest quote
	// right before signing" refetch, which stays untouched since a real
	// time gap exists there (the agent's tool-calling loop runs between
	// that dispatch and its own pay attempt).

	// Every real x402 call -- v2 or legacy, run-funded or per-call -- bills
	// the platform's flat markup on top of the real vendor amount, not just
	// the vendor amount alone. amount and markup are reserved/committed
	// against two SEPARATE pools here, not summed into one: cfg.Ledger is
	// sized to `estimate` by reserveAndFundRun -- the ceiling on what may
	// be paid OUT to vendors from Wallet 2 -- so it correctly gates the
	// real PayTargetFromWallet2 call below at this run's own vendor-cost
	// budget. cfg.MarkupLedger is a second pool sized to markupTotal, a
	// pure credits-side accounting cap on top of the SAME on-chain
	// settlement (both pools are backed by reserveAndFundRun's single
	// creditReserve = estimate+markupTotal transfer, not two separate
	// ones). Reserving amount+markup from ONE pool sized estimate+markupTotal
	// would let a single call's real amount exceed `estimate` by borrowing
	// unused markup headroom from other funded-but-uncalled tools, causing
	// Wallet 2 to pay out real USDC this run never backed on-chain.
	markup := int64(models.X402PlatformFeeUSDMicros)

	// Nil-safe like every other ledger call site in this file, even though
	// this path is only ever reached with fully-populated cfg.Ledger/
	// cfg.MarkupLedger today (only from ExecuteTool402V2 once
	// toolIsRunFunded is true) -- so a future caller that forgets to wire
	// them up fails loudly instead of panicking on a nil func call.
	if reserve := cfg.Ledger.Reserve; reserve != nil {
		if err := reserve(ctx, amount); err != nil {
			return Tool402PaymentResult{}, &ErrBalanceBlocked{Err: err}
		}
	}
	if reserve := cfg.MarkupLedger.Reserve; reserve != nil {
		if err := reserve(ctx, markup); err != nil {
			if release := cfg.Ledger.Release; release != nil {
				release(ctx, amount)
			}
			return Tool402PaymentResult{}, &ErrBalanceBlocked{Err: err}
		}
	}

	result, payErr := PayTargetFromWallet2(ctx, cfg.Wallet2, node.Endpoint, method, body, quote)

	// Branch on result.Signed, not on payErr != nil -- Signed becomes true
	// the instant a real payment group is signed and submitted (see
	// Wallet2PayResult's doc comment in walletpay.go), meaning real money
	// has already left Wallet 2, and stays true regardless of what happens
	// afterward (the target unreachable at the network level, a non-2xx
	// target response, or a failure recording the audit row below).
	// Releasing the reservation for any of those would understate real
	// spend: a later call in the same agent turn could then Reserve
	// phantom headroom this pool doesn't actually have, and cleanup's
	// end-of-run release-unused-pool-to-DB would over-refund the user
	// relative to what was genuinely spent.
	if payErr != nil && !result.Signed {
		// Money never moved (asset mismatch, over-cap, or a failure signing
		// the payment group, all checked/attempted before any real payment
		// was sent) -- release both reservations, nothing was ever spent.
		if release := cfg.Ledger.Release; release != nil {
			release(ctx, amount)
		}
		if release := cfg.MarkupLedger.Release; release != nil {
			release(ctx, markup)
		}
		return Tool402PaymentResult{}, payErr
	}

	// result.Signed is true past this point: the reservation must be
	// Committed, never Released, no matter what happens next.
	if cfg.RecordSettlement != nil {
		if recordErr := cfg.RecordSettlement(ctx, node.Endpoint, amount, result.Settled); recordErr != nil {
			// A bookkeeping failure, not a payment failure -- real money
			// already left Wallet 2, so this is not reversible. Alert so an
			// operator can reconcile the missing audit row by hand, matching
			// the identical pattern reserveAndFundRun already uses when
			// RecordRunFunding fails after a successful FundRunReserve.
			msg := fmt.Sprintf("CRITICAL: run-level x402 payment settled (node %s, target %s, amount %d) but RecordSettlement failed: %v",
				node.ID, node.Endpoint, amount, recordErr)
			log.Print(msg)
			go alert.Notify(context.Background(), alert.ChannelPayments, msg)
		}
	} else {
		// No RecordSettlement configured at all -- a wiring gap, not a
		// payment failure. Money still moved and the ledger Commit below
		// still runs; this only means the audit row is never written.
		msg := fmt.Sprintf("CRITICAL: run-level x402 payment settled (node %s, target %s, amount %d) with no RecordSettlement configured -- audit row never written",
			node.ID, node.Endpoint, amount)
		log.Print(msg)
		go alert.Notify(context.Background(), alert.ChannelPayments, msg)
	}

	// Two separate debit_ledger rows for one call, each committed against
	// the pool it was reserved from above: the real vendor cost (what
	// Wallet 2 actually paid out, cfg.Ledger) and the platform's flat
	// markup on top of it (cfg.MarkupLedger) -- both already backed by the
	// SAME real on-chain transfer, the run's single up-front FundRunReserve
	// settlement (see reserveAndFundRun's creditReserve), not a second
	// transfer per call. Commit only ever writes the audit row, it never
	// touches either pool's remaining balance.
	if commit := cfg.Ledger.Commit; commit != nil {
		commit(ctx, node.ID, amount, models.DebitKindX402RelayCost)
	}
	if commit := cfg.MarkupLedger.Commit; commit != nil {
		commit(ctx, node.ID, markup, models.DebitKindX402PlatformFee)
	}

	if payErr != nil {
		// Target unreachable at the network level -- no response body to
		// return, nothing else to give the caller but the error. The ledger
		// above still reflects the real spend via Commit, not Release.
		return Tool402PaymentResult{}, &ErrPaymentAlreadyCommitted{Err: payErr}
	}

	var response any
	if err := json.Unmarshal(result.ResponseBody, &response); err != nil {
		response = string(result.ResponseBody)
	}

	// result.Settled (true only for a 2xx from target) is intentionally
	// separate from billing above -- money already left Wallet 2 the
	// instant it was signed, so Commit above is correct regardless. But
	// the node must still report failure when target rejected the paid
	// request, or a run silently "succeeds" while relaying target's own
	// un-paid 402/error body back as if it were real data -- the exact
	// bug this comment is fixing (confirmed live 2026-08-01, same failure
	// mode as executeTool402V2Relay above).
	if !result.Settled {
		const errSnippetLimit = 512
		snippet := result.ResponseBody
		if len(snippet) > errSnippetLimit {
			snippet = snippet[:errSnippetLimit]
		}
		return Tool402PaymentResult{Response: response, SettledUSDMicros: amount, DebitKind: models.DebitKindX402RelayCost, PlatformFeeUSDMicros: markup},
			&ErrPaymentAlreadyCommitted{Err: fmt.Errorf("x402 run-level: target rejected the paid request (status %d): %s", result.StatusCode, snippet)}
	}

	out := Tool402PaymentResult{Response: response, SettledUSDMicros: amount, DebitKind: models.DebitKindX402RelayCost, PlatformFeeUSDMicros: markup}

	// The INBOUND leg for a run-funded call is the run's single up-front
	// funding settlement (Wallet 1 -> Wallet 2), settled once in bulk by
	// reserveAndFundRun before this agent's loop started -- there is no
	// per-call inbound settlement on this path, and nothing else here can
	// ever produce one. Reporting it on every run-funded receipt is what
	// makes these calls auditable on-chain at all: before this, the console
	// showed a paid tool402 step carrying no tx id whatsoever, and the
	// usage page's settlements list stayed permanently empty for any agent-
	// attached x402 tool. The same id repeats across every call in the run
	// by design -- one real settlement funded all of them -- so consumers
	// that key on tx id (frontend lib/settlements.ts) must de-duplicate.
	out.TxID = cfg.RunFundingTxID
	if out.TxID != "" {
		out.ExplorerURL = ExplorerURLForAsset(cfg.ExpectedAssetID, out.TxID)
	}
	// The outbound leg's own settlement id (Wallet 2 -> target), when the
	// target returned one -- see the matching merge in
	// executeTool402V2Relay above for why this is surfaced in the node's
	// output rather than only the DB audit trail.
	if result.OutboundTxID != "" {
		out.OutboundTxID = result.OutboundTxID
		out.OutboundExplorerURL = ExplorerURLForAsset(cfg.ExpectedAssetID, result.OutboundTxID)
	}
	// Merged into the response body too (best-effort, only possible when
	// the target answered with a JSON object) so a STANDALONE tool402 node,
	// whose console row renders the raw response map rather than a payment
	// receipt, shows the same links -- see runner.go's NodeTypeTool402 case,
	// which returns paymentResult.Response and discards everything else.
	if m, ok := response.(map[string]any); ok {
		if out.TxID != "" {
			m["txId"] = out.TxID
			m["amount"] = formatUSDCAmount(amount)
			m["explorerURL"] = out.ExplorerURL
		}
		if out.OutboundTxID != "" {
			m["outboundTxId"] = out.OutboundTxID
			m["outboundExplorerURL"] = out.OutboundExplorerURL
		}
	}
	return out, nil
}

// formatUSDCAmount renders a USDC micro-unit amount as the decimal string
// the console displays (LogDrawer's OutputCell parseFloat's it directly).
// The legacy direct-pay dialect has always emitted this shape; the relay
// path used to emit raw micros here instead, which rendered a one-cent call
// as "10000.000000 paid".
func formatUSDCAmount(usdMicros int64) string {
	return fmt.Sprintf("%.6f", float64(usdMicros)/1e6)
}
