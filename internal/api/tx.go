package api

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// errHandled is a sentinel a withTx callback can return to mean "roll back
// (nothing destructive has happened, or shouldn't be kept), but the caller
// has already written its own status/body — don't also write a 500". Used
// for control-flow exits like 404/409 discovered partway through a
// transactional handler.
var errHandled = errors.New("response already written")

// withTx runs fn inside a transaction, committing on success and rolling
// back on any error (or panic — pgx.Tx.Rollback is always safe to call after
// Commit, it's just a no-op then). Any handler doing more than one write
// statement should use this: without it, e.g. an item create that inserts
// the row but fails partway through linking its tags leaves a real item
// with only some of its tags attached, rather than either fully succeeding
// or fully not existing.
func withTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
