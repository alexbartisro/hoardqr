// Package migrations embeds the SQL migration files into the binary. Lives
// inside migrations/ itself (not internal/db/) because go:embed paths are
// relative to the source file's own directory and can't traverse ".." —
// same constraint noted in web/embed.go for the frontend build output.
package migrations

import "embed"

//go:embed *.sql
var Files embed.FS
