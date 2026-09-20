package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// toolError marks an error message crafted deliberately for an MCP caller to
// see — a "not found"/"ambiguous match"/validation message — as distinct
// from a raw error surfaced by a DB driver, JSON marshaller, or other
// internal call. sanitizeToolError uses this distinction to decide what's
// safe to send back over the wire.
type toolError struct{ msg string }

func (e *toolError) Error() string { return e.msg }

// toolErrorf builds a toolError the same way fmt.Errorf builds a plain one.
func toolErrorf(format string, args ...any) error {
	return &toolError{msg: fmt.Sprintf(format, args...)}
}

// sanitizeToolError is this package's equivalent of internal/api/httpjson.go's
// serverError: a raw pgx/Postgres or JSON error string is an implementation
// detail that shouldn't reach an MCP caller — and, from there, whatever LLM
// is relaying it on to an end user — a Postgres constraint name or SQLSTATE
// isn't actionable information for either side. A toolError, by contrast,
// was built specifically to be shown, so it passes through unchanged. Every
// handler in tools.go routes every error it returns through this, at the
// point closest to the mcp.CallToolResult boundary, rather than leaving each
// call site to decide case by case whether its error is safe to surface.
func sanitizeToolError(ctx context.Context, toolName string, err error) error {
	if err == nil {
		return nil
	}
	var te *toolError
	if errors.As(err, &te) {
		return err
	}
	slog.ErrorContext(ctx, "mcp tool internal error", "tool", toolName, "error", err)
	return errors.New("internal error")
}
