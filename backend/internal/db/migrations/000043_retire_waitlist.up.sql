-- Preserve collected rows for export and rollback.
ALTER TABLE IF EXISTS waitlist RENAME TO waitlist_archived;
