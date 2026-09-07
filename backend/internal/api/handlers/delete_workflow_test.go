package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentmesh/backend/internal/api/handlers"
	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/wallet"
)

// deleteWorkflowReq issues DELETE /workflows/{id} as userID.
func deleteWorkflowReq(d *handlers.Deps, workflowID, userID string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := withURLParam(httptest.NewRequest(http.MethodDelete, "/workflows/"+workflowID, nil), "id", workflowID)
	d.DeleteWorkflow(rec, withUser(req, userID))
	return rec
}

// A workflow that has been run must still be deletable. Before migration
// 000019 this was the common case and it always failed: runs.workflow_id had no
// ON DELETE action, so Postgres refused with a raw runs_workflow_id_fkey
// violation. This test fails against the old schema and is the regression guard
// for that migration.
func TestDeleteWorkflowWithRunHistory(t *testing.T) {
	d := testDeps(t)
	ctx := context.Background()

	user, err := d.Store.CreateUser(ctx, "wf-del-runs-"+randSuffix(t)+"@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	wf, err := d.Store.CreateWorkflow(ctx, "Delete With Runs", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := d.Store.CreateRun(ctx, wf.ID, "test", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	// A run log too: run_logs cascades from runs, so this exercises the full
	// two-level cascade the delete now depends on.
	if _, err := d.Store.InsertRunLog(ctx, models.RunLog{
		RunID: run.ID, StepIndex: 0, NodeID: "n1", NodeType: "agent", Status: "success",
	}); err != nil {
		t.Fatal(err)
	}

	if rec := deleteWorkflowReq(d, wf.ID, user.ID); rec.Code != http.StatusNoContent {
		t.Fatalf("delete got %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}

	// Gone for real, not just reported as deleted.
	if _, err := d.Store.GetWorkflow(ctx, wf.ID); err == nil {
		t.Error("workflow still readable after a 204 delete")
	}
	if _, err := d.Store.GetRun(ctx, run.ID); err == nil {
		t.Error("run survived the workflow it belonged to; the cascade did not fire")
	}
}

// Deleting is authorized by ownership, and a workflow you don't own must be
// indistinguishable from one that doesn't exist — a 403 would confirm the id is
// real. The row must also survive.
func TestDeleteWorkflowOtherUserGets404(t *testing.T) {
	d := testDeps(t)
	ctx := context.Background()

	owner, err := d.Store.CreateUser(ctx, "wf-del-owner-"+randSuffix(t)+"@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	other, err := d.Store.CreateUser(ctx, "wf-del-other-"+randSuffix(t)+"@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	wf, err := d.Store.CreateWorkflow(ctx, "Not Yours", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Store.DeleteWorkflow(context.Background(), wf.ID) })

	if rec := deleteWorkflowReq(d, wf.ID, other.ID); rec.Code != http.StatusNotFound {
		t.Fatalf("other user got %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	if _, err := d.Store.GetWorkflow(ctx, wf.ID); err != nil {
		t.Errorf("workflow was destroyed by a non-owner's delete: %v", err)
	}
}

// tendril_leases is ON DELETE RESTRICT on purpose (migration 000018): its row
// holds the only copy of an active lease's encrypted SSH credentials, so a
// workflow with lease history must refuse to delete — and say so in plain
// language rather than leaking the constraint name and SQLSTATE, which is what
// the handler used to render straight into the UI.
func TestDeleteWorkflowWithLeaseHistoryGets409(t *testing.T) {
	d := testDeps(t)
	ctx := context.Background()

	user, err := d.Store.CreateUser(ctx, "wf-del-lease-"+randSuffix(t)+"@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	wf, err := d.Store.CreateWorkflow(ctx, "Has A Lease", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := d.Store.CreateRun(ctx, wf.ID, "test", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	keyEnc, err := wallet.Encrypt("-----BEGIN OPENSSH PRIVATE KEY-----\nfake\n-----END OPENSSH PRIVATE KEY-----", d.EncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Store.InsertTendrilLease(ctx, models.TendrilLease{
		UserID: user.ID, WorkflowID: wf.ID, RunID: run.ID, NodeID: "n2",
		LeaseID: "lease_del_" + randSuffix(t), LeaseTokenEnc: "enc-token", TendrilNodeID: "node1",
		SSHPrivateKeyEnc:     keyEnc,
		RateUSDMicrosPerHour: 1, HoursPurchased: 1, ReservedUSDMicros: 1,
		FundedUntil: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	rec := deleteWorkflowReq(d, wf.ID, user.ID)
	if rec.Code != http.StatusConflict {
		t.Fatalf("got %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Tendril machine lease history") {
		t.Errorf("409 body is not the friendly message: %s", body)
	}
	// The raw driver message must never reach the client.
	for _, leak := range []string{"SQLSTATE", "fkey", "constraint"} {
		if strings.Contains(body, leak) {
			t.Errorf("409 body leaks driver detail %q: %s", leak, body)
		}
	}
	if _, err := d.Store.GetWorkflow(ctx, wf.ID); err != nil {
		t.Errorf("workflow should have survived the refused delete: %v", err)
	}
}
