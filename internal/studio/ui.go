package studio

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// ui serves the built browser app: a static export, so every route is a file,
// with index.html for any path that is not one (the app routes client-side).
// Without a build it serves one page saying how to make one, rather than a
// 404 that looks like a broken server.
func (s *Server) ui() http.Handler {
	if s.UI == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(placeholder))
		})
	}
	files := http.FileServerFS(s.UI)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(s.UI, p); err != nil {
			// A trailing-slash export writes route/index.html.
			if _, err := fs.Stat(s.UI, path.Join(p, "index.html")); err != nil {
				r = r.Clone(r.Context())
				r.URL.Path = "/"
			}
		}
		files.ServeHTTP(w, r)
	})
}

const placeholder = `<!doctype html><meta charset="utf-8"><title>Studio</title>
<body style="font:15px/1.5 system-ui;margin:3rem;max-width:40rem">
<h1>Studio's UI is not built into this binary</h1>
<p>The API is up. Build the UI with <code>make studio</code> (needs Node 20+),
then rebuild <code>sandbox-cli</code>; a release build includes it.</p>`
