package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hoardqr/internal/db"
)

// testPool mirrors internal/api's testPool — connects to DATABASE_URL_TEST
// (falling back to DATABASE_URL) and skips if neither is set. Duplicated
// rather than shared: it's a few lines, and the two packages have no other
// reason to depend on each other.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL_TEST")
	if url == "" {
		url = os.Getenv("DATABASE_URL")
	}
	if url == "" {
		t.Skip("DATABASE_URL(_TEST) not set — skipping test that needs a real Postgres")
	}
	pool, err := db.Connect(context.Background(), url)
	if err != nil {
		t.Fatalf("connecting to test database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// testClient connects a real MCP client to the real tool server over an
// in-memory transport — exercises the actual protocol (schema validation,
// JSON marshaling of typed input/output) without needing a live HTTP server.
func testClient(t *testing.T, pool *pgxpool.Pool) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	s := New(pool)
	c := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	cs, err := c.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// callTool calls a tool and decodes its structured output (via the JSON text
// content ToolHandlerFor populates automatically) into out. Fails the test
// on a protocol error or a tool-level error (IsError) — call cs.CallTool
// directly (as the error-path tests below do) when a tool-level error is
// actually expected.
func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, out any) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): protocol error: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("CallTool(%s): tool error: %s", name, textOf(t, res))
	}
	if out != nil {
		if err := json.Unmarshal([]byte(textOf(t, res)), out); err != nil {
			t.Fatalf("CallTool(%s): decoding result: %v (text: %s)", name, err, textOf(t, res))
		}
	}
}

func textOf(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatalf("result has no content")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("result content is %T, not *mcp.TextContent", res.Content[0])
	}
	return tc.Text
}

// TestMCPToolsEndToEnd drives all six tools in sequence against a real
// Postgres, the same way a chat client actually would: create a root
// location, nest another inside it, add an item, find it three different
// ways, move it, and confirm both the move and its audit_log entry.
func TestMCPToolsEndToEnd(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	cs := testClient(t, pool)
	ctx := context.Background()

	var root, nested addLocationOutput
	callTool(t, cs, "add_location", map[string]any{"name": "MCP Test Balcony"}, &root)
	if root.ID == 0 || root.Path != "MCP Test Balcony" {
		t.Fatalf("unexpected root: %+v", root)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM locations WHERE name LIKE 'MCP Test %'`); err != nil {
			t.Logf("cleanup: deleting test locations: %v", err)
		}
	})

	callTool(t, cs, "add_location", map[string]any{"name": "MCP Test Cabinet", "parent": "mcp test balcony"}, &nested)
	if nested.Path != "MCP Test Balcony > MCP Test Cabinet" {
		t.Fatalf("expected nested path under fuzzy-matched parent, got %q", nested.Path)
	}

	var item addItemOutput
	qty := int32(3)
	callTool(t, cs, "add_item", map[string]any{"name": "MCP Test Widget", "location": "MCP Test Cabinet", "quantity": qty}, &item)
	if item.ID == 0 || item.Location != "MCP Test Cabinet" || item.Code == "" {
		t.Fatalf("unexpected item: %+v", item)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM items WHERE name LIKE 'MCP Test %'`); err != nil {
			t.Logf("cleanup: deleting test items: %v", err)
		}
	})

	// find_items: fuzzy/substring match, must include our item with its breadcrumb.
	var found findItemsOutput
	callTool(t, cs, "find_items", map[string]any{"query": "widget"}, &found)
	if !containsItem(found.Items, item.ID, "MCP Test Balcony > MCP Test Cabinet") {
		t.Fatalf("find_items didn't return the expected item: %+v", found.Items)
	}

	// where_is: same resolution, singular result.
	var where whereIsOutput
	callTool(t, cs, "where_is", map[string]any{"name": "mcp test widget"}, &where)
	if where.Item != "MCP Test Widget" || where.Location != "MCP Test Balcony > MCP Test Cabinet" {
		t.Fatalf("unexpected where_is result: %+v", where)
	}

	// list_contents on the root must recurse into the nested cabinet and find the item.
	var contents listContentsOutput
	callTool(t, cs, "list_contents", map[string]any{"location": "MCP Test Balcony"}, &contents)
	if !containsContentItem(contents.Items, item.ID, 3) {
		t.Fatalf("list_contents on root didn't recursively include the item: %+v", contents.Items)
	}

	// move_item: relocate to the root directly.
	var moved moveItemOutput
	callTool(t, cs, "move_item", map[string]any{"item": "MCP Test Widget", "new_location": "MCP Test Balcony"}, &moved)
	if moved.FromLocation != "MCP Test Balcony > MCP Test Cabinet" || moved.ToLocation != "MCP Test Balcony" {
		t.Fatalf("unexpected move_item result: %+v", moved)
	}

	// The move must actually be reflected...
	var afterMove whereIsOutput
	callTool(t, cs, "where_is", map[string]any{"name": "MCP Test Widget"}, &afterMove)
	if afterMove.Location != "MCP Test Balcony" {
		t.Fatalf("item wasn't actually moved: %+v", afterMove)
	}

	// ...and audit_log must have a matching row (audit_log's first real writer).
	var auditCount int
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE entity_type = 'item' AND entity_id = $1 AND action = 'moved'`,
		item.ID,
	).Scan(&auditCount)
	if err != nil {
		t.Fatalf("querying audit_log: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("expected exactly one audit_log row for the move, got %d", auditCount)
	}
}

// requireNoLeftoverTestRows guards against a prior run's cleanup having
// failed silently: if it had, resolveItem/resolveLocation's first-match
// behavior (or, after the ambiguity fix, a spurious "matches more than one"
// error) would make this run flaky in a way that's hard to diagnose from the
// failure alone.
func requireNoLeftoverTestRows(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var count int
	err := pool.QueryRow(context.Background(), `SELECT count(*) FROM locations WHERE name LIKE 'MCP Test %'`).Scan(&count)
	if err != nil {
		t.Fatalf("checking for leftover test rows: %v", err)
	}
	if count > 0 {
		t.Fatalf("found %d leftover 'MCP Test %%' location(s) from a previous run's failed cleanup — clean up manually before re-running", count)
	}
}

// TestMCPToolsAmbiguousResolution proves that two items (or locations) tied
// for the best name match refuse to resolve rather than silently picking
// one — critical for move_item specifically, since silently moving the
// wrong one of two identically-named items isn't something the caller could
// notice from the response alone.
func TestMCPToolsAmbiguousResolution(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	cs := testClient(t, pool)
	ctx := context.Background()
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM items WHERE name LIKE 'MCP Test %'`); err != nil {
			t.Logf("cleanup: deleting test items: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM locations WHERE name LIKE 'MCP Test %'`); err != nil {
			t.Logf("cleanup: deleting test locations: %v", err)
		}
	})

	var boxA, boxB addLocationOutput
	callTool(t, cs, "add_location", map[string]any{"name": "MCP Test Box A"}, &boxA)
	callTool(t, cs, "add_location", map[string]any{"name": "MCP Test Box B"}, &boxB)

	var itemA, itemB addItemOutput
	callTool(t, cs, "add_item", map[string]any{"name": "MCP Test Duplicate", "location": "MCP Test Box A"}, &itemA)
	callTool(t, cs, "add_item", map[string]any{"name": "MCP Test Duplicate", "location": "MCP Test Box B"}, &itemB)

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "where_is", Arguments: map[string]any{"name": "MCP Test Duplicate"}})
	if err != nil {
		t.Fatalf("CallTool: unexpected protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected where_is to refuse an ambiguous item name, got success: %s", textOf(t, res))
	}

	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "move_item", Arguments: map[string]any{"item": "MCP Test Duplicate", "new_location": "MCP Test Box A"}})
	if err != nil {
		t.Fatalf("CallTool: unexpected protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected move_item to refuse an ambiguous item name rather than silently moving one, got success: %s", textOf(t, res))
	}

	// Both items must be untouched — a rejected move must not partially apply.
	var stillInA, stillInB int
	if err := pool.QueryRow(ctx, `SELECT location_id FROM items WHERE id = $1`, itemA.ID).Scan(&stillInA); err != nil {
		t.Fatalf("querying item A: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT location_id FROM items WHERE id = $1`, itemB.ID).Scan(&stillInB); err != nil {
		t.Fatalf("querying item B: %v", err)
	}
	if int64(stillInA) != boxA.ID || int64(stillInB) != boxB.ID {
		t.Fatalf("ambiguous move_item call must not have moved anything, got itemA.location_id=%d itemB.location_id=%d", stillInA, stillInB)
	}

	// Ambiguous locations (add_item's target, add_location's parent) must
	// refuse the same way — two locations sharing the exact same name both
	// score a guaranteed-tied 1.0 (an exact-name match), unlike two
	// differently-suffixed names, whose trigram similarity scores aren't
	// actually equal (verified empirically: pg_trgm's similarity() isn't
	// symmetric across a trailing single-character difference).
	var dupA, dupB addLocationOutput
	callTool(t, cs, "add_location", map[string]any{"name": "MCP Test Ambiguous Shelf"}, &dupA)
	callTool(t, cs, "add_location", map[string]any{"name": "MCP Test Ambiguous Shelf"}, &dupB)

	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "add_item", Arguments: map[string]any{"name": "MCP Test Orphan", "location": "MCP Test Ambiguous Shelf"}})
	if err != nil {
		t.Fatalf("CallTool: unexpected protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected add_item to refuse an ambiguous location, got success: %s", textOf(t, res))
	}
}

// TestMCPToolsFindItemsByTag proves find_items honors its own description
// ("Search for items by name, tag, or code") for a query that matches only a
// tag name, not any item's own name or code.
func TestMCPToolsFindItemsByTag(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	cs := testClient(t, pool)
	ctx := context.Background()
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM items WHERE name LIKE 'MCP Test %'`); err != nil {
			t.Logf("cleanup: deleting test items: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM locations WHERE name LIKE 'MCP Test %'`); err != nil {
			t.Logf("cleanup: deleting test locations: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM tags WHERE name = 'mcptesttag'`); err != nil {
			t.Logf("cleanup: deleting test tag: %v", err)
		}
	})

	var loc addLocationOutput
	callTool(t, cs, "add_location", map[string]any{"name": "MCP Test Tag Shelf"}, &loc)
	var item addItemOutput
	callTool(t, cs, "add_item", map[string]any{"name": "MCP Test Tagged Thing", "location": "MCP Test Tag Shelf"}, &item)

	var tagID int64
	if err := pool.QueryRow(ctx, `INSERT INTO tags (name) VALUES ('mcptesttag') RETURNING id`).Scan(&tagID); err != nil {
		t.Fatalf("inserting test tag: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO item_tags (item_id, tag_id) VALUES ($1, $2)`, item.ID, tagID); err != nil {
		t.Fatalf("linking test tag: %v", err)
	}

	var found findItemsOutput
	callTool(t, cs, "find_items", map[string]any{"query": "mcptesttag"}, &found)
	if !containsItem(found.Items, item.ID, "MCP Test Tag Shelf") {
		t.Fatalf("find_items didn't surface the item via its tag: %+v", found.Items)
	}
}

// TestMCPToolsNotFoundErrors proves resolution failures surface as tool
// errors (IsError, a readable message) rather than a protocol error or a
// panic — the shape an LLM caller needs to see and self-correct from.
func TestMCPToolsNotFoundErrors(t *testing.T) {
	pool := testPool(t)
	cs := testClient(t, pool)

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "where_is",
		Arguments: map[string]any{"name": "definitely-does-not-exist-anywhere-12345"},
	})
	if err != nil {
		t.Fatalf("CallTool: unexpected protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected a tool-level error for an unresolvable item, got success: %s", textOf(t, res))
	}

	res, err = cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "add_item",
		Arguments: map[string]any{"name": "orphan", "location": "definitely-does-not-exist-anywhere-12345"},
	})
	if err != nil {
		t.Fatalf("CallTool: unexpected protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected a tool-level error for add_item with an unresolvable location, got success: %s", textOf(t, res))
	}
}

func containsItem(items []foundItem, id int64, location string) bool {
	for _, i := range items {
		if i.ID == id && i.Location == location {
			return true
		}
	}
	return false
}

func containsContentItem(items []contentItem, id int64, quantity int32) bool {
	for _, i := range items {
		if i.ID == id && i.Quantity == quantity {
			return true
		}
	}
	return false
}
