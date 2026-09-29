package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hoardqr/internal/store"
)

// TestStorageDeletePromoteRollsBackOnFailure proves the delete transaction
// still commits-or-fails-together now that the destructive force branch is
// gone (see TestStorageDeleteWithDirectItemsAlwaysConflicts below) — the
// remaining multi-statement path (PromoteItemsToParent, then DeleteStorage)
// is still real: a failure between the two would otherwise leave an item
// already moved to the parent while the child storage it was promoted out
// of is still sitting there, unpromoted-looking to anything that reads it
// again. Exercises the real generated queries, not raw SQL, so it tracks
// the actual handler code path.
func TestStorageDeletePromoteRollsBackOnFailure(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)

	root, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "tx rollback promote root", QrToken: "TXTEST-PROMOTE-ROOT", IsShared: true,
	})
	if err != nil {
		t.Fatalf("InsertStorage(root): %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = $1`, root.ID) })

	child, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "tx rollback promote child", QrToken: "TXTEST-PROMOTE-CHILD", IsShared: true, ParentID: &root.ID,
	})
	if err != nil {
		t.Fatalf("InsertStorage(child): %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = $1`, child.ID) })

	item, err := q.InsertItem(ctx, store.InsertItemParams{
		StorageID: child.ID, Name: "item that must not move", QrToken: "TXTEST-ITEM-PROMOTE",
		IsShared: true, Quantity: 1, CustomFields: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("InsertItem: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM items WHERE id = $1`, item.ID) })

	sentinel := errors.New("forced failure between promoting items and deleting the storage")
	err = withTx(ctx, pool, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if err := txq.PromoteItemsToParent(ctx, store.PromoteItemsToParentParams{
			NewStorageID: root.ID, OldStorageID: child.ID,
		}); err != nil {
			return err
		}
		return sentinel // never reaches DeleteStorage
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}

	reloaded, err := q.GetItemByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetItemByID: %v", err)
	}
	if reloaded.StorageID != child.ID {
		t.Fatalf("item was permanently promoted to the parent even though the transaction that promoted it was rolled back (storage_id = %d, want %d)", reloaded.StorageID, child.ID)
	}
	childExists, err := q.StorageExists(ctx, child.ID)
	if err != nil {
		t.Fatalf("StorageExists: %v", err)
	}
	if !childExists {
		t.Fatal("child storage should still exist — DeleteStorage never ran")
	}
}

// TestStorageDeleteWithDirectItemsAlwaysConflicts proves the current rule:
// a root storage holding items directly always 409s on delete, with no
// override — deleting a storage must never delete the items inside it
// (user decision, 2026-09-20, reversing the old `?force=true` escape hatch
// that used to delete those items outright; see CLAUDE.md). Exercises the
// real handler end-to-end, including that a `force=true` query param (a
// caller might still send it out of habit, or from stale API docs) is
// silently ignored rather than treated as a real toggle.
func TestStorageDeleteWithDirectItemsAlwaysConflicts(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)
	router := NewRouter(pool, t.TempDir())

	storage, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "delete-blocked root", QrToken: "TXTEST-LOC-NOFORCE", IsShared: true,
	})
	if err != nil {
		t.Fatalf("InsertStorage: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = $1`, storage.ID) })

	item, err := q.InsertItem(ctx, store.InsertItemParams{
		StorageID: storage.ID, Name: "item that must survive", QrToken: "TXTEST-ITEM-NOFORCE",
		IsShared: true, Quantity: 1, CustomFields: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("InsertItem: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM items WHERE id = $1`, item.ID) })

	req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/storages/%d?force=true", storage.ID), nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 even with force=true, got %d: %s", rec.Code, rec.Body.String())
	}

	itemExists, err := q.ItemExists(ctx, item.ID)
	if err != nil {
		t.Fatalf("ItemExists: %v", err)
	}
	if !itemExists {
		t.Fatal("item was deleted — force=true must no longer do this")
	}
	storageExists, err := q.StorageExists(ctx, storage.ID)
	if err != nil {
		t.Fatalf("StorageExists: %v", err)
	}
	if !storageExists {
		t.Fatal("storage should still exist — the delete must have been rejected")
	}
}

// TestStorageUpdateRejectsParentCycle proves the fix for the blocking issue
// found by the full-branch Opus review: PATCH /api/storages/:id previously
// accepted any parent_id, including one that made a storage its own
// ancestor or descendant. Once that happens, StorageBreadcrumb and
// DescendantStorageIDs — both unbounded recursive CTEs — never terminate,
// hanging every code path that resolves a storage (item/storage detail,
// the dashboard feed, search, every MCP tool). Exercises the real handler
// end-to-end (not just the query layer) since the fix lives in
// StoragesHandler.update's request validation.
func TestStorageUpdateRejectsParentCycle(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)
	router := NewRouter(pool, t.TempDir())

	root, err := q.InsertStorage(ctx, store.InsertStorageParams{Name: "cycle test root", QrToken: "CYCLETEST-ROOT", IsShared: true})
	if err != nil {
		t.Fatalf("InsertStorage root: %v", err)
	}
	child, err := q.InsertStorage(ctx, store.InsertStorageParams{Name: "cycle test child", QrToken: "CYCLETEST-CHILD", IsShared: true, ParentID: &root.ID})
	if err != nil {
		t.Fatalf("InsertStorage child: %v", err)
	}
	grandchild, err := q.InsertStorage(ctx, store.InsertStorageParams{Name: "cycle test grandchild", QrToken: "CYCLETEST-GRANDCHILD", IsShared: true, ParentID: &child.ID})
	if err != nil {
		t.Fatalf("InsertStorage grandchild: %v", err)
	}
	unrelated, err := q.InsertStorage(ctx, store.InsertStorageParams{Name: "cycle test unrelated", QrToken: "CYCLETEST-UNRELATED", IsShared: true})
	if err != nil {
		t.Fatalf("InsertStorage unrelated: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = ANY($1)`, []int64{root.ID, child.ID, grandchild.ID, unrelated.ID})
	})

	patch := func(id, newParentID int64) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"parent_id": %d}`, newParentID)
		req := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/storages/%d", id), strings.NewReader(body))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	if rec := patch(root.ID, root.ID); rec.Code != http.StatusConflict {
		t.Fatalf("self-parent: expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := patch(root.ID, grandchild.ID); rec.Code != http.StatusConflict {
		t.Fatalf("descendant-parent: expected 409, got %d: %s", rec.Code, rec.Body.String())
	}

	// A legitimate reparent onto an unrelated storage must still work — the
	// guard must reject only genuine cycles, not every parent_id change.
	rec := patch(root.ID, unrelated.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("legitimate reparent: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	got, err := q.GetStorageByID(ctx, root.ID)
	if err != nil {
		t.Fatalf("GetStorageByID: %v", err)
	}
	if got.ParentID == nil || *got.ParentID != unrelated.ID {
		t.Fatalf("expected root's parent_id to be %d, got %v", unrelated.ID, got.ParentID)
	}

	// parent_id: null must still go through — it's the recovery path for an
	// already-cyclic storage (set it back to root-level to break the loop),
	// and the guard's type assertion (set["parent_id"].(*int64), non-nil)
	// must keep letting a nil *int64 skip the cycle check rather than ever
	// being tightened into blocking this too.
	req := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/storages/%d", root.ID), strings.NewReader(`{"parent_id": null}`))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("parent_id:null: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	got, err = q.GetStorageByID(ctx, root.ID)
	if err != nil {
		t.Fatalf("GetStorageByID: %v", err)
	}
	if got.ParentID != nil {
		t.Fatalf("expected root's parent_id to be cleared, got %v", *got.ParentID)
	}
}

// TestRecursiveStorageQueriesTerminateOnCycle is the defense-in-depth half
// of the cycle fix: even if a cyclic parent_id chain exists anyway (data
// predating the guard above, or a direct DB edit), StorageBreadcrumb and
// DescendantStorageIDs must still return instead of hanging forever. Forces
// a cycle directly via raw SQL — bypassing StoragesHandler.update entirely,
// since the whole point is to prove these queries don't depend on that guard
// to terminate — then asserts both queries return within a generous timeout
// and with a row count bounded by the number of storages actually involved
// (not unbounded growth).
func TestRecursiveStorageQueriesTerminateOnCycle(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)

	a, err := q.InsertStorage(ctx, store.InsertStorageParams{Name: "cycle raw a", QrToken: "CYCLERAW-A", IsShared: true})
	if err != nil {
		t.Fatalf("InsertStorage a: %v", err)
	}
	b, err := q.InsertStorage(ctx, store.InsertStorageParams{Name: "cycle raw b", QrToken: "CYCLERAW-B", IsShared: true, ParentID: &a.ID})
	if err != nil {
		t.Fatalf("InsertStorage b: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = ANY($1)`, []int64{a.ID, b.ID})
	})

	// Force the cycle directly — a is b's child and b is a's child.
	if _, err := pool.Exec(ctx, `UPDATE storages SET parent_id = $1 WHERE id = $2`, b.ID, a.ID); err != nil {
		t.Fatalf("forcing cycle: %v", err)
	}

	// A context timeout (rather than a goroutine + time.After) makes Postgres
	// itself cancel a genuinely-hanging query instead of leaving it running
	// after the test moves on — a leaked query would otherwise pin a pool
	// connection and make the eventual t.Cleanup(pool.Close) hang too,
	// muddying the real failure with a second one.
	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	breadcrumb, err := q.StorageBreadcrumb(timeoutCtx, a.ID)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("StorageBreadcrumb did not terminate against a cyclic parent_id chain")
		}
		t.Fatalf("StorageBreadcrumb: %v", err)
	}
	descendants, err := q.DescendantStorageIDs(timeoutCtx, a.ID)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("DescendantStorageIDs did not terminate against a cyclic parent_id chain")
		}
		t.Fatalf("DescendantStorageIDs: %v", err)
	}

	if len(breadcrumb) > 2 {
		t.Fatalf("expected StorageBreadcrumb to stop after visiting the 2 storages in the cycle, got %d rows", len(breadcrumb))
	}
	if len(descendants) > 2 {
		t.Fatalf("expected DescendantStorageIDs to stop after visiting the 2 storages in the cycle, got %d rows", len(descendants))
	}
}

// TestStorageCreateTreatsEmptyQrTokenAsAbsent proves an explicit
// qr_token: "" is treated the same as an absent qr_token (auto-generate a
// real code), not as "the caller's chosen token happens to be the empty
// string". Before this fix, "" occupied storages.qr_token's unique slot,
// so a second storage also sent with qr_token: "" would 409 against the
// first one's "" rather than each getting its own distinct generated code.
func TestStorageCreateTreatsEmptyQrTokenAsAbsent(t *testing.T) {
	pool := testPool(t)
	router := NewRouter(pool, t.TempDir())

	// Swept by name unconditionally, not by ID collected after each
	// successful create — a t.Fatalf on an unexpected assertion (exactly
	// the failure mode this test guards against) would otherwise skip the
	// cleanup registration for that row entirely. Bit this once already
	// (a stray row cleaned up by hand during a revert-check) — see the
	// same reasoning in TestItemsRejectNonObjectCustomFields.
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM storages WHERE name = 'empty qr_token test'`); err != nil {
			t.Logf("cleanup: deleting test storages: %v", err)
		}
	})

	create := func() (*httptest.ResponseRecorder, StorageDTO) {
		body := `{"name": "empty qr_token test", "qr_token": ""}`
		req := httptest.NewRequest(http.MethodPost, "/api/storages", strings.NewReader(body))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		var loc StorageDTO
		if rec.Code == http.StatusCreated {
			if err := json.Unmarshal(rec.Body.Bytes(), &loc); err != nil {
				t.Fatalf("decoding created storage: %v", err)
			}
		}
		return rec, loc
	}

	rec1, storage1 := create()
	if rec1.Code != http.StatusCreated {
		t.Fatalf("first create: expected 201, got %d: %s", rec1.Code, rec1.Body.String())
	}
	if storage1.QrToken == "" {
		t.Fatal("expected an auto-generated non-empty qr_token, got an empty one")
	}

	// A second storage, also sent with qr_token: "", must succeed with its
	// own distinct generated code — not 409 against the first one's "" as
	// if both had explicitly chosen the same empty string.
	rec2, storage2 := create()
	if rec2.Code != http.StatusCreated {
		t.Fatalf("second create: expected 201, got %d: %s", rec2.Code, rec2.Body.String())
	}
	if storage2.QrToken == "" {
		t.Fatal("expected an auto-generated non-empty qr_token, got an empty one")
	}
	if storage2.QrToken == storage1.QrToken {
		t.Fatalf("expected two distinct generated codes, got the same one twice: %q", storage1.QrToken)
	}
}

// TestStorageUpdateRejectsBlankName proves PATCH /api/storages/:id can't
// blank out an existing storage's name — the strings.TrimSpace(...) == ""
// check only ever lived in the create handler.
func TestStorageUpdateRejectsBlankName(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)
	router := NewRouter(pool, t.TempDir())

	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM storages WHERE qr_token = 'BLANKNAME-PATCH-LOC'`); err != nil {
			t.Logf("cleanup: deleting test storage: %v", err)
		}
	})

	storage, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "original name", QrToken: "BLANKNAME-PATCH-LOC", IsShared: true,
	})
	if err != nil {
		t.Fatalf("InsertStorage: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/storages/%d", storage.ID), strings.NewReader(`{"name": "   "}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a blank name, got %d: %s", rec.Code, rec.Body.String())
	}

	got, err := q.GetStorageByID(ctx, storage.ID)
	if err != nil {
		t.Fatalf("GetStorageByID: %v", err)
	}
	if got.Name != "original name" {
		t.Fatalf("expected the name to be untouched, got %q", got.Name)
	}
}

// TestStorageCreateRejectsNonexistentParent proves a nonexistent parent_id
// gets a clean 422, not a raw FK violation surfacing as a 500 — mirrors
// ItemsHandler's existing StorageExists pre-check for storage_id, closing
// the gap where storages and items used to disagree on this. Split from
// the update version below (rather than one test covering both) so a
// failure in one half doesn't prevent the other half from ever running via
// t.Fatalf's early return — a revert-check against only the create fix
// would otherwise never exercise the update path.
func TestStorageCreateRejectsNonexistentParent(t *testing.T) {
	pool := testPool(t)
	router := NewRouter(pool, t.TempDir())

	const bogusParentID = 999999999
	body := fmt.Sprintf(`{"name": "orphan create test", "parent_id": %d}`, bogusParentID)
	req := httptest.NewRequest(http.MethodPost, "/api/storages", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for a nonexistent parent_id, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestStorageUpdateRejectsNonexistentParent is
// TestStorageCreateRejectsNonexistentParent's PATCH equivalent — see its
// comment for why this is a separate test rather than a second half.
func TestStorageUpdateRejectsNonexistentParent(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)
	router := NewRouter(pool, t.TempDir())

	storage, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "orphan update test", QrToken: "ORPHANPARENT-LOC", IsShared: true,
	})
	if err != nil {
		t.Fatalf("InsertStorage: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM storages WHERE id = $1`, storage.ID); err != nil {
			t.Logf("cleanup: deleting test storage: %v", err)
		}
	})

	const bogusParentID = 999999999
	body := fmt.Sprintf(`{"parent_id": %d}`, bogusParentID)
	req := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/storages/%d", storage.ID), strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for a nonexistent parent_id, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestStorageCreateWithLocation proves a root storage can be created with a
// location assigned directly, and TestStorageCreateWithParentAndLocationRejected
// proves the two are mutually exclusive at create time (migration 000004's
// storages_location_only_on_root, pre-checked for a clean 422 rather than a
// raw 23514).
func TestStorageCreateWithLocation(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)
	router := NewRouter(pool, t.TempDir())

	location, err := q.InsertLocation(ctx, store.InsertLocationParams{Name: "LOCTEST Create Root House", IsShared: true})
	if err != nil {
		t.Fatalf("InsertLocation: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM locations WHERE id = $1`, location.ID) })

	body := fmt.Sprintf(`{"name": "LOCTEST Root With Location", "location_id": %d}`, location.ID)
	req := httptest.NewRequest(http.MethodPost, "/api/storages", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var got StorageDTO
	decodeJSON(t, rec, &got)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = $1`, got.ID) })
	if got.LocationID == nil || *got.LocationID != location.ID {
		t.Fatalf("expected location_id %d on the created storage, got %+v", location.ID, got)
	}
}

func TestStorageCreateWithParentAndLocationRejected(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)
	router := NewRouter(pool, t.TempDir())

	parent, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "LOCTEST parent for rejection", QrToken: "LOCREJECT-PARENT", IsShared: true,
	})
	if err != nil {
		t.Fatalf("InsertStorage: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = $1`, parent.ID) })

	location, err := q.InsertLocation(ctx, store.InsertLocationParams{Name: "LOCTEST Reject Combo House", IsShared: true})
	if err != nil {
		t.Fatalf("InsertLocation: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM locations WHERE id = $1`, location.ID) })

	body := fmt.Sprintf(`{"name": "LOCTEST nested with location", "parent_id": %d, "location_id": %d}`, parent.ID, location.ID)
	req := httptest.NewRequest(http.MethodPost, "/api/storages", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for parent_id+location_id together, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestStorageUpdateAddingParentClearsLocation proves the silent-loss path
// design decision 1 calls out explicitly: PATCHing a non-nil parent_id onto
// a storage that currently has a location must clear location_id in the
// same UPDATE, not surface a raw check-violation.
func TestStorageUpdateAddingParentClearsLocation(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)
	router := NewRouter(pool, t.TempDir())

	location, err := q.InsertLocation(ctx, store.InsertLocationParams{Name: "LOCTEST Clear-On-Nest House", IsShared: true})
	if err != nil {
		t.Fatalf("InsertLocation: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM locations WHERE id = $1`, location.ID) })

	newParent, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "LOCTEST new parent", QrToken: "LOCCLEAR-PARENT", IsShared: true,
	})
	if err != nil {
		t.Fatalf("InsertStorage: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = $1`, newParent.ID) })

	storage, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "LOCTEST about to be nested", QrToken: "LOCCLEAR-STORAGE", IsShared: true, LocationID: &location.ID,
	})
	if err != nil {
		t.Fatalf("InsertStorage: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = $1`, storage.ID) })

	body := fmt.Sprintf(`{"parent_id": %d}`, newParent.ID)
	req := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/storages/%d", storage.ID), strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	got, err := q.GetStorageByID(ctx, storage.ID)
	if err != nil {
		t.Fatalf("GetStorageByID: %v", err)
	}
	if got.LocationID != nil {
		t.Fatalf("expected location_id to be cleared once nested under a parent, got %v", got.LocationID)
	}
}

// TestStorageDeletePropagatesLocationToPromotedChildren is the silent-loss
// path design decision 1 also calls out: deleting a root storage that has a
// location assigned must carry that location onto any direct children it
// promotes to root (the FK's ON DELETE SET NULL fires as part of the
// delete) — otherwise they'd silently become unassigned.
func TestStorageDeletePropagatesLocationToPromotedChildren(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)
	router := NewRouter(pool, t.TempDir())

	location, err := q.InsertLocation(ctx, store.InsertLocationParams{Name: "LOCTEST Propagate House", IsShared: true})
	if err != nil {
		t.Fatalf("InsertLocation: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM locations WHERE id = $1`, location.ID) })

	root, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "LOCTEST root to delete", QrToken: "LOCPROP-ROOT", IsShared: true, LocationID: &location.ID,
	})
	if err != nil {
		t.Fatalf("InsertStorage: %v", err)
	}
	// Deleting root.ID is this test's own subject, not incidental cleanup —
	// but a t.Fatalf anywhere between here and the DELETE request below
	// would otherwise leave this row behind with a fixed qr_token, failing
	// the *next* run with a misleading duplicate-key error instead of
	// whatever actually broke. Deleting an already-deleted row is a
	// harmless no-op.
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = $1`, root.ID) })

	child, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "LOCTEST child to be promoted", QrToken: "LOCPROP-CHILD", IsShared: true, ParentID: &root.ID,
	})
	if err != nil {
		t.Fatalf("InsertStorage child: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = $1`, child.ID) })

	req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/storages/%d", root.ID), nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	got, err := q.GetStorageByID(ctx, child.ID)
	if err != nil {
		t.Fatalf("GetStorageByID: %v", err)
	}
	if got.ParentID != nil {
		t.Fatalf("expected the child to be promoted to root, still has parent_id %v", got.ParentID)
	}
	if got.LocationID == nil || *got.LocationID != location.ID {
		t.Fatalf("expected the promoted child to inherit the deleted root's location %d, got %v", location.ID, got.LocationID)
	}
}

// TestStorageDeleteOfNestedStoragePropagatesEffectiveLocation is the nested
// counterpart of the test above — a real bug found in review, not just a
// hypothetical: deleting a *nested* storage (not the root itself) also
// promotes its own direct children to root, and they must inherit the
// property's *effective* location (resolved via StorageBreadcrumb's root
// walk), not the deleted storage's own LocationID column — a nested
// storage's own LocationID is always nil (storages_location_only_on_root),
// so using it directly silently dropped the promoted grandchild out of its
// Location entirely.
func TestStorageDeleteOfNestedStoragePropagatesEffectiveLocation(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)
	router := NewRouter(pool, t.TempDir())

	location, err := q.InsertLocation(ctx, store.InsertLocationParams{Name: "LOCTEST Nested Propagate House", IsShared: true})
	if err != nil {
		t.Fatalf("InsertLocation: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM locations WHERE id = $1`, location.ID) })

	root, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "LOCTEST nested root", QrToken: "LOCNESTPROP-ROOT", IsShared: true, LocationID: &location.ID,
	})
	if err != nil {
		t.Fatalf("InsertStorage root: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = $1`, root.ID) })

	middle, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "LOCTEST middle to delete", QrToken: "LOCNESTPROP-MIDDLE", IsShared: true, ParentID: &root.ID,
	})
	if err != nil {
		t.Fatalf("InsertStorage middle: %v", err)
	}
	// middle is this test's own delete subject — see the root-delete test's
	// comment above for why this needs its own t.Cleanup even though the
	// happy path deletes it via the HTTP request below.
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = $1`, middle.ID) })

	grandchild, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "LOCTEST grandchild to be promoted", QrToken: "LOCNESTPROP-GRANDCHILD", IsShared: true, ParentID: &middle.ID,
	})
	if err != nil {
		t.Fatalf("InsertStorage grandchild: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = $1`, grandchild.ID) })

	req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/storages/%d", middle.ID), nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	got, err := q.GetStorageByID(ctx, grandchild.ID)
	if err != nil {
		t.Fatalf("GetStorageByID: %v", err)
	}
	if got.ParentID != nil {
		t.Fatalf("expected the grandchild to be promoted to root, still has parent_id %v", got.ParentID)
	}
	if got.LocationID == nil || *got.LocationID != location.ID {
		t.Fatalf("expected the promoted grandchild to inherit the property's effective location %d (via the deleted nested storage's own root ancestor), got %v", location.ID, got.LocationID)
	}
}

// TestStorageLocationCheckConstraintRejectsBothColumns proves the CHECK
// constraint itself (migration 000004's storages_location_only_on_root),
// not just the handler's pre-checks — a raw insert bypassing the API must
// still be rejected by Postgres.
func TestStorageLocationCheckConstraintRejectsBothColumns(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)

	location, err := q.InsertLocation(ctx, store.InsertLocationParams{Name: "LOCTEST Constraint House", IsShared: true})
	if err != nil {
		t.Fatalf("InsertLocation: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM locations WHERE id = $1`, location.ID) })

	parent, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "LOCTEST constraint parent", QrToken: "LOCCONSTRAINT-PARENT", IsShared: true,
	})
	if err != nil {
		t.Fatalf("InsertStorage: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = $1`, parent.ID) })

	_, err = pool.Exec(ctx,
		`INSERT INTO storages (parent_id, location_id, is_shared, name, qr_token) VALUES ($1, $2, true, 'LOCTEST should be rejected', 'LOCCONSTRAINT-BAD')`,
		parent.ID, location.ID,
	)
	if err == nil {
		_, _ = pool.Exec(ctx, `DELETE FROM storages WHERE qr_token = 'LOCCONSTRAINT-BAD'`)
		t.Fatal("expected the CHECK constraint to reject parent_id and location_id both set, got no error")
	}
	if !strings.Contains(err.Error(), "storages_location_only_on_root") {
		t.Fatalf("expected the storages_location_only_on_root constraint to fire, got: %v", err)
	}
}

// storageTreeNode mirrors StorageTreeNodeDTO for decoding in tests.
type storageTreeNode struct {
	ID           int64             `json:"id"`
	Name         string            `json:"name"`
	LocationName *string           `json:"location_name"`
	ChildCount   int               `json:"child_count"`
	Children     []storageTreeNode `json:"children"`
	Items        []struct {
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		Quantity int32  `json:"quantity"`
	} `json:"items"`
}

func getStorageTree(t *testing.T, pool *pgxpool.Pool, query string) []storageTreeNode {
	t.Helper()
	rec := httptest.NewRecorder()
	NewRouter(pool, t.TempDir()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/storages/tree"+query, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/storages/tree%s: expected 200, got %d: %s", query, rec.Code, rec.Body.String())
	}
	var roots []storageTreeNode
	if err := json.NewDecoder(rec.Body).Decode(&roots); err != nil {
		t.Fatalf("decoding tree: %v", err)
	}
	return roots
}

func findTreeRoot(roots []storageTreeNode, id int64) *storageTreeNode {
	for i := range roots {
		if roots[i].ID == id {
			return &roots[i]
		}
	}
	return nil
}

// TestStorageTreeDepthCapChildCountsAndOrder builds a 5-deep chain plus a
// sibling and checks: the depth cap truncates (children omitted) while
// child_count still reports the true number of direct children, siblings sort
// by name, a root carries its Location's name, and every children array is a
// real (possibly empty) JSON array rather than null.
func TestStorageTreeDepthCapChildCountsAndOrder(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)

	house, err := q.InsertLocation(ctx, store.InsertLocationParams{Name: "TREETEST House", IsShared: true})
	if err != nil {
		t.Fatalf("InsertLocation: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM locations WHERE id = $1`, house.ID) })

	mk := func(name string, parent *int64, loc *int64) store.Storage {
		s, err := q.InsertStorage(ctx, store.InsertStorageParams{
			Name: name, QrToken: "TREETEST-" + name, IsShared: true, ParentID: parent, LocationID: loc,
		})
		if err != nil {
			t.Fatalf("InsertStorage %s: %v", name, err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = $1`, s.ID) })
		return s
	}
	root := mk("R", nil, &house.ID)
	// Inserted out of name order on purpose: expect B before Z.
	z := mk("Z", &root.ID, nil)
	b := mk("B", &root.ID, nil)
	g := mk("G", &b.ID, nil)
	gg := mk("GG", &g.ID, nil)
	ggg := mk("GGG", &gg.ID, nil)
	gggg := mk("GGGG", &ggg.ID, nil)
	_ = mk("GGGGG", &gggg.ID, nil) // 7th level: the default must not stop at 5 or 6

	// depth=3 -> R(1) > B(2) > G(3); G's child GG is cut but counted.
	r := findTreeRoot(getStorageTree(t, pool, "?depth=3"), root.ID)
	if r == nil {
		t.Fatalf("root %d missing from tree", root.ID)
	}
	if r.LocationName == nil || *r.LocationName != "TREETEST House" {
		t.Fatalf("expected the root to carry its Location name, got %v", r.LocationName)
	}
	if r.ChildCount != 2 || len(r.Children) != 2 || r.Children[0].ID != b.ID || r.Children[1].ID != z.ID {
		t.Fatalf("expected children [B, Z] (sorted by name) with child_count 2, got %+v", r)
	}
	gn := r.Children[0].Children
	if len(gn) != 1 || gn[0].ID != g.ID {
		t.Fatalf("expected B's children to be [G], got %+v", gn)
	}
	if gn[0].ChildCount != 1 || gn[0].Children == nil || len(gn[0].Children) != 0 {
		t.Fatalf("expected G truncated at the cap: child_count 1 but an empty (non-null) children array, got %+v", gn[0])
	}
	if r.Children[1].Children == nil || len(r.Children[1].Children) != 0 || r.Children[1].ChildCount != 0 {
		t.Fatalf("expected leaf Z to have child_count 0 and an empty (non-null) children array, got %+v", r.Children[1])
	}

	// depth=1 is roots only: R is there, its children cut but counted.
	r1 := findTreeRoot(getStorageTree(t, pool, "?depth=1"), root.ID)
	if r1 == nil || len(r1.Children) != 0 || r1.Children == nil || r1.ChildCount != 2 {
		t.Fatalf("depth=1: expected R with no children but child_count 2, got %+v", r1)
	}

	// depth=5 stops at level 5 (GGG, its child GGGG cut but counted); with NO
	// depth param every level comes back, including the 6th.
	r5 := findTreeRoot(getStorageTree(t, pool, "?depth=5"), root.ID)
	if r5 == nil {
		t.Fatalf("depth=5: root %d missing from tree", root.ID)
	}
	ggg5 := r5.Children[0].Children[0].Children[0].Children[0]
	if ggg5.Name != "GGG" || len(ggg5.Children) != 0 || ggg5.ChildCount != 1 {
		t.Fatalf("depth=5: expected GGG cut with child_count 1 and no children, got %+v", ggg5)
	}
	// No depth param, and an absurdly large one (past int32), both return
	// every level — all 7 of them.
	for _, qs := range []string{"", "?depth=3000000000"} {
		rAll := findTreeRoot(getStorageTree(t, pool, qs), root.ID)
		if rAll == nil {
			t.Fatalf("%q: root %d missing from tree", qs, root.ID)
		}
		n, levels := &rAll.Children[0], 2 // R(1) > B(2)
		for len(n.Children) > 0 {
			n, levels = &n.Children[0], levels+1
		}
		if n.Name != "GGGGG" || levels != 7 {
			t.Fatalf("%q: expected the chain to reach GGGGG at level 7, ended at %s (level %d)", qs, n.Name, levels)
		}
	}
	// A root has no parent, so it must not be duplicated as a nested child anywhere.
	for _, n := range getStorageTree(t, pool, "") {
		if n.ID == b.ID {
			t.Fatalf("non-root storage B leaked into the top level")
		}
	}
}

// TestStorageTreeIncludesItems checks each node carries the items stored
// directly in it (name order, non-null array when empty), and that items of
// storages cut off by ?depth= are not attached anywhere.
func TestStorageTreeIncludesItems(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)

	mk := func(name string, parent *int64) store.Storage {
		s, err := q.InsertStorage(ctx, store.InsertStorageParams{
			Name: name, QrToken: "TREEITEMS-" + name, IsShared: true, ParentID: parent,
		})
		if err != nil {
			t.Fatalf("InsertStorage %s: %v", name, err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = $1`, s.ID) })
		return s
	}
	addItem := func(storageID int64, name string, qty int32) store.Item {
		it, err := q.InsertItem(ctx, store.InsertItemParams{
			StorageID: storageID, IsShared: true, Name: name, Quantity: qty, QrToken: "TREEITEMS-" + name, CustomFields: []byte("{}"),
		})
		if err != nil {
			t.Fatalf("InsertItem %s: %v", name, err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM items WHERE id = $1`, it.ID) })
		return it
	}
	root := mk("TIR", nil)
	child := mk("TIC", &root.ID)
	// Out of name order on purpose: expect Anvil before Zip.
	zip := addItem(root.ID, "Zip", 1)
	anvil := addItem(root.ID, "Anvil", 3)
	deep := addItem(child.ID, "Deep", 1)

	r := findTreeRoot(getStorageTree(t, pool, ""), root.ID)
	if r == nil || len(r.Items) != 2 || r.Items[0].ID != anvil.ID || r.Items[1].ID != zip.ID || r.Items[0].Quantity != 3 {
		t.Fatalf("expected root items [Anvil x3, Zip] in name order, got %+v", r)
	}
	if len(r.Children) != 1 || len(r.Children[0].Items) != 1 || r.Children[0].Items[0].ID != deep.ID {
		t.Fatalf("expected the child's own item under it, got %+v", r.Children)
	}
	if empty := r.Children[0].Children; empty == nil || len(empty) != 0 {
		t.Fatalf("expected a leaf storage's children to be an empty array, got %v", empty)
	}

	// depth=1: the root's own items stay; the child (and its item) is cut.
	r1 := findTreeRoot(getStorageTree(t, pool, "?depth=1"), root.ID)
	if r1 == nil || len(r1.Items) != 2 || len(r1.Children) != 0 {
		t.Fatalf("depth=1: expected the root's 2 items and no children, got %+v", r1)
	}
	// A storage with no items still gets an empty (non-null) array.
	lone := mk("TIL", nil)
	rl := findTreeRoot(getStorageTree(t, pool, ""), lone.ID)
	if rl == nil || rl.Items == nil || len(rl.Items) != 0 {
		t.Fatalf("expected an empty non-null items array for an empty storage, got %+v", rl)
	}
}

func TestStorageTreeRejectsBadDepth(t *testing.T) {
	pool := testPool(t)
	for _, qs := range []string{"?depth=abc", "?depth=0", "?depth=-2"} {
		rec := httptest.NewRecorder()
		NewRouter(pool, t.TempDir()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/storages/tree"+qs, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d: %s", qs, rec.Code, rec.Body.String())
		}
	}
}
