package mcpserver

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// New builds the MCP server exposing the original six tools from
// architecture plan §11 (find_items, where_is, list_contents, add_item,
// add_storage, move_item) plus everything added since: list_locations
// (alongside add_storage's optional location param), and full add/edit/
// move/delete CRUD for locations, storages, and items, plus additive
// item-tag operations (added 2026-09-20, user request — see CLAUDE.md).
// The server authenticates as one configured user via a static API token
// (checked by the caller — see cmd/hoardqr/main.go's use of
// auth.RequireBearerToken) rather than anything here; per §11, per-caller
// identity can be layered on later if it's ever needed.
func New(pool *pgxpool.Pool) *mcp.Server {
	s := mcp.NewServer(
		&mcp.Implementation{Name: "hoardqr", Version: "0.1.0"},
		&mcp.ServerOptions{
			Logger: slog.Default(),
			Instructions: "Query and update a HoardQR home inventory: find items, resolve where something lives, list what's stored in a storage or Location, " +
				"add/edit/move/delete items and storages, add or remove tags on an item, and add/edit/delete Locations (physical properties like a house or garage) " +
				"a root-level storage can optionally belong to. Deleting a storage never deletes the items inside it — move them elsewhere first (move_item).",
		},
	)
	registerTools(s, pool)
	return s
}
