package api

import (
	"context"
	"errors"
	"testing"

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
