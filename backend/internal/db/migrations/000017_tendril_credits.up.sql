-- A user's spendable Tendril balance. Distinct from credit_balance_usd_micros:
-- AgentMesh credits buy Tendril credits, Tendril credits buy machine time.
-- The CHECK is the whole safety property — it makes overspending a database
-- error rather than something application code has to remember to prevent.
ALTER TABLE users ADD COLUMN IF NOT EXISTS tendril_credit_usd_micros BIGINT NOT NULL DEFAULT 0;
ALTER TABLE users ADD CONSTRAINT tendril_credit_non_negative
    CHECK (tendril_credit_usd_micros >= 0);

CREATE TABLE IF NOT EXISTS tendril_credit_ledger (
    id                TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    user_id           TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind              TEXT NOT NULL,
    amount_usd_micros BIGINT NOT NULL,
    lease_id          TEXT,
    tx_id             TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT tendril_credit_kind_valid CHECK (kind IN ('topup', 'charge', 'refund')),
    CONSTRAINT tendril_credit_amount_positive CHECK (amount_usd_micros > 0)
);

CREATE INDEX IF NOT EXISTS idx_tendril_credit_ledger_user ON tendril_credit_ledger(user_id, created_at DESC);
