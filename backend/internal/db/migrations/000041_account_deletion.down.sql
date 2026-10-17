BEGIN;

ALTER TABLE tendril_leases DROP CONSTRAINT tendril_leases_user_id_fkey;
ALTER TABLE tendril_leases ADD CONSTRAINT tendril_leases_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE x402_relay_settlements DROP CONSTRAINT x402_relay_settlements_run_funding_id_fkey;
ALTER TABLE x402_relay_settlements ADD CONSTRAINT x402_relay_settlements_run_funding_id_fkey
    FOREIGN KEY (run_funding_id) REFERENCES x402_run_fundings(id);

ALTER TABLE x402_run_fundings DROP CONSTRAINT x402_run_fundings_run_id_fkey;
ALTER TABLE tool_credentials DROP CONSTRAINT tool_credentials_user_id_fkey;
ALTER TABLE workflows DROP CONSTRAINT workflows_user_id_fkey;

COMMIT;
