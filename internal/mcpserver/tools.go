package mcpserver

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hoardqr/internal/codegen"
	"hoardqr/internal/store"
)

func registerTools(s *mcp.Server, pool *pgxpool.Pool) {
	q := store.New(pool)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "find_items",
		Description: "Search for items by name, tag, or code. Returns each match with the storage it's stored in.",
	}, findItemsHandler(q))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "where_is",
		Description: "Resolve an item's name to the full storage path it's stored in.",
	}, whereIsHandler(q))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_contents",
		Description: "List everything stored in a storage (including everything nested inside its child storages), or everything across all of a Location's root-level storages combined. Provide exactly one of storage or location.",
	}, listContentsHandler(q))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "add_item",
		Description: "Catalog a new object and place it in a storage. Accepts the object's full details (description, condition, purchase date/price, receipt URL, tags) up front, not just name/storage/quantity.",
	}, addItemHandler(pool))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "add_storage",
		Description: "Create a new storage container (a box, shelf, cabinet, room, etc.), optionally nested inside an existing one, or optionally assigned to a Location (a physical property like a house or garage) if it's root-level.",
	}, addStorageHandler(q))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "edit_item",
		Description: "Change an existing object's fields (name, description, quantity, condition, purchase date/price, receipt URL). Does not move it (see move_item) or change its tags (see add_item_tag/remove_item_tag).",
	}, editItemHandler(q))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_item",
		Description: "Permanently delete an object. Requires an exact name match or a numeric id, not a fuzzy match — resolve it first (e.g. find_items) if unsure.",
	}, deleteItemHandler(q))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "add_item_tag",
		Description: "Add a tag to an object, e.g. \"add the electronic tool tag to the drill\" — creates the tag if it doesn't exist yet. Does not require knowing the object's existing tags.",
	}, addItemTagHandler(pool))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "remove_item_tag",
		Description: "Remove a tag from an object.",
	}, removeItemTagHandler(q))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "edit_storage",
		Description: "Rename a storage and/or change its notes. Does not move it (see move_storage).",
	}, editStorageHandler(q))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "move_storage",
		Description: "Move a storage into a different parent storage, or promote it to root level under a different Location. Provide exactly one of new_parent or new_location.",
	}, moveStorageHandler(pool))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_storage",
		Description: "Permanently delete a storage. Requires an exact name match or a numeric id, not a fuzzy match — resolve it first (e.g. list_contents) if unsure. Child storages are promoted to root level automatically. Refuses if the storage directly holds objects — move them elsewhere first (see move_item); a storage delete never deletes the objects inside it.",
	}, deleteStorageHandler(pool))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "move_item",
		Description: "Move an existing item to a different storage.",
	}, moveItemHandler(pool))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_locations",
		Description: "List every Location — a flat, non-nested physical property (a house, garage, etc.) that a root-level storage can optionally belong to. Read-only; use this to see valid values before calling add_storage with a location.",
	}, listLocationsHandler(q))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "add_location",
		Description: "Create a new Location (a physical property like a house or garage).",
	}, addLocationHandler(q))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "edit_location",
		Description: "Rename a Location.",
	}, editLocationHandler(q))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_location",
		Description: "Permanently delete a Location. Refuses if storages are still assigned to it unless force is given, which only clears that assignment (the storages themselves are never deleted or otherwise altered).",
	}, deleteLocationHandler(pool))
}

// --- find_items ---

type findItemsInput struct {
	Query string `json:"query" jsonschema:"the name, tag, or code to search for"`
}

type foundItem struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Storage string `json:"storage" jsonschema:"breadcrumb path from root to where this item lives"`
}

type findItemsOutput struct {
	Items []foundItem `json:"items"`
}

func findItemsHandler(q *store.Queries) mcp.ToolHandlerFor[findItemsInput, findItemsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in findItemsInput) (*mcp.CallToolResult, findItemsOutput, error) {
		// Trimmed, like the REST search handler (internal/api/search.go's
		// Suggest) and resolveStorage/resolveItem — untrimmed, a leading or
		// trailing space (e.g. an LLM relaying a user's pasted code verbatim)
		// breaks the exact-code match branch and finds nothing, the same class
		// of bug already found and fixed once for the REST endpoint. An empty
		// query returns no results rather than erroring, matching the REST
		// handler's own empty-query behavior — asking to find "nothing" isn't
		// a caller mistake worth a tool error.
		out := findItemsOutput{Items: []foundItem{}}
		query := strings.TrimSpace(in.Query)
		if query == "" {
			return nil, out, nil
		}

		// SearchSuggestByKind, not SearchSuggest — a shared top-10 across
		// kinds let a higher-scoring storage/tag match crowd out real item
		// matches entirely (a query could come back empty despite a genuine
		// match existing). Two separate calls, one per kind actually needed
		// here, rather than one mixed query filtered in Go. See the Obsidian
		// backend TODO for the verified failure case.
		seen := make(map[int64]bool)

		itemRows, err := q.SearchSuggestByKind(ctx, store.SearchSuggestByKindParams{Query: query, Kind: "item"})
		if err != nil {
			return nil, findItemsOutput{}, sanitizeToolError(ctx, "find_items", err)
		}
		for _, row := range itemRows {
			if row.StorageID == nil {
				continue
			}
			breadcrumb, err := breadcrumbText(ctx, q, *row.StorageID)
			if err != nil {
				return nil, findItemsOutput{}, sanitizeToolError(ctx, "find_items", err)
			}
			out.Items = append(out.Items, foundItem{ID: row.ID, Name: row.Name, Storage: breadcrumb})
			seen[row.ID] = true
		}

		// find_items's own description promises matching "by name, tag, or
		// code" — a query that only matches a tag name (not any item's own
		// name/code) still needs to surface its items here, via the same
		// exact-tag lookup ListItems already offers the REST item-browsing
		// endpoint (§9's Mock API surface gaps, see CLAUDE.md).
		tagRows, err := q.SearchSuggestByKind(ctx, store.SearchSuggestByKindParams{Query: query, Kind: "tag"})
		if err != nil {
			return nil, findItemsOutput{}, sanitizeToolError(ctx, "find_items", err)
		}
		for _, row := range tagRows {
			tag := row.Name
			taggedItems, err := q.ListItems(ctx, store.ListItemsParams{Tag: &tag})
			if err != nil {
				return nil, findItemsOutput{}, sanitizeToolError(ctx, "find_items", err)
			}
			for _, item := range taggedItems {
				if seen[item.ID] {
					continue
				}
				breadcrumb, err := breadcrumbText(ctx, q, item.StorageID)
				if err != nil {
					return nil, findItemsOutput{}, sanitizeToolError(ctx, "find_items", err)
				}
				out.Items = append(out.Items, foundItem{ID: item.ID, Name: item.Name, Storage: breadcrumb})
				seen[item.ID] = true
			}
		}

		return nil, out, nil
	}
}

// --- where_is ---

type whereIsInput struct {
	Name string `json:"name" jsonschema:"the item's name, or close to it"`
}

type whereIsOutput struct {
	Item    string `json:"item" jsonschema:"the matched item's actual name"`
	Storage string `json:"storage" jsonschema:"breadcrumb path from root to where it lives"`
}

func whereIsHandler(q *store.Queries) mcp.ToolHandlerFor[whereIsInput, whereIsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in whereIsInput) (*mcp.CallToolResult, whereIsOutput, error) {
		_, matchedName, storageID, err := resolveItem(ctx, q, in.Name)
		if err != nil {
			return nil, whereIsOutput{}, sanitizeToolError(ctx, "where_is", err)
		}
		breadcrumb, err := breadcrumbText(ctx, q, storageID)
		if err != nil {
			return nil, whereIsOutput{}, sanitizeToolError(ctx, "where_is", err)
		}
		return nil, whereIsOutput{Item: matchedName, Storage: breadcrumb}, nil
	}
}

// --- list_contents ---

type listContentsInput struct {
	Storage  *string `json:"storage,omitempty" jsonschema:"a storage's name, or close to it — provide exactly one of storage or location"`
	Location *string `json:"location,omitempty" jsonschema:"a Location's exact name (see list_locations) — lists everything across all of that Location's root-level storages combined; provide exactly one of storage or location"`
}

type contentItem struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Quantity int32  `json:"quantity"`
}

type listContentsOutput struct {
	Storage  *string       `json:"storage,omitempty" jsonschema:"the matched storage's actual name, present when a storage was queried"`
	Location *string       `json:"location,omitempty" jsonschema:"the matched Location's actual name, present when a location was queried"`
	Path     string        `json:"path" jsonschema:"breadcrumb path from root to this storage, or the Location's own name when a location was queried"`
	Items    []contentItem `json:"items" jsonschema:"every item stored here (or, for a location, across all its root storages) or in anything nested inside"`
}

func listContentsHandler(q *store.Queries) mcp.ToolHandlerFor[listContentsInput, listContentsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in listContentsInput) (*mcp.CallToolResult, listContentsOutput, error) {
		if in.Storage == nil && in.Location == nil {
			return nil, listContentsOutput{}, toolErrorf("provide either storage or location")
		}
		if in.Storage != nil && in.Location != nil {
			return nil, listContentsOutput{}, toolErrorf("provide only one of storage or location, not both")
		}

		if in.Location != nil {
			return listLocationContents(ctx, q, *in.Location)
		}

		storageID, matchedName, err := resolveStorage(ctx, q, *in.Storage)
		if err != nil {
			return nil, listContentsOutput{}, sanitizeToolError(ctx, "list_contents", err)
		}
		path, err := breadcrumbText(ctx, q, storageID)
		if err != nil {
			return nil, listContentsOutput{}, sanitizeToolError(ctx, "list_contents", err)
		}
		descendantIDs, err := q.DescendantStorageIDs(ctx, storageID)
		if err != nil {
			return nil, listContentsOutput{}, sanitizeToolError(ctx, "list_contents", err)
		}
		rows, err := q.ListItemsByStorageIDs(ctx, descendantIDs)
		if err != nil {
			return nil, listContentsOutput{}, sanitizeToolError(ctx, "list_contents", err)
		}
		out := listContentsOutput{Storage: &matchedName, Path: path, Items: []contentItem{}}
		for _, row := range rows {
			out.Items = append(out.Items, contentItem{ID: row.ID, Name: row.Name, Quantity: row.Quantity})
		}
		return nil, out, nil
	}
}

// listLocationContents is list_contents' location-query branch, split out
// since it fans out over every root storage assigned to the Location
// (there's no single storage_id to resolve a breadcrumb from the way the
// plain-storage branch has) rather than resolving one storage and its
// descendants.
func listLocationContents(ctx context.Context, q *store.Queries, location string) (*mcp.CallToolResult, listContentsOutput, error) {
	locationID, matchedLocation, err := resolveLocation(ctx, q, location)
	if err != nil {
		return nil, listContentsOutput{}, sanitizeToolError(ctx, "list_contents", err)
	}
	roots, err := q.ListStoragesAtLocation(ctx, &locationID)
	if err != nil {
		return nil, listContentsOutput{}, sanitizeToolError(ctx, "list_contents", err)
	}

	out := listContentsOutput{Location: &matchedLocation, Path: matchedLocation, Items: []contentItem{}}
	if len(roots) == 0 {
		return nil, out, nil
	}

	var allIDs []int64
	for _, root := range roots {
		descendantIDs, err := q.DescendantStorageIDs(ctx, root.ID)
		if err != nil {
			return nil, listContentsOutput{}, sanitizeToolError(ctx, "list_contents", err)
		}
		allIDs = append(allIDs, descendantIDs...)
	}
	rows, err := q.ListItemsByStorageIDs(ctx, allIDs)
	if err != nil {
		return nil, listContentsOutput{}, sanitizeToolError(ctx, "list_contents", err)
	}
	for _, row := range rows {
		out.Items = append(out.Items, contentItem{ID: row.ID, Name: row.Name, Quantity: row.Quantity})
	}
	return nil, out, nil
}

// --- add_item ---

type addItemInput struct {
	Name          string   `json:"name" jsonschema:"the object's name"`
	Storage       string   `json:"storage" jsonschema:"where to store it — a storage's name, or close to it"`
	Quantity      *int32   `json:"quantity,omitempty" jsonschema:"how many; defaults to 1"`
	Description   *string  `json:"description,omitempty"`
	Condition     *string  `json:"condition,omitempty" jsonschema:"e.g. new, good, fair, poor"`
	PurchaseDate  *string  `json:"purchase_date,omitempty" jsonschema:"YYYY-MM-DD"`
	PurchasePrice *float64 `json:"purchase_price,omitempty"`
	ReceiptURL    *string  `json:"receipt_url,omitempty"`
	Tags          []string `json:"tags,omitempty" jsonschema:"tag names to attach — each is created automatically (case-insensitively) if it doesn't exist yet"`
}

type addItemOutput struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Storage string `json:"storage" jsonschema:"the matched storage's actual name"`
	Code    string `json:"code" jsonschema:"the generated code, printable on a label for this item"`
}

// addItemHandler takes the pool (not just q) as of the tags param: linking
// tags is a second write alongside InsertItem, so the two now need to
// commit or fail together — same reasoning as internal/api/items.go's
// create handler, which wraps the identical pair in withTx.
func addItemHandler(pool *pgxpool.Pool) mcp.ToolHandlerFor[addItemInput, addItemOutput] {
	q := store.New(pool)
	return func(ctx context.Context, _ *mcp.CallToolRequest, in addItemInput) (*mcp.CallToolResult, addItemOutput, error) {
		// Mirrors internal/api/items.go's create handler's own
		// strings.TrimSpace(req.Name) == "" check — REST rejects a blank name,
		// so an MCP caller shouldn't be able to create one either.
		name := strings.TrimSpace(in.Name)
		if name == "" {
			return nil, addItemOutput{}, toolErrorf("name is required")
		}
		storageID, matchedStorage, err := resolveStorage(ctx, q, in.Storage)
		if err != nil {
			return nil, addItemOutput{}, sanitizeToolError(ctx, "add_item", err)
		}
		quantity := int32(1)
		if in.Quantity != nil {
			quantity = *in.Quantity
		}
		purchaseDate, err := optionalDate(in.PurchaseDate)
		if err != nil {
			return nil, addItemOutput{}, toolErrorf("purchase_date must be YYYY-MM-DD")
		}
		code := codegen.PlainTextCode()

		var item store.Item
		txErr := withTx(ctx, pool, func(tx pgx.Tx) error {
			txq := store.New(tx)
			var err error
			item, err = txq.InsertItem(ctx, store.InsertItemParams{
				StorageID:     storageID,
				IsShared:      true, // no auth yet — every row is public until then (see CLAUDE.md)
				Name:          name,
				Quantity:      quantity,
				QrToken:       code,
				Description:   in.Description,
				Condition:     in.Condition,
				PurchaseDate:  purchaseDate,
				PurchasePrice: optionalNumeric(in.PurchasePrice),
				ReceiptUrl:    in.ReceiptURL,
				CustomFields:  []byte("{}"),
			})
			if err != nil {
				return err
			}
			if len(in.Tags) == 0 {
				return nil
			}
			tagIDs, err := resolveTagIDs(ctx, txq, in.Tags)
			if err != nil {
				return err
			}
			for _, tagID := range tagIDs {
				if err := txq.LinkItemTag(ctx, store.LinkItemTagParams{ItemID: item.ID, TagID: tagID}); err != nil {
					return err
				}
			}
			return nil
		})
		if txErr != nil {
			if isNumericOutOfRange(txErr) {
				return nil, addItemOutput{}, toolErrorf("purchase_price is out of range")
			}
			return nil, addItemOutput{}, sanitizeToolError(ctx, "add_item", txErr)
		}
		return nil, addItemOutput{ID: item.ID, Name: item.Name, Storage: matchedStorage, Code: item.QrToken}, nil
	}
}

// --- add_storage ---

type addStorageInput struct {
	Name     string  `json:"name" jsonschema:"the new storage's name"`
	Parent   *string `json:"parent,omitempty" jsonschema:"an existing storage to nest this inside; omit for a root-level storage"`
	Location *string `json:"location,omitempty" jsonschema:"an existing Location (a physical property, e.g. a house or garage — see list_locations) this root-level storage belongs to; never combine with parent, since a nested storage always inherits its location from its root ancestor instead"`
	Notes    *string `json:"notes,omitempty"`
}

type addStorageOutput struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Path string `json:"path" jsonschema:"breadcrumb path from root, including the new storage itself, and its Location if it has one"`
	Code string `json:"code" jsonschema:"the generated code, printable on a label for this storage"`
}

func addStorageHandler(q *store.Queries) mcp.ToolHandlerFor[addStorageInput, addStorageOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in addStorageInput) (*mcp.CallToolResult, addStorageOutput, error) {
		// Mirrors internal/api/storages.go's create handler's own
		// strings.TrimSpace(req.Name) == "" check — REST rejects a blank name,
		// so an MCP caller shouldn't be able to create one either.
		name := strings.TrimSpace(in.Name)
		if name == "" {
			return nil, addStorageOutput{}, toolErrorf("name is required")
		}

		// Mirrors internal/api/storages.go's create handler: a location is
		// only meaningful on a root storage (storages_location_only_on_root,
		// migration 000004) — reject the combination outright rather than
		// silently picking one, the same "contradictory, not a case to guess
		// at" reasoning as the REST handler's PATCH equivalent.
		if in.Parent != nil && in.Location != nil {
			return nil, addStorageOutput{}, toolErrorf("a nested storage inherits its location from its root storage — provide parent or location, not both")
		}

		var parentID *int64
		if in.Parent != nil {
			id, _, err := resolveStorage(ctx, q, *in.Parent)
			if err != nil {
				return nil, addStorageOutput{}, sanitizeToolError(ctx, "add_storage", err)
			}
			parentID = &id
		}

		var locationID *int64
		if in.Location != nil {
			id, _, err := resolveLocation(ctx, q, *in.Location)
			if err != nil {
				return nil, addStorageOutput{}, sanitizeToolError(ctx, "add_storage", err)
			}
			locationID = &id
		}

		// Retries on a generated-code collision (architecture plan §4: "a
		// non-event, not something to design around") — same reasoning as
		// internal/api/storages.go's create handler.
		var storage store.Storage
		for attempt := 0; ; attempt++ {
			var err error
			storage, err = q.InsertStorage(ctx, store.InsertStorageParams{
				ParentID:   parentID,
				LocationID: locationID,
				IsShared:   true, // no auth yet — every row is public until then (see CLAUDE.md)
				Name:       name,
				QrToken:    codegen.PlainTextCode(),
				Notes:      in.Notes,
			})
			if err == nil {
				break
			}
			if !isConflict(err) || attempt >= 5 {
				return nil, addStorageOutput{}, sanitizeToolError(ctx, "add_storage", err)
			}
		}

		// Already Location-prefixed for free when locationID is set —
		// breadcrumbText resolves it via StorageBreadcrumb's root walk
		// (migration 000004), no separate lookup needed here.
		path, err := breadcrumbText(ctx, q, storage.ID)
		if err != nil {
			return nil, addStorageOutput{}, sanitizeToolError(ctx, "add_storage", err)
		}
		return nil, addStorageOutput{ID: storage.ID, Name: storage.Name, Path: path, Code: storage.QrToken}, nil
	}
}

// --- list_locations ---

type listLocationsInput struct{}

type locationSummary struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type listLocationsOutput struct {
	Locations []locationSummary `json:"locations"`
}

func listLocationsHandler(q *store.Queries) mcp.ToolHandlerFor[listLocationsInput, listLocationsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ listLocationsInput) (*mcp.CallToolResult, listLocationsOutput, error) {
		locations, err := q.ListLocations(ctx)
		if err != nil {
			return nil, listLocationsOutput{}, sanitizeToolError(ctx, "list_locations", err)
		}
		out := listLocationsOutput{Locations: make([]locationSummary, len(locations))}
		for i, l := range locations {
			out.Locations[i] = locationSummary{ID: l.ID, Name: l.Name}
		}
		return nil, out, nil
	}
}

// --- move_item ---

type moveItemInput struct {
	Item       string `json:"item" jsonschema:"the item's name, or close to it"`
	NewStorage string `json:"new_storage" jsonschema:"where to move it — a storage's name, or close to it"`
}

type moveItemOutput struct {
	Item        string `json:"item" jsonschema:"the matched item's actual name"`
	FromStorage string `json:"from_storage"`
	ToStorage   string `json:"to_storage"`
}

// auditDetails is the shape written to audit_log.details for a move_item
// call — just enough to reconstruct what happened without re-deriving it
// from other tables later.
type auditDetails struct {
	FromStorageID int64 `json:"from_storage_id"`
	ToStorageID   int64 `json:"to_storage_id"`
}

func moveItemHandler(pool *pgxpool.Pool) mcp.ToolHandlerFor[moveItemInput, moveItemOutput] {
	// A read-only lookup pass (bound to the pool, not a transaction) resolves
	// both names first — resolveItem/resolveStorage errors should surface
	// as plain "not found" tool errors, not a rolled-back transaction's
	// generic failure.
	q := store.New(pool)

	return func(ctx context.Context, _ *mcp.CallToolRequest, in moveItemInput) (*mcp.CallToolResult, moveItemOutput, error) {
		itemID, matchedItem, fromStorageID, err := resolveItem(ctx, q, in.Item)
		if err != nil {
			return nil, moveItemOutput{}, sanitizeToolError(ctx, "move_item", err)
		}
		toStorageID, _, err := resolveStorage(ctx, q, in.NewStorage)
		if err != nil {
			return nil, moveItemOutput{}, sanitizeToolError(ctx, "move_item", err)
		}
		fromBreadcrumb, err := breadcrumbText(ctx, q, fromStorageID)
		if err != nil {
			return nil, moveItemOutput{}, sanitizeToolError(ctx, "move_item", err)
		}
		// Both ends of the move get the same full breadcrumb treatment —
		// matchedToStorage alone (resolveStorage's bare matched name) used to
		// leave `to_storage` looking like a leaf name next to `from_storage`'s
		// full path, an asymmetry an LLM relaying this would likely repeat
		// back to the user as a less useful destination than it actually is.
		toBreadcrumb, err := breadcrumbText(ctx, q, toStorageID)
		if err != nil {
			return nil, moveItemOutput{}, sanitizeToolError(ctx, "move_item", err)
		}

		details, err := json.Marshal(auditDetails{FromStorageID: fromStorageID, ToStorageID: toStorageID})
		if err != nil {
			return nil, moveItemOutput{}, sanitizeToolError(ctx, "move_item", err)
		}

		// The reassignment and its audit trail commit or fail together —
		// otherwise a failure between the two either loses the move or
		// records a move that never happened. UpdateItemStorage's affected
		// row count also guards a TOCTOU: resolveItem ran on the pool, before
		// this transaction started, so a concurrent delete of the item in
		// between would otherwise make the UPDATE a silent no-op — 0 rows
		// affected, no error — while InsertAuditLog still committed a
		// "moved" row and this handler still reported success for a move
		// that never happened.
		err = withTx(ctx, pool, func(tx pgx.Tx) error {
			txq := store.New(tx)
			rows, err := txq.UpdateItemStorage(ctx, store.UpdateItemStorageParams{
				ID: itemID, StorageID: toStorageID,
			})
			if err != nil {
				return err
			}
			if rows == 0 {
				return toolErrorf("item %q was deleted before the move completed", matchedItem)
			}
			return txq.InsertAuditLog(ctx, store.InsertAuditLogParams{
				EntityType: "item",
				EntityID:   itemID,
				Action:     "moved",
				UserID:     nil, // no auth yet — see CLAUDE.md
				Details:    details,
			})
		})
		if err != nil {
			return nil, moveItemOutput{}, sanitizeToolError(ctx, "move_item", err)
		}

		return nil, moveItemOutput{Item: matchedItem, FromStorage: fromBreadcrumb, ToStorage: toBreadcrumb}, nil
	}
}
