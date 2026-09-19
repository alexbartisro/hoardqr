// Package api implements the REST surface from architecture plan §9 (plus
// the extra endpoints lib/api.ts's mock needed — see CLAUDE.md's "Mock API
// surface beyond §9") on top of chi and the sqlc-generated store package.
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// errorBody is the wire shape for every non-2xx response — a real
// counterpart of the mock's ApiError(status, message) (web/src/lib/types.ts).
// The frontend's fetch wrapper (Phase 3 step 4) parses this back into an
// ApiError, so component code above lib/api.ts never has to change.
type errorBody struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorBody{Error: message})
}

func notFound(w http.ResponseWriter, entity string) {
	writeError(w, http.StatusNotFound, entity+" not found")
}

// serverError logs the real error server-side (visible via `docker logs -f`,
// step 3.a) and sends the client a generic message instead of the raw error
// text — a Postgres driver error or a Go error string is an implementation
// detail an API caller shouldn't see, and previously every 500 in this
// package sent exactly that. Every internal-error path in this package
// should go through this, not a bare writeError(w, 500, err.Error()).
func serverError(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "internal server error",
		"method", r.Method, "path", r.URL.Path, "error", err)
	writeError(w, http.StatusInternalServerError, "internal server error")
}

// pgConflict reports whether err is a Postgres unique-violation (23505),
// e.g. a duplicate locations.qr_token — the DB's own constraint is the
// source of truth here, not a separate SELECT-then-INSERT check, which
// would leave a race window between the check and the insert.
func pgConflict(err error) bool {
	return pgErrorCode(err) == "23505"
}

// pgForeignKeyViolation reports whether err is a Postgres foreign-key
// violation (23503) — used where a pre-check (like LocationExists) isn't
// the shape of the fix, e.g. items.go's PATCH: by the time LinkItemTag
// fires this, resolveTagIDs already succeeded, so the only FK left that
// could still fail is item_tags.item_id referencing an item deleted
// between this request's id being parsed and the transaction running.
func pgForeignKeyViolation(err error) bool {
	return pgErrorCode(err) == "23503"
}

// pgNumericOutOfRange reports whether err is Postgres's numeric_value_out_of_range
// (22003) — e.g. a purchase_price too large for NUMERIC(10,2).
func pgNumericOutOfRange(err error) bool {
	return pgErrorCode(err) == "22003"
}

func pgErrorCode(err error) string {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return ""
	}
	return pgErr.Code
}

// isNoRows reports whether err is pgx's "no rows in result set" — the
// standard way a :one sqlc query signals "not found".
func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
