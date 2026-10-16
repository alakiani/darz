// Package panel serves the user panel, a single-page app built from web/
// into dist/app and embedded in the Coordinator binary (ADR 0001).
package panel

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the panel: files by path, and index.html for every other
// path, so the app's own routes load it. Without a build it says so.
func Handler() http.Handler {
	files, _ := fs.Sub(dist, "dist/app")
	_, err := fs.Stat(files, "index.html")
	built := err == nil
	fileServer := http.FileServerFS(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// Only our own scripts and styles; QR codes are data: images.
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		if !built {
			http.Error(w, "the panel is not built: run npm run build in web/", http.StatusServiceUnavailable)
			return
		}
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" || p == "index.html" {
			serveIndex(w, r, files)
			return
		}
		if _, err := fs.Stat(files, p); err != nil {
			serveIndex(w, r, files)
			return
		}
		if strings.HasPrefix(p, "assets/") {
			h.Set("Cache-Control", "public, max-age=31536000, immutable") // names carry content hashes
		}
		fileServer.ServeHTTP(w, r)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, files fs.FS) {
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFileFS(w, r, files, "index.html")
}
