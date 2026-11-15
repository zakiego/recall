// Package web serves the dashboard SPA under /app (apps/app, built with Vite,
// base "/app/"). The build is embedded from dist/ at compile time; a
// placeholder index.html is committed so the module builds before the
// dashboard does. In development, Dir points at a build on disk instead.
//
// Hashed files under assets/ get a year of immutable caching. Every other
// file and index.html get no-cache, so a deploy shows up on the next load.
// Any /app path that isn't a file gets index.html (client-side routing).
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
)

//go:embed all:dist
var embedded embed.FS

// Embedded is the dist directory compiled into the binary.
func Embedded() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}

// Handler serves files from dir (a path on disk) or, when dir is "", from
// the embedded build. Mount it at /app/.
func Handler(dir string) http.Handler {
	var files fs.FS
	if dir != "" {
		files = os.DirFS(dir)
	} else {
		files = Embedded()
	}
	return &handler{files: files}
}

type handler struct{ files fs.FS }

const (
	immutable = "public, max-age=31536000, immutable"
	noCache   = "no-cache"
)

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("allow", "GET, HEAD")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rel := strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(r.URL.Path, "/app")), "/")
	if rel != "" && rel != "index.html" {
		if st, err := fs.Stat(h.files, rel); err == nil && !st.IsDir() {
			if strings.HasPrefix(rel, "assets/") {
				w.Header().Set("cache-control", immutable)
			} else {
				w.Header().Set("cache-control", noCache)
			}
			http.ServeFileFS(w, r, h.files, rel)
			return
		}
		// A missing hashed asset is a stale tab after a deploy: a 404 makes
		// the client reload instead of parsing index.html as a script.
		if strings.HasPrefix(rel, "assets/") {
			http.NotFound(w, r)
			return
		}
	}
	index, err := fs.ReadFile(h.files, "index.html")
	if err != nil {
		http.Error(w, "The dashboard isn't built.", http.StatusNotFound)
		return
	}
	w.Header().Set("content-type", "text/html; charset=utf-8")
	w.Header().Set("cache-control", noCache)
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(index)
	}
}
