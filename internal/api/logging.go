package api

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// RequestLogger logs one line per request via slog — method, path, status,
// duration, and the client address — so a running container can be followed
// live with `docker logs -f` (step 3.a) instead of only being inspectable
// after the fact. Wraps chi's own middleware.WrapResponseWriter to capture
// the status code, since http.ResponseWriter doesn't expose what was
// written to it.
//
// Successful (<400) requests to /healthz and to anything outside /api/ —
// i.e. the embedded SPA's own static assets, served by this same router's
// NotFound fallback (main.go's spaHandler) — are skipped: /healthz is
// polled by Uptime Kuma every ~60s (CLAUDE.md §12) and a single cold load of
// the frontend pulls in dozens of JS/CSS/icon requests, and this middleware
// exists specifically so the log stream stays something a person can follow
// live, not so it can drown in polling and asset noise. A *failing* request
// on either of those paths still logs — that's exactly the case worth
// seeing — only the successful, high-volume, low-information ones are cut.
func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		next.ServeHTTP(ww, r)

		status := ww.Status()
		isAPI := strings.HasPrefix(r.URL.Path, "/api/")
		if status < 400 && (r.URL.Path == "/healthz" || !isAPI) {
			return
		}

		level := slog.LevelInfo
		if status >= 500 {
			level = slog.LevelError
		} else if status >= 400 {
			level = slog.LevelWarn
		}
		slog.LogAttrs(r.Context(), level, "request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", status),
			slog.Duration("duration", time.Since(start)),
			slog.String("remote_addr", r.RemoteAddr),
		)
	})
}
