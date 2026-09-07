DROP INDEX IF EXISTS idx_workflows_user_visible;
ALTER TABLE workflows DROP COLUMN IF EXISTS is_system;
