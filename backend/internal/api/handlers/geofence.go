package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/agentmesh/backend/internal/db"
	"github.com/agentmesh/backend/internal/geo"
	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/respond"
)

// Geofence trigger: a phone crossing the edge of a zone starts a run, the same
// way manual, webhook and cron triggers do.
//
// The interesting part is not the geometry (internal/geo) but deciding what
// counts as an EVENT. A background-location client pushes a fix every 30-60s
// while a session is active, and flushes whatever it queued while offline in a
// burst on reconnect. So the same boundary can be reported many times, out of
// order, and long after the fact. Store.RecordGeofenceFix carries that rule --
// only a change between two known states, never a replay -- and this file is
// the transport around it.

// Zones smaller than this are below what consumer GPS can resolve: a fence of
// a few metres would flap between inside and outside while the phone sits
// still on a table, firing a run each time. Rejecting it is kinder than
// letting someone build a trigger that behaves like a random number generator.
const minGeofenceRadiusM = 50

// A zone larger than this is not a geofence, it is a region, and the crossing
// would be so rare and so imprecise that it is almost certainly a mistake --
// a misplaced decimal point in the radius.
const maxGeofenceRadiusM = 100_000

// A client pushing more often than this is not telling us anything new: the
// background-location plugin's own reporting floor is well above it.
const minPingInterval = 5 * time.Second

// pingLimiter throttles location ingest per workflow.
//
// Scope, stated honestly: this is IN-PROCESS and therefore PER-REPLICA, so it
// protects the database from a stuck or malicious client; it is not a business
// rule. How often a workflow may actually be TRIGGERED is
// Store.CreateRunWithCooldown, which is DB-backed and correct across replicas.
// Worth not confusing the two: a run must never double-fire, whereas a ping
// accepted twice costs one indexed UPDATE and changes nothing.
// pingEntryTTL bounds how long a workflow's entry survives without another
// ping. Well above minPingInterval so an active client never gets swept out
// from under itself; its purpose is only to stop the map growing forever with
// one permanent entry per workflow that has ever pinged.
const pingEntryTTL = 10 * time.Minute

// How often allow() bothers scanning the whole map for expired entries.
// Amortizes the O(n) sweep across many calls instead of paying it, and
// holding the lock for it, on every single accepted ping.
const pingSweepInterval = time.Minute

type pingLimiter struct {
	mu        sync.Mutex
	last      map[string]time.Time
	lastSweep time.Time
}

func (p *pingLimiter) allow(workflowID string, now time.Time) bool {
	p.mu.Lock()
	if p.last == nil {
		p.last = make(map[string]time.Time)
	}
	if prev, ok := p.last[workflowID]; ok && now.Sub(prev) < minPingInterval {
		p.mu.Unlock()
		return false
	}
	p.last[workflowID] = now
	needsSweep := now.Sub(p.lastSweep) > pingSweepInterval
	if needsSweep {
		p.lastSweep = now
	}
	p.mu.Unlock()

	// Run off the request path, not inline: the O(n) sweep still needs the
	// same mutex for correctness, but the caller whose ping happened to land
	// on the sweep minute should not have its own accept/reject decision --
	// already made above -- wait behind a full map scan before its HTTP
	// response can proceed.
	//
	// No context.Context here, unlike this repo's looping background
	// goroutines (e.g. StartLeaseReaper's ticker loop): this one is bounded
	// and one-shot, not a forever-loop that needs a shutdown signal to stop.
	// It iterates an in-memory map bounded by "workflows pinged in the last
	// 10 minutes" and returns; there is no I/O, no blocking call, and nothing
	// to leak if the process exits while it is mid-sweep.
	if needsSweep {
		go p.sweep(now)
	}
	return true
}

func (p *pingLimiter) sweep(now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, t := range p.last {
		if now.Sub(t) > pingEntryTTL {
			delete(p.last, id)
		}
	}
}

var locationPings pingLimiter

// geofenceOwnedWorkflow resolves the workflow and confirms the caller owns it.
// Every failure answers 404 rather than 403, matching the rest of this package:
// a caller who does not own a workflow should not be able to learn it exists.
func (d *Deps) geofenceOwnedWorkflow(w http.ResponseWriter, r *http.Request) (models.Workflow, bool) {
	ctx := r.Context()
	userID, _ := ctx.Value(CtxUserID).(string)
	wf, err := d.Store.GetWorkflow(ctx, chi.URLParam(r, "id"))
	if err != nil || wf.UserID != userID {
		respond.Error(w, http.StatusNotFound, "workflow not found")
		return models.Workflow{}, false
	}
	return wf, true
}

// SetGeofence configures the zone that triggers this workflow.
func (d *Deps) SetGeofence(w http.ResponseWriter, r *http.Request) {
	wf, ok := d.geofenceOwnedWorkflow(w, r)
	if !ok {
		return
	}
	// Same reasoning as SetSchedule: a trigger saved on a draft would sit
	// there looking configured and never fire, with nothing telling the
	// caller why.
	if wf.Status != models.WorkflowStatusDeployed {
		respond.Error(w, http.StatusConflict, "deploy this workflow before adding a geofence trigger")
		return
	}

	var body struct {
		Lat     *float64 `json:"lat"`
		Lng     *float64 `json:"lng"`
		RadiusM *float64 `json:"radiusM"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// Pointers, so a missing field is distinguishable from a zero one: lat/lng
	// of 0,0 is a real place, and defaulting a missing radius to 0 would store
	// a fence nothing can ever be inside.
	if body.Lat == nil || body.Lng == nil || body.RadiusM == nil {
		respond.Error(w, http.StatusBadRequest, "lat, lng and radiusM are all required")
		return
	}
	if !geo.ValidCoord(*body.Lat, *body.Lng) {
		respond.Error(w, http.StatusBadRequest, "lat must be within 90 degrees and lng within 180")
		return
	}
	if math.IsNaN(*body.RadiusM) || *body.RadiusM < minGeofenceRadiusM || *body.RadiusM > maxGeofenceRadiusM {
		respond.Error(w, http.StatusBadRequest,
			"radiusM must be between "+strconv.Itoa(minGeofenceRadiusM)+" and "+strconv.Itoa(maxGeofenceRadiusM))
		return
	}

	if err := d.Store.SetWorkflowGeofence(r.Context(), wf.ID, *body.Lat, *body.Lng, *body.RadiusM); err != nil {
		respond.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"lat": *body.Lat, "lng": *body.Lng, "radiusM": *body.RadiusM,
	})
}

// ClearGeofence removes the geofence trigger. Idempotent.
func (d *Deps) ClearGeofence(w http.ResponseWriter, r *http.Request) {
	wf, ok := d.geofenceOwnedWorkflow(w, r)
	if !ok {
		return
	}
	if err := d.Store.ClearWorkflowGeofence(r.Context(), wf.ID); err != nil {
		respond.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// LocationPing ingests one location fix from the device and fires a run if it
// crossed the boundary.
//
// Deliberately an AUTHENTICATED route, unlike the public webhook trigger. That
// one is opened up by a workflow's own webhook node carrying a per-workflow
// secret; a geofence has no such node and no such secret, and an
// unauthenticated location endpoint keyed only on a workflow id would let
// anyone who learned that id both fire someone's workflow and locate their
// fence by bisecting coordinates against the response.
func (d *Deps) LocationPing(w http.ResponseWriter, r *http.Request) {
	// Rate-limited on the raw path id, before the DB fetch below -- keyed by
	// workflow id string, not by anything ownership-derived, so this reveals
	// nothing about whether the id is valid or belongs to the caller. A phone
	// flushing a queued burst has every fix after the first within the 5s
	// window rejected here, before it can pay for a full GetWorkflow (which
	// also unmarshals the entire node/edge graph) and body decode only to be
	// dropped by the same check moments later.
	id := chi.URLParam(r, "id")
	if !locationPings.allow(id, time.Now()) {
		w.Header().Set("Retry-After", strconv.Itoa(int(minPingInterval.Seconds())))
		respond.Error(w, http.StatusTooManyRequests, "location fixes for this workflow are arriving too fast")
		return
	}

	wf, ok := d.geofenceOwnedWorkflow(w, r)
	if !ok {
		return
	}
	if wf.GeofenceLat == nil || wf.GeofenceLng == nil || wf.GeofenceRadiusM == nil {
		respond.Error(w, http.StatusConflict, "this workflow has no geofence configured")
		return
	}

	var body struct {
		Lat        *float64   `json:"lat"`
		Lng        *float64   `json:"lng"`
		AccuracyM  *float64   `json:"accuracyM"`
		RecordedAt *time.Time `json:"recordedAt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Lat == nil || body.Lng == nil {
		respond.Error(w, http.StatusBadRequest, "lat and lng are required")
		return
	}
	if !geo.ValidCoord(*body.Lat, *body.Lng) {
		respond.Error(w, http.StatusBadRequest, "lat must be within 90 degrees and lng within 180")
		return
	}

	// The DEVICE's timestamp orders these, not arrival time: a queue flushed
	// after a tunnel arrives newest-first as easily as oldest-first. Falling
	// back to now for a client that sends none keeps the endpoint usable; such
	// a client simply gets no replay protection.
	fixAt := time.Now().UTC()
	if body.RecordedAt != nil {
		fixAt = body.RecordedAt.UTC()
		// A fix from the future would poison the ordering permanently: every
		// subsequent real fix would look stale against it and be ignored.
		if fixAt.After(time.Now().UTC().Add(time.Minute)) {
			respond.Error(w, http.StatusBadRequest, "recordedAt is in the future")
			return
		}
	}

	inside := geo.Inside(*wf.GeofenceLat, *wf.GeofenceLng, *wf.GeofenceRadiusM, *body.Lat, *body.Lng)
	crossing, err := d.Store.RecordGeofenceFix(r.Context(), wf.ID, inside, fixAt)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err.Error())
		return
	}

	if !crossing.Fired {
		// 200, not 202: the fix was accepted and recorded, it simply was not
		// an event. A client flushing a queue needs to tell "handled" from
		// "rejected", and every one of these is a success.
		respond.JSON(w, http.StatusOK, map[string]any{
			"inside": inside, "triggered": false, "stale": crossing.Stale,
		})
		return
	}

	direction := "leave"
	if crossing.Entered {
		direction = "enter"
	}
	runID, err := d.startGeofenceRun(r, wf, direction, *body.Lat, *body.Lng, fixAt)
	if err != nil {
		var cooldownErr *db.ErrRunOnCooldown
		if errors.As(err, &cooldownErr) {
			retryAfter := int64(math.Ceil(cooldownErr.RetryAfter.Seconds()))
			w.Header().Set("Retry-After", strconv.FormatInt(retryAfter, 10))
			respond.Error(w, http.StatusTooManyRequests, "this workflow was triggered too recently")
			return
		}
		respond.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if runID == "" {
		// A run for this workflow is already in flight. The crossing is
		// recorded either way, so the next one still fires correctly.
		respond.JSON(w, http.StatusOK, map[string]any{
			"inside": inside, "triggered": false, "direction": direction,
			"skipped": "a run for this workflow is already in progress",
		})
		return
	}
	respond.JSON(w, http.StatusAccepted, map[string]any{
		"inside": inside, "triggered": true, "direction": direction, "runId": runID,
	})
}

// startGeofenceRun creates and starts the run for a crossing. Returns an empty
// id (and no error) when a run is already in flight for this workflow.
func (d *Deps) startGeofenceRun(
	r *http.Request, wf models.Workflow, direction string, lat, lng float64, fixAt time.Time,
) (string, error) {
	ctx := r.Context()

	// Mirrors the scheduler's overlap check rather than the manual trigger's.
	// A crossing is an automatic event like a cron tick, not a person asking
	// for this run now, so it must never supersede work already running.
	if running, err := d.Store.HasRunningRun(ctx, wf.ID); err != nil {
		return "", err
	} else if running {
		return "", nil
	}

	input, err := json.Marshal(map[string]any{
		"trigger": "geofence", "direction": direction,
		"lat": lat, "lng": lng, "recordedAt": fixAt,
	})
	if err != nil {
		return "", fmt.Errorf("marshal geofence run input: %w", err)
	}
	run, err := d.Store.CreateRunWithCooldown(ctx, wf.ID, "geofence", input, runTriggerCooldown)
	if err != nil {
		return "", err
	}

	wf.Nodes = DecryptNodes(wf.Nodes, d.EncryptionKey)
	// StartIfNotRunning, not Start: the HasRunningRun check above is
	// check-then-act, so a manual trigger could register in the window between
	// it and here. Start would cancel that run out from under the user,
	// possibly mid-payment. Same reasoning as the scheduler's.
	if !d.Engine.StartIfNotRunning(wf, run) {
		log.Printf("geofence: workflow %s registered a run between the overlap check and start; marking run %s failed", wf.ID, run.ID)
		if err := d.Store.FinishRun(ctx, run.ID, models.RunStatusFailed); err != nil {
			log.Printf("geofence: marking redundant run %s failed also failed: %v", run.ID, err)
		}
		return "", nil
	}
	return run.ID, nil
}
