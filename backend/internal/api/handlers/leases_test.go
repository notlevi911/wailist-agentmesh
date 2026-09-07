package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentmesh/backend/internal/api/handlers"
	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/wallet"
)

// withUser injects an authenticated user id into a request context, the same
// way NewAuthMiddleware does after verifying a JWT.
func withUser(r *http.Request, userID string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), handlers.CtxUserID, userID))
}

// leaseFixture inserts one active lease owned by a fresh "owner" user and
// returns Deps, the lease's own row id, plus a second, unrelated "other"
// user's id.
func leaseFixture(t *testing.T) (d *handlers.Deps, leaseID, owner, other string) {
	t.Helper()
	d = testDeps(t)
	ctx := context.Background()

	ownerUser, err := d.Store.CreateUser(ctx, "lease-owner-"+randSuffix(t)+"@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	otherUser, err := d.Store.CreateUser(ctx, "lease-other-"+randSuffix(t)+"@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	wf, err := d.Store.CreateWorkflow(ctx, "Lease Test WF", ownerUser.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Store.DeleteWorkflow(context.Background(), wf.ID) })
	run, err := d.Store.CreateRun(ctx, wf.ID, "test", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}

	privKeyPEM := "-----BEGIN OPENSSH PRIVATE KEY-----\nfake\n-----END OPENSSH PRIVATE KEY-----"
	keyEnc, err := wallet.Encrypt(privKeyPEM, d.EncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := d.Store.InsertTendrilLease(ctx, models.TendrilLease{
		UserID: ownerUser.ID, WorkflowID: wf.ID, RunID: run.ID, NodeID: "n2",
		LeaseID: "lease_x_" + randSuffix(t), LeaseTokenEnc: "enc-token", TendrilNodeID: "node1",
		SSHPrivateKeyEnc:     keyEnc,
		RateUSDMicrosPerHour: 1, HoursPurchased: 1, ReservedUSDMicros: 1,
		FundedUntil: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	return d, saved.ID, ownerUser.ID, otherUser.ID
}

func randSuffix(t *testing.T) string {
	t.Helper()
	return time.Now().Format("20060102150405.000000000")
}

// A lease's private key is SSH access to a running machine. It must be
// downloadable only by the user who rented it.
func TestLeaseKeyDeniedToOtherUsers(t *testing.T) {
	d, leaseID, owner, other := leaseFixture(t)

	rec := httptest.NewRecorder()
	req := withURLParam(httptest.NewRequest(http.MethodGet, "/leases/"+leaseID+"/key", nil), "id", leaseID)
	d.DownloadLeaseKey(rec, withUser(req, other))
	if rec.Code != http.StatusNotFound {
		t.Errorf("other user got %d, want 404", rec.Code)
	}

	rec2 := httptest.NewRecorder()
	req2 := withURLParam(httptest.NewRequest(http.MethodGet, "/leases/"+leaseID+"/key", nil), "id", leaseID)
	d.DownloadLeaseKey(rec2, withUser(req2, owner))
	if rec2.Code != http.StatusOK {
		t.Fatalf("owner got %d, want 200", rec2.Code)
	}
	if !strings.Contains(rec2.Body.String(), "OPENSSH PRIVATE KEY") {
		t.Errorf("body is not a private key:\n%s", rec2.Body.String())
	}
	if cd := rec2.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q, want an attachment", cd)
	}
}

func TestListLeasesOnlyReturnsOwnActiveLeases(t *testing.T) {
	d, _, owner, other := leaseFixture(t)

	rec := httptest.NewRecorder()
	d.ListLeases(rec, withUser(httptest.NewRequest(http.MethodGet, "/leases", nil), owner))
	var got struct {
		Leases []models.TendrilLease `json:"leases"`
	}
	json.NewDecoder(rec.Body).Decode(&got)
	if len(got.Leases) != 1 {
		t.Fatalf("owner sees %d leases, want 1", len(got.Leases))
	}
	// The encrypted token and key must never be serialized to a client.
	if strings.Contains(rec.Body.String(), "lease_token_enc") ||
		strings.Contains(rec.Body.String(), "PRIVATE KEY") {
		t.Error("lease JSON leaked a secret")
	}

	rec2 := httptest.NewRecorder()
	d.ListLeases(rec2, withUser(httptest.NewRequest(http.MethodGet, "/leases", nil), other))
	json.NewDecoder(rec2.Body).Decode(&got)
	if len(got.Leases) != 0 {
		t.Errorf("other user sees %d leases, want 0", len(got.Leases))
	}
}
