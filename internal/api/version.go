package api

import (
	"net/http"

	"hoardqr/internal/version"
)

// VersionDTO mirrors VersionInfo in web/src/lib/types.ts. Commit is empty
// for a local build with no -ldflags commit stamp.
type VersionDTO struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

// VersionHandler answers GET /api/version — the app's release version (and
// build commit, when stamped) for the page footer. No database access, so
// it works even when Postgres is down.
func VersionHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, VersionDTO{Version: version.Version(), Commit: version.ShortCommit()})
}
