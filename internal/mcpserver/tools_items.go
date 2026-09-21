package mcpserver

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hoardqr/internal/store"
)

// optionalNumeric/optionalDate are internal/api/items.go's
// floatToNumeric/parseNullableDate, adapted to this file's "nil means leave
// unchanged" convention (see UpdateItemFields' own doc comment) rather than
// REST's "nil means explicit null" — a *float64/*string that's nil here
// must produce an invalid pgtype value, which COALESCE then reads as "keep
// the existing column value," not "set it to NULL."

func optionalNumeric(f *float64) pgtype.Numeric {
	if f == nil {
		return pgtype.Numeric{}
	}
	var n pgtype.Numeric
	_ = n.Scan(strconv.FormatFloat(*f, 'f', -1, 64))
	return n
}

func optionalDate(s *string) (pgtype.Date, error) {
	if s == nil || strings.TrimSpace(*s) == "" {
		return pgtype.Date{}, nil
	}
	var d pgtype.Date
	if err := d.Scan(*s); err != nil {
		return pgtype.Date{}, err
	}
	return d, nil
}

// isNumericOutOfRange mirrors internal/api/httpjson.go's pgNumericOutOfRange
// — Postgres's numeric_value_out_of_range (22003), e.g. a purchase_price
// too large for NUMERIC(10,2). Without this, add_item/edit_item both
// collapse that into sanitizeToolError's generic "internal error," which
// gives an LLM caller nothing to self-correct from — the REST API already
// fixed the identical gap for the same column.
func isNumericOutOfRange(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "22003"
}

// --- edit_item ---

type editItemInput struct {
	Item          string   `json:"item" jsonschema:"the item to edit, by name or close to it"`
	Name          *string  `json:"name,omitempty" jsonschema:"a new name; omit to leave unchanged"`
	Description   *string  `json:"description,omitempty" jsonschema:"a new description; omit to leave unchanged"`
	Quantity      *int32   `json:"quantity,omitempty" jsonschema:"a new quantity; omit to leave unchanged"`
	Condition     *string  `json:"condition,omitempty" jsonschema:"a new condition (e.g. new, good, fair, poor); omit to leave unchanged"`
	PurchaseDate  *string  `json:"purchase_date,omitempty" jsonschema:"YYYY-MM-DD; omit to leave unchanged"`
	PurchasePrice *float64 `json:"purchase_price,omitempty" jsonschema:"omit to leave unchanged"`
	ReceiptURL    *string  `json:"receipt_url,omitempty" jsonschema:"omit to leave unchanged"`
}

type editItemOutput struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// editItemHandler covers every item field except storage (move_item's job,
// with its own audit trail) and tags (add_item_tag/remove_item_tag's job,
// additive so a caller never needs an item's full existing tag list just to
// change one). None of these fields can be explicitly cleared back to NULL
// through this tool once set — see UpdateItemFields' doc comment for why;
// the web app's PATCH-based edit can do that if it's ever actually needed.
func editItemHandler(q *store.Queries) mcp.ToolHandlerFor[editItemInput, editItemOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in editItemInput) (*mcp.CallToolResult, editItemOutput, error) {
		if in.Name == nil && in.Description == nil && in.Quantity == nil && in.Condition == nil &&
			in.PurchaseDate == nil && in.PurchasePrice == nil && in.ReceiptURL == nil {
			return nil, editItemOutput{}, toolErrorf("provide at least one field to change")
		}
		var name *string
		if in.Name != nil {
			trimmed := strings.TrimSpace(*in.Name)
			if trimmed == "" {
				return nil, editItemOutput{}, toolErrorf("name cannot be blank")
			}
			name = &trimmed
		}

		itemID, _, _, err := resolveItem(ctx, q, in.Item)
		if err != nil {
			return nil, editItemOutput{}, sanitizeToolError(ctx, "edit_item", err)
		}

		purchaseDate, err := optionalDate(in.PurchaseDate)
		if err != nil {
			return nil, editItemOutput{}, toolErrorf("purchase_date must be YYYY-MM-DD")
		}

		updated, err := q.UpdateItemFields(ctx, store.UpdateItemFieldsParams{
			ID:            itemID,
			Name:          name,
			Description:   in.Description,
			Quantity:      in.Quantity,
			Condition:     in.Condition,
			PurchaseDate:  purchaseDate,
			PurchasePrice: optionalNumeric(in.PurchasePrice),
			ReceiptUrl:    in.ReceiptURL,
		})
		if err != nil {
			if isNumericOutOfRange(err) {
				return nil, editItemOutput{}, toolErrorf("purchase_price is out of range")
			}
			return nil, editItemOutput{}, sanitizeToolError(ctx, "edit_item", err)
		}
		return nil, editItemOutput{ID: updated.ID, Name: updated.Name}, nil
	}
}

// --- delete_item ---

type deleteItemInput struct {
	Item string `json:"item" jsonschema:"the item to delete — its exact name or its numeric id (see find_items/where_is). Fuzzy matching is deliberately not used here, unlike every other tool: a delete must never act on an approximate match."`
}

type deleteItemOutput struct {
	Deleted string `json:"deleted"`
}

// deleteItemHandler has no cascade or force concern the way delete_storage
// does — an item has nothing nested inside it to protect. Still resolves
// via resolveItemStrict, not the fuzzy resolveItem every other tool uses
// (added 2026-09-20, user request) — same reasoning as delete_storage: a
// delete must never act on an approximate match.
func deleteItemHandler(q *store.Queries) mcp.ToolHandlerFor[deleteItemInput, deleteItemOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in deleteItemInput) (*mcp.CallToolResult, deleteItemOutput, error) {
		itemID, matchedName, err := resolveItemStrict(ctx, q, in.Item)
		if err != nil {
			return nil, deleteItemOutput{}, sanitizeToolError(ctx, "delete_item", err)
		}
		if err := q.DeleteItem(ctx, itemID); err != nil {
			return nil, deleteItemOutput{}, sanitizeToolError(ctx, "delete_item", err)
		}
		return nil, deleteItemOutput{Deleted: matchedName}, nil
	}
}

// --- add_item_tag ---

type addItemTagInput struct {
	Item string `json:"item" jsonschema:"the item to tag, by name or close to it"`
	Tag  string `json:"tag" jsonschema:"the tag name to add — created automatically (case-insensitively) if it doesn't exist yet"`
}

type addItemTagOutput struct {
	Item string   `json:"item"`
	Tags []string `json:"tags" jsonschema:"the item's full tag list after this change"`
}

// addItemTagHandler is additive and idempotent — adding a tag the item
// already has is a no-op success, not an error, matching LinkItemTag's own
// ON CONFLICT DO NOTHING. This (plus remove_item_tag) exists specifically
// so a caller can act on a request like "add the electronic tool tag to the
// drill" without first having to look up the drill's entire existing tag
// list, which a REST-style full-array PATCH would require.
func addItemTagHandler(pool *pgxpool.Pool) mcp.ToolHandlerFor[addItemTagInput, addItemTagOutput] {
	q := store.New(pool)
	return func(ctx context.Context, _ *mcp.CallToolRequest, in addItemTagInput) (*mcp.CallToolResult, addItemTagOutput, error) {
		tagName := strings.TrimSpace(in.Tag)
		if tagName == "" {
			return nil, addItemTagOutput{}, toolErrorf("tag is required")
		}
		itemID, matchedItem, _, err := resolveItem(ctx, q, in.Item)
		if err != nil {
			return nil, addItemTagOutput{}, sanitizeToolError(ctx, "add_item_tag", err)
		}

		var tags []string
		txErr := withTx(ctx, pool, func(tx pgx.Tx) error {
			txq := store.New(tx)
			tagIDs, err := resolveTagIDs(ctx, txq, []string{tagName})
			if err != nil {
				return err
			}
			if err := txq.LinkItemTag(ctx, store.LinkItemTagParams{ItemID: itemID, TagID: tagIDs[0]}); err != nil {
				return err
			}
			tags, err = txq.TagNamesForItem(ctx, itemID)
			return err
		})
		if txErr != nil {
			return nil, addItemTagOutput{}, sanitizeToolError(ctx, "add_item_tag", txErr)
		}
		return nil, addItemTagOutput{Item: matchedItem, Tags: tags}, nil
	}
}

// --- remove_item_tag ---

type removeItemTagInput struct {
	Item string `json:"item" jsonschema:"the item to untag, by name or close to it"`
	Tag  string `json:"tag" jsonschema:"the tag name to remove (matched case-insensitively)"`
}

type removeItemTagOutput struct {
	Item string   `json:"item"`
	Tags []string `json:"tags" jsonschema:"the item's full tag list after this change"`
}

// removeItemTagHandler errors on a name that doesn't resolve to a real tag,
// or a real tag the item isn't actually wearing — both most likely a typo
// worth surfacing rather than a silent no-op, unlike add_item_tag's
// deliberately idempotent success.
func removeItemTagHandler(q *store.Queries) mcp.ToolHandlerFor[removeItemTagInput, removeItemTagOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in removeItemTagInput) (*mcp.CallToolResult, removeItemTagOutput, error) {
		tagName := strings.TrimSpace(in.Tag)
		if tagName == "" {
			return nil, removeItemTagOutput{}, toolErrorf("tag is required")
		}
		itemID, matchedItem, _, err := resolveItem(ctx, q, in.Item)
		if err != nil {
			return nil, removeItemTagOutput{}, sanitizeToolError(ctx, "remove_item_tag", err)
		}
		tag, err := q.GetTagByNameCI(ctx, tagName)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, removeItemTagOutput{}, toolErrorf("no tag named %q exists", tagName)
		}
		if err != nil {
			return nil, removeItemTagOutput{}, sanitizeToolError(ctx, "remove_item_tag", err)
		}
		rows, err := q.UnlinkItemTag(ctx, store.UnlinkItemTagParams{ItemID: itemID, TagID: tag.ID})
		if err != nil {
			return nil, removeItemTagOutput{}, sanitizeToolError(ctx, "remove_item_tag", err)
		}
		if rows == 0 {
			return nil, removeItemTagOutput{}, toolErrorf("%q isn't tagged with %q", matchedItem, tag.Name)
		}
		tags, err := q.TagNamesForItem(ctx, itemID)
		if err != nil {
			return nil, removeItemTagOutput{}, sanitizeToolError(ctx, "remove_item_tag", err)
		}
		return nil, removeItemTagOutput{Item: matchedItem, Tags: tags}, nil
	}
}
