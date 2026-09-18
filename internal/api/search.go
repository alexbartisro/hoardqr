package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"hoardqr/internal/store"
)

// SearchHandler backs §5's unified autocomplete and §6/§7's scan resolution.
// Unlike LocationsHandler/ItemsHandler/TagsHandler these three routes don't
// share a path prefix, so they're mounted individually in router.go instead
// of via a Routes(r chi.Router) method.
type SearchHandler struct {
	q *store.Queries
}

func NewSearchHandler(pool *pgxpool.Pool) *SearchHandler {
	return &SearchHandler{q: store.New(pool)}
}

// GET /api/search/suggest?q= (§5) — live autocomplete across items,
// locations, and tags; item hits include their own location_id + breadcrumb
// (no second lookup needed on the frontend).
func (h *SearchHandler) Suggest(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if strings.TrimSpace(query) == "" {
		writeJSON(w, http.StatusOK, []SearchSuggestionDTO{})
		return
	}

	rows, err := h.q.SearchSuggest(r.Context(), query)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	suggestions := make([]SearchSuggestionDTO, len(rows))
	for i, row := range rows {
		s := SearchSuggestionDTO{Kind: row.Kind, ID: row.ID, Name: row.Name, Score: row.Score}
		if row.Kind == "item" {
			locationID, breadcrumb, err := h.itemLocationAndBreadcrumb(r.Context(), row.ID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			s.LocationID = &locationID
			s.Breadcrumb = &breadcrumb
		}
		suggestions[i] = s
	}
	writeJSON(w, http.StatusOK, suggestions)
}

func (h *SearchHandler) itemLocationAndBreadcrumb(ctx context.Context, itemID int64) (int64, string, error) {
	locationID, err := h.q.GetItemLocationID(ctx, itemID)
	if err != nil {
		return 0, "", err
	}
	crumb, err := h.q.LocationBreadcrumb(ctx, locationID)
	if err != nil {
		return 0, "", err
	}
	names := make([]string, len(crumb))
	for i, c := range crumb {
		names[i] = c.Name
	}
	return locationID, strings.Join(names, " > "), nil
}

// GET /api/scan?code= (§6) — resolve a scanned code to item(s), a location,
// or no match.
func (h *SearchHandler) Scan(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if strings.TrimSpace(code) == "" {
		writeError(w, http.StatusBadRequest, "code is required")
		return
	}

	items, err := h.q.FindItemsByNormalizedCode(r.Context(), code)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(items) > 0 {
		writeJSON(w, http.StatusOK, map[string]any{"kind": "item", "items": toItemDTOsFromScanRows(items)})
		return
	}

	location, err := h.q.FindLocationByNormalizedCode(r.Context(), code)
	if isNoRows(err) {
		writeJSON(w, http.StatusOK, map[string]any{"kind": "none"})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"kind": "location", "location": toLocationDTO(location)})
}

// GET /api/resolve-location?code= (§7) — the Location Picker's scan-tab
// equivalent: always resolves to a location_id (an item code resolves to
// *its* location), or the same ambiguous-items picker as Scan when several
// items share the code, or none.
func (h *SearchHandler) ResolveLocation(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if strings.TrimSpace(code) == "" {
		writeError(w, http.StatusBadRequest, "code is required")
		return
	}

	items, err := h.q.FindItemsByNormalizedCode(r.Context(), code)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(items) == 1 {
		writeJSON(w, http.StatusOK, map[string]any{"kind": "location", "location_id": items[0].LocationID})
		return
	}
	if len(items) > 1 {
		writeJSON(w, http.StatusOK, map[string]any{"kind": "items", "items": toItemDTOsFromScanRows(items)})
		return
	}

	location, err := h.q.FindLocationByNormalizedCode(r.Context(), code)
	if isNoRows(err) {
		writeJSON(w, http.StatusOK, map[string]any{"kind": "none"})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"kind": "location", "location_id": location.ID})
}

func toItemDTOsFromScanRows(rows []store.FindItemsByNormalizedCodeRow) []ItemDTO {
	out := make([]ItemDTO, len(rows))
	for i, row := range rows {
		out[i] = toItemDTOFromScanRow(row)
	}
	return out
}
