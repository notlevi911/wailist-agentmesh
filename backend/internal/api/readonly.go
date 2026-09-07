package api

import (
	"crypto/subtle"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/agentmesh/backend/internal/respond"
)

// Read-only mode: refuse to let a client author a workflow.
//
// Note what this is NOT. The frontend decides who may author by viewport --
// a desktop is an editor, a phone is a viewer (frontend/src/lib/readonly.ts) --
// and a server cannot see a viewport. So this middleware cannot enforce that
// split, and does not try to. It answers a coarser question: may ANY client
// write to this deployment at all?
//
// That makes it useful for a deployment that is meant to be wholly read-only --
// a public demo, a mirror, a read replica -- and useless for the ordinary one,
// which is why it is off unless switched on. The client-side split remains a
// UX decision, honestly labelled as such rather than dressed up as a security
// boundary.
//
// Configured from the environment rather than from the request's identity:
// recording *which* clients may write would mean a user- or session-level
// capability, and that means a schema change. Nothing here reads or writes
// the database.

const (
	// Off unless explicitly enabled, so an existing deployment keeps behaving
	// exactly as it does today until someone opts in.
	readOnlyEnvVar = "WEB_READONLY_MODE"

	// The desktop app's way through. It is a shared secret, so it is exactly
	// as strong as that app's ability to keep one -- which is why the frontend
	// never sends it and never learns it.
	editorKeyEnvVar    = "EDITOR_CLIENT_KEY"
	editorKeyHeaderKey = "X-AgentMesh-Editor-Key"
)

// The graph-mutating endpoints, and only those. Running and stopping a
// workflow, reading anything, and every billing or account call are all
// deliberately absent -- a viewer can still operate a workflow somebody else
// built, which is the whole point of the second screen.
//
// Kept in step with WRITE_RULES in frontend/src/lib/readonly.ts: a call this
// list rejects but the frontend permits becomes a control that fails at the
// server with no explanation.
var readOnlyBlocked = []struct {
	method string
	path   *regexp.Regexp
}{
	{http.MethodPost, regexp.MustCompile(`^/workflows$`)},
	{http.MethodPut, regexp.MustCompile(`^/workflows/[^/]+$`)},
	{http.MethodDelete, regexp.MustCompile(`^/workflows/[^/]+$`)},
	{http.MethodPost, regexp.MustCompile(`^/workflows/[^/]+/deploy$`)},
	{http.MethodPost, regexp.MustCompile(`^/workflows/[^/]+/build$`)},
	{http.MethodPut, regexp.MustCompile(`^/workflows/[^/]+/schedule$`)},
	{http.MethodDelete, regexp.MustCompile(`^/workflows/[^/]+/schedule$`)},
	{http.MethodPut, regexp.MustCompile(`^/workflows/[^/]+/geofence$`)},
	{http.MethodDelete, regexp.MustCompile(`^/workflows/[^/]+/geofence$`)},
	// GET, but find-or-creates a workflow row server-side, so it belongs on
	// this list despite the general rule that reads are exempt.
	{http.MethodGet, regexp.MustCompile(`^/tendril/console$`)},
	// Same exception, same reason: find-or-creates the console's workflow
	// row. /prism/console/exists is the non-creating variant and is exempt.
	{http.MethodGet, regexp.MustCompile(`^/prism/console$`)},
}

// blocksWrite reports whether read-only mode rejects this method and path.
// Split out from the middleware so the rules can be tested without standing up
// a router or a request chain.
//
// The trailing slash is normalised away first. To be precise about why, since
// an earlier version of this comment got it wrong: chi does NOT currently
// route "/workflows/" to the "/workflows" handler. With no StripSlashes or
// RedirectSlashes registered in router.go, the trailing-slash form matches no
// route at all and chi answers 404 -- before this middleware is consulted,
// because a group-scoped middleware only runs for paths the router matched.
// TestTrailingSlashNeverReachesTheGroup in router_test.go pins that.
//
// So the normalisation is defence in depth, not a live requirement: the day
// somebody adds slash-handling middleware, these rules must still hold, and
// four lines that fail safe are cheaper than the bypass they would leave.
func blocksWrite(method, path string) bool {
	if len(path) > 1 {
		path = strings.TrimRight(path, "/")
		if path == "" {
			path = "/"
		}
	}
	for _, rule := range readOnlyBlocked {
		if rule.method == method && rule.path.MatchString(path) {
			return true
		}
	}
	return false
}

// NewReadOnlyMiddleware rejects graph-mutating requests with 403 while
// WEB_READONLY_MODE is set. When it is not, the returned middleware is a
// pass-through.
//
// The environment is read once, when the router is built, so a request cannot
// change the mode it is judged under.
func NewReadOnlyMiddleware() func(http.Handler) http.Handler {
	enabled := isTruthy(os.Getenv(readOnlyEnvVar))
	editorKey := os.Getenv(editorKeyEnvVar)

	// Logged because the failure mode is silent: a mistyped variable name
	// leaves the mode OFF and nothing anywhere says so. An operator who set
	// it and reads "disabled" here knows immediately that it did not take.
	// (A typed config loader would be the real fix, but this repo has no
	// config package -- auth.go and middleware.go read the environment the
	// same way -- and introducing one is well outside this change.)
	state := "disabled"
	if enabled {
		state = "enabled"
	}
	log.Printf("web read-only: %s (%s)", state, readOnlyEnvVar)

	return func(next http.Handler) http.Handler {
		if !enabled {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !blocksWrite(r.Method, r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			// An empty EDITOR_CLIENT_KEY must never mean "everyone is an
			// editor", so the bypass is only available once a key is set.
			if editorKey != "" && constantTimeEqual(r.Header.Get(editorKeyHeaderKey), editorKey) {
				next.ServeHTTP(w, r)
				return
			}
			respond.Error(w, http.StatusForbidden,
				"workflows are read-only here; edit them in the AgentMesh desktop app")
		})
	}
}

func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// Compared in constant time so a wrong key cannot be discovered a byte at a
// time from response timing.
func constantTimeEqual(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
