package api

import (
	"context"
	"testing"

	"hoardqr/internal/store"
)

// TestFindStorageByNormalizedCodeHandlesMisreadsAndCase is the test the
// step-3 post-mortem asked for: an earlier version of this normalization had
// TRANSLATE and UPPER in the wrong order, which silently failed on exactly
// this input (lowercase, with a letter substituted for the digit it's
// supposed to normalize to) — and shipped because the tests at the time used
// randomly-generated codes that never happened to contain a 0 or 1.
func TestFindStorageByNormalizedCodeHandlesMisreadsAndCase(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)

	storage, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "normalize test", QrToken: "H4K9P0", IsShared: true,
	})
	if err != nil {
		t.Fatalf("InsertStorage: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = $1`, storage.ID) })

	// Lowercase, and the '0' typed back as a letter 'o' — exactly what OCR or
	// a person retyping a handwritten label produces.
	got, err := q.FindStorageByNormalizedCode(ctx, "h4k9po")
	if err != nil {
		t.Fatalf("FindStorageByNormalizedCode: %v", err)
	}
	if got.ID != storage.ID {
		t.Fatalf("expected to resolve to storage %d, got %d", storage.ID, got.ID)
	}
}

// TestSearchSuggestDedupesCodeAndNameMatch proves the dedup step: an entity
// that matches a query both by its exact code and by fuzzy/substring name
// must appear once, at the higher (exact-code) score — not twice.
func TestSearchSuggestDedupesCodeAndNameMatch(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)

	storage, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "ZEBRA9", QrToken: "ZEBRA9", IsShared: true,
	})
	if err != nil {
		t.Fatalf("InsertStorage: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = $1`, storage.ID) })

	rows, err := q.SearchSuggest(ctx, "ZEBRA9")
	if err != nil {
		t.Fatalf("SearchSuggest: %v", err)
	}

	var hits []store.SearchSuggestRow
	for _, r := range rows {
		if r.Kind == "storage" && r.ID == storage.ID {
			hits = append(hits, r)
		}
	}
	if len(hits) != 1 {
		t.Fatalf("expected exactly one hit for the dual-match storage, got %d: %+v", len(hits), hits)
	}
	if hits[0].Score != 1.0 {
		t.Fatalf("expected the surviving hit to keep the exact-match score 1.0, got %v", hits[0].Score)
	}
}

// TestSearchSuggestMatchesSubstring is the regression test for the bug an
// interim review caught: prefix-only ILIKE plus the pg_trgm '%' operator's
// default 0.3 threshold silently drops a genuine substring match that the
// mock (web/src/lib/api.ts's fuzzyScore) returns at score 0.5. A word buried
// in the middle of a longer, unrelated name is exactly the case a plain
// prefix match can't cover.
func TestSearchSuggestMatchesSubstring(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)

	storage, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "Balcony", QrToken: "SUBSTRTEST1", IsShared: true,
	})
	if err != nil {
		t.Fatalf("InsertStorage: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = $1`, storage.ID) })

	item, err := q.InsertItem(ctx, store.InsertItemParams{
		StorageID: storage.ID, Name: "Extension Cord 5m Heavy Duty Outdoor",
		QrToken: "SUBSTRTEST2", IsShared: true, Quantity: 1, CustomFields: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("InsertItem: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM items WHERE id = $1`, item.ID) })

	rows, err := q.SearchSuggest(ctx, "cord")
	if err != nil {
		t.Fatalf("SearchSuggest: %v", err)
	}
	for _, r := range rows {
		if r.Kind == "item" && r.ID == item.ID {
			if r.Score < 0.5 {
				t.Fatalf("expected the substring match to score at least 0.5, got %v", r.Score)
			}
			if r.StorageID == nil || *r.StorageID != storage.ID {
				t.Fatalf("expected storage_id %d on the item hit, got %v", storage.ID, r.StorageID)
			}
			return
		}
	}
	t.Fatalf("expected a substring match for %q, got none in %+v", "cord", rows)
}

// TestSearchSuggestEscapesLikeMetacharacters proves a literal '%' or '_' in
// the query is matched literally, not treated as a LIKE wildcard — searching
// for "50%" should not behave like searching for "50" followed by anything.
func TestSearchSuggestEscapesLikeMetacharacters(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)

	storage, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "50% Off Bin", QrToken: "PCTTEST1", IsShared: true,
	})
	if err != nil {
		t.Fatalf("InsertStorage: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = $1`, storage.ID) })

	decoy, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "50X Off Bin Unrelated", QrToken: "PCTTEST2", IsShared: true,
	})
	if err != nil {
		t.Fatalf("InsertStorage: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = $1`, decoy.ID) })

	rows, err := q.SearchSuggest(ctx, "50%")
	if err != nil {
		t.Fatalf("SearchSuggest: %v", err)
	}
	var foundReal, foundDecoy bool
	for _, r := range rows {
		if r.Kind == "storage" && r.ID == storage.ID {
			foundReal = true
		}
		if r.Kind == "storage" && r.ID == decoy.ID {
			foundDecoy = true
		}
	}
	if !foundReal {
		t.Fatalf("expected the literal '50%%' match to be found, got %+v", rows)
	}
	if foundDecoy {
		t.Fatalf("'%%' should not act as a wildcard matching '50X ...' when searching for the literal '50%%', got %+v", rows)
	}
}
