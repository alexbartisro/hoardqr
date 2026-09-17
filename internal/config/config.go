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
}

// Load reads and validates configuration for both run modes (serve and mcp).
// DATABASE_URL is required for both; SESSION_SECRET and MCP_API_TOKEN are
// only meaningful to the mode that uses them, but are read here regardless
// so a missing value fails fast at startup rather than the first request
// that needs it.
func Load() (Config, error) {
	cfg := Config{
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		AuthRequired:  os.Getenv("AUTH_REQUIRED") == "true",
		SessionSecret: os.Getenv("SESSION_SECRET"),
		MCPAPIToken:   os.Getenv("MCP_API_TOKEN"),
	}
	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	return cfg, nil
}
