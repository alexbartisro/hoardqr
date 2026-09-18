package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHealthzReportsDatabaseConnectivity proves Phase 3 step 7's actual
// point: /healthz must reflect real Postgres reachability, not just answer
// 200 unconditionally like the Phase 1 placeholder did.
func TestHealthzReportsDatabaseConnectivity(t *testing.T) {
	pool := testPool(t)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	HealthzHandler(pool).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with a reachable database, got %d: %s", rec.Code, rec.Body.String())
	}

	// A closed pool can't reach the database — Ping must fail fast (no
	// waiting out healthzTimeout) and the handler must turn that into a 503,
	// not a 200 or a panic.
	pool.Close()
	rec = httptest.NewRecorder()
	HealthzHandler(pool).ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 with an unreachable database, got %d: %s", rec.Code, rec.Body.String())
	}
}
