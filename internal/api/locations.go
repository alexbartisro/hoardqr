package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hoardqr/internal/store"
)

// LocationsHandler backs the flat, non-nested physical-property entity
// added by migration 000004 (House, Garage, Parent's House) — a root
// storage may optionally belong to one. See architecture plan §3 and
// CLAUDE.md's Locations design-decision notes.
type LocationsHandler struct {
	pool *pgxpool.Pool
	q    *store.Queries
}

func NewLocationsHandler(pool *pgxpool.Pool) *LocationsHandler {
	return &LocationsHandler{pool: pool, q: store.New(pool)}
}

func (h *LocationsHandler) Routes(r chi.Router) {
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Get("/{id}", h.get)
	r.Patch("/{id}", h.update)
	r.Delete("/{id}", h.delete)
}

// GET /api/locations
func (h *LocationsHandler) list(w http.ResponseWriter, r *http.Request) {
	locations, err := h.q.ListLocations(r.Context())
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toLocationDTOs(locations))
}

type createLocationRequest struct {
	Name string `json:"name"`
}

// POST /api/locations
func (h *LocationsHandler) create(w http.ResponseWriter, r *http.Request) {
	var req createLocationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	// No TOCTOU pre-check — locations.name's UNIQUE constraint is the
	// authority (same pattern as storages.qr_token's create handler): a
	// SELECT-then-INSERT check would leave a race window between the two.
	location, err := h.q.InsertLocation(r.Context(), store.InsertLocationParams{
		OwnerID:  nil, // no auth yet (Phase 3 step 5) — every row is ownerless until then
		IsShared: true,
		Name:     name,
	})
	if err != nil {
		if pgConflict(err) {
			writeError(w, http.StatusConflict, fmt.Sprintf("a location named %q already exists", name))
			return
		}
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toLocationDTO(location))
}

// GET /api/locations/:id — includes its assigned root storages so the
// manage page shows counts with one call.
func (h *LocationsHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := parseIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	location, err := h.q.GetLocationByID(r.Context(), id)
	if isNoRows(err) {
		notFound(w, "location")
		return
	}
	if err != nil {
		serverError(w, r, err)
		return
	}
	storages, err := h.q.ListStoragesAtLocation(r.Context(), &id)
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"location": toLocationDTO(location),
		"storages": toStorageDTOs(storages),
	})
}

type updateLocationRequest struct {
	Name string `json:"name"`
}

// PATCH /api/locations/:id — name only, a location's one editable field.
func (h *LocationsHandler) update(w http.ResponseWriter, r *http.Request) {
	id, err := parseIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req updateLocationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	location, err := h.q.UpdateLocationName(r.Context(), store.UpdateLocationNameParams{ID: id, Name: name})
	if isNoRows(err) {
		notFound(w, "location")
		return
	}
	if err != nil {
		if pgConflict(err) {
			writeError(w, http.StatusConflict, fmt.Sprintf("a location named %q already exists", name))
			return
		}
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toLocationDTO(location))
}

// DELETE /api/locations/:id?force= — 409 if any storage still references
// it, naming the count; force=true clears that assignment (storages become
// unassigned, not deleted) then deletes the location, both inside one
// transaction — the same "second step failing after the first commits"
// risk StoragesHandler.delete already guards against.
func (h *LocationsHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := parseIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	force := r.URL.Query().Get("force") == "true"

	exists, err := h.q.LocationExists(r.Context(), id)
	if err != nil {
		serverError(w, r, err)
		return
	}
	if !exists {
		notFound(w, "location")
		return
	}

	count, err := h.q.CountStoragesAtLocation(r.Context(), &id)
	if err != nil {
		serverError(w, r, err)
		return
	}
	if count > 0 && !force {
		writeError(w, http.StatusConflict,
			fmt.Sprintf("location has %d storage(s) assigned — retry with force=true", count))
		return
	}

	err = withTx(r.Context(), h.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		if count > 0 {
			if err := q.ClearLocationFromStorages(r.Context(), &id); err != nil {
				return err
			}
		}
		return q.DeleteLocation(r.Context(), id)
	})
	if err != nil {
		serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
