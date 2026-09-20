package api

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// healthzTimeout bounds how long a single /healthz request will wait on the
// database — a hung Postgres connection shouldn't also hang the liveness
// probe indefinitely, since a monitoring tool (Uptime Kuma, per architecture
// plan §12) calling this repeatedly needs a bounded answer either way.
const healthzTimeout = 2 * time.Second

// HealthzHandler answers GET /healthz (§9) with an actual Postgres
// connectivity check — Phase 3 step 7's "swap the static 200 OK from Phase 1
// for a real check". Shared by both run modes (serve's chi router and mcp's
// plain http.ServeMux — see cmd/hoardqr/main.go) since both hold their own
// pool and both want the same check, not two copies of it.
//
// Doesn't log the failure itself — both callers wrap their handler in
// RequestLogger (step 3.a), which already logs any non-2xx /healthz
// response (see its own doc comment); a second log line here would just be
// the same failure twice in `docker logs -f` every time a monitoring tool
// polls during an outage.
func HealthzHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), healthzTimeout)
		defer cancel()

		if err := pool.Ping(ctx); err != nil {
			// 503, not 500 — the service itself is fine, its database
			// dependency isn't.
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("database unreachable"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}
}
