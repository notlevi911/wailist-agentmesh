package nodes

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/tendril"
)

func TestAutoTopupBounds(t *testing.T) {
	for _, tc := range []struct {
		name          string
		minimum       int64
		maximum       int64
		accountCredit int64
		wantAmount    string
		wantError     string
	}{
		{"partial credit below minimum", 500_000, 2_000_000, 10_000_000, "500000", "did not settle"},
		{"minimum equals budget", 1_000_000, 2_000_000, 10_000_000, "1000000", "did not settle"},
		{"minimum exceeds budget", 1_500_000, 2_000_000, 10_000_000, "", "exceeds the"},
		{"provider maximum", 500_000, 400_000, 10_000_000, "", "maximum topup"},
		{"cannot afford raised topup", 500_000, 2_000_000, 0, "", "AgentMesh credits"},
		{"shortfall already satisfies minimum", 100_000, 2_000_000, 10_000_000, "250000", "did not settle"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var amount string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/platform":
					fmt.Fprintf(w, `{"minTopUpAtomic":%d,"maxTopUpAtomic":%d}`, tc.minimum, tc.maximum)
				case "/explorer":
					fmt.Fprint(w, `{"nodes":[{"id":"m1","status":"online","pricePerHourUsd":2}]}`)
				case "/topup":
					amount = r.URL.Query().Get("amount")
					// No payment challenge: assert sizing without settling money.
					fmt.Fprint(w, `{}`)
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer srv.Close()
			store := &fakeTendrilStore{tendrilCredit: 750_000, agentMeshCredit: tc.accountCredit}
			_, err := ExecuteTendril(context.Background(), models.WorkflowNode{
				TendrilAction: "auto", TendrilAmount: "1",
				CustomParams: []models.CustomParam{{Name: "payload", Value: "test"}},
			}, emptyRunContext{}, TendrilConfig{Client: tendril.NewClient(srv.URL), Store: store, UserID: "user1"})
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("error = %v, want %q", err, tc.wantError)
			}
			if amount != tc.wantAmount {
				t.Errorf("topup amount = %q, want %q", amount, tc.wantAmount)
			}
			if store.tendrilCredit != 750_000 || store.agentMeshCredit != tc.accountCredit {
				t.Fatal("credit changed without payment settlement")
			}
		})
	}
}

func TestAutoRejectsNonFiniteOrOversizedBudgetBeforeRequests(t *testing.T) {
	for _, budget := range []string{"NaN", "Inf", "-Inf", "1e20"} {
		t.Run(budget, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("unexpected request with invalid budget: %s", r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
			}))
			defer srv.Close()
			_, err := ExecuteTendril(context.Background(), models.WorkflowNode{
				TendrilAction: "auto", TendrilAmount: budget,
				CustomParams: []models.CustomParam{{Name: "payload", Value: "test"}},
			}, emptyRunContext{}, TendrilConfig{Client: tendril.NewClient(srv.URL), Store: &fakeTendrilStore{}, UserID: "user1"})
			if err == nil {
				t.Fatal("invalid budget accepted")
			}
		})
	}
}
