// Package mcpserver implements the six MCP tools from architecture plan
// §11, as the `hoardqr mcp` run mode's tool set. Named mcpserver (not mcp)
// specifically to avoid colliding with the imported
// github.com/modelcontextprotocol/go-sdk/mcp package, which every file here
// also imports as mcp.
package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"hoardqr/internal/store"
)

// scoreEpsilon treats SearchSuggest scores within this margin as tied — the
// column is a Postgres `real` (float32), so an exact equality check would
// occasionally miss two rows that are conceptually identical (e.g. two exact
// name matches, both scored 1.0) due to floating-point noise.
const scoreEpsilon = 1e-6

// ambiguousError formats the "can't tell which one" case shared by
// resolveLocation/resolveItem — a name-based tool must refuse to guess when
// two or more candidates are tied for the best match, rather than silently
// acting on whichever one the query happened to return first. Read-only
// tools (where_is, list_contents) become merely imprecise if this is
// skipped; move_item is a write, and moving the wrong one of two
// identically-named items is not recoverable by the caller noticing —
// nothing in the response would indicate a choice was made.
func ambiguousError(kind, query string, matches []string) error {
	return fmt.Errorf("%q matches more than one %s, be more specific: %s", query, kind, strings.Join(matches, "; "))
}

// resolveLocation finds the best-matching location for a free-text name or
// code, built on the same SearchSuggest query (§5) find_items also uses —
// the same "type a name or code, let fuzzy/exact matching sort it out"
// principle the web Location Picker (§7) already uses for this exact
// problem. Returns the location id and its actual name (which may differ
// from the query, e.g. a fuzzy or case-insensitive match), so callers can
// tell the caller what was actually resolved rather than echoing back
// whatever text was passed in. Errors if the top score is tied across two or
// more locations — see ambiguousError.
func resolveLocation(ctx context.Context, q *store.Queries, name string) (id int64, matchedName string, err error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return 0, "", fmt.Errorf("location name is required")
	}
	rows, err := q.SearchSuggest(ctx, trimmed)
	if err != nil {
		return 0, "", err
	}

	var candidates []store.SearchSuggestRow
	for _, row := range rows {
		if row.Kind == "location" {
			candidates = append(candidates, row)
		}
	}
	if len(candidates) == 0 {
		return 0, "", fmt.Errorf("no location matching %q found", trimmed)
	}

	// candidates preserves SearchSuggest's own `ORDER BY score DESC, name`,
	// so candidates[0] is always a top scorer; find the rest tied with it.
	best := candidates[0].Score
	var tied []store.SearchSuggestRow
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
		return 0, "", ambiguousError("location", trimmed, paths)
	}

	return candidates[0].ID, candidates[0].Name, nil
}

// resolveItem is resolveLocation's item-side equivalent — also returns the
// matched item's location_id, since every caller needs it (either to report
// where the item lives, or as the "from" side of a move). Errors if the top
// score is tied across two or more items — see ambiguousError.
func resolveItem(ctx context.Context, q *store.Queries, name string) (id int64, matchedName string, locationID int64, err error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return 0, "", 0, fmt.Errorf("item name is required")
	}
	rows, err := q.SearchSuggest(ctx, trimmed)
	if err != nil {
		return 0, "", 0, err
	}

	var candidates []store.SearchSuggestRow
	for _, row := range rows {
		if row.Kind == "item" {
			if row.LocationID == nil {
				// Can't happen given SearchSuggest's LEFT JOIN always supplies
				// location_id for kind="item" rows, but fail loudly rather than
				// silently if that invariant ever breaks.
				return 0, "", 0, fmt.Errorf("item %q has no location_id (data inconsistency)", row.Name)
			}
			candidates = append(candidates, row)
		}
	}
	if len(candidates) == 0 {
		return 0, "", 0, fmt.Errorf("no item matching %q found", trimmed)
	}

	best := candidates[0].Score
	var tied []store.SearchSuggestRow
	for _, c := range candidates {
		if best-c.Score < scoreEpsilon {
			tied = append(tied, c)
		}
	}
	if len(tied) > 1 {
		descriptions := make([]string, len(tied))
		for i, t := range tied {
			path, err := breadcrumbText(ctx, q, *t.LocationID)
			if err != nil {
				return 0, "", 0, err
			}
			descriptions[i] = fmt.Sprintf("%s (in %s)", t.Name, path)
		}
		return 0, "", 0, ambiguousError("item", trimmed, descriptions)
	}

	top := candidates[0]
	return top.ID, top.Name, *top.LocationID, nil
}

// breadcrumbText joins a location's root-to-leaf path the same way every
// other breadcrumb in the app does (e.g. internal/api/items.go's
// GetItemByID handler) — "Balcony > Storage Cabinet".
func breadcrumbText(ctx context.Context, q *store.Queries, locationID int64) (string, error) {
	crumb, err := q.LocationBreadcrumb(ctx, locationID)
	if err != nil {
		return "", err
	}
	names := make([]string, len(crumb))
	for i, c := range crumb {
		names[i] = c.Name
	}
	return strings.Join(names, " > "), nil
}
