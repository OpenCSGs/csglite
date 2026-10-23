package server

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"strings"
)

//go:embed all:static
var embeddedFS embed.FS

// staticHandler returns a handler that serves embedded static files,
// falling back to index.html for SPA routes.
func staticHandler() http.Handler {
	subFS, err := fs.Sub(embeddedFS, "static")
	if err != nil {
		panic("failed to create sub filesystem: " + err.Error())
	}
	fileServer := http.FileServer(http.FS(subFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}

		isHTML := path == "index.html" || !strings.Contains(path, ".")
		if isHTML {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		}

		// Try to open the file; if it exists, serve it
		if f, err := subFS.Open(path); err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}

		// SPA fallback: serve index.html for non-file paths, and for page
		// loads whose route merely contains a dot, such as the detail page of
		// a model named Qwen2-0.5B-Instruct-GGUF. Missing assets still 404.
		if isHTML || acceptsHTML(r) {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
			return
		}

		http.NotFound(w, r)
	})
}

// hasEmbeddedStatic checks if the embedded filesystem contains actual web assets.
func hasEmbeddedStatic() bool {
	f, err := embeddedFS.Open("static/index.html")
	if err != nil {
		return false
	}
	f.Close()
	return true
}

// devStaticHandler serves files from a local directory (for development).
func devStaticHandler(dir string) http.Handler {
	if _, err := os.Stat(dir); err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		})
	}
	fileServer := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		fullPath := dir + "/" + path
		if _, err := os.Stat(fullPath); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}
		// SPA fallback
		if !strings.Contains(path, ".") {
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})
}

// acceptsHTML reports whether the request is a browser page navigation.
func acceptsHTML(r *http.Request) bool {
	return r.Method == http.MethodGet && strings.Contains(r.Header.Get("Accept"), "text/html")
}
