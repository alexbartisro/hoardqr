-- idx_items_qr_token (migration 000001) indexes the raw column, but every
-- code lookup against items (FindItemsByNormalizedCode, SearchSuggest's item
-- exact-code branch) compares TRANSLATE(UPPER(qr_token), 'OIL', '011') —
-- an expression the planner can't match against a plain-column index, so
-- those lookups sequential-scan items on every scan and every debounced
-- keystroke. Non-unique (unlike locations' equivalent index): items.qr_token
-- is deliberately not unique even raw (§3 — the same retail barcode can tag
-- several items kept in different places), so there's nothing to enforce
-- here, only a lookup path to speed up.
CREATE INDEX idx_items_qr_token_normalized
    ON items (TRANSLATE(UPPER(qr_token), 'OIL', '011'));
