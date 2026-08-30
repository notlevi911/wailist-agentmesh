CREATE TABLE IF NOT EXISTS workflow_variables (
    workflow_id TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    key         TEXT NOT NULL CHECK (char_length(key) BETWEEN 1 AND 128),
    value       JSONB NOT NULL CHECK (octet_length(value::text) <= 16384),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (workflow_id, key)
);
