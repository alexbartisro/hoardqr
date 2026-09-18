package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"hoardqr/internal/store"
)

// TestLocationDeleteForceRollsBackOnFailure proves the fix for
// LocationsHandler.delete's force-delete branch: DeleteItemsAtLocation and
// DeleteLocation must commit or fail together. Before this fix they were two
// separate statements outside any transaction — a failure between them
// would have permanently destroyed the items while leaving the location
// (now holding none directly) still in place. Exercises the real generated
// queries, not raw SQL, so it tracks the actual handler code path.
func TestLocationDeleteForceRollsBackOnFailure(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)

	loc, err := q.InsertLocation(ctx, store.InsertLocationParams{
		Name: "tx rollback root", QrToken: "TXTEST-LOC-ROLLBACK", IsShared: true,
	})
	if err != nil {
		t.Fatalf("InsertLocation: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM locations WHERE id = $1`, loc.ID) })

	item, err := q.InsertItem(ctx, store.InsertItemParams{
		LocationID: loc.ID, Name: "item that must survive", QrToken: "TXTEST-ITEM-ROLLBACK",
		IsShared: true, Quantity: 1, CustomFields: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("InsertItem: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM items WHERE id = $1`, item.ID) })

	sentinel := errors.New("forced failure between deleting items and deleting the location")
	err = withTx(ctx, pool, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if err := txq.DeleteItemsAtLocation(ctx, loc.ID); err != nil {
			return err
		}
		return sentinel // never reaches DeleteLocation
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}

	itemExists, err := q.ItemExists(ctx, item.ID)
	if err != nil {
		t.Fatalf("ItemExists: %v", err)
	}
	if !itemExists {
		t.Fatal("item was permanently deleted even though the transaction that deleted it was rolled back")
	}
	locExists, err := q.LocationExists(ctx, loc.ID)
	if err != nil {
		t.Fatalf("LocationExists: %v", err)
	}
	if !locExists {
		t.Fatal("location should still exist — DeleteLocation never ran")
	}
}

// TestLocationUpdateRejectsParentCycle proves the fix for the blocking issue
// found by the full-branch Opus review: PATCH /api/locations/:id previously
// accepted any parent_id, including one that made a location its own
// ancestor or descendant. Once that happens, LocationBreadcrumb and
// DescendantLocationIDs — both unbounded recursive CTEs — never terminate,
// hanging every code path that resolves a location (item/location detail,
// the dashboard feed, search, every MCP tool). Exercises the real handler
// end-to-end (not just the query layer) since the fix lives in
// LocationsHandler.update's request validation.
func TestLocationUpdateRejectsParentCycle(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)
	router := NewRouter(pool, t.TempDir())

	root, err := q.InsertLocation(ctx, store.InsertLocationParams{Name: "cycle test root", QrToken: "CYCLETEST-ROOT", IsShared: true})
	if err != nil {
		t.Fatalf("InsertLocation root: %v", err)
	}
	child, err := q.InsertLocation(ctx, store.InsertLocationParams{Name: "cycle test child", QrToken: "CYCLETEST-CHILD", IsShared: true, ParentID: &root.ID})
	if err != nil {
		t.Fatalf("InsertLocation child: %v", err)
	}
	grandchild, err := q.InsertLocation(ctx, store.InsertLocationParams{Name: "cycle test grandchild", QrToken: "CYCLETEST-GRANDCHILD", IsShared: true, ParentID: &child.ID})
	if err != nil {
		t.Fatalf("InsertLocation grandchild: %v", err)
	}
	unrelated, err := q.InsertLocation(ctx, store.InsertLocationParams{Name: "cycle test unrelated", QrToken: "CYCLETEST-UNRELATED", IsShared: true})
	if err != nil {
		t.Fatalf("InsertLocation unrelated: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM locations WHERE id = ANY($1)`, []int64{root.ID, child.ID, grandchild.ID, unrelated.ID})
	})

	patch := func(id, newParentID int64) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"parent_id": %d}`, newParentID)
		req := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/locations/%d", id), strings.NewReader(body))
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

	// A legitimate reparent onto an unrelated location must still work — the
	// guard must reject only genuine cycles, not every parent_id change.
	rec := patch(root.ID, unrelated.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("legitimate reparent: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	got, err := q.GetLocationByID(ctx, root.ID)
	if err != nil {
		t.Fatalf("GetLocationByID: %v", err)
	}
	if got.ParentID == nil || *got.ParentID != unrelated.ID {
		t.Fatalf("expected root's parent_id to be %d, got %v", unrelated.ID, got.ParentID)
	}

	// parent_id: null must still go through — it's the recovery path for an
	// already-cyclic location (set it back to root-level to break the loop),
	// and the guard's type assertion (set["parent_id"].(*int64), non-nil)
	// must keep letting a nil *int64 skip the cycle check rather than ever
	// being tightened into blocking this too.
	req := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/locations/%d", root.ID), strings.NewReader(`{"parent_id": null}`))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("parent_id:null: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	got, err = q.GetLocationByID(ctx, root.ID)
	if err != nil {
		t.Fatalf("GetLocationByID: %v", err)
	}
	if got.ParentID != nil {
		t.Fatalf("expected root's parent_id to be cleared, got %v", *got.ParentID)
	}
}

// TestRecursiveLocationQueriesTerminateOnCycle is the defense-in-depth half
// of the cycle fix: even if a cyclic parent_id chain exists anyway (data
// predating the guard above, or a direct DB edit), LocationBreadcrumb and
// DescendantLocationIDs must still return instead of hanging forever. Forces
// a cycle directly via raw SQL — bypassing LocationsHandler.update entirely,
// since the whole point is to prove these queries don't depend on that guard
// to terminate — then asserts both queries return within a generous timeout
// and with a row count bounded by the number of locations actually involved
// (not unbounded growth).
func TestRecursiveLocationQueriesTerminateOnCycle(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)

	a, err := q.InsertLocation(ctx, store.InsertLocationParams{Name: "cycle raw a", QrToken: "CYCLERAW-A", IsShared: true})
	if err != nil {
		t.Fatalf("InsertLocation a: %v", err)
	}
	b, err := q.InsertLocation(ctx, store.InsertLocationParams{Name: "cycle raw b", QrToken: "CYCLERAW-B", IsShared: true, ParentID: &a.ID})
	if err != nil {
		t.Fatalf("InsertLocation b: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM locations WHERE id = ANY($1)`, []int64{a.ID, b.ID})
	})

	// Force the cycle directly — a is b's child and b is a's child.
	if _, err := pool.Exec(ctx, `UPDATE locations SET parent_id = $1 WHERE id = $2`, b.ID, a.ID); err != nil {
		t.Fatalf("forcing cycle: %v", err)
	}

	// A context timeout (rather than a goroutine + time.After) makes Postgres
	// itself cancel a genuinely-hanging query instead of leaving it running
	// after the test moves on — a leaked query would otherwise pin a pool
	// connection and make the eventual t.Cleanup(pool.Close) hang too,
	// muddying the real failure with a second one.
	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	breadcrumb, err := q.LocationBreadcrumb(timeoutCtx, a.ID)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("LocationBreadcrumb did not terminate against a cyclic parent_id chain")
		}
		t.Fatalf("LocationBreadcrumb: %v", err)
	}
	descendants, err := q.DescendantLocationIDs(timeoutCtx, a.ID)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("DescendantLocationIDs did not terminate against a cyclic parent_id chain")
		}
		t.Fatalf("DescendantLocationIDs: %v", err)
	}

	if len(breadcrumb) > 2 {
		t.Fatalf("expected LocationBreadcrumb to stop after visiting the 2 locations in the cycle, got %d rows", len(breadcrumb))
	}
	if len(descendants) > 2 {
		t.Fatalf("expected DescendantLocationIDs to stop after visiting the 2 locations in the cycle, got %d rows", len(descendants))
	}
}
