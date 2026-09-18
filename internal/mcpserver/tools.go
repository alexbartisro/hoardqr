package mcpserver

import (
	"context"
	"encoding/json"

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
		Description: "Search for items by name, tag, or code. Returns each match with the location it's stored in.",
	}, findItemsHandler(q))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "where_is",
		Description: "Resolve an item's name to the full location path it's stored in.",
	}, whereIsHandler(q))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_contents",
		Description: "List everything stored in a location, including everything nested inside its child locations.",
	}, listContentsHandler(q))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "add_item",
		Description: "Catalog a new object and place it in a location.",
	}, addItemHandler(q))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "add_location",
		Description: "Create a new storage location (a box, shelf, cabinet, room, etc.), optionally nested inside an existing one.",
	}, addLocationHandler(q))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "move_item",
		Description: "Move an existing item to a different location.",
	}, moveItemHandler(pool))
}

// --- find_items ---

type findItemsInput struct {
	Query string `json:"query" jsonschema:"the name, tag, or code to search for"`
}

type foundItem struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Location string `json:"location" jsonschema:"breadcrumb path from root to where this item lives"`
}

type findItemsOutput struct {
	Items []foundItem `json:"items"`
}

func findItemsHandler(q *store.Queries) mcp.ToolHandlerFor[findItemsInput, findItemsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in findItemsInput) (*mcp.CallToolResult, findItemsOutput, error) {
		rows, err := q.SearchSuggest(ctx, in.Query)
		if err != nil {
			return nil, findItemsOutput{}, err
		}
		out := findItemsOutput{Items: []foundItem{}}
		seen := make(map[int64]bool)

		for _, row := range rows {
			if row.Kind != "item" || row.LocationID == nil {
				continue
			}
			breadcrumb, err := breadcrumbText(ctx, q, *row.LocationID)
			if err != nil {
				return nil, findItemsOutput{}, err
			}
			out.Items = append(out.Items, foundItem{ID: row.ID, Name: row.Name, Location: breadcrumb})
			seen[row.ID] = true
		}

		// SearchSuggest's own item/location/tag rows never expand a tag match
		// into the items carrying it — the description promises "by name, tag,
		// or code" though, so a query that only matches a tag name (not any
		// item's own name/code) still needs to surface its items here, via the
		// same exact-tag lookup ListItems already offers the REST item-browsing
		// endpoint (§9's Mock API surface gaps, see CLAUDE.md).
		for _, row := range rows {
			if row.Kind != "tag" {
				continue
			}
			tag := row.Name
			taggedItems, err := q.ListItems(ctx, store.ListItemsParams{Tag: &tag})
			if err != nil {
				return nil, findItemsOutput{}, err
			}
			for _, item := range taggedItems {
				if seen[item.ID] {
					continue
				}
				breadcrumb, err := breadcrumbText(ctx, q, item.LocationID)
				if err != nil {
					return nil, findItemsOutput{}, err
				}
				out.Items = append(out.Items, foundItem{ID: item.ID, Name: item.Name, Location: breadcrumb})
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
	Item     string `json:"item" jsonschema:"the matched item's actual name"`
	Location string `json:"location" jsonschema:"breadcrumb path from root to where it lives"`
}

func whereIsHandler(q *store.Queries) mcp.ToolHandlerFor[whereIsInput, whereIsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in whereIsInput) (*mcp.CallToolResult, whereIsOutput, error) {
		_, matchedName, locationID, err := resolveItem(ctx, q, in.Name)
		if err != nil {
			return nil, whereIsOutput{}, err
		}
		breadcrumb, err := breadcrumbText(ctx, q, locationID)
		if err != nil {
			return nil, whereIsOutput{}, err
		}
		return nil, whereIsOutput{Item: matchedName, Location: breadcrumb}, nil
	}
}

// --- list_contents ---

type listContentsInput struct {
	Location string `json:"location" jsonschema:"the location's name, or close to it"`
}

type contentItem struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Quantity int32  `json:"quantity"`
}

type listContentsOutput struct {
	Location string        `json:"location" jsonschema:"the matched location's actual name"`
	Path     string        `json:"path" jsonschema:"breadcrumb path from root to this location"`
	Items    []contentItem `json:"items" jsonschema:"every item stored here or in any location nested inside it"`
}

func listContentsHandler(q *store.Queries) mcp.ToolHandlerFor[listContentsInput, listContentsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in listContentsInput) (*mcp.CallToolResult, listContentsOutput, error) {
		locationID, matchedName, err := resolveLocation(ctx, q, in.Location)
		if err != nil {
			return nil, listContentsOutput{}, err
		}
		path, err := breadcrumbText(ctx, q, locationID)
		if err != nil {
			return nil, listContentsOutput{}, err
		}
		descendantIDs, err := q.DescendantLocationIDs(ctx, locationID)
		if err != nil {
			return nil, listContentsOutput{}, err
		}
		rows, err := q.ListItemsByLocationIDs(ctx, descendantIDs)
		if err != nil {
			return nil, listContentsOutput{}, err
		}
		out := listContentsOutput{Location: matchedName, Path: path, Items: []contentItem{}}
		for _, row := range rows {
			out.Items = append(out.Items, contentItem{ID: row.ID, Name: row.Name, Quantity: row.Quantity})
		}
		return nil, out, nil
	}
}

// --- add_item ---

type addItemInput struct {
	Name     string `json:"name" jsonschema:"the object's name"`
	Location string `json:"location" jsonschema:"where to store it — a location's name, or close to it"`
	Quantity *int32 `json:"quantity,omitempty" jsonschema:"how many; defaults to 1"`
}

type addItemOutput struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Location string `json:"location" jsonschema:"the matched location's actual name"`
	Code     string `json:"code" jsonschema:"the generated code, printable on a label for this item"`
}

func addItemHandler(q *store.Queries) mcp.ToolHandlerFor[addItemInput, addItemOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in addItemInput) (*mcp.CallToolResult, addItemOutput, error) {
		locationID, matchedLocation, err := resolveLocation(ctx, q, in.Location)
		if err != nil {
			return nil, addItemOutput{}, err
		}
		quantity := int32(1)
		if in.Quantity != nil {
			quantity = *in.Quantity
		}
		code := codegen.PlainTextCode()
		item, err := q.InsertItem(ctx, store.InsertItemParams{
			LocationID:   locationID,
			IsShared:     true, // no auth yet — every row is public until then (see CLAUDE.md)
			Name:         in.Name,
			Quantity:     quantity,
			QrToken:      code,
			CustomFields: []byte("{}"),
		})
		if err != nil {
			return nil, addItemOutput{}, err
		}
		return nil, addItemOutput{ID: item.ID, Name: item.Name, Location: matchedLocation, Code: item.QrToken}, nil
	}
}

// --- add_location ---

type addLocationInput struct {
	Name   string  `json:"name" jsonschema:"the new location's name"`
	Parent *string `json:"parent,omitempty" jsonschema:"an existing location to nest this inside; omit for a root-level location"`
}

type addLocationOutput struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Path string `json:"path" jsonschema:"breadcrumb path from root, including the new location itself"`
	Code string `json:"code" jsonschema:"the generated code, printable on a label for this location"`
}

func addLocationHandler(q *store.Queries) mcp.ToolHandlerFor[addLocationInput, addLocationOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in addLocationInput) (*mcp.CallToolResult, addLocationOutput, error) {
		var parentID *int64
		if in.Parent != nil {
			id, _, err := resolveLocation(ctx, q, *in.Parent)
			if err != nil {
				return nil, addLocationOutput{}, err
			}
			parentID = &id
		}

		// Retries on a generated-code collision (architecture plan §4: "a
		// non-event, not something to design around") — same reasoning as
		// internal/api/locations.go's create handler.
		var location store.Location
		for attempt := 0; ; attempt++ {
			var err error
			location, err = q.InsertLocation(ctx, store.InsertLocationParams{
				ParentID: parentID,
				IsShared: true, // no auth yet — every row is public until then (see CLAUDE.md)
				Name:     in.Name,
				QrToken:  codegen.PlainTextCode(),
			})
			if err == nil {
				break
			}
			if !isConflict(err) || attempt >= 5 {
				return nil, addLocationOutput{}, err
			}
		}

		path, err := breadcrumbText(ctx, q, location.ID)
		if err != nil {
			return nil, addLocationOutput{}, err
		}
		return nil, addLocationOutput{ID: location.ID, Name: location.Name, Path: path, Code: location.QrToken}, nil
	}
}

// --- move_item ---

type moveItemInput struct {
	Item        string `json:"item" jsonschema:"the item's name, or close to it"`
	NewLocation string `json:"new_location" jsonschema:"where to move it — a location's name, or close to it"`
}

type moveItemOutput struct {
	Item         string `json:"item" jsonschema:"the matched item's actual name"`
	FromLocation string `json:"from_location"`
	ToLocation   string `json:"to_location"`
}

// auditDetails is the shape written to audit_log.details for a move_item
// call — just enough to reconstruct what happened without re-deriving it
// from other tables later.
type auditDetails struct {
	FromLocationID int64 `json:"from_location_id"`
	ToLocationID   int64 `json:"to_location_id"`
}

func moveItemHandler(pool *pgxpool.Pool) mcp.ToolHandlerFor[moveItemInput, moveItemOutput] {
	// A read-only lookup pass (bound to the pool, not a transaction) resolves
	// both names first — resolveItem/resolveLocation errors should surface
	// as plain "not found" tool errors, not a rolled-back transaction's
	// generic failure.
	q := store.New(pool)

	return func(ctx context.Context, _ *mcp.CallToolRequest, in moveItemInput) (*mcp.CallToolResult, moveItemOutput, error) {
		itemID, matchedItem, fromLocationID, err := resolveItem(ctx, q, in.Item)
		if err != nil {
			return nil, moveItemOutput{}, err
		}
		toLocationID, matchedToLocation, err := resolveLocation(ctx, q, in.NewLocation)
		if err != nil {
			return nil, moveItemOutput{}, err
		}
		fromBreadcrumb, err := breadcrumbText(ctx, q, fromLocationID)
		if err != nil {
			return nil, moveItemOutput{}, err
		}

		details, err := json.Marshal(auditDetails{FromLocationID: fromLocationID, ToLocationID: toLocationID})
		if err != nil {
			return nil, moveItemOutput{}, err
		}

		// The reassignment and its audit trail commit or fail together —
		// otherwise a failure between the two either loses the move or
		// records a move that never happened.
		err = withTx(ctx, pool, func(tx pgx.Tx) error {
			txq := store.New(tx)
			if err := txq.UpdateItemLocation(ctx, store.UpdateItemLocationParams{
				ID: itemID, LocationID: toLocationID,
			}); err != nil {
				return err
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
			return nil, moveItemOutput{}, err
		}

		return nil, moveItemOutput{Item: matchedItem, FromLocation: fromBreadcrumb, ToLocation: matchedToLocation}, nil
	}
}
