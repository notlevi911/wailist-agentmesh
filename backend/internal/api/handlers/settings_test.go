package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentmesh/backend/internal/api"
	"github.com/agentmesh/backend/internal/api/handlers"
	"github.com/agentmesh/backend/internal/models"
)

func settingsRequest(router http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestSettingsRoutesRequireSession(t *testing.T) {
	router := api.NewRouter(&handlers.Deps{JWTSecret: deletionSecret})
	for _, tc := range []struct{ method, path string }{{"GET", "/settings"}, {"PATCH", "/settings"}, {"POST", "/auth/password"}} {
		rec := settingsRequest(router, tc.method, tc.path, "", "{}")
		if rec.Code != 401 {
			t.Fatalf("%s %s: %d", tc.method, tc.path, rec.Code)
		}
	}
}

func TestSettingsPreserveCookieAndNativeViewerAccess(t *testing.T) {
	t.Setenv("WEB_READONLY_MODE", "true")
	d := testDeps(t)
	owner := deletionUser(t, d, "password")
	token := api.TestMakeToken(deletionSecret, owner.ID)
	router := api.NewRouter(d)
	for _, native := range []bool{false, true} {
		req := httptest.NewRequest(http.MethodPatch, "/settings", strings.NewReader(`{"lowBalanceUsdMicros":1234567}`))
		req.Header.Set("Content-Type", "application/json")
		if native {
			req.Header.Set(handlers.NativeClientHeader, "android")
			req.Header.Set("Authorization", "Bearer "+token)
		} else {
			req.AddCookie(&http.Cookie{Name: "agentmesh_token", Value: token})
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("native=%v: %d %s", native, rec.Code, rec.Body)
		}
	}
}

func TestSettingsOwnedPatchAndDeletedSession(t *testing.T) {
	d := testDeps(t)
	owner := deletionUser(t, d, "")
	other := deletionUser(t, d, "")
	router := api.NewRouter(d)
	token := api.TestMakeToken(deletionSecret, owner.ID)
	for _, body := range []string{`{"lowBalanceUsdMicros":1234567}`, `{"maxCallSpendUsdMicros":765432}`} {
		rec := settingsRequest(router, "PATCH", "/settings", token, body)
		if rec.Code != 200 {
			t.Fatalf("patch: %d %s", rec.Code, rec.Body)
		}
	}
	rec := settingsRequest(router, "GET", "/settings", token, "")
	var got struct {
		models.UserSettings
		Limits map[string]int64 `json:"limits"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 || got.LowBalanceUSDMicros != 1234567 || got.MaxCallSpendUSDMicros == nil || *got.MaxCallSpendUSDMicros != 765432 {
		t.Fatalf("get: %d %s %v", rec.Code, rec.Body, err)
	}
	if got.Limits["minCallSpendUsdMicros"] != models.X402ProbeFloorUSDMicros || got.Limits["maxCallSpendUsdMicros"] != models.MaxSingleX402QuoteUSDMicros {
		t.Fatalf("bounds drifted: %+v", got.Limits)
	}
	untouched, err := d.Store.GetUserSettings(t.Context(), other.ID)
	if err != nil || untouched.LowBalanceUSDMicros != models.DefaultLowBalanceUSDMicros || untouched.MaxCallSpendUSDMicros != nil {
		t.Fatalf("other owner changed: %+v %v", untouched, err)
	}
	if rec = settingsRequest(router, "PATCH", "/settings", token, `{"userId":"`+other.ID+`","lowBalanceUsdMicros":0}`); rec.Code != 400 {
		t.Fatalf("owner override: %d", rec.Code)
	}
	if rec = deletionRequest(router, token, `{"confirmation":"DELETE"}`); rec.Code != 204 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec = settingsRequest(api.NewRouter(d), "GET", "/settings", token, ""); rec.Code != 401 {
		t.Fatalf("deleted session read: %d", rec.Code)
	}
}
