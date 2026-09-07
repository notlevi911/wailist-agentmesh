DROP INDEX IF EXISTS idx_workflows_geofence_enabled;
ALTER TABLE workflows DROP COLUMN IF EXISTS geofence_last_fix_at;
ALTER TABLE workflows DROP COLUMN IF EXISTS geofence_inside;
ALTER TABLE workflows DROP COLUMN IF EXISTS geofence_radius_m;
ALTER TABLE workflows DROP COLUMN IF EXISTS geofence_lng;
ALTER TABLE workflows DROP COLUMN IF EXISTS geofence_lat;
