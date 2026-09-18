package api

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

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
