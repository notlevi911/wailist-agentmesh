package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/agentmesh/backend/internal/db"
	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/respond"
)

func (d *Deps) ListWorkflows(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(CtxUserID).(string)
	wfs, err := d.Store.ListWorkflows(r.Context(), userID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if wfs == nil {
		wfs = []models.Workflow{}
	}
	for i := range wfs {
		wfs[i].Nodes = maskNodes(wfs[i].Nodes)
	}
	respond.JSON(w, http.StatusOK, wfs)
}

func (d *Deps) CreateWorkflow(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(CtxUserID).(string)
	var body struct {
		Name string `json:"name"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Name == "" {
		body.Name = "Untitled workflow"
	}
	wf, err := d.Store.CreateWorkflow(r.Context(), body.Name, userID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	respond.JSON(w, http.StatusCreated, wf)
}

func (d *Deps) GetWorkflow(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	userID, _ := r.Context().Value(CtxUserID).(string)
	wf, err := d.Store.GetWorkflow(r.Context(), id)
	if err != nil || wf.UserID != userID {
		respond.Error(w, http.StatusNotFound, "workflow not found")
		return
	}
	wf.Nodes = maskNodes(wf.Nodes)
	respond.JSON(w, http.StatusOK, wf)
}

func (d *Deps) UpdateWorkflow(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	userID, _ := r.Context().Value(CtxUserID).(string)
	existing, err := d.Store.GetWorkflow(r.Context(), id)
	if err != nil || existing.UserID != userID {
		respond.Error(w, http.StatusNotFound, "workflow not found")
		return
	}
	var body struct {
		Name  string                `json:"name"`
		Nodes []models.WorkflowNode `json:"nodes"`
		Edges []models.WorkflowEdge `json:"edges"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	encryptedNodes := encryptNodes(body.Nodes, d.EncryptionKey, existing.Nodes)
	graph := models.WorkflowGraph{Nodes: encryptedNodes, Edges: body.Edges}
	wf, err := d.Store.UpdateWorkflow(r.Context(), id, body.Name, graph)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	wf.Nodes = maskNodes(wf.Nodes)
	respond.JSON(w, http.StatusOK, wf)
}

func (d *Deps) DeleteWorkflow(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	userID, _ := r.Context().Value(CtxUserID).(string)
	existing, err := d.Store.GetWorkflow(r.Context(), id)
	if err != nil || existing.UserID != userID {
		respond.Error(w, http.StatusNotFound, "workflow not found")
		return
	}
	if err := d.Store.DeleteWorkflow(r.Context(), id); err != nil {
		respond.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListVariables returns a workflow's persisted key/value state.
func (d *Deps) ListVariables(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ctx := r.Context()
	userID, _ := ctx.Value(CtxUserID).(string)

	wf, err := d.Store.GetWorkflow(ctx, id)
	if err != nil || wf.UserID != userID {
		respond.Error(w, http.StatusNotFound, "workflow not found")
		return
	}
	vars, err := d.Store.GetWorkflowVariables(ctx, id)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"variables": vars})
}

// SetVariable writes one variable by hand — for seeding a cursor before
// the first run, or correcting one after a failure.
func (d *Deps) SetVariable(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	key := chi.URLParam(r, "key")
	ctx := r.Context()
	userID, _ := ctx.Value(CtxUserID).(string)

	wf, err := d.Store.GetWorkflow(ctx, id)
	if err != nil || wf.UserID != userID {
		respond.Error(w, http.StatusNotFound, "workflow not found")
		return
	}

	var body struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Value) == 0 {
		respond.Error(w, http.StatusBadRequest, `body must be {"value": <json>}`)
		return
	}

	if err := d.Store.SetWorkflowVariable(ctx, id, key, body.Value); err != nil {
		// Quota and size errors are the caller's fault, not the server's.
		if errors.Is(err, db.ErrVariableQuotaExceeded) || errors.Is(err, db.ErrVariableTooLarge) {
			respond.Error(w, http.StatusBadRequest, err.Error())
			return
		}
		respond.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"key": key})
}

// DeleteVariable removes one variable.
func (d *Deps) DeleteVariable(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	key := chi.URLParam(r, "key")
	ctx := r.Context()
	userID, _ := ctx.Value(CtxUserID).(string)

	wf, err := d.Store.GetWorkflow(ctx, id)
	if err != nil || wf.UserID != userID {
		respond.Error(w, http.StatusNotFound, "workflow not found")
		return
	}
	if err := d.Store.DeleteWorkflowVariable(ctx, id, key); err != nil {
		respond.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
