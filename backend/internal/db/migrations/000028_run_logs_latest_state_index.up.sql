CREATE INDEX IF NOT EXISTS idx_run_logs_run_id_node_id_ts ON run_logs (run_id, node_id, ts DESC);
