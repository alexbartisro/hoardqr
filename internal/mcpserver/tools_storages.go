package mcpserver

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hoardqr/internal/store"
)

// --- edit_storage ---

type editStorageInput struct {
	Storage string  `json:"storage" jsonschema:"the storage to edit, by name or close to it"`
	Name    *string `json:"name,omitempty" jsonschema:"a new name; omit to leave unchanged"`
	Notes   *string `json:"notes,omitempty" jsonschema:"new freeform notes; omit to leave unchanged (this tool cannot clear notes back to empty once set — use the web app for that)"`
}

type editStorageOutput struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// editStorageHandler covers metadata only — moving (re-parenting or
// reassigning a Location) is move_storage's job, kept separate the same way
// move_item is separate from a hypothetical "edit_item" would be for
// storage_id: a structural change and a metadata edit are different enough
// operations that conflating them into one tool's optional fields would
// blur what each call is actually doing.
func editStorageHandler(q *store.Queries) mcp.ToolHandlerFor[editStorageInput, editStorageOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in editStorageInput) (*mcp.CallToolResult, editStorageOutput, error) {
		if in.Name == nil && in.Notes == nil {
			return nil, editStorageOutput{}, toolErrorf("provide name and/or notes to change")
		}
		var name *string
		if in.Name != nil {
			trimmed := strings.TrimSpace(*in.Name)
			if trimmed == "" {
				return nil, editStorageOutput{}, toolErrorf("name cannot be blank")
			}
			name = &trimmed
		}

		storageID, _, err := resolveStorage(ctx, q, in.Storage)
		if err != nil {
			return nil, editStorageOutput{}, sanitizeToolError(ctx, "edit_storage", err)
		}

		updated, err := q.UpdateStorageMetadata(ctx, store.UpdateStorageMetadataParams{
			ID: storageID, Name: name, Notes: in.Notes,
		})
		if err != nil {
			return nil, editStorageOutput{}, sanitizeToolError(ctx, "edit_storage", err)
		}
		return nil, editStorageOutput{ID: updated.ID, Name: updated.Name}, nil
	}
}

// --- move_storage ---

type moveStorageInput struct {
	Storage     string  `json:"storage" jsonschema:"the storage to move, by name or close to it"`
	NewParent   *string `json:"new_parent,omitempty" jsonschema:"nest this storage inside another storage, by name or close to it; mutually exclusive with new_location"`
	NewLocation *string `json:"new_location,omitempty" jsonschema:"promote this storage to root level and assign it to this Location (see list_locations); mutually exclusive with new_parent"`
}

type moveStorageOutput struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Path string `json:"path" jsonschema:"breadcrumb path from root to this storage after the move, including its Location if it has one"`
}

// moveStorageHandler mirrors internal/api/storages.go's PATCH update handler
// exactly for these two fields (cycle rejection, location auto-clear on
// nest), just split into its own tool rather than folded into edit_storage
// — see the reasoning on editStorageHandler.
func moveStorageHandler(pool *pgxpool.Pool) mcp.ToolHandlerFor[moveStorageInput, moveStorageOutput] {
	q := store.New(pool)
	return func(ctx context.Context, _ *mcp.CallToolRequest, in moveStorageInput) (*mcp.CallToolResult, moveStorageOutput, error) {
		if in.NewParent == nil && in.NewLocation == nil {
			return nil, moveStorageOutput{}, toolErrorf("provide new_parent or new_location")
		}
		if in.NewParent != nil && in.NewLocation != nil {
			return nil, moveStorageOutput{}, toolErrorf("provide only one of new_parent or new_location, not both — a nested storage inherits its location from its root ancestor instead")
		}

		storageID, matchedName, err := resolveStorage(ctx, q, in.Storage)
		if err != nil {
			return nil, moveStorageOutput{}, sanitizeToolError(ctx, "move_storage", err)
		}

		var updated store.Storage
		if in.NewParent != nil {
			newParentID, _, err := resolveStorage(ctx, q, *in.NewParent)
			if err != nil {
				return nil, moveStorageOutput{}, sanitizeToolError(ctx, "move_storage", err)
			}
			// Mirrors StoragesHandler.update's own cycle check
			// (internal/api/storages.go) — DescendantStorageIDs(id) already
			// includes id itself, so membership in that set is exactly the
			// "would cycle" condition (covers both "parent of itself" and
			// "parent is its own descendant" in one check).
			descendantIDs, err := q.DescendantStorageIDs(ctx, storageID)
			if err != nil {
				return nil, moveStorageOutput{}, sanitizeToolError(ctx, "move_storage", err)
			}
			for _, d := range descendantIDs {
				if d == newParentID {
					return nil, moveStorageOutput{}, toolErrorf("cannot move %q under itself or one of its own descendants", matchedName)
				}
			}
			updated, err = q.SetStorageParent(ctx, store.SetStorageParentParams{ID: storageID, ParentID: &newParentID})
			if err != nil {
				return nil, moveStorageOutput{}, sanitizeToolError(ctx, "move_storage", err)
			}
		} else {
			newLocationID, _, err := resolveLocation(ctx, q, *in.NewLocation)
			if err != nil {
				return nil, moveStorageOutput{}, sanitizeToolError(ctx, "move_storage", err)
			}
			updated, err = q.PromoteStorageToRoot(ctx, store.PromoteStorageToRootParams{ID: storageID, LocationID: &newLocationID})
			if err != nil {
				return nil, moveStorageOutput{}, sanitizeToolError(ctx, "move_storage", err)
			}
		}

		path, err := breadcrumbText(ctx, q, updated.ID)
		if err != nil {
			return nil, moveStorageOutput{}, sanitizeToolError(ctx, "move_storage", err)
		}
		return nil, moveStorageOutput{ID: updated.ID, Name: updated.Name, Path: path}, nil
	}
}

// --- delete_storage ---

type deleteStorageInput struct {
	Storage string `json:"storage" jsonschema:"the storage to delete, by name or close to it"`
}

type deleteStorageOutput struct {
	Deleted          string `json:"deleted"`
	ChildrenPromoted int64  `json:"children_promoted" jsonschema:"how many child storages were promoted to root level as a result"`
}

// deleteStorageHandler mirrors internal/api/storages.go's delete handler —
// no force param at all, deliberately: deleting a storage must never delete
// the items inside it, in any case (user decision, 2026-09-20). A root
// storage holding items directly always refuses; the caller has to
// move_item them elsewhere first. Non-root storages keep auto-promoting
// their direct items to the parent, same as REST — that's not a deletion,
// nothing is destroyed.
func deleteStorageHandler(pool *pgxpool.Pool) mcp.ToolHandlerFor[deleteStorageInput, deleteStorageOutput] {
	q := store.New(pool)
	return func(ctx context.Context, _ *mcp.CallToolRequest, in deleteStorageInput) (*mcp.CallToolResult, deleteStorageOutput, error) {
		storageID, matchedName, err := resolveStorage(ctx, q, in.Storage)
		if err != nil {
			return nil, deleteStorageOutput{}, sanitizeToolError(ctx, "delete_storage", err)
		}

		var childrenPromoted int64
		txErr := withTx(ctx, pool, func(tx pgx.Tx) error {
			txq := store.New(tx)

			storage, err := txq.GetStorageByID(ctx, storageID)
			if err != nil {
				return err
			}

			if storage.ParentID != nil {
				if err := txq.PromoteItemsToParent(ctx, store.PromoteItemsToParentParams{
					NewStorageID: *storage.ParentID, OldStorageID: storageID,
				}); err != nil {
					return err
				}
			} else {
				count, err := txq.CountDirectItemsAtStorage(ctx, storageID)
				if err != nil {
					return err
				}
				if count > 0 {
					return toolErrorf(
						"%q holds %d item(s) directly and has no parent to promote them to — move them to another storage first (see move_item), then retry",
						matchedName, count,
					)
				}
			}

			// Same effective-location propagation as internal/api/storages.go's
			// delete handler — resolve via StorageBreadcrumb (the *effective*
			// location, which for a nested storage is inherited, not its own
			// always-nil column) before deleting, apply after (setting
			// location_id while parent_id is still non-null would trip
			// storages_location_only_on_root itself).
			breadcrumbRows, err := txq.StorageBreadcrumb(ctx, storageID)
			if err != nil {
				return err
			}
			effectiveLocationID := effectiveLocationID(breadcrumbRows)

			var promotedChildIDs []int64
			if effectiveLocationID != nil {
				promotedChildIDs, err = txq.DirectChildStorageIDs(ctx, &storageID)
				if err != nil {
					return err
				}
			}

			if err := txq.DeleteStorage(ctx, storageID); err != nil {
				return err
			}

			if len(promotedChildIDs) > 0 {
				if err := txq.SetLocationForStorages(ctx, store.SetLocationForStoragesParams{
					LocationID: *effectiveLocationID, Ids: promotedChildIDs,
				}); err != nil {
					return err
				}
			}
			childrenPromoted = int64(len(promotedChildIDs))
			return nil
		})
		if txErr != nil {
			return nil, deleteStorageOutput{}, sanitizeToolError(ctx, "delete_storage", txErr)
		}
		return nil, deleteStorageOutput{Deleted: matchedName, ChildrenPromoted: childrenPromoted}, nil
	}
}
