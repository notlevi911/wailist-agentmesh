CREATE TABLE IF NOT EXISTS tendril_leases (
    id                        TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    user_id                   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workflow_id               TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    run_id                    TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    node_id                   TEXT NOT NULL,
    lease_id                  TEXT NOT NULL UNIQUE,
    lease_token_enc           TEXT NOT NULL,
    tendril_node_id           TEXT NOT NULL,
    tendril_node_label        TEXT NOT NULL DEFAULT '',
    ssh_host                  TEXT NOT NULL DEFAULT '',
    ssh_port                  INTEGER NOT NULL DEFAULT 22,
    ssh_username              TEXT NOT NULL DEFAULT 'root',
    ssh_command               TEXT NOT NULL DEFAULT '',
    ssh_public_key            TEXT NOT NULL DEFAULT '',
    ssh_private_key_enc       TEXT NOT NULL DEFAULT '',
    ssh_password_enc          TEXT NOT NULL DEFAULT '',
    rate_usd_micros_per_hour  BIGINT NOT NULL,
    hours_purchased           NUMERIC NOT NULL,
    reserved_usd_micros       BIGINT NOT NULL,
    charged_usd_micros        BIGINT,
    used_seconds              BIGINT,
    status                    TEXT NOT NULL DEFAULT 'active',
    started_at                TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    funded_until              TIMESTAMPTZ NOT NULL,
    released_at               TIMESTAMPTZ,
    CONSTRAINT tendril_lease_status_valid CHECK (status IN ('active', 'released', 'failed'))
);

CREATE INDEX IF NOT EXISTS idx_tendril_leases_user   ON tendril_leases(user_id);
CREATE INDEX IF NOT EXISTS idx_tendril_leases_active ON tendril_leases(status, funded_until);
