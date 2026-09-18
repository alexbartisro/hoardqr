-- name: GetLocationsByParent :many
SELECT * FROM locations WHERE parent_id IS NOT DISTINCT FROM sqlc.narg('parent_id') ORDER BY name;

-- name: GetLocationByID :one
SELECT * FROM locations WHERE id = $1;

-- name: LocationExists :one
SELECT EXISTS(SELECT 1 FROM locations WHERE id = $1);

-- name: LocationBreadcrumb :many
-- Root-to-leaf path (architecture plan §3).
WITH RECURSIVE path AS (
    SELECT l0.id, l0.parent_id, l0.name, 1 AS depth FROM locations l0 WHERE l0.id = $1
    UNION ALL
    SELECT l.id, l.parent_id, l.name, p.depth + 1
    FROM locations l JOIN path p ON l.id = p.parent_id
)
SELECT path.id, path.name FROM path ORDER BY depth DESC;

-- name: DescendantLocationIDs :many
-- Includes the root id itself, matching the mock's descendantLocationIds().
WITH RECURSIVE descendants AS (
    SELECT l0.id FROM locations l0 WHERE l0.id = $1
    UNION ALL
    SELECT l.id FROM locations l JOIN descendants d ON l.parent_id = d.id
)
SELECT descendants.id FROM descendants;

-- name: InsertLocation :one
INSERT INTO locations (parent_id, owner_id, is_shared, name, qr_token, photo_url, notes)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- Location PATCH (rename, move, edit code, toggle is_shared — §9) is hand-written
-- in internal/api/locations.go, not sqlc-generated: it accepts any subset of
-- columns, including explicitly setting parent_id back to NULL, which a
-- COALESCE-based static query can't distinguish from "not provided".

-- name: CountDirectItemsAtLocation :one
SELECT count(*) FROM items WHERE location_id = $1;

-- name: PromoteItemsToParent :exec
-- Only valid when the location being deleted has a parent — moves its direct
-- items up one level. Items nested inside child locations are unaffected;
-- those child locations become root-level on their own via the FK's
-- ON DELETE SET NULL when the DeleteLocation statement runs.
UPDATE items SET location_id = sqlc.arg(new_location_id)::bigint
WHERE location_id = sqlc.arg(old_location_id)::bigint;

-- name: DeleteItemsAtLocation :exec
-- Only used for the root-location force-delete case (§3) — a root location
-- has no parent to promote its direct items to.
DELETE FROM items WHERE location_id = $1;

-- name: DeleteLocation :exec
DELETE FROM locations WHERE id = $1;

-- name: LocationQRTokenInUse :one
-- Compared normalized (not raw), matching idx_locations_qr_token_normalized
-- (migration 000002) — otherwise this pre-check could say "not in use" for a
-- value the unique index would still reject. TRANSLATE(UPPER(x), ...), not
-- UPPER(TRANSLATE(x, ...)) — see migration 000002's comment for why the
-- order matters.
SELECT EXISTS(
    SELECT 1 FROM locations
    WHERE TRANSLATE(UPPER(qr_token), 'OIL', '011') = TRANSLATE(UPPER(sqlc.arg(qr_token)::text), 'OIL', '011')
      AND id != sqlc.arg(id)::bigint
);
