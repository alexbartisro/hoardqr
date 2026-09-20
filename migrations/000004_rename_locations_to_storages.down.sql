-- Reverses 000004.up.sql in exact inverse order. This is load-bearing, not
-- decorative: migration 000002's own down is
-- `DROP INDEX IF EXISTS idx_locations_qr_token_normalized` — if this file
-- didn't restore that exact index name, running 000002's down after this one
-- would silently no-op (IF EXISTS swallows the miss) and leave the index
-- behind under its "storages" name.
UPDATE audit_log SET entity_type = 'location' WHERE entity_type = 'storage';

ALTER INDEX idx_items_storage RENAME TO idx_items_location;
ALTER TABLE items RENAME CONSTRAINT items_storage_id_fkey TO items_location_id_fkey;
ALTER TABLE items RENAME COLUMN storage_id TO location_id;

ALTER INDEX idx_storages_qr_token_normalized RENAME TO idx_locations_qr_token_normalized;
ALTER INDEX idx_storages_name_trgm RENAME TO idx_locations_name_trgm;
ALTER INDEX idx_storages_parent RENAME TO idx_locations_parent;
ALTER TABLE storages RENAME CONSTRAINT storages_qr_token_key TO locations_qr_token_key;
ALTER TABLE storages RENAME CONSTRAINT storages_owner_id_fkey TO locations_owner_id_fkey;
ALTER TABLE storages RENAME CONSTRAINT storages_parent_id_fkey TO locations_parent_id_fkey;
ALTER TABLE storages RENAME CONSTRAINT storages_pkey TO locations_pkey;
ALTER SEQUENCE storages_id_seq RENAME TO locations_id_seq;
ALTER TABLE storages RENAME TO locations;
