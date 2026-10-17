package nodes

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/tendril"
	"github.com/agentmesh/backend/internal/wallet"
)

func TestAutoFiltersEligibleOwnedLeasesBeforeChoosing(t *testing.T) {
	now := time.Now()
	lease := func(id, machine string, started, funded time.Time, hours float64) models.TendrilLease {
		token, err := wallet.Encrypt(id, testEncKey)
		if err != nil {
			t.Fatal(err)
		}
		return models.TendrilLease{ID: id, UserID: "user1", Status: "active", TendrilNodeID: machine,
			StartedAt: started, HoursPurchased: hours, FundedUntil: funded, LeaseTokenEnc: token}
	}
	older := lease("older-a", "a", now.Add(-30*time.Minute), now.Add(3*time.Hour), 1)
	newer := lease("newer-b", "b", now.Add(-15*time.Minute), now.Add(3*time.Hour), 1)
	expiredPurchase := lease("expired-purchase", "a", now.Add(-2*time.Hour), now.Add(3*time.Hour), 1)
	expiredProvider := lease("expired-provider", "a", now.Add(-10*time.Minute), now.Add(-time.Minute), 1)
	newestExpired := lease("newest-expired", "a", now.Add(-10*time.Minute), now.Add(3*time.Hour), 0.1)
	foreign := newer
	foreign.UserID = "another-user"
	listErr := errors.New("lease lookup unavailable")
	for _, tc := range []struct {
		name, machine, wantToken string
		leases                   []models.TendrilLease
		lookupErr                error
	}{
		{name: "eligible-control", leases: []models.TendrilLease{older}, wantToken: "older-a"},
		{name: "purchased-time-expired-with-funded-pool", leases: []models.TendrilLease{expiredPurchase}},
		{name: "provider-expired-before-purchased-window", leases: []models.TendrilLease{expiredProvider}},
		{name: "older-requested-machine", machine: "a", leases: []models.TendrilLease{newer, older}, wantToken: "older-a"},
		{name: "expired-newest-before-eligible-older", leases: []models.TendrilLease{newestExpired, older}, wantToken: "older-a"},
		{name: "earlier-provider-deadline-before-eligible-older", leases: []models.TendrilLease{expiredProvider, older}, wantToken: "older-a"},
		{name: "another-user-excluded", leases: []models.TendrilLease{foreign, older}, wantToken: "older-a"},
		{name: "newest-eligible-without-machine-filter", leases: []models.TendrilLease{newer, older}, wantToken: "newer-b"},
		{name: "lookup-error-must-not-rent", lookupErr: listErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var paths, tokens []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/x402/run" {
					tokens = append(tokens, r.Header.Get("Authorization"))
					w.Write([]byte(`{"ok":true}`))
				} else if r.URL.Path == "/explorer" {
					w.Write([]byte(`{"nodes":[]}`))
				} else {
					t.Errorf("unexpected provider path %s", r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer srv.Close()
			store := &fakeTendrilStore{activeLeases: tc.leases, activeLeaseErr: tc.lookupErr}
			if len(tc.leases) > 0 {
				store.hasLatestLease, store.latestLease = true, tc.leases[0]
			}
			_, err := executeTendrilAuto(context.Background(), models.WorkflowNode{TendrilNodeID: tc.machine,
				CustomParams: []models.CustomParam{{Name: "payload", Value: "print(1)"}}}, emptyRunContext{},
				TendrilConfig{Client: tendril.NewClient(srv.URL), Store: store, EncryptKey: testEncKey, UserID: "user1"})
			if tc.lookupErr != nil {
				if !errors.Is(err, tc.lookupErr) || len(paths) != 0 {
					t.Fatalf("lookup failure: error=%v paths=%v", err, paths)
				}
			} else if tc.wantToken != "" {
				if err != nil || len(paths) != 1 || len(tokens) != 1 || tokens[0] != "Bearer "+tc.wantToken {
					t.Fatalf("reuse: error=%v paths=%v tokens=%v, want %s only", err, paths, tokens, tc.wantToken)
				}
			} else if err == nil || len(tokens) != 0 || len(paths) != 1 || paths[0] != "/explorer" {
				t.Fatalf("expired lease: error=%v paths=%v tokens=%v, want market lookup only", err, paths, tokens)
			}
		})
	}
}
