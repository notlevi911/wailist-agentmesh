package handlers

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/prism"
)

func fileField(b64, name string) prismRunField {
	return prismRunField{Kind: "file", Value: b64, FileName: name, MIMEType: "application/pdf"}
}

// TestBuildPrismNodeRejectsAnUnknownEndpoint is the guard that keeps
// /prism/run from being an open x402 proxy. The URL called always comes from
// the endpoint table; a caller who could name an arbitrary target could spend
// their own credit against any host on the internet through our platform
// wallet — and collect our $1.50 fee's worth of relay work while doing it.
func TestBuildPrismNodeRejectsAnUnknownEndpoint(t *testing.T) {
	for _, id := range []string{
		"",
		"code-review",
		"https://evil.example.com/free-money",
		"../code-review-fast",
		"CODE-REVIEW-FAST",
	} {
		_, err := buildPrismNode(prismRunRequest{Endpoint: id})
		if err == nil {
			t.Errorf("endpoint %q was accepted; only ids from prism.Endpoints() may resolve", id)
		}
	}
}

// TestBuildPrismNodeRejectsAMissingRequiredFieldBeforePaying is the money
// test. Every validation failure has to be detectable without making a
// request, because the alternative is the user paying $1.75 (0.25 vendor +
// 1.50 platform fee) to be told their body was malformed. buildPrismNode is
// deliberately pure so this is checkable at all.
func TestBuildPrismNodeRejectsAMissingRequiredFieldBeforePaying(t *testing.T) {
	for _, e := range prism.Endpoints() {
		for _, f := range e.Fields {
			if !f.Required {
				continue
			}
			// Every required field present except this one.
			fields := map[string]prismRunField{}
			for _, other := range e.Fields {
				if other.Name == f.Name {
					continue
				}
				if other.Kind == prism.FieldFile {
					fields[other.Name] = fileField(base64.StdEncoding.EncodeToString([]byte("x")), "a.pdf")
				} else {
					fields[other.Name] = prismRunField{Kind: "text", Value: "something"}
				}
			}
			_, err := buildPrismNode(prismRunRequest{Endpoint: e.ID, Fields: fields})
			if err == nil {
				t.Errorf("%s: missing required field %q was accepted", e.ID, f.Name)
				continue
			}
			// Case-insensitive: the message reads as a sentence ("Fill in job
			// description before running."), so the label appears lowercased
			// mid-sentence. What matters is that the user is told WHICH field.
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(f.Label)) {
				t.Errorf("%s: error %q should name the field (%q) so the user knows what to fill in", e.ID, err, f.Label)
			}
		}
	}
}

// A whitespace-only value is empty as far as the endpoint is concerned, and
// paying to send one is the same wasted charge as sending none.
func TestBuildPrismNodeTreatsWhitespaceAsEmpty(t *testing.T) {
	_, err := buildPrismNode(prismRunRequest{
		Endpoint: "code-review-fast",
		Fields: map[string]prismRunField{
			"raw_url":   {Kind: "text", Value: "   \t\n  "},
			"file_path": {Kind: "text", Value: "src/index.ts"},
		},
	})
	if err == nil {
		t.Fatal("a whitespace-only required field was accepted")
	}
}

func TestBuildPrismNodeRejectsUnreadableAndOversizedFiles(t *testing.T) {
	base := map[string]prismRunField{
		"task_description": {Kind: "text", Value: "Senior React developer"},
	}

	t.Run("not base64", func(t *testing.T) {
		fields := map[string]prismRunField{"resume": fileField("!!!not base64!!!", "cv.pdf")}
		for k, v := range base {
			fields[k] = v
		}
		if _, err := buildPrismNode(prismRunRequest{Endpoint: "resume-screen-accurate", Fields: fields}); err == nil {
			t.Fatal("unreadable file content was accepted")
		}
	})

	t.Run("over the size cap", func(t *testing.T) {
		big := base64.StdEncoding.EncodeToString(make([]byte, maxPrismFileBytes+1))
		fields := map[string]prismRunField{"resume": fileField(big, "cv.pdf")}
		for k, v := range base {
			fields[k] = v
		}
		_, err := buildPrismNode(prismRunRequest{Endpoint: "resume-screen-accurate", Fields: fields})
		if err == nil {
			t.Fatal("an oversized file was accepted; the client-side cap is not a control")
		}
		if !strings.Contains(err.Error(), "limit") {
			t.Errorf("error %q should tell the user what the limit is", err)
		}
	})

	t.Run("missing filename", func(t *testing.T) {
		f := fileField(base64.StdEncoding.EncodeToString([]byte("%PDF-1.5")), "")
		fields := map[string]prismRunField{"resume": f}
		for k, v := range base {
			fields[k] = v
		}
		// {{fileName:resume}} would expand to "" and Prism would receive a
		// nameless file — better to refuse than to pay to find out.
		if _, err := buildPrismNode(prismRunRequest{Endpoint: "resume-screen-accurate", Fields: fields}); err == nil {
			t.Fatal("a file with no filename was accepted")
		}
	})
}

// TestBuildPrismNodeProducesTheDocumentedResumeShape is the end-to-end check
// that the console's form actually yields Prism's real body. It asserts the
// node's configuration; engine/nodes.TestBuildTargetRequestJSONBodyModeProducesPrismShape
// asserts that this exact configuration expands to the documented JSON.
func TestBuildPrismNodeProducesTheDocumentedResumeShape(t *testing.T) {
	pdf := base64.StdEncoding.EncodeToString([]byte("%PDF-1.5 fake"))
	node, err := buildPrismNode(prismRunRequest{
		Endpoint: "resume-screen-accurate",
		Fields: map[string]prismRunField{
			"task_description": {Kind: "text", Value: "Senior React Developer"},
			"resume":           fileField(pdf, "john_doe.pdf"),
		},
	})
	if err != nil {
		t.Fatalf("want a node, got %v", err)
	}
	if node.Type != models.NodeTypeTool402 {
		t.Errorf("node type = %q", node.Type)
	}
	if node.Endpoint != "https://prism-99h2.onrender.com/resume-screen-accurate" {
		t.Errorf("endpoint = %q — it must come from the table, not the request", node.Endpoint)
	}
	if node.BodyMode != models.BodyModeJSON {
		t.Errorf("bodyMode = %q, want json — a file cannot travel as a query param", node.BodyMode)
	}
	if !strings.Contains(node.BodyTemplate, `"files"`) {
		t.Errorf("body template lost Prism's files array: %s", node.BodyTemplate)
	}

	var text, file *models.CustomParam
	for i := range node.CustomParams {
		switch node.CustomParams[i].Name {
		case "task_description":
			text = &node.CustomParams[i]
		case "resume":
			file = &node.CustomParams[i]
		}
	}
	if text == nil || text.Kind != "text" || text.Value != "Senior React Developer" {
		t.Errorf("task_description param = %+v", text)
	}
	if file == nil || file.Kind != "file" {
		t.Fatalf("resume param = %+v", file)
	}
	if file.Value != pdf {
		t.Error("the file's base64 bytes must reach the node unmodified")
	}
	if file.FileName != "john_doe.pdf" {
		t.Errorf("fileName = %q, want the uploaded file's own name", file.FileName)
	}
}

// A query-mode endpoint must NOT be given a body template: buildTargetRequest
// switches a request with a body to POST, so setting one here would silently
// change how a documented GET endpoint is called.
func TestBuildPrismNodeLeavesQueryEndpointsInQueryMode(t *testing.T) {
	node, err := buildPrismNode(prismRunRequest{
		Endpoint: "code-review-accurate",
		Fields: map[string]prismRunField{
			"raw_url":   {Kind: "text", Value: "https://raw.githubusercontent.com/o/r/main/a.ts"},
			"file_path": {Kind: "text", Value: "a.ts"},
		},
	})
	if err != nil {
		t.Fatalf("want a node, got %v", err)
	}
	if node.BodyMode != "" || node.BodyTemplate != "" {
		t.Errorf("query-mode endpoint got a body: mode=%q template=%q", node.BodyMode, node.BodyTemplate)
	}
	if node.Method != "GET" {
		t.Errorf("method = %q, want GET", node.Method)
	}
	if len(node.CustomParams) != 2 {
		t.Errorf("want both fields as query params, got %d", len(node.CustomParams))
	}
}

// TestEveryPrismEndpointCanBuildANode is a smoke test over the whole table: a
// new endpoint added to internal/prism must be constructible here, or the
// console will offer a form that cannot be submitted.
func TestEveryPrismEndpointCanBuildANode(t *testing.T) {
	for _, e := range prism.Endpoints() {
		fields := map[string]prismRunField{}
		for _, f := range e.Fields {
			if f.Kind == prism.FieldFile {
				fields[f.Name] = fileField(base64.StdEncoding.EncodeToString([]byte("data")), "f.pdf")
			} else {
				fields[f.Name] = prismRunField{Kind: "text", Value: "value"}
			}
		}
		node, err := buildPrismNode(prismRunRequest{Endpoint: e.ID, Fields: fields})
		if err != nil {
			t.Errorf("%s: %v", e.ID, err)
			continue
		}
		if node.Endpoint != e.URL() {
			t.Errorf("%s: endpoint = %q, want %q", e.ID, node.Endpoint, e.URL())
		}
		if len(node.CustomParams) != len(e.Fields) {
			t.Errorf("%s: %d params for %d fields — a field is being dropped", e.ID, len(node.CustomParams), len(e.Fields))
		}
	}
}

// TestPrismEndpointsResponseHidesTheBodyTemplate: the console renders a form
// and posts values. The body shape that gets paid for is decided server-side,
// and is not something a client should see or be able to echo back.
func TestPrismEndpointsResponseHidesTheBodyTemplate(t *testing.T) {
	blob, err := json.Marshal(prism.Endpoints())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), "content_base64") || strings.Contains(string(blob), "{{file:") {
		t.Errorf("the serialized endpoint list leaks its body template: %s", blob)
	}
}

// TestPrismFeeIsTheSharedPlatformConstant pins the pricing the console
// advertises to the one the relay actually charges. These must be the same
// number, read from the same place: quoting a fee the ledger does not match is
// a billing dispute waiting to happen.
func TestPrismFeeIsTheSharedPlatformConstant(t *testing.T) {
	if models.X402PlatformFeeUSDMicros != 1_500_000 {
		t.Fatalf("platform fee = %d micros; the Prism console's cost display assumes $1.50 and must be revisited", models.X402PlatformFeeUSDMicros)
	}
	// Prism's vendor prices are far below the flat fee, so the fee dominates
	// every total. If that stops being true the console's "0.25 + 1.50" copy
	// is still correct, but the emphasis in the UI is worth another look.
	for _, e := range prism.Endpoints() {
		if e.AmountMicros >= models.X402PlatformFeeUSDMicros {
			t.Logf("note: %s (%d) now costs at least as much as the platform fee", e.ID, e.AmountMicros)
		}
	}
}

// TestRelayUnpayableSeparatesMisconfigurationFromAQuietTarget covers a review
// finding: executeTool402V2Relay signals "this server has no spend wallet" by
// returning a response body with a NIL error, which is indistinguishable from
// a successful call unless the body is inspected. Without this split the
// console reported a broken deployment to the user as "Prism answered without
// asking for payment, so this run was free" — blaming the vendor for our own
// misconfiguration.
//
// The sentinel is matched against nodes.ErrMsgNoPlatformSpendWallet, the same
// constant executeTool402V2Relay builds its body from, so a reword there can
// never leave this test passing while the real matcher silently stops firing.
func TestRelayUnpayableSeparatesMisconfigurationFromAQuietTarget(t *testing.T) {
	cannotPay := map[string]any{"error": nodes.ErrMsgNoPlatformSpendWallet}
	if !relayUnpayable(cannotPay) {
		t.Error("the relay's own cannot-pay sentinel must be recognised")
	}

	// A target's real answer must never be mistaken for our misconfiguration,
	// including one that happens to carry an unrelated `error` key.
	for _, notOurs := range []any{
		map[string]any{"candidates": []any{}},
		map[string]any{"error": "No files provided"},
		map[string]any{"error": "rate limited"},
		"a plain string body",
		nil,
		[]any{1, 2, 3},
	} {
		if relayUnpayable(notOurs) {
			t.Errorf("%v was treated as a payment misconfiguration; it is the target's own response", notOurs)
		}
	}
}
