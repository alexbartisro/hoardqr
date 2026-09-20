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

// TestLocationCreateAndList proves the basic create -> list round trip and
// that names are returned alphabetically (ListLocations' ORDER BY name).
func TestLocationCreateAndList(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	router := NewRouter(pool, t.TempDir())

	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM locations WHERE name LIKE 'LOCTEST %'`); err != nil {
			t.Logf("cleanup: deleting test locations: %v", err)
		}
	})

	for _, name := range []string{"LOCTEST Garage", "LOCTEST Apartment"} {
		body := fmt.Sprintf(`{"name": %q}`, name)
		req := httptest.NewRequest(http.MethodPost, "/api/locations", strings.NewReader(body))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201 creating %q, got %d: %s", name, rec.Code, rec.Body.String())
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/locations", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var locations []LocationDTO
	decodeJSON(t, rec, &locations)
	var names []string
	for _, l := range locations {
		if strings.HasPrefix(l.Name, "LOCTEST ") {
			names = append(names, l.Name)
		}
	}
	if len(names) != 2 || names[0] != "LOCTEST Apartment" || names[1] != "LOCTEST Garage" {
		t.Fatalf("expected [LOCTEST Apartment, LOCTEST Garage] in that order, got %v", names)
	}
}

// TestLocationCreateDuplicateNameReturns409 proves locations.name's UNIQUE
// constraint is translated into a clean 409, not a raw 500 — same pattern
// as storages.qr_token's create handler.
func TestLocationCreateDuplicateNameReturns409(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	router := NewRouter(pool, t.TempDir())

	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM locations WHERE name = 'LOCTEST Duplicate House'`); err != nil {
			t.Logf("cleanup: deleting test location: %v", err)
		}
	})

	body := `{"name": "LOCTEST Duplicate House"}`
	req := httptest.NewRequest(http.MethodPost, "/api/locations", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for the first create, got %d: %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/locations", strings.NewReader(body))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for the duplicate name, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestLocationUpdateRename proves PATCH's one editable field works and
// rejects a blank name the same way storages/items do.
func TestLocationUpdateRename(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)
	router := NewRouter(pool, t.TempDir())

	location, err := q.InsertLocation(ctx, store.InsertLocationParams{Name: "LOCTEST Before Rename", IsShared: true})
	if err != nil {
		t.Fatalf("InsertLocation: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM locations WHERE id = $1`, location.ID) })

	req := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/locations/%d", location.ID), strings.NewReader(`{"name": "LOCTEST After Rename"}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got LocationDTO
	decodeJSON(t, rec, &got)
	if got.Name != "LOCTEST After Rename" {
		t.Fatalf("expected renamed location, got %+v", got)
	}

	req = httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/locations/%d", location.ID), strings.NewReader(`{"name": "   "}`))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a blank name, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestLocationDeleteBlockedThenForced proves the two-step delete: a 409
// naming the assigned-storage count when storages still reference the
// location, then a successful force=true that clears the assignment
// (storage becomes unassigned, not deleted) and removes the location.
func TestLocationDeleteBlockedThenForced(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)
	router := NewRouter(pool, t.TempDir())

	location, err := q.InsertLocation(ctx, store.InsertLocationParams{Name: "LOCTEST Delete Me", IsShared: true})
	if err != nil {
		t.Fatalf("InsertLocation: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM locations WHERE id = $1`, location.ID) })

	storage, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "LOCTEST assigned storage", QrToken: "LOCDEL-STORAGE", IsShared: true, LocationID: &location.ID,
	})
	if err != nil {
		t.Fatalf("InsertStorage: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = $1`, storage.ID) })

	req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/locations/%d", location.ID), nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 with a storage still assigned, got %d: %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/locations/%d?force=true", location.ID), nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for force delete, got %d: %s", rec.Code, rec.Body.String())
	}

	stillExists, err := q.LocationExists(ctx, location.ID)
	if err != nil {
		t.Fatalf("LocationExists: %v", err)
	}
	if stillExists {
		t.Fatal("expected the location to be gone after force delete")
	}

	updated, err := q.GetStorageByID(ctx, storage.ID)
	if err != nil {
		t.Fatalf("GetStorageByID: %v", err)
	}
	if updated.LocationID != nil {
		t.Fatalf("expected the storage to be unassigned (not deleted), got location_id %v", updated.LocationID)
	}
}

// TestLocationCreateRejectsCaseVariantDuplicate proves migration 000005's
// idx_locations_name_ci — a real bug found in review: locations.name's
// plain UNIQUE (migration 000004) is case-sensitive, but resolveLocation
// (internal/mcpserver/resolve.go) and the frontend both treat names
// case-insensitively. Without the case-insensitive index, "House" and
// "house" could both be created, and add_storage's location param — a
// write, not just a lookup — would silently resolve to whichever one
// Postgres happened to return first for a case-insensitive match.
func TestLocationCreateRejectsCaseVariantDuplicate(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	router := NewRouter(pool, t.TempDir())

	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM locations WHERE lower(name) = lower('LOCTEST CaseVariant House')`); err != nil {
			t.Logf("cleanup: deleting test locations: %v", err)
		}
	})

	req := httptest.NewRequest(http.MethodPost, "/api/locations", strings.NewReader(`{"name": "LOCTEST CaseVariant House"}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for the first create, got %d: %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/locations", strings.NewReader(`{"name": "loctest casevariant house"}`))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for a case-variant duplicate name, got %d: %s", rec.Code, rec.Body.String())
	}
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.NewDecoder(rec.Body).Decode(v); err != nil {
		t.Fatalf("decoding response body: %v (body: %s)", err, rec.Body.String())
	}
}
