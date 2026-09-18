// Package config loads runtime configuration from environment variables —
// the same names docker-compose.yml and .env.example already document.
package config

import (
	"fmt"
	"os"
)

type Config struct {
	DatabaseURL   string
	AuthRequired  bool
	SessionSecret string
	MCPAPIToken   string
	UploadDir     string
}

// Load reads and validates configuration for both run modes (serve and mcp).
// DATABASE_URL is required for both; SESSION_SECRET, MCP_API_TOKEN, and
// UPLOAD_DIR are only meaningful to the mode that uses them, but are read
// here regardless so a missing value fails fast at startup rather than the
// first request that needs it.
func Load() (Config, error) {
	uploadDir := os.Getenv("UPLOAD_DIR")
	if uploadDir == "" {
		// Matches docker-compose.yml's `./uploads:/data/uploads` volume mount
		// (architecture plan §12) — a deployment using that compose file needs
		// no UPLOAD_DIR override at all. Only local (non-Docker) dev running
		// the bare binary needs to set it, e.g. to ./uploads.
		uploadDir = "/data/uploads"
	}
	cfg := Config{
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		AuthRequired:  os.Getenv("AUTH_REQUIRED") == "true",
		SessionSecret: os.Getenv("SESSION_SECRET"),
		MCPAPIToken:   os.Getenv("MCP_API_TOKEN"),
		UploadDir:     uploadDir,
	}
	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	return cfg, nil
}
