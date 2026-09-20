package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewRouter mounts every handler under /api, uploaded-photo static serving
// at /uploads/ (§12 — separate from /api since it's plain file serving, not
// JSON), and the liveness check at /healthz (a real Postgres connectivity
// check as of Phase 3 step 7 — see HealthzHandler). Anything not matched
// here (the SPA's own routes) is left to the caller — see
// cmd/hoardqr/main.go's spaHandler fallback.
func NewRouter(pool *pgxpool.Pool, uploadDir string) chi.Router {
	r := chi.NewRouter()
	// RequestLogger outermost, Recoverer inside it — not the other way
	// around: middleware.Recoverer stops a panic from propagating, but if it
	// wrapped *outside* RequestLogger, RequestLogger's next.ServeHTTP call
	// would itself panic and its post-call logging code would never run, so
	// the one request most worth seeing in `docker logs -f` (a 500 from a
	// panic) would be the one request that never gets logged.
	r.Use(RequestLogger)
	r.Use(middleware.Recoverer)

	r.Get("/healthz", HealthzHandler(pool))

	r.Route("/api/storages", NewStoragesHandler(pool).Routes)
	r.Route("/api/locations", NewLocationsHandler(pool).Routes)
	r.Route("/api/items", NewItemsHandler(pool).Routes)
	r.Route("/api/tags", NewTagsHandler(pool).Routes)
	r.Route("/api/photos", NewPhotosHandler(uploadDir).Routes)

	search := NewSearchHandler(pool)
	r.Get("/api/search/suggest", search.Suggest)
	r.Get("/api/scan", search.Scan)
	r.Get("/api/resolve-storage", search.ResolveStorage)

	r.Handle("/uploads/*", http.StripPrefix("/uploads/", uploadsFileServer(uploadDir)))

	return r
}
