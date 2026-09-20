-- The app's original "locations" concept (a self-referencing tree of boxes,
-- shelves, rooms — the UI already calls this "Storage") is renamed to
-- "storages" so the word "location" is free for a new, separate, flat
-- (non-nested) entity representing a physical property (House, Garage,
-- Parent's House) — see migration 000005. Plain ALTER TABLE ... RENAME TO
-- does NOT rename the sequence, PK, other FKs, or indexes, so every one of
-- those is spelled out explicitly here too; every name below was confirmed
-- against the live schema (psql \d locations / \d items) rather than
-- assumed, since a name mismatch here would silently no-op instead of erroring.
ALTER TABLE locations RENAME TO storages;
ALTER SEQUENCE locations_id_seq RENAME TO storages_id_seq;
ALTER TABLE storages RENAME CONSTRAINT locations_pkey TO storages_pkey;
ALTER TABLE storages RENAME CONSTRAINT locations_parent_id_fkey TO storages_parent_id_fkey;
ALTER TABLE storages RENAME CONSTRAINT locations_owner_id_fkey TO storages_owner_id_fkey;
ALTER TABLE storages RENAME CONSTRAINT locations_qr_token_key TO storages_qr_token_key;
ALTER INDEX idx_locations_parent RENAME TO idx_storages_parent;
ALTER INDEX idx_locations_name_trgm RENAME TO idx_storages_name_trgm;
ALTER INDEX idx_locations_qr_token_normalized RENAME TO idx_storages_qr_token_normalized;

ALTER TABLE items RENAME COLUMN location_id TO storage_id;
ALTER TABLE items RENAME CONSTRAINT items_location_id_fkey TO items_storage_id_fkey;
ALTER INDEX idx_items_location RENAME TO idx_items_storage;

-- audit_log.entity_type is a free-text column (no CHECK/FK), so existing
-- 'location' rows are just data to update, not a schema object to rename.
UPDATE audit_log SET entity_type = 'storage' WHERE entity_type = 'location';
