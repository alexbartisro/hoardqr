CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE users (
    id BIGSERIAL PRIMARY KEY,
    username TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('admin', 'user')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE locations (
    id BIGSERIAL PRIMARY KEY,
    parent_id BIGINT REFERENCES locations(id) ON DELETE SET NULL, -- a deleted location's children become root-level, not deleted
    owner_id BIGINT REFERENCES users(id),
    is_shared BOOLEAN NOT NULL DEFAULT true,   -- visible to every user in the instance
    name TEXT NOT NULL,
    qr_token TEXT UNIQUE NOT NULL,             -- encoded in the printed QR/barcode
    photo_url TEXT,
    notes TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_locations_parent ON locations(parent_id);
CREATE INDEX idx_locations_name_trgm ON locations USING gin (name gin_trgm_ops);

CREATE TABLE items (
    id BIGSERIAL PRIMARY KEY,
    location_id BIGINT NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
    owner_id BIGINT REFERENCES users(id),
    is_shared BOOLEAN NOT NULL DEFAULT true,
    name TEXT NOT NULL,
    description TEXT,
    quantity INT NOT NULL DEFAULT 1,
    condition TEXT,
    qr_token TEXT NOT NULL,                    -- not globally unique: the same retail barcode can legitimately tag several item rows kept in different places
    photo_url TEXT,
    purchase_date DATE,
    purchase_price NUMERIC(10,2),
    receipt_url TEXT,
    custom_fields JSONB NOT NULL DEFAULT '{}', -- per-item flexible metadata, no migration needed to add a field
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_items_location ON items(location_id);
CREATE INDEX idx_items_name_trgm ON items USING gin (name gin_trgm_ops);
CREATE INDEX idx_items_custom_fields ON items USING gin (custom_fields);
CREATE INDEX idx_items_qr_token ON items(qr_token);

CREATE TABLE tags (
    id BIGSERIAL PRIMARY KEY,
    name TEXT UNIQUE NOT NULL
);
CREATE INDEX idx_tags_name_trgm ON tags USING gin (name gin_trgm_ops);

CREATE TABLE item_tags (
    item_id BIGINT REFERENCES items(id) ON DELETE CASCADE,
    tag_id BIGINT REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (item_id, tag_id)
);

CREATE TABLE audit_log (
    id BIGSERIAL PRIMARY KEY,
    entity_type TEXT NOT NULL,       -- 'item' | 'location'
    entity_id BIGINT NOT NULL,
    action TEXT NOT NULL,            -- 'created' | 'moved' | 'updated' | 'deleted'
    user_id BIGINT REFERENCES users(id),
    details JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
