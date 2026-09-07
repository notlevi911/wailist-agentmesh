package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// CORS_ORIGIN accepts a list because two first-party clients now call this
// API from different origins: the web app on its own domain, and the native
// Android shell, whose WebView origin is https://localhost. These tests pin
// the part that is easy to get wrong -- credentials must be granted to a
// recognised origin and to nothing else, and the answer must vary per request.

const (
	webOrigin    = "https://www.agent-mesh.app"
	shellOrigin  = "https://localhost"
	otherOrigin  = "https://not-ours.example"
	corsEnvName  = "CORS_ORIGIN"
	okStatusBody = "ok"
)

func corsResponse(t *testing.T, envValue, requestOrigin string) *http.Response {
	t.Helper()
	t.Setenv(corsEnvName, envValue)

	h := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(okStatusBody))
	}))
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	if requestOrigin != "" {
		req.Header.Set("Origin", requestOrigin)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

// Both configured origins must work, and each must be echoed back as itself.
// A fixed string cannot do this: with two permitted origins there is no single
// correct value, and a credentialed response may not answer "*".
func TestListedOriginsAreEchoedWithCredentials(t *testing.T) {
	for _, origin := range []string{webOrigin, shellOrigin} {
		res := corsResponse(t, webOrigin+","+shellOrigin, origin)
		if got := res.Header.Get("Access-Control-Allow-Origin"); got != origin {
			t.Errorf("origin %q: allow-origin = %q, want the origin echoed back", origin, got)
		}
		if got := res.Header.Get("Access-Control-Allow-Credentials"); got != "true" {
			t.Errorf("origin %q: allow-credentials = %q, want true", origin, got)
		}
	}
}

// The whole point of an allow-list is that something is off it.
func TestUnlistedOriginGetsNoCredentials(t *testing.T) {
	res := corsResponse(t, webOrigin+","+shellOrigin, otherOrigin)
	if got := res.Header.Get("Access-Control-Allow-Origin"); got == otherOrigin {
		t.Error("an unlisted origin must never be echoed back")
	}
	if got := res.Header.Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("allow-credentials = %q, want empty for an unlisted origin", got)
	}
}

// The deployment that exists today sets exactly one origin. It must keep
// behaving identically -- this change must not be able to sign the web app out.
func TestSingleOriginBehavesAsBefore(t *testing.T) {
	res := corsResponse(t, webOrigin, webOrigin)
	if got := res.Header.Get("Access-Control-Allow-Origin"); got != webOrigin {
		t.Errorf("allow-origin = %q, want %q", got, webOrigin)
	}
	if got := res.Header.Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("allow-credentials = %q, want true", got)
	}
}

// Before multi-origin support, this middleware never looked at the request's
// actual Origin at all -- it answered a single configured value unconditionally.
// A request with no Origin header (same-origin, a non-browser caller, or one
// stripped somewhere in transit) must keep getting that same single origin
// with credentials, not silently lose them just because there is nothing to
// compare against.
func TestMissingOriginFallsBackToTheSingleConfiguredOrigin(t *testing.T) {
	res := corsResponse(t, webOrigin, "")
	if got := res.Header.Get("Access-Control-Allow-Origin"); got != webOrigin {
		t.Errorf("allow-origin = %q, want %q", got, webOrigin)
	}
	if got := res.Header.Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("allow-credentials = %q, want true", got)
	}
}

// With more than one origin configured there is no single safe default to
// fall back to for a missing Origin -- granting credentials to whichever one
// happens to be first would be a guess, not a verified match.
func TestMissingOriginGetsNoCredentialsWithMultipleConfigured(t *testing.T) {
	res := corsResponse(t, webOrigin+","+shellOrigin, "")
	if got := res.Header.Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("allow-credentials = %q, want empty when the origin can't be verified", got)
	}
}

// An unset variable keeps the old permissive, credential-free behaviour.
func TestUnsetOriginFallsBackToWildcard(t *testing.T) {
	res := corsResponse(t, "", webOrigin)
	if got := res.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("allow-origin = %q, want *", got)
	}
	if got := res.Header.Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("allow-credentials = %q, want empty: a wildcard may not carry credentials", got)
	}
}

// Whitespace and trailing slashes are what a human actually types into a
// dashboard env var, and neither should silently break the match.
func TestOriginListToleratesSpacingAndTrailingSlashes(t *testing.T) {
	res := corsResponse(t, "  "+webOrigin+"/ ,  "+shellOrigin+"/  ", shellOrigin)
	if got := res.Header.Get("Access-Control-Allow-Origin"); got != shellOrigin {
		t.Errorf("allow-origin = %q, want %q", got, shellOrigin)
	}
}

// Without Vary, a shared cache can serve the web app's CORS headers to the
// native shell -- an intermittent failure with no pattern to it.
func TestResponseVariesByOrigin(t *testing.T) {
	res := corsResponse(t, webOrigin+","+shellOrigin, webOrigin)
	if got := res.Header.Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want Origin", got)
	}
}

// readonly.go's EDITOR_CLIENT_KEY bypass reads X-AgentMesh-Editor-Key off
// incoming requests; if it is missing from the CORS allow-list, a browser's
// preflight rejects the header before the server ever sees it, silently
// defeating the bypass for any browser-based caller.
func TestAllowedHeadersIncludesTheEditorKey(t *testing.T) {
	res := corsResponse(t, webOrigin, webOrigin)
	if got := res.Header.Get("Access-Control-Allow-Headers"); !strings.Contains(got, editorKeyHeaderKey) {
		t.Errorf("allow-headers = %q, want it to include %q", got, editorKeyHeaderKey)
	}
}

// Preflight must short-circuit and still carry the headers.
func TestPreflightAnswersWithoutReachingTheHandler(t *testing.T) {
	t.Setenv(corsEnvName, webOrigin+","+shellOrigin)
	reached := false
	h := corsMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))

	req := httptest.NewRequest(http.MethodOptions, "/health", nil)
	req.Header.Set("Origin", shellOrigin)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if reached {
		t.Error("preflight should not reach the wrapped handler")
	}
	if rec.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != shellOrigin {
		t.Errorf("preflight allow-origin = %q, want %q", got, shellOrigin)
	}
}
