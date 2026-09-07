package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBlocksWrite(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		// The five endpoints that change what a workflow is.
		{"create", http.MethodPost, "/workflows", true},
		{"update", http.MethodPut, "/workflows/wf_123", true},
		{"delete", http.MethodDelete, "/workflows/wf_123", true},
		{"deploy", http.MethodPost, "/workflows/wf_123/deploy", true},
		{"build", http.MethodPost, "/workflows/wf_123/build", true},

		// Schedule and geofence configuration change what triggers a workflow,
		// same category of authoring as the five above. Must mirror WRITE_RULES
		// in frontend/src/lib/readonly.ts one for one.
		{"set schedule", http.MethodPut, "/workflows/wf_123/schedule", true},
		{"clear schedule", http.MethodDelete, "/workflows/wf_123/schedule", true},
		{"set geofence", http.MethodPut, "/workflows/wf_123/geofence", true},
		{"clear geofence", http.MethodDelete, "/workflows/wf_123/geofence", true},
		// GET, but find-or-creates a workflow row server-side (see the comment
		// on readOnlyBlocked), so it belongs on this list despite the verb.
		{"tendril console", http.MethodGet, "/tendril/console", true},
		// The read-only counterpart never creates anything, so it stays open.
		{"tendril console exists", http.MethodGet, "/tendril/console/exists", false},
		// The Prism console's pair behaves identically, for the same reasons.
		{"prism console", http.MethodGet, "/prism/console", true},
		{"prism console exists", http.MethodGet, "/prism/console/exists", false},
		// Running a paid Prism call is operating, not authoring -- the same
		// line /tendril/run and /tendril/topup already sit on. A viewer who
		// can spend their own credit on a Tendril job can spend it here too.
		{"prism run", http.MethodPost, "/prism/run", false},
		{"prism endpoints", http.MethodGet, "/prism/endpoints", false},

		// Operating a workflow somebody else built stays available -- this is
		// the line the whole feature draws.
		{"run", http.MethodPost, "/workflows/wf_123/run", false},
		{"stop", http.MethodPost, "/workflows/wf_123/stop", false},
		{"list", http.MethodGet, "/workflows", false},
		{"get", http.MethodGet, "/workflows/wf_123", false},

		// Account and billing are not workflow authoring.
		{"settings", http.MethodPatch, "/settings", false},
		{"password", http.MethodPost, "/auth/password", false},
		{"coupon", http.MethodPost, "/credits/redeem-coupon", false},
		{"topup", http.MethodPost, "/tendril/topup", false},

		// A trailing slash routes to the same handler, so it must not be a
		// way around the rules.
		{"create trailing slash", http.MethodPost, "/workflows/", true},
		{"update trailing slash", http.MethodPut, "/workflows/wf_1/", true},

		// The id is exactly one path segment; anything deeper is a different
		// endpoint and must not be swept up.
		{"agent balance", http.MethodGet, "/workflows/wf_1/agents/a_1/balance", false},
		{"fund agent", http.MethodPost, "/workflows/wf_1/agents/a_1/fund", false},

		// A blocked path under a different verb is a different endpoint.
		{"get deploy", http.MethodGet, "/workflows/wf_1/deploy", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := blocksWrite(tc.method, tc.path); got != tc.want {
				t.Fatalf("blocksWrite(%q, %q) = %v, want %v", tc.method, tc.path, got, tc.want)
			}
		})
	}
}

// ok is the handler the middleware wraps; a 200 means the request got through.
func ok(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

func serve(t *testing.T, mw func(http.Handler) http.Handler, method, path, key string) int {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if key != "" {
		req.Header.Set(editorKeyHeaderKey, key)
	}
	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(ok)).ServeHTTP(rec, req)
	return rec.Code
}

func TestReadOnlyMiddlewareDisabledByDefault(t *testing.T) {
	// No WEB_READONLY_MODE set: an existing deployment must keep working
	// exactly as it did.
	mw := NewReadOnlyMiddleware()
	if got := serve(t, mw, http.MethodPost, "/workflows", ""); got != http.StatusOK {
		t.Fatalf("create with read-only off = %d, want %d", got, http.StatusOK)
	}
}

func TestReadOnlyMiddlewareBlocksWrites(t *testing.T) {
	t.Setenv(readOnlyEnvVar, "1")
	mw := NewReadOnlyMiddleware()

	if got := serve(t, mw, http.MethodPost, "/workflows", ""); got != http.StatusForbidden {
		t.Fatalf("create = %d, want %d", got, http.StatusForbidden)
	}
	if got := serve(t, mw, http.MethodPost, "/workflows/wf_1/run", ""); got != http.StatusOK {
		t.Fatalf("run = %d, want %d", got, http.StatusOK)
	}
	if got := serve(t, mw, http.MethodGet, "/workflows", ""); got != http.StatusOK {
		t.Fatalf("list = %d, want %d", got, http.StatusOK)
	}
}

func TestReadOnlyMiddlewareEditorKey(t *testing.T) {
	t.Setenv(readOnlyEnvVar, "true")
	t.Setenv(editorKeyEnvVar, "s3cret")
	mw := NewReadOnlyMiddleware()

	if got := serve(t, mw, http.MethodPost, "/workflows", "s3cret"); got != http.StatusOK {
		t.Fatalf("create with editor key = %d, want %d", got, http.StatusOK)
	}
	if got := serve(t, mw, http.MethodPost, "/workflows", "wrong"); got != http.StatusForbidden {
		t.Fatalf("create with wrong key = %d, want %d", got, http.StatusForbidden)
	}
	if got := serve(t, mw, http.MethodPost, "/workflows", ""); got != http.StatusForbidden {
		t.Fatalf("create with no key = %d, want %d", got, http.StatusForbidden)
	}
}

// An unset EDITOR_CLIENT_KEY must not mean "every client is an editor" -- a
// missing header would otherwise compare equal to a missing key and let the
// whole blocklist through.
func TestReadOnlyMiddlewareEmptyEditorKeyGrantsNothing(t *testing.T) {
	t.Setenv(readOnlyEnvVar, "1")
	t.Setenv(editorKeyEnvVar, "")
	mw := NewReadOnlyMiddleware()

	if got := serve(t, mw, http.MethodPost, "/workflows", ""); got != http.StatusForbidden {
		t.Fatalf("no key configured, no header = %d, want %d", got, http.StatusForbidden)
	}
	if got := serve(t, mw, http.MethodPost, "/workflows", "anything"); got != http.StatusForbidden {
		t.Fatalf("no key configured, header sent = %d, want %d", got, http.StatusForbidden)
	}
}

func TestIsTruthy(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE", " yes ", "on"} {
		if !isTruthy(v) {
			t.Errorf("isTruthy(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"", "0", "false", "no", "off", "maybe"} {
		if isTruthy(v) {
			t.Errorf("isTruthy(%q) = true, want false", v)
		}
	}
}
