// Package scheduler fires deployed workflows on their configured cron
// schedule (issue #87's scheduler half). It only decides WHEN to start a
// run -- once due, a scheduled run goes through exactly the same
// store.CreateRun + engine.Runner.Start path api/handlers/runs.go's
// TriggerRun/PublicTrigger already use, just with triggeredBy="schedule"
// instead of "manual"/"webhook".
package scheduler

import (
	"context"
	"log"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/agentmesh/backend/internal/api/handlers"
	"github.com/agentmesh/backend/internal/db"
	"github.com/agentmesh/backend/internal/engine"
	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/sse"
)

// pollInterval bounds how late a schedule can fire: up to one interval
// after its due time, since ClaimDueSchedules only runs on this tick, not
// continuously. A minute matches typical cron granularity (nothing finer
// than "* * * * *" exists to fire late against anyway).
const pollInterval = time.Minute

// cronParser accepts the standard 5-field expression only (no seconds
// field, no macro extensions like @hourly) -- ParseStandard, not the
// looser cron.New(cron.WithSeconds()) parser, matching what most users
// mean by "a cron expression" and what SetSchedule below validates against
// before ever writing to the DB.
var cronParser = cron.ParseStandard

// nextCronRun is ClaimDueSchedules' caller-supplied "how to advance a
// schedule" callback, kept in this package (not db) so internal/db has no
// cron-parsing dependency of its own.
func nextCronRun(cronExpr string, after time.Time) (time.Time, error) {
	sched, err := cronParser(cronExpr)
	if err != nil {
		return time.Time{}, err
	}
	return sched.Next(after), nil
}

type Scheduler struct {
	store         *db.Store
	engine        *engine.Runner
	broker        *sse.Broker
	encryptionKey string
}

func New(store *db.Store, eng *engine.Runner, broker *sse.Broker, encryptionKey string) *Scheduler {
	return &Scheduler{store: store, engine: eng, broker: broker, encryptionKey: encryptionKey}
}

// Run polls once per pollInterval until ctx is cancelled. Intended to be
// launched in its own goroutine from cmd/server/main.go, guarded by the
// same shutdown context as the rest of the server.
func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Scheduler) tick(ctx context.Context) {
	due, err := s.store.ClaimDueSchedules(ctx, time.Now().UTC(), nextCronRun)
	if err != nil {
		log.Printf("scheduler: claim failed: %v", err)
		return
	}
	for _, wf := range due {
		// A workflow whose previous scheduled run is still executing (e.g.
		// its cron interval is shorter than its own runtime) must not fire
		// again here -- Runner.Start's registry always supersedes (cancels)
		// any previous run for the same workflow ID, so starting a new one
		// now would silently truncate the one still in flight.
		//
		// s.engine.IsRunning is a fast, free, in-process check that catches
		// the common single-replica case with no DB round trip -- but it's
		// invisible to every OTHER backend replica. The Postgres deployment
		// here runs multiple replicas (see MarkRunRunning's own doc comment
		// for the same concern on Resume), so a run one replica started is
		// otherwise undetectable to the replica whose tick lands next.
		// HasRunningRun reads shared DB state instead, closing that gap:
		// checked second (only when the cheap local check says "not
		// running") since it costs a query, not because it's less
		// authoritative -- it's the one that's actually correct here.
		if s.engine.IsRunning(wf.ID) {
			log.Printf("scheduler: skipping workflow %s -- previous scheduled run still in progress (local)", wf.ID)
			continue
		}
		if running, err := s.store.HasRunningRun(ctx, wf.ID); err != nil {
			log.Printf("scheduler: checking in-flight run failed for workflow %s: %v", wf.ID, err)
			continue
		} else if running {
			log.Printf("scheduler: skipping workflow %s -- previous scheduled run still in progress (another replica)", wf.ID)
			continue
		}
		run, err := s.store.CreateRun(ctx, wf.ID, "schedule", []byte("{}"))
		if err != nil {
			log.Printf("scheduler: create run failed for workflow %s: %v", wf.ID, err)
			continue
		}
		wf.Nodes = handlers.DecryptNodes(wf.Nodes, s.encryptionKey)
		// StartIfNotRunning, not Start: Start's registry unconditionally
		// cancels any run already registered for wf.ID, which is correct
		// for a user deliberately re-triggering their own workflow but
		// wrong here -- the IsRunning/HasRunningRun checks above are
		// check-then-act, so a manual trigger or resume for this same
		// workflow could have registered in the window between those
		// checks and this call. Using Start here would silently cancel
		// that run, possibly mid-payment, out from under the user.
		// StartIfNotRunning makes the actual registration atomic and
		// non-destructive instead: it refuses rather than superseding, and
		// creates the broker hub itself only once it actually wins (see
		// its own doc comment) -- not done here, since a hub created for a
		// run that turns out to never execute would just leak forever.
		if !s.engine.StartIfNotRunning(wf, run) {
			log.Printf("scheduler: workflow %s registered a run between the overlap check and start -- marking this redundant run row failed instead of executing it", wf.ID)
			if err := s.store.FinishRun(context.Background(), run.ID, models.RunStatusFailed); err != nil {
				log.Printf("scheduler: marking redundant run %s failed also failed: %v", run.ID, err)
			}
		}
	}
}
