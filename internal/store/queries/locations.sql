-- Locations (architecture plan §3, migration 000004) are a flat, non-nested
-- physical-property entity: House, Garage, Parent's House. No recursion, no
-- code/qr_token, no breadcrumb of their own — a storage's StorageBreadcrumb
-- (storages.sql) picks up its assigned location (if any) by walking to its
-- root ancestor.

-- name: ListLocations :many
SELECT * FROM locations ORDER BY name;

-- name: GetLocationByID :one
SELECT * FROM locations WHERE id = $1;

-- name: LocationExists :one
SELECT EXISTS(SELECT 1 FROM locations WHERE id = $1);

-- name: InsertLocation :one
INSERT INTO locations (owner_id, is_shared, name) VALUES ($1, $2, $3) RETURNING *;

-- name: UpdateLocationName :one
-- Static single-column update — unlike storages/items, a location has one
-- editable field, so the dynamic-SET machinery those PATCH handlers need
-- isn't warranted here.
UPDATE locations SET name = $2 WHERE id = $1 RETURNING *;

-- name: CountStoragesAtLocation :one
SELECT count(*) FROM storages WHERE location_id = $1;

-- name: ListStoragesAtLocation :many
SELECT * FROM storages WHERE location_id = $1 ORDER BY name;

-- name: ClearLocationFromStorages :exec
-- Used by the force-delete path (§3-equivalent for locations): every
-- storage assigned to a location being deleted becomes unassigned rather
-- than the delete failing or cascading further.
UPDATE storages SET location_id = NULL WHERE location_id = $1;

-- name: DeleteLocation :exec
DELETE FROM locations WHERE id = $1;

-- name: FindLocationByName :one
-- Case-insensitive exact match, for MCP resolution — locations aren't in
-- SearchSuggest (deliberately: no codes, a handful of rows, and adding them
-- would crowd the shared top-10 the same way already fixed once for
-- storages/items/tags — see CLAUDE.md).
SELECT * FROM locations WHERE lower(name) = lower(sqlc.arg(name)::text);
