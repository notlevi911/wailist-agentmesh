package db_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/agentmesh/backend/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestClaimDueSchedulesClaimsAndAdvances verifies the core scheduler
// contract: a deployed workflow whose schedule_next_run_at is in the past
// is claimed, and its next_run_at is advanced to whatever the caller's
// nextRun callback returns -- in the SAME call, so a second sweep before
// that new time doesn't re-claim it.
func TestClaimDueSchedulesClaimsAndAdvances(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	email := fmt.Sprintf("schedule-test-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(ctx, email, "hash")
	if err != nil {
		t.Fatal(err)
	}
	wf, err := store.CreateWorkflow(ctx, "Schedule Test WF", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetWorkflowDeployed(ctx, wf.ID, "https://example.com/run", time.Now()); err != nil {
		t.Fatal(err)
	}

	past := time.Now().Add(-time.Minute)
	if err := store.SetWorkflowSchedule(ctx, wf.ID, "0 9 * * *", past); err != nil {
		t.Fatal(err)
	}

	future := time.Now().Add(24 * time.Hour)
	nextRun := func(cronExpr string, after time.Time) (time.Time, error) {
		if cronExpr != "0 9 * * *" {
			t.Errorf("nextRun got cronExpr %q, want the workflow's own expression", cronExpr)
		}
		return future, nil
	}

	due, err := store.ClaimDueSchedules(ctx, time.Now(), nextRun)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].ID != wf.ID {
		t.Fatalf("claimed %d workflows, want exactly [%s]", len(due), wf.ID)
	}

	// A second sweep right away must not re-claim it -- next_run_at was
	// already advanced into the future inside the first call.
	due2, err := store.ClaimDueSchedules(ctx, time.Now(), nextRun)
	if err != nil {
		t.Fatal(err)
	}
	if len(due2) != 0 {
		t.Fatalf("second sweep claimed %d workflows, want 0 (already advanced past now)", len(due2))
	}

	got, err := store.GetWorkflow(ctx, wf.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Postgres timestamptz is microsecond-precision; time.Now() is
	// nanosecond-precision, so an exact Equal would fail on the sub-
	// microsecond remainder lost in the round trip. A millisecond
	// tolerance confirms the right value made it through without being
	// that strict about it.
	if got.ScheduleNextRunAt == nil {
		t.Fatal("schedule_next_run_at is nil, want the advanced time")
	}
	if diff := got.ScheduleNextRunAt.Sub(future); diff > time.Millisecond || diff < -time.Millisecond {
		t.Errorf("schedule_next_run_at = %v, want %v (diff %v)", got.ScheduleNextRunAt, future, diff)
	}
}

// TestClaimDueSchedulesSkipsDraftAndUnscheduledWorkflows verifies the query
// filters correctly: a draft (never deployed) workflow and a deployed
// workflow with no schedule set must never be claimed, even with a due
// schedule_next_run_at set directly for the draft one.
func TestClaimDueSchedulesSkipsDraftAndUnscheduledWorkflows(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	email := fmt.Sprintf("schedule-draft-test-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(ctx, email, "hash")
	if err != nil {
		t.Fatal(err)
	}

	draftWF, err := store.CreateWorkflow(ctx, "Draft WF", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Minute)
	if err := store.SetWorkflowSchedule(ctx, draftWF.ID, "0 9 * * *", past); err != nil {
		t.Fatal(err)
	}

	unscheduledWF, err := store.CreateWorkflow(ctx, "Unscheduled Deployed WF", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetWorkflowDeployed(ctx, unscheduledWF.ID, "https://example.com/run", time.Now()); err != nil {
		t.Fatal(err)
	}

	nextRun := func(string, time.Time) (time.Time, error) { return time.Now().Add(time.Hour), nil }
	due, err := store.ClaimDueSchedules(ctx, time.Now(), nextRun)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range due {
		if w.ID == draftWF.ID {
			t.Error("a draft (never deployed) workflow must never be claimed")
		}
		if w.ID == unscheduledWF.ID {
			t.Error("a deployed workflow with no schedule must never be claimed")
		}
	}
}

// TestClaimDueSchedulesClearsInvalidExpression verifies a schedule whose
// cron expression no longer parses (nextRun returns an error) is cleared
// rather than left to be re-claimed and fail identically every tick
// forever.
func TestClaimDueSchedulesClearsInvalidExpression(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	email := fmt.Sprintf("schedule-invalid-test-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(ctx, email, "hash")
	if err != nil {
		t.Fatal(err)
	}
	wf, err := store.CreateWorkflow(ctx, "Invalid Schedule WF", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetWorkflowDeployed(ctx, wf.ID, "https://example.com/run", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.SetWorkflowSchedule(ctx, wf.ID, "not a cron expr", time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}

	failingNextRun := func(string, time.Time) (time.Time, error) {
		return time.Time{}, fmt.Errorf("invalid expression")
	}
	due, err := store.ClaimDueSchedules(ctx, time.Now(), failingNextRun)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("claimed %d workflows for an unparseable schedule, want 0", len(due))
	}

	got, err := store.GetWorkflow(ctx, wf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ScheduleCron != nil {
		t.Errorf("schedule_cron = %v, want cleared (nil) after an unparseable expression", got.ScheduleCron)
	}
}

// TestListWorkflowsAndUpdateWorkflowReportScheduleFields is a regression
// test for a review finding: ListWorkflows' SELECT and UpdateWorkflow's
// RETURNING clause both omitted schedule_cron/schedule_next_run_at, so
// GET /workflows always reported a null schedule even for a workflow with
// one actively set, and the response immediately after any PUT
// /workflows/{id} did too -- inconsistent with GetWorkflow, which always
// populated them correctly.
func TestListWorkflowsAndUpdateWorkflowReportScheduleFields(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	email := fmt.Sprintf("schedule-list-update-test-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(ctx, email, "hash")
	if err != nil {
		t.Fatal(err)
	}
	wf, err := store.CreateWorkflow(ctx, "Schedule List Update Test", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetWorkflowDeployed(ctx, wf.ID, "https://example.com/run", time.Now()); err != nil {
		t.Fatal(err)
	}
	nextRun := time.Now().Add(24 * time.Hour)
	if err := store.SetWorkflowSchedule(ctx, wf.ID, "0 9 * * *", nextRun); err != nil {
		t.Fatal(err)
	}

	wfs, err := store.ListWorkflows(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, w := range wfs {
		if w.ID != wf.ID {
			continue
		}
		found = true
		if w.ScheduleCron == nil || *w.ScheduleCron != "0 9 * * *" {
			t.Errorf("ListWorkflows: schedule_cron = %v, want \"0 9 * * *\"", w.ScheduleCron)
		}
		if w.ScheduleNextRunAt == nil {
			t.Error("ListWorkflows: schedule_next_run_at is nil, want the set time")
		}
	}
	if !found {
		t.Fatalf("workflow %s not found in ListWorkflows result", wf.ID)
	}

	updated, err := store.UpdateWorkflow(ctx, wf.ID, "Schedule List Update Test (renamed)", models.WorkflowGraph{})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ScheduleCron == nil || *updated.ScheduleCron != "0 9 * * *" {
		t.Errorf("UpdateWorkflow response: schedule_cron = %v, want \"0 9 * * *\" (must survive an unrelated save)", updated.ScheduleCron)
	}
	if updated.ScheduleNextRunAt == nil {
		t.Error("UpdateWorkflow response: schedule_next_run_at is nil, want the set time to survive an unrelated save")
	}
}

// TestFindSystemWorkflowReportsScheduleFields is a regression test for a
// review finding: FindSystemWorkflow's SELECT was left on the pre-existing
// 9-column list when schedule_cron/schedule_next_run_at were added to
// GetWorkflow, ListWorkflows, and UpdateWorkflow's queries -- so a workflow
// found via FindSystemWorkflow/GetOrCreateSystemWorkflow (the Tendril
// console's hidden per-user workflow) always reported a null schedule
// regardless of the row's real DB state.
func TestFindSystemWorkflowReportsScheduleFields(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	email := fmt.Sprintf("schedule-find-system-test-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(ctx, email, "hash")
	if err != nil {
		t.Fatal(err)
	}
	const systemWorkflowName = "Tendril Console"
	wf, err := store.GetOrCreateSystemWorkflow(ctx, user.ID, systemWorkflowName)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetWorkflowDeployed(ctx, wf.ID, "https://example.com/run", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.SetWorkflowSchedule(ctx, wf.ID, "0 9 * * *", time.Now().Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	found, ok, err := store.FindSystemWorkflow(ctx, user.ID, systemWorkflowName)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("FindSystemWorkflow: want found=true")
	}
	if found.ScheduleCron == nil || *found.ScheduleCron != "0 9 * * *" {
		t.Errorf("FindSystemWorkflow: schedule_cron = %v, want \"0 9 * * *\"", found.ScheduleCron)
	}
	if found.ScheduleNextRunAt == nil {
		t.Error("FindSystemWorkflow: schedule_next_run_at is nil, want the set time")
	}
}

// TestFindSystemWorkflowIgnoresANameCollisionFromAnOrdinaryWorkflow is the
// regression test for the hijack migration 000033 (is_system) closes.
// UpdateWorkflow has never validated names, so before is_system existed, a
// user renaming their OWN ordinary workflow to exactly the console's name
// made FindSystemWorkflow's name-only WHERE clause return THAT workflow --
// oldest match wins -- silently swapping it for the console from then on:
// inaccessible via the canvas (WorkflowRoute dispatches on id) with console
// runs/spend attributed to it. is_system is a column no rename can touch.
func TestFindSystemWorkflowIgnoresANameCollisionFromAnOrdinaryWorkflow(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	email := fmt.Sprintf("system-workflow-collision-test-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(ctx, email, "hash")
	if err != nil {
		t.Fatal(err)
	}

	const systemWorkflowName = "Collision Console (managed, do not edit)"

	// The user's own, ordinary workflow -- created the normal way, is_system
	// false -- happens to carry the exact name a console would use. This is
	// the state UpdateWorkflow's missing validation lets a rename produce.
	ordinary, err := store.CreateWorkflow(ctx, systemWorkflowName, user.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Before is_system, this would have returned the ordinary workflow.
	if _, ok, err := store.FindSystemWorkflow(ctx, user.ID, systemWorkflowName); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatal("FindSystemWorkflow matched the ordinary, non-system workflow by name alone")
	}

	// GetOrCreateSystemWorkflow must therefore create a SEPARATE row, not
	// silently adopt the ordinary one.
	sys, err := store.GetOrCreateSystemWorkflow(ctx, user.ID, systemWorkflowName)
	if err != nil {
		t.Fatal(err)
	}
	if sys.ID == ordinary.ID {
		t.Fatal("GetOrCreateSystemWorkflow returned the ordinary workflow's id instead of creating its own row")
	}
	if !sys.IsSystem {
		t.Error("the row GetOrCreateSystemWorkflow just created has IsSystem = false, want true")
	}

	// The ordinary workflow -- same name, is_system false -- must still be
	// visible to the user; the hidden system row must not be.
	list, err := store.ListWorkflows(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	var sawOrdinary, sawSystem bool
	for _, w := range list {
		if w.ID == ordinary.ID {
			sawOrdinary = true
		}
		if w.ID == sys.ID {
			sawSystem = true
		}
	}
	if !sawOrdinary {
		t.Error("ListWorkflows dropped the user's own ordinary workflow")
	}
	if sawSystem {
		t.Error("ListWorkflows returned the hidden system workflow row")
	}
}

// TestMigration000033BackfillOnlyPromotesARowWithARealConsoleRun is a
// regression test for a security-review finding against migration 000033's
// FIRST version: backfilling is_system by name match alone would have
// completed, not closed, the exact hijack the migration exists to fix -- a
// workflow a user had already renamed to the console's exact name
// (pre-migration) would get promoted to is_system=true by a blind name-match
// UPDATE, permanently.
//
// This runs the migration's actual backfill SQL (copied verbatim from
// 000033_workflow_is_system.up.sql -- keep the two in sync) against two rows
// sharing the reserved name: one with a run tagged the way only the real
// console handlers can produce, one without. Only the corroborated row may
// end up is_system=true.
func TestMigration000033BackfillOnlyPromotesARowWithARealConsoleRun(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	url := os.Getenv("TEST_DATABASE_URL")

	email := fmt.Sprintf("migration-000033-backfill-test-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(ctx, email, "hash")
	if err != nil {
		t.Fatal(err)
	}

	const reservedName = "Tendril Console (managed — do not edit)"

	// The hijack: an ordinary workflow that happens to carry the reserved
	// name, with no console run history at all.
	hijacked, err := store.CreateWorkflow(ctx, reservedName, user.ID)
	if err != nil {
		t.Fatal(err)
	}

	// The real console row: same name, but with a run only
	// runTendrilAction (tendril_console.go) could have produced.
	real, err := store.CreateWorkflow(ctx, reservedName, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRun(ctx, real.ID, "tendril-console", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	// A decoy: run history that exists but was never produced by the real
	// console handler. Must not be treated as corroborating.
	if _, err := store.CreateRun(ctx, hijacked.ID, "manual", []byte("{}")); err != nil {
		t.Fatal(err)
	}

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	// Verbatim from 000033_workflow_is_system.up.sql's backfill UPDATE.
	if _, err := pool.Exec(ctx, `
		UPDATE workflows SET is_system = true
		    WHERE name IN ('Tendril Console (managed — do not edit)', 'Prism Console (managed, do not edit)')
		      AND EXISTS (
		          SELECT 1 FROM runs
		          WHERE runs.workflow_id = workflows.id
		            AND runs.triggered_by IN ('tendril-console', 'prism-console')
		      )
	`); err != nil {
		t.Fatal(err)
	}

	gotHijacked, err := store.GetWorkflow(ctx, hijacked.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotHijacked.IsSystem {
		t.Error("the hijacked (name-matching, no real console run) row was promoted to is_system=true")
	}

	gotReal, err := store.GetWorkflow(ctx, real.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !gotReal.IsSystem {
		t.Error("the real console row (name-matching, with a tendril-console-tagged run) was NOT promoted to is_system=true")
	}
}

// TestClaimDueSchedulesAnchorsNextRunOnDueTimeNotSweepTime is a regression
// test for a review finding: ClaimDueSchedules used to call
// nextRun(cronExpr, now) -- the SWEEP time -- rather than the row's own
// due time. A scheduler tick landing long after a schedule's due time (a
// restart, a slow prior tick, downtime) would silently skip every
// occurrence between the due time and now, jumping straight to whatever
// comes after "right now" and shifting the cron's cadence off its original
// anchor. Anchoring on the due time instead means a badly-delayed tick
// still advances one occurrence at a time from where the schedule actually
// was -- confirmed here by checking nextRun receives the workflow's own
// schedule_next_run_at, not the (much later) `now` passed to
// ClaimDueSchedules.
func TestClaimDueSchedulesAnchorsNextRunOnDueTimeNotSweepTime(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	email := fmt.Sprintf("schedule-anchor-test-%d@example.com", time.Now().UnixNano())
	user, err := store.CreateUser(ctx, email, "hash")
	if err != nil {
		t.Fatal(err)
	}
	wf, err := store.CreateWorkflow(ctx, "Schedule Anchor Test WF", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	// This test deliberately leaves schedule_next_run_at still in the past
	// relative to real wall-clock time (see dueAt below, and nextRun's
	// +1-hour advance from ~110 minutes ago) -- without cleanup, this
	// workflow stays "due" forever after and gets silently re-claimed by
	// any later test in this package that does its own broad
	// ClaimDueSchedules(ctx, time.Now(), ...) sweep, inflating its claimed
	// count (confirmed: this exact leak broke
	// TestClaimDueSchedulesClaimsAndAdvances before this cleanup was added).
	t.Cleanup(func() { store.DeleteWorkflow(context.Background(), wf.ID) })
	if err := store.SetWorkflowDeployed(ctx, wf.ID, "https://example.com/run", time.Now()); err != nil {
		t.Fatal(err)
	}

	// The schedule was due nearly 2 hours ago -- simulating a scheduler
	// that was down or badly delayed, sweeping now instead of anywhere
	// close to when this was actually due.
	dueAt := time.Now().Add(-110 * time.Minute).Truncate(time.Second)
	if err := store.SetWorkflowSchedule(ctx, wf.ID, "0 9 * * *", dueAt); err != nil {
		t.Fatal(err)
	}

	// Stands in for a real cron expression's Next(): always advances by
	// exactly one hour from whatever `after` it's given, so the catch-up
	// loop's per-step anchor is directly observable and its result stays
	// predictable.
	var firstAfter time.Time
	var calls int
	nextRun := func(cronExpr string, after time.Time) (time.Time, error) {
		calls++
		if calls == 1 {
			firstAfter = after
		}
		return after.Add(time.Hour), nil
	}

	sweepTime := time.Now()
	due, err := store.ClaimDueSchedules(ctx, sweepTime, nextRun)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].ID != wf.ID {
		t.Fatalf("claimed %d workflows, want exactly [%s]", len(due), wf.ID)
	}

	// The FIRST step of the catch-up loop must anchor on the row's own due
	// time, not the (110-minutes-later) sweep time -- confirming the fix
	// itself, independent of how many catch-up steps it then takes.
	if diff := firstAfter.Sub(dueAt); diff > time.Second || diff < -time.Second {
		t.Fatalf("nextRun's first `after` = %v, want the row's own due time %v (diff %v) -- must not start from the much-later sweep time %v", firstAfter, dueAt, diff, sweepTime)
	}
	// dueAt was ~110 minutes ago; stepping by whole hours from there needs
	// exactly 2 steps to clear sweepTime (dueAt+1h is still ~50 min in the
	// past, dueAt+2h is ~10 min in the future) -- pins the loop's actual
	// stopping behavior, not just that it eventually stops somewhere.
	if calls != 2 {
		t.Fatalf("nextRun called %d times, want exactly 2 (one step still short of sweepTime, one that clears it)", calls)
	}

	got, err := store.GetWorkflow(ctx, wf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ScheduleNextRunAt == nil {
		t.Fatal("schedule_next_run_at is nil after claiming")
	}
	// The persisted result must actually be in the future relative to the
	// sweep -- otherwise this row stays "due" and gets re-claimed on the
	// very next tick, double-firing for an ordinary delay instead of
	// genuine catch-up (the regression this test guards against).
	if !got.ScheduleNextRunAt.After(sweepTime) {
		t.Fatalf("schedule_next_run_at = %v, want strictly after the sweep time %v (must clear `now`, not just advance one step past the stale due time)", got.ScheduleNextRunAt, sweepTime)
	}
	// Anchored on dueAt's own clock alignment (whole hours from it), not
	// re-based off sweepTime's arbitrary second/sub-second offset.
	if diff := got.ScheduleNextRunAt.Sub(dueAt); diff.Round(time.Second) != 2*time.Hour {
		t.Fatalf("schedule_next_run_at is %v after dueAt, want exactly 2h (anchored on the due time's own cadence)", diff)
	}
}
