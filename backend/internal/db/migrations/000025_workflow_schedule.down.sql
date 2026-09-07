DROP INDEX IF EXISTS idx_workflows_schedule_due;
ALTER TABLE workflows DROP COLUMN IF EXISTS schedule_next_run_at;
ALTER TABLE workflows DROP COLUMN IF EXISTS schedule_cron;
