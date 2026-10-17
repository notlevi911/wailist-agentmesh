package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentmesh/backend/internal/api/handlers"
)

type accountStub struct {
	exists bool
	err    error
	seen   string
}

func (s *accountStub) UserExists(_ context.Context, id string) (bool, error) {
	s.seen = id
	return s.exists, s.err
}

func TestAccountGuard(t *testing.T) {
	for _, tc := range []struct {
		name, id string
		exists   bool
		err      error
		status   int
	}{
		{"current account", "owner", true, nil, http.StatusNoContent},
		{"deleted account", "owner", false, nil, http.StatusUnauthorized},
		{"empty subject", "", true, nil, http.StatusUnauthorized},
		{"database unavailable", "owner", false, errors.New("unavailable"), http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &accountStub{exists: tc.exists, err: tc.err}
			called := false
			handler := requireAccount(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequest(http.MethodGet, "/workflows", nil)
			req = req.WithContext(context.WithValue(req.Context(), handlers.CtxUserID, tc.id))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.status || called != (tc.status == http.StatusNoContent) || store.seen != tc.id {
				t.Fatalf("status=%d called=%v checked=%q", rec.Code, called, store.seen)
			}
		})
	}
}
