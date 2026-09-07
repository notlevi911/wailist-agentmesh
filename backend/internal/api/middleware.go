package api

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/agentmesh/backend/internal/api/handlers"
	"github.com/agentmesh/backend/internal/respond"
)

const authCookieName = "agentmesh_token"

// allowedRequestHeaders backs Access-Control-Allow-Headers below. Built from
// handlers.NativeClientHeader rather than a hand-typed literal for that one
// entry: a future rename there is a compile error here instead of a CORS
// preflight silently rejecting every native request until someone notices.
// The x402/relay headers have no equivalent shared constant elsewhere in the
// codebase yet, so they stay literal for now rather than inventing one just
// for this list.
var allowedRequestHeaders = strings.Join([]string{
	"Content-Type",
	"Authorization",
	"Payment-Signature",
	"X-Payment",
	"X-Relay-Method",
	"X-Relay-Body",
	handlers.NativeClientHeader,
	// readonly.go's EDITOR_CLIENT_KEY bypass reads this off incoming
	// requests; omitting it here means a browser-based caller's preflight
	// rejects the header before the server ever sees it, silently defeating
	// the bypass for any such caller.
	editorKeyHeaderKey,
}, ", ")

// allowedOrigins parses CORS_ORIGIN, which accepts a comma-separated list.
//
// A list rather than a single value because this API is now called by two
// different first-party clients: the web app on its own domain, and the native
// Android shell, whose WebView origin is https://localhost. One value cannot
// serve both, and the obvious escape -- falling back to "*" -- is not
// available: a wildcard is illegal on a credentialed request, so it would sign
// the web app out.
func allowedOrigins() []string {
	raw := strings.Split(os.Getenv("CORS_ORIGIN"), ",")
	out := make([]string, 0, len(raw))
	for _, o := range raw {
		if o = strings.TrimRight(strings.TrimSpace(o), "/"); o != "" {
			out = append(out, o)
		}
	}
	return out
}

func corsMiddleware(next http.Handler) http.Handler {
	origins := allowedOrigins()
	// With no origins configured we keep the old permissive behaviour: a
	// wildcard, and therefore no credentials. Unchanged for any deployment
	// that never set the variable.
	wildcard := len(origins) == 0

	allowed := make(map[string]bool, len(origins))
	for _, o := range origins {
		allowed[o] = true
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo the REQUEST's origin rather than a fixed string. With more than
		// one permitted origin there is no single correct value to hardcode,
		// and a credentialed response may not answer "*" -- the browser
		// rejects it outright.
		reqOrigin := strings.TrimRight(r.Header.Get("Origin"), "/")
		allowCreds := false
		switch {
		case wildcard:
			w.Header().Set("Access-Control-Allow-Origin", "*")
		case allowed[reqOrigin]:
			w.Header().Set("Access-Control-Allow-Origin", reqOrigin)
			allowCreds = true
		case reqOrigin == "" && len(origins) == 1:
			// No Origin header at all -- not an unrecognised origin, an ABSENT
			// one. A cross-origin browser request always carries one; this is
			// either a same-origin request, a non-browser caller that ignores
			// CORS headers regardless, or a request that had its Origin
			// stripped somewhere in transit before reaching here. The prior
			// version of this middleware (before multi-origin support)
			// answered a single configured origin unconditionally, with no
			// comparison at all, so this restores that behaviour for exactly
			// the one case it covered -- never for a MISMATCHED origin, which
			// is a real different caller and must still be denied below.
			w.Header().Set("Access-Control-Allow-Origin", origins[0])
			allowCreds = true
		default:
			// Unrecognised origin: send the first configured one, which the
			// browser will compare against the caller and reject. Answering
			// nothing at all is equivalent, but this keeps the header shape
			// consistent for anything inspecting responses.
			w.Header().Set("Access-Control-Allow-Origin", origins[0])
		}
		// The response now varies by request origin, so any shared cache must
		// key on it. Without this a proxy can serve the web app's CORS headers
		// to the native shell, and the failure looks like an intermittent
		// CORS error with no pattern to it.
		w.Header().Add("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		// x402 clients send payment proofs via Payment-Signature (the v2
		// spec's canonical header, base64 JSON) or X-Payment (legacy, raw
		// JSON); X-Relay-Method and X-Relay-Body tell the relay what to
		// forward to the target. Without these on the allow-list, no
		// browser-based agent or crawler can complete a paid call at all --
		// the preflight rejects the payment header before the request is
		// ever sent.
		w.Header().Set("Access-Control-Allow-Headers", allowedRequestHeaders)
		// Response headers are invisible to browser JS unless exposed:
		// Payment-Required carries the 402 challenge (base64 JSON), and the
		// three settlement headers carry the inbound/outbound tx ids the run
		// console displays.
		w.Header().Set("Access-Control-Expose-Headers", "Payment-Required, X-Inbound-Settled, X-Settlement-TxId, X-Outbound-Settlement-TxId")
		if allowCreds {
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// NewAuthMiddleware returns a middleware that validates HS256 JWTs.
func NewAuthMiddleware(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			// EventSource with withCredentials sends cookies automatically.
			// Cookie is the primary auth path; Authorization header is kept for
			// non-browser clients (CLI tools, tests, etc.).
			if raw == "" {
				if c, err := r.Cookie(authCookieName); err == nil {
					raw = c.Value
				}
			}
			if raw == "" {
				respond.Error(w, http.StatusUnauthorized, "missing token")
				return
			}
			claims := &jwtClaims{}
			_, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
				if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, jwt.ErrSignatureInvalid
				}
				return []byte(secret), nil
			})
			if err != nil {
				respond.Error(w, http.StatusUnauthorized, "invalid token")
				return
			}
			// Reject anything minted for a narrower purpose than a session —
			// e.g. the connector-OAuth state JWT, which is signed with this
			// same secret but travels on a front channel (provider redirects,
			// logs) and must never work as a bearer credential here.
			if claims.Issuer != "" {
				respond.Error(w, http.StatusUnauthorized, "invalid token")
				return
			}
			ctx := context.WithValue(r.Context(), handlers.CtxUserID, claims.UserID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

type jwtClaims struct {
	UserID string `json:"sub"`
	Email  string `json:"email"`
	jwt.RegisteredClaims
}

// TestMakeToken creates a valid signed token for use in tests only.
func TestMakeToken(secret, userID string) string {
	claims := jwtClaims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	t, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	return t
}
