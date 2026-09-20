-- name: GetStoragesByParent :many
-- location_name is only ever non-null on a root row (location_id itself is
-- CHECK-constrained to root-only — see migration 000004) — the LEFT JOIN
-- costs nothing extra for a nested row, which just gets a null. Used by the
-- /storages browse page to group root storages by location without a
-- second round trip per row.
SELECT s.*, loc.name AS location_name
FROM storages s
LEFT JOIN locations loc ON loc.id = s.location_id
WHERE s.parent_id IS NOT DISTINCT FROM sqlc.narg('parent_id')
ORDER BY s.name;

-- name: GetStorageByID :one
SELECT * FROM storages WHERE id = $1;

-- name: StorageExists :one
SELECT EXISTS(SELECT 1 FROM storages WHERE id = $1);

-- name: StorageBreadcrumb :many
-- Root-to-leaf path (architecture plan §3). The `visited` array + the
-- `WHERE NOT (s.id = ANY(p.visited))` guard is deliberate cycle-detection
-- insurance: it's the only thing standing between a corrupted/cyclic
-- parent_id chain and an infinite loop. Plain UNION (instead of UNION ALL)
-- would NOT be enough here on its own — `depth` increments every iteration,
-- so every row in a cycle is still a distinct tuple and UNION's dedup would
-- never kick in. The real prevention is StoragesHandler.update rejecting a
-- parent_id that would create a cycle in the first place
-- (internal/api/storages.go) — this is just a backstop for rows that
-- predate that check or were edited directly, and it bounds recursion to at
-- most one pass over all storages regardless.
--
-- Extended (migration 000004) to also resolve the assigned Location, if
-- any: `root` picks off the terminal (highest-depth) row's location_id —
-- ORDER BY depth DESC LIMIT 1 rather than WHERE parent_id IS NULL so a
-- corrupted cyclic chain degrades gracefully (no root row -> NULL via the
-- LEFT JOIN, not a crash) instead of finding no row at all. Every returned
-- row carries the same location_id/location_name (from the one root), which
-- is redundant but simplest — callers just read it off any row (rows[0]).
WITH RECURSIVE path AS (
    SELECT s0.id, s0.parent_id, s0.name, s0.location_id, 1 AS depth, ARRAY[s0.id] AS visited
    FROM storages s0 WHERE s0.id = $1
    UNION ALL
    SELECT s.id, s.parent_id, s.name, s.location_id, p.depth + 1, p.visited || s.id
    FROM storages s JOIN path p ON s.id = p.parent_id
    WHERE NOT (s.id = ANY(p.visited))
),
root AS (
    SELECT location_id FROM path ORDER BY depth DESC LIMIT 1
)
SELECT path.id, path.name, loc.id AS location_id, loc.name AS location_name
FROM path
LEFT JOIN root ON true
LEFT JOIN locations loc ON loc.id = root.location_id
ORDER BY path.depth DESC;

-- name: DirectChildStorageIDs :many
-- Only immediate children (unlike DescendantStorageIDs, which recurses) —
-- used by the delete handler to know which storages are about to be
-- promoted to root by the FK's ON DELETE SET NULL, so their (now-orphaned)
-- location can be propagated from the deleted root storage onto them in the
-- same transaction. Grandchildren stay nested under the promoted children
-- and don't need this — they inherit the location transitively through
-- StorageBreadcrumb's root walk either way.
SELECT id FROM storages WHERE parent_id = $1;

-- name: SetLocationForStorages :exec
UPDATE storages SET location_id = sqlc.arg(location_id)::bigint WHERE id = ANY(sqlc.arg(ids)::bigint[]);

-- name: DescendantStorageIDs :many
-- Includes the root id itself. Same visited-array cycle-detection insurance
-- as StorageBreadcrumb above.
WITH RECURSIVE descendants AS (
    SELECT s0.id, ARRAY[s0.id] AS visited FROM storages s0 WHERE s0.id = $1
    UNION ALL
    SELECT s.id, d.visited || s.id
    FROM storages s JOIN descendants d ON s.parent_id = d.id
    WHERE NOT (s.id = ANY(d.visited))
)
SELECT descendants.id FROM descendants;

-- name: InsertStorage :one
INSERT INTO storages (parent_id, owner_id, is_shared, name, qr_token, photo_url, notes, location_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- Storage PATCH (rename, move, edit code, toggle is_shared — §9) is hand-written
-- in internal/api/storages.go, not sqlc-generated: it accepts any subset of
-- columns, including explicitly setting parent_id back to NULL, which a
-- COALESCE-based static query can't distinguish from "not provided".

-- name: CountDirectItemsAtStorage :one
SELECT count(*) FROM items WHERE storage_id = $1;

-- name: PromoteItemsToParent :exec
-- Only valid when the storage being deleted has a parent — moves its direct
-- items up one level. Items nested inside child storages are unaffected;
-- those child storages become root-level on their own via the FK's
-- ON DELETE SET NULL when the DeleteStorage statement runs.
UPDATE items SET storage_id = sqlc.arg(new_storage_id)::bigint
WHERE storage_id = sqlc.arg(old_storage_id)::bigint;

-- name: DeleteItemsAtStorage :exec
-- Only used for the root-storage force-delete case (§3) — a root storage
-- has no parent to promote its direct items to.
DELETE FROM items WHERE storage_id = $1;

-- name: DeleteStorage :exec
DELETE FROM storages WHERE id = $1;

-- name: StorageQRTokenInUse :one
-- Compared normalized (not raw), matching idx_storages_qr_token_normalized
-- (migration 000002) — otherwise this pre-check could say "not in use" for a
-- value the unique index would still reject. TRANSLATE(UPPER(x), ...), not
-- UPPER(TRANSLATE(x, ...)) — see migration 000002's comment for why the
-- order matters.
SELECT EXISTS(
    SELECT 1 FROM storages
    WHERE TRANSLATE(UPPER(qr_token), 'OIL', '011') = TRANSLATE(UPPER(sqlc.arg(qr_token)::text), 'OIL', '011')
      AND id != sqlc.arg(id)::bigint
);
