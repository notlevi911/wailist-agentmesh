CREATE TABLE user_settings (
    user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    low_balance_usd_micros BIGINT NOT NULL DEFAULT 5000000,
    max_call_spend_usd_micros BIGINT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT user_settings_low_balance_range
        CHECK (low_balance_usd_micros BETWEEN 0 AND 9007199254740991),
    CONSTRAINT user_settings_call_spend_range
        CHECK (max_call_spend_usd_micros IS NULL OR
               max_call_spend_usd_micros BETWEEN 50000 AND 1000000000)
);
