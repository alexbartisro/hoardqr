package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"hoardqr/internal/codegen"
	"hoardqr/internal/store"
)

type ItemsHandler struct {
	pool *pgxpool.Pool
	q    *store.Queries
}

func NewItemsHandler(pool *pgxpool.Pool) *ItemsHandler {
	return &ItemsHandler{pool: pool, q: store.New(pool)}
}

func (h *ItemsHandler) Routes(r chi.Router) {
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Get("/{id}", h.get)
	r.Patch("/{id}", h.update)
	r.Delete("/{id}", h.delete)
}

// GET /api/items — two shapes behind one path, matching lib/api.ts's mock:
//   - plain filters (q/tag/location_id) -> a bare Item[] (getItems)
//   - ?sort=created_desc&page=&pageSize= -> {entries, total} (getRecentItems,
//     the dashboard's newest-first feed — not in §9's table, see CLAUDE.md)
func (h *ItemsHandler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("sort") == "created_desc" {
		h.listRecent(w, r)
		return
	}

	var locationID *int64
	if raw := q.Get("location_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "location_id must be an integer")
			return
		}
		locationID = &id
	}
	var queryText, tag *string
	if v := q.Get("q"); v != "" {
		queryText = &v
	}
	if v := q.Get("tag"); v != "" {
		tag = &v
	}

	rows, err := h.q.ListItems(r.Context(), store.ListItemsParams{
		LocationID: locationID,
		Q:          queryText,
		Tag:        tag,
	})
	if err != nil {
		serverError(w, r, err)
		return
	}
	items := make([]ItemDTO, len(rows))
	for i, row := range rows {
		items[i] = toItemDTOFromListRow(row)
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *ItemsHandler) listRecent(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page := int32(1)
	if raw := q.Get("page"); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 32); err == nil && v > 0 {
			page = int32(v)
		}
	}
	pageSize := int32(10)
	if raw := q.Get("pageSize"); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 32); err == nil && v > 0 {
			pageSize = int32(v)
		}
	}

	rows, err := h.q.ListRecentItems(r.Context(), store.ListRecentItemsParams{
		Limit:  pageSize,
		Offset: (page - 1) * pageSize,
	})
	if err != nil {
		serverError(w, r, err)
		return
	}
	total, err := h.q.CountItems(r.Context())
	if err != nil {
		serverError(w, r, err)
		return
	}

	type entry struct {
		Item       ItemDTO `json:"item"`
		Breadcrumb string  `json:"breadcrumb"`
	}
	entries := make([]entry, len(rows))
	for i, row := range rows {
		// Deepest-location-first (opposite of every other breadcrumb in the
		// app) — deliberate, see CLAUDE.md: for a scan-down-the-list glance,
		// the immediate location is the more useful headline than the root.
		crumb, err := h.q.LocationBreadcrumb(r.Context(), row.LocationID)
		if err != nil {
			serverError(w, r, err)
			return
		}
		names := make([]string, len(crumb))
		for j, c := range crumb {
			names[len(crumb)-1-j] = c.Name
		}
		entries[i] = entry{Item: toItemDTOFromRecentRow(row), Breadcrumb: strings.Join(names, " > ")}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "total": total})
}

// GET /api/items/:id
func (h *ItemsHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := parseIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	row, err := h.q.GetItemByID(r.Context(), id)
	if isNoRows(err) {
		notFound(w, "item")
		return
	}
	if err != nil {
		serverError(w, r, err)
		return
	}
	breadcrumbRows, err := h.q.LocationBreadcrumb(r.Context(), row.LocationID)
	if err != nil {
		serverError(w, r, err)
		return
	}
	breadcrumb := make([]BreadcrumbEntryDTO, len(breadcrumbRows))
	for i, b := range breadcrumbRows {
		breadcrumb[i] = BreadcrumbEntryDTO{ID: b.ID, Name: b.Name}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"item":       toItemDTOFromGetRow(row),
		"breadcrumb": breadcrumb,
	})
}

type createItemRequest struct {
	Name          string           `json:"name"`
	LocationID    int64            `json:"location_id"`
	Description   *string          `json:"description"`
	Quantity      *int32           `json:"quantity"`
	Condition     *string          `json:"condition"`
	QrToken       *string          `json:"qr_token"`
	PhotoURL      *string          `json:"photo_url"`
	PurchaseDate  *string          `json:"purchase_date"`
	PurchasePrice *float64         `json:"purchase_price"`
	ReceiptURL    *string          `json:"receipt_url"`
	CustomFields  *json.RawMessage `json:"custom_fields"`
	IsShared      *bool            `json:"is_shared"`
	Tags          []string         `json:"tags"`
}

// resolveTagIDs upserts-by-case-insensitive-name every tag name given,
// creating rows for any that don't exist yet — the same "created in the
// background and persisted" guarantee createTag gives the tag autocomplete
// (web/src/lib/api.ts), but enforced here too so any caller of this endpoint
// (MCP's add_item later, for instance) gets it for free, not just the UI
// path that happens to call POST /api/tags first. Takes q explicitly (not
// h.q) so callers running inside a transaction pass the tx-scoped Queries —
// see withTx.
func resolveTagIDs(ctx context.Context, q *store.Queries, names []string) ([]int64, error) {
	ids := make([]int64, 0, len(names))
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		tag, err := q.GetTagByNameCI(ctx, trimmed)
		if isNoRows(err) {
			tag, err = q.InsertTag(ctx, trimmed)
		}
		if err != nil {
			return nil, err
		}
		ids = append(ids, tag.ID)
	}
	return ids, nil
}

// POST /api/items
func (h *ItemsHandler) create(w http.ResponseWriter, r *http.Request) {
	var req createItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	exists, err := h.q.LocationExists(r.Context(), req.LocationID)
	if err != nil {
		serverError(w, r, err)
		return
	}
	if !exists {
		writeError(w, http.StatusUnprocessableEntity, "location does not exist")
		return
	}

	quantity := int32(1)
	if req.Quantity != nil {
		quantity = *req.Quantity
	}
	isShared := true
	if req.IsShared != nil {
		isShared = *req.IsShared
	}
	qrToken := req.QrToken
	if qrToken == nil {
		token := codegen.PlainTextCode()
		qrToken = &token
	}
	purchaseDate, err := parseNullableDate(req.PurchaseDate)
	if err != nil {
		writeError(w, http.StatusBadRequest, "purchase_date must be YYYY-MM-DD")
		return
	}
	customFields := []byte("{}")
	// An explicit `"custom_fields": null` never reaches validateCustomFieldsObject
	// here: CustomFields is *json.RawMessage, and encoding/json sets an
	// explicit JSON null on a pointer field to a nil Go pointer — the same
	// nil this check sees for an omitted field. It falls through to the
	// default below instead of being rejected. Harmless (treating explicit
	// null as "not provided" is reasonable), but worth knowing: PATCH
	// /api/items/:id *does* reject an explicit null for the same field,
	// since its generic map[string]json.RawMessage decode can actually see
	// it as present. See TestItemsRejectNonObjectCustomFields.
	if req.CustomFields != nil {
		if err := validateCustomFieldsObject(*req.CustomFields); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		customFields = *req.CustomFields
	}

	// Insert + tag resolution + linking run as one transaction — otherwise a
	// mid-sequence failure (e.g. the second of three tag links) leaves a real
	// item row with only some of its tags attached instead of either fully
	// succeeding or not existing at all.
	var item store.Item
	var tagNames []string
	err = withTx(r.Context(), h.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		var err error
		item, err = q.InsertItem(r.Context(), store.InsertItemParams{
			LocationID:    req.LocationID,
			OwnerID:       nil, // no auth yet (Phase 3 step 5)
			IsShared:      isShared,
			Name:          req.Name,
			Description:   req.Description,
			Quantity:      quantity,
			Condition:     req.Condition,
			QrToken:       *qrToken,
			PhotoUrl:      req.PhotoURL,
			PurchaseDate:  purchaseDate,
			PurchasePrice: floatToNumeric(req.PurchasePrice),
			ReceiptUrl:    req.ReceiptURL,
			CustomFields:  customFields,
		})
		if err != nil {
			return err
		}
		tagIDs, err := resolveTagIDs(r.Context(), q, req.Tags)
		if err != nil {
			return err
		}
		for _, tagID := range tagIDs {
			if err := q.LinkItemTag(r.Context(), store.LinkItemTagParams{ItemID: item.ID, TagID: tagID}); err != nil {
				return err
			}
		}
		tagNames, err = q.TagNamesForItem(r.Context(), item.ID)
		return err
	})
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toItemDTOFromItem(item, tagNames))
}

// PATCH /api/items/:id — edit (including code), move, toggle is_shared (§9).
// Same hand-written-dynamic-SET reasoning as LocationsHandler.update, plus
// "tags" gets special handling since it isn't a real items column — it's the
// item_tags join, replaced wholesale rather than merged (matching the mock's
// plain Object.assign(item, patch), which overwrites the whole tags array).
func (h *ItemsHandler) update(w http.ResponseWriter, r *http.Request) {
	id, err := parseIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	var fields map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	set := map[string]any{}
	var newTags []string
	var hasTags bool
	for key, raw := range fields {
		switch key {
		case "name":
			var v string
			if err := json.Unmarshal(raw, &v); err != nil {
				writeError(w, http.StatusBadRequest, "name must be a string")
				return
			}
			set["name"] = v
		case "location_id":
			var v int64
			if err := json.Unmarshal(raw, &v); err != nil {
				writeError(w, http.StatusBadRequest, "location_id must be an integer")
				return
			}
			exists, err := h.q.LocationExists(r.Context(), v)
			if err != nil {
				serverError(w, r, err)
				return
			}
			if !exists {
				writeError(w, http.StatusUnprocessableEntity, "location does not exist")
				return
			}
			set["location_id"] = v
		case "description":
			var v *string
			if err := json.Unmarshal(raw, &v); err != nil {
				writeError(w, http.StatusBadRequest, "description must be a string or null")
				return
			}
			set["description"] = v
		case "quantity":
			var v int32
			if err := json.Unmarshal(raw, &v); err != nil {
				writeError(w, http.StatusBadRequest, "quantity must be an integer")
				return
			}
			set["quantity"] = v
		case "condition":
			var v *string
			if err := json.Unmarshal(raw, &v); err != nil {
				writeError(w, http.StatusBadRequest, "condition must be a string or null")
				return
			}
			set["condition"] = v
		case "qr_token":
			var v string
			if err := json.Unmarshal(raw, &v); err != nil {
				writeError(w, http.StatusBadRequest, "qr_token must be a string")
				return
			}
			set["qr_token"] = v
		case "photo_url":
			var v *string
			if err := json.Unmarshal(raw, &v); err != nil {
				writeError(w, http.StatusBadRequest, "photo_url must be a string or null")
				return
			}
			set["photo_url"] = v
		case "purchase_date":
			var v *string
			if err := json.Unmarshal(raw, &v); err != nil {
				writeError(w, http.StatusBadRequest, "purchase_date must be a string or null")
				return
			}
			d, err := parseNullableDate(v)
			if err != nil {
				writeError(w, http.StatusBadRequest, "purchase_date must be YYYY-MM-DD")
				return
			}
			set["purchase_date"] = d
		case "purchase_price":
			var v *float64
			if err := json.Unmarshal(raw, &v); err != nil {
				writeError(w, http.StatusBadRequest, "purchase_price must be a number or null")
				return
			}
			set["purchase_price"] = floatToNumeric(v)
		case "receipt_url":
			var v *string
			if err := json.Unmarshal(raw, &v); err != nil {
				writeError(w, http.StatusBadRequest, "receipt_url must be a string or null")
				return
			}
			set["receipt_url"] = v
		case "custom_fields":
			if err := validateCustomFieldsObject(raw); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			set["custom_fields"] = []byte(raw)
		case "is_shared":
			var v bool
			if err := json.Unmarshal(raw, &v); err != nil {
				writeError(w, http.StatusBadRequest, "is_shared must be a boolean")
				return
			}
			set["is_shared"] = v
		case "tags":
			if err := json.Unmarshal(raw, &newTags); err != nil {
				writeError(w, http.StatusBadRequest, "tags must be an array of strings")
				return
			}
			hasTags = true
		}
	}
	if len(set) == 0 && !hasTags {
		writeError(w, http.StatusBadRequest, "no updatable fields provided")
		return
	}

	// Column update + tag replace/link run as one transaction, same reasoning
	// as create() — a rename that succeeds but a tag-link that fails partway
	// would otherwise leave the item half-updated.
	err = withTx(r.Context(), h.pool, func(tx pgx.Tx) error {
		if len(set) > 0 {
			query, args := buildUpdateQuery("items", id, set)
			// Appended as a literal SQL clause, not routed through `set` —
			// doing it via the map relied on "updated_at" sorting last among
			// the present keys to compute the right placeholder number and
			// strip the right arg, which broke for any column name sorting
			// after it.
			query = strings.Replace(query, " WHERE id = $1", ", updated_at = now() WHERE id = $1", 1)
			if _, err := tx.Exec(r.Context(), query, args...); err != nil {
				return err
			}
		}

		if hasTags {
			q := store.New(tx)
			tagIDs, err := resolveTagIDs(r.Context(), q, newTags)
			if err != nil {
				return err
			}
			if err := q.ReplaceItemTags(r.Context(), id); err != nil {
				return err
			}
			for _, tagID := range tagIDs {
				if err := q.LinkItemTag(r.Context(), store.LinkItemTagParams{ItemID: id, TagID: tagID}); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		if pgConflict(err) {
			writeError(w, http.StatusConflict, "conflict updating item")
			return
		}
		serverError(w, r, err)
		return
	}

	row, err := h.q.GetItemByID(r.Context(), id)
	if isNoRows(err) {
		notFound(w, "item")
		return
	}
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toItemDTOFromGetRow(row))
}

// DELETE /api/items/:id
func (h *ItemsHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := parseIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	exists, err := h.q.ItemExists(r.Context(), id)
	if err != nil {
		serverError(w, r, err)
		return
	}
	if !exists {
		notFound(w, "item")
		return
	}
	if err := h.q.DeleteItem(r.Context(), id); err != nil {
		serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func parseNullableDate(s *string) (pgtype.Date, error) {
	if s == nil || *s == "" {
		return pgtype.Date{}, nil
	}
	var d pgtype.Date
	if err := d.Scan(*s); err != nil {
		return pgtype.Date{}, err
	}
	return d, nil
}

func floatToNumeric(f *float64) pgtype.Numeric {
	if f == nil {
		return pgtype.Numeric{}
	}
	var n pgtype.Numeric
	_ = n.Scan(strconv.FormatFloat(*f, 'f', -1, 64))
	return n
}

// validateCustomFieldsObject rejects anything that isn't a JSON object —
// json.RawMessage only guarantees syntactically valid JSON, not that it's
// the shape items.custom_fields (JSONB NOT NULL DEFAULT '{}') and the
// frontend's Record<string, unknown> both assume. Without this, a scalar
// like `5`, an array, or `null` (which would otherwise violate the column's
// NOT NULL constraint as a raw Postgres error) is accepted and stored as-is.
func validateCustomFieldsObject(raw json.RawMessage) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("custom_fields must be valid JSON")
	}
	if _, ok := v.(map[string]any); !ok {
		return fmt.Errorf("custom_fields must be a JSON object")
	}
	return nil
}
