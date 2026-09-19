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
	// deleted before the location (items.location_id has no promotion logic
	// via a raw DELETE, unlike LocationsHandler.delete's real endpoint).
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM items WHERE name = 'cf item'`); err != nil {
			t.Logf("cleanup: deleting test items: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM locations WHERE qr_token = 'CFTEST-LOC'`); err != nil {
			t.Logf("cleanup: deleting test location: %v", err)
		}
	})

	loc, err := q.InsertLocation(ctx, store.InsertLocationParams{
		Name: "custom fields test root", QrToken: "CFTEST-LOC", IsShared: true,
	})
	if err != nil {
		t.Fatalf("InsertLocation: %v", err)
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
		body := fmt.Sprintf(`{"name": "cf item", "location_id": %d, "custom_fields": %s}`, loc.ID, invalid)
		req := httptest.NewRequest(http.MethodPost, "/api/items", strings.NewReader(body))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("create with custom_fields=%s: expected 400, got %d: %s", invalid, rec.Code, rec.Body.String())
		}
	}

	// A genuine object must still work.
	body := fmt.Sprintf(`{"name": "cf item", "location_id": %d, "custom_fields": {"color": "red"}}`, loc.ID)
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
		if _, err := pool.Exec(ctx, `DELETE FROM locations WHERE qr_token = 'BLANKNAME-LOC'`); err != nil {
			t.Logf("cleanup: deleting test location: %v", err)
		}
	})

	loc, err := q.InsertLocation(ctx, store.InsertLocationParams{
		Name: "blank name test root", QrToken: "BLANKNAME-LOC", IsShared: true,
	})
	if err != nil {
		t.Fatalf("InsertLocation: %v", err)
	}
	item, err := q.InsertItem(ctx, store.InsertItemParams{
		LocationID: loc.ID, Name: "original name", QrToken: "BLANKNAME-ITEM",
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
// TestLocationCreateTreatsEmptyQrTokenAsAbsent — an explicit qr_token: ""
// must auto-generate a real code, not store the empty string. Lower stakes
// than the locations version (items.qr_token isn't unique, so there's no
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
		if _, err := pool.Exec(ctx, `DELETE FROM locations WHERE qr_token = 'EMPTYQR-ITEM-LOC'`); err != nil {
			t.Logf("cleanup: deleting test location: %v", err)
		}
	})

	loc, err := q.InsertLocation(ctx, store.InsertLocationParams{
		Name: "empty qr_token item test root", QrToken: "EMPTYQR-ITEM-LOC", IsShared: true,
	})
	if err != nil {
		t.Fatalf("InsertLocation: %v", err)
	}

	body := fmt.Sprintf(`{"name": "empty qr_token item test", "location_id": %d, "qr_token": ""}`, loc.ID)
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
// and issue one LocationBreadcrumb query per row (N+1). Needs more than
// maxRecentItemsPageSize real rows to be a meaningful assertion (the dev
// database this runs against may otherwise have too few items for the cap
// to ever actually bind), so this creates its own fixture data directly via
// the store rather than the slower create-one-per-HTTP-request route.
func TestRecentItemsCapsPageSize(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)
	router := NewRouter(pool, t.TempDir())

	loc, err := q.InsertLocation(ctx, store.InsertLocationParams{
		Name: "recent items cap test root", QrToken: "RECENTCAP-LOC", IsShared: true,
	})
	if err != nil {
		t.Fatalf("InsertLocation: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM items WHERE location_id = $1`, loc.ID); err != nil {
			t.Logf("cleanup: deleting test items: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM locations WHERE id = $1`, loc.ID); err != nil {
			t.Logf("cleanup: deleting test location: %v", err)
		}
	})

	const fixtureCount = maxRecentItemsPageSize + 5
	for i := 0; i < fixtureCount; i++ {
		if _, err := q.InsertItem(ctx, store.InsertItemParams{
			LocationID: loc.ID, Name: fmt.Sprintf("recent cap item %d", i), QrToken: codegen.PlainTextCode(),
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

	loc, err := q.InsertLocation(ctx, store.InsertLocationParams{
		Name: "like escape test root", QrToken: "LIKEESC-LOC", IsShared: true,
	})
	if err != nil {
		t.Fatalf("InsertLocation: %v", err)
	}
	hammer, err := q.InsertItem(ctx, store.InsertItemParams{
		LocationID: loc.ID, Name: "Hammer", QrToken: "LIKEESC-HAMMER",
		IsShared: true, Quantity: 1, CustomFields: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("InsertItem hammer: %v", err)
	}
	percentItem, err := q.InsertItem(ctx, store.InsertItemParams{
		LocationID: loc.ID, Name: "50% Off Coupon", QrToken: "LIKEESC-PERCENT",
		IsShared: true, Quantity: 1, CustomFields: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("InsertItem percent: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM items WHERE id = ANY($1)`, []int64{hammer.ID, percentItem.ID})
		_, _ = pool.Exec(ctx, `DELETE FROM locations WHERE id = $1`, loc.ID)
	})

	// A literal "%" query must not match every item — only the one that
	// actually contains a "%" character.
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/items?location_id=%d&q=%%25", loc.ID), nil)
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
	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/items?location_id=%d&q=_amme_", loc.ID), nil)
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
	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/items?location_id=%d", loc.ID), nil)
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
