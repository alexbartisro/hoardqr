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
-- ⚠️ This query's `matches` CTE is byte-identical to SearchSuggestByKind's
-- below (kept as two separate queries for sqlc's static-query model, not
-- shared) — a change to scoring tiers, LIKE-escaping, or exact-code matching
-- must land in BOTH, including the TRANSLATE(UPPER(x)) ordering below, which
-- already shipped wrong once in this exact file. A future edit to only one
-- copy silently reintroduces a scoring/matching mismatch between the web
-- autocomplete and the MCP tools.
--
-- Unified autocomplete (§5): exact (normalized) code matches score 1.0 and
-- always sort first; everything else is the higher of pg_trgm similarity or
-- a name-match tier mirroring the mock's fuzzyScore() (exact name = 1.0,
-- prefix = 0.8, substring = 0.5) — trigram similarity alone would silently
-- drop a genuine substring match (e.g. "cord" inside "Extension Cord 5m
-- Heavy Duty Outdoor") below the LIMIT 10 cutoff or the % operator's default
-- 0.3 threshold, which the mock's tiering doesn't.
--
-- Also note: this query's own LIMIT 10 applies across ALL kinds combined —
-- correct for an autocomplete dropdown (and for location-picker.svelte,
-- which also calls this via GET /api/search/suggest), wrong for anything
-- that wants the best matches of one specific kind. Use SearchSuggestByKind
-- for that instead of filtering this query's results by kind in Go — see
-- its own comment for why (a real bug this project shipped and fixed).
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

-- name: SearchSuggestByKind :many
-- ⚠️ This query's `matches` CTE is byte-identical to SearchSuggest's above
-- (kept separate for sqlc's static-query model) — edit both together, same
-- warning as SearchSuggest's own comment.
--
-- Same matching logic as SearchSuggest above, but LIMIT 10 applies after
-- restricting to a single kind, not before — SearchSuggest's shared top-10
-- across kinds is exactly right for an autocomplete dropdown (the web
-- search bar, backed by SearchSuggest directly — including
-- location-picker.svelte, which resolves locations specifically but still
-- goes through the shared, unfiltered query; a human seeing "no results"
-- there is a lesser version of this same crowding shape, accepted for now
-- since it's read-only with a human in the loop, not silently wrong like a
-- write path would be), but wrong for the MCP resolvers
-- (internal/mcpserver/resolve.go's resolveLocation/resolveItem) and
-- find_items, which each want the best matches of ONE kind and were
-- filtering SearchSuggest's mixed top-10 in Go. That let higher-scoring
-- matches of a different kind silently crowd out the kind actually being
-- searched for — a query could come back "not found" despite a real match
-- existing, and worse, could silently defeat the resolvers' tie-detection
-- ambiguity guard: a competing same-kind, same-score match got cut by the
-- shared limit before Go ever saw it to compare against. Verified via the
-- full-branch Opus review (2026-09-18); see the Obsidian backend TODO.
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
    WHERE kind = sqlc.arg(kind)::text
    ORDER BY kind, id, score DESC
)
SELECT d.kind, d.id, d.name, d.score, i.location_id
FROM deduped d
LEFT JOIN items i ON d.kind = 'item' AND i.id = d.id
ORDER BY d.score DESC, d.name
LIMIT 10;
