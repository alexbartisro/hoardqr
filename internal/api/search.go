package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"hoardqr/internal/store"
)

// SearchHandler backs §5's unified autocomplete and §6/§7's scan resolution.
// Unlike StoragesHandler/ItemsHandler/TagsHandler these three routes don't
// share a path prefix, so they're mounted individually in router.go instead
// of via a Routes(r chi.Router) method.
type SearchHandler struct {
	q *store.Queries
}

func NewSearchHandler(pool *pgxpool.Pool) *SearchHandler {
	return &SearchHandler{q: store.New(pool)}
}

// GET /api/search/suggest?q= (§5) — live autocomplete across items,
// storages, and tags; item hits include their own storage_id + breadcrumb
// (no second lookup needed on the frontend). storage_id comes straight off
// the SearchSuggest row (a LEFT JOIN within that one query, not a separate
// per-hit round trip) — see the query's own comment for why that matters.
func (h *SearchHandler) Suggest(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeJSON(w, http.StatusOK, []SearchSuggestionDTO{})
		return
	}

	rows, err := h.q.SearchSuggest(r.Context(), query)
	if err != nil {
		serverError(w, r, err)
		return
	}

	suggestions := make([]SearchSuggestionDTO, len(rows))
	for i, row := range rows {
		s := SearchSuggestionDTO{Kind: row.Kind, ID: row.ID, Name: row.Name, Score: row.Score}
		if row.Kind == "item" && row.StorageID != nil {
			breadcrumb, err := h.breadcrumbText(r.Context(), *row.StorageID)
			if err != nil {
				serverError(w, r, err)
				return
			}
			s.StorageID = row.StorageID
			s.Breadcrumb = &breadcrumb
		}
		suggestions[i] = s
	}
	writeJSON(w, http.StatusOK, suggestions)
}

func (h *SearchHandler) breadcrumbText(ctx context.Context, storageID int64) (string, error) {
	crumb, err := h.q.StorageBreadcrumb(ctx, storageID)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(crumb)+1)
	// Root-first, so a location (further out than even the root storage)
	// prepends rather than appends — the opposite end from listRecent's
	// deepest-first breadcrumb, which appends it instead.
	if len(crumb) > 0 && crumb[0].LocationName != nil {
		names = append(names, *crumb[0].LocationName)
	}
	for _, c := range crumb {
		names = append(names, c.Name)
	}
	return strings.Join(names, " > "), nil
}

// GET /api/scan?code= (§6) — resolve a scanned code to item(s), a storage,
// or no match.
func (h *SearchHandler) Scan(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		writeError(w, http.StatusBadRequest, "code is required")
		return
	}

	items, err := h.q.FindItemsByNormalizedCode(r.Context(), code)
	if err != nil {
		serverError(w, r, err)
		return
	}
	if len(items) > 0 {
		writeJSON(w, http.StatusOK, map[string]any{"kind": "item", "items": toItemDTOsFromScanRows(items)})
		return
	}

	storage, err := h.q.FindStorageByNormalizedCode(r.Context(), code)
	if isNoRows(err) {
		writeJSON(w, http.StatusOK, map[string]any{"kind": "none"})
		return
	}
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"kind": "storage", "storage": toStorageDTO(storage)})
}

// GET /api/resolve-storage?code= (§7) — the Storage Picker's scan-tab
// equivalent: always resolves to a storage_id (an item code resolves to
// *its* storage), or the same ambiguous-items picker as Scan when several
// items share the code, or none.
func (h *SearchHandler) ResolveStorage(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		writeError(w, http.StatusBadRequest, "code is required")
		return
	}

	items, err := h.q.FindItemsByNormalizedCode(r.Context(), code)
	if err != nil {
		serverError(w, r, err)
		return
	}
	if len(items) == 1 {
		writeJSON(w, http.StatusOK, map[string]any{"kind": "storage", "storage_id": items[0].StorageID})
		return
	}
	if len(items) > 1 {
		writeJSON(w, http.StatusOK, map[string]any{"kind": "items", "items": toItemDTOsFromScanRows(items)})
		return
	}

	storage, err := h.q.FindStorageByNormalizedCode(r.Context(), code)
	if isNoRows(err) {
		writeJSON(w, http.StatusOK, map[string]any{"kind": "none"})
		return
	}
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"kind": "storage", "storage_id": storage.ID})
}

func toItemDTOsFromScanRows(rows []store.FindItemsByNormalizedCodeRow) []ItemDTO {
	out := make([]ItemDTO, len(rows))
	for i, row := range rows {
		out[i] = toItemDTOFromScanRow(row)
	}
	return out
}
