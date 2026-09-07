-- Geofence trigger configuration, one zone per workflow.
--
-- Mirrors the shape schedule_cron/schedule_next_run_at established for cron
-- in 000025: configuration plus the small piece of state the trigger needs to
-- decide whether THIS event is a crossing. Run.triggered_by is free text, so
-- "geofence" needs no schema change of its own.
ALTER TABLE workflows ADD COLUMN IF NOT EXISTS geofence_lat DOUBLE PRECISION;
ALTER TABLE workflows ADD COLUMN IF NOT EXISTS geofence_lng DOUBLE PRECISION;
ALTER TABLE workflows ADD COLUMN IF NOT EXISTS geofence_radius_m DOUBLE PRECISION;

-- Tri-state on purpose. NULL means "no fix has ever been recorded", which is
-- NOT the same as "outside": treating an unknown position as outside would
-- make the very first ping from inside the zone look like an entry the user
-- never made. The first fix establishes the baseline silently; only a change
-- from a KNOWN state is a crossing.
ALTER TABLE workflows ADD COLUMN IF NOT EXISTS geofence_inside BOOLEAN;

-- The client's own timestamp for the last fix that was acted on. The Android
-- app queues pings while offline and flushes them in a burst, so pings can
-- arrive late and out of order; this is what lets a replayed burst be ignored
-- rather than re-firing a crossing that was already handled.
ALTER TABLE workflows ADD COLUMN IF NOT EXISTS geofence_last_fix_at TIMESTAMPTZ;

-- Partial index: only geofenced workflows are ever looked up this way, so
-- indexing the (overwhelmingly common) NULL case would be pure waste. Same
-- reasoning as idx_workflows_schedule_due.
CREATE INDEX IF NOT EXISTS idx_workflows_geofence_enabled
    ON workflows (id)
    WHERE geofence_lat IS NOT NULL;
