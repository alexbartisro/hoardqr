package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewRouter mounts every handler under /api and the plain liveness check at
// /healthz. Anything not matched here (the SPA's own routes) is left to the
// caller — see cmd/hoardqr/main.go's spaHandler fallback.
func NewRouter(pool *pgxpool.Pool) chi.Router {
	r := chi.NewRouter()

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	r.Route("/api/locations", NewLocationsHandler(pool).Routes)
	r.Route("/api/items", NewItemsHandler(pool).Routes)
	r.Route("/api/tags", NewTagsHandler(pool).Routes)

	return r
}
