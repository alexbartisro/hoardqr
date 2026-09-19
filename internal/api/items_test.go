package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
