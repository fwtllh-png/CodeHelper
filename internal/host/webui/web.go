// Package web serves the embedded CodeHelper control page over the Runtime API.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed static/*
var staticFS embed.FS

// Handler serves /ui/ assets. API calls remain on the Runtime HTTP handler.
func Handler() (http.Handler, error) {
	subtree, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, err
	}
	fileServer := http.FileServer(http.FS(subtree))
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		path := strings.TrimPrefix(request.URL.Path, "/ui")
		path = strings.TrimPrefix(path, "/")
		if path == "" || path == "index.html" {
			data, err := staticFS.ReadFile("static/index.html")
			if err != nil {
				http.Error(writer, "index missing", http.StatusInternalServerError)
				return
			}
			writer.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = writer.Write(data)
			return
		}
		request.URL.Path = "/" + path
		fileServer.ServeHTTP(writer, request)
	}), nil
}

// Mount wraps an existing Runtime API handler so /ui/* is served from embed.FS
// while all other routes stay on the Runtime API.
func Mount(api http.Handler) (http.Handler, error) {
	ui, err := Handler()
	if err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/ui") {
			ui.ServeHTTP(writer, request)
			return
		}
		api.ServeHTTP(writer, request)
	}), nil
}
