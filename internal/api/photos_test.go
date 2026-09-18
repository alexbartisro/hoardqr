package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// jpegMagic is enough for http.DetectContentType to sniff "image/jpeg" —
// the sniffer only inspects the leading bytes, so real JPEG data isn't
// needed to exercise the content-type allowlist.
var jpegMagic = []byte{0xFF, 0xD8, 0xFF, 0xE0}

func multipartPhotoBody(t *testing.T, fieldName, filename string, content []byte) (*bytes.Buffer, string) {
	t.Helper()
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	part, err := w.CreateFormFile(fieldName, filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("writing form file: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("closing multipart writer: %v", err)
	}
	return buf, w.FormDataContentType()
}

// TestPhotosUploadStoresAndServesBack proves the full round trip: a valid
// image is accepted, stored under a random opaque filename (never the
// client's own "my-photo.jpg"), and immediately servable back from
// /uploads/ with X-Content-Type-Options: nosniff set.
func TestPhotosUploadStoresAndServesBack(t *testing.T) {
	uploadDir := t.TempDir()
	router := NewRouter(nil, uploadDir)

	content := append(append([]byte{}, jpegMagic...), []byte("rest of a fake jpeg")...)
	body, contentType := multipartPhotoBody(t, "photo", "my-photo.jpg", content)

	req := httptest.NewRequest(http.MethodPost, "/api/photos", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		PhotoURL string `json:"photo_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if !strings.HasPrefix(resp.PhotoURL, "/uploads/") || !strings.HasSuffix(resp.PhotoURL, ".jpg") {
		t.Fatalf("expected a /uploads/....jpg URL, got %q", resp.PhotoURL)
	}
	if strings.Contains(resp.PhotoURL, "my-photo") {
		t.Fatalf("expected an opaque filename, got %q (leaked the client's own filename)", resp.PhotoURL)
	}

	// The file must actually be on disk under uploadDir, not just a URL that
	// happens to look right.
	onDisk := filepath.Join(uploadDir, filepath.Base(resp.PhotoURL))
	if _, err := os.Stat(onDisk); err != nil {
		t.Fatalf("expected file at %s: %v", onDisk, err)
	}

	// And it must be servable back through the actual router, not just
	// present on disk.
	getReq := httptest.NewRequest(http.MethodGet, resp.PhotoURL, nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET %s: expected 200, got %d", resp.PhotoURL, getRec.Code)
	}
	if !bytes.Equal(getRec.Body.Bytes(), content) {
		t.Fatalf("served content doesn't match what was uploaded")
	}
	if getRec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("expected X-Content-Type-Options: nosniff on served photos")
	}
}

// TestPhotosUploadRejectsNonImageContent proves the content-type allowlist
// is enforced against the file's real bytes, not the client-supplied
// filename or Content-Type — the specific concern is a same-origin
// stored-XSS via a file that looks like an image to the client but sniffs
// as HTML.
func TestPhotosUploadRejectsNonImageContent(t *testing.T) {
	uploadDir := t.TempDir()
	router := NewRouter(nil, uploadDir)

	html := []byte("<html><body><script>alert(1)</script></body></html>")
	body, contentType := multipartPhotoBody(t, "photo", "totally-a-photo.jpg", html)

	req := httptest.NewRequest(http.MethodPost, "/api/photos", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415, got %d: %s", rec.Code, rec.Body.String())
	}

	entries, err := os.ReadDir(uploadDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected nothing written to disk for a rejected upload, found %d entries", len(entries))
	}
}

// TestPhotosUploadRejectsOversizedBody proves the server-side 8MB cap is a
// real backstop, independent of whatever the client claims it already
// compressed the image down to.
func TestPhotosUploadRejectsOversizedBody(t *testing.T) {
	uploadDir := t.TempDir()
	router := NewRouter(nil, uploadDir)

	oversized := make([]byte, maxUploadBytes+1024)
	copy(oversized, jpegMagic)
	body, contentType := multipartPhotoBody(t, "photo", "huge.jpg", oversized)

	req := httptest.NewRequest(http.MethodPost, "/api/photos", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an oversized upload, got %d: %s", rec.Code, rec.Body.String())
	}

	entries, err := os.ReadDir(uploadDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected nothing written to disk for a rejected upload, found %d entries", len(entries))
	}
}

// TestPhotosUploadRejectsMissingField proves a request with no "photo" part
// at all gets a clear 400, not a panic or a 500.
func TestPhotosUploadRejectsMissingField(t *testing.T) {
	uploadDir := t.TempDir()
	router := NewRouter(nil, uploadDir)

	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	_ = w.WriteField("not-a-photo-field", "value")
	_ = w.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/photos", buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
