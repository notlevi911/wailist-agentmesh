package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/agentmesh/backend/internal/db"
	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/respond"
)

// isReservedSystemWorkflowName reports whether name is one of the exact
// names GetOrCreateSystemWorkflow uses for a partner console's hidden row.
// is_system (models.Workflow) is what actually decides a workflow's
// identity now, so this reservation is belt-and-suspenders, not
// load-bearing on its own: it stops a user's rename from ever recreating
// the name-collision shape in the first place, rather than relying solely
// on is_system to survive it if it does.
func isReservedSystemWorkflowName(name string) bool {
	switch name {
	case tendrilConsoleWorkflowName, prismConsoleWorkflowName:
		return true
	default:
		return false
	}
}

// ListWorkflows excludes a partner console's hidden row (Tendril, Prism) via
// Store.ListWorkflows' own WHERE NOT is_system -- filtered at the query, not
// here, so a row that will never be shown doesn't get decrypted and
// aggregated into Runs/Spend for nothing. See is_system's doc comment
// (models.Workflow) for why that column, not a name match, is what decides.
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
	decrypted := decryptNodes(wf.Nodes, d.EncryptionKey)
	wf.Nodes = unmaskWebhookSecrets(maskNodes(wf.Nodes), decrypted)
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
	// is_system is the real identity guard (FindSystemWorkflow requires it,
	// so a rename alone can no longer forge a console). This check exists so
	// the collision shape can't arise at all: an ordinary workflow renamed to
	// exactly a console's name would otherwise sit there sharing that name
	// forever, one migration or code path away from mattering again. A
	// row that already IS the console is exempt -- its own name never goes
	// through user-facing rename UI, but there's no reason to block it here.
	if !existing.IsSystem && isReservedSystemWorkflowName(body.Name) {
		respond.Error(w, http.StatusBadRequest, "That name is reserved. Please choose another.")
		return
	}
	clampRetryFields(body.Nodes)
	encryptedNodes := encryptNodes(body.Nodes, d.EncryptionKey, existing.Nodes)
	encryptedNodes = ensureWebhookSecrets(encryptedNodes, d.EncryptionKey)
	graph := models.WorkflowGraph{Nodes: encryptedNodes, Edges: body.Edges}
	wf, err := d.Store.UpdateWorkflow(r.Context(), id, body.Name, graph)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	decrypted := decryptNodes(wf.Nodes, d.EncryptionKey)
	wf.Nodes = unmaskWebhookSecrets(maskNodes(wf.Nodes), decrypted)
	respond.JSON(w, http.StatusOK, wf)
}

// clampRetryFields bounds every node's MaxRetries/RetryBackoffMs to
// models.MaxNodeRetries/MaxNodeRetryBackoffMs in place, so a saved workflow
// can never configure a node to retry for hours against a third-party
// endpoint -- see those constants' doc comment. Negative values are floored
// to 0 rather than left to produce a nonsensical negative retry count/delay.
func clampRetryFields(nodes []models.WorkflowNode) {
	for i := range nodes {
		if nodes[i].MaxRetries < 0 {
			nodes[i].MaxRetries = 0
		} else if nodes[i].MaxRetries > models.MaxNodeRetries {
			nodes[i].MaxRetries = models.MaxNodeRetries
		}
		if nodes[i].RetryBackoffMs < 0 {
			nodes[i].RetryBackoffMs = 0
		} else if nodes[i].RetryBackoffMs > models.MaxNodeRetryBackoffMs {
			nodes[i].RetryBackoffMs = models.MaxNodeRetryBackoffMs
		}
	}
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
		// tendril_leases.workflow_id is ON DELETE RESTRICT, not CASCADE
		// (migration 000018) -- a workflow with lease history (active or
		// released) can't be silently destroyed along with the only copy
		// of an active lease's encrypted credentials. Surface that as a
		// clear 409, not the raw Postgres constraint-violation message.
		// Both tendril_leases FKs are RESTRICT: workflow_id fires when the
		// workflow itself is deleted, run_id when the cascade reaches its runs.
		if strings.Contains(err.Error(), "tendril_leases_workflow_id_fkey") ||
			strings.Contains(err.Error(), "tendril_leases_run_id_fkey") {
			respond.Error(w, http.StatusConflict, "this workflow has Tendril machine lease history and can't be deleted")
			return
		}
		// Anything else is ours to fix, not the user's to read: a raw driver
		// message (constraint names, SQLSTATE) was being rendered straight into
		// the workflows list.
		log.Printf("delete workflow %s: %v", id, err)
		respond.Error(w, http.StatusInternalServerError, "could not delete this workflow")
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

// BuildWorkflow edits the workflow graph via chat: a platform-key Gemini
// meta-agent calls add_node/update_node/remove_node/add_edge/remove_edge
// tools against the current graph, then the result is persisted through the
// same encrypt path UpdateWorkflow uses. The graph handed to the model is
// redacted first (redactNodesForBuildAgent) so neither an untouched node's
// real API key/secret value nor an uploaded file's raw bytes are ever sent
// to Gemini -- only the "__enc__" sentinel encryptField already knows how to
// preserve on save, and file metadata with the content stripped.
func (d *Deps) BuildWorkflow(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	userID, _ := r.Context().Value(CtxUserID).(string)
	existing, err := d.Store.GetWorkflow(r.Context(), id)
	if err != nil || existing.UserID != userID {
		respond.Error(w, http.StatusNotFound, "workflow not found")
		return
	}
	var body struct {
		Message string `json:"message"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if strings.TrimSpace(body.Message) == "" {
		respond.Error(w, http.StatusBadRequest, "message required")
		return
	}
	if d.PlatformGeminiAPIKey == "" {
		respond.Error(w, http.StatusServiceUnavailable, "workflow builder is not configured")
		return
	}

	maskedGraph := models.WorkflowGraph{Nodes: redactNodesForBuildAgent(existing.Nodes), Edges: existing.Edges}
	result, err := nodes.BuildGraph(r.Context(), d.PlatformGeminiAPIKey, body.Message, maskedGraph)
	if err != nil {
		// The upstream text (a raw Gemini error body, keys and all) lands
		// straight in the user's chat bubble if forwarded -- same anti-pattern
		// DeleteWorkflow was already fixed for. Log it, say something plain.
		log.Printf("build workflow %s: %v", id, err)
		respond.Error(w, http.StatusBadGateway, "the workflow builder could not complete this request")
		return
	}

	// Not currently exploitable -- BuildGraph's field-setter only allows
	// string-typed node fields, so MaxRetries/RetryBackoffMs stay at their
	// zero value on this path today -- but this save must go through the
	// same clamp UpdateWorkflow's own HTTP handler applies, so a future
	// change that lets the chat-driven builder set these fields (or a raw
	// JSON passthrough bug) can't silently bypass the retry-storm cap.
	clampRetryFields(result.Graph.Nodes)
	encryptedNodes := encryptNodes(result.Graph.Nodes, d.EncryptionKey, existing.Nodes)
	encryptedNodes = ensureWebhookSecrets(encryptedNodes, d.EncryptionKey)
	graph := models.WorkflowGraph{Nodes: encryptedNodes, Edges: result.Graph.Edges}
	wf, err := d.Store.UpdateWorkflow(r.Context(), id, existing.Name, graph)
	if err != nil {
		log.Printf("build workflow %s: save: %v", id, err)
		respond.Error(w, http.StatusInternalServerError, "could not save the updated workflow")
		return
	}
	decrypted := decryptNodes(wf.Nodes, d.EncryptionKey)
	wf.Nodes = unmaskWebhookSecrets(maskNodes(wf.Nodes), decrypted)
	respond.JSON(w, http.StatusOK, map[string]any{"reply": result.Reply, "workflow": wf})
}
