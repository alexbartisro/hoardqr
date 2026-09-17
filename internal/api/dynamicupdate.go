package api

import (
	"fmt"
	"sort"
	"strings"
)

// buildUpdateQuery constructs `UPDATE <table> SET col = $2, ... WHERE id = $1`
// from a map of column name -> new value, for PATCH endpoints where any
// subset of columns (including explicit nulls) may be present — see the
// comment on LocationsHandler.update. table and the keys of set must come
// from a fixed, code-controlled allowlist, never directly from request
// input: column/table names can't be parameterized in SQL, only values can,
// so this is safe exactly because callers never pass a map built from
// arbitrary request keys.
func buildUpdateQuery(table string, id int64, set map[string]any) (string, []any) {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic clause order, not otherwise meaningful

	args := make([]any, 1, len(set)+1)
	args[0] = id
	clauses := make([]string, len(keys))
	for i, k := range keys {
		args = append(args, set[k])
		clauses[i] = fmt.Sprintf("%s = $%d", k, len(args))
	}
	query := fmt.Sprintf("UPDATE %s SET %s WHERE id = $1", table, strings.Join(clauses, ", "))
	return query, args
}
