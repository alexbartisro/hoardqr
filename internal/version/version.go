// Package version holds the app's release version — one source of truth for
// the footer label (GET /api/version) instead of a git tag or package.json
// that can drift. CI only builds pushes to main/dev, never tags, so the tag
// itself can't be what the image learns its version from.
//
// Bump the VERSION file when cutting a release (right before tagging).
package version

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var raw string

// Commit is the git commit the binary was built from. Set at build time via
// `-ldflags "-X hoardqr/internal/version.Commit=<sha>"` (the Dockerfile's
// GIT_SHA build arg); empty for a plain local `go build`/`go run`.
var Commit string

// Version returns the release version, e.g. "0.0.2".
func Version() string { return strings.TrimSpace(raw) }

// ShortCommit returns the first 7 characters of Commit, or "" when unset.
func ShortCommit() string {
	c := strings.TrimSpace(Commit)
	if len(c) > 7 {
		c = c[:7]
	}
	return c
}
