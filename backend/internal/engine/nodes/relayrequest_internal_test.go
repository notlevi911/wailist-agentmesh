package nodes

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRelayRequestSendsLargeBodyAsBody pins the fix for a file param never
// reaching the target. The body destined for the target used to travel
// base64-encoded in the X-Relay-Body header, justified by net/http's 1MB
// MaxHeaderBytes -- but our own server's limit is not the binding one. Every
// proxy in front of it caps headers far lower, so a 138KB PDF (a ~184KB
// header) had its connection dropped upstream before the relay handler ran:
// confirmed live 2026-08-03 against prism-99h2.onrender.com, which failed at
// ~2.5s with no payment attempted.
func TestRelayRequestSendsLargeBodyAsBody(t *testing.T) {
	var gotBodyLen int
	var gotHeaderBody, gotMethod, gotRelayMethod, gotContentType string
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBodyLen = len(b)
		gotHeaderBody = r.Header.Get("X-Relay-Body")
		gotMethod = r.Method
		gotRelayMethod = r.Header.Get("X-Relay-Method")
		gotContentType = r.Header.Get("X-Relay-Content-Type")
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"accepts":[]}`))
	}))
	defer relay.Close()

	// Larger than any real proxy's per-header limit, and larger than the
	// multipart body a single 2 MiB file param produces.
	large := make([]byte, 184*1024)
	req, err := newRelayRequest(context.Background(), relay.URL+"/x402/relay", http.MethodPost, large, "multipart/form-data; boundary=abc123", "")
	if err != nil {
		t.Fatalf("building the relay request failed: %v", err)
	}
	resp, err := relay.Client().Do(req)
	if err != nil {
		t.Fatalf("relay request failed: %v", err)
	}
	resp.Body.Close()

	if gotBodyLen != len(large) {
		t.Fatalf("want the %d-byte target body delivered as the request body, got %d bytes", len(large), gotBodyLen)
	}
	if gotHeaderBody != "" {
		t.Fatalf("want no X-Relay-Body header for a body this size -- that is exactly what the proxies drop (got %d header bytes)", len(gotHeaderBody))
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("want the relay reached with POST when there is a body to send, got %s", gotMethod)
	}
	// The method and encoding the TARGET needs travel separately: how we
	// reach the relay has never meant anything to the target.
	if gotRelayMethod != http.MethodPost {
		t.Fatalf("want X-Relay-Method=POST passed through, got %q", gotRelayMethod)
	}
	if gotContentType != "multipart/form-data; boundary=abc123" {
		t.Fatalf("want the multipart content type (boundary included) passed through, got %q", gotContentType)
	}
}

// TestRelayRequestKeepsHeaderForSmallBody confirms a small body still rides
// in X-Relay-Body as well, so a relay that has not been redeployed yet -- one
// that only knows how to read the header -- keeps working for the bodies it
// could actually deliver.
func TestRelayRequestKeepsHeaderForSmallBody(t *testing.T) {
	body := []byte(`{"url":"https://example.com"}`)

	req, err := newRelayRequest(context.Background(), "https://relay.example/x402/relay", http.MethodPost, body, "application/json", "")
	if err != nil {
		t.Fatalf("building the relay request failed: %v", err)
	}

	decoded, err := base64.StdEncoding.DecodeString(req.Header.Get("X-Relay-Body"))
	if err != nil {
		t.Fatalf("want a base64 X-Relay-Body for a small body, got error: %v", err)
	}
	if string(decoded) != string(body) {
		t.Fatalf("want the header to carry the same bytes, got %q", decoded)
	}
	sent, _ := io.ReadAll(req.Body)
	if string(sent) != string(body) {
		t.Fatalf("want the request body to carry them too, got %q", sent)
	}
}

// TestRelayRequestStaysGETWithoutBody confirms the no-body case is unchanged:
// every existing bodyless tool402 call keeps reaching the relay exactly as
// it did before.
func TestRelayRequestStaysGETWithoutBody(t *testing.T) {
	req, err := newRelayRequest(context.Background(), "https://relay.example/x402/relay", "", nil, "", "")
	if err != nil {
		t.Fatalf("building the relay request failed: %v", err)
	}
	if req.Method != http.MethodGet {
		t.Fatalf("want GET when there is no body, got %s", req.Method)
	}
	if req.Body != nil {
		t.Fatal("want no request body when there is nothing to send")
	}
	for _, h := range []string{"X-Relay-Body", "X-Relay-Method", "X-Relay-Content-Type"} {
		if req.Header.Get(h) != "" {
			t.Fatalf("want %s unset for a plain bodyless GET, got %q", h, req.Header.Get(h))
		}
	}
}
