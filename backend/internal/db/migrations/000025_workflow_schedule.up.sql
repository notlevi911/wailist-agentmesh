ALTER TABLE workflows ADD COLUMN IF NOT EXISTS schedule_cron TEXT;
ALTER TABLE workflows ADD COLUMN IF NOT EXISTS schedule_next_run_at TIMESTAMPTZ;

-- Partial index: only scheduled workflows are ever queried by this column,
-- so indexing the (common) NULL case would be pure waste.
CREATE INDEX IF NOT EXISTS idx_workflows_schedule_due
    ON workflows (schedule_next_run_at)
    WHERE schedule_cron IS NOT NULL;
