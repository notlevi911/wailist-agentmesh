CREATE TABLE IF NOT EXISTS dead_letter_runs (
    id            TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    run_id        TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    node_id       TEXT NOT NULL,
    error         TEXT NOT NULL,
    attempt_count INT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_dead_letter_runs_run_id ON dead_letter_runs (run_id);
