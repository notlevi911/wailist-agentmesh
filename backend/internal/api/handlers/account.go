package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"

	"github.com/agentmesh/backend/internal/db"
	"github.com/agentmesh/backend/internal/respond"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

func (d *Deps) DeleteAccount(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(CtxUserID).(string)
	if userID == "" {
		respond.Error(w, http.StatusUnauthorized, "sign in to delete your account")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		respond.Error(w, http.StatusUnsupportedMediaType, "application/json required")
		return
	}
	var body struct {
		Confirmation string `json:"confirmation"`
		Password     string `json:"password"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid deletion request")
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		respond.Error(w, http.StatusBadRequest, "invalid deletion request")
		return
	}
	if body.Confirmation != "DELETE" {
		respond.Error(w, http.StatusBadRequest, "type DELETE exactly to confirm account deletion")
		return
	}

	user, err := d.Store.GetUserByID(r.Context(), userID)
	if errors.Is(err, pgx.ErrNoRows) {
		respond.Error(w, http.StatusUnauthorized, "account no longer exists")
		return
	}
	if err != nil {
		log.Printf("load account for deletion: %v", err)
		respond.Error(w, http.StatusInternalServerError, "could not delete account")
		return
	}
	if user.PasswordHash != "" && bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(body.Password)) != nil {
		respond.Error(w, http.StatusUnauthorized, "current password is incorrect")
		return
	}
	if err := d.Store.DeleteAccount(r.Context(), user.ID, user.PasswordHash); err != nil {
		switch {
		case errors.Is(err, db.ErrActiveMachineLeases):
			respond.Error(w, http.StatusConflict, "release all active machines before deleting your account")
		case errors.Is(err, db.ErrAccountChanged):
			respond.Error(w, http.StatusConflict, "account credentials changed; reload and try again")
		case errors.Is(err, pgx.ErrNoRows):
			respond.Error(w, http.StatusUnauthorized, "account no longer exists")
		default:
			log.Printf("delete account: %v", err)
			respond.Error(w, http.StatusInternalServerError, "could not delete account")
		}
		return
	}
	d.clearAuthCookie(w)
	d.clearUICookie(w)
	w.WriteHeader(http.StatusNoContent)
}
