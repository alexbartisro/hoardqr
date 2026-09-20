package api

import (
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"hoardqr/internal/store"
)

// DTO structs mirror web/src/lib/types.ts field-for-field (including
// snake_case) so the frontend's fetch wrapper can decode the JSON body
// straight into its existing TS interfaces — no translation layer on either
// side. Kept separate from the sqlc-generated store types (pgtype.Numeric,
// pgtype.Date, pgtype.Timestamptz) since none of those marshal to JSON in
// the plain shape the frontend already expects.

type StorageDTO struct {
	ID        int64   `json:"id"`
	ParentID  *int64  `json:"parent_id"`
	OwnerID   *int64  `json:"owner_id"`
	IsShared  bool    `json:"is_shared"`
	Name      string  `json:"name"`
	QrToken   string  `json:"qr_token"`
	PhotoURL  *string `json:"photo_url"`
	Notes     *string `json:"notes"`
	CreatedAt string  `json:"created_at"`
}

func toStorageDTO(s store.Storage) StorageDTO {
	return StorageDTO{
		ID:        s.ID,
		ParentID:  s.ParentID,
		OwnerID:   s.OwnerID,
		IsShared:  s.IsShared,
		Name:      s.Name,
		QrToken:   s.QrToken,
		PhotoURL:  s.PhotoUrl,
		Notes:     s.Notes,
		CreatedAt: timestamptzToString(s.CreatedAt),
	}
}

func toStorageDTOs(ss []store.Storage) []StorageDTO {
	out := make([]StorageDTO, len(ss))
	for i, s := range ss {
		out[i] = toStorageDTO(s)
	}
	return out
}

type BreadcrumbEntryDTO struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type ItemDTO struct {
	ID            int64           `json:"id"`
	StorageID     int64           `json:"storage_id"`
	OwnerID       *int64          `json:"owner_id"`
	IsShared      bool            `json:"is_shared"`
	Name          string          `json:"name"`
	Description   *string         `json:"description"`
	Quantity      int32           `json:"quantity"`
	Condition     *string         `json:"condition"`
	QrToken       string          `json:"qr_token"`
	PhotoURL      *string         `json:"photo_url"`
	PurchaseDate  *string         `json:"purchase_date"`
	PurchasePrice *float64        `json:"purchase_price"`
	ReceiptURL    *string         `json:"receipt_url"`
	CustomFields  json.RawMessage `json:"custom_fields"`
	CreatedAt     string          `json:"created_at"`
	UpdatedAt     string          `json:"updated_at"`
	Tags          []string        `json:"tags"`
}

// itemFields is the shape every item-returning sqlc query shares — Item
// (from InsertItem, which has no Tags column) and the four GROUP BY variants
// (GetItemByIDRow, ListItemsRow, ListItemsByStorageIDsRow,
// ListRecentItemsRow) all have identical fields but distinct generated
// struct types, so each gets a thin wrapper into this one conversion.
type itemFields struct {
	ID            int64
	StorageID     int64
	OwnerID       *int64
	IsShared      bool
	Name          string
	Description   *string
	Quantity      int32
	Condition     *string
	QrToken       string
	PhotoUrl      *string
	PurchaseDate  pgtype.Date
	PurchasePrice pgtype.Numeric
	ReceiptUrl    *string
	CustomFields  []byte
	CreatedAt     pgtype.Timestamptz
	UpdatedAt     pgtype.Timestamptz
}

func toItemDTO(f itemFields, tags []string) ItemDTO {
	if tags == nil {
		tags = []string{}
	}
	return ItemDTO{
		ID:            f.ID,
		StorageID:     f.StorageID,
		OwnerID:       f.OwnerID,
		IsShared:      f.IsShared,
		Name:          f.Name,
		Description:   f.Description,
		Quantity:      f.Quantity,
		Condition:     f.Condition,
		QrToken:       f.QrToken,
		PhotoURL:      f.PhotoUrl,
		PurchaseDate:  dateToString(f.PurchaseDate),
		PurchasePrice: numericToFloat64(f.PurchasePrice),
		ReceiptURL:    f.ReceiptUrl,
		CustomFields:  customFieldsOrEmpty(f.CustomFields),
		CreatedAt:     timestamptzToString(f.CreatedAt),
		UpdatedAt:     timestamptzToString(f.UpdatedAt),
		Tags:          tags,
	}
}

func toItemDTOFromItem(i store.Item, tags []string) ItemDTO {
	return toItemDTO(itemFields(i), tags)
}

func toItemDTOFromGetRow(r store.GetItemByIDRow) ItemDTO {
	return toItemDTO(itemFields{
		ID: r.ID, StorageID: r.StorageID, OwnerID: r.OwnerID, IsShared: r.IsShared,
		Name: r.Name, Description: r.Description, Quantity: r.Quantity, Condition: r.Condition,
		QrToken: r.QrToken, PhotoUrl: r.PhotoUrl, PurchaseDate: r.PurchaseDate,
		PurchasePrice: r.PurchasePrice, ReceiptUrl: r.ReceiptUrl, CustomFields: r.CustomFields,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}, r.Tags)
}

func toItemDTOFromListRow(r store.ListItemsRow) ItemDTO {
	return toItemDTO(itemFields{
		ID: r.ID, StorageID: r.StorageID, OwnerID: r.OwnerID, IsShared: r.IsShared,
		Name: r.Name, Description: r.Description, Quantity: r.Quantity, Condition: r.Condition,
		QrToken: r.QrToken, PhotoUrl: r.PhotoUrl, PurchaseDate: r.PurchaseDate,
		PurchasePrice: r.PurchasePrice, ReceiptUrl: r.ReceiptUrl, CustomFields: r.CustomFields,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}, r.Tags)
}

func toItemDTOFromContentsRow(r store.ListItemsByStorageIDsRow) ItemDTO {
	return toItemDTO(itemFields{
		ID: r.ID, StorageID: r.StorageID, OwnerID: r.OwnerID, IsShared: r.IsShared,
		Name: r.Name, Description: r.Description, Quantity: r.Quantity, Condition: r.Condition,
		QrToken: r.QrToken, PhotoUrl: r.PhotoUrl, PurchaseDate: r.PurchaseDate,
		PurchasePrice: r.PurchasePrice, ReceiptUrl: r.ReceiptUrl, CustomFields: r.CustomFields,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}, r.Tags)
}

func toItemDTOFromScanRow(r store.FindItemsByNormalizedCodeRow) ItemDTO {
	return toItemDTO(itemFields{
		ID: r.ID, StorageID: r.StorageID, OwnerID: r.OwnerID, IsShared: r.IsShared,
		Name: r.Name, Description: r.Description, Quantity: r.Quantity, Condition: r.Condition,
		QrToken: r.QrToken, PhotoUrl: r.PhotoUrl, PurchaseDate: r.PurchaseDate,
		PurchasePrice: r.PurchasePrice, ReceiptUrl: r.ReceiptUrl, CustomFields: r.CustomFields,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}, r.Tags)
}

func toItemDTOFromRecentRow(r store.ListRecentItemsRow) ItemDTO {
	return toItemDTO(itemFields{
		ID: r.ID, StorageID: r.StorageID, OwnerID: r.OwnerID, IsShared: r.IsShared,
		Name: r.Name, Description: r.Description, Quantity: r.Quantity, Condition: r.Condition,
		QrToken: r.QrToken, PhotoUrl: r.PhotoUrl, PurchaseDate: r.PurchaseDate,
		PurchasePrice: r.PurchasePrice, ReceiptUrl: r.ReceiptUrl, CustomFields: r.CustomFields,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}, r.Tags)
}

// SearchSuggestionDTO mirrors SearchSuggestion in web/src/lib/types.ts.
// StorageID/Breadcrumb are only ever set on item hits (optional fields on
// the frontend type) — omitempty so a storage/tag hit's JSON simply omits
// them rather than sending explicit nulls.
type SearchSuggestionDTO struct {
	Kind       string  `json:"kind"`
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Score      float32 `json:"score"`
	StorageID  *int64  `json:"storage_id,omitempty"`
	Breadcrumb *string `json:"breadcrumb,omitempty"`
}

type TagDTO struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func toTagDTO(t store.Tag) TagDTO {
	return TagDTO{ID: t.ID, Name: t.Name}
}

func toTagDTOs(ts []store.Tag) []TagDTO {
	out := make([]TagDTO, len(ts))
	for i, t := range ts {
		out[i] = toTagDTO(t)
	}
	return out
}

func timestamptzToString(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return t.Time.UTC().Format(time.RFC3339)
}

func dateToString(d pgtype.Date) *string {
	if !d.Valid {
		return nil
	}
	s := d.Time.Format("2006-01-02")
	return &s
}

func numericToFloat64(n pgtype.Numeric) *float64 {
	if !n.Valid {
		return nil
	}
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return nil
	}
	return &f.Float64
}

// customFieldsOrEmpty guarantees a valid JSON value even for a row whose
// jsonb column somehow scanned as empty bytes — Item.custom_fields on the
// frontend is typed as a plain object, never undefined.
func customFieldsOrEmpty(b []byte) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage("{}")
	}
	return json.RawMessage(b)
}
