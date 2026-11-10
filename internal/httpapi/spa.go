package httpapi

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/aluo96078/shinsou_sync_service/internal/apierr"
)

// spaFallback hands GET and HEAD requests the API does not claim to the front end. It asks the mux
// first, so every API route keeps its own 404/405 behaviour (including the JSON 404 for unknown
// /api/ paths) and only genuinely unmatched, non-reserved paths reach the single-page app.
func spaFallback(mux *http.ServeMux, spa http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.Method == http.MethodGet || r.Method == http.MethodHead) && !reservedPath(r.URL.Path) {
			if _, pattern := mux.Handler(r); pattern == "" {
				spa.ServeHTTP(w, r)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

// reservedPath is what the front end never answers for.
func reservedPath(p string) bool {
	return strings.HasPrefix(p, "/api/") || p == "/api" ||
		strings.HasPrefix(p, "/.well-known/") || p == "/healthz"
}

const contentSecurityPolicy = "default-src 'self'; img-src 'self' https: data:; style-src 'self' 'unsafe-inline'; " +
	"connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"

// newSPA serves a built single-page app from fsys: an existing file as is, any other path as
// index.html so the client-side router can take over. Missing files under /assets/ stay 404,
// otherwise a stale script URL would answer with HTML.
func newSPA(fsys fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")

		if _, err := fs.Stat(fsys, "index.html"); err != nil {
			writeAPIError(w, apierr.NotFound("this server was built without the web front end"))
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" || !fs.ValidPath(name) {
			name = "index.html"
		}
		if info, err := fs.Stat(fsys, name); err != nil || info.IsDir() {
			if strings.HasPrefix(name, "assets/") {
				writeAPIError(w, apierr.NotFound("no such file"))
				return
			}
			name = "index.html"
		}
		if strings.HasPrefix(name, "assets/") {
			h.Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			h.Set("Cache-Control", "no-cache")
		}
		serveFile(w, r, fsys, name)
	})
}

func serveFile(w http.ResponseWriter, r *http.Request, fsys fs.FS, name string) {
	f, err := fsys.Open(name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeAPIError(w, apierr.NotFound("no such file"))
			return
		}
		writeAPIError(w, apierr.Internal())
		return
	}
	defer f.Close()
	rs, ok := f.(io.ReadSeeker)
	if !ok {
		b, err := io.ReadAll(f)
		if err != nil {
			writeAPIError(w, apierr.Internal())
			return
		}
		rs = bytes.NewReader(b)
	}
	// A zero modification time leaves out Last-Modified; the embedded files have none.
	http.ServeContent(w, r, name, time.Time{}, rs)
}
