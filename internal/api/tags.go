package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hoardqr/internal/store"
)

// TagsHandler backs GET/POST /api/tags — not in §9's table, but the Add
// Object tag autocomplete (web/src/lib/api.ts's getTags/createTag) needs
// both a full list and a create-if-missing endpoint. See CLAUDE.md's "Mock
// API surface beyond §9".
type TagsHandler struct {
	q *store.Queries
}

func NewTagsHandler(pool *pgxpool.Pool) *TagsHandler {
	return &TagsHandler{q: store.New(pool)}
}

func (h *TagsHandler) Routes(r chi.Router) {
	r.Get("/", h.list)
	r.Post("/", h.create)
}

func (h *TagsHandler) list(w http.ResponseWriter, r *http.Request) {
	tags, err := h.q.ListTags(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toTagDTOs(tags))
}

type createTagRequest struct {
	Name string `json:"name"`
}

// Idempotent by case-insensitive name — a duplicate create (e.g. the same
// new tag typed on two Add Object drafts) returns the tag already created
// instead of erroring or inserting a second row. Matches the mock exactly.
func (h *TagsHandler) create(w http.ResponseWriter, r *http.Request) {
	var req createTagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	trimmed := strings.TrimSpace(req.Name)
	if trimmed == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	existing, err := h.q.GetTagByNameCI(r.Context(), trimmed)
	if err == nil {
		writeJSON(w, http.StatusOK, toTagDTO(existing))
		return
	}
	if !isNoRows(err) {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	tag, err := h.q.InsertTag(r.Context(), trimmed)
	if pgConflict(err) {
		// Lost a race with another request creating the same tag between our
		// case-insensitive lookup and this insert — fetch and return it
		// rather than erroring on what the caller sees as a normal outcome.
		existing, err := h.q.GetTagByNameCI(r.Context(), trimmed)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, toTagDTO(existing))
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, toTagDTO(tag))
}
