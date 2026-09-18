package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-chi/chi/v5"
)

// maxUploadBytes is the server-side backstop from architecture plan §12 — a
// generous cap independent of client-side compression (browser-image-
// compression already shrinks a typical phone photo to well under this
// before it's ever sent); compression is for storage economy, not the only
// thing standing between a stray request and the disk.
const maxUploadBytes = 8 << 20 // 8MB

// allowedPhotoTypes maps a sniffed MIME type to the extension the stored
// file gets. The type is sniffed from the file's own bytes (http.DetectContentType),
// never trusted from the client-supplied Content-Type or filename, and the
// extension is always the one this map picks — never anything derived from
// the client's filename. This closes the obvious way an unauthenticated
// upload endpoint that serves files back from the app's own origin could be
// abused: an upload with a spoofed image Content-Type or a ".jpg" filename
// wrapping real HTML/SVG, later served back and executed as same-origin
// content.
var allowedPhotoTypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
	"image/gif":  ".gif",
}

type PhotosHandler struct {
	uploadDir string
}

func NewPhotosHandler(uploadDir string) *PhotosHandler {
	return &PhotosHandler{uploadDir: uploadDir}
}

func (h *PhotosHandler) Routes(r chi.Router) {
	r.Post("/", h.upload)
}

// POST /api/photos — not in architecture plan §9's table (the plan describes
// the storage/serving shape in §12 but never designs the upload endpoint
// itself). Accepts a single multipart "photo" field, stores it under a
// random opaque filename (never the client's own filename), and returns the
// URL to save as an item's or location's photo_url via the existing
// PATCH/POST routes — this endpoint only ever produces a URL, it doesn't
// touch the items/locations tables itself.
func (h *PhotosHandler) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		writeError(w, http.StatusBadRequest, "photo missing, malformed, or larger than 8MB")
		return
	}
	file, _, err := r.FormFile("photo")
	if err != nil {
		writeError(w, http.StatusBadRequest, `missing "photo" file field`)
		return
	}
	defer file.Close()

	// Sniff the real content type from the file's own leading bytes — see
	// allowedPhotoTypes above for why this must never come from the client.
	sniff := make([]byte, 512)
	n, err := io.ReadFull(file, sniff)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		serverError(w, r, err)
		return
	}
	sniff = sniff[:n]
	contentType := http.DetectContentType(sniff)
	ext, ok := allowedPhotoTypes[contentType]
	if !ok {
		writeError(w, http.StatusUnsupportedMediaType, fmt.Sprintf("unsupported image type %q", contentType))
		return
	}

	filename, err := randomFilename(ext)
	if err != nil {
		serverError(w, r, err)
		return
	}
	out, err := os.OpenFile(filepath.Join(h.uploadDir, filename), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		serverError(w, r, err)
		return
	}
	defer out.Close()

	if _, err := out.Write(sniff); err != nil {
		serverError(w, r, err)
		return
	}
	if _, err := io.Copy(out, file); err != nil {
		serverError(w, r, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"photo_url": "/uploads/" + filename})
}

// randomFilename generates an unguessable filename. Unlike
// internal/codegen.PlainTextCode (a short human-typed label code where a
// collision is a non-event to retry past), this filename is the only thing
// standing between an unauthenticated /uploads/ request and someone else's
// photo (architecture plan §12: "anyone with the exact URL can view a
// photo") — so it's crypto/rand, not math/rand, and 16 bytes of entropy
// makes a same-instant collision with an existing file astronomically
// unlikely rather than something worth a retry loop.
func randomFilename(ext string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b) + ext, nil
}

// uploadsFileServer serves uploaded photos as plain static files (§12) —
// simple and fast, but, per the same section, deliberately not gated by the
// sharing rules the rest of the API applies elsewhere; fine as long as the
// URLs never leave the app. X-Content-Type-Options: nosniff is a second
// layer alongside upload()'s own strict content-type allowlist — belt and
// suspenders against a browser trying to sniff something other than the
// image type already verified at upload time.
func uploadsFileServer(uploadDir string) http.Handler {
	fileServer := http.FileServer(http.Dir(uploadDir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		fileServer.ServeHTTP(w, r)
	})
}
