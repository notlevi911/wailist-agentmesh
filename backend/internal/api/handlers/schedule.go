package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/robfig/cron/v3"

	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/respond"
)

// SetSchedule enables or updates a workflow's cron schedule. Parses the
// expression here (standard 5-field cron, same parser the scheduler package
// uses at fire time) rather than importing internal/scheduler for it --
// that package already imports this one for DecryptNodes, and a handler ->
// scheduler -> handler import would be a cycle.
func (d *Deps) SetSchedule(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ctx := r.Context()
	userID, _ := ctx.Value(CtxUserID).(string)

	wf, err := d.Store.GetWorkflow(ctx, id)
	if err != nil || wf.UserID != userID {
		respond.Error(w, http.StatusNotFound, "workflow not found")
		return
	}
	// ClaimDueSchedules only ever claims status='deployed' workflows -- a
	// schedule saved on a draft would silently never fire until the
	// workflow happens to be deployed later, with nothing here to warn the
	// caller that the nextRunAt it just got back is aspirational.
	if wf.Status != models.WorkflowStatusDeployed {
		respond.Error(w, http.StatusConflict, "deploy this workflow before scheduling it")
		return
	}

	var body struct {
		Cron string `json:"cron"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	sched, err := cron.ParseStandard(body.Cron)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid cron expression: "+err.Error())
		return
	}

	next := sched.Next(time.Now().UTC())
	if err := d.Store.SetWorkflowSchedule(ctx, id, body.Cron, next); err != nil {
		respond.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"cron": body.Cron, "nextRunAt": next})
}

// ClearSchedule disables a workflow's schedule.
func (d *Deps) ClearSchedule(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ctx := r.Context()
	userID, _ := ctx.Value(CtxUserID).(string)

	wf, err := d.Store.GetWorkflow(ctx, id)
	if err != nil || wf.UserID != userID {
		respond.Error(w, http.StatusNotFound, "workflow not found")
		return
	}
	if err := d.Store.ClearWorkflowSchedule(ctx, id); err != nil {
		respond.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
