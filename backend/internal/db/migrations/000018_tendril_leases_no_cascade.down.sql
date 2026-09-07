ALTER TABLE tendril_leases DROP CONSTRAINT tendril_leases_workflow_id_fkey;
ALTER TABLE tendril_leases ADD CONSTRAINT tendril_leases_workflow_id_fkey
    FOREIGN KEY (workflow_id) REFERENCES workflows(id) ON DELETE CASCADE;

ALTER TABLE tendril_leases DROP CONSTRAINT tendril_leases_run_id_fkey;
ALTER TABLE tendril_leases ADD CONSTRAINT tendril_leases_run_id_fkey
    FOREIGN KEY (run_id) REFERENCES runs(id) ON DELETE CASCADE;
