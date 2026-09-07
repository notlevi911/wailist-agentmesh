package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The web app must keep getting exactly what it got before: a cookie and an
// empty body. Handing a browser its own JWT in readable form would be a
// straight downgrade, since page JavaScript (and therefore any XSS) could
// read it. Only a client that cannot use cookies asks for the token.

func TestWebCallerGetsNoToken(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/auth/signin", nil)
	if wantsBearerToken(req) {
		t.Fatal("a request with no client header must not be treated as native")
	}
	if _, ok := authPayload(req, "jwt-value")["token"]; ok {
		t.Fatal("the web response body must not carry the token")
	}
}

func TestNativeCallerGetsTheToken(t *testing.T) {
	for _, client := range []string{"android", "Android", "  ANDROID  ", "ios"} {
		req := httptest.NewRequest(http.MethodPost, "/auth/signin", nil)
		req.Header.Set(NativeClientHeader, client)

		if !wantsBearerToken(req) {
			t.Fatalf("client %q should be treated as native", client)
		}
		got, ok := authPayload(req, "jwt-value")["token"]
		if !ok || got != "jwt-value" {
			t.Fatalf("client %q should receive the token, got %v", client, got)
		}
	}
}

// An unrecognised value must fall back to the safe (web) behaviour rather
// than being treated as native, so a typo cannot silently start leaking the
// token into a browser-readable body.
func TestUnknownClientFallsBackToWebBehaviour(t *testing.T) {
	for _, client := range []string{"", "web", "andriod", "curl", "browser"} {
		req := httptest.NewRequest(http.MethodPost, "/auth/signin", nil)
		req.Header.Set(NativeClientHeader, client)
		if wantsBearerToken(req) {
			t.Fatalf("client %q must not be treated as native", client)
		}
	}
}
