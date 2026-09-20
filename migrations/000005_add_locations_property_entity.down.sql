DROP INDEX IF EXISTS idx_storages_location;
ALTER TABLE storages DROP CONSTRAINT IF EXISTS storages_location_only_on_root;
ALTER TABLE storages DROP COLUMN IF EXISTS location_id;
DROP TABLE IF EXISTS locations;
