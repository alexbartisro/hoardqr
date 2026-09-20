package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hoardqr/internal/codegen"
	"hoardqr/internal/db"
	"hoardqr/internal/store"
)

// testPool mirrors internal/api's testPool — connects to DATABASE_URL_TEST
// and skips if it's unset, deliberately NOT falling back to DATABASE_URL (a
// developer's shell can have that set for an unrelated reason, e.g. running
// the bare binary locally, and a fallback here would silently point this
// package's tests — which insert and delete real rows — at it). Duplicated
// rather than shared: it's a few lines, and the two packages have no other
// reason to depend on each other.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL_TEST")
	if url == "" {
		t.Skip("DATABASE_URL_TEST not set — skipping test that needs a real Postgres")
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
// storage, nest another inside it, add an item, find it three different
// ways, move it, and confirm both the move and its audit_log entry.
func TestMCPToolsEndToEnd(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	cs := testClient(t, pool)
	ctx := context.Background()

	var root, nested addStorageOutput
	callTool(t, cs, "add_storage", map[string]any{"name": "MCP Test Balcony"}, &root)
	if root.ID == 0 || root.Path != "MCP Test Balcony" {
		t.Fatalf("unexpected root: %+v", root)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM storages WHERE name LIKE 'MCP Test %'`); err != nil {
			t.Logf("cleanup: deleting test storages: %v", err)
		}
	})

	callTool(t, cs, "add_storage", map[string]any{"name": "MCP Test Cabinet", "parent": "mcp test balcony"}, &nested)
	if nested.Path != "MCP Test Balcony > MCP Test Cabinet" {
		t.Fatalf("expected nested path under fuzzy-matched parent, got %q", nested.Path)
	}

	var item addItemOutput
	qty := int32(3)
	callTool(t, cs, "add_item", map[string]any{"name": "MCP Test Widget", "storage": "MCP Test Cabinet", "quantity": qty}, &item)
	if item.ID == 0 || item.Storage != "MCP Test Cabinet" || item.Code == "" {
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
	if where.Item != "MCP Test Widget" || where.Storage != "MCP Test Balcony > MCP Test Cabinet" {
		t.Fatalf("unexpected where_is result: %+v", where)
	}

	// list_contents on the root must recurse into the nested cabinet and find the item.
	var contents listContentsOutput
	callTool(t, cs, "list_contents", map[string]any{"storage": "MCP Test Balcony"}, &contents)
	if !containsContentItem(contents.Items, item.ID, 3) {
		t.Fatalf("list_contents on root didn't recursively include the item: %+v", contents.Items)
	}

	// move_item: relocate to the root directly.
	var moved moveItemOutput
	callTool(t, cs, "move_item", map[string]any{"item": "MCP Test Widget", "new_storage": "MCP Test Balcony"}, &moved)
	if moved.FromStorage != "MCP Test Balcony > MCP Test Cabinet" || moved.ToStorage != "MCP Test Balcony" {
		t.Fatalf("unexpected move_item result: %+v", moved)
	}

	// The move must actually be reflected...
	var afterMove whereIsOutput
	callTool(t, cs, "where_is", map[string]any{"name": "MCP Test Widget"}, &afterMove)
	if afterMove.Storage != "MCP Test Balcony" {
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
// failed silently: if it had, resolveItem/resolveStorage's first-match
// behavior (or, after the ambiguity fix, a spurious "matches more than one"
// error) would make this run flaky in a way that's hard to diagnose from the
// failure alone. Checks both this file's two test-data naming conventions:
// 'MCP Test %' (most tests) and 'zzz%' (the SearchSuggestByKind crowding
// tests below, which need names starting with their query token for
// prefix-tier scoring, so they can't also carry the 'MCP Test ' prefix) — a
// handful of leftover 'zzzcord'/'zzzreel' storages would silently become
// live decoys for any later test querying a similarly short token.
func requireNoLeftoverTestRows(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var count int
	// The third clause catches TestMCPToolsRejectBlankNames's own failure
	// case: a run where add_storage's blank-name check regresses creates a
	// blank/whitespace-named storage, which matches neither 'MCP Test %'
	// nor 'zzz%' — invisible to this guard otherwise, and it's already bitten
	// once (caught manually while verifying that fix, not by this check).
	err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM storages WHERE name LIKE 'MCP Test %' OR name LIKE 'zzz%' OR trim(name) = ''`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("checking for leftover test rows: %v", err)
	}
	if count > 0 {
		t.Fatalf("found %d leftover test storage(s) from a previous run's failed cleanup — clean up manually before re-running", count)
	}

	// Locations added alongside Milestone 3's list_locations/add_storage
	// location param — every location test registers its own t.Cleanup, but
	// this guard exists precisely for the case where one didn't run (an
	// earlier t.Fatalf), so it needs to sweep locations too, not just
	// storages.
	var locationCount int
	err = pool.QueryRow(context.Background(),
		`SELECT count(*) FROM locations WHERE name LIKE 'MCP Test %'`,
	).Scan(&locationCount)
	if err != nil {
		t.Fatalf("checking for leftover test locations: %v", err)
	}
	if locationCount > 0 {
		t.Fatalf("found %d leftover test location(s) from a previous run's failed cleanup — clean up manually before re-running", locationCount)
	}

	// Items and tags, added for the full-CRUD (edit/delete/move/tag) tools —
	// same reasoning as the locations sweep above: this guard exists for the
	// case where a test's own t.Cleanup didn't run.
	var itemCount int
	err = pool.QueryRow(context.Background(),
		`SELECT count(*) FROM items WHERE name LIKE 'MCP Test %' OR trim(name) = ''`,
	).Scan(&itemCount)
	if err != nil {
		t.Fatalf("checking for leftover test items: %v", err)
	}
	if itemCount > 0 {
		t.Fatalf("found %d leftover test item(s) from a previous run's failed cleanup — clean up manually before re-running", itemCount)
	}

	var tagCount int
	err = pool.QueryRow(context.Background(),
		`SELECT count(*) FROM tags WHERE name LIKE 'MCP Test %'`,
	).Scan(&tagCount)
	if err != nil {
		t.Fatalf("checking for leftover test tags: %v", err)
	}
	if tagCount > 0 {
		t.Fatalf("found %d leftover test tag(s) from a previous run's failed cleanup — clean up manually before re-running", tagCount)
	}
}

// TestMCPToolsAmbiguousResolution proves that two items (or storages) tied
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
		if _, err := pool.Exec(ctx, `DELETE FROM storages WHERE name LIKE 'MCP Test %'`); err != nil {
			t.Logf("cleanup: deleting test storages: %v", err)
		}
	})

	var boxA, boxB addStorageOutput
	callTool(t, cs, "add_storage", map[string]any{"name": "MCP Test Box A"}, &boxA)
	callTool(t, cs, "add_storage", map[string]any{"name": "MCP Test Box B"}, &boxB)

	var itemA, itemB addItemOutput
	callTool(t, cs, "add_item", map[string]any{"name": "MCP Test Duplicate", "storage": "MCP Test Box A"}, &itemA)
	callTool(t, cs, "add_item", map[string]any{"name": "MCP Test Duplicate", "storage": "MCP Test Box B"}, &itemB)

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "where_is", Arguments: map[string]any{"name": "MCP Test Duplicate"}})
	if err != nil {
		t.Fatalf("CallTool: unexpected protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected where_is to refuse an ambiguous item name, got success: %s", textOf(t, res))
	}

	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "move_item", Arguments: map[string]any{"item": "MCP Test Duplicate", "new_storage": "MCP Test Box A"}})
	if err != nil {
		t.Fatalf("CallTool: unexpected protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected move_item to refuse an ambiguous item name rather than silently moving one, got success: %s", textOf(t, res))
	}

	// Both items must be untouched — a rejected move must not partially apply.
	var stillInA, stillInB int
	if err := pool.QueryRow(ctx, `SELECT storage_id FROM items WHERE id = $1`, itemA.ID).Scan(&stillInA); err != nil {
		t.Fatalf("querying item A: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT storage_id FROM items WHERE id = $1`, itemB.ID).Scan(&stillInB); err != nil {
		t.Fatalf("querying item B: %v", err)
	}
	if int64(stillInA) != boxA.ID || int64(stillInB) != boxB.ID {
		t.Fatalf("ambiguous move_item call must not have moved anything, got itemA.storage_id=%d itemB.storage_id=%d", stillInA, stillInB)
	}

	// Ambiguous storages (add_item's target, add_storage's parent) must
	// refuse the same way — two storages sharing the exact same name both
	// score a guaranteed-tied 1.0 (an exact-name match), unlike two
	// differently-suffixed names, whose trigram similarity scores aren't
	// actually equal (verified empirically: pg_trgm's similarity() isn't
	// symmetric across a trailing single-character difference).
	var dupA, dupB addStorageOutput
	callTool(t, cs, "add_storage", map[string]any{"name": "MCP Test Ambiguous Shelf"}, &dupA)
	callTool(t, cs, "add_storage", map[string]any{"name": "MCP Test Ambiguous Shelf"}, &dupB)

	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "add_item", Arguments: map[string]any{"name": "MCP Test Orphan", "storage": "MCP Test Ambiguous Shelf"}})
	if err != nil {
		t.Fatalf("CallTool: unexpected protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected add_item to refuse an ambiguous storage, got success: %s", textOf(t, res))
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
		if _, err := pool.Exec(ctx, `DELETE FROM storages WHERE name LIKE 'MCP Test %'`); err != nil {
			t.Logf("cleanup: deleting test storages: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM tags WHERE name = 'mcptesttag'`); err != nil {
			t.Logf("cleanup: deleting test tag: %v", err)
		}
	})

	var loc addStorageOutput
	callTool(t, cs, "add_storage", map[string]any{"name": "MCP Test Tag Shelf"}, &loc)
	var item addItemOutput
	callTool(t, cs, "add_item", map[string]any{"name": "MCP Test Tagged Thing", "storage": "MCP Test Tag Shelf"}, &item)

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

// TestMCPToolsFindItemsTrimsQuery proves find_items trims its query the same
// way the REST search handler and resolveStorage/resolveItem already do —
// a leading/trailing space (e.g. an LLM relaying a pasted code verbatim)
// must not break the exact-code match branch, and an empty/whitespace-only
// query must return no results rather than erroring.
func TestMCPToolsFindItemsTrimsQuery(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	cs := testClient(t, pool)
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM items WHERE name LIKE 'MCP Test %'`); err != nil {
			t.Logf("cleanup: deleting test items: %v", err)
		}
		if _, err := pool.Exec(context.Background(), `DELETE FROM storages WHERE name LIKE 'MCP Test %'`); err != nil {
			t.Logf("cleanup: deleting test storages: %v", err)
		}
	})

	var loc addStorageOutput
	callTool(t, cs, "add_storage", map[string]any{"name": "MCP Test Trim Shelf"}, &loc)
	var item addItemOutput
	callTool(t, cs, "add_item", map[string]any{"name": "MCP Test Trim Widget", "storage": "MCP Test Trim Shelf"}, &item)

	var found findItemsOutput
	callTool(t, cs, "find_items", map[string]any{"query": "  " + item.Code + "  "}, &found)
	if !containsItem(found.Items, item.ID, "MCP Test Trim Shelf") {
		t.Fatalf("find_items didn't match an exact code padded with whitespace: %+v", found.Items)
	}

	callTool(t, cs, "find_items", map[string]any{"query": "   "}, &found)
	if len(found.Items) != 0 {
		t.Fatalf("expected a whitespace-only query to return no results, got %+v", found.Items)
	}
}

// TestMCPToolsSearchByKindAvoidsFalseNotFound reproduces the false-negative
// half of the Obsidian backend TODO's SearchSuggest LIMIT-10 finding:
// resolveItem/resolveStorage used to filter a single SearchSuggest call's
// shared top-10 (across all kinds) down to the kind they wanted, so enough
// higher-scoring matches of the WRONG kind could crowd a real match of the
// RIGHT kind out of the top 10 entirely. 11 storages that prefix-match the
// query (score 0.8) outnumber the 10-row budget on their own, which used to
// bury an item that only substring-matches (score 0.5) — where_is would
// report "not found" despite the item genuinely existing.
func TestMCPToolsSearchByKindAvoidsFalseNotFound(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	ctx := context.Background()
	q := store.New(pool)
	cs := testClient(t, pool)

	var storageIDs []int64
	for i := 0; i < 11; i++ {
		loc, err := q.InsertStorage(ctx, store.InsertStorageParams{
			Name: fmt.Sprintf("zzzcord decoy shelf %02d", i), QrToken: codegen.PlainTextCode(), IsShared: true,
		})
		if err != nil {
			t.Fatalf("InsertStorage decoy %d: %v", i, err)
		}
		storageIDs = append(storageIDs, loc.ID)
	}
	decoyStorage, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "zzzcord item's actual home", QrToken: codegen.PlainTextCode(), IsShared: true,
	})
	if err != nil {
		t.Fatalf("InsertStorage for item: %v", err)
	}
	item, err := q.InsertItem(ctx, store.InsertItemParams{
		StorageID: decoyStorage.ID, Name: "Extension zzzcord Heavy Duty", QrToken: codegen.PlainTextCode(),
		IsShared: true, Quantity: 1, CustomFields: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("InsertItem: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM items WHERE id = $1`, item.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = ANY($1)`, append(storageIDs, decoyStorage.ID))
	})

	var where whereIsOutput
	callTool(t, cs, "where_is", map[string]any{"name": "zzzcord"}, &where)
	if where.Item != item.Name {
		t.Fatalf("expected where_is to find %q despite 11 higher-scoring storages, got %+v", item.Name, where)
	}
}

// TestMCPToolsSearchByKindPreservesAmbiguityAcrossCrowding reproduces the
// more serious half of the same finding: the ambiguity-refusal guard
// (resolveItem/resolveStorage) only compares candidates that actually made
// it into the SearchSuggest response. With a shared top-10 across kinds, 9
// storages tied at score 1.0 fill the whole budget except one slot, so of
// two equally-scored competing items only one survives to be compared — the
// guard sees a single "candidate" and moves it without ever knowing a tie
// existed. move_item on an ambiguous name must still refuse once resolution
// is restricted to the relevant kind first.
func TestMCPToolsSearchByKindPreservesAmbiguityAcrossCrowding(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	ctx := context.Background()
	q := store.New(pool)
	cs := testClient(t, pool)

	var decoyStorageIDs []int64
	for i := 0; i < 9; i++ {
		loc, err := q.InsertStorage(ctx, store.InsertStorageParams{
			Name: "zzzreel", QrToken: codegen.PlainTextCode(), IsShared: true,
		})
		if err != nil {
			t.Fatalf("InsertStorage decoy %d: %v", i, err)
		}
		decoyStorageIDs = append(decoyStorageIDs, loc.ID)
	}
	itemHome, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "zzzreel items' home", QrToken: codegen.PlainTextCode(), IsShared: true,
	})
	if err != nil {
		t.Fatalf("InsertStorage for items: %v", err)
	}
	moveTarget, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "zzzreel move target", QrToken: codegen.PlainTextCode(), IsShared: true,
	})
	if err != nil {
		t.Fatalf("InsertStorage move target: %v", err)
	}
	itemBlue, err := q.InsertItem(ctx, store.InsertItemParams{
		StorageID: itemHome.ID, Name: "zzzreel Blue", QrToken: codegen.PlainTextCode(),
		IsShared: true, Quantity: 1, CustomFields: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("InsertItem blue: %v", err)
	}
	itemGreen, err := q.InsertItem(ctx, store.InsertItemParams{
		StorageID: itemHome.ID, Name: "zzzreel Green", QrToken: codegen.PlainTextCode(),
		IsShared: true, Quantity: 1, CustomFields: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("InsertItem green: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM items WHERE id = ANY($1)`, []int64{itemBlue.ID, itemGreen.ID})
		_, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = ANY($1)`, append(decoyStorageIDs, itemHome.ID, moveTarget.ID))
	})

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "move_item",
		Arguments: map[string]any{"item": "zzzreel", "new_storage": "zzzreel move target"},
	})
	if err != nil {
		t.Fatalf("CallTool: unexpected protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected move_item to refuse an ambiguous item name crowded by 9 tied storages, got success: %s", textOf(t, res))
	}

	// Neither item should have moved — a rejected move must not partially apply.
	var stillHomeBlue, stillHomeGreen int64
	if err := pool.QueryRow(ctx, `SELECT storage_id FROM items WHERE id = $1`, itemBlue.ID).Scan(&stillHomeBlue); err != nil {
		t.Fatalf("querying item blue: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT storage_id FROM items WHERE id = $1`, itemGreen.ID).Scan(&stillHomeGreen); err != nil {
		t.Fatalf("querying item green: %v", err)
	}
	if stillHomeBlue != itemHome.ID || stillHomeGreen != itemHome.ID {
		t.Fatalf("ambiguous move_item call must not have moved anything, got blue.storage_id=%d green.storage_id=%d", stillHomeBlue, stillHomeGreen)
	}
}

// TestMCPToolsRejectBlankNames proves add_item/add_storage reject a
// blank/whitespace-only name the same way the REST layer's create handlers
// already do (internal/api/items.go, internal/api/storages.go) — an MCP
// caller shouldn't be able to create a row REST itself would refuse.
func TestMCPToolsRejectBlankNames(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	cs := testClient(t, pool)
	ctx := context.Background()
	// Defense in depth beyond requireNoLeftoverTestRows: if either rejection
	// below ever regresses, the resulting row is blank-named and matches
	// neither this file's 'MCP Test %' nor 'zzz%' conventions — sweep it
	// unconditionally rather than relying solely on the next run's guard to
	// notice (a real gap caught once already while developing this fix).
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM items WHERE trim(name) = ''`); err != nil {
			t.Logf("cleanup: deleting blank-named test items: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM storages WHERE trim(name) = ''`); err != nil {
			t.Logf("cleanup: deleting blank-named test storages: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM storages WHERE name LIKE 'MCP Test %'`); err != nil {
			t.Logf("cleanup: deleting test storages: %v", err)
		}
	})

	var beforeItems, beforeStorages int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM items`).Scan(&beforeItems); err != nil {
		t.Fatalf("counting items: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM storages`).Scan(&beforeStorages); err != nil {
		t.Fatalf("counting storages: %v", err)
	}

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "add_storage",
		Arguments: map[string]any{"name": "   "},
	})
	if err != nil {
		t.Fatalf("CallTool: unexpected protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected add_storage to reject a blank name, got success: %s", textOf(t, res))
	}
	var afterFirstStorages int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM storages`).Scan(&afterFirstStorages); err != nil {
		t.Fatalf("counting storages: %v", err)
	}
	if afterFirstStorages != beforeStorages {
		t.Fatalf("expected the rejected add_storage call to create nothing, storage count went from %d to %d", beforeStorages, afterFirstStorages)
	}

	// add_item needs a resolvable storage to even reach its own name check —
	// give it a real one so a "no storage matching" error can't be mistaken
	// for the name check actually firing.
	var loc addStorageOutput
	callTool(t, cs, "add_storage", map[string]any{"name": "MCP Test Blank Name Shelf"}, &loc)

	res, err = cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "add_item",
		Arguments: map[string]any{"name": "  ", "storage": "MCP Test Blank Name Shelf"},
	})
	if err != nil {
		t.Fatalf("CallTool: unexpected protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected add_item to reject a blank name, got success: %s", textOf(t, res))
	}

	var afterItems int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM items`).Scan(&afterItems); err != nil {
		t.Fatalf("counting items: %v", err)
	}
	if afterItems != beforeItems {
		t.Fatalf("expected the rejected add_item call to create nothing, item count went from %d to %d", beforeItems, afterItems)
	}
}

// TestUpdateItemStorageReportsZeroRowsForDeletedItem proves the primitive
// move_item's TOCTOU fix depends on: UpdateItemStorage (:execrows, not
// :exec) correctly reports 0 rows affected against an item id that no
// longer exists, rather than succeeding silently. This is exactly the
// situation a concurrent delete between moveItemHandler's resolveItem call
// (on the pool, before any transaction starts) and its UpdateItemStorage
// call (inside the transaction) would produce.
//
// This test doesn't drive the race through the real moveItemHandler/MCP
// tool call end-to-end: resolveItem re-resolves the item by name at call
// time, so a single-threaded test can't observe "found during resolution,
// gone by the time the transaction runs" without either genuine concurrency
// (racy, non-deterministic — Postgres row-locks would make timing hard to
// control precisely) or restructuring the handler purely for testability.
// Verified the handler's logic by hand instead: moveItemHandler treats
// rows == 0 as a hard error inside the transaction, so InsertAuditLog never
// runs and withTx rolls back — the same "traced by hand, confirm under real
// concurrent use" approach already used elsewhere in this codebase for a
// similarly timing-dependent path (see CLAUDE.md's /scan duplicate-picker
// note).
func TestUpdateItemStorageReportsZeroRowsForDeletedItem(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)

	storage, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "toctou test shelf", QrToken: codegen.PlainTextCode(), IsShared: true,
	})
	if err != nil {
		t.Fatalf("InsertStorage: %v", err)
	}
	otherStorage, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "toctou test other shelf", QrToken: codegen.PlainTextCode(), IsShared: true,
	})
	if err != nil {
		t.Fatalf("InsertStorage other: %v", err)
	}
	item, err := q.InsertItem(ctx, store.InsertItemParams{
		StorageID: storage.ID, Name: "toctou test item", QrToken: codegen.PlainTextCode(),
		IsShared: true, Quantity: 1, CustomFields: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("InsertItem: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM items WHERE id = $1`, item.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = ANY($1)`, []int64{storage.ID, otherStorage.ID})
	})

	// Simulates "deleted concurrently, between resolution and the move's
	// transaction" — sequential here since a single-threaded test can't
	// race it for real, but the effect on UpdateItemStorage is identical:
	// the row is gone by the time it runs.
	if _, err := pool.Exec(ctx, `DELETE FROM items WHERE id = $1`, item.ID); err != nil {
		t.Fatalf("deleting item to simulate the race: %v", err)
	}

	rows, err := q.UpdateItemStorage(ctx, store.UpdateItemStorageParams{ID: item.ID, StorageID: otherStorage.ID})
	if err != nil {
		t.Fatalf("UpdateItemStorage: %v", err)
	}
	if rows != 0 {
		t.Fatalf("expected 0 rows affected for a deleted item, got %d", rows)
	}
}

// TestMoveItemTransactionAbortsAndSkipsAuditLogForDeletedItem tests the
// actual invariant that matters from moveItemHandler's rows == 0 branch —
// no audit_log row for a move that didn't happen — by replicating the
// transaction closure's body directly against a deleted item's id.
// moveItemHandler itself can't be driven into this branch through the real
// MCP tool call (resolveItem re-resolves by name and would fail first, not
// silently proceed with a stale id — see
// TestUpdateItemStorageReportsZeroRowsForDeletedItem's comment), so this
// duplicates the closure body rather than the whole handler, which is the
// smallest way to exercise the branch deterministically instead of leaving
// it purely hand-traced.
func TestMoveItemTransactionAbortsAndSkipsAuditLogForDeletedItem(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)

	storage, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "toctou handler test shelf", QrToken: codegen.PlainTextCode(), IsShared: true,
	})
	if err != nil {
		t.Fatalf("InsertStorage: %v", err)
	}
	item, err := q.InsertItem(ctx, store.InsertItemParams{
		StorageID: storage.ID, Name: "toctou handler test item", QrToken: codegen.PlainTextCode(),
		IsShared: true, Quantity: 1, CustomFields: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("InsertItem: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM items WHERE id = $1`, item.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = $1`, storage.ID)
	})

	if _, err := pool.Exec(ctx, `DELETE FROM items WHERE id = $1`, item.ID); err != nil {
		t.Fatalf("deleting item to simulate the race: %v", err)
	}

	var auditCountBefore int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE entity_type = 'item' AND entity_id = $1`, item.ID).Scan(&auditCountBefore); err != nil {
		t.Fatalf("counting audit_log rows: %v", err)
	}

	// ⚠️ Same body as moveItemHandler's withTx closure
	// (internal/mcpserver/tools.go) — edit both together, same duplication
	// hazard as search.sql's two matches CTEs (see that file's comment):
	// a change to the transaction body here goes stale silently otherwise.
	txErr := withTx(ctx, pool, func(tx pgx.Tx) error {
		txq := store.New(tx)
		rows, err := txq.UpdateItemStorage(ctx, store.UpdateItemStorageParams{ID: item.ID, StorageID: storage.ID})
		if err != nil {
			return err
		}
		if rows == 0 {
			return fmt.Errorf("item %q was deleted before the move completed", "toctou handler test item")
		}
		return txq.InsertAuditLog(ctx, store.InsertAuditLogParams{
			EntityType: "item",
			EntityID:   item.ID,
			Action:     "moved",
			UserID:     nil,
			Details:    []byte("{}"),
		})
	})
	if txErr == nil {
		t.Fatal("expected the transaction to return an error for a deleted item, got nil")
	}

	var auditCountAfter int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE entity_type = 'item' AND entity_id = $1`, item.ID).Scan(&auditCountAfter); err != nil {
		t.Fatalf("counting audit_log rows: %v", err)
	}
	if auditCountAfter != auditCountBefore {
		t.Fatalf("expected no new audit_log row for a move that never happened, count went from %d to %d", auditCountBefore, auditCountAfter)
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
	// The crafted "no item matching" message must reach the caller verbatim
	// — it's a toolError, not a raw internal error, so sanitizeToolError
	// (added alongside this test) must pass it through unchanged rather than
	// collapsing it to the generic "internal error" text.
	if text := textOf(t, res); text != `no item matching "definitely-does-not-exist-anywhere-12345" found` {
		t.Fatalf("expected the crafted not-found message, got: %q", text)
	}

	res, err = cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "add_item",
		Arguments: map[string]any{"name": "orphan", "storage": "definitely-does-not-exist-anywhere-12345"},
	})
	if err != nil {
		t.Fatalf("CallTool: unexpected protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected a tool-level error for add_item with an unresolvable storage, got success: %s", textOf(t, res))
	}
}

// TestMCPToolsSanitizeInternalErrors proves a raw internal error (a pgx/
// Postgres driver error, in this case) never reaches an MCP caller as-is —
// only sanitizeToolError's generic "internal error" text should, with the
// real error logged server-side instead. Closing the pool before the call is
// the simplest way to force a genuine internal error deterministically,
// without depending on a specific constraint name or SQLSTATE staying
// stable. Contrast with TestMCPToolsNotFoundErrors above, which proves the
// opposite case: a deliberately-crafted toolError must NOT be collapsed to
// this same generic text.
func TestMCPToolsSanitizeInternalErrors(t *testing.T) {
	pool := testPool(t)
	cs := testClient(t, pool)

	pool.Close()

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "find_items",
		Arguments: map[string]any{"query": "anything"},
	})
	if err != nil {
		t.Fatalf("CallTool: unexpected protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected a tool-level error once the pool is closed, got success: %s", textOf(t, res))
	}
	if text := textOf(t, res); text != "internal error" {
		t.Fatalf("expected the sanitized generic message, got a leaked raw error instead: %q", text)
	}
}

func containsItem(items []foundItem, id int64, storage string) bool {
	for _, i := range items {
		if i.ID == id && i.Storage == storage {
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

// TestMCPToolsAddStorageWithLocation proves add_storage's optional location
// param resolves and applies correctly, and that the returned path is
// already Location-prefixed for free — breadcrumbText resolves it via
// StorageBreadcrumb's root walk, no separate lookup needed in the handler.
func TestMCPToolsAddStorageWithLocation(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)
	cs := testClient(t, pool)

	location, err := q.InsertLocation(ctx, store.InsertLocationParams{Name: "MCP Test Location House", IsShared: true})
	if err != nil {
		t.Fatalf("InsertLocation: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM locations WHERE id = $1`, location.ID) })

	var root addStorageOutput
	callTool(t, cs, "add_storage", map[string]any{"name": "MCP Test Location Balcony", "location": "MCP Test Location House"}, &root)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = $1`, root.ID) })

	if root.Path != "MCP Test Location House > MCP Test Location Balcony" {
		t.Fatalf("expected the Location-prefixed path, got %q", root.Path)
	}

	got, err := q.GetStorageByID(ctx, root.ID)
	if err != nil {
		t.Fatalf("GetStorageByID: %v", err)
	}
	if got.LocationID == nil || *got.LocationID != location.ID {
		t.Fatalf("expected location_id %d on the created storage, got %v", location.ID, got.LocationID)
	}
}

// TestMCPToolsAddStorageRejectsParentAndLocationTogether mirrors REST's
// create-handler 422 (internal/api/storages_test.go's
// TestStorageCreateWithParentAndLocationRejected) — the combination is
// contradictory (a nested storage always inherits its location from its
// root ancestor), so add_storage must refuse it rather than guess which one
// wins.
func TestMCPToolsAddStorageRejectsParentAndLocationTogether(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)
	cs := testClient(t, pool)

	parent, err := q.InsertStorage(ctx, store.InsertStorageParams{Name: "MCP Test Combo Parent", QrToken: codegen.PlainTextCode(), IsShared: true})
	if err != nil {
		t.Fatalf("InsertStorage: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = $1`, parent.ID) })

	location, err := q.InsertLocation(ctx, store.InsertLocationParams{Name: "MCP Test Combo House", IsShared: true})
	if err != nil {
		t.Fatalf("InsertLocation: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM locations WHERE id = $1`, location.ID) })

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "add_storage",
		Arguments: map[string]any{
			"name": "MCP Test Combo Child", "parent": "MCP Test Combo Parent", "location": "MCP Test Combo House",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: unexpected protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected a tool error for parent+location together, got success: %s", textOf(t, res))
	}
}

// TestMCPToolsAddStorageLocationNotFoundEnumeratesNames proves
// resolveLocation's not-found error lists every real Location name, so an
// LLM caller can self-correct in one turn rather than guessing blind again
// — locations aren't fuzzy-searchable, so there's no "close match" to fall
// back on the way resolveStorage/resolveItem have.
func TestMCPToolsAddStorageLocationNotFoundEnumeratesNames(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)
	cs := testClient(t, pool)

	location, err := q.InsertLocation(ctx, store.InsertLocationParams{Name: "MCP Test Enumerate House", IsShared: true})
	if err != nil {
		t.Fatalf("InsertLocation: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM locations WHERE id = $1`, location.ID) })

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "add_storage",
		Arguments: map[string]any{"name": "MCP Test Enumerate Storage", "location": "definitely-not-a-real-location-98765"},
	})
	if err != nil {
		t.Fatalf("CallTool: unexpected protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected a tool error for an unresolvable location, got success: %s", textOf(t, res))
	}
	text := textOf(t, res)
	if !strings.Contains(text, "MCP Test Enumerate House") {
		t.Fatalf("expected the not-found error to enumerate real location names, got: %q", text)
	}
}

// TestMCPToolsListContentsByLocation proves list_contents' location branch
// fans out across every root storage assigned to a Location — the "browse
// by location" half of the user's explicit "everywhere" requirement that
// add_storage's location param and list_locations alone don't cover. Two
// root storages at the same Location, each with its own item and its own
// nested child, must all surface in one call.
func TestMCPToolsListContentsByLocation(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)
	cs := testClient(t, pool)

	location, err := q.InsertLocation(ctx, store.InsertLocationParams{Name: "MCP Test Contents House", IsShared: true})
	if err != nil {
		t.Fatalf("InsertLocation: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM locations WHERE id = $1`, location.ID) })

	rootA, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "MCP Test Contents Root A", QrToken: codegen.PlainTextCode(), IsShared: true, LocationID: &location.ID,
	})
	if err != nil {
		t.Fatalf("InsertStorage rootA: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = $1`, rootA.ID) })

	rootB, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "MCP Test Contents Root B", QrToken: codegen.PlainTextCode(), IsShared: true, LocationID: &location.ID,
	})
	if err != nil {
		t.Fatalf("InsertStorage rootB: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = $1`, rootB.ID) })

	child, err := q.InsertStorage(ctx, store.InsertStorageParams{
		Name: "MCP Test Contents Child", QrToken: codegen.PlainTextCode(), IsShared: true, ParentID: &rootA.ID,
	})
	if err != nil {
		t.Fatalf("InsertStorage child: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE id = $1`, child.ID) })

	itemInChild, err := q.InsertItem(ctx, store.InsertItemParams{
		StorageID: child.ID, Name: "MCP Test Contents Item In Child", QrToken: codegen.PlainTextCode(),
		IsShared: true, Quantity: 1, CustomFields: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("InsertItem itemInChild: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM items WHERE id = $1`, itemInChild.ID) })

	itemInRootB, err := q.InsertItem(ctx, store.InsertItemParams{
		StorageID: rootB.ID, Name: "MCP Test Contents Item In Root B", QrToken: codegen.PlainTextCode(),
		IsShared: true, Quantity: 2, CustomFields: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("InsertItem itemInRootB: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM items WHERE id = $1`, itemInRootB.ID) })

	var out listContentsOutput
	callTool(t, cs, "list_contents", map[string]any{"location": "MCP Test Contents House"}, &out)

	if out.Location == nil || *out.Location != "MCP Test Contents House" {
		t.Fatalf("expected the location field to resolve, got %+v", out.Location)
	}
	if !containsContentItem(out.Items, itemInChild.ID, 1) {
		t.Fatalf("expected the item nested under root A's child to be included, got %+v", out.Items)
	}
	if !containsContentItem(out.Items, itemInRootB.ID, 2) {
		t.Fatalf("expected root B's own item to be included, got %+v", out.Items)
	}

	// storage and location together must be rejected, not silently pick one.
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "list_contents",
		Arguments: map[string]any{"storage": "MCP Test Contents Root A", "location": "MCP Test Contents House"},
	})
	if err != nil {
		t.Fatalf("CallTool: unexpected protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected a tool error for storage+location together, got success: %s", textOf(t, res))
	}
}

// TestMCPToolsListLocations proves the read-only list_locations tool
// surfaces real rows in name order (ListLocations' own ORDER BY).
func TestMCPToolsListLocations(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := store.New(pool)
	cs := testClient(t, pool)

	for _, name := range []string{"MCP Test List Garage", "MCP Test List Apartment"} {
		loc, err := q.InsertLocation(ctx, store.InsertLocationParams{Name: name, IsShared: true})
		if err != nil {
			t.Fatalf("InsertLocation(%q): %v", name, err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM locations WHERE id = $1`, loc.ID) })
	}

	var out listLocationsOutput
	callTool(t, cs, "list_locations", map[string]any{}, &out)

	var names []string
	for _, l := range out.Locations {
		if strings.HasPrefix(l.Name, "MCP Test List ") {
			names = append(names, l.Name)
		}
	}
	if len(names) != 2 || names[0] != "MCP Test List Apartment" || names[1] != "MCP Test List Garage" {
		t.Fatalf("expected [MCP Test List Apartment, MCP Test List Garage] in that order, got %v", names)
	}
}

// ==================== Locations CRUD ====================

func TestMCPToolsAddLocation(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	cs := testClient(t, pool)
	ctx := context.Background()

	var out addLocationOutput
	callTool(t, cs, "add_location", map[string]any{"name": "MCP Test Add Location House"}, &out)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM locations WHERE id = $1`, out.ID) })

	if out.ID == 0 || out.Name != "MCP Test Add Location House" {
		t.Fatalf("unexpected add_location result: %+v", out)
	}
}

func TestMCPToolsAddLocationRejectsCaseVariantDuplicate(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	cs := testClient(t, pool)
	ctx := context.Background()

	var first addLocationOutput
	callTool(t, cs, "add_location", map[string]any{"name": "MCP Test Dup Location"}, &first)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM locations WHERE id = $1`, first.ID) })

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "add_location", Arguments: map[string]any{"name": "mcp test dup location"},
	})
	if err != nil {
		t.Fatalf("CallTool: unexpected protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected a duplicate-name error, got success: %s", textOf(t, res))
	}
}

func TestMCPToolsEditLocation(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	cs := testClient(t, pool)
	ctx := context.Background()

	var created addLocationOutput
	callTool(t, cs, "add_location", map[string]any{"name": "MCP Test Edit Location Old"}, &created)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM locations WHERE id = $1`, created.ID) })

	var edited editLocationOutput
	callTool(t, cs, "edit_location", map[string]any{
		"location": "mcp test edit location old", "name": "MCP Test Edit Location New",
	}, &edited)
	if edited.ID != created.ID || edited.Name != "MCP Test Edit Location New" {
		t.Fatalf("unexpected edit_location result: %+v", edited)
	}
}

func TestMCPToolsDeleteLocationWithoutStoragesSucceeds(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	cs := testClient(t, pool)
	ctx := context.Background()

	var created addLocationOutput
	callTool(t, cs, "add_location", map[string]any{"name": "MCP Test Delete Empty Location"}, &created)

	var out deleteLocationOutput
	callTool(t, cs, "delete_location", map[string]any{"location": "MCP Test Delete Empty Location"}, &out)
	if out.StoragesUnassigned != 0 || out.Deleted != "MCP Test Delete Empty Location" {
		t.Fatalf("unexpected delete_location result: %+v", out)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM locations WHERE id = $1`, created.ID).Scan(&count); err != nil {
		t.Fatalf("checking deletion: %v", err)
	}
	if count != 0 {
		t.Fatal("location should be deleted")
	}
}

// TestMCPToolsDeleteLocationWithStoragesRequiresForce proves delete_location's
// force is safe to expose (unlike delete_storage's, removed entirely): it
// only ever clears a storage's location assignment, never deletes or
// otherwise alters the storage itself.
func TestMCPToolsDeleteLocationWithStoragesRequiresForce(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	cs := testClient(t, pool)
	ctx := context.Background()
	q := store.New(pool)

	var loc addLocationOutput
	callTool(t, cs, "add_location", map[string]any{"name": "MCP Test Delete Blocked Location"}, &loc)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM locations WHERE id = $1`, loc.ID) })

	var storage addStorageOutput
	callTool(t, cs, "add_storage", map[string]any{
		"name": "MCP Test Delete Blocked Storage", "location": "MCP Test Delete Blocked Location",
	}, &storage)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE name LIKE 'MCP Test %'`) })

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "delete_location", Arguments: map[string]any{"location": "MCP Test Delete Blocked Location"},
	})
	if err != nil {
		t.Fatalf("CallTool: unexpected protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected refusal without force, got success: %s", textOf(t, res))
	}

	exists, err := q.StorageExists(ctx, storage.ID)
	if err != nil {
		t.Fatalf("StorageExists: %v", err)
	}
	if !exists {
		t.Fatal("storage must not be deleted or altered by a refused delete_location call")
	}

	var out deleteLocationOutput
	callTool(t, cs, "delete_location", map[string]any{
		"location": "MCP Test Delete Blocked Location", "force": true,
	}, &out)
	if out.StoragesUnassigned != 1 {
		t.Fatalf("expected 1 storage unassigned, got %d", out.StoragesUnassigned)
	}

	s, err := q.GetStorageByID(ctx, storage.ID)
	if err != nil {
		t.Fatalf("GetStorageByID: %v", err)
	}
	if s.LocationID != nil {
		t.Fatal("storage should be unassigned, not left with a location pointing at a deleted row")
	}
}

// ==================== Storage edit/move/delete ====================

func TestMCPToolsEditStorageNameAndNotes(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	cs := testClient(t, pool)
	ctx := context.Background()

	var storage addStorageOutput
	callTool(t, cs, "add_storage", map[string]any{"name": "MCP Test Edit Storage Old"}, &storage)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE name LIKE 'MCP Test %'`) })

	var edited editStorageOutput
	callTool(t, cs, "edit_storage", map[string]any{
		"storage": "mcp test edit storage old", "name": "MCP Test Edit Storage New", "notes": "some notes",
	}, &edited)
	if edited.Name != "MCP Test Edit Storage New" {
		t.Fatalf("unexpected edit_storage result: %+v", edited)
	}

	var notes *string
	if err := pool.QueryRow(ctx, `SELECT notes FROM storages WHERE id = $1`, storage.ID).Scan(&notes); err != nil {
		t.Fatalf("checking notes: %v", err)
	}
	if notes == nil || *notes != "some notes" {
		t.Fatalf("expected notes to be saved, got %v", notes)
	}
}

func TestMCPToolsEditStorageRejectsNoFields(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	cs := testClient(t, pool)
	ctx := context.Background()

	var storage addStorageOutput
	callTool(t, cs, "add_storage", map[string]any{"name": "MCP Test Edit Storage NoFields"}, &storage)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE name LIKE 'MCP Test %'`) })

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "edit_storage", Arguments: map[string]any{"storage": "MCP Test Edit Storage NoFields"},
	})
	if err != nil {
		t.Fatalf("CallTool: unexpected protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected a tool error when no fields are given, got success: %s", textOf(t, res))
	}
}

// TestMCPToolsMoveStorageNests proves the new_parent branch nests the
// storage and clears its own location — the exact auto-clear-on-nest
// behavior internal/api/storages.go's PATCH handler already enforces.
func TestMCPToolsMoveStorageNests(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	cs := testClient(t, pool)
	ctx := context.Background()
	q := store.New(pool)

	var loc addLocationOutput
	callTool(t, cs, "add_location", map[string]any{"name": "MCP Test Move Storage Location"}, &loc)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM locations WHERE id = $1`, loc.ID) })

	var root, other addStorageOutput
	callTool(t, cs, "add_storage", map[string]any{
		"name": "MCP Test Move Root", "location": "MCP Test Move Storage Location",
	}, &root)
	callTool(t, cs, "add_storage", map[string]any{"name": "MCP Test Move Other Root"}, &other)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE name LIKE 'MCP Test %'`) })

	var moved moveStorageOutput
	callTool(t, cs, "move_storage", map[string]any{
		"storage": "MCP Test Move Root", "new_parent": "MCP Test Move Other Root",
	}, &moved)
	if moved.Path != "MCP Test Move Other Root > MCP Test Move Root" {
		t.Fatalf("unexpected path after nesting: %q", moved.Path)
	}

	s, err := q.GetStorageByID(ctx, root.ID)
	if err != nil {
		t.Fatalf("GetStorageByID: %v", err)
	}
	if s.ParentID == nil || *s.ParentID != other.ID {
		t.Fatalf("expected parent_id to be set to the new parent, got %v", s.ParentID)
	}
	if s.LocationID != nil {
		t.Fatal("expected location_id to be cleared once nested — a nested storage can't have its own location")
	}
}

// TestMCPToolsMoveStoragePromotesToRootWithLocation proves the new_location
// branch detaches from any parent and assigns the Location directly.
func TestMCPToolsMoveStoragePromotesToRootWithLocation(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	cs := testClient(t, pool)
	ctx := context.Background()
	q := store.New(pool)

	var loc addLocationOutput
	callTool(t, cs, "add_location", map[string]any{"name": "MCP Test Promote Location"}, &loc)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM locations WHERE id = $1`, loc.ID) })

	var root, child addStorageOutput
	callTool(t, cs, "add_storage", map[string]any{"name": "MCP Test Promote Root"}, &root)
	callTool(t, cs, "add_storage", map[string]any{
		"name": "MCP Test Promote Child", "parent": "MCP Test Promote Root",
	}, &child)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE name LIKE 'MCP Test %'`) })

	var moved moveStorageOutput
	callTool(t, cs, "move_storage", map[string]any{
		"storage": "MCP Test Promote Child", "new_location": "MCP Test Promote Location",
	}, &moved)
	if moved.Path != "MCP Test Promote Location > MCP Test Promote Child" {
		t.Fatalf("unexpected path after promotion: %q", moved.Path)
	}

	s, err := q.GetStorageByID(ctx, child.ID)
	if err != nil {
		t.Fatalf("GetStorageByID: %v", err)
	}
	if s.ParentID != nil {
		t.Fatal("expected parent_id to be cleared — this storage should now be root-level")
	}
	if s.LocationID == nil || *s.LocationID != loc.ID {
		t.Fatalf("expected location_id to be set to the new location, got %v", s.LocationID)
	}
}

func TestMCPToolsMoveStorageRejectsBothOrNeither(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	cs := testClient(t, pool)
	ctx := context.Background()

	var storage addStorageOutput
	callTool(t, cs, "add_storage", map[string]any{"name": "MCP Test Move Both Or Neither"}, &storage)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE name LIKE 'MCP Test %'`) })

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "move_storage", Arguments: map[string]any{"storage": "MCP Test Move Both Or Neither"},
	})
	if err != nil {
		t.Fatalf("CallTool: unexpected protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected refusal when neither new_parent nor new_location is given, got success: %s", textOf(t, res))
	}

	res, err = cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "move_storage",
		Arguments: map[string]any{
			"storage": "MCP Test Move Both Or Neither", "new_parent": "x", "new_location": "y",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: unexpected protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected refusal when both new_parent and new_location are given, got success: %s", textOf(t, res))
	}
}

func TestMCPToolsMoveStorageRejectsCycle(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	cs := testClient(t, pool)
	ctx := context.Background()

	var root, child addStorageOutput
	callTool(t, cs, "add_storage", map[string]any{"name": "MCP Test Cycle Root"}, &root)
	callTool(t, cs, "add_storage", map[string]any{
		"name": "MCP Test Cycle Child", "parent": "MCP Test Cycle Root",
	}, &child)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE name LIKE 'MCP Test %'`) })

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "move_storage",
		Arguments: map[string]any{"storage": "MCP Test Cycle Root", "new_parent": "MCP Test Cycle Child"},
	})
	if err != nil {
		t.Fatalf("CallTool: unexpected protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected a cycle to be rejected, got success: %s", textOf(t, res))
	}
}

// TestMCPToolsDeleteStorageWithDirectItemsAlwaysRefuses proves delete_storage
// has no force override at all — a root storage holding items directly
// always refuses, and the items are never touched, matching the
// REST/web-UI-wide rule (2026-09-20: deleting a storage must never delete
// the items inside it, in any case).
func TestMCPToolsDeleteStorageWithDirectItemsAlwaysRefuses(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	cs := testClient(t, pool)
	ctx := context.Background()
	q := store.New(pool)

	var storage addStorageOutput
	callTool(t, cs, "add_storage", map[string]any{"name": "MCP Test Delete Blocked Storage2"}, &storage)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE name LIKE 'MCP Test %'`) })

	var item addItemOutput
	callTool(t, cs, "add_item", map[string]any{
		"name": "MCP Test Delete Blocked Item", "storage": "MCP Test Delete Blocked Storage2",
	}, &item)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM items WHERE name LIKE 'MCP Test %'`) })

	// No "force" field exists on this tool's schema at all — passing one
	// anyway is rejected at the protocol layer before the handler ever
	// runs, which is even stronger proof there's no override than a
	// business-logic refusal would be.
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "delete_storage",
		Arguments: map[string]any{"storage": "MCP Test Delete Blocked Storage2", "force": true},
	})
	if err != nil {
		t.Fatalf("CallTool: unexpected protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected a schema-validation error for an unrecognized force param, got success: %s", textOf(t, res))
	}
	if !strings.Contains(textOf(t, res), "force") {
		t.Fatalf("expected the schema error to name the unrecognized force field, got %q", textOf(t, res))
	}

	// The real (fieldless) call must refuse too, and point at move_item.
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "delete_storage",
		Arguments: map[string]any{"storage": "MCP Test Delete Blocked Storage2"},
	})
	if err != nil {
		t.Fatalf("CallTool: unexpected protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected delete_storage to always refuse when items are held directly, got success: %s", textOf(t, res))
	}
	if !strings.Contains(textOf(t, res), "move_item") {
		t.Fatalf("expected the refusal to point at move_item, got %q", textOf(t, res))
	}

	itemExists, err := q.ItemExists(ctx, item.ID)
	if err != nil {
		t.Fatalf("ItemExists: %v", err)
	}
	if !itemExists {
		t.Fatal("item must survive a refused delete_storage call")
	}
	storageExists, err := q.StorageExists(ctx, storage.ID)
	if err != nil {
		t.Fatalf("StorageExists: %v", err)
	}
	if !storageExists {
		t.Fatal("storage must survive a refused delete_storage call")
	}
}

// TestMCPToolsDeleteStoragePromotesChildrenAndLocation proves the
// non-destructive parts of delete_storage still work: a non-root storage's
// direct items promote to its parent, and a deleted storage's effective
// Location propagates onto any children it promotes to root — mirroring
// internal/api/storages.go's delete handler exactly.
func TestMCPToolsDeleteStoragePromotesChildrenAndLocation(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	cs := testClient(t, pool)
	ctx := context.Background()
	q := store.New(pool)

	var loc addLocationOutput
	callTool(t, cs, "add_location", map[string]any{"name": "MCP Test Delete Propagate Location"}, &loc)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM locations WHERE id = $1`, loc.ID) })

	var root, middle, leaf addStorageOutput
	callTool(t, cs, "add_storage", map[string]any{
		"name": "MCP Test Delete Propagate Root", "location": "MCP Test Delete Propagate Location",
	}, &root)
	callTool(t, cs, "add_storage", map[string]any{
		"name": "MCP Test Delete Propagate Middle", "parent": "MCP Test Delete Propagate Root",
	}, &middle)
	callTool(t, cs, "add_storage", map[string]any{
		"name": "MCP Test Delete Propagate Leaf", "parent": "MCP Test Delete Propagate Middle",
	}, &leaf)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE name LIKE 'MCP Test %'`) })

	var item addItemOutput
	callTool(t, cs, "add_item", map[string]any{
		"name": "MCP Test Delete Propagate Item", "storage": "MCP Test Delete Propagate Middle",
	}, &item)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM items WHERE name LIKE 'MCP Test %'`) })

	var out deleteStorageOutput
	callTool(t, cs, "delete_storage", map[string]any{"storage": "MCP Test Delete Propagate Middle"}, &out)
	if out.ChildrenPromoted != 1 {
		t.Fatalf("expected 1 child storage promoted, got %d", out.ChildrenPromoted)
	}

	// The item directly in "Middle" must have promoted to "Root" (Middle's parent).
	updatedItem, err := q.GetItemByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetItemByID: %v", err)
	}
	if updatedItem.StorageID != root.ID {
		t.Fatalf("expected item to promote to the deleted storage's parent (%d), got %d", root.ID, updatedItem.StorageID)
	}

	// "Leaf" must now be root-level and must have inherited Root's location.
	updatedLeaf, err := q.GetStorageByID(ctx, leaf.ID)
	if err != nil {
		t.Fatalf("GetStorageByID: %v", err)
	}
	if updatedLeaf.ParentID != nil {
		t.Fatalf("expected leaf to be promoted to root, still has parent_id = %v", updatedLeaf.ParentID)
	}
	if updatedLeaf.LocationID == nil || *updatedLeaf.LocationID != loc.ID {
		t.Fatalf("expected leaf to inherit the deleted storage's effective location, got %v", updatedLeaf.LocationID)
	}
}

// ==================== Item edit/delete/tags ====================

func TestMCPToolsEditItemFields(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	cs := testClient(t, pool)
	ctx := context.Background()

	var storage addStorageOutput
	callTool(t, cs, "add_storage", map[string]any{"name": "MCP Test Edit Item Storage"}, &storage)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE name LIKE 'MCP Test %'`) })

	var item addItemOutput
	callTool(t, cs, "add_item", map[string]any{
		"name": "MCP Test Edit Item Widget", "storage": "MCP Test Edit Item Storage",
	}, &item)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM items WHERE name LIKE 'MCP Test %'`) })

	price := 12.5
	var edited editItemOutput
	callTool(t, cs, "edit_item", map[string]any{
		"item": "mcp test edit item widget", "name": "MCP Test Edit Item Widget Renamed",
		"description": "a new description", "quantity": int32(4), "condition": "good",
		"purchase_date": "2026-01-15", "purchase_price": price, "receipt_url": "https://example.com/r.pdf",
	}, &edited)
	if edited.Name != "MCP Test Edit Item Widget Renamed" {
		t.Fatalf("unexpected edit_item result: %+v", edited)
	}

	var description, condition, receiptURL *string
	var quantity int32
	var purchaseDate *time.Time
	var purchasePrice *float64
	err := pool.QueryRow(ctx,
		`SELECT description, quantity, condition, purchase_date, purchase_price, receipt_url FROM items WHERE id = $1`,
		item.ID,
	).Scan(&description, &quantity, &condition, &purchaseDate, &purchasePrice, &receiptURL)
	if err != nil {
		t.Fatalf("checking saved fields: %v", err)
	}
	if description == nil || *description != "a new description" {
		t.Fatalf("expected description to be saved, got %v", description)
	}
	if quantity != 4 {
		t.Fatalf("expected quantity 4, got %d", quantity)
	}
	if condition == nil || *condition != "good" {
		t.Fatalf("expected condition 'good', got %v", condition)
	}
	if purchaseDate == nil || purchaseDate.Format("2006-01-02") != "2026-01-15" {
		t.Fatalf("expected purchase_date 2026-01-15, got %v", purchaseDate)
	}
	if purchasePrice == nil || *purchasePrice != 12.5 {
		t.Fatalf("expected purchase_price 12.5, got %v", purchasePrice)
	}
	if receiptURL == nil || *receiptURL != "https://example.com/r.pdf" {
		t.Fatalf("expected receipt_url to be saved, got %v", receiptURL)
	}
}

func TestMCPToolsEditItemRejectsNoFields(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	cs := testClient(t, pool)
	ctx := context.Background()

	var storage addStorageOutput
	callTool(t, cs, "add_storage", map[string]any{"name": "MCP Test Edit Item NoFields Storage"}, &storage)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE name LIKE 'MCP Test %'`) })
	var item addItemOutput
	callTool(t, cs, "add_item", map[string]any{
		"name": "MCP Test Edit Item NoFields Widget", "storage": "MCP Test Edit Item NoFields Storage",
	}, &item)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM items WHERE name LIKE 'MCP Test %'`) })

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "edit_item", Arguments: map[string]any{"item": "MCP Test Edit Item NoFields Widget"},
	})
	if err != nil {
		t.Fatalf("CallTool: unexpected protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected a tool error when no fields are given, got success: %s", textOf(t, res))
	}
}

func TestMCPToolsDeleteItem(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	cs := testClient(t, pool)
	ctx := context.Background()
	q := store.New(pool)

	var storage addStorageOutput
	callTool(t, cs, "add_storage", map[string]any{"name": "MCP Test Delete Item Storage"}, &storage)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE name LIKE 'MCP Test %'`) })
	var item addItemOutput
	callTool(t, cs, "add_item", map[string]any{
		"name": "MCP Test Delete Item Widget", "storage": "MCP Test Delete Item Storage",
	}, &item)

	var out deleteItemOutput
	callTool(t, cs, "delete_item", map[string]any{"item": "mcp test delete item widget"}, &out)
	if out.Deleted != "MCP Test Delete Item Widget" {
		t.Fatalf("unexpected delete_item result: %+v", out)
	}

	exists, err := q.ItemExists(ctx, item.ID)
	if err != nil {
		t.Fatalf("ItemExists: %v", err)
	}
	if exists {
		t.Fatal("item should be deleted")
	}
}

// TestMCPToolsAddItemTagIsIdempotent proves adding the same tag twice
// doesn't duplicate the link (LinkItemTag's own ON CONFLICT DO NOTHING) and
// that a not-yet-existing tag is created on the fly.
func TestMCPToolsAddItemTagIsIdempotent(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	cs := testClient(t, pool)
	ctx := context.Background()

	var storage addStorageOutput
	callTool(t, cs, "add_storage", map[string]any{"name": "MCP Test Tag Storage"}, &storage)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE name LIKE 'MCP Test %'`) })
	var item addItemOutput
	callTool(t, cs, "add_item", map[string]any{
		"name": "MCP Test Tag Drill", "storage": "MCP Test Tag Storage",
	}, &item)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM items WHERE name LIKE 'MCP Test %'`) })
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM tags WHERE name LIKE 'MCP Test %'`) })

	var first addItemTagOutput
	callTool(t, cs, "add_item_tag", map[string]any{"item": "MCP Test Tag Drill", "tag": "MCP Test Tag Electronic Tool"}, &first)
	if len(first.Tags) != 1 || first.Tags[0] != "MCP Test Tag Electronic Tool" {
		t.Fatalf("unexpected tags after first add: %+v", first.Tags)
	}

	var second addItemTagOutput
	callTool(t, cs, "add_item_tag", map[string]any{"item": "MCP Test Tag Drill", "tag": "mcp test tag electronic tool"}, &second)
	if len(second.Tags) != 1 {
		t.Fatalf("expected adding the same tag again to stay idempotent, got %+v", second.Tags)
	}
}

func TestMCPToolsRemoveItemTag(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	cs := testClient(t, pool)
	ctx := context.Background()

	var storage addStorageOutput
	callTool(t, cs, "add_storage", map[string]any{"name": "MCP Test Untag Storage"}, &storage)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE name LIKE 'MCP Test %'`) })
	var item addItemOutput
	callTool(t, cs, "add_item", map[string]any{
		"name": "MCP Test Untag Drill", "storage": "MCP Test Untag Storage",
		"tags": []string{"MCP Test Untag TagA", "MCP Test Untag TagB"},
	}, &item)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM items WHERE name LIKE 'MCP Test %'`) })
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM tags WHERE name LIKE 'MCP Test %'`) })

	var out removeItemTagOutput
	callTool(t, cs, "remove_item_tag", map[string]any{"item": "MCP Test Untag Drill", "tag": "mcp test untag taga"}, &out)
	if len(out.Tags) != 1 || out.Tags[0] != "MCP Test Untag TagB" {
		t.Fatalf("expected only TagB left, got %+v", out.Tags)
	}
}

func TestMCPToolsRemoveItemTagErrorsWhenNotTagged(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	cs := testClient(t, pool)
	ctx := context.Background()

	var storage addStorageOutput
	callTool(t, cs, "add_storage", map[string]any{"name": "MCP Test Untag Missing Storage"}, &storage)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE name LIKE 'MCP Test %'`) })
	var item addItemOutput
	callTool(t, cs, "add_item", map[string]any{
		"name": "MCP Test Untag Missing Drill", "storage": "MCP Test Untag Missing Storage",
	}, &item)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM items WHERE name LIKE 'MCP Test %'`) })

	// Tag doesn't exist at all.
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "remove_item_tag",
		Arguments: map[string]any{"item": "MCP Test Untag Missing Drill", "tag": "MCP Test Nonexistent Tag"},
	})
	if err != nil {
		t.Fatalf("CallTool: unexpected protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected an error for a nonexistent tag, got success: %s", textOf(t, res))
	}

	// Tag exists (created via a different item) but isn't attached to this one.
	var other addItemOutput
	callTool(t, cs, "add_item", map[string]any{
		"name": "MCP Test Untag Missing Other", "storage": "MCP Test Untag Missing Storage",
		"tags": []string{"MCP Test Untag Missing RealTag"},
	}, &other)

	res, err = cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "remove_item_tag",
		Arguments: map[string]any{"item": "MCP Test Untag Missing Drill", "tag": "MCP Test Untag Missing RealTag"},
	})
	if err != nil {
		t.Fatalf("CallTool: unexpected protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected an error for a real tag the item isn't wearing, got success: %s", textOf(t, res))
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM tags WHERE name LIKE 'MCP Test %'`) })
}

// ==================== add_item / add_storage field parity ====================

func TestMCPToolsAddItemAcceptsFullFieldsAndTags(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	cs := testClient(t, pool)
	ctx := context.Background()

	var storage addStorageOutput
	callTool(t, cs, "add_storage", map[string]any{"name": "MCP Test Full Fields Storage"}, &storage)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE name LIKE 'MCP Test %'`) })

	price := 99.99
	var item addItemOutput
	callTool(t, cs, "add_item", map[string]any{
		"name": "MCP Test Full Fields Widget", "storage": "MCP Test Full Fields Storage",
		"description": "full field test", "condition": "new", "purchase_date": "2026-02-01",
		"purchase_price": price, "receipt_url": "https://example.com/receipt.pdf",
		"tags": []string{"MCP Test Full Fields TagA", "MCP Test Full Fields TagB"},
	}, &item)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM items WHERE name LIKE 'MCP Test %'`) })
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM tags WHERE name LIKE 'MCP Test %'`) })

	q := store.New(pool)
	row, err := q.GetItemByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetItemByID: %v", err)
	}
	if row.Description == nil || *row.Description != "full field test" {
		t.Fatalf("expected description saved, got %v", row.Description)
	}
	if row.Condition == nil || *row.Condition != "new" {
		t.Fatalf("expected condition saved, got %v", row.Condition)
	}
	if len(row.Tags) != 2 || row.Tags[0] != "MCP Test Full Fields TagA" || row.Tags[1] != "MCP Test Full Fields TagB" {
		t.Fatalf("expected both tags attached in order, got %v", row.Tags)
	}
}

func TestMCPToolsAddStorageAcceptsNotes(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	cs := testClient(t, pool)
	ctx := context.Background()

	var storage addStorageOutput
	callTool(t, cs, "add_storage", map[string]any{
		"name": "MCP Test Storage Notes", "notes": "kept in the garage",
	}, &storage)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE name LIKE 'MCP Test %'`) })

	var notes *string
	if err := pool.QueryRow(ctx, `SELECT notes FROM storages WHERE id = $1`, storage.ID).Scan(&notes); err != nil {
		t.Fatalf("checking notes: %v", err)
	}
	if notes == nil || *notes != "kept in the garage" {
		t.Fatalf("expected notes to be saved, got %v", notes)
	}
}

// TestMCPToolsEditItemLeavesOmittedFieldsUnchanged proves UpdateItemFields'
// COALESCE(sqlc.narg(x), x) pattern actually works as documented — editing
// only quantity must not touch description/condition/purchase_price, which
// TestMCPToolsEditItemFields alone (it sets every field) can't catch: if
// UpdateItemFields' COALESCE were ever simplified away to a plain SET, this
// is the only test that would fail.
func TestMCPToolsEditItemLeavesOmittedFieldsUnchanged(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	cs := testClient(t, pool)
	ctx := context.Background()

	var storage addStorageOutput
	callTool(t, cs, "add_storage", map[string]any{"name": "MCP Test Unchanged Fields Storage"}, &storage)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE name LIKE 'MCP Test %'`) })

	price := 42.0
	var item addItemOutput
	callTool(t, cs, "add_item", map[string]any{
		"name": "MCP Test Unchanged Fields Widget", "storage": "MCP Test Unchanged Fields Storage",
		"description": "original description", "condition": "good", "purchase_price": price,
	}, &item)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM items WHERE name LIKE 'MCP Test %'`) })

	var edited editItemOutput
	callTool(t, cs, "edit_item", map[string]any{
		"item": "MCP Test Unchanged Fields Widget", "quantity": int32(7),
	}, &edited)

	var description, condition *string
	var quantity int32
	var purchasePrice *float64
	err := pool.QueryRow(ctx,
		`SELECT description, quantity, condition, purchase_price FROM items WHERE id = $1`, item.ID,
	).Scan(&description, &quantity, &condition, &purchasePrice)
	if err != nil {
		t.Fatalf("checking fields: %v", err)
	}
	if quantity != 7 {
		t.Fatalf("expected quantity to change to 7, got %d", quantity)
	}
	if description == nil || *description != "original description" {
		t.Fatalf("expected description to survive an edit_item call that didn't mention it, got %v", description)
	}
	if condition == nil || *condition != "good" {
		t.Fatalf("expected condition to survive an edit_item call that didn't mention it, got %v", condition)
	}
	if purchasePrice == nil || *purchasePrice != 42.0 {
		t.Fatalf("expected purchase_price to survive an edit_item call that didn't mention it, got %v", purchasePrice)
	}
}

// TestMCPToolsEditStorageLeavesOmittedFieldUnchanged is
// TestMCPToolsEditItemLeavesOmittedFieldsUnchanged's sibling for
// UpdateStorageMetadata.
func TestMCPToolsEditStorageLeavesOmittedFieldUnchanged(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	cs := testClient(t, pool)
	ctx := context.Background()

	var storage addStorageOutput
	callTool(t, cs, "add_storage", map[string]any{
		"name": "MCP Test Unchanged Storage Old", "notes": "original notes",
	}, &storage)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE name LIKE 'MCP Test %'`) })

	callTool(t, cs, "edit_storage", map[string]any{
		"storage": "MCP Test Unchanged Storage Old", "name": "MCP Test Unchanged Storage New",
	}, new(editStorageOutput))

	var notes *string
	if err := pool.QueryRow(ctx, `SELECT notes FROM storages WHERE id = $1`, storage.ID).Scan(&notes); err != nil {
		t.Fatalf("checking notes: %v", err)
	}
	if notes == nil || *notes != "original notes" {
		t.Fatalf("expected notes to survive an edit_storage call that only renamed, got %v", notes)
	}
}

// TestMCPToolsAddItemRejectsPurchasePriceOutOfRange and its edit_item
// sibling prove a NUMERIC(10,2) overflow surfaces as a clear tool error, not
// sanitizeToolError's generic "internal error" — the REST API already fixed
// the identical gap for this column (CLAUDE.md's "Map FK/constraint
// violations to proper 4xx" item); MCP needs the same treatment since it
// exposes the same column now.
func TestMCPToolsAddItemRejectsPurchasePriceOutOfRange(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	cs := testClient(t, pool)
	ctx := context.Background()

	var storage addStorageOutput
	callTool(t, cs, "add_storage", map[string]any{"name": "MCP Test Price Range Storage"}, &storage)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE name LIKE 'MCP Test %'`) })

	tooLarge := 1e12
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "add_item",
		Arguments: map[string]any{
			"name": "MCP Test Price Range Widget", "storage": "MCP Test Price Range Storage",
			"purchase_price": tooLarge,
		},
	})
	if err != nil {
		t.Fatalf("CallTool: unexpected protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected an out-of-range error, got success: %s", textOf(t, res))
	}
	if !strings.Contains(textOf(t, res), "purchase_price") || strings.Contains(textOf(t, res), "internal error") {
		t.Fatalf("expected a clear purchase_price error, not an internal error, got %q", textOf(t, res))
	}
}

func TestMCPToolsEditItemRejectsPurchasePriceOutOfRange(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	cs := testClient(t, pool)
	ctx := context.Background()

	var storage addStorageOutput
	callTool(t, cs, "add_storage", map[string]any{"name": "MCP Test Edit Price Range Storage"}, &storage)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM storages WHERE name LIKE 'MCP Test %'`) })
	var item addItemOutput
	callTool(t, cs, "add_item", map[string]any{
		"name": "MCP Test Edit Price Range Widget", "storage": "MCP Test Edit Price Range Storage",
	}, &item)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM items WHERE name LIKE 'MCP Test %'`) })

	tooLarge := 1e12
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "edit_item",
		Arguments: map[string]any{"item": "MCP Test Edit Price Range Widget", "purchase_price": tooLarge},
	})
	if err != nil {
		t.Fatalf("CallTool: unexpected protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected an out-of-range error, got success: %s", textOf(t, res))
	}
	if !strings.Contains(textOf(t, res), "purchase_price") || strings.Contains(textOf(t, res), "internal error") {
		t.Fatalf("expected a clear purchase_price error, not an internal error, got %q", textOf(t, res))
	}
}

// TestMCPToolsEditLocationRejectsCaseVariantDuplicate is
// TestMCPToolsAddLocationRejectsCaseVariantDuplicate's edit_location
// sibling, flagged as untested during review.
func TestMCPToolsEditLocationRejectsCaseVariantDuplicate(t *testing.T) {
	pool := testPool(t)
	requireNoLeftoverTestRows(t, pool)
	cs := testClient(t, pool)
	ctx := context.Background()

	var a, b addLocationOutput
	callTool(t, cs, "add_location", map[string]any{"name": "MCP Test Edit Dup A"}, &a)
	callTool(t, cs, "add_location", map[string]any{"name": "MCP Test Edit Dup B"}, &b)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM locations WHERE name LIKE 'MCP Test %'`) })

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "edit_location",
		Arguments: map[string]any{"location": "MCP Test Edit Dup B", "name": "mcp test edit dup a"},
	})
	if err != nil {
		t.Fatalf("CallTool: unexpected protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected a duplicate-name error when renaming onto an existing name, got success: %s", textOf(t, res))
	}
}
