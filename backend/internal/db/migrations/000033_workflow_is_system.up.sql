-- Marks a workflow row as backing a partner console (Tendril, Prism) rather
-- than something a user built. GetOrCreateSystemWorkflow sets this true on
-- INSERT; FindSystemWorkflow now requires it on lookup instead of matching
-- on name alone.
--
-- Name-only matching had a real hijack path: UpdateWorkflow never validated
-- names, so a user renaming their OWN workflow to exactly
-- "Tendril Console (managed — do not edit)" -- and FindSystemWorkflow's
-- ORDER BY created_at ASC LIMIT 1 picks the OLDEST match -- made that real
-- workflow BE the console from then on: inaccessible via the normal canvas
-- editor (WorkflowRoute dispatches on id), with console runs/spend
-- attributed to it. is_system makes identity a real column a rename can
-- never touch, not a string a user's own input can spoof.
ALTER TABLE workflows ADD COLUMN IF NOT EXISTS is_system BOOLEAN NOT NULL DEFAULT false;

-- Backfill, addressed by a security review of the first version of this
-- migration: that version set is_system = true for every row matching a
-- console name, which is exactly the flaw above, applied ONE MORE TIME, at
-- migration time instead of request time. A workflow a user had already
-- hijacked (renamed to the console's exact name, pre-migration) would have
-- been "promoted" to is_system = true by that blind UPDATE -- completing
-- the hijack permanently, since after this migration is_system is the only
-- thing that matters and the backfill would have set it on the wrong row.
--
-- The fix: only promote a name-matching row that also has run history
-- POSSIBLE ONLY through the real console handlers. PrismConsoleRun/
-- runTendrilAction tag every run they create with a triggered_by value
-- ('prism-console' / 'tendril-console') no other code path can produce --
-- an ordinary workflow, however renamed, can only get a run row tagged that
-- way by actually having been resolved and executed AS the console, which
-- pre-migration requires already passing the exact name check this
-- migration exists to stop trusting. A hijacked row's own run history (if
-- any) will carry the tags real triggers use ('manual', 'schedule', a
-- webhook id, etc.), never these.
--
-- Accepted trade-off: a real console row a user opened but never actually
-- ran yet (no run row exists at all) is NOT promoted here. It is orphaned,
-- not corrupted -- FindSystemWorkflow simply won't find it post-migration,
-- so GetOrCreateSystemWorkflow mints a fresh is_system=true row on that
-- user's next visit, and the old orphaned row is left exactly as it was,
-- visible as an ordinary (if oddly-named) entry in their workflow list.
-- Inconvenient for that one case, never a hijack.
UPDATE workflows SET is_system = true
    WHERE name IN ('Tendril Console (managed — do not edit)', 'Prism Console (managed, do not edit)')
      AND EXISTS (
          SELECT 1 FROM runs
          WHERE runs.workflow_id = workflows.id
            AND runs.triggered_by IN ('tendril-console', 'prism-console')
      );

-- ListWorkflows filters WHERE NOT is_system so a partner console never shows
-- up next to a user's real workflows. Every user's list is small, but there
-- is no reason to make that a sequential predicate over an unindexed column
-- when a partial index (same pattern as idx_workflows_geofence_enabled,
-- migration 000029) covers exactly the query it runs.
CREATE INDEX IF NOT EXISTS idx_workflows_user_visible
    ON workflows (user_id, updated_at DESC)
    WHERE NOT is_system;
