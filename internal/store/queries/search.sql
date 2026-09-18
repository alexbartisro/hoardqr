-- Exact-code lookups (§4/§6/§7) normalize the handful of common OCR/typing
-- misreads before comparing — O→0, I/L→1, case-insensitive — the same way
-- Crockford's own Base32 spec does, and the same way web/src/lib/api.ts's
-- normalizeCode()/codesMatch() do for the mock. Always TRANSLATE(UPPER(x),
-- ...), never UPPER(TRANSLATE(x, ...)) — TRANSLATE's match set ('OIL') is
-- uppercase-only, so running it before UPPER() silently leaves a lowercase
-- input's o/i/l untouched. (Caught late, via manual psql verification —
-- UPPER(TRANSLATE('h4k9po','OIL','011')) comes back 'H4K9PO', not 'H4K9P0' —
-- after this wrong order shipped once already: the random plain-text test
-- codes never happened to contain a literal 0 or 1 for it to matter.)

-- name: FindItemsByNormalizedCode :many
SELECT i.*, COALESCE(array_agg(t.name ORDER BY t.name) FILTER (WHERE t.name IS NOT NULL), '{}')::text[] AS tags
FROM items i
LEFT JOIN item_tags it ON it.item_id = i.id
LEFT JOIN tags t ON t.id = it.tag_id
WHERE TRANSLATE(UPPER(i.qr_token), 'OIL', '011') = TRANSLATE(UPPER(sqlc.arg(code)::text), 'OIL', '011')
GROUP BY i.id
ORDER BY i.id;

-- name: FindLocationByNormalizedCode :one
SELECT * FROM locations
WHERE TRANSLATE(UPPER(qr_token), 'OIL', '011') = TRANSLATE(UPPER(sqlc.arg(code)::text), 'OIL', '011');

-- name: SearchSuggest :many
-- Unified autocomplete (§5): exact (normalized) code matches score 1.0 and
-- always sort first; everything else is the higher of pg_trgm similarity or
-- a name-match tier mirroring the mock's fuzzyScore() (exact name = 1.0,
-- prefix = 0.8, substring = 0.5) — trigram similarity alone would silently
-- drop a genuine substring match (e.g. "cord" inside "Extension Cord 5m
-- Heavy Duty Outdoor") below the LIMIT 10 cutoff or the % operator's default
-- 0.3 threshold, which the mock's tiering doesn't.
--
-- params computes the raw query once and a LIKE-escaped copy once (backslash
-- and pg's LIKE metacharacters % and _ doubled/escaped) so a search
-- containing a literal "%" or "_" is matched literally instead of as a
-- wildcard — every ILIKE below uses like_escaped + ESCAPE '\', every
-- similarity()/exact-match comparison uses raw.
--
-- Deduped by (kind, id) keeping the higher score before the final sort —
-- a query can hit both an entity's code and its name, and without the dedup
-- step that entity would appear twice.
--
-- Each item hit's location_id comes from a LEFT JOIN against deduped, not a
-- separate per-row lookup — an earlier version fetched it with a follow-up
-- GetItemLocationID :one call per hit, which (a) meant up to 10 extra round
-- trips per keystroke and (b) would 500 the whole request if an item was
-- deleted between the two queries, since a :one query returning zero rows is
-- pgx.ErrNoRows and that path wasn't checked. One statement = one snapshot,
-- so within this query that race can't happen at all, not just "is handled".
WITH params AS (
    SELECT
        sqlc.arg(query)::text AS raw,
        replace(replace(replace(sqlc.arg(query)::text, '\', '\\'), '%', '\%'), '_', '\_') AS like_escaped
),
matches AS (
    (
        SELECT 'item'::text AS kind, i.id, i.name, 1.0::real AS score
        FROM items i, params p
        WHERE TRANSLATE(UPPER(i.qr_token), 'OIL', '011') = TRANSLATE(UPPER(p.raw), 'OIL', '011')
    )
    UNION ALL
    (
        SELECT 'location'::text AS kind, l.id, l.name, 1.0::real AS score
        FROM locations l, params p
        WHERE TRANSLATE(UPPER(l.qr_token), 'OIL', '011') = TRANSLATE(UPPER(p.raw), 'OIL', '011')
    )
    UNION ALL
    (
        SELECT 'item'::text AS kind, i.id, i.name,
            GREATEST(
                similarity(i.name, p.raw),
                CASE
                    WHEN lower(i.name) = lower(p.raw) THEN 1.0
                    WHEN i.name ILIKE p.like_escaped || '%' ESCAPE '\' THEN 0.8
                    WHEN i.name ILIKE '%' || p.like_escaped || '%' ESCAPE '\' THEN 0.5
                    ELSE 0.0
                END
            )::real AS score
        FROM items i, params p
        WHERE i.name ILIKE '%' || p.like_escaped || '%' ESCAPE '\' OR i.name % p.raw
    )
    UNION ALL
    (
        SELECT 'location'::text AS kind, l.id, l.name,
            GREATEST(
                similarity(l.name, p.raw),
                CASE
                    WHEN lower(l.name) = lower(p.raw) THEN 1.0
                    WHEN l.name ILIKE p.like_escaped || '%' ESCAPE '\' THEN 0.8
                    WHEN l.name ILIKE '%' || p.like_escaped || '%' ESCAPE '\' THEN 0.5
                    ELSE 0.0
                END
            )::real AS score
        FROM locations l, params p
        WHERE l.name ILIKE '%' || p.like_escaped || '%' ESCAPE '\' OR l.name % p.raw
    )
    UNION ALL
    (
        SELECT 'tag'::text AS kind, tg.id, tg.name,
            GREATEST(
                similarity(tg.name, p.raw),
                CASE
                    WHEN lower(tg.name) = lower(p.raw) THEN 1.0
                    WHEN tg.name ILIKE p.like_escaped || '%' ESCAPE '\' THEN 0.8
                    WHEN tg.name ILIKE '%' || p.like_escaped || '%' ESCAPE '\' THEN 0.5
                    ELSE 0.0
                END
            )::real AS score
        FROM tags tg, params p
        WHERE tg.name ILIKE '%' || p.like_escaped || '%' ESCAPE '\' OR tg.name % p.raw
    )
),
deduped AS (
    SELECT DISTINCT ON (kind, id) kind, id, name, score
    FROM matches
    ORDER BY kind, id, score DESC
)
SELECT d.kind, d.id, d.name, d.score, i.location_id
FROM deduped d
LEFT JOIN items i ON d.kind = 'item' AND i.id = d.id
ORDER BY d.score DESC, d.name
LIMIT 10;
