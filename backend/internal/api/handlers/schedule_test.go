package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentmesh/backend/internal/api/handlers"
)

func TestSetScheduleRejectsUndeployedWorkflow(t *testing.T) {
	d := testDeps(t)

	wf, err := d.Store.CreateWorkflow(t.Context(), "Schedule Draft Test", "dev")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Store.DeleteWorkflow(context.Background(), wf.ID) })

	body, _ := json.Marshal(map[string]string{"cron": "0 9 * * *"})
	req := httptest.NewRequest(http.MethodPut, "/workflows/"+wf.ID+"/schedule", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), handlers.CtxUserID, "dev"))
	req = withURLParam(req, "id", wf.ID)
	w := httptest.NewRecorder()
	d.SetSchedule(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("want 409 for a draft (undeployed) workflow, got %d body=%s", w.Code, w.Body.String())
	}

	got, err := d.Store.GetWorkflow(t.Context(), wf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ScheduleCron != nil {
		t.Errorf("schedule_cron = %v, want nil (a rejected schedule must never be persisted)", got.ScheduleCron)
	}
}

func TestSetScheduleValidatesCronExpression(t *testing.T) {
	d := testDeps(t)

	wf, err := d.Store.CreateWorkflow(t.Context(), "Schedule Handler Test", "dev")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Store.DeleteWorkflow(context.Background(), wf.ID) })
	if err := d.Store.SetWorkflowDeployed(t.Context(), wf.ID, "https://example.test/run", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]string{"cron": "not a cron expr"})
	req := httptest.NewRequest(http.MethodPut, "/workflows/"+wf.ID+"/schedule", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), handlers.CtxUserID, "dev"))
	req = withURLParam(req, "id", wf.ID)
	w := httptest.NewRecorder()
	d.SetSchedule(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for an invalid cron expression, got %d body=%s", w.Code, w.Body.String())
	}

	got, err := d.Store.GetWorkflow(t.Context(), wf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ScheduleCron != nil {
		t.Errorf("schedule_cron = %v, want nil (an invalid expression must never be persisted)", got.ScheduleCron)
	}
}

func TestSetAndClearScheduleRoundTrip(t *testing.T) {
	d := testDeps(t)

	wf, err := d.Store.CreateWorkflow(t.Context(), "Schedule Handler Round Trip Test", "dev")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Store.DeleteWorkflow(context.Background(), wf.ID) })
	if err := d.Store.SetWorkflowDeployed(t.Context(), wf.ID, "https://example.test/run", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]string{"cron": "0 9 * * *"})
	req := httptest.NewRequest(http.MethodPut, "/workflows/"+wf.ID+"/schedule", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), handlers.CtxUserID, "dev"))
	req = withURLParam(req, "id", wf.ID)
	w := httptest.NewRecorder()
	d.SetSchedule(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}

	got, err := d.Store.GetWorkflow(t.Context(), wf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ScheduleCron == nil || *got.ScheduleCron != "0 9 * * *" {
		t.Fatalf("schedule_cron = %v, want \"0 9 * * *\"", got.ScheduleCron)
	}
	if got.ScheduleNextRunAt == nil {
		t.Fatal("schedule_next_run_at is nil after SetSchedule")
	}

	clearReq := httptest.NewRequest(http.MethodDelete, "/workflows/"+wf.ID+"/schedule", nil)
	clearReq = clearReq.WithContext(context.WithValue(clearReq.Context(), handlers.CtxUserID, "dev"))
	clearReq = withURLParam(clearReq, "id", wf.ID)
	cw := httptest.NewRecorder()
	d.ClearSchedule(cw, clearReq)
	if cw.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d body=%s", cw.Code, cw.Body.String())
	}

	got, err = d.Store.GetWorkflow(t.Context(), wf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ScheduleCron != nil {
		t.Errorf("schedule_cron = %v, want nil after ClearSchedule", got.ScheduleCron)
	}
}

func TestSetScheduleRejectsNonOwner(t *testing.T) {
	d := testDeps(t)

	wf, err := d.Store.CreateWorkflow(t.Context(), "Schedule Owner Test", "owner")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Store.DeleteWorkflow(context.Background(), wf.ID) })

	body, _ := json.Marshal(map[string]string{"cron": "0 9 * * *"})
	req := httptest.NewRequest(http.MethodPut, "/workflows/"+wf.ID+"/schedule", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), handlers.CtxUserID, "someone-else"))
	req = withURLParam(req, "id", wf.ID)
	w := httptest.NewRecorder()
	d.SetSchedule(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404 for a non-owner, got %d body=%s", w.Code, w.Body.String())
	}
}
