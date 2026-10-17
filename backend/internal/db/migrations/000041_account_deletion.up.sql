BEGIN;

-- Legacy development rows can have no owner; enforce ownership for all new writes.
ALTER TABLE workflows ADD CONSTRAINT workflows_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE NOT VALID;
ALTER TABLE tool_credentials ADD CONSTRAINT tool_credentials_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE NOT VALID;
ALTER TABLE x402_run_fundings ADD CONSTRAINT x402_run_fundings_run_id_fkey
    FOREIGN KEY (run_id) REFERENCES runs(id) ON DELETE CASCADE NOT VALID;

ALTER TABLE x402_relay_settlements DROP CONSTRAINT x402_relay_settlements_run_funding_id_fkey;
ALTER TABLE x402_relay_settlements ADD CONSTRAINT x402_relay_settlements_run_funding_id_fkey
    FOREIGN KEY (run_funding_id) REFERENCES x402_run_fundings(id) ON DELETE CASCADE;

-- Lease credentials must survive until the machine is released explicitly.
ALTER TABLE tendril_leases DROP CONSTRAINT tendril_leases_user_id_fkey;
ALTER TABLE tendril_leases ADD CONSTRAINT tendril_leases_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT;

COMMIT;
