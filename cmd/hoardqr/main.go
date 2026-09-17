package main

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"hoardqr/internal/config"
	"hoardqr/internal/db"
	"hoardqr/web"
)

func main() {
	mode := "serve"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}

	switch mode {
	case "serve":
		serve()
	case "mcp":
		mcp()
	default:
		fmt.Fprintf(os.Stderr, "unknown mode %q (expected \"serve\" or \"mcp\")\n", mode)
		os.Exit(1)
	}
}

// connectAndMigrate is shared by both run modes: each needs its own pool,
// and applying migrations here (rather than a separate manual step) means
// there's nothing to remember to run before starting either container —
// db.Migrate is a no-op once the schema is current, guarded by Postgres's
// own advisory lock if serve and mcp happen to start at the same time.
func connectAndMigrate(ctx context.Context, cfg config.Config) *pgxpool.Pool {
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("connecting to database: %v", err)
	}
	if err := db.Migrate(cfg.DatabaseURL); err != nil {
		log.Fatalf("running migrations: %v", err)
	}
	return pool
}

func serve() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("loading config: %v", err)
	}
	ctx := context.Background()
	pool := connectAndMigrate(ctx, cfg)

	webFS, err := fs.Sub(web.Assets, "build")
	if err != nil {
		log.Fatalf("failed to load embedded web assets: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)
	mux.Handle("/", spaHandler(webFS))

	const addr = ":8080"
	log.Printf("hoardqr serve listening on %s", addr)
	// Captured (not log.Fatal'd directly) so pool.Close() actually runs before
	// exit — log.Fatal calls os.Exit, which skips defers.
	serveErr := http.ListenAndServe(addr, mux)
	pool.Close()
	log.Fatal(serveErr)
}

func mcp() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("loading config: %v", err)
	}
	ctx := context.Background()
	pool := connectAndMigrate(ctx, cfg)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)

	const addr = ":8081"
	log.Printf("hoardqr mcp listening on %s (MCP tools not implemented yet)", addr)
	serveErr := http.ListenAndServe(addr, mux)
	pool.Close()
	log.Fatal(serveErr)
}

func healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// spaHandler serves static files from webFS, falling back to index.html for
// any path that isn't a real file. The frontend is a client-rendered SPA
// (adapter-static + ssr=false — see CLAUDE.md), so every non-asset route
// (e.g. /items/42) is resolved by the client-side router, not this server.
func spaHandler(webFS fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(webFS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(webFS, path); err != nil {
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})
}
