package handlers_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/agentmesh/backend/internal/api"
	"golang.org/x/crypto/bcrypt"
)

func TestPasswordChangeReauthAndLegacySession(t *testing.T) {
	d := testDeps(t)
	owner := deletionUser(t, d, "old-password")
	other := deletionUser(t, d, "other-password")
	router := api.NewRouter(d)
	token := api.TestMakeToken(deletionSecret, owner.ID)
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"currentPassword":"wrong","newPassword":"new-password"}`, 401},
		{`{"currentPassword":"old-password","newPassword":"short"}`, 400},
		{`{"currentPassword":"old-password","newPassword":"` + strings.Repeat("é", 37) + `"}`, 400},
		{`{"currentPassword":"old-password","newPassword":"new-password","userId":"` + other.ID + `"}`, 400},
		{`{"currentPassword":"old-password","newPassword":"new-password"}{}`, 400},
		{"{", 400},
	} {
		rec := settingsRequest(router, "POST", "/auth/password", token, tc.body)
		if rec.Code != tc.status {
			t.Fatalf("reject: %d %s", rec.Code, rec.Body)
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Fatal("rejected change changed cookies")
		}
	}
	rec := settingsRequest(router, "POST", "/auth/password", token, `{"currentPassword":"old-password","newPassword":"new-password"}`)
	if rec.Code != 204 || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("change: %d %s", rec.Code, rec.Body)
	}
	user, err := d.Store.GetUserByID(t.Context(), owner.ID)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte("new-password")) != nil {
		t.Fatalf("new password not stored: %v", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte("old-password")) == nil {
		t.Fatal("old password accepted")
	}
	user, err = d.Store.GetUserByID(t.Context(), other.ID)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte("other-password")) != nil {
		t.Fatal("other password changed")
	}
	for _, path := range []string{"/auth/me", "/settings"} {
		if rec = settingsRequest(api.NewRouter(d), "GET", path, token, ""); rec.Code != http.StatusOK {
			t.Fatalf("valid legacy session lost on %s: %d", path, rec.Code)
		}
	}
	if rec = deletionRequest(router, token, `{"confirmation":"DELETE","password":"old-password"}`); rec.Code != 401 {
		t.Fatalf("deletion accepted old password: %d", rec.Code)
	}
}

func TestOAuthOnlyCannotCreatePassword(t *testing.T) {
	d := testDeps(t)
	user := deletionUser(t, d, "")
	rec := settingsRequest(api.NewRouter(d), "POST", "/auth/password", api.TestMakeToken(deletionSecret, user.ID), `{"currentPassword":"","newPassword":"new-password"}`)
	if rec.Code != 400 {
		t.Fatalf("OAuth password: %d %s", rec.Code, rec.Body)
	}
}

func TestProfileUpdatePreservesAccountMetadata(t *testing.T) {
	d := testDeps(t)
	user := deletionUser(t, d, "password")
	router := api.NewRouter(d)
	token := api.TestMakeToken(deletionSecret, user.ID)
	rec := settingsRequest(router, "PATCH", "/auth/me", token, `{"name":" Updated ","orgName":" Team "}`)
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 {
		t.Fatalf("profile: %d %s %v", rec.Code, rec.Body, err)
	}
	if got["id"] != user.ID || got["name"] != "Updated" || got["orgName"] != "Team" || got["hasPassword"] != true || got["createdAt"] == nil || got["lowBalanceUsdMicros"] != float64(5_000_000) {
		t.Fatalf("profile fields: %+v", got)
	}
}
