ALTER TABLE runs DROP CONSTRAINT runs_workflow_id_fkey;
ALTER TABLE runs ADD CONSTRAINT runs_workflow_id_fkey
    FOREIGN KEY (workflow_id) REFERENCES workflows(id);
