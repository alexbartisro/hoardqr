package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
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
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)

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
