package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentmesh/backend/internal/api"
	"github.com/agentmesh/backend/internal/api/handlers"
	"github.com/agentmesh/backend/internal/models"
	"golang.org/x/crypto/bcrypt"
)

const deletionSecret = "account-deletion-test-secret-at-least-32-bytes"

func deletionRequest(router http.Handler, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodDelete, "/auth/me", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func deletionUser(t *testing.T, d *handlers.Deps, password string) models.User {
	t.Helper()
	hash := ""
	if password != "" {
		value, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
		if err != nil {
			t.Fatal(err)
		}
		hash = string(value)
	}
	u, err := d.Store.CreateUser(t.Context(), "delete-"+randSuffix(t)+"@example.test", hash)
	if err != nil {
		t.Fatal(err)
	}
	d.JWTSecret = deletionSecret
	return u
}

func TestDeleteAccountRequiresAuthentication(t *testing.T) {
	router := api.NewRouter(&handlers.Deps{JWTSecret: deletionSecret})
	for _, token := range []string{"", "invalid", api.TestMakeToken("wrong-secret", "owner"), api.TestMakeToken(deletionSecret, "")} {
		if rec := deletionRequest(router, token, `{"confirmation":"DELETE"}`); rec.Code != http.StatusUnauthorized {
			t.Fatalf("want 401, got %d: %s", rec.Code, rec.Body.String())
		}
	}
}

func TestDeleteAccountRejectsInvalidRequests(t *testing.T) {
	d := testDeps(t)
	u := deletionUser(t, d, "correct-password")
	router := api.NewRouter(d)
	token := api.TestMakeToken(deletionSecret, u.ID)
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"empty", "", 400},
		{"malformed", "{", 400},
		{"missing confirmation", `{"password":"correct-password"}`, 400},
		{"lowercase", `{"confirmation":"delete","password":"correct-password"}`, 400},
		{"leading space", `{"confirmation":" DELETE","password":"correct-password"}`, 400},
		{"trailing space", `{"confirmation":"DELETE ","password":"correct-password"}`, 400},
		{"trailing document", `{"confirmation":"DELETE","password":"correct-password"}{}`, 400},
		{"override owner", `{"confirmation":"DELETE","password":"correct-password","userId":"victim"}`, 400},
		{"oversized", `{"confirmation":"DELETE","password":"` + strings.Repeat("x", 4096) + `"}`, 400},
		{"missing password", `{"confirmation":"DELETE"}`, 401},
		{"wrong password", `{"confirmation":"DELETE","password":"wrong"}`, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := deletionRequest(router, token, tc.body)
			if rec.Code != tc.status {
				t.Fatalf("want %d, got %d: %s", tc.status, rec.Code, rec.Body.String())
			}
			if len(rec.Result().Cookies()) != 0 {
				t.Fatal("rejected deletion cleared cookies")
			}
			if _, err := d.Store.GetUserByID(t.Context(), u.ID); err != nil {
				t.Fatal("rejected deletion removed account", err)
			}
		})
	}
	req := httptest.NewRequest(http.MethodDelete, "/auth/me", strings.NewReader(`{"confirmation":"DELETE","password":"correct-password"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("unsafe content type: %d", rec.Code)
	}
}

func TestDeleteAccountInvalidatesSessions(t *testing.T) {
	for _, password := range []string{"correct-password", ""} {
		t.Run("password="+password, func(t *testing.T) {
			t.Setenv("BASE_URL", "https://app.example.test")
			d := testDeps(t)
			u := deletionUser(t, d, password)
			other := deletionUser(t, d, "other-password")
			router := api.NewRouter(d)
			token := api.TestMakeToken(deletionSecret, u.ID)
			me := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
			me.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, me)
			var profile struct {
				HasPassword bool `json:"hasPassword"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &profile); err != nil || rec.Code != 200 || profile.HasPassword != (password != "") {
				t.Fatalf("profile: %d %s %v", rec.Code, rec.Body.String(), err)
			}
			body, _ := json.Marshal(map[string]string{"confirmation": "DELETE", "password": password})
			rec = deletionRequest(router, token, string(body))
			if rec.Code != http.StatusNoContent {
				t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
			}
			cookies := map[string]*http.Cookie{}
			for _, cookie := range rec.Result().Cookies() {
				cookies[cookie.Name] = cookie
			}
			for _, name := range []string{"agentmesh_token", "agentmesh_ui"} {
				cookie := cookies[name]
				if cookie == nil || cookie.MaxAge != -1 || cookie.Value != "" || cookie.Path != "/" {
					t.Fatalf("cookie %s not cleared: %+v", name, cookie)
				}
			}
			if !cookies["agentmesh_token"].HttpOnly || !cookies["agentmesh_token"].Secure || cookies["agentmesh_token"].SameSite != http.SameSiteNoneMode {
				t.Fatal("session cookie attributes changed during clearing")
			}
			if _, err := d.Store.GetUserByID(t.Context(), other.ID); err != nil {
				t.Fatal("another account was deleted", err)
			}
			// A new router represents a backend instance with no local revocation cache.
			second := api.NewRouter(d)
			for _, path := range []string{"/auth/me", "/workflows", "/runs"} {
				for _, bearer := range []bool{true, false} {
					req := httptest.NewRequest(http.MethodGet, path, nil)
					if bearer {
						req.Header.Set("Authorization", "Bearer "+token)
					} else {
						req.AddCookie(&http.Cookie{Name: "agentmesh_token", Value: token})
					}
					rec := httptest.NewRecorder()
					second.ServeHTTP(rec, req)
					if rec.Code != http.StatusUnauthorized {
						t.Fatalf("old session accepted on %s: %d", path, rec.Code)
					}
				}
			}
			newUser, err := d.Store.CreateUser(t.Context(), u.Email, u.PasswordHash)
			if err != nil || newUser.ID == u.ID {
				t.Fatalf("re-registration: %+v %v", newUser, err)
			}
			if rec := deletionRequest(second, token, string(body)); rec.Code != 401 {
				t.Fatalf("old token can delete recreated account: %d", rec.Code)
			}
		})
	}
}

func TestDeleteAccountBlocksActiveMachineLeases(t *testing.T) {
	d := testDeps(t)
	u := deletionUser(t, d, "")
	wf, err := d.Store.CreateWorkflow(t.Context(), "Machine", u.ID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := d.Store.CreateRun(t.Context(), wf.ID, "test", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	lease, err := d.Store.InsertTendrilLease(t.Context(), models.TendrilLease{
		UserID: u.ID, WorkflowID: wf.ID, RunID: run.ID, NodeID: "node",
		LeaseID: "deletion-" + u.ID, LeaseTokenEnc: "encrypted", TendrilNodeID: "machine",
		RateUSDMicrosPerHour: 1, HoursPurchased: 1, ReservedUSDMicros: 1,
		FundedUntil: time.Now().Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := deletionRequest(api.NewRouter(d), api.TestMakeToken(deletionSecret, u.ID), `{"confirmation":"DELETE"}`)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "release all active machines") {
		t.Fatalf("active lease: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := d.Store.GetTendrilLease(t.Context(), lease.ID); err != nil {
		t.Fatal("active lease lost", err)
	}
	if _, err := d.Store.GetUserByID(t.Context(), u.ID); err != nil {
		t.Fatal("account lost", err)
	}
}
