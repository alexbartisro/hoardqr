package main

import (
	"context"
	"crypto/subtle"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"hoardqr/internal/api"
	"hoardqr/internal/config"
	"hoardqr/internal/db"
	"hoardqr/internal/mcpserver"
	"hoardqr/web"
)

func main() {
	initLogging()

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

// initLogging sets the default slog logger for the whole process — plain
// text, one line per event, readable following the container live with
// `docker logs -f` (step 3.a) rather than only useful piped through a JSON
// log processor. LOG_LEVEL (debug/info/warn/error, case-insensitive)
// optionally overrides the default of info; anything unrecognized is
// treated as info rather than failing startup over a typo'd env var.
func initLogging() {
	level := slog.LevelInfo
	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})))
}

// fatal logs a structured error and exits — the slog equivalent of
// log.Fatalf, used instead of it so a startup failure has the same line
// shape as everything else in the log rather than a plain unstructured
// stdlib-log line.
func fatal(msg string, args ...any) {
	slog.Error(msg, args...)
	os.Exit(1)
}

// connectAndMigrate is shared by both run modes: each needs its own pool,
// and applying migrations here (rather than a separate manual step) means
// there's nothing to remember to run before starting either container —
// db.Migrate is a no-op once the schema is current, guarded by Postgres's
// own advisory lock if serve and mcp happen to start at the same time.
func connectAndMigrate(ctx context.Context, cfg config.Config) *pgxpool.Pool {
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		fatal("connecting to database", "error", err)
	}
	slog.Info("database connected")
	if err := db.Migrate(cfg.DatabaseURL); err != nil {
		fatal("running migrations", "error", err)
	}
	slog.Info("migrations up to date")
	return pool
}

func serve() {
	cfg, err := config.Load()
	if err != nil {
		fatal("loading config", "error", err)
	}
	ctx := context.Background()
	pool := connectAndMigrate(ctx, cfg)

	if err := os.MkdirAll(cfg.UploadDir, 0o755); err != nil {
		fatal("creating upload directory", "error", err, "dir", cfg.UploadDir)
	}

	webFS, err := fs.Sub(web.Assets, "build")
	if err != nil {
		fatal("loading embedded web assets", "error", err)
	}

	router := api.NewRouter(pool, cfg.UploadDir)
	router.NotFound(spaHandler(webFS).ServeHTTP)

	const addr = ":8080"
	slog.Info("hoardqr serve listening", "addr", addr)
	// Captured (not fatal'd directly) so pool.Close() actually runs before
	// exit — os.Exit (which fatal calls) skips defers same as log.Fatal did.
	serveErr := http.ListenAndServe(addr, router)
	pool.Close()
	fatal("server stopped", "error", serveErr)
}

func mcp() {
	cfg, err := config.Load()
	if err != nil {
		fatal("loading config", "error", err)
	}
	ctx := context.Background()
	pool := connectAndMigrate(ctx, cfg)

	mcpServer := mcpserver.New(pool)
	streamable := mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server { return mcpServer }, nil)

	var mcpHandler http.Handler = streamable
	if cfg.MCPAPIToken != "" {
		mcpHandler = auth.RequireBearerToken(
			staticTokenVerifier(cfg.MCPAPIToken),
			&auth.RequireBearerTokenOptions{AllowMissingExpiration: true}, // a static, never-expiring token has no exp claim to check
		)(streamable)
	} else {
		// Matches AUTH_REQUIRED=false's existing "insecure but functional for
		// solo/trusted-network use" default elsewhere in the app — real
		// deployments set MCP_API_TOKEN (see .env.example).
		slog.Warn("MCP_API_TOKEN not set — /mcp is unauthenticated")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)
	mux.Handle("/mcp", mcpHandler)

	const addr = ":8081"
	slog.Info("hoardqr mcp listening", "addr", addr, "path", "/mcp")
	serveErr := http.ListenAndServe(addr, mux)
	pool.Close()
	fatal("server stopped", "error", serveErr)
}

// staticTokenVerifier checks a bearer token against the single configured
// MCP_API_TOKEN — architecture plan §11's "authenticates as one configured
// user (a static API token)". The comparison is constant-time so response
// timing can't leak how much of a guessed token matched.
func staticTokenVerifier(expected string) auth.TokenVerifier {
	return func(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		if subtle.ConstantTimeCompare([]byte(token), []byte(expected)) != 1 {
			return nil, auth.ErrInvalidToken
		}
		return &auth.TokenInfo{UserID: "configured-user"}, nil
	}
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
