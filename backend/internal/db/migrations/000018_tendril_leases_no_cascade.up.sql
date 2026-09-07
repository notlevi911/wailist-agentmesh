-- tendril_leases deliberately outlives the run that opened it (see the
-- model's own doc comment), and its row is the ONLY copy of the encrypted
-- SSH credentials and lease token needed to release a machine that is still
-- actively metering against the shared pool. Deleting a workflow or run
-- (e.g. from the workflows list, or a future run-history cleanup feature)
-- must never silently destroy that row -- an active lease with no local
-- record becomes a machine nobody can release and the reaper can't find.
-- RESTRICT, not CASCADE: a workflow/run with lease history can't be deleted
-- until its leases are gone, rather than deleting it wiping them out.
ALTER TABLE tendril_leases DROP CONSTRAINT tendril_leases_workflow_id_fkey;
ALTER TABLE tendril_leases ADD CONSTRAINT tendril_leases_workflow_id_fkey
    FOREIGN KEY (workflow_id) REFERENCES workflows(id) ON DELETE RESTRICT;

ALTER TABLE tendril_leases DROP CONSTRAINT tendril_leases_run_id_fkey;
ALTER TABLE tendril_leases ADD CONSTRAINT tendril_leases_run_id_fkey
    FOREIGN KEY (run_id) REFERENCES runs(id) ON DELETE RESTRICT;
