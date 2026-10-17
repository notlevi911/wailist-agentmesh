package api

import (
	"context"
	"log"
	"net/http"

	"github.com/agentmesh/backend/internal/api/handlers"
	"github.com/agentmesh/backend/internal/respond"
)

type accountStore interface {
	UserExists(context.Context, string) (bool, error)
}

func requireAccount(store accountStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, _ := r.Context().Value(handlers.CtxUserID).(string)
			if userID == "" {
				respond.Error(w, http.StatusUnauthorized, "invalid session")
				return
			}
			exists, err := store.UserExists(r.Context(), userID)
			if err != nil {
				log.Printf("verify session account: %v", err)
				respond.Error(w, http.StatusServiceUnavailable, "could not verify session")
				return
			}
			if !exists {
				respond.Error(w, http.StatusUnauthorized, "account no longer exists")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
