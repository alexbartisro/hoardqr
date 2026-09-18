-- Closes the gap CLAUDE.md documented on 2026-09-17: locations.qr_token's
-- plain UNIQUE only catches identical raw strings, but scan/search lookups
-- normalize common OCR/typing misreads first (O→0, I/L→1 — see §4 and
-- internal/store/queries/search.sql). Two raw values like "H4K9P0" and
-- "H4K9PO" pass the raw UNIQUE (they're different strings) but collide once
-- normalized, and the second one silently becomes unreachable by its own
-- code — confirmed empirically while building Phase 3 step 3. A unique
-- index on the normalized expression makes that collision a real, catchable
-- conflict (23505) at write time instead.
-- TRANSLATE(UPPER(x), ...), not UPPER(TRANSLATE(x, ...)) — TRANSLATE's match
-- set ('OIL') is uppercase-only, so applying it before UPPER() silently
-- leaves lowercase o/i/l untouched. Caught via manual psql verification
-- (UPPER(TRANSLATE('h4k9po','OIL','011')) = 'H4K9PO', unchanged) after this
-- ordering bug slipped through the automated tests: the random plain-text
-- codes used to test it never happened to contain a literal 0 or 1 for the
-- lowercase substitution to matter.
CREATE UNIQUE INDEX idx_locations_qr_token_normalized
    ON locations (TRANSLATE(UPPER(qr_token), 'OIL', '011'));
