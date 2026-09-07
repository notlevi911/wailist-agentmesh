-- runs.workflow_id was the one child FK of workflows declared with no ON
-- DELETE action (000001), i.e. NO ACTION -- so deleting a workflow that had
-- ever been run failed with a raw constraint violation
-- ("runs_workflow_id_fkey"), making the workflows list's Delete unusable for
-- exactly the workflows people actually used. Every other child already
-- cascades: agent_wallets (000001), run_logs via runs (000001), debit_ledger
-- on both workflow_id and run_id (000005). This brings runs in line with them.
--
-- tendril_leases is deliberately NOT part of this: it stays ON DELETE RESTRICT
-- on both workflow_id and run_id (000018), because its row is the only copy of
-- an active lease's encrypted credentials. A workflow with lease history still
-- refuses to delete, which is intended -- the API surfaces that as a clear 409.
ALTER TABLE runs DROP CONSTRAINT runs_workflow_id_fkey;
ALTER TABLE runs ADD CONSTRAINT runs_workflow_id_fkey
    FOREIGN KEY (workflow_id) REFERENCES workflows(id) ON DELETE CASCADE;
