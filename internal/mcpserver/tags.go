package mcpserver

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"hoardqr/internal/store"
)

// resolveTagIDs is internal/api/items.go's resolveTagIDs helper, duplicated
// rather than shared — same reasoning as tx.go's withTx/isConflict: small,
// single-purpose, and each package's own call site. Upserts-by-
// case-insensitive-name every tag given, creating rows for any that don't
// exist yet (matches the web UI's create-on-the-fly tag autocomplete).
// Blank/whitespace-only names are silently skipped rather than rejected —
// add_item's own name check already rejects an outright blank required
// field; a stray blank entry in a tags list is far more likely to be
// caller sloppiness (a trailing comma) than a deliberate request worth
// erroring over.
func resolveTagIDs(ctx context.Context, q *store.Queries, names []string) ([]int64, error) {
	ids := make([]int64, 0, len(names))
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		tag, err := q.GetTagByNameCI(ctx, trimmed)
		if errors.Is(err, pgx.ErrNoRows) {
			tag, err = q.InsertTag(ctx, trimmed)
		}
		if err != nil {
			return nil, err
		}
		ids = append(ids, tag.ID)
	}
	return ids, nil
}
