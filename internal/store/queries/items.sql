-- Every item-returning query aggregates item_tags/tags into a plain text[] —
-- Item.tags on the frontend is a joined convenience field, not a items table
-- column (see CLAUDE.md), so a plain `SELECT * FROM items` is never enough.

-- name: GetItemByID :one
SELECT i.*, COALESCE(array_agg(t.name ORDER BY t.name) FILTER (WHERE t.name IS NOT NULL), '{}')::text[] AS tags
FROM items i
LEFT JOIN item_tags it ON it.item_id = i.id
LEFT JOIN tags t ON t.id = it.tag_id
WHERE i.id = $1
GROUP BY i.id;

-- name: ListItems :many
-- location_id/q/tag are all optional filters (§9) — sqlc.narg + the
-- "IS NULL OR ..." pattern lets one static query cover every combination.
SELECT i.*, COALESCE(array_agg(t.name ORDER BY t.name) FILTER (WHERE t.name IS NOT NULL), '{}')::text[] AS tags
FROM items i
LEFT JOIN item_tags it ON it.item_id = i.id
LEFT JOIN tags t ON t.id = it.tag_id
WHERE (sqlc.narg('location_id')::bigint IS NULL OR i.location_id = sqlc.narg('location_id'))
  AND (sqlc.narg('q')::text IS NULL OR i.name ILIKE '%' || sqlc.narg('q') || '%')
  AND (
    sqlc.narg('tag')::text IS NULL OR EXISTS (
      SELECT 1 FROM item_tags it2
      JOIN tags t2 ON t2.id = it2.tag_id
      WHERE it2.item_id = i.id AND t2.name = sqlc.narg('tag')
    )
  )
GROUP BY i.id
ORDER BY i.name;

-- name: ListItemsByLocationIDs :many
-- Backs GET /api/locations/:id/contents — location_ids is the recursive
-- descendant set from DescendantLocationIDs.
SELECT i.*, COALESCE(array_agg(t.name ORDER BY t.name) FILTER (WHERE t.name IS NOT NULL), '{}')::text[] AS tags
FROM items i
LEFT JOIN item_tags it ON it.item_id = i.id
LEFT JOIN tags t ON t.id = it.tag_id
WHERE i.location_id = ANY(sqlc.arg('location_ids')::bigint[])
GROUP BY i.id
ORDER BY i.name;

-- name: ListRecentItems :many
-- Backs getRecentItems (not in §9 — see CLAUDE.md's "Mock API surface"
-- notes): the dashboard's newest-first feed.
SELECT i.*, COALESCE(array_agg(t.name ORDER BY t.name) FILTER (WHERE t.name IS NOT NULL), '{}')::text[] AS tags
FROM items i
LEFT JOIN item_tags it ON it.item_id = i.id
LEFT JOIN tags t ON t.id = it.tag_id
GROUP BY i.id
ORDER BY i.created_at DESC
LIMIT $1 OFFSET $2;

-- name: CountItems :one
SELECT count(*) FROM items;

-- name: InsertItem :one
INSERT INTO items (
    location_id, owner_id, is_shared, name, description, quantity, condition,
    qr_token, photo_url, purchase_date, purchase_price, receipt_url, custom_fields
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING *;

-- Item PATCH is hand-written in internal/api/items.go, same reasoning as
-- location PATCH — arbitrary subset of columns, nullable fields included.

-- name: DeleteItem :exec
DELETE FROM items WHERE id = $1;

-- name: UpdateItemLocation :execrows
-- Backs the MCP move_item tool (§11) — a plain reassignment, paired with an
-- InsertAuditLog call in the same handler (audit_log's first real writer;
-- §3 defines the table but nothing has written to it before this tool).
-- :execrows (not :exec) so move_item can tell a real reassignment apart
-- from a no-op against an item deleted between resolveItem's lookup (on
-- the pool, outside this transaction) and this UPDATE — without it, 0 rows
-- affected still committed an audit_log "moved" row and reported success
-- for a move that never happened.
UPDATE items SET location_id = sqlc.arg(location_id)::bigint, updated_at = now()
WHERE id = sqlc.arg(id)::bigint;

-- name: ItemExists :one
SELECT EXISTS(SELECT 1 FROM items WHERE id = $1);

-- name: ListTags :many
SELECT * FROM tags ORDER BY name;

-- name: GetTagByNameCI :one
-- Matches the mock createTag's case-insensitive idempotency (tags.name has
-- no case-insensitive unique index — application-level check, same as mock).
SELECT * FROM tags WHERE lower(name) = lower($1);

-- name: InsertTag :one
INSERT INTO tags (name) VALUES ($1) RETURNING *;

-- name: GetTagByID :one
SELECT * FROM tags WHERE id = $1;

-- name: ReplaceItemTags :exec
DELETE FROM item_tags WHERE item_id = $1;

-- name: LinkItemTag :exec
INSERT INTO item_tags (item_id, tag_id) VALUES ($1, $2) ON CONFLICT DO NOTHING;

-- name: TagNamesForItem :many
SELECT t.name FROM tags t
JOIN item_tags it ON it.tag_id = t.id
WHERE it.item_id = $1
ORDER BY t.name;
