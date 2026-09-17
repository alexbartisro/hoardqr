package main

import (
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"

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

func serve() {
	webFS, err := fs.Sub(web.Assets, "build")
	if err != nil {
		log.Fatalf("failed to load embedded web assets: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)
	mux.Handle("/", spaHandler(webFS))

	const addr = ":8080"
	log.Printf("hoardqr serve listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func mcp() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)

	const addr = ":8081"
	log.Printf("hoardqr mcp listening on %s (MCP tools not implemented yet)", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
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
