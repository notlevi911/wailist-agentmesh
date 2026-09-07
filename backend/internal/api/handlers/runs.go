package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/agentmesh/backend/internal/db"
	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/respond"
)

// runTriggerCooldown is the minimum gap between two run starts for the same
// workflow, across BOTH TriggerRun (authenticated) and PublicTrigger
// (unauthenticated webhook) -- the latter is the real target, since it's
// reachable by anyone who has the URL with no rate limiting of its own
// otherwise, but it's enforced on the shared startRun path so a bot hitting
// either endpoint (or both, alternating) is caught the same way. A flat
// constant rather than a per-workflow setting: this is a blunt deterrent
// against naive burst-triggering, not a real per-customer rate limit --
// legitimate rapid re-runs are rare enough that 5s costs nothing.
const runTriggerCooldown = 5 * time.Second

func (d *Deps) TriggerRun(w http.ResponseWriter, r *http.Request) {
	workflowID := chi.URLParam(r, "id")
	d.startRun(w, r, workflowID, "manual", true)
}

func (d *Deps) PublicTrigger(w http.ResponseWriter, r *http.Request) {
	workflowID := chi.URLParam(r, "workflowId")
	d.startRun(w, r, workflowID, "webhook", false)
}

func (d *Deps) startRun(w http.ResponseWriter, r *http.Request, workflowID, triggeredBy string, checkOwner bool) {
	ctx := r.Context()

	wf, err := d.Store.GetWorkflow(ctx, workflowID)
	if err != nil {
		respond.Error(w, http.StatusNotFound, "workflow not found")
		return
	}
	// Decrypted here, before the public-path branch below, so it can read
	// the webhook node's secret -- callers of startRun with checkOwner=true
	// still get the same decrypted nodes handed to the engine at the bottom
	// as before, just decrypted earlier.
	wf.Nodes = decryptNodes(wf.Nodes, d.EncryptionKey)
	if checkOwner {
		userID, _ := ctx.Value(CtxUserID).(string)
		if wf.UserID != userID {
			respond.Error(w, http.StatusNotFound, "workflow not found")
			return
		}
	} else {
		// Public webhook path: only a deployed workflow whose trigger node is
		// SPECIFICALLY template "webhook" can be invoked without authentication --
		// checking merely NodeTypeTrigger let a "manual" or "chat" trigger
		// workflow be fired the same way, unauthenticated, contradicting its
		// own trigger's intended access model. Return 404 on all failures
		// (missing workflow, wrong trigger type, bad/missing secret) to avoid
		// leaking whether a workflow ID exists or what its trigger type is.
		if wf.Status != models.WorkflowStatusDeployed {
			respond.Error(w, http.StatusNotFound, "workflow not found")
			return
		}
		var webhookNode *models.WorkflowNode
		for i, n := range wf.Nodes {
			if n.Type == models.NodeTypeTrigger && n.Template == "webhook" {
				webhookNode = &wf.Nodes[i]
				break
			}
		}
		if webhookNode == nil {
			respond.Error(w, http.StatusNotFound, "workflow not found")
			return
		}
		// The webhook node always carries a secret once saved (UpdateWorkflow
		// generates one for any webhook trigger node that doesn't already have
		// one) -- an empty secret here means the workflow predates that and
		// hasn't been re-saved since, so treat it the same as a wrong one
		// rather than letting it through unauthenticated.
		secret := webhookNode.Secrets["webhookSecret"]
		given := r.Header.Get("X-Webhook-Secret")
		if secret == "" || subtle.ConstantTimeCompare([]byte(given), []byte(secret)) != 1 {
			respond.Error(w, http.StatusNotFound, "workflow not found")
			return
		}
	}

	var inputBody any
	json.NewDecoder(r.Body).Decode(&inputBody)
	inputJSON, _ := json.Marshal(inputBody)

	// CreateRunWithCooldown enforces runTriggerCooldown and creates the run
	// atomically in one DB transaction -- see its own doc comment for why
	// that matters (a failed insert must never leave a phantom cooldown
	// behind) and why this is DB-backed rather than an in-process
	// check-then-write (bounded storage, correct across replicas).
	//
	// The cooldown check happens deep inside this call, not before it --
	// deliberately after the existence/ownership/deploy checks above, same
	// as before this refactor: doing it earlier would let an
	// unauthenticated caller on the PublicTrigger path distinguish
	// "workflow doesn't exist" (404) from "workflow exists but is on
	// cooldown" (429) for an ID they have no business confirming -- the
	// exact leak the deploy/trigger checks above already go out of their
	// way to avoid.
	run, err := d.Store.CreateRunWithCooldown(ctx, workflowID, triggeredBy, inputJSON, runTriggerCooldown)
	if err != nil {
		var cooldownErr *db.ErrRunOnCooldown
		if errors.As(err, &cooldownErr) {
			// math.Ceil, not %.0f (which rounds to nearest): the caller must
			// never be told to wait less than the real remaining cooldown, or
			// a client that retries exactly as told lands inside the
			// still-active window and gets hit with a second, unexpected 429.
			retryAfterSecs := int64(math.Ceil(cooldownErr.RetryAfter.Seconds()))
			w.Header().Set("Retry-After", strconv.FormatInt(retryAfterSecs, 10))
			respond.Error(w, http.StatusTooManyRequests,
				fmt.Sprintf("this workflow was triggered too recently — wait %ds and try again", retryAfterSecs))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err.Error())
		return
	}

	d.Broker.Create(run.ID)
	d.Engine.Start(wf, run)

	respond.JSON(w, http.StatusAccepted, map[string]string{"runId": run.ID})
}

func (d *Deps) StopWorkflow(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ctx := r.Context()
	userID, _ := ctx.Value(CtxUserID).(string)

	wf, err := d.Store.GetWorkflow(ctx, id)
	if err != nil || wf.UserID != userID {
		respond.Error(w, http.StatusNotFound, "workflow not found")
		return
	}

	d.Engine.Stop(id)
	w.WriteHeader(http.StatusNoContent)
}

func (d *Deps) GetRun(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "runId")
	ctx := r.Context()
	userID, _ := ctx.Value(CtxUserID).(string)

	run, err := d.Store.GetRun(ctx, runID)
	if err != nil {
		respond.Error(w, http.StatusNotFound, "run not found")
		return
	}
	wf, err := d.Store.GetWorkflow(ctx, run.WorkflowID)
	if err != nil || wf.UserID != userID {
		respond.Error(w, http.StatusNotFound, "run not found")
		return
	}
	logs, err := d.Store.GetRunLogs(ctx, runID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if logs == nil {
		logs = []models.RunLog{}
	}
	deadLetters, err := d.Store.GetDeadLetterRuns(ctx, runID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if deadLetters == nil {
		deadLetters = []models.DeadLetterRun{}
	}
	respond.JSON(w, http.StatusOK, map[string]any{"run": run, "logs": logs, "deadLetters": deadLetters})
}

// ResumeRun continues a run that ended in "failed" or "stopped": nodes
// already logged as success are skipped, everything else re-executes. See
// engine.Runner.Resume for the skip contract.
//
// ?force=true is required when the run has a payment-risk dead-letter row
// (see models.DeadLetterRun.PaymentRisk) -- checked here for a synchronous
// 409 instead of the caller only finding out after the async resume fails,
// and again inside Runner.Resume itself as the actual enforcement.
//
// The run.Status == running check below is a cheap, non-authoritative
// fast path for the common case -- the real, race-safe admission decision
// is StartResume's synchronous MarkRunRunning claim. Two concurrent
// requests can both pass this read (neither has claimed yet); only one of
// them will get claimed=true back from StartResume, and the loser gets a
// 409 here without ever having started or cancelled anything.
func (d *Deps) ResumeRun(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "runId")
	ctx := r.Context()
	userID, _ := ctx.Value(CtxUserID).(string)

	run, err := d.Store.GetRun(ctx, runID)
	if err != nil {
		respond.Error(w, http.StatusNotFound, "run not found")
		return
	}
	// Ownership checked before anything about the run's state is revealed --
	// a non-owner (or a caller enumerating run IDs) must get the same
	// generic 404 regardless of whether the run exists, is running, or has
	// dead letters, matching GetRun's own check-first order below.
	wf, err := d.Store.GetWorkflow(ctx, run.WorkflowID)
	if err != nil || wf.UserID != userID {
		respond.Error(w, http.StatusNotFound, "run not found")
		return
	}
	if run.Status == models.RunStatusRunning {
		respond.Error(w, http.StatusConflict, "run is already in progress")
		return
	}

	force := r.URL.Query().Get("force") == "true"
	if !force {
		deadLetters, err := d.Store.GetDeadLetterRuns(ctx, runID)
		if err != nil {
			respond.Error(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, dl := range deadLetters {
			if dl.PaymentRisk {
				respond.Error(w, http.StatusConflict, "run has a payment-risk failure (node "+dl.NodeID+") -- resume with ?force=true to accept the risk of a double charge")
				return
			}
		}
		// A fast, synchronous version of Runner.Resume's own "node stuck
		// mid-execution" check (see its doc comment there for why this is
		// refused rather than guessed) -- gives the caller an immediate
		// 409 instead of only finding out after StartResume's async
		// goroutine already refused it.
		states, err := d.Store.GetLatestNodeStates(ctx, runID)
		if err != nil {
			respond.Error(w, http.StatusInternalServerError, err.Error())
			return
		}
		for nodeID, l := range states {
			if l.Status == models.LogStatusRunning {
				respond.Error(w, http.StatusConflict, "node "+nodeID+" was mid-execution when this run crashed -- its side effect's fate is unknown; resume with ?force=true to accept the risk of repeating it")
				return
			}
		}
	}

	wf.Nodes = decryptNodes(wf.Nodes, d.EncryptionKey)
	// StartResume creates the broker hub itself, synchronously, only once
	// its admission claim actually succeeds -- see its doc comment. Not
	// done here: two concurrent resume requests for the same run.ID both
	// calling Broker.Create before either claim resolves could overwrite
	// each other's hub and orphan a client already subscribed via GET
	// /runs/:id/stream, regardless of which request wins the claim.
	claimed, err := d.Engine.StartResume(ctx, wf, run, force)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !claimed {
		respond.Error(w, http.StatusConflict, "run is already in progress or already finished")
		return
	}

	respond.JSON(w, http.StatusAccepted, map[string]string{"runId": run.ID})
}

func (d *Deps) StreamRun(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "runId")
	ctx := r.Context()
	userID, _ := ctx.Value(CtxUserID).(string)

	run, err := d.Store.GetRun(ctx, runID)
	if err != nil {
		respond.Error(w, http.StatusNotFound, "run not found")
		return
	}
	wf, err := d.Store.GetWorkflow(ctx, run.WorkflowID)
	if err != nil || wf.UserID != userID {
		respond.Error(w, http.StatusNotFound, "run not found")
		return
	}
	_ = wf

	flusher, ok := w.(http.Flusher)
	if !ok {
		respond.Error(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, unsub := d.Broker.Subscribe(runID)
	defer unsub()

	done := d.Broker.Done(runID)

	for {
		select {
		case ev, open := <-ch:
			if !open {
				return
			}
			b, _ := json.Marshal(ev)
			fmt.Fprintf(w, "event: log\ndata: %s\n\n", string(b))
			flusher.Flush()
		case <-done:
			fmt.Fprintf(w, "event: done\ndata: {}\n\n")
			flusher.Flush()
			return
		case <-r.Context().Done():
			return
		// Keepalive well under the ~35s at which these connections were
		// observed dying with no data flowing, so an idle stream keeps
		// proving itself alive instead of looking hung to the browser or any
		// intermediary. A run can legitimately be silent far longer than
		// this: a single agent step takes ~70s.
		case <-time.After(10 * time.Second):
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}
