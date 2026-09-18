package mcpserver

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// withTx runs fn in a transaction, committing on success and rolling back
// otherwise — move_item is the one tool here doing more than one write
// (reassign the item, then record it in audit_log), and without this a
// failure between the two would move an item with no audit trail of it.
// Same pattern as internal/api/tx.go's withTx; duplicated rather than
// shared since it's the only call site in this package and the two
// packages otherwise have no reason to depend on each other.
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

// isConflict reports whether err is a Postgres unique-violation (23505) —
// used by add_location's retry-on-generated-code-collision loop, same
// reasoning as internal/api/locations.go's create handler.
func isConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
