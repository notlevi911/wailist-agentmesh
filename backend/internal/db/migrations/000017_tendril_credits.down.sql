DROP TABLE IF EXISTS tendril_credit_ledger;
ALTER TABLE users DROP CONSTRAINT IF EXISTS tendril_credit_non_negative;
ALTER TABLE users DROP COLUMN IF EXISTS tendril_credit_usd_micros;
