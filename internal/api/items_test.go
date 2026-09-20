package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hoardqr/internal/codegen"
	"hoardqr/internal/store"
)

// TestItemsRejectNonObjectCustomFields proves custom_fields must be a JSON
// object on both create and update — a scalar like 5, an array, or null
// used to be accepted and stored as-is (a scalar/array mismatches the
// column's JSONB usage and the frontend's Record<string, unknown> type; a
// null would have violated items.custom_fields's NOT NULL constraint as a
// raw, unsanitized Postgres error instead of a clean 400).
func TestItemsRejectNonObjectCustomFields(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)
	router := NewRouter(pool, t.TempDir())

	// Cleans up by name/qr_token, not by ID collected mid-test — if an
	// assertion below ever fails on an unexpectedly-created row (exactly the
	// scenario this test guards against), a t.Fatalf skips any cleanup
	// registration further down, and an ID-based cleanup registered only
	// after a successful create would never run for that row. Items must be
	// deleted before the storage (items.storage_id has no promotion logic
	// via a raw DELETE, unlike StoragesHandler.delete's real endpoint).
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM items WHERE name = 'cf item'`); err != nil {
			t.Logf("cleanup: deleting test items: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM storages WHERE qr_token = 'CFTEST-LOC'`); err != nil {
			t.Logf("cleanup: deleting test storage: %v", err)
		}
	})

	storage, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "custom fields test root", QrToken: "CFTEST-LOC", IsShared: true,
	})
	if err != nil {
		t.Fatalf("InsertStorage: %v", err)
	}

	// "null" is deliberately not in this list: createItemRequest.CustomFields
	// is *json.RawMessage, and Go's encoding/json sets an explicit JSON null
	// on a pointer field to a nil Go pointer — indistinguishable from the
	// field being omitted entirely, so it falls through to the same "{}"
	// default rather than reaching validateCustomFieldsObject at all. That's
	// fine (treating explicit null as "not provided" is reasonable, not the
	// bug this test is about) and differs from PATCH below, where the
	// generic map[string]json.RawMessage decode does see a present "null"
	// and validateCustomFieldsObject correctly rejects it there.
	for _, invalid := range []string{`5`, `[1,2,3]`, `"a string"`, `true`} {
		body := fmt.Sprintf(`{"name": "cf item", "storage_id": %d, "custom_fields": %s}`, storage.ID, invalid)
		req := httptest.NewRequest(http.MethodPost, "/api/items", strings.NewReader(body))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("create with custom_fields=%s: expected 400, got %d: %s", invalid, rec.Code, rec.Body.String())
		}
	}

	// A genuine object must still work.
	body := fmt.Sprintf(`{"name": "cf item", "storage_id": %d, "custom_fields": {"color": "red"}}`, storage.ID)
	req := httptest.NewRequest(http.MethodPost, "/api/items", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create with a real object: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created ItemDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decoding created item: %v", err)
	}

	// PATCH must reject the same shapes.
	for _, invalid := range []string{`5`, `[1,2,3]`, `null`} {
		body := fmt.Sprintf(`{"custom_fields": %s}`, invalid)
		req := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/items/%d", created.ID), strings.NewReader(body))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("update with custom_fields=%s: expected 400, got %d: %s", invalid, rec.Code, rec.Body.String())
		}
	}

	// A genuine object must still work via PATCH too.
	req = httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/items/%d", created.ID), strings.NewReader(`{"custom_fields": {"color": "blue"}}`))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update with a real object: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestItemUpdateRejectsBlankName proves PATCH /api/items/:id can't blank out
// an existing item's name — the strings.TrimSpace(...) == "" check only
// ever lived in the create handler.
func TestItemUpdateRejectsBlankName(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)
	router := NewRouter(pool, t.TempDir())

	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM items WHERE qr_token = 'BLANKNAME-ITEM'`); err != nil {
			t.Logf("cleanup: deleting test item: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM storages WHERE qr_token = 'BLANKNAME-LOC'`); err != nil {
			t.Logf("cleanup: deleting test storage: %v", err)
		}
	})

	storage, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "blank name test root", QrToken: "BLANKNAME-LOC", IsShared: true,
	})
	if err != nil {
		t.Fatalf("InsertStorage: %v", err)
	}
	item, err := q.InsertItem(ctx, store.InsertItemParams{
		StorageID: storage.ID, Name: "original name", QrToken: "BLANKNAME-ITEM",
		IsShared: true, Quantity: 1, CustomFields: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("InsertItem: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/items/%d", item.ID), strings.NewReader(`{"name": "   "}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a blank name, got %d: %s", rec.Code, rec.Body.String())
	}

	got, err := q.GetItemByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetItemByID: %v", err)
	}
	if got.Name != "original name" {
		t.Fatalf("expected the name to be untouched, got %q", got.Name)
	}
}

// TestItemCreateTreatsEmptyQrTokenAsAbsent mirrors
// TestStorageCreateTreatsEmptyQrTokenAsAbsent — an explicit qr_token: ""
// must auto-generate a real code, not store the empty string. Lower stakes
// than the storages version (items.qr_token isn't unique, so there's no
// 409-masking failure mode), but an item stored with qr_token = "" would
// still be unreachable by exact-code scan/search.
func TestItemCreateTreatsEmptyQrTokenAsAbsent(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)
	router := NewRouter(pool, t.TempDir())

	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM items WHERE name = 'empty qr_token item test'`); err != nil {
			t.Logf("cleanup: deleting test items: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM storages WHERE qr_token = 'EMPTYQR-ITEM-LOC'`); err != nil {
			t.Logf("cleanup: deleting test storage: %v", err)
		}
	})

	storage, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "empty qr_token item test root", QrToken: "EMPTYQR-ITEM-LOC", IsShared: true,
	})
	if err != nil {
		t.Fatalf("InsertStorage: %v", err)
	}

	body := fmt.Sprintf(`{"name": "empty qr_token item test", "storage_id": %d, "qr_token": ""}`, storage.ID)
	req := httptest.NewRequest(http.MethodPost, "/api/items", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created ItemDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decoding created item: %v", err)
	}
	if created.QrToken == "" {
		t.Fatal("expected an auto-generated non-empty qr_token, got an empty one")
	}
}

// TestRecentItemsHandlesLargePageWithoutOverflow proves a large page number
// no longer overflows into a negative OFFSET (a bare int32 product wraps
// around well before values an HTTP client can trivially send) — Postgres
// rejects a negative OFFSET outright, which used to surface as a generic
// 500 instead of a clean, empty result page.
func TestRecentItemsHandlesLargePageWithoutOverflow(t *testing.T) {
	pool := testPool(t)
	router := NewRouter(pool, t.TempDir())

	// page * pageSize here (300000 * 10000 = 3,000,000,000) exceeds
	// int32's ~2.1 billion max — the exact shape of value that used to wrap
	// the offset negative.
	req := httptest.NewRequest(http.MethodGet, "/api/items?sort=created_desc&page=300000&pageSize=10000", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for a large page number, got %d: %s", rec.Code, rec.Body.String())
	}

	// A page value past int64's own range can't even be parsed —
	// strconv.ParseInt fails and listRecent silently keeps the page=1
	// default, which is what actually stops the (page-1)*pageSize product
	// from ever reaching a value large enough to matter, more than the
	// offset<0 clamp does. Pinning this so a future "validate inputs
	// properly" refactor doesn't turn this silent fallback into a 400 (or
	// worse, an unguarded parse) without someone noticing the behavior
	// changed.
	req = httptest.NewRequest(http.MethodGet, "/api/items?sort=created_desc&page=99999999999999999999&pageSize=10", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for a page value past int64's range, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestRecentItemsCapsPageSize proves an unbounded pageSize is clamped —
// without a cap, a single request could pull an arbitrary number of rows
// and issue one StorageBreadcrumb query per row (N+1). Needs more than
// maxRecentItemsPageSize real rows to be a meaningful assertion (the dev
// database this runs against may otherwise have too few items for the cap
// to ever actually bind), so this creates its own fixture data directly via
// the store rather than the slower create-one-per-HTTP-request route.
func TestRecentItemsCapsPageSize(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)
	router := NewRouter(pool, t.TempDir())

	storage, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "recent items cap test root", QrToken: "RECENTCAP-LOC", IsShared: true,
	})
	if err != nil {
		t.Fatalf("InsertStorage: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM items WHERE storage_id = $1`, storage.ID); err != nil {
			t.Logf("cleanup: deleting test items: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM storages WHERE id = $1`, storage.ID); err != nil {
			t.Logf("cleanup: deleting test storage: %v", err)
		}
	})

	const fixtureCount = maxRecentItemsPageSize + 5
	for i := 0; i < fixtureCount; i++ {
		if _, err := q.InsertItem(ctx, store.InsertItemParams{
			StorageID: storage.ID, Name: fmt.Sprintf("recent cap item %d", i), QrToken: codegen.PlainTextCode(),
			IsShared: true, Quantity: 1, CustomFields: []byte("{}"),
		}); err != nil {
			t.Fatalf("InsertItem %d: %v", i, err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/items?sort=created_desc&pageSize=1000000", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Entries []json.RawMessage `json:"entries"`
		Total   int64             `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(body.Entries) != maxRecentItemsPageSize {
		t.Fatalf("expected exactly %d entries (the cap) with %d real rows and pageSize=1000000, got %d", maxRecentItemsPageSize, fixtureCount, len(body.Entries))
	}
	if body.Total < fixtureCount {
		t.Fatalf("expected total to reflect the true row count (>= %d), got %d — the cap should only bound the page, not the total", fixtureCount, body.Total)
	}
}

// TestItemsQueryEscapesLikeMetacharacters proves GET /api/items?q= treats a
// literal "%" or "_" in the search term literally instead of as a SQL LIKE
// wildcard — before this fix, q=% matched every item and q=_amme_ matched
// "Hammer" regardless of what actually preceded/followed those characters.
func TestItemsQueryEscapesLikeMetacharacters(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)
	router := NewRouter(pool, t.TempDir())

	storage, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "like escape test root", QrToken: "LIKEESC-LOC", IsShared: true,
	})
	if err != nil {
		t.Fatalf("InsertStorage: %v", err)
	}
	hammer, err := q.InsertItem(ctx, store.InsertItemParams{
		StorageID: storage.ID, Name: "Hammer", QrToken: "LIKEESC-HAMMER",
		IsShared: true, Quantity: 1, CustomFields: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("InsertItem hammer: %v", err)
	}
	percentItem, err := q.InsertItem(ctx, store.InsertItemParams{
		StorageID: storage.ID, Name: "50% Off Coupon", QrToken: "LIKEESC-PERCENT",
		IsShared: true, Quantity: 1, CustomFields: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("InsertItem percent: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM items WHERE id = ANY($1)`, []int64{hammer.ID, percentItem.ID})
		_, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = $1`, storage.ID)
	})

	// A literal "%" query must not match every item — only the one that
	// actually contains a "%" character.
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/items?storage_id=%d&q=%%25", storage.ID), nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var items []ItemDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(items) != 1 || items[0].ID != percentItem.ID {
		t.Fatalf("expected q=%% to match only %q, got %+v", percentItem.Name, items)
	}

	// A literal "_amme_" query must not wildcard-match "Hammer" via _ standing
	// in for any single character — it must not match at all.
	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/items?storage_id=%d&q=_amme_", storage.ID), nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	items = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected q=_amme_ to match nothing (not wildcard-match %q), got %+v", hammer.Name, items)
	}

	// No q at all must still return everything — the escaped-q scalar
	// subquery runs regardless of whether q was provided (replace(NULL, …)
	// yields NULL, and `q IS NULL OR ...` short-circuits on the NULL check
	// before ever evaluating the NULL-valued ILIKE), so this pins that the
	// CTE didn't accidentally make the no-q path depend on it.
	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/items?storage_id=%d", storage.ID), nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	items = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected both fixture items with no q filter, got %+v", items)
	}
}

// TestItemUpdateWithTagsOnNonexistentItemReturns404 proves PATCHing tags
// onto a nonexistent item id gets a clean 404, not a raw item_tags.item_id
// foreign-key violation surfacing as a 500. A plain update with no tags
// already 404s for a nonexistent id (a no-op UPDATE affecting 0 rows isn't
// an error, but the later GetItemByID is) — this is the tags-specific
// version of the same outcome, which used to disagree.
func TestItemUpdateWithTagsOnNonexistentItemReturns404(t *testing.T) {
	pool := testPool(t)
	router := NewRouter(pool, t.TempDir())

	const bogusItemID = 999999999
	req := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/items/%d", bogusItemID), strings.NewReader(`{"tags": ["orphan-tag-test"]}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	// The tag insert happens inside the same transaction as the failing
	// LinkItemTag call — confirms it rolled back rather than leaving an
	// orphaned tag behind.
	exists, err := store.New(pool).GetTagByNameCI(context.Background(), "orphan-tag-test")
	if err == nil {
		t.Fatalf("expected the tag insert to roll back with the rest of the transaction, but it exists: %+v", exists)
	} else if !isNoRows(err) {
		t.Fatalf("GetTagByNameCI: %v", err)
	}
}

// TestItemUpdateRejectsPurchasePriceOutOfRange proves a purchase_price too
// large for NUMERIC(10,2) gets a clean 400, not a raw
// numeric_value_out_of_range surfacing as a 500.
func TestItemUpdateRejectsPurchasePriceOutOfRange(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)
	router := NewRouter(pool, t.TempDir())

	storage, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "price overflow test root", QrToken: "PRICEOVERFLOW-LOC", IsShared: true,
	})
	if err != nil {
		t.Fatalf("InsertStorage: %v", err)
	}
	item, err := q.InsertItem(ctx, store.InsertItemParams{
		StorageID: storage.ID, Name: "price overflow test item", QrToken: "PRICEOVERFLOW-ITEM",
		IsShared: true, Quantity: 1, CustomFields: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("InsertItem: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM items WHERE id = $1`, item.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = $1`, storage.ID)
	})

	// NUMERIC(10,2) allows at most 8 digits before the decimal point.
	req := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/items/%d", item.ID), strings.NewReader(`{"purchase_price": 1000000000000.00}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestItemCreateRejectsPurchasePriceOutOfRange is create's equivalent of
// TestItemUpdateRejectsPurchasePriceOutOfRange — the same NUMERIC(10,2)
// overflow can happen on create too (InsertItem, not LinkItemTag), and
// needed the identical fix.
func TestItemCreateRejectsPurchasePriceOutOfRange(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)
	router := NewRouter(pool, t.TempDir())

	storage, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "create price overflow test root", QrToken: "PRICEOVERFLOW-CREATE-LOC", IsShared: true,
	})
	if err != nil {
		t.Fatalf("InsertStorage: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM items WHERE storage_id = $1`, storage.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = $1`, storage.ID)
	})

	body := fmt.Sprintf(`{"name": "price overflow create item", "storage_id": %d, "purchase_price": 1000000000000.00}`, storage.ID)
	req := httptest.NewRequest(http.MethodPost, "/api/items", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
