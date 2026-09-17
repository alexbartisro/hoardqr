// Package api implements the REST surface from architecture plan §9 (plus
// the extra endpoints lib/api.ts's mock needed — see CLAUDE.md's "Mock API
// surface beyond §9") on top of chi and the sqlc-generated store package.
package api

import (
	"encoding/json"
	"errors"
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

// pgConflict reports whether err is a Postgres unique-violation (23505),
// e.g. a duplicate locations.qr_token — the DB's own constraint is the
// source of truth here, not a separate SELECT-then-INSERT check, which
// would leave a race window between the check and the insert.
func pgConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// isNoRows reports whether err is pgx's "no rows in result set" — the
// standard way a :one sqlc query signals "not found".
func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
