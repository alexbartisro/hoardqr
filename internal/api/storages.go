package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hoardqr/internal/codegen"
	"hoardqr/internal/store"
)

type StoragesHandler struct {
	pool *pgxpool.Pool
	q    *store.Queries
}

func NewStoragesHandler(pool *pgxpool.Pool) *StoragesHandler {
	return &StoragesHandler{pool: pool, q: store.New(pool)}
}

func (h *StoragesHandler) Routes(r chi.Router) {
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Get("/{id}", h.get)
	r.Get("/{id}/contents", h.contents)
	r.Patch("/{id}", h.update)
	r.Delete("/{id}", h.delete)
}

func parseIDParam(r *http.Request) (int64, error) {
	return strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
}

// breadcrumbAndLocation fetches a storage's root-to-leaf path plus its
// resolved location (if any) — shared by every handler below that needs
// one alongside the storage/item itself.
func (h *StoragesHandler) breadcrumbAndLocation(ctx context.Context, storageID int64) ([]BreadcrumbEntryDTO, *LocationRefDTO, error) {
	rows, err := h.q.StorageBreadcrumb(ctx, storageID)
	if err != nil {
		return nil, nil, err
	}
	breadcrumb, location := breadcrumbAndLocation(rows)
	return breadcrumb, location, nil
}

// GET /api/storages?parent_id=
func (h *StoragesHandler) list(w http.ResponseWriter, r *http.Request) {
	var parentID *int64
	if raw := r.URL.Query().Get("parent_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "parent_id must be an integer")
			return
		}
		parentID = &id
	}
	storages, err := h.q.GetStoragesByParent(r.Context(), parentID)
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toStorageDTOsFromListRows(storages))
}

// GET /api/storages/:id
func (h *StoragesHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := parseIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	storage, err := h.q.GetStorageByID(r.Context(), id)
	if isNoRows(err) {
		notFound(w, "storage")
		return
	}
	if err != nil {
		serverError(w, r, err)
		return
	}
	breadcrumb, location, err := h.breadcrumbAndLocation(r.Context(), id)
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"storage":    toStorageDTO(storage),
		"breadcrumb": breadcrumb,
		"location":   location,
	})
}

// GET /api/storages/:id/contents — recursive: everything under it (§3).
func (h *StoragesHandler) contents(w http.ResponseWriter, r *http.Request) {
	id, err := parseIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	storage, err := h.q.GetStorageByID(r.Context(), id)
	if isNoRows(err) {
		notFound(w, "storage")
		return
	}
	if err != nil {
		serverError(w, r, err)
		return
	}
	breadcrumb, location, err := h.breadcrumbAndLocation(r.Context(), id)
	if err != nil {
		serverError(w, r, err)
		return
	}
	descendantIDs, err := h.q.DescendantStorageIDs(r.Context(), id)
	if err != nil {
		serverError(w, r, err)
		return
	}
	rows, err := h.q.ListItemsByStorageIDs(r.Context(), descendantIDs)
	if err != nil {
		serverError(w, r, err)
		return
	}
	items := make([]ItemDTO, len(rows))
	for i, row := range rows {
		items[i] = toItemDTOFromContentsRow(row)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"storage":    toStorageDTO(storage),
		"breadcrumb": breadcrumb,
		"location":   location,
		"items":      items,
	})
}

type createStorageRequest struct {
	Name       string  `json:"name"`
	ParentID   *int64  `json:"parent_id"`
	LocationID *int64  `json:"location_id"`
	QrToken    *string `json:"qr_token"`
	PhotoURL   *string `json:"photo_url"`
	Notes      *string `json:"notes"`
	IsShared   *bool   `json:"is_shared"`
}

// POST /api/storages
func (h *StoragesHandler) create(w http.ResponseWriter, r *http.Request) {
	var req createStorageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	// Mirrors ItemsHandler.create's StorageExists pre-check for storage_id
	// — without it, a nonexistent parent_id fell through to a raw FK
	// violation (23503) and a generic 500 instead of a clean 422, unlike
	// items, which already pre-checks. Same "check first" reasoning as
	// items: a storage doesn't get deleted out from under a create request
	// often enough for the pgConflict-style "let the DB constraint be the
	// race-free source of truth" argument to outweigh a clean error here.
	if req.ParentID != nil {
		exists, err := h.q.StorageExists(r.Context(), *req.ParentID)
		if err != nil {
			serverError(w, r, err)
			return
		}
		if !exists {
			writeError(w, http.StatusUnprocessableEntity, "parent storage does not exist")
			return
		}
	}

	// A location is only meaningful on a root storage — a nested storage
	// inherits its location transitively from its root ancestor (see
	// StorageBreadcrumb). storages_location_only_on_root (migration 000004)
	// enforces this at the DB level too; this pre-check turns what would
	// otherwise be a raw 23514 check-violation into a clean 422, same
	// reasoning as the ParentID existence check above.
	if req.ParentID != nil && req.LocationID != nil {
		writeError(w, http.StatusUnprocessableEntity, "a nested storage inherits its location from its root storage")
		return
	}
	if req.LocationID != nil {
		exists, err := h.q.LocationExists(r.Context(), *req.LocationID)
		if err != nil {
			serverError(w, r, err)
			return
		}
		if !exists {
			writeError(w, http.StatusUnprocessableEntity, "location does not exist")
			return
		}
	}

	isShared := true
	if req.IsShared != nil {
		isShared = *req.IsShared
	}

	// A caller-supplied qr_token (adopted retail barcode, or the scanned code
	// from /scan's "create here" outcome) that collides is a real conflict —
	// return 409. A collision on an auto-generated code is "a non-event, not
	// something to design around" (§4): retry a few times before giving up,
	// since the caller never saw the value that collided.
	//
	// An explicit qr_token: "" is treated the same as an absent one (still
	// auto-generated) — not as "the caller provided the empty string as
	// their token". Without this, "" occupies storages.qr_token's unique
	// slot, and the next caller that omits qr_token entirely gets a
	// confusing `qr_token "" already in use` 409 instead of a fresh
	// generated code.
	autoGenerated := req.QrToken == nil || strings.TrimSpace(*req.QrToken) == ""
	qrToken := ""
	if !autoGenerated {
		qrToken = *req.QrToken
	}

	var storage store.Storage
	var err error
	for attempt := 0; ; attempt++ {
		if autoGenerated {
			qrToken = codegen.PlainTextCode()
		}
		storage, err = h.q.InsertStorage(r.Context(), store.InsertStorageParams{
			ParentID:   req.ParentID,
			OwnerID:    nil, // no auth yet (Phase 3 step 5) — every row is ownerless until then
			IsShared:   isShared,
			Name:       req.Name,
			QrToken:    qrToken,
			PhotoUrl:   req.PhotoURL,
			Notes:      req.Notes,
			LocationID: req.LocationID,
		})
		if err == nil {
			break
		}
		if !pgConflict(err) {
			serverError(w, r, err)
			return
		}
		if !autoGenerated || attempt >= 5 {
			writeError(w, http.StatusConflict, fmt.Sprintf("qr_token %q already in use", qrToken))
			return
		}
	}
	writeJSON(w, http.StatusCreated, toStorageDTO(storage))
}

// PATCH /api/storages/:id — rename, move, edit code, toggle is_shared (§9).
// Hand-written rather than sqlc-generated: any subset of columns may be
// present, and a provided `parent_id: null` (make this a root storage) must
// be distinguishable from an absent parent_id (leave unchanged) — a
// COALESCE-based static query can't tell those apart, so this builds the SET
// clause from whichever keys actually appear in the JSON body.
func (h *StoragesHandler) update(w http.ResponseWriter, r *http.Request) {
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
	for key, raw := range fields {
		switch key {
		case "name":
			var v string
			if err := json.Unmarshal(raw, &v); err != nil {
				writeError(w, http.StatusBadRequest, "name must be a string")
				return
			}
			// Mirrors create's strings.TrimSpace(req.Name) == "" check — that
			// check only ever lived on create, so PATCH could blank out an
			// existing storage's name entirely.
			if strings.TrimSpace(v) == "" {
				writeError(w, http.StatusBadRequest, "name is required")
				return
			}
			set["name"] = v
		case "parent_id":
			var v *int64
			if err := json.Unmarshal(raw, &v); err != nil {
				writeError(w, http.StatusBadRequest, "parent_id must be an integer or null")
				return
			}
			set["parent_id"] = v
		case "location_id":
			var v *int64
			if err := json.Unmarshal(raw, &v); err != nil {
				writeError(w, http.StatusBadRequest, "location_id must be an integer or null")
				return
			}
			set["location_id"] = v
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
		case "notes":
			var v *string
			if err := json.Unmarshal(raw, &v); err != nil {
				writeError(w, http.StatusBadRequest, "notes must be a string or null")
				return
			}
			set["notes"] = v
		case "is_shared":
			var v bool
			if err := json.Unmarshal(raw, &v); err != nil {
				writeError(w, http.StatusBadRequest, "is_shared must be a boolean")
				return
			}
			set["is_shared"] = v
		}
	}
	if len(set) == 0 {
		writeError(w, http.StatusBadRequest, "no updatable fields provided")
		return
	}

	if token, ok := set["qr_token"].(string); ok {
		inUse, err := h.q.StorageQRTokenInUse(r.Context(), store.StorageQRTokenInUseParams{QrToken: token, ID: id})
		if err != nil {
			serverError(w, r, err)
			return
		}
		if inUse {
			writeError(w, http.StatusConflict, fmt.Sprintf("qr_token %q already in use", token))
			return
		}
	}

	// A new parent that is this storage itself, or one of its own
	// descendants, would create a cycle — the FK alone doesn't prevent this,
	// and StorageBreadcrumb/DescendantStorageIDs are unbounded recursive
	// CTEs that would then never terminate for anything touching the
	// resulting loop (item/storage detail, the dashboard feed, search, and
	// every MCP tool that resolves a storage). DescendantStorageIDs(id)
	// already includes id itself, so membership in that set is exactly the
	// "would cycle" condition, with no extra query needed.
	if newParentID, ok := set["parent_id"].(*int64); ok && newParentID != nil {
		// Mirrors create's StorageExists pre-check — without it, a
		// nonexistent parent_id fell through to a raw FK violation (23503)
		// and a generic 500 instead of a clean 422.
		exists, err := h.q.StorageExists(r.Context(), *newParentID)
		if err != nil {
			serverError(w, r, err)
			return
		}
		if !exists {
			writeError(w, http.StatusUnprocessableEntity, "parent storage does not exist")
			return
		}

		descendantIDs, err := h.q.DescendantStorageIDs(r.Context(), id)
		if err != nil {
			serverError(w, r, err)
			return
		}
		for _, d := range descendantIDs {
			if d == *newParentID {
				writeError(w, http.StatusConflict, "parent_id would create a cycle (it is this storage or one of its own descendants)")
				return
			}
		}

		// A location is only meaningful on a root storage — moving this
		// storage under a real parent must clear any location assigned to
		// it, rather than surface a raw 23514 check-violation (migration
		// 000004's storages_location_only_on_root). Reject outright rather
		// than silently override when the caller also explicitly asked for
		// a non-nil location_id in this same request — that combination is
		// contradictory, not a case to guess at.
		if newLocationID, ok := set["location_id"].(*int64); ok && newLocationID != nil {
			writeError(w, http.StatusUnprocessableEntity, "a nested storage inherits its location from its root storage")
			return
		}
		set["location_id"] = (*int64)(nil)
	} else if newLocationID, ok := set["location_id"].(*int64); ok && newLocationID != nil {
		// Assigning a location directly is only valid on a root storage.
		// This request's own parent_id key (if present) reflects the
		// *new* effective parent — e.g. "parent_id": null promoting this
		// storage to root in the same request as assigning a location —
		// so it takes precedence over the current DB value, which is only
		// consulted when parent_id isn't part of this request at all.
		effectiveParentID, hasParentKey := set["parent_id"].(*int64)
		if !hasParentKey {
			current, err := h.q.GetStorageByID(r.Context(), id)
			if isNoRows(err) {
				notFound(w, "storage")
				return
			}
			if err != nil {
				serverError(w, r, err)
				return
			}
			effectiveParentID = current.ParentID
		}
		if effectiveParentID != nil {
			writeError(w, http.StatusUnprocessableEntity, "a nested storage inherits its location from its root storage")
			return
		}

		exists, err := h.q.LocationExists(r.Context(), *newLocationID)
		if err != nil {
			serverError(w, r, err)
			return
		}
		if !exists {
			writeError(w, http.StatusUnprocessableEntity, "location does not exist")
			return
		}
	}

	query, args := buildUpdateQuery("storages", id, set)
	if _, err := h.pool.Exec(r.Context(), query, args...); err != nil {
		if pgConflict(err) {
			writeError(w, http.StatusConflict, "qr_token already in use")
			return
		}
		serverError(w, r, err)
		return
	}

	storage, err := h.q.GetStorageByID(r.Context(), id)
	if isNoRows(err) {
		notFound(w, "storage")
		return
	}
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toStorageDTO(storage))
}

// DELETE /api/storages/:id (§3). Child storages always become root-level
// automatically (the FK's ON DELETE SET NULL fires as part of the DELETE
// below) — never blocked. A root storage that still directly holds items
// always 409s, with no override — deleting a storage must never delete the
// items inside it (user decision, 2026-09-20, reversing this handler's
// original `?force=true` escape hatch, which used to delete those items
// outright; see CLAUDE.md). The caller must move the items elsewhere first
// (PATCH their storage_id, or MCP's move_item) and retry.
//
// Runs as one transaction: promoting the direct items to a parent (when
// there is one) and then deleting the storage are two separate statements,
// and if DeleteStorage failed after PromoteItemsToParent had already
// committed, those items would be left pointing at a parent that never
// actually got the storage removed underneath it as the caller expected.
//
// The item-clearing step (CountDirectItemsAtStorage for a root storage,
// PromoteItemsToParent for a non-root one) happens inside this transaction,
// but under Postgres's default READ COMMITTED isolation that only
// guarantees each statement its own up-to-date snapshot, not a snapshot
// frozen for the whole transaction — it does NOT close the race a stronger
// isolation level would: a direct item concurrently inserted into this
// storage right after CountDirectItemsAtStorage runs (and commits
// elsewhere) but before this transaction's later DeleteStorage statement
// is still there when DeleteStorage runs, and items.storage_id is ON
// DELETE RESTRICT (migrations/000001_init.up.sql), so DeleteStorage fails
// outright. Verified empirically with two concurrent psql sessions on both
// branches: a storage correctly counted/promoted as having no direct items
// still ends up failing DeleteStorage with a raw 23503
// (foreign-key-violation) once a concurrent insert lands in the gap. No
// data corruption either way: the FK's RESTRICT is what stops it, rolling
// the whole transaction back rather than leaving the concurrently-inserted
// item pointing at a storage that got deleted anyway. Worst case is a
// request that should have cleanly 409'd (root) or actually succeeded
// against the pre-race state (non-root) failing loudly with an uncaught 500
// instead of a retry-worthy error — so this is flagged rather than fixed
// with a stronger isolation level or an explicit row lock; SERIALIZABLE (or
// SELECT ... FOR UPDATE on the storage row) would close it if this ever
// becomes a real problem in practice.
func (h *StoragesHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := parseIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	var status int
	var message string

	err = withTx(r.Context(), h.pool, func(tx pgx.Tx) error {
		q := store.New(tx)

		storage, err := q.GetStorageByID(r.Context(), id)
		if isNoRows(err) {
			status, message = http.StatusNotFound, "storage not found"
			return errHandled
		}
		if err != nil {
			return err
		}

		if storage.ParentID != nil {
			if err := q.PromoteItemsToParent(r.Context(), store.PromoteItemsToParentParams{
				NewStorageID: *storage.ParentID,
				OldStorageID: id,
			}); err != nil {
				return err
			}
		} else {
			count, err := q.CountDirectItemsAtStorage(r.Context(), id)
			if err != nil {
				return err
			}
			if count > 0 {
				status, message = http.StatusConflict,
					"storage holds items directly and has no parent to promote them to — move them to another storage first, then retry"
				return errHandled
			}
		}

		// Deleting a storage that has an *effective* Location — its own, if
		// it's root, or inherited transitively via StorageBreadcrumb's root
		// walk if it's nested several levels deep — must propagate that
		// location onto any direct children it promotes to root, at any
		// nesting depth. Using storage.LocationID directly here (the raw
		// column) was a real bug: a nested storage's own LocationID is
		// always nil (CHECK constraint, migration 000004), so deleting a
		// nested storage silently dropped its promoted children out of
		// their property instead of keeping it — the root-delete case had
		// a test (TestStorageDeletePropagatesLocationToPromotedChildren),
		// the nested case didn't. childIDs must be captured *before*
		// DeleteStorage runs (their parent_id is about to change), but
		// SetLocationForStorages must run *after* it — setting location_id
		// on a storage that still has a non-null parent_id (true until the
		// FK's ON DELETE SET NULL actually fires) trips
		// storages_location_only_on_root itself, turning what should be an
		// internal propagation step into a spurious 500.
		breadcrumbRows, err := q.StorageBreadcrumb(r.Context(), id)
		if err != nil {
			return err
		}
		_, effectiveLocation := breadcrumbAndLocation(breadcrumbRows)

		var promotedChildIDs []int64
		if effectiveLocation != nil {
			promotedChildIDs, err = q.DirectChildStorageIDs(r.Context(), &id)
			if err != nil {
				return err
			}
		}

		// Same transaction as the promote-or-delete-items branch above — if
		// this fails, the items move/delete above rolls back with it instead
		// of being left committed with the storage still sitting there.
		if err := q.DeleteStorage(r.Context(), id); err != nil {
			return err
		}

		if len(promotedChildIDs) > 0 {
			if err := q.SetLocationForStorages(r.Context(), store.SetLocationForStoragesParams{
				LocationID: effectiveLocation.ID,
				Ids:        promotedChildIDs,
			}); err != nil {
				return err
			}
		}
		return nil
	})

	if errors.Is(err, errHandled) {
		writeError(w, status, message)
		return
	}
	if err != nil {
		serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
