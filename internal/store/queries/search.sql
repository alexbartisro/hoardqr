-- Exact-code lookups (§4/§6/§7) normalize the handful of common OCR/typing
-- misreads before comparing — O→0, I/L→1, case-insensitive — the same way
-- Crockford's own Base32 spec does, and the same way web/src/lib/api.ts's
-- normalizeCode()/codesMatch() do for the mock. TRANSLATE after UPPER is
-- order-independent here since the two replacement sets are disjoint
-- characters, matching normalizeCode()'s .toUpperCase() then two .replace()
-- calls exactly.

-- name: FindItemsByNormalizedCode :many
SELECT i.*, COALESCE(array_agg(t.name ORDER BY t.name) FILTER (WHERE t.name IS NOT NULL), '{}')::text[] AS tags
FROM items i
LEFT JOIN item_tags it ON it.item_id = i.id
LEFT JOIN tags t ON t.id = it.tag_id
WHERE UPPER(TRANSLATE(i.qr_token, 'OIL', '011')) = UPPER(TRANSLATE(sqlc.arg(code)::text, 'OIL', '011'))
GROUP BY i.id
ORDER BY i.id;

-- name: FindLocationByNormalizedCode :one
SELECT * FROM locations
WHERE UPPER(TRANSLATE(qr_token, 'OIL', '011')) = UPPER(TRANSLATE(sqlc.arg(code)::text, 'OIL', '011'));

-- name: SearchSuggest :many
-- Unified autocomplete (§5): exact (normalized) code matches score 1.0 and
-- always sort first; everything else is pg_trgm similarity/prefix matching.
-- Deduped by (kind, id) keeping the higher score before the final sort —
-- a query can hit both an entity's code and its name (e.g. typing part of a
-- location's own plain-text code that also happens to prefix-match a name),
-- and without the dedup step that entity would appear twice.
-- location_id deliberately isn't selected here even for item hits — mixing
-- a real bigint (items) with a literal NULL (locations/tags) in one UNION
-- confused sqlc's static nullability inference into emitting a non-nullable
-- int64 field that would fail to scan a real NULL row at runtime. Callers
-- fetch each item hit's location_id + breadcrumb with GetItemLocationID
-- below instead — a handful of extra tiny queries per search, fine at this
-- app's scale, and it sidesteps the inference problem entirely.
WITH matches AS (
    (
        SELECT 'item'::text AS kind, i.id, i.name, 1.0::real AS score
        FROM items i
        WHERE UPPER(TRANSLATE(i.qr_token, 'OIL', '011')) = UPPER(TRANSLATE(sqlc.arg(query)::text, 'OIL', '011'))
    )
    UNION ALL
    (
        SELECT 'location'::text AS kind, l.id, l.name, 1.0::real AS score
        FROM locations l
        WHERE UPPER(TRANSLATE(l.qr_token, 'OIL', '011')) = UPPER(TRANSLATE(sqlc.arg(query)::text, 'OIL', '011'))
    )
    UNION ALL
    (
        SELECT 'item'::text AS kind, i.id, i.name, similarity(i.name, sqlc.arg(query)::text) AS score
        FROM items i
        WHERE i.name ILIKE sqlc.arg(query)::text || '%' OR i.name % sqlc.arg(query)::text
    )
    UNION ALL
    (
        SELECT 'location'::text AS kind, l.id, l.name, similarity(l.name, sqlc.arg(query)::text) AS score
        FROM locations l
        WHERE l.name ILIKE sqlc.arg(query)::text || '%' OR l.name % sqlc.arg(query)::text
    )
    UNION ALL
    (
        SELECT 'tag'::text AS kind, tg.id, tg.name, similarity(tg.name, sqlc.arg(query)::text) AS score
        FROM tags tg
        WHERE tg.name ILIKE sqlc.arg(query)::text || '%' OR tg.name % sqlc.arg(query)::text
    )
),
deduped AS (
    SELECT DISTINCT ON (kind, id) kind, id, name, score
    FROM matches
    ORDER BY kind, id, score DESC
)
SELECT kind, id, name, score FROM deduped
ORDER BY score DESC, name
LIMIT 10;

-- name: GetItemLocationID :one
SELECT location_id FROM items WHERE id = $1;
