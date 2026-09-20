package mcpserver

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// New builds the MCP server exposing the six tools from architecture plan
// §11 (find_items, where_is, list_contents, add_item, add_storage,
// move_item) plus list_locations, added alongside add_storage's optional
// location param (see CLAUDE.md's Locations design-decision notes). The
// server authenticates as one configured user via a static API token
// (checked by the caller — see cmd/hoardqr/main.go's use of
// auth.RequireBearerToken) rather than anything here; per §11, per-caller
// identity can be layered on later if it's ever needed.
func New(pool *pgxpool.Pool) *mcp.Server {
	s := mcp.NewServer(
		&mcp.Implementation{Name: "hoardqr", Version: "0.1.0"},
		&mcp.ServerOptions{
			Logger:       slog.Default(),
			Instructions: "Query and update a HoardQR home inventory: find items, resolve where something lives, list what's stored in a storage, add or move items and storages, and list Locations (physical properties like a house or garage) a root-level storage can optionally belong to.",
		},
	)
	registerTools(s, pool)
	return s
}
