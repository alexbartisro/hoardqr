package api

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hoardqr/internal/db"
)

// testPool connects to DATABASE_URL_TEST (falling back to DATABASE_URL) and
// skips the test if neither is set — there's no CI database wired up yet,
// so this only runs when a developer has one available locally.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL_TEST")
	if url == "" {
		url = os.Getenv("DATABASE_URL")
	}
	if url == "" {
		t.Skip("DATABASE_URL(_TEST) not set — skipping test that needs a real Postgres")
	}
	pool, err := db.Connect(context.Background(), url)
	if err != nil {
		t.Fatalf("connecting to test database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestWithTxRollsBackOnError proves the actual bug this fixes: a
// multi-statement write where a later statement fails must undo the earlier
// one, not leave a half-written row behind. Inserts a location, then forces
// an error before commit, and asserts the location was never persisted.
func TestWithTxRollsBackOnError(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	const tokenA = "TXTEST-ROLLBACK-A"
	sentinel := errors.New("forced failure after the insert")

	err := withTx(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO locations (name, qr_token) VALUES ($1, $2)`, "tx rollback test", tokenA,
		); err != nil {
			return err
		}
		return sentinel // simulates the second statement in a real handler failing
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM locations WHERE qr_token = $1`, tokenA).Scan(&count); err != nil {
		t.Fatalf("querying: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected the insert to be rolled back, but found %d row(s)", count)
	}
}

// TestWithTxCommitsOnSuccess is the positive case alongside the rollback
// test above — withTx must not roll back a fn that returns nil.
func TestWithTxCommitsOnSuccess(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	const tokenB = "TXTEST-COMMIT-B"

	err := withTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO locations (name, qr_token) VALUES ($1, $2)`, "tx commit test", tokenB,
		)
		return err
	})
	if err != nil {
		t.Fatalf("withTx: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM locations WHERE qr_token = $1`, tokenB)
	})

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM locations WHERE qr_token = $1`, tokenB).Scan(&count); err != nil {
		t.Fatalf("querying: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected the insert to be committed, found %d row(s)", count)
	}
}
