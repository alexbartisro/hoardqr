-- The "Location" entity (architecture plan §3) — a flat, non-nested
-- physical property (House, Garage, Parent's House), separate from the
-- self-referencing `storages` tree (boxes/shelves/rooms) migration 000001
-- already defines. Deliberately bare: no qr_token (a house isn't physically
-- labeled), no photo_url/notes (trivial to add later, not needed for a
-- handful of rows).
CREATE TABLE locations (
    id BIGSERIAL PRIMARY KEY,
    owner_id BIGINT REFERENCES users(id),
    is_shared BOOLEAN NOT NULL DEFAULT true,
    name TEXT UNIQUE NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- A root storage (parent_id IS NULL) may optionally belong to a location.
-- Nested storages inherit their location transitively by walking to the
-- root ancestor (see StorageBreadcrumb's extension) rather than carrying
-- their own location_id — a nested storage can't physically be in a
-- different building than its container. The CHECK enforces this at the DB
-- level, not just in the handler: internal/api/storages.go's create/update
-- both pre-check this too, but the constraint is the real backstop against
-- a row inserted or edited some other way.
ALTER TABLE storages ADD COLUMN location_id BIGINT REFERENCES locations(id) ON DELETE SET NULL;
ALTER TABLE storages ADD CONSTRAINT storages_location_only_on_root
    CHECK (location_id IS NULL OR parent_id IS NULL);
CREATE INDEX idx_storages_location ON storages(location_id);
