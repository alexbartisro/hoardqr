package mcpserver

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hoardqr/internal/store"
)

// --- add_location ---

type addLocationInput struct {
	Name string `json:"name" jsonschema:"the new Location's name (e.g. a house, garage, or other physical property)"`
}

type addLocationOutput struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func addLocationHandler(q *store.Queries) mcp.ToolHandlerFor[addLocationInput, addLocationOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in addLocationInput) (*mcp.CallToolResult, addLocationOutput, error) {
		name := strings.TrimSpace(in.Name)
		if name == "" {
			return nil, addLocationOutput{}, toolErrorf("name is required")
		}
		// No TOCTOU pre-check — locations.name's case-insensitive unique index
		// (migration 000005) is the authority, same pattern as
		// internal/api/locations.go's create handler.
		location, err := q.InsertLocation(ctx, store.InsertLocationParams{
			OwnerID:  nil, // no auth yet — every row is public until then (see CLAUDE.md)
			IsShared: true,
			Name:     name,
		})
		if err != nil {
			if isConflict(err) {
				return nil, addLocationOutput{}, toolErrorf("a location named %q already exists", name)
			}
			return nil, addLocationOutput{}, sanitizeToolError(ctx, "add_location", err)
		}
		return nil, addLocationOutput{ID: location.ID, Name: location.Name}, nil
	}
}

// --- edit_location ---

type editLocationInput struct {
	Location string `json:"location" jsonschema:"the Location's exact current name (see list_locations)"`
	Name     string `json:"name" jsonschema:"the new name"`
}

type editLocationOutput struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// editLocationHandler is a rename — name is the only field a Location has
// beyond identity. resolveLocation is exact-match-only (locations aren't
// fuzzy-searchable, see resolve.go), which is the right behavior for a
// write: an approximate match silently renaming the wrong property would be
// a real, unnoticeable mistake.
func editLocationHandler(q *store.Queries) mcp.ToolHandlerFor[editLocationInput, editLocationOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in editLocationInput) (*mcp.CallToolResult, editLocationOutput, error) {
		name := strings.TrimSpace(in.Name)
		if name == "" {
			return nil, editLocationOutput{}, toolErrorf("name is required")
		}
		id, _, err := resolveLocation(ctx, q, in.Location)
		if err != nil {
			return nil, editLocationOutput{}, sanitizeToolError(ctx, "edit_location", err)
		}
		updated, err := q.UpdateLocationName(ctx, store.UpdateLocationNameParams{ID: id, Name: name})
		if err != nil {
			if isConflict(err) {
				return nil, editLocationOutput{}, toolErrorf("a location named %q already exists", name)
			}
			return nil, editLocationOutput{}, sanitizeToolError(ctx, "edit_location", err)
		}
		return nil, editLocationOutput{ID: updated.ID, Name: updated.Name}, nil
	}
}

// --- delete_location ---

type deleteLocationInput struct {
	Location string `json:"location" jsonschema:"the Location's exact current name (see list_locations)"`
	Force    *bool  `json:"force,omitempty" jsonschema:"required to actually delete when storages are still assigned to it — clears their assignment (they become unassigned, never deleted or altered otherwise) rather than deleting anything"`
}

type deleteLocationOutput struct {
	Deleted            string `json:"deleted"`
	StoragesUnassigned int64  `json:"storages_unassigned" jsonschema:"how many storages had this Location cleared as part of the delete"`
}

// deleteLocationHandler mirrors internal/api/locations.go's delete handler
// exactly — force here is safe to expose (unlike delete_storage's, which is
// removed entirely): the worst it ever does is clear a storage's location_id
// back to unassigned. No storage or item is ever deleted or altered
// otherwise by this tool.
func deleteLocationHandler(pool *pgxpool.Pool) mcp.ToolHandlerFor[deleteLocationInput, deleteLocationOutput] {
	q := store.New(pool)
	return func(ctx context.Context, _ *mcp.CallToolRequest, in deleteLocationInput) (*mcp.CallToolResult, deleteLocationOutput, error) {
		id, matchedName, err := resolveLocation(ctx, q, in.Location)
		if err != nil {
			return nil, deleteLocationOutput{}, sanitizeToolError(ctx, "delete_location", err)
		}
		force := in.Force != nil && *in.Force

		count, err := q.CountStoragesAtLocation(ctx, &id)
		if err != nil {
			return nil, deleteLocationOutput{}, sanitizeToolError(ctx, "delete_location", err)
		}
		if count > 0 && !force {
			return nil, deleteLocationOutput{}, toolErrorf(
				"%q has %d storage(s) assigned — retry with force to clear that assignment (the storages themselves are never deleted) and delete the location",
				matchedName, count,
			)
		}

		err = withTx(ctx, pool, func(tx pgx.Tx) error {
			txq := store.New(tx)
			if count > 0 {
				if err := txq.ClearLocationFromStorages(ctx, &id); err != nil {
					return err
				}
			}
			return txq.DeleteLocation(ctx, id)
		})
		if err != nil {
			return nil, deleteLocationOutput{}, sanitizeToolError(ctx, "delete_location", err)
		}
		return nil, deleteLocationOutput{Deleted: matchedName, StoragesUnassigned: count}, nil
	}
}
