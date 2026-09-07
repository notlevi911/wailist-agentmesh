package db

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/agentmesh/backend/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrPasswordAccountExists is returned when an OAuth login resolves to an email
// that already belongs to a password account. We refuse to silently link them,
// since our password signup does not verify email ownership — auto-linking would
// allow a pre-registered password account to capture a victim's OAuth identity.
var ErrPasswordAccountExists = errors.New("password account exists for email")

type Store struct {
	pool *pgxpool.Pool
	// coupons is the redeemable coupon catalog (code -> USD micros), loaded
	// from configuration at startup. See SetCouponCatalog.
	coupons map[string]int64
}

func (s *Store) Close() {
	s.pool.Close()
}

// --- Workflow methods ---

// workflowColumns is the single source of truth for every query in this
// file that reads a full `workflows` row -- CreateWorkflow, GetWorkflow,
// ListWorkflows, UpdateWorkflow, ClaimDueSchedules, and FindSystemWorkflow
// all select and scan this exact list via scanWorkflowRow below, instead of
// each hand-writing its own copy. That hand-writing is exactly what let
// FindSystemWorkflow silently fall behind when schedule_cron/
// schedule_next_run_at were added to the others in an earlier pass: five
// independent copies meant a new column had to be remembered at five call
// sites, and one was missed. A future column now only needs to be added
// here and in scanWorkflowRow's Scan call, once, for every caller to pick
// it up automatically.
const workflowColumns = `id, user_id, name, status, graph, deployed_at, run_endpoint, created_at, updated_at, schedule_cron, schedule_next_run_at, geofence_lat, geofence_lng, geofence_radius_m, geofence_inside, geofence_last_fix_at, is_system`

// rowScanner is satisfied by both pgx.Row (QueryRow) and *pgx.Rows
// (Query's per-row iteration) -- scanWorkflowRow works with either, so a
// single-row lookup and a multi-row list can share the same scan logic.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanWorkflowRow scans one row shaped like workflowColumns into a
// models.Workflow, handling the nullable run_endpoint column and the
// graph JSON unmarshal every caller needs identically.
func scanWorkflowRow(row rowScanner) (models.Workflow, error) {
	var w models.Workflow
	var graphJSON []byte
	var runEndpoint *string
	if err := row.Scan(
		&w.ID, &w.UserID, &w.Name, &w.Status, &graphJSON,
		&w.DeployedAt, &runEndpoint, &w.CreatedAt, &w.UpdatedAt,
		&w.ScheduleCron, &w.ScheduleNextRunAt,
		&w.GeofenceLat, &w.GeofenceLng, &w.GeofenceRadiusM,
		&w.GeofenceInside, &w.GeofenceLastFixAt, &w.IsSystem,
	); err != nil {
		return models.Workflow{}, err
	}
	if runEndpoint != nil {
		w.RunEndpoint = *runEndpoint
	}
	unmarshalGraph(graphJSON, &w)
	return w, nil
}

func (s *Store) CreateWorkflow(ctx context.Context, name, userID string) (models.Workflow, error) {
	return s.createWorkflowRow(ctx, name, userID, false)
}

// createWorkflowRow is CreateWorkflow's and GetOrCreateSystemWorkflow's
// shared INSERT, split out so isSystem can never be set by anything but
// GetOrCreateSystemWorkflow itself -- CreateWorkflow (the ordinary
// POST /workflows path) always passes false, hardcoded at its one call site
// rather than threaded through from a request body a user could set.
func (s *Store) createWorkflowRow(ctx context.Context, name, userID string, isSystem bool) (models.Workflow, error) {
	id := uuid.New().String()
	emptyGraph := `{"nodes":[],"edges":[]}`
	row := s.pool.QueryRow(ctx, `
		INSERT INTO workflows (id, user_id, name, status, graph, is_system)
		VALUES ($1, $2, $3, 'draft', $4::jsonb, $5)
		RETURNING `+workflowColumns+`
	`, id, userID, name, emptyGraph, isSystem)
	return scanWorkflowRow(row)
}

// FindSystemWorkflow is GetOrCreateSystemWorkflow's read-only half: looks
// up this user's workflow row with the given name WITHOUT creating one if
// it's missing. found is false, with a zero Workflow, when there's nothing
// to find yet -- that is a normal, expected outcome for a user who has
// never triggered the system workflow's creation, not an error condition.
//
// Exists specifically for callers that need to know "is THIS id the system
// workflow" (e.g. WorkflowRoute deciding whether to render the Tendril
// console instead of the canvas for a given workflowId) without the side
// effect of silently creating a hidden row the moment they check -- every
// workflow-page visit calling GetOrCreateSystemWorkflow instead would mint
// an empty "Tendril Console" row for every user who has never touched
// Tendril, the instant they open ANY of their own workflows.
// AND is_system = true is load-bearing, not redundant with the name match:
// without it, a user renaming their OWN workflow to this exact name (there
// is no name validation on UpdateWorkflow) would resolve here as THE system
// workflow -- oldest match wins -- silently swapping their real workflow for
// the console from then on. is_system is a column no rename can touch, so
// only a row this store itself created via createWorkflowRow(isSystem=true)
// can ever match.
func (s *Store) FindSystemWorkflow(ctx context.Context, userID, name string) (w models.Workflow, found bool, err error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+workflowColumns+`
		FROM workflows WHERE user_id = $1 AND name = $2 AND is_system = true ORDER BY created_at ASC LIMIT 1
	`, userID, name)
	if err != nil {
		return models.Workflow{}, false, err
	}
	for rows.Next() {
		if w, err = scanWorkflowRow(rows); err != nil {
			rows.Close()
			return models.Workflow{}, false, err
		}
		found = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return models.Workflow{}, false, err
	}
	return w, found, nil
}

// GetOrCreateSystemWorkflow returns this user's workflow row with the given
// name, creating it if it doesn't exist yet. Backs direct-action UIs (the
// Tendril console) that never show a canvas but still need a real
// workflow_id/run_id pair for the engine's node executors and the FK
// constraints on rows like tendril_leases. Use FindSystemWorkflow instead
// when a missing row should mean "not found", not "create one now".
func (s *Store) GetOrCreateSystemWorkflow(ctx context.Context, userID, name string) (models.Workflow, error) {
	w, found, err := s.FindSystemWorkflow(ctx, userID, name)
	if err != nil {
		return models.Workflow{}, err
	}
	if found {
		return w, nil
	}
	return s.createWorkflowRow(ctx, name, userID, true)
}

func (s *Store) GetWorkflow(ctx context.Context, id string) (models.Workflow, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+workflowColumns+` FROM workflows WHERE id = $1`, id)
	return scanWorkflowRow(row)
}

// SetWorkflowSchedule enables (or updates) this workflow's cron schedule.
// nextRunAt is caller-computed (scheduler.nextCronRun) rather than derived
// here, so this method has no cron-parsing dependency of its own.
func (s *Store) SetWorkflowSchedule(ctx context.Context, workflowID, cronExpr string, nextRunAt time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE workflows SET schedule_cron=$2, schedule_next_run_at=$3 WHERE id=$1
	`, workflowID, cronExpr, nextRunAt)
	return err
}

// ClearWorkflowSchedule disables scheduling for a workflow. Idempotent --
// clearing an already-unscheduled workflow is a no-op, not an error.
func (s *Store) ClearWorkflowSchedule(ctx context.Context, workflowID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE workflows SET schedule_cron=NULL, schedule_next_run_at=NULL WHERE id=$1
	`, workflowID)
	return err
}

// SetWorkflowGeofence configures the zone. All three values move together --
// a half-set zone cannot be evaluated -- and the recorded state is reset so a
// moved fence does not inherit an inside/outside answer that was true of the
// OLD location. After this the next fix re-establishes the baseline silently.
func (s *Store) SetWorkflowGeofence(ctx context.Context, workflowID string, lat, lng, radiusM float64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE workflows
		   SET geofence_lat=$2, geofence_lng=$3, geofence_radius_m=$4,
		       geofence_inside=NULL, geofence_last_fix_at=NULL
		 WHERE id=$1
	`, workflowID, lat, lng, radiusM)
	return err
}

// ClearWorkflowGeofence disables the geofence trigger. Idempotent, matching
// ClearWorkflowSchedule: clearing an unfenced workflow is a no-op.
func (s *Store) ClearWorkflowGeofence(ctx context.Context, workflowID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE workflows
		   SET geofence_lat=NULL, geofence_lng=NULL, geofence_radius_m=NULL,
		       geofence_inside=NULL, geofence_last_fix_at=NULL
		 WHERE id=$1
	`, workflowID)
	return err
}

// GeofenceCrossing is what RecordGeofenceFix decided about one location fix.
type GeofenceCrossing struct {
	// Fired is true only for an actual transition between two KNOWN states.
	Fired bool
	// Entered distinguishes the direction of a fired crossing.
	Entered bool
	// Stale is true when the fix was older than the last one already acted
	// on, so it was ignored entirely. Reported rather than silently swallowed
	// because a client flushing a queue deserves to know its ping was a
	// replay, not a failure.
	Stale bool
}

// RecordGeofenceFix records one location fix and reports whether it crossed
// the boundary.
//
// The whole point is that this is ATOMIC. The Android client pushes a fix
// every 30-60s while a session is active and flushes a queued burst after
// reconnecting, so two pings for the same workflow can easily be in flight at
// once. Read-compare-write outside a transaction would let both observe the
// same prior state and both fire, double-charging the user for one crossing.
// SELECT ... FOR UPDATE serialises them, exactly as ClaimDueSchedules does for
// the cron path.
//
// Three things are deliberately NOT a crossing:
//   - the first fix ever (geofence_inside IS NULL) -- it establishes the
//     baseline, because an unknown position is not the same as an outside one;
//   - a fix at the same state as the last one (the common case: parked inside
//     the zone, pinging every minute);
//   - a fix older than the last one acted on -- a replayed queue must not
//     re-fire a crossing that has already been handled.
func (s *Store) RecordGeofenceFix(
	ctx context.Context, workflowID string, inside bool, fixAt time.Time,
) (GeofenceCrossing, error) {
	// Postgres TIMESTAMPTZ stores microseconds; Go's time.Time carries
	// nanoseconds. Without truncating here, a value does not survive its own
	// round trip: what is written is compared on the next call against a
	// version of itself that has lost sub-microsecond digits, so
	// fixAt.After(prevAt) is TRUE for the very same instant and a resent fix
	// looks new rather than replayed. That defeats the replay guard this
	// column exists for, at exactly the boundary an offline flush hits --
	// clients resend identical timestamps, they do not invent fresh ones.
	fixAt = fixAt.UTC().Truncate(time.Microsecond)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return GeofenceCrossing{}, err
	}
	defer tx.Rollback(ctx)

	var prevInside *bool
	var prevAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT geofence_inside, geofence_last_fix_at
		  FROM workflows
		 WHERE id=$1 AND geofence_lat IS NOT NULL
		 FOR UPDATE
	`, workflowID).Scan(&prevInside, &prevAt)
	if err != nil {
		return GeofenceCrossing{}, err
	}

	// Out-of-order replay: leave the recorded state exactly as it is. Writing
	// an older fix over a newer one would move the baseline backwards and let
	// the NEXT live fix look like a crossing it is not.
	if prevAt != nil && !fixAt.After(*prevAt) {
		return GeofenceCrossing{Stale: true}, tx.Commit(ctx)
	}

	crossing := GeofenceCrossing{}
	if prevInside != nil && *prevInside != inside {
		crossing.Fired = true
		crossing.Entered = inside
	}

	if _, err := tx.Exec(ctx, `
		UPDATE workflows SET geofence_inside=$2, geofence_last_fix_at=$3 WHERE id=$1
	`, workflowID, inside, fixAt); err != nil {
		return GeofenceCrossing{}, err
	}
	return crossing, tx.Commit(ctx)
}

// maxScheduleCatchUpIterations bounds ClaimDueSchedules' per-workflow
// catch-up loop (walking forward one occurrence at a time from a
// schedule's own due time until the result clears `now`) -- generous
// enough to catch up even a once-a-minute cron across roughly two months
// of scheduler downtime in one call (a few hundred thousand pure in-memory
// iterations, no I/O per step), while still bounding against a
// non-advancing nextRun implementation looping forever. Exhausting it
// without clearing `now` isn't treated as an error: the row is left at
// whatever the loop last computed and simply gets caught up further on
// each subsequent tick, same as it always would across ticks anyway.
const maxScheduleCatchUpIterations = 500_000

// ClaimDueSchedules finds every deployed workflow whose schedule is due at
// or before `now`, advances each one's schedule_next_run_at (via nextRun,
// caller-supplied so this package carries no cron-parsing dependency) in
// the SAME transaction that claims it, and returns the claimed workflows.
//
// Uses SELECT ... FOR UPDATE SKIP LOCKED rather than the
// pg_advisory_xact_lock(hashtext(id)) pattern LockOAuthCredentialForRefresh
// uses: that pattern fits one caller-known ID, while this claims an
// unknown-in-advance BATCH of due rows in one pass. SKIP LOCKED is the
// standard Postgres job-queue idiom for exactly that -- a second replica's
// concurrent sweep simply skips any row this transaction already holds,
// rather than blocking on it, so the same tick can never fire twice.
func (s *Store) ClaimDueSchedules(ctx context.Context, now time.Time, nextRun func(cronExpr string, after time.Time) (time.Time, error)) ([]models.Workflow, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		SELECT `+workflowColumns+`
		FROM workflows
		WHERE status = 'deployed' AND schedule_cron IS NOT NULL AND schedule_next_run_at <= $1
		FOR UPDATE SKIP LOCKED
	`, now)
	if err != nil {
		return nil, err
	}
	var batch []models.Workflow
	for rows.Next() {
		w, err := scanWorkflowRow(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		batch = append(batch, w)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]models.Workflow, 0, len(batch))
	for _, w := range batch {

		// Anchored on the row's own due time (w.ScheduleNextRunAt), NOT
		// `now` (the sweep time) -- a scheduler that's down or delayed past
		// a due firing must resume counting from where the schedule
		// actually was, not silently skip every occurrence between the due
		// time and whenever the tick happens to land, which would shift
		// the cron's cadence off its original anchor (e.g. an hourly cron
		// due at 14:00, swept at 15:47, must keep landing on the :00
		// boundary -- nextRun(cron, 15:47) would instead jump to whatever
		// second `now` happens to be, off that anchor forever after).
		//
		// A single nextRun(cron, dueTime) call isn't enough on its own,
		// though: for a schedule whose own period is <= the scheduler's
		// poll interval (e.g. an every-minute cron on a 1-minute poll),
		// ordinary tick jitter can mean stepping forward exactly one
		// period from dueTime STILL lands at or before `now` -- not a
		// long-outage scenario, just routine timing, and left as a single
		// call this would leave the row due again for an immediate second
		// tick (confirmed live: TestTickFiresDueScheduleAndAdvancesIt's
		// second-tick-must-not-double-fire assertion broke on exactly
		// this). So this walks forward one occurrence at a time from the
		// original due time -- preserving the anchor -- until the result
		// is actually past `now`, catching up every missed occurrence to
		// the correct future time in this single call while still firing
		// only once this tick (matching the existing one-row-per-tick
		// contract: `due` returns each workflow at most once regardless of
		// how many occurrences it missed).
		next := *w.ScheduleNextRunAt
		var err error
		invalidExpr := false
		for i := 0; i < maxScheduleCatchUpIterations; i++ {
			next, err = nextRun(*w.ScheduleCron, next)
			if err != nil {
				invalidExpr = true
				break
			}
			if next.After(now) {
				break
			}
		}
		if invalidExpr {
			// A schedule that no longer parses (edited into an invalid
			// expression some other way) must not wedge the sweep forever
			// re-claiming the same broken row every tick -- clear it and
			// move on rather than failing the whole batch.
			if _, clearErr := tx.Exec(ctx, `UPDATE workflows SET schedule_cron=NULL, schedule_next_run_at=NULL WHERE id=$1`, w.ID); clearErr != nil {
				return nil, clearErr
			}
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE workflows SET schedule_next_run_at=$2 WHERE id=$1`, w.ID, next); err != nil {
			return nil, err
		}
		out = append(out, w)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// AND NOT is_system excludes a partner console's hidden row (Tendril,
// Prism): the user never authored it and there is nothing to open on a
// canvas, so it has no place in a list of things the user built. Filtered
// here, in the query, rather than after the fact in the handler -- a row
// excluded post-hoc still paid for its own decrypt and its own
// attachWorkflowStats aggregation for nothing; idx_workflows_user_visible
// (migration 000033) covers this exact predicate.
func (s *Store) ListWorkflows(ctx context.Context, userID string) ([]models.Workflow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+workflowColumns+`
		FROM workflows WHERE user_id = $1 AND NOT is_system ORDER BY updated_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var wfs []models.Workflow
	for rows.Next() {
		w, err := scanWorkflowRow(rows)
		if err != nil {
			return nil, err
		}
		wfs = append(wfs, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.attachWorkflowStats(ctx, userID, wfs); err != nil {
		// The Runs/Spend columns are decoration over `runs` and
		// `debit_ledger`. A problem aggregating them must not turn "show me
		// my workflows" into a 500 -- log it and return the list with those
		// fields left at their zero values.
		log.Printf("db: list workflows for user %s: stats aggregation failed: %v", userID, err)
	}
	return wfs, nil
}

// workflowStatsWindow is the trailing period the Runs/Spend columns on the
// workflows list summarise. The UI labels those columns "· 30d", so the two
// have to agree; changing one means changing the other.
const workflowStatsWindow = 30 * 24 * time.Hour

// attachWorkflowStats fills in the Runs and Spend fields that ListWorkflows'
// own SELECT cannot produce -- they are aggregates over `runs` and
// `debit_ledger`, not columns on `workflows`. Kept as a separate pass rather
// than a join so workflowColumns/scanWorkflowRow stay the single shared
// read path for a workflow row.
//
// Both queries are scoped by user_id, not just by the workflow ids in hand,
// so a row belonging to someone else can never be counted into this user's
// totals even if a workflow id were somehow reused.
func (s *Store) attachWorkflowStats(ctx context.Context, userID string, wfs []models.Workflow) error {
	if len(wfs) == 0 {
		return nil
	}
	since := time.Now().Add(-workflowStatsWindow)

	runCounts := map[string]int{}
	rows, err := s.pool.Query(ctx, `
		SELECT r.workflow_id, COUNT(*)
		FROM runs r
		JOIN workflows w ON w.id = r.workflow_id
		WHERE w.user_id = $1 AND r.started_at >= $2
		GROUP BY r.workflow_id
	`, userID, since)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			rows.Close()
			return err
		}
		runCounts[id] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	spendMicros := map[string]int64{}
	rows, err = s.pool.Query(ctx, `
		SELECT workflow_id, COALESCE(SUM(amount_usd_micros), 0)
		FROM debit_ledger
		WHERE user_id = $1 AND created_at >= $2
		GROUP BY workflow_id
	`, userID, since)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		var micros int64
		if err := rows.Scan(&id, &micros); err != nil {
			rows.Close()
			return err
		}
		spendMicros[id] = micros
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for i := range wfs {
		wfs[i].Runs = runCounts[wfs[i].ID]
		// Spend is a display string in USD. Left empty when nothing settled
		// so the UI renders its "no data" dash rather than a misleading
		// "$0.00" on a workflow that has simply never run.
		if micros, ok := spendMicros[wfs[i].ID]; ok && micros > 0 {
			wfs[i].Spend = fmt.Sprintf("%.2f", float64(micros)/1e6)
		}
	}
	return nil
}

func (s *Store) UpdateWorkflow(ctx context.Context, id, name string, graph models.WorkflowGraph) (models.Workflow, error) {
	graphJSON, _ := json.Marshal(graph)
	row := s.pool.QueryRow(ctx, `
		UPDATE workflows SET name=$2, graph=$3::jsonb, updated_at=NOW()
		WHERE id=$1
		RETURNING `+workflowColumns+`
	`, id, name, string(graphJSON))
	return scanWorkflowRow(row)
}

func (s *Store) DeleteWorkflow(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM workflows WHERE id=$1`, id)
	return err
}

func (s *Store) SetWorkflowDeployed(ctx context.Context, id, runEndpoint string, deployedAt time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE workflows SET status='deployed', run_endpoint=$2, deployed_at=$3, updated_at=NOW()
		WHERE id=$1
	`, id, runEndpoint, deployedAt)
	return err
}

func unmarshalGraph(data []byte, w *models.Workflow) {
	var g models.WorkflowGraph
	if err := json.Unmarshal(data, &g); err == nil {
		w.Nodes = g.Nodes
		w.Edges = g.Edges
	}
}

// --- Run methods ---

// rowQuerier is the subset of *pgxpool.Pool and pgx.Tx that insertRun
// needs, so it can run either as its own implicit single-statement
// transaction (CreateRun, via s.pool) or as part of a caller-managed one
// (CreateRunWithCooldown, via its tx).
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// insertRun does the actual runs INSERT+RETURNING+decode shared by
// CreateRun and CreateRunWithCooldown -- pulled out so the two can't drift
// (they used to duplicate this block verbatim) and so a fix here, like the
// one below, only has to happen once.
//
// A failure decoding the returned input_context back into r.InputContext
// is logged, not returned as a hard error: InputContext is typed `any`,
// so this can't actually fail for the syntactically-valid JSON Postgres
// already required to accept the row via `$3::jsonb` at INSERT time (a
// real syntax error fails there, before this ever runs) -- but staying
// silent about it would still violate this codebase's own "never swallow
// an error silently" convention if that ever stops being true (e.g.
// InputContext becoming a concrete struct type later), and the row itself
// is already durably inserted at this point regardless, so there's
// nothing to roll back over a decode issue.
func insertRun(ctx context.Context, q rowQuerier, workflowID, triggeredBy string, inputContext []byte) (models.Run, error) {
	var r models.Run
	var ic []byte
	err := q.QueryRow(ctx, `
		INSERT INTO runs (workflow_id, triggered_by, status, input_context)
		VALUES ($1, $2, 'running', $3::jsonb)
		RETURNING id, workflow_id, triggered_by, status, started_at, finished_at, input_context
	`, workflowID, triggeredBy, string(inputContext)).Scan(
		&r.ID, &r.WorkflowID, &r.TriggeredBy, &r.Status,
		&r.StartedAt, &r.FinishedAt, &ic,
	)
	if err != nil {
		return models.Run{}, err
	}
	if ic != nil {
		if err := json.Unmarshal(ic, &r.InputContext); err != nil {
			log.Printf("db: run %s: failed to decode stored input_context (%d bytes): %v", r.ID, len(ic), err)
		}
	}
	return r, nil
}

func (s *Store) CreateRun(ctx context.Context, workflowID, triggeredBy string, inputContext []byte) (models.Run, error) {
	return insertRun(ctx, s.pool, workflowID, triggeredBy, inputContext)
}

// advisoryLockNamespaceRunCooldown is the first key of the two-key
// pg_advisory_xact_lock CreateRunWithCooldown takes. Any int32 works here --
// what matters is that it puts this lock in Postgres's two-key advisory
// lock space, which never overlaps the single-key space
// LockOAuthCredentialForRefresh uses, so the two can never collide no
// matter what workflowID/credential id hash to.
const advisoryLockNamespaceRunCooldown = 1

// ErrRunOnCooldown is returned by CreateRunWithCooldown when workflowID
// started a run within the last cooldown window passed to it. RetryAfter
// is how much longer the caller must wait.
type ErrRunOnCooldown struct {
	RetryAfter time.Duration
}

func (e *ErrRunOnCooldown) Error() string {
	return fmt.Sprintf("workflow run cooldown active, retry after %s", e.RetryAfter.Round(time.Second))
}

// CreateRunWithCooldown is CreateRun plus an atomic, DB-backed minimum gap
// between two run starts for the same workflow -- a blunt deterrent
// against a leaked webhook URL or a bot hammering the public trigger
// endpoint with no rate limit otherwise (handlers.TriggerRun/PublicTrigger
// are the only callers).
//
// Deliberately DB-backed rather than an in-process map: (1) the check and
// the insert happen in the same transaction, so a CreateRun failure below
// this point rolls the whole thing back -- a caller that reasonably
// retries right after a transient DB error never sees a phantom cooldown
// for a run that never actually started; (2) it piggybacks on the
// existing runs table instead of a separate unbounded map, so there is no
// new storage to leak over a long-running process's lifetime; (3) since
// Postgres is the one shared source of truth, this is correct regardless
// of how many backend replicas are running, unlike an in-process lock
// that only ever sees its own replica's traffic.
//
// pg_try_advisory_xact_lock (non-blocking), not the plain blocking
// pg_advisory_xact_lock LockOAuthCredentialForRefresh uses below -- that
// function WANTS a caller to wait for a concurrent refresh of the same
// credential to finish. Here, waiting would be actively harmful: this repo
// runs against the Supabase transaction pooler's small shared connection
// budget, and a blocking lock means every request in a burst against the
// same workflow (exactly the burst this cooldown exists to reject) queues
// holding a pooled connection until the one ahead of it finishes -- turning
// this anti-abuse check into a connection-pool-exhaustion vector that can
// starve unrelated, legitimate requests across the whole app. A failed
// try-lock is treated as "another request for this workflow is already
// mid-check" and answered with the same cooldown response, so a burst still
// gets rejected, it just never blocks a connection to do it.
//
// Uses the two-key form of the advisory lock (advisoryLockNamespaceRunCooldown,
// hashtext(workflowID)) rather than a string-prefixed single key. Postgres
// guarantees the two-key lock space never overlaps the single-key space
// LockOAuthCredentialForRefresh uses below, so this new lock can never
// collide with it regardless of hash values -- without needing to change
// LockOAuthCredentialForRefresh's existing key formula (see its own doc
// comment for why that matters: it's pre-existing, and changing its key
// shape would desync old/new replicas mid-rollout).
func (s *Store) CreateRunWithCooldown(ctx context.Context, workflowID, triggeredBy string, inputContext []byte, cooldown time.Duration) (models.Run, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.Run{}, err
	}
	defer tx.Rollback(ctx)

	var locked bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1, hashtext($2))`, advisoryLockNamespaceRunCooldown, workflowID).Scan(&locked); err != nil {
		return models.Run{}, fmt.Errorf("run cooldown: acquire advisory lock: %w", err)
	}
	if !locked {
		// Best-effort: report the actual remaining cooldown, not the full
		// duration. This read doesn't need (and doesn't take) the advisory
		// lock -- it's a plain MVCC snapshot read on the already-held tx
		// connection (not a fresh pool acquisition), purely to give
		// the caller a more accurate Retry-After than "the whole window,"
		// which could be up to `cooldown` longer than the real remaining
		// wait if the lock holder's own check is almost done. Any error, or
		// no rows yet, falls back to reporting the full cooldown -- correct
		// (if imprecise) either way, since RetryAfter is a hint, not a
		// correctness guarantee.
		retryAfter := cooldown
		var elapsedSecs float64
		if err := tx.QueryRow(ctx, `
			SELECT EXTRACT(EPOCH FROM (now() - started_at)) FROM runs
			WHERE workflow_id = $1 ORDER BY started_at DESC LIMIT 1
		`, workflowID).Scan(&elapsedSecs); err == nil {
			if elapsed := time.Duration(elapsedSecs * float64(time.Second)); elapsed < cooldown {
				retryAfter = cooldown - elapsed
			}
		}
		return models.Run{}, &ErrRunOnCooldown{RetryAfter: retryAfter}
	}

	// EXTRACT(EPOCH FROM (now() - started_at)), not started_at scanned into
	// Go and compared via time.Since: the elapsed duration must be computed
	// against Postgres's own clock throughout, not the app server's --
	// otherwise clock skew between hosts could make the cooldown window
	// effectively longer or shorter than `cooldown` actually specifies.
	var elapsedSecs float64
	err = tx.QueryRow(ctx, `
		SELECT EXTRACT(EPOCH FROM (now() - started_at)) FROM runs
		WHERE workflow_id = $1 ORDER BY started_at DESC LIMIT 1
	`, workflowID).Scan(&elapsedSecs)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return models.Run{}, fmt.Errorf("run cooldown: check last run: %w", err)
	}
	if err == nil {
		if elapsed := time.Duration(elapsedSecs * float64(time.Second)); elapsed < cooldown {
			return models.Run{}, &ErrRunOnCooldown{RetryAfter: cooldown - elapsed}
		}
	}

	r, err := insertRun(ctx, tx, workflowID, triggeredBy, inputContext)
	if err != nil {
		return models.Run{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return models.Run{}, err
	}
	return r, nil
}

func (s *Store) GetRun(ctx context.Context, runID string) (models.Run, error) {
	var r models.Run
	var ic []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id, workflow_id, triggered_by, status, started_at, finished_at, input_context
		FROM runs WHERE id=$1
	`, runID).Scan(
		&r.ID, &r.WorkflowID, &r.TriggeredBy, &r.Status,
		&r.StartedAt, &r.FinishedAt, &ic,
	)
	if err != nil {
		return r, err
	}
	if ic != nil {
		json.Unmarshal(ic, &r.InputContext)
	}
	return r, nil
}

func (s *Store) FinishRun(ctx context.Context, runID string, status models.RunStatus) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE runs SET status=$2, finished_at=NOW() WHERE id=$1
	`, runID, string(status))
	return err
}

// --- RunLog methods ---

func (s *Store) InsertRunLog(ctx context.Context, l models.RunLog) (models.RunLog, error) {
	inputJSON, _ := json.Marshal(l.Input)
	var out models.RunLog
	var inJSON, outJSON []byte
	var durationMs *int
	err := s.pool.QueryRow(ctx, `
		INSERT INTO run_logs (run_id, step_index, node_id, node_type, status, input)
		VALUES ($1,$2,$3,$4,$5,$6::jsonb)
		RETURNING id, run_id, step_index, node_id, node_type, status, input, output, duration_ms, ts
	`, l.RunID, l.StepIndex, l.NodeID, string(l.NodeType), string(l.Status), string(inputJSON)).Scan(
		&out.ID, &out.RunID, &out.StepIndex, &out.NodeID, &out.NodeType,
		&out.Status, &inJSON, &outJSON, &durationMs, &out.Ts,
	)
	if err != nil {
		return out, err
	}
	if durationMs != nil {
		out.DurationMs = *durationMs
	}
	if inJSON != nil {
		json.Unmarshal(inJSON, &out.Input)
	}
	return out, nil
}

// configHash is only meaningful (and only ever read back) for a
// LogStatusSuccess row -- see GetLatestNodeStates and RunLog.ConfigHash's
// own doc comment. Callers updating any other status pass "".
func (s *Store) UpdateRunLog(ctx context.Context, id string, status models.LogStatus, outputJSON []byte, durationMs int, configHash string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE run_logs SET status=$2, output=$3::jsonb, duration_ms=$4, node_config_hash=$5 WHERE id=$1
	`, id, string(status), string(outputJSON), durationMs, configHash)
	return err
}

func (s *Store) GetRunLogs(ctx context.Context, runID string) ([]models.RunLog, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, run_id, step_index, node_id, node_type, status, output, duration_ms, ts
		FROM run_logs WHERE run_id=$1 ORDER BY step_index, ts
	`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var logs []models.RunLog
	for rows.Next() {
		var l models.RunLog
		var outJSON []byte
		var durationMs *int
		if err := rows.Scan(
			&l.ID, &l.RunID, &l.StepIndex, &l.NodeID, &l.NodeType,
			&l.Status, &outJSON, &durationMs, &l.Ts,
		); err != nil {
			return nil, err
		}
		if durationMs != nil {
			l.DurationMs = *durationMs
		}
		if outJSON != nil {
			json.Unmarshal(outJSON, &l.Output)
		}
		logs = append(logs, l)
	}
	return logs, rows.Err()
}

// GetLatestNodeStates returns each node's most recent logged status/output
// for a run, keyed by node ID. Runner.Resume uses this to skip re-executing
// (and re-billing/re-paying) any node that already reached a terminal state
// on a prior attempt.
//
// Supported by migration 000028's idx_run_logs_run_id_node_id_ts index --
// this DISTINCT ON/ORDER BY shape matches it exactly (run_id, node_id, ts
// DESC), so Postgres can satisfy it with an index scan instead of sorting
// every row for the run in memory, which otherwise gets more expensive the
// more times a run has been retried/resumed and the more rows per node it
// accumulates.
func (s *Store) GetLatestNodeStates(ctx context.Context, runID string) (map[string]models.RunLog, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (node_id) node_id, status, output, node_config_hash
		FROM run_logs WHERE run_id=$1
		ORDER BY node_id, ts DESC
	`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	states := make(map[string]models.RunLog)
	for rows.Next() {
		var l models.RunLog
		var outJSON []byte
		if err := rows.Scan(&l.NodeID, &l.Status, &outJSON, &l.ConfigHash); err != nil {
			return nil, err
		}
		if outJSON != nil {
			json.Unmarshal(outJSON, &l.Output)
		}
		states[l.NodeID] = l
	}
	return states, rows.Err()
}

// MarkRunRunning resets a run back to "running" with no finish time -- used
// by Resume to undo the "failed"/"stopped" terminal state a prior attempt
// left behind, so the run reads correctly as in-progress while it's retried.
//
// The WHERE clause is Resume's only admission gate: two concurrent resume
// calls for the same run (double-click, retried request) race this same
// UPDATE, and Postgres's row-level lock lets exactly one of them observe the
// pre-terminal status and flip it -- the loser's statement matches zero rows
// and must not execute any node. The same clause rejects a resume on a run
// that's already "running" or already "success", since neither is in the
// IN-list. Returns (false, nil) rather than an error for "not resumable" --
// that's an expected outcome, not a failure.
func (s *Store) MarkRunRunning(ctx context.Context, runID string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE runs SET status='running', finished_at=NULL
		WHERE id=$1 AND status IN ('failed','stopped')
	`, runID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// HasRunningRun reports whether workflowID has any run currently in
// "running" status, read straight from Postgres rather than any
// in-process registry -- the scheduler's overlap guard needs this to be
// visible across every backend replica, not just the one whose tick happens
// to land next. Like every other admission check in this file, this is a
// point-in-time read, not a claim: a run can transition between this call
// and whatever the caller does next, so it narrows the cross-replica gap
// engine.Runner.IsRunning has (in-process only) without claiming to make
// the scheduler's decision fully atomic.
func (s *Store) HasRunningRun(ctx context.Context, workflowID string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM runs WHERE workflow_id=$1 AND status='running')
	`, workflowID).Scan(&exists)
	return exists, err
}

// --- DeadLetterRun methods ---

func (s *Store) InsertDeadLetterRun(ctx context.Context, dl models.DeadLetterRun) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO dead_letter_runs (run_id, node_id, error, attempt_count, payment_risk)
		VALUES ($1,$2,$3,$4,$5)
	`, dl.RunID, dl.NodeID, dl.Error, dl.AttemptCount, dl.PaymentRisk)
	return err
}

// DeleteDeadLettersForNode removes every dead-letter row for nodeID within
// runID -- called once that node reaches a real success within this same
// run (a fresh run, or a resume that retried it), so a node's earlier
// failed attempt stops permanently gating every future resume of this run.
// Without this, a single PaymentRisk row that gets force-resolved (forced
// past, node succeeds) still shows up in GetDeadLetterRuns forever after,
// so an unrelated later node failing for an ordinary transient reason would
// ALSO require force to resume, since the stale, already-resolved row is
// still in the result set alongside it. Scoped to (run_id, node_id), not
// the whole run, so any OTHER node's still-unresolved dead-letter row is
// untouched.
func (s *Store) DeleteDeadLettersForNode(ctx context.Context, runID, nodeID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM dead_letter_runs WHERE run_id=$1 AND node_id=$2`, runID, nodeID)
	return err
}

// GetDeadLetterRuns returns every dead-letter entry for a run, oldest
// first -- normally one row (the level failure stops the run), but a
// workflow can have more than one node fail in the same parallel level.
func (s *Store) GetDeadLetterRuns(ctx context.Context, runID string) ([]models.DeadLetterRun, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, run_id, node_id, error, attempt_count, payment_risk, created_at
		FROM dead_letter_runs WHERE run_id=$1 ORDER BY created_at
	`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.DeadLetterRun
	for rows.Next() {
		var dl models.DeadLetterRun
		if err := rows.Scan(&dl.ID, &dl.RunID, &dl.NodeID, &dl.Error, &dl.AttemptCount, &dl.PaymentRisk, &dl.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, dl)
	}
	return out, rows.Err()
}

// --- AgentWallet methods ---

func (s *Store) InsertAgentWallet(ctx context.Context, w models.AgentWallet) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO agent_wallets (workflow_id, agent_node_id, address, encrypted_mnemonic, network)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (workflow_id, agent_node_id) DO UPDATE
		  SET address=EXCLUDED.address, encrypted_mnemonic=EXCLUDED.encrypted_mnemonic
	`, w.WorkflowID, w.AgentNodeID, w.Address, w.EncryptedMnemonic, w.Network)
	return err
}

func (s *Store) GetAgentWallet(ctx context.Context, workflowID, agentNodeID string) (models.AgentWallet, error) {
	var w models.AgentWallet
	err := s.pool.QueryRow(ctx, `
		SELECT id, workflow_id, agent_node_id, address, encrypted_mnemonic, network
		FROM agent_wallets WHERE workflow_id=$1 AND agent_node_id=$2
	`, workflowID, agentNodeID).Scan(
		&w.ID, &w.WorkflowID, &w.AgentNodeID, &w.Address, &w.EncryptedMnemonic, &w.Network,
	)
	return w, err
}

func (s *Store) ListAgentWallets(ctx context.Context, workflowID string) ([]models.AgentWallet, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, workflow_id, agent_node_id, address, encrypted_mnemonic, network
		FROM agent_wallets WHERE workflow_id=$1
	`, workflowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var wallets []models.AgentWallet
	for rows.Next() {
		var w models.AgentWallet
		if err := rows.Scan(&w.ID, &w.WorkflowID, &w.AgentNodeID, &w.Address, &w.EncryptedMnemonic, &w.Network); err != nil {
			return nil, err
		}
		wallets = append(wallets, w)
	}
	return wallets, rows.Err()
}

// --- User methods ---

func (s *Store) CreateUser(ctx context.Context, email, passwordHash string) (models.User, error) {
	var u models.User
	err := s.pool.QueryRow(ctx, `
		INSERT INTO users (id, email, password_hash)
		VALUES (gen_random_uuid()::text, $1, $2)
		RETURNING id, email, password_hash, name, org_name, created_at
	`, email, passwordHash).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.OrgName, &u.CreatedAt)
	return u, err
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (models.User, error) {
	var u models.User
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, password_hash, name, org_name, created_at
		FROM users WHERE email = $1
	`, email).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.OrgName, &u.CreatedAt)
	return u, err
}

func (s *Store) GetUserByID(ctx context.Context, id string) (models.User, error) {
	var u models.User
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, password_hash, name, org_name, created_at
		FROM users WHERE id = $1
	`, id).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.OrgName, &u.CreatedAt)
	return u, err
}

// UpdateProfile sets the display name and organization name for a user —
// collected at signup for password accounts, or via a post-login onboarding
// step for OAuth accounts (which have no name/org until the provider redirect
// completes, since neither Google nor GitHub's basic profile scope carries one).
func (s *Store) UpdateProfile(ctx context.Context, userID, name, orgName string) (models.User, error) {
	var u models.User
	err := s.pool.QueryRow(ctx, `
		UPDATE users SET name = $2, org_name = $3
		WHERE id = $1
		RETURNING id, email, password_hash, name, org_name, created_at
	`, userID, name, orgName).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.OrgName, &u.CreatedAt)
	return u, err
}

// GetOrCreateOAuthUser returns the user for a verified OAuth email, creating an
// OAuth-only account (empty password_hash, so bcrypt password login always fails)
// when none exists. Linking to an existing OAuth account by verified email is
// allowed; linking to a password account returns ErrPasswordAccountExists.
func (s *Store) GetOrCreateOAuthUser(ctx context.Context, email string) (models.User, error) {
	var u models.User
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, password_hash, name, org_name, created_at FROM users WHERE email = $1
	`, email).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.OrgName, &u.CreatedAt)
	if err == nil {
		if u.PasswordHash != "" {
			return models.User{}, ErrPasswordAccountExists
		}
		return u, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return models.User{}, err
	}

	// No existing user — create an OAuth-only account. name/org_name are left
	// blank: neither provider's basic profile scope carries an organization,
	// and the frontend prompts for both once the user lands back on the app
	// (see Deps.Me's needsOnboarding and Deps.UpdateProfile).
	err = s.pool.QueryRow(ctx, `
		INSERT INTO users (id, email, password_hash)
		VALUES (gen_random_uuid()::text, $1, '')
		ON CONFLICT (email) DO NOTHING
		RETURNING id, email, password_hash, name, org_name, created_at
	`, email).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.OrgName, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// Lost a race: a row appeared between SELECT and INSERT. Re-fetch and
		// apply the same password-account guard.
		err = s.pool.QueryRow(ctx, `
			SELECT id, email, password_hash, name, org_name, created_at FROM users WHERE email = $1
		`, email).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.OrgName, &u.CreatedAt)
		if err == nil && u.PasswordHash != "" {
			return models.User{}, ErrPasswordAccountExists
		}
	}
	return u, err
}

// --- Waitlist methods ---

func (s *Store) InsertWaitlistEmail(ctx context.Context, email string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO waitlist (email) VALUES ($1) ON CONFLICT (email) DO NOTHING
	`, email)
	return err
}

// --- Credit ledger methods ---

func (s *Store) CreateCreditTransaction(ctx context.Context, userID, providerOrderID string, amountINRPaise int64, fxRate float64) (models.CreditTransaction, error) {
	return s.CreateCreditTransactionForProvider(ctx, "cashfree", userID, providerOrderID, amountINRPaise, fxRate)
}

func (s *Store) CreateCreditTransactionForProvider(ctx context.Context, provider, userID, providerOrderID string, amountINRPaise int64, fxRate float64) (models.CreditTransaction, error) {
	creditUSDMicros := int64(math.Round(float64(amountINRPaise) / 100.0 * fxRate * 1e6))
	var txn models.CreditTransaction
	err := s.pool.QueryRow(ctx, `
		INSERT INTO credit_ledger (user_id, provider, provider_order_id, status, amount_inr_paise, fx_rate_usd_per_inr, credit_usd_micros)
		VALUES ($1, $2, $3, 'pending', $4, $5, $6)
		RETURNING id, user_id, provider, provider_order_id, status, amount_inr_paise, fx_rate_usd_per_inr, credit_usd_micros, created_at
	`, userID, provider, providerOrderID, amountINRPaise, fxRate, creditUSDMicros).Scan(
		&txn.ID, &txn.UserID, &txn.Provider, &txn.ProviderOrderID, &txn.Status,
		&txn.AmountINRPaise, &txn.FXRateUSDPerINR, &txn.CreditUSDMicros, &txn.CreatedAt,
	)
	return txn, err
}

// CreateCryptoCreditTransaction records a pending ledger row for a hosted crypto invoice
// (NOWPayments or any future crypto gateway sharing this shape). Unlike the Razorpay path,
// the amount is already USD-denominated by the gateway, so there is no FX rate to store.
func (s *Store) CreateCryptoCreditTransaction(ctx context.Context, userID, provider, providerOrderID string, amountUSDCents int64) (models.CreditTransaction, error) {
	creditUSDMicros := amountUSDCents * 10_000
	var txn models.CreditTransaction
	err := s.pool.QueryRow(ctx, `
		INSERT INTO credit_ledger (user_id, provider, provider_order_id, status, amount_usd_cents, credit_usd_micros)
		VALUES ($1, $2, $3, 'pending', $4, $5)
		RETURNING id, user_id, provider, provider_order_id, status, amount_usd_cents, credit_usd_micros, created_at
	`, userID, provider, providerOrderID, amountUSDCents, creditUSDMicros).Scan(
		&txn.ID, &txn.UserID, &txn.Provider, &txn.ProviderOrderID, &txn.Status,
		&txn.AmountUSDCents, &txn.CreditUSDMicros, &txn.CreatedAt,
	)
	return txn, err
}

// ErrCreditTransactionNotFound is returned when no credit_ledger row exists for the given
// provider order ID — the caller supplied an order Razorpay never told us about (or that
// our own CreateCreditTransaction failed to record). Callers should treat this as a
// permanent 4xx, not a transient failure: retrying an unknown order will never succeed.
var ErrCreditTransactionNotFound = errors.New("credit transaction not found")

// CompleteCreditTransaction marks the ledger row for providerOrderID as completed and
// credits the user's cached balance, atomically. Idempotent: if the row is already
// completed (webhook/verify replay), it returns the stored amount without re-crediting.
// The bool return is true only when this call is the one that actually completed the
// transaction (false on a replay) — callers use it to fire an audit-log notification
// exactly once per real credit, not once per redundant client-verify/webhook race.
func (s *Store) CompleteCreditTransaction(ctx context.Context, provider, providerOrderID, providerPaymentID string) (int64, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback(ctx)

	var (
		id              string
		userID          string
		status          string
		creditUSDMicros int64
		completedAt     *time.Time
	)
	err = tx.QueryRow(ctx, `
		SELECT id, user_id, status, credit_usd_micros, completed_at
		FROM credit_ledger
		WHERE provider_order_id = $1 AND provider = $2
		FOR UPDATE
	`, providerOrderID, provider).Scan(&id, &userID, &status, &creditUSDMicros, &completedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, ErrCreditTransactionNotFound
	}
	if err != nil {
		return 0, false, err
	}

	// Gate on completed_at (replay-safety, unchanged) *and* on status != 'failed': a row
	// a crypto webhook already marked failed/expired must never be resurrected by a
	// late or out-of-order "finished" IPN retry — see MarkCreditTransactionStatus.
	if completedAt != nil || status == "failed" {
		return creditUSDMicros, false, nil
	}

	if _, err := tx.Exec(ctx, `
		UPDATE credit_ledger SET status = 'completed', provider_payment_id = $1, completed_at = NOW()
		WHERE id = $2
	`, providerPaymentID, id); err != nil {
		return 0, false, err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE users SET credit_balance_usd_micros = credit_balance_usd_micros + $1 WHERE id = $2
	`, creditUSDMicros, userID); err != nil {
		return 0, false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, false, err
	}
	return creditUSDMicros, true, nil
}

// RefundCreditTransaction reverses previously-credited USD micros when Razorpay reports a
// refund against an order. totalRefundedINRPaise is the *cumulative* amount refunded on the
// payment so far — Razorpay resends this on every refund event (partial or full), so this
// method tracks refunded_inr_paise on the ledger row and only acts on the delta between the
// new total and what was already applied, making repeated/replayed events safe.
//
// If the order was never completed in our ledger (still 'pending' or already 'expired'), no
// credit was ever granted, so no balance reversal happens — only the bookkeeping columns are
// updated. credit_balance_usd_micros is floored at 0 via GREATEST so a reversal can never push
// a user negative even under an unexpected ordering of events.
//
// The bool return is true only when this call applied a new refund delta (false when the
// cumulative total matches what's already recorded, i.e. a replayed webhook) — callers use
// it to fire an audit-log notification exactly once per real refund event.
func (s *Store) RefundCreditTransaction(ctx context.Context, providerOrderID string, totalRefundedINRPaise int64) (int64, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback(ctx)

	var (
		id               string
		userID           string
		status           string
		amountINRPaise   int64
		fxRate           float64
		refundedINRPaise int64
	)
	err = tx.QueryRow(ctx, `
		SELECT id, user_id, status, amount_inr_paise, fx_rate_usd_per_inr, refunded_inr_paise
		FROM credit_ledger
		WHERE provider_order_id = $1
		FOR UPDATE
	`, providerOrderID).Scan(&id, &userID, &status, &amountINRPaise, &fxRate, &refundedINRPaise)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, ErrCreditTransactionNotFound
	}
	if err != nil {
		return 0, false, err
	}

	delta := totalRefundedINRPaise - refundedINRPaise
	if delta <= 0 {
		return 0, false, nil
	}

	var reversedUSDMicros int64
	if status == "completed" || status == "refunded" {
		reversedUSDMicros = int64(math.Round(float64(delta) / 100.0 * fxRate * 1e6))
		if _, err := tx.Exec(ctx, `
			UPDATE users SET credit_balance_usd_micros = GREATEST(0, credit_balance_usd_micros - $1) WHERE id = $2
		`, reversedUSDMicros, userID); err != nil {
			return 0, false, err
		}
	}

	newStatus := status
	if totalRefundedINRPaise >= amountINRPaise {
		newStatus = "refunded"
	}

	if _, err := tx.Exec(ctx, `
		UPDATE credit_ledger SET refunded_inr_paise = $1, status = $2 WHERE id = $3
	`, totalRefundedINRPaise, newStatus, id); err != nil {
		return 0, false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, false, err
	}
	return reversedUSDMicros, true, nil
}

func (s *Store) GetCreditBalance(ctx context.Context, userID string) (int64, error) {
	var balance int64
	err := s.pool.QueryRow(ctx, `SELECT credit_balance_usd_micros FROM users WHERE id = $1`, userID).Scan(&balance)
	return balance, err
}

// CreditBalance is a thin alias for GetCreditBalance, named to match
// nodes.TendrilStore's method set (TendrilCreditBalance/CreditBalance read
// as a pair there) without a second implementation of the same query.
func (s *Store) CreditBalance(ctx context.Context, userID string) (int64, error) {
	return s.GetCreditBalance(ctx, userID)
}

// ListCreditTransactions returns a user's top-up history, newest first.
//
// This is the read side of the same credit_ledger rows CreateCreditTransaction
// writes and CompleteCreditTransaction settles — the authoritative record of
// what a user paid and what it granted. It exists because the billing page
// previously kept its own copy in localStorage, which is per-browser: signing
// in elsewhere (or as a different account in the same browser) showed the
// wrong history for money the database had recorded correctly all along.
//
// Rows of every status are returned, not just 'completed'. A pending or failed
// top-up is exactly what a user comes to this page to see after a payment that
// did not visibly land, and hiding it would make the page a worse answer than
// the localStorage version it replaces.
func (s *Store) ListCreditTransactions(ctx context.Context, userID string, limit int) ([]models.CreditTransaction, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, provider, provider_order_id, provider_payment_id, status,
		       amount_inr_paise, fx_rate_usd_per_inr, amount_usd_cents, credit_usd_micros,
		       created_at, completed_at
		FROM credit_ledger
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Non-nil so an account with no top-ups marshals as [] rather than null.
	out := make([]models.CreditTransaction, 0, limit)
	for rows.Next() {
		var t models.CreditTransaction
		if err := rows.Scan(
			&t.ID, &t.UserID, &t.Provider, &t.ProviderOrderID, &t.ProviderPaymentID,
			&t.Status, &t.AmountINRPaise, &t.FXRateUSDPerINR, &t.AmountUSDCents,
			&t.CreditUSDMicros, &t.CreatedAt, &t.CompletedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// --- Coupons ---

// The coupon catalog is configuration, not code: it comes from the COUPON_CODES
// env var (parsed by ParseCouponCatalog, installed via SetCouponCatalog at
// startup) so codes can be minted, repriced, or retired without a deploy. An
// empty catalog — the zero value, and what an unset COUPON_CODES gives you —
// means every code is rejected as invalid, which is the right default: a
// forgotten hardcoded code that still grants real credits is a standing
// liability.
//
// Each code is independently redeemable once per user (enforced by the UNIQUE
// (user_id, code) constraint on coupon_redemptions) — redeeming multiple
// distinct codes stacks.

var (
	ErrCouponInvalid         = errors.New("invalid coupon code")
	ErrCouponAlreadyRedeemed = errors.New("coupon already redeemed")
)

// SetCouponCatalog installs the redeemable coupon catalog, keyed by
// already-uppercased code, valued in USD micros. Called once at startup,
// before the server accepts requests.
func (s *Store) SetCouponCatalog(catalog map[string]int64) {
	s.coupons = catalog
}

// CouponCatalog returns the installed catalog (nil if none was set).
func (s *Store) CouponCatalog() map[string]int64 {
	return s.coupons
}

// ParseCouponCatalog parses a COUPON_CODES spec into a catalog. The format is
// a comma-separated list of CODE:AMOUNT pairs, where AMOUNT is in US dollars:
//
//	COUPON_CODES="WELCOME5:5,LAUNCH:12.50"
//
// Codes are upper-cased to match the handler, which upper-cases user input
// before lookup. An empty spec yields an empty catalog with no error; a
// malformed entry is an error rather than a silent skip, so a typo in one code
// can't quietly leave a coupon campaign half-live.
//
// A repeated code is an error for the same reason. Last-write-wins on a
// duplicate would mean "WELCOME5:5,WELCOME5:10" quietly grants $10 while
// reading, to anyone scanning the config, like it grants $5 — the exact class
// of silent misconfiguration the rest of this parser refuses.
func ParseCouponCatalog(spec string) (map[string]int64, error) {
	catalog := map[string]int64{}
	for _, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		code, amountStr, ok := strings.Cut(entry, ":")
		if !ok {
			return nil, fmt.Errorf("coupon entry %q is not in CODE:AMOUNT form", entry)
		}
		code = strings.ToUpper(strings.TrimSpace(code))
		if code == "" {
			return nil, fmt.Errorf("coupon entry %q has an empty code", entry)
		}
		if _, dup := catalog[code]; dup {
			return nil, fmt.Errorf("coupon %s: listed more than once", code)
		}
		usd, err := strconv.ParseFloat(strings.TrimSpace(amountStr), 64)
		if err != nil {
			return nil, fmt.Errorf("coupon %s: amount %q is not a number", code, strings.TrimSpace(amountStr))
		}
		if usd <= 0 {
			return nil, fmt.Errorf("coupon %s: amount must be greater than 0", code)
		}
		// Round rather than truncate: 0.1 lands a hair under 100000 micros in
		// binary floating point, and truncating would quietly short every
		// redemption by one micro.
		catalog[code] = int64(math.Round(usd * 1e6))
	}
	return catalog, nil
}

// RedeemCoupon credits a user's balance for an unredeemed, known coupon code,
// atomically. The UNIQUE (user_id, code) constraint plus ON CONFLICT DO
// NOTHING is what actually enforces "once per user per code" under
// concurrent requests — RowsAffected == 0 means another request (or an
// earlier one) already claimed this code for this user.
//
// Returns the user's new balance and the amount this redemption credited, both
// in USD micros — the caller shows the credited amount, which is per-code
// configuration and no longer a fixed $5 it could hardcode.
func (s *Store) RedeemCoupon(ctx context.Context, userID, code string) (newBalance, credited int64, err error) {
	amount, ok := s.coupons[code]
	if !ok {
		return 0, 0, ErrCouponInvalid
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		INSERT INTO coupon_redemptions (user_id, code, credit_usd_micros)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, code) DO NOTHING
	`, userID, code, amount)
	if err != nil {
		return 0, 0, err
	}
	if tag.RowsAffected() == 0 {
		return 0, 0, ErrCouponAlreadyRedeemed
	}

	if err := tx.QueryRow(ctx, `
		UPDATE users SET credit_balance_usd_micros = credit_balance_usd_micros + $1
		WHERE id = $2
		RETURNING credit_balance_usd_micros
	`, amount, userID).Scan(&newBalance); err != nil {
		return 0, 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, 0, err
	}
	return newBalance, amount, nil
}

// MarkCreditTransactionStatus moves a still-pending ledger row directly to status
// (e.g. "failed"/"expired" for a NOWPayments IPN that will never complete, or "partial"
// for partially_paid) without touching the user's balance — a pending row never credited
// anything, so there's nothing to reverse. No-op if the row is no longer pending, so it's
// safe to call on IPN replays.
func (s *Store) MarkCreditTransactionStatus(ctx context.Context, provider, providerOrderID, status string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE credit_ledger SET status = $1
		WHERE provider_order_id = $2 AND provider = $3 AND status = 'pending'
	`, status, providerOrderID, provider)
	return err
}

// ExpireStalePendingTransactions marks credit_ledger rows for provider still 'pending'
// after olderThan as 'expired' — checkouts the user opened but never completed (closed
// tab, abandoned QR scan, on-chain payment never sent). Scoped to a single provider so
// callers can use a per-provider staleness window: fast checkout providers like Razorpay
// warrant a short window, while on-chain crypto providers like NOWPayments need a much
// longer one to avoid expiring payments still working through block confirmations. Keeps
// 'pending' meaningful as "still in progress" rather than accumulating dead rows.
func (s *Store) ExpireStalePendingTransactions(ctx context.Context, provider string, olderThan time.Duration) (int64, error) {
	// The cutoff is computed by the database, not by this process.
	// created_at is written by Postgres NOW(), so comparing it against an
	// app-computed time.Now() straddles two clocks: any skew between them
	// (a containerised Postgres on a macOS VM routinely runs a fraction of
	// a second ahead of the host) shifts the effective window by that skew,
	// and a row created moments ago can be newer than a cutoff that was
	// supposed to already include it. Evaluating both sides in the DB makes
	// the window exactly olderThan, whatever either clock says.
	tag, err := s.pool.Exec(ctx, `
		UPDATE credit_ledger SET status = 'expired'
		WHERE status = 'pending' AND provider = $1
		  AND created_at < NOW() - make_interval(secs => $2)
	`, provider, olderThan.Seconds())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// --- Debit ledger methods ---

// ErrInsufficientCredits is returned by DebitCredits when the user's balance
// is below the amount being charged. Callers treat this as a permanent
// failure for that call — the node did not run (or, for x402, the payment
// already happened and this is logged rather than retried).
var ErrInsufficientCredits = errors.New("insufficient credits")

// debitCredits atomically locks userID's balance, checks it covers
// amountUSDMicros, decrements it, then lets insertLedger write whatever
// debit_ledger row shape the caller needs inside the same transaction.
// Shared by DebitCredits and DebitCreditsForPlatformLLM so both kinds get
// the identical atomicity guarantee — lock, check, decrement, all inside
// one transaction, same pattern as CompleteCreditTransaction.
func (s *Store) debitCredits(ctx context.Context, userID string, amountUSDMicros int64, insertLedger func(ctx context.Context, tx pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var balance int64
	if err := tx.QueryRow(ctx, `
		SELECT credit_balance_usd_micros FROM users WHERE id = $1 FOR UPDATE
	`, userID).Scan(&balance); err != nil {
		return err
	}

	if balance < amountUSDMicros {
		return ErrInsufficientCredits
	}

	if _, err := tx.Exec(ctx, `
		UPDATE users SET credit_balance_usd_micros = credit_balance_usd_micros - $1 WHERE id = $2
	`, amountUSDMicros, userID); err != nil {
		return err
	}

	if err := insertLedger(ctx, tx); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// ReserveCredits atomically checks a user's balance covers amountUSDMicros
// and, if so, immediately decrements it — without yet writing a debit_ledger
// row. x402 payments split the check from the real payment attempt by a
// network round trip (sign, relay, wait for settlement); reserving the
// balance up front, at the same atomic-decrement primitive DebitCredits
// already uses, closes the gap where concurrent or sequential calls within
// one node execution could all pass a check against the same stale balance.
// Pair with CommitReservedDebit once the payment is confirmed settled, or
// ReleaseReservedCredits if it never happened.
func (s *Store) ReserveCredits(ctx context.Context, userID string, amountUSDMicros int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var balance int64
	if err := tx.QueryRow(ctx, `
		SELECT credit_balance_usd_micros FROM users WHERE id = $1 FOR UPDATE
	`, userID).Scan(&balance); err != nil {
		return err
	}
	if balance < amountUSDMicros {
		return fmt.Errorf("insufficient credits: balance %d micros, need %d micros: %w", balance, amountUSDMicros, ErrInsufficientCredits)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE users SET credit_balance_usd_micros = credit_balance_usd_micros - $1 WHERE id = $2
	`, amountUSDMicros, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// CommitReservedDebit records the debit_ledger audit row for a
// ReserveCredits reservation that turned into a real, settled charge. The
// balance was already decremented at reservation time, so this only writes
// the audit trail — it must never be called with an amount that wasn't
// already reserved.
func (s *Store) CommitReservedDebit(ctx context.Context, userID string, amountUSDMicros int64, kind, workflowID, runID, nodeID string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO debit_ledger (user_id, workflow_id, run_id, node_id, kind, amount_usd_micros)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, userID, workflowID, runID, nodeID, kind, amountUSDMicros)
	return err
}

// ReleaseReservedCredits credits back a ReserveCredits reservation that
// never became a real charge (the payment attempt failed, or was never
// confirmed settled, before any money moved). No debit_ledger row: nothing
// was ever actually charged, so there is nothing there to reverse.
func (s *Store) ReleaseReservedCredits(ctx context.Context, userID string, amountUSDMicros int64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE users SET credit_balance_usd_micros = credit_balance_usd_micros + $1 WHERE id = $2
	`, amountUSDMicros, userID)
	return err
}

// DebitCredits atomically charges a user's credit balance for a metered
// action inside a workflow run, and records the charge in debit_ledger.
func (s *Store) DebitCredits(ctx context.Context, userID string, amountUSDMicros int64, kind, workflowID, runID, nodeID string) error {
	return s.debitCredits(ctx, userID, amountUSDMicros, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO debit_ledger (user_id, workflow_id, run_id, node_id, kind, amount_usd_micros)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, userID, workflowID, runID, nodeID, kind, amountUSDMicros)
		return err
	})
}

// DebitCreditsForPlatformLLM is DebitCredits specialized for the
// platform_key_llm_fee kind: same atomic lock/check/decrement guarantee,
// plus the model and token counts captured for internal margin tracking —
// the charge is always the flat tier fee in amountUSDMicros regardless of
// actual token count, so these columns are informational, not billing.
func (s *Store) DebitCreditsForPlatformLLM(ctx context.Context, userID string, amountUSDMicros int64, workflowID, runID, nodeID, model string, tokensIn, tokensOut int) error {
	return s.debitCredits(ctx, userID, amountUSDMicros, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO debit_ledger (user_id, workflow_id, run_id, node_id, kind, amount_usd_micros, model, tokens_in, tokens_out)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		`, userID, workflowID, runID, nodeID, models.DebitKindPlatformKeyLLMFee, amountUSDMicros, model, tokensIn, tokensOut)
		return err
	})
}

// ListDebitLedger returns every debit_ledger row for a run, oldest first.
// Used by the credits/usage dashboard and by tests asserting exactly which
// charges a run produced.
func (s *Store) ListDebitLedger(ctx context.Context, runID string) ([]models.DebitEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, workflow_id, run_id, node_id, kind, amount_usd_micros, created_at, model, tokens_in, tokens_out
		FROM debit_ledger WHERE run_id = $1 ORDER BY created_at ASC
	`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.DebitEntry
	for rows.Next() {
		var e models.DebitEntry
		if err := rows.Scan(&e.ID, &e.UserID, &e.WorkflowID, &e.RunID, &e.NodeID, &e.Kind, &e.AmountUSDMicros, &e.CreatedAt, &e.Model, &e.TokensIn, &e.TokensOut); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// --- X402 Relay Settlement methods ---

// ErrDuplicateSettlement is returned when an inbound settlement's txid has already
// been recorded — a replayed X-PAYMENT payload must never be processed twice.
var ErrDuplicateSettlement = errors.New("duplicate settlement txid")

func (s *Store) RecordInboundSettlement(ctx context.Context, targetURL, inboundTxID string, amountAssetMicros int64) (models.X402RelaySettlement, error) {
	var row models.X402RelaySettlement
	err := s.pool.QueryRow(ctx, `
		INSERT INTO x402_relay_settlements (target_url, inbound_tx_id, amount_asset_micros)
		VALUES ($1, $2, $3)
		RETURNING id, target_url, inbound_tx_id, outbound_tx_id, amount_asset_micros, status, created_at
	`, targetURL, inboundTxID, amountAssetMicros).Scan(
		&row.ID, &row.TargetURL, &row.InboundTxID, &row.OutboundTxID, &row.AmountAssetMicros, &row.Status, &row.CreatedAt,
	)
	if err != nil && strings.Contains(err.Error(), "duplicate key value") {
		return models.X402RelaySettlement{}, ErrDuplicateSettlement
	}
	return row, err
}

func (s *Store) RecordOutboundSettlement(ctx context.Context, id, outboundTxID, status string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE x402_relay_settlements SET outbound_tx_id = $2, status = $3 WHERE id = $1
	`, id, outboundTxID, status)
	return err
}

// GetX402RelaySettlementByInboundTx looks up a relay ledger row by its
// inbound settlement tx id — used to verify what was actually recorded
// (e.g. the settled amount) after a relay flow completes.
func (s *Store) GetX402RelaySettlementByInboundTx(ctx context.Context, inboundTxID string) (models.X402RelaySettlement, error) {
	var row models.X402RelaySettlement
	err := s.pool.QueryRow(ctx, `
		SELECT id, target_url, inbound_tx_id, outbound_tx_id, amount_asset_micros, status, created_at
		FROM x402_relay_settlements WHERE inbound_tx_id = $1
	`, inboundTxID).Scan(
		&row.ID, &row.TargetURL, &row.InboundTxID, &row.OutboundTxID, &row.AmountAssetMicros, &row.Status, &row.CreatedAt,
	)
	return row, err
}

// RecordRunFunding inserts one x402_run_fundings audit row for a real,
// already-settled inbound payment (Wallet 1 -> Wallet 2) that pre-funds a
// whole run's worth of downstream x402 tool calls, instead of one inbound
// settlement per call.
func (s *Store) RecordRunFunding(ctx context.Context, runID, inboundTxID string, amountAssetMicros int64) (models.X402RunFunding, error) {
	var f models.X402RunFunding
	err := s.pool.QueryRow(ctx, `
		INSERT INTO x402_run_fundings (run_id, inbound_tx_id, amount_asset_micros)
		VALUES ($1, $2, $3)
		RETURNING id, run_id, inbound_tx_id, amount_asset_micros, created_at
	`, runID, inboundTxID, amountAssetMicros).Scan(&f.ID, &f.RunID, &f.InboundTxID, &f.AmountAssetMicros, &f.CreatedAt)
	return f, err
}

// RecordRunFundedSettlement inserts an x402_relay_settlements audit row
// attributed to an existing run-level bulk settlement (run_funding_id)
// instead of a fresh per-call inbound one (inbound_tx_id). Takes
// amountAssetMicros directly at INSERT time — RecordOutboundSettlement only
// ever updates outbound_tx_id/status, never amount_asset_micros, so there is
// no later call that could backfill a placeholder value here.
// RecordInboundSettlement (the existing per-call equivalent) already sets
// the real amount at INSERT time for the same reason — this mirrors that,
// not a new pattern. status is left unset so it defaults to
// 'pending_outbound', matching RecordInboundSettlement's behavior;
// RecordOutboundSettlement's later call must pass "settled" or "failed" to
// satisfy the table's status CHECK constraint.
func (s *Store) RecordRunFundedSettlement(ctx context.Context, runFundingID, targetURL string, amountAssetMicros int64) (models.X402RelaySettlement, error) {
	var row models.X402RelaySettlement
	err := s.pool.QueryRow(ctx, `
		INSERT INTO x402_relay_settlements (target_url, run_funding_id, amount_asset_micros)
		VALUES ($1, $2, $3)
		RETURNING id, target_url, inbound_tx_id, outbound_tx_id, amount_asset_micros, status, created_at
	`, targetURL, runFundingID, amountAssetMicros).Scan(&row.ID, &row.TargetURL, &row.InboundTxID, &row.OutboundTxID, &row.AmountAssetMicros, &row.Status, &row.CreatedAt)
	return row, err
}

// ListX402RunFundingsByRun returns every x402_run_fundings row for a given
// run, oldest first. Used by tests asserting exactly one run-level pre-fund
// happened per agent run (Task 5's reserveAndFundRun).
func (s *Store) ListX402RunFundingsByRun(ctx context.Context, runID string) ([]models.X402RunFunding, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, run_id, inbound_tx_id, amount_asset_micros, created_at
		FROM x402_run_fundings WHERE run_id = $1 ORDER BY created_at ASC
	`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.X402RunFunding
	for rows.Next() {
		var row models.X402RunFunding
		if err := rows.Scan(&row.ID, &row.RunID, &row.InboundTxID, &row.AmountAssetMicros, &row.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// ListX402RelaySettlementsByRunFunding returns every x402_relay_settlements
// row attributed to a given run-level bulk funding (run_funding_id), oldest
// first. Used by tests asserting exactly which per-call settlements a
// run-funded agent turn produced.
func (s *Store) ListX402RelaySettlementsByRunFunding(ctx context.Context, runFundingID string) ([]models.X402RelaySettlement, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, target_url, inbound_tx_id, outbound_tx_id, amount_asset_micros, status, created_at
		FROM x402_relay_settlements WHERE run_funding_id = $1 ORDER BY created_at ASC
	`, runFundingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.X402RelaySettlement
	for rows.Next() {
		var row models.X402RelaySettlement
		if err := rows.Scan(&row.ID, &row.TargetURL, &row.InboundTxID, &row.OutboundTxID, &row.AmountAssetMicros, &row.Status, &row.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// --- Workflow variable methods ---

const (
	// MaxWorkflowVariables caps how many keys one workflow may hold. This
	// is bounded key/value state for "remember the last row I processed",
	// not a document store.
	MaxWorkflowVariables = 64
	// maxWorkflowVariableBytes mirrors the CHECK constraint on the column,
	// enforced here too so the caller gets a typed error instead of a raw
	// Postgres constraint violation.
	maxWorkflowVariableBytes = 16384
)

var (
	ErrVariableQuotaExceeded = errors.New("workflow variable limit reached")
	ErrVariableTooLarge      = errors.New("workflow variable value too large")
)

// GetWorkflowVariables returns every variable for a workflow, JSON-decoded.
// Returns an empty (non-nil) map when the workflow has none, so callers can
// index it without a nil check.
func (s *Store) GetWorkflowVariables(ctx context.Context, workflowID string) (map[string]any, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT key, value FROM workflow_variables WHERE workflow_id=$1
	`, workflowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]any)
	for rows.Next() {
		var k string
		var v []byte
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		var decoded any
		if err := json.Unmarshal(v, &decoded); err != nil {
			return nil, fmt.Errorf("workflow variable %q: stored value is not valid JSON: %w", k, err)
		}
		out[k] = decoded
	}
	return out, rows.Err()
}

// SetWorkflowVariable upserts one variable. Concurrent writers are
// last-write-wins by design: the alternative (optimistic versioning) would
// make the common cases — "cache this token", "remember this cursor" —
// fail spuriously when two runs overlap. Callers that need a correct
// counter under concurrency use IncrementWorkflowVariable instead, which
// is atomic in the database.
//
// The key-count quota is checked inside the same transaction as the write
// so two concurrent inserts cannot both slip past the cap.
func (s *Store) SetWorkflowVariable(ctx context.Context, workflowID, key string, valueJSON []byte) error {
	// Compact before measuring: the caller's JSON may carry insignificant
	// whitespace this check would otherwise count against the quota, while
	// Postgres's own CHECK measures octet_length(value::text) on the JSONB
	// column -- which never stores that whitespace. Compacting here keeps
	// the two checks measuring the same thing, so a value cannot pass one
	// and fail the other depending on how it happened to be formatted.
	var compact bytes.Buffer
	if err := json.Compact(&compact, valueJSON); err != nil {
		return fmt.Errorf("workflow variable value must be valid JSON: %w", err)
	}
	valueJSON = compact.Bytes()
	if len(valueJSON) > maxWorkflowVariableBytes {
		return fmt.Errorf("%w: %d bytes, limit %d", ErrVariableTooLarge, len(valueJSON), maxWorkflowVariableBytes)
	}
	if key == "" || len(key) > 128 {
		return errors.New("workflow variable key must be 1-128 characters")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var count int
	var exists bool
	// COALESCE because BOOL_OR over zero rows is NULL, which will not scan
	// into a bool.
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*), COALESCE(BOOL_OR(key=$2), FALSE)
		FROM workflow_variables WHERE workflow_id=$1
	`, workflowID, key).Scan(&count, &exists); err != nil {
		return err
	}
	// Updating a key that already exists never adds to the count, so it is
	// allowed even at the cap.
	if !exists && count >= MaxWorkflowVariables {
		return fmt.Errorf("%w: %d keys, limit %d", ErrVariableQuotaExceeded, count, MaxWorkflowVariables)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO workflow_variables (workflow_id, key, value, updated_at)
		VALUES ($1,$2,$3::jsonb,NOW())
		ON CONFLICT (workflow_id, key) DO UPDATE
		SET value = EXCLUDED.value, updated_at = NOW()
	`, workflowID, key, string(valueJSON)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// IncrementWorkflowVariable adds delta to a numeric variable and returns
// the new value, creating it at delta if absent. The insert/update itself
// is a single statement, so overlapping runs of the same workflow cannot
// lose an update the way a read-then-write from application code would --
// the quota check runs in the same transaction as that statement, same as
// SetWorkflowVariable, so a key that doesn't exist yet still can't slip
// past MaxWorkflowVariables (a state/increment node with a templated key,
// or just many distinct counter keys, would otherwise grow the table past
// the cap unbounded, since this path took no count check at all before).
//
// A non-numeric existing value is replaced by delta rather than erroring —
// the counter use case wants to keep counting, not to fail a run because
// something once wrote a string there.
func (s *Store) IncrementWorkflowVariable(ctx context.Context, workflowID, key string, delta float64) (float64, error) {
	if key == "" || len(key) > 128 {
		return 0, errors.New("workflow variable key must be 1-128 characters")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var count int
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*), COALESCE(BOOL_OR(key=$2), FALSE)
		FROM workflow_variables WHERE workflow_id=$1
	`, workflowID, key).Scan(&count, &exists); err != nil {
		return 0, err
	}
	if !exists && count >= MaxWorkflowVariables {
		return 0, fmt.Errorf("%w: %d keys, limit %d", ErrVariableQuotaExceeded, count, MaxWorkflowVariables)
	}

	var out float64
	if err := tx.QueryRow(ctx, `
		INSERT INTO workflow_variables (workflow_id, key, value, updated_at)
		VALUES ($1,$2,to_jsonb($3::numeric),NOW())
		ON CONFLICT (workflow_id, key) DO UPDATE
		SET value = to_jsonb(
			CASE WHEN jsonb_typeof(workflow_variables.value) = 'number'
			     THEN (workflow_variables.value)::numeric + $3::numeric
			     ELSE $3::numeric
			END),
		    updated_at = NOW()
		RETURNING (value)::numeric
	`, workflowID, key, delta).Scan(&out); err != nil {
		return 0, err
	}
	return out, tx.Commit(ctx)
}

// DeleteWorkflowVariable removes one key. Deleting a key that does not
// exist is not an error.
func (s *Store) DeleteWorkflowVariable(ctx context.Context, workflowID, key string) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM workflow_variables WHERE workflow_id=$1 AND key=$2
	`, workflowID, key)
	return err
}

const tendrilLeaseCols = `id, user_id, workflow_id, run_id, node_id, lease_id,
	lease_token_enc, tendril_node_id, tendril_node_label, ssh_host, ssh_port,
	ssh_username, ssh_command, ssh_public_key, ssh_private_key_enc,
	ssh_password_enc, rate_usd_micros_per_hour, hours_purchased,
	reserved_usd_micros, charged_usd_micros, used_seconds, status, started_at,
	funded_until, released_at`

func scanTendrilLease(row pgx.Row) (models.TendrilLease, error) {
	var l models.TendrilLease
	err := row.Scan(&l.ID, &l.UserID, &l.WorkflowID, &l.RunID, &l.NodeID, &l.LeaseID,
		&l.LeaseTokenEnc, &l.TendrilNodeID, &l.TendrilNodeLabel, &l.SSHHost, &l.SSHPort,
		&l.SSHUsername, &l.SSHCommand, &l.SSHPublicKey, &l.SSHPrivateKeyEnc,
		&l.SSHPasswordEnc, &l.RateUSDMicrosPerHour, &l.HoursPurchased,
		&l.ReservedUSDMicros, &l.ChargedUSDMicros, &l.UsedSeconds, &l.Status,
		&l.StartedAt, &l.FundedUntil, &l.ReleasedAt)
	return l, err
}

func (s *Store) InsertTendrilLease(ctx context.Context, l models.TendrilLease) (models.TendrilLease, error) {
	return scanTendrilLease(s.pool.QueryRow(ctx, `
		INSERT INTO tendril_leases (user_id, workflow_id, run_id, node_id, lease_id,
			lease_token_enc, tendril_node_id, tendril_node_label, ssh_host, ssh_port,
			ssh_username, ssh_command, ssh_public_key, ssh_private_key_enc,
			ssh_password_enc, rate_usd_micros_per_hour, hours_purchased,
			reserved_usd_micros, funded_until)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
		RETURNING `+tendrilLeaseCols,
		l.UserID, l.WorkflowID, l.RunID, l.NodeID, l.LeaseID, l.LeaseTokenEnc,
		l.TendrilNodeID, l.TendrilNodeLabel, l.SSHHost, l.SSHPort, l.SSHUsername,
		l.SSHCommand, l.SSHPublicKey, l.SSHPrivateKeyEnc, l.SSHPasswordEnc,
		l.RateUSDMicrosPerHour, l.HoursPurchased, l.ReservedUSDMicros, l.FundedUntil))
}

func (s *Store) GetTendrilLease(ctx context.Context, id string) (models.TendrilLease, error) {
	return scanTendrilLease(s.pool.QueryRow(ctx,
		`SELECT `+tendrilLeaseCols+` FROM tendril_leases WHERE id = $1`, id))
}

func (s *Store) ListActiveTendrilLeases(ctx context.Context, userID string) ([]models.TendrilLease, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+tendrilLeaseCols+` FROM tendril_leases
		 WHERE user_id = $1 AND status = 'active' ORDER BY started_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.TendrilLease
	for rows.Next() {
		l, err := scanTendrilLease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ListExpiredTendrilLeases feeds the reaper. Reaps on started_at +
// hours_purchased -- the window THIS renter actually paid for -- not
// funded_until, which reflects how long Tendril's shared pool wallet (every
// AgentMesh user's topups combined) could fund the machine at its rate.
// funded_until is almost always far more than any one user bought (confirmed
// live: a 1-hour rent showed a ~2-hour funded_until), so reaping on it let a
// renter keep metering against the shared pool, for free, well past their
// own paid window -- worse the healthier the pool's balance, since that's
// exactly what stretches funded_until further from what was actually paid
// for. hours_purchased is fractional (a 0.5-hour rent is valid), hence the
// interval multiplication rather than an integer add.
func (s *Store) ListExpiredTendrilLeases(ctx context.Context, now time.Time) ([]models.TendrilLease, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+tendrilLeaseCols+` FROM tendril_leases
		 WHERE status = 'active' AND started_at + (hours_purchased * interval '1 hour') <= $1
		 ORDER BY started_at + (hours_purchased * interval '1 hour')`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.TendrilLease
	for rows.Next() {
		l, err := scanTendrilLease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// MarkTendrilLeaseReleased transitions a lease from active to released, and
// reports via the bool whether THIS call performed a genuine transition (as
// opposed to a no-op update against a row some other caller already closed
// -- concurrent release attempts, or the reaper racing a user's own click).
// A caller that refunded an unused reservation without checking this bool
// used to double-refund on that race, or report a refund that never
// happened when Tendril's own watchdog closed the lease first (a 404 from
// Tendril has no charged amount for us to reconcile against).
func (s *Store) MarkTendrilLeaseReleased(ctx context.Context, id string, usedSeconds, chargedUSDMicros int64) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE tendril_leases
		   SET status = 'released', released_at = NOW(),
		       used_seconds = $2, charged_usd_micros = $3
		 WHERE id = $1 AND status = 'active'`, id, usedSeconds, chargedUSDMicros)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// LatestActiveLeaseForRun is how a run/release node finds the lease its own
// run opened, without the canvas having to thread an id between nodes.
func (s *Store) LatestActiveLeaseForRun(ctx context.Context, runID string) (models.TendrilLease, error) {
	return scanTendrilLease(s.pool.QueryRow(ctx,
		`SELECT `+tendrilLeaseCols+` FROM tendril_leases
		 WHERE run_id = $1 AND status = 'active'
		 ORDER BY started_at DESC LIMIT 1`, runID))
}

// LatestActiveLeaseForUser is the fallback resolveLease reaches for once a
// Run/Release step is split into its own standalone one-node workflow: its
// own run_id never matches the Rent step's (that was a different run
// entirely), so the only thing left to resolve against is "whichever
// machine this user currently has open" — still scoped to one user, never
// the shared pool, so it can never resolve to someone else's lease.
func (s *Store) LatestActiveLeaseForUser(ctx context.Context, userID string) (models.TendrilLease, error) {
	return scanTendrilLease(s.pool.QueryRow(ctx,
		`SELECT `+tendrilLeaseCols+` FROM tendril_leases
		 WHERE user_id = $1 AND status = 'active'
		 ORDER BY started_at DESC LIMIT 1`, userID))
}

const oauthCredentialCols = `id, user_id, provider, account_label, access_token_enc,
	refresh_token_enc, scopes, expires_at, created_at, updated_at`

func scanOAuthCredential(row pgx.Row) (models.OAuthCredential, error) {
	var c models.OAuthCredential
	err := row.Scan(&c.ID, &c.UserID, &c.Provider, &c.AccountLabel, &c.AccessTokenEnc,
		&c.RefreshTokenEnc, &c.Scopes, &c.ExpiresAt, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

// InsertOAuthCredential persists a newly-connected account, or replaces the
// existing one in place (same id) if this user already has a credential for
// the same provider+account_label -- reconnecting the same account must not
// pile up duplicate rows with stale, still-valid refresh tokens, and must
// not change the row's id, since workflow nodes reference credentials by id.
// accessTokenEnc/refreshTokenEnc must already be encrypted -- this layer
// never sees a raw token, mirroring how encryptNodes/decryptNodes keep node
// secrets out of the store package (here it's the caller's job instead,
// since the caller is the one holding the encryption key during the OAuth
// callback).
func (s *Store) InsertOAuthCredential(ctx context.Context, c models.OAuthCredential) (models.OAuthCredential, error) {
	return scanOAuthCredential(s.pool.QueryRow(ctx, `
		INSERT INTO oauth_credentials (user_id, provider, account_label, access_token_enc,
			refresh_token_enc, scopes, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (user_id, provider, account_label) DO UPDATE
		   SET access_token_enc = EXCLUDED.access_token_enc,
		       refresh_token_enc = EXCLUDED.refresh_token_enc,
		       scopes = EXCLUDED.scopes,
		       expires_at = EXCLUDED.expires_at,
		       updated_at = now()
		RETURNING `+oauthCredentialCols,
		c.UserID, c.Provider, c.AccountLabel, c.AccessTokenEnc,
		c.RefreshTokenEnc, c.Scopes, c.ExpiresAt))
}

func (s *Store) GetOAuthCredential(ctx context.Context, id string) (models.OAuthCredential, error) {
	return scanOAuthCredential(s.pool.QueryRow(ctx,
		`SELECT `+oauthCredentialCols+` FROM oauth_credentials WHERE id = $1`, id))
}

// ListOAuthCredentials backs the Inspector's "connect account" picker --
// never returns the encrypted tokens themselves (the struct's json tags
// already omit them, but this is also the query boundary: no caller of this
// method needs the ciphertext, only GetOAuthCredential's node-execution path
// does).
func (s *Store) ListOAuthCredentials(ctx context.Context, userID, provider string) ([]models.OAuthCredential, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+oauthCredentialCols+` FROM oauth_credentials
		 WHERE user_id = $1 AND provider = $2 ORDER BY created_at DESC`, userID, provider)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.OAuthCredential
	for rows.Next() {
		c, err := scanOAuthCredential(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpdateOAuthCredentialTokens persists a refreshed access token (and,
// usually, a rotated refresh token) after RefreshToken exchanges an expired
// one. refreshTokenEnc may be "" -- a provider re-issuing an access token
// doesn't always send a new refresh token, and "" here means "leave the
// existing one alone" (COALESCE against NULLIF), never "erase it": erasing
// a still-valid refresh token would permanently strand this credential the
// next time its access token expires.
func (s *Store) UpdateOAuthCredentialTokens(ctx context.Context, id, accessTokenEnc, refreshTokenEnc string, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE oauth_credentials
		   SET access_token_enc = $2,
		       refresh_token_enc = COALESCE(NULLIF($3, ''), refresh_token_enc),
		       expires_at = $4,
		       updated_at = now()
		 WHERE id = $1`, id, accessTokenEnc, refreshTokenEnc, expiresAt)
	return err
}

// DeleteOAuthCredential is owner-checked by the caller (handlers layer)
// before this runs, same pattern as DeleteWorkflow.
func (s *Store) DeleteOAuthCredential(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM oauth_credentials WHERE id = $1`, id)
	return err
}

// LockOAuthCredentialForRefresh serializes concurrent token refreshes of the
// same credential ACROSS PROCESSES, not just within one -- oauthcred's
// refreshLocks (an in-memory sync.Mutex keyed by credential ID) only
// protects against a race between goroutines in a single backend replica.
// On a multi-replica deployment (Railway runs several), two replicas can
// each independently observe the same expired credential, both hit the
// provider's refresh endpoint, and both write tokens back with no
// coordination between them.
//
// Uses pg_advisory_XACT_lock (transaction-scoped), not the session-scoped
// pg_advisory_lock/unlock pair a first version of this used -- this project
// connects through Supabase's transaction-mode pooler in production
// (CLAUDE.md mandates port 6543; db.go's DefaultQueryExecMode workaround
// exists for the same PgBouncer transaction-mode reality). Under
// transaction-mode pooling, a client is only guaranteed the SAME real
// Postgres backend for the duration of one explicit transaction -- two bare
// Exec calls with no transaction between them (the session-scoped version's
// lock and unlock) can silently land on two different backends. The unlock
// would then no-op against the wrong backend and the lock would never
// actually release, hanging every future refresh of that credential (or
// anything hashing to the same key) forever. A transaction-scoped advisory
// lock sidesteps this entirely: it's automatically released when the
// transaction ends (commit OR rollback), with no separate unlock statement
// that could be misrouted.
//
// This does mean the transaction stays open for the whole check-refresh-
// persist sequence, including the outbound HTTP call to the provider's
// refresh endpoint -- normally worth avoiding, but oauthcred's httpClient
// caps that call at a 10s timeout, well inside any reasonable
// idle-in-transaction timeout, so the tradeoff is acceptable here in
// exchange for correctness under the pooler this project actually runs
// behind.
//
// hashtext() returns a 32-bit int4, so this has a 32-bit collision space --
// not the 64-bit space pg_advisory_xact_lock's bigint argument might
// suggest. A false-positive collision between two different credential
// UUIDs would only ever cause two unrelated refreshes to serialize behind
// each other, never a correctness issue, so this is an acceptable
// consequence at this scale rather than a negligible one -- worth
// revisiting (e.g. hashing into a wider key, or the two-key form) if the
// number of distinct OAuth credentials ever refreshed concurrently grows
// large enough for that to matter in practice. Collision against
// CreateRunWithCooldown's unrelated lock isn't a concern either way: that
// one lives in Postgres's separate two-key advisory lock space (see its
// own doc comment), so it can't collide with this single-key one
// regardless of either one's actual key width.
//
// Key formula deliberately left as plain hashtext(id) -- unchanged since
// before CreateRunWithCooldown was introduced. Prefixing it (e.g.
// hashtext('oauth_credential:' || id)) to "namespace" it would change what
// every in-flight replica computes for the same credential id: during a
// rolling deploy, an old-binary replica and a new-binary replica would
// then use different keys for the same credential and no longer serialize
// against each other -- exactly the race this lock exists to prevent, and
// avoidable entirely by giving new lock users their own key space instead
// of changing this pre-existing one's.
//
// release must be called (via defer) once the caller is done -- it commits
// the underlying transaction, which is what actually releases the lock.
//
// Same Begin+pg_advisory_xact_lock shape as CreateRunWithCooldown,
// deliberately not shared: this one blocks until the lock is free (a
// caller here wants to wait for a concurrent refresh of the same
// credential to finish), whereas CreateRunWithCooldown uses the
// non-blocking pg_try_advisory_xact_lock specifically to avoid queuing
// pooled connections under a burst -- see that function's doc comment.
func (s *Store) LockOAuthCredentialForRefresh(ctx context.Context, id string) (release func(context.Context), err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, id); err != nil {
		tx.Rollback(ctx)
		return nil, err
	}
	return func(releaseCtx context.Context) {
		tx.Commit(releaseCtx)
	}, nil
}
