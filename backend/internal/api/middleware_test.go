package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/agentmesh/backend/internal/api"
	"github.com/agentmesh/backend/internal/api/handlers"
)

func TestNewAuthMiddlewareRejectsNoToken(t *testing.T) {
	handler := api.NewAuthMiddleware("secret")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/workflows", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 got %d", w.Code)
	}
}

func TestNewAuthMiddlewareAcceptsValidToken(t *testing.T) {
	token := api.TestMakeToken("secret", "user-123")
	handler := api.NewAuthMiddleware("secret")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/workflows", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", w.Code)
	}
}

// A connector-OAuth state JWT is signed with the same secret and carries the
// same `sub` shape as a session token because it has to be self-contained on
// the front channel to the provider. It must never work as a session bearer
// token, since it travels through places a session token doesn't (provider
// redirect logs, browser history, our own access logs).
func TestNewAuthMiddlewareRejectsConnectorLinkToken(t *testing.T) {
	claims := struct {
		UserID string `json:"sub"`
		jwt.RegisteredClaims
	}{
		UserID: "user-123",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    handlers.ConnectorLinkIssuer,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(10 * time.Minute)),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("secret"))
	if err != nil {
		t.Fatalf("signing test token: %v", err)
	}

	handler := api.NewAuthMiddleware("secret")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/workflows", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 got %d — connector-link state token was accepted as a session token", w.Code)
	}
}
