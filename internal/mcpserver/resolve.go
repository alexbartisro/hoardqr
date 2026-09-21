// Package mcpserver implements the seven MCP tools from architecture plan
// §11 (six) plus list_locations (added alongside add_storage's optional
// location param — see CLAUDE.md's Locations design-decision notes), as the
// `hoardqr mcp` run mode's tool set. Named mcpserver (not mcp) specifically
// to avoid colliding with the imported
// github.com/modelcontextprotocol/go-sdk/mcp package, which every file here
// also imports as mcp.
package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"hoardqr/internal/store"
)

// scoreEpsilon treats SearchSuggest scores within this margin as tied — the
// column is a Postgres `real` (float32), so an exact equality check would
// occasionally miss two rows that are conceptually identical (e.g. two exact
// name matches, both scored 1.0) due to floating-point noise.
const scoreEpsilon = 1e-6

// ambiguousError formats the "can't tell which one" case shared by
// resolveStorage/resolveItem — a name-based tool must refuse to guess when
// two or more candidates are tied for the best match, rather than silently
// acting on whichever one the query happened to return first. Read-only
// tools (where_is, list_contents) become merely imprecise if this is
// skipped; move_item is a write, and moving the wrong one of two
// identically-named items is not recoverable by the caller noticing —
// nothing in the response would indicate a choice was made.
func ambiguousError(kind, query string, matches []string) error {
	return toolErrorf("%q matches more than one %s, be more specific: %s", query, kind, strings.Join(matches, "; "))
}

// resolveStorage finds the best-matching storage for a free-text name or
// code, built on the same SearchSuggest matching logic (§5) find_items also
// uses — the same "type a name or code, let fuzzy/exact matching sort it
// out" principle the web Storage Picker (§7) already uses for this exact
// problem. Uses SearchSuggestByKind, not SearchSuggest — a shared top-10
// across kinds let a higher-scoring item/tag match crowd out the storage
// this is actually looking for (or worse, cut a competing same-score
// storage before the tie could even be detected — see the Obsidian backend
// TODO for the verified failure case). Returns the storage id and its
// actual name (which may differ from the query, e.g. a fuzzy or
// case-insensitive match), so callers can tell the caller what was actually
// resolved rather than echoing back whatever text was passed in. Errors if
// the top score is tied across two or more storages — see ambiguousError.
func resolveStorage(ctx context.Context, q *store.Queries, name string) (id int64, matchedName string, err error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return 0, "", toolErrorf("storage name is required")
	}
	candidates, err := q.SearchSuggestByKind(ctx, store.SearchSuggestByKindParams{Query: trimmed, Kind: "storage"})
	if err != nil {
		return 0, "", err
	}
	if len(candidates) == 0 {
		return 0, "", toolErrorf("no storage matching %q found", trimmed)
	}

	// candidates preserves SearchSuggestByKind's own `ORDER BY score DESC,
	// name`, so candidates[0] is always a top scorer; find the rest tied
	// with it.
	best := candidates[0].Score
	var tied []store.SearchSuggestByKindRow
	for _, c := range candidates {
		if best-c.Score < scoreEpsilon {
			tied = append(tied, c)
		}
	}
	if len(tied) > 1 {
		paths := make([]string, len(tied))
		for i, t := range tied {
			path, err := breadcrumbText(ctx, q, t.ID)
			if err != nil {
				return 0, "", err
			}
			paths[i] = path
		}
		return 0, "", ambiguousError("storage", trimmed, paths)
	}

	return candidates[0].ID, candidates[0].Name, nil
}

// resolveLocation finds a Location by exact, case-insensitive name —
// locations are deliberately not fuzzy-searchable (kept out of SearchSuggest
// on purpose, see CLAUDE.md: no codes, a handful of rows, and adding them
// would crowd the shared top-10 the same way already fixed once for
// storages/items/tags), so there's no tie-detection here the way
// resolveStorage/resolveItem need. An unmatched name enumerates every real
// Location (there are at most a handful) so an LLM caller can self-correct
// in one turn rather than guessing blind a second time.
func resolveLocation(ctx context.Context, q *store.Queries, name string) (id int64, matchedName string, err error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return 0, "", toolErrorf("location name is required")
	}
	loc, err := q.FindLocationByName(ctx, trimmed)
	if err == nil {
		return loc.ID, loc.Name, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, "", err
	}

	locations, listErr := q.ListLocations(ctx)
	if listErr != nil {
		return 0, "", listErr
	}
	if len(locations) == 0 {
		return 0, "", toolErrorf("no location matching %q found — no locations exist yet (use list_locations to check)", trimmed)
	}
	names := make([]string, len(locations))
	for i, l := range locations {
		names[i] = l.Name
	}
	return 0, "", toolErrorf("no location matching %q found — existing locations: %s", trimmed, strings.Join(names, ", "))
}

// resolveItem is resolveStorage's item-side equivalent — also returns the
// matched item's storage_id, since every caller needs it (either to report
// where the item lives, or as the "from" side of a move). Uses
// SearchSuggestByKind for the same crowding reason documented on
// resolveStorage above. Errors if the top score is tied across two or more
// items — see ambiguousError.
func resolveItem(ctx context.Context, q *store.Queries, name string) (id int64, matchedName string, storageID int64, err error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return 0, "", 0, toolErrorf("item name is required")
	}
	rows, err := q.SearchSuggestByKind(ctx, store.SearchSuggestByKindParams{Query: trimmed, Kind: "item"})
	if err != nil {
		return 0, "", 0, err
	}

	var candidates []store.SearchSuggestByKindRow
	for _, row := range rows {
		if row.StorageID == nil {
			// Can't happen given SearchSuggestByKind's LEFT JOIN always
			// supplies storage_id for kind="item" rows, but fail loudly
			// rather than silently if that invariant ever breaks.
			return 0, "", 0, toolErrorf("item %q has no storage_id (data inconsistency)", row.Name)
		}
		candidates = append(candidates, row)
	}
	if len(candidates) == 0 {
		return 0, "", 0, toolErrorf("no item matching %q found", trimmed)
	}

	best := candidates[0].Score
	var tied []store.SearchSuggestByKindRow
	for _, c := range candidates {
		if best-c.Score < scoreEpsilon {
			tied = append(tied, c)
		}
	}
	if len(tied) > 1 {
		descriptions := make([]string, len(tied))
		for i, t := range tied {
			path, err := breadcrumbText(ctx, q, *t.StorageID)
			if err != nil {
				return 0, "", 0, err
			}
			descriptions[i] = fmt.Sprintf("%s (in %s)", t.Name, path)
		}
		return 0, "", 0, ambiguousError("item", trimmed, descriptions)
	}

	top := candidates[0]
	return top.ID, top.Name, *top.StorageID, nil
}

// effectiveLocationID reads the *effective* Location off a StorageBreadcrumb
// result — its own location_id if it's root, or inherited transitively if
// it's nested several levels deep — the same value breadcrumbText's own
// Location-prefix check reads off crumb[0]. Used by delete_storage to decide
// whether a deleted storage's promoted children need that location applied
// to them (internal/api/storages.go's delete handler does the identical
// check via its own breadcrumbAndLocation helper, unexported in that
// package — this is mcpserver's equivalent, not a duplicate of a shared
// utility that could just be imported).
func effectiveLocationID(crumb []store.StorageBreadcrumbRow) *int64 {
	if len(crumb) == 0 {
		return nil
	}
	return crumb[0].LocationID
}

// breadcrumbText joins a storage's root-to-leaf path the same way every
// other breadcrumb in the app does (e.g. internal/api/items.go's
// GetItemByID handler) — "Balcony > Storage Cabinet".
func breadcrumbText(ctx context.Context, q *store.Queries, storageID int64) (string, error) {
	crumb, err := q.StorageBreadcrumb(ctx, storageID)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(crumb)+1)
	// Root-first, so an assigned Location (further out than even the root
	// storage) prepends — same convention as internal/api/search.go's
	// breadcrumbText, which this comes for free alongside (migration
	// 000004 extended the shared StorageBreadcrumb query both call).
	if len(crumb) > 0 && crumb[0].LocationName != nil {
		names = append(names, *crumb[0].LocationName)
	}
	for _, c := range crumb {
		names = append(names, c.Name)
	}
	return strings.Join(names, " > "), nil
}

// resolveStorageStrict resolves a storage for delete_storage specifically —
// either a numeric database id, or an exact (case-insensitive, non-fuzzy)
// name match. Unlike resolveStorage's fuzzy matching (used by every
// non-destructive tool), this never acts on an approximate match: deleting
// the wrong storage isn't something a caller could notice from the result
// the way a bad move_item would be. Storage names aren't unique, so an
// exact-name match can still tie across two or more storages — refused the
// same way resolveStorage refuses a fuzzy tie, just pointing the caller at
// an id instead of "be more specific" (a name alone can't disambiguate two
// identically-named storages the way it can for a fuzzy, scored match).
func resolveStorageStrict(ctx context.Context, q *store.Queries, input string) (id int64, name string, err error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return 0, "", toolErrorf("storage is required")
	}

	// Both interpretations are checked unconditionally, not "numeric string
	// means id, full stop" — a storage can legitimately be *named* a number
	// (a numbered bin, a room number), and checking only the id
	// interpretation would either miss that storage entirely (no id happens
	// to match) or, worse, silently delete a same-numbered but unrelated
	// row if one does. Candidates from both paths are deduped by id: a
	// numeric input that happens to also be some *other* row's exact name
	// is a real ambiguity, refused the same way two name-matches are.
	seen := map[int64]bool{}
	var candIDs []int64
	var candNames []string
	add := func(candID int64, candName string) {
		if !seen[candID] {
			seen[candID] = true
			candIDs = append(candIDs, candID)
			candNames = append(candNames, candName)
		}
	}

	if parsedID, convErr := strconv.ParseInt(trimmed, 10, 64); convErr == nil {
		s, err := q.GetStorageByID(ctx, parsedID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return 0, "", err
		}
		if err == nil {
			add(s.ID, s.Name)
		}
	}
	nameMatches, err := q.FindStoragesByExactName(ctx, trimmed)
	if err != nil {
		return 0, "", err
	}
	for _, m := range nameMatches {
		add(m.ID, m.Name)
	}

	switch len(candIDs) {
	case 0:
		return 0, "", toolErrorf("no storage named %q or with that id found — delete requires an exact name match or an id (try find_items/list_contents to look one up)", trimmed)
	case 1:
		return candIDs[0], candNames[0], nil
	default:
		paths := make([]string, len(candIDs))
		for i, candID := range candIDs {
			path, err := breadcrumbText(ctx, q, candID)
			if err != nil {
				return 0, "", err
			}
			paths[i] = fmt.Sprintf("%s (id %d)", path, candID)
		}
		return 0, "", toolErrorf("%q matches more than one storage — use its id instead: %s", trimmed, strings.Join(paths, "; "))
	}
}

// resolveItemStrict is resolveStorageStrict's item-side equivalent, used by
// delete_item.
func resolveItemStrict(ctx context.Context, q *store.Queries, input string) (id int64, name string, err error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return 0, "", toolErrorf("item is required")
	}

	// Same both-interpretations-checked, deduped-by-id approach as
	// resolveStorageStrict — see its comment for why a numeric input can't
	// just be assumed to mean "id" (an item can legitimately be named a
	// number too).
	seen := map[int64]bool{}
	var candIDs, candStorageIDs []int64
	var candNames []string
	add := func(candID, storageID int64, candName string) {
		if !seen[candID] {
			seen[candID] = true
			candIDs = append(candIDs, candID)
			candStorageIDs = append(candStorageIDs, storageID)
			candNames = append(candNames, candName)
		}
	}

	if parsedID, convErr := strconv.ParseInt(trimmed, 10, 64); convErr == nil {
		row, err := q.GetItemByID(ctx, parsedID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return 0, "", err
		}
		if err == nil {
			add(row.ID, row.StorageID, row.Name)
		}
	}
	nameMatches, err := q.FindItemsByExactName(ctx, trimmed)
	if err != nil {
		return 0, "", err
	}
	for _, m := range nameMatches {
		add(m.ID, m.StorageID, m.Name)
	}

	switch len(candIDs) {
	case 0:
		return 0, "", toolErrorf("no item named %q or with that id found — delete requires an exact name match or an id (try find_items/where_is to look one up)", trimmed)
	case 1:
		return candIDs[0], candNames[0], nil
	default:
		descriptions := make([]string, len(candIDs))
		for i := range candIDs {
			path, err := breadcrumbText(ctx, q, candStorageIDs[i])
			if err != nil {
				return 0, "", err
			}
			descriptions[i] = fmt.Sprintf("%s in %s (id %d)", candNames[i], path, candIDs[i])
		}
		return 0, "", toolErrorf("%q matches more than one item — use its id instead: %s", trimmed, strings.Join(descriptions, "; "))
	}
}
