package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"hoardqr/internal/version"
)

func TestVersionHandlerReturnsVersionAndCommit(t *testing.T) {
	old := version.Commit
	version.Commit = "0123456789abcdef"
	t.Cleanup(func() { version.Commit = old })

	rec := httptest.NewRecorder()
	VersionHandler(rec, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var got VersionDTO
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(got.Version) {
		t.Fatalf("version %q is not semver-shaped (is the embedded VERSION file empty or padded?)", got.Version)
	}
	if got.Commit != "0123456" {
		t.Fatalf("expected the commit truncated to 7 chars, got %q", got.Commit)
	}
}

func TestVersionHandlerCommitEmptyWhenUnstamped(t *testing.T) {
	old := version.Commit
	version.Commit = ""
	t.Cleanup(func() { version.Commit = old })

	rec := httptest.NewRecorder()
	VersionHandler(rec, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	var got VersionDTO
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.Commit != "" {
		t.Fatalf("expected an empty commit, got %q", got.Commit)
	}
}
