package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentmesh/backend/internal/api"
	"github.com/agentmesh/backend/internal/api/handlers"
)

func TestWaitlistEndpointRetired(t *testing.T) {
	router := api.NewRouter(&handlers.Deps{})
	for _, path := range []string{"/waitlist", "/waitlist/"} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"email":"retired@example.test"}`))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusNotFound {
				t.Fatalf("POST %s = %d, want 404", path, response.Code)
			}
		})
	}

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/auth/signup", strings.NewReader("{")))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("direct signup validation = %d, want 400", response.Code)
	}
}
