package static

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed dist/* dist/assets/*
var files embed.FS

func Handler() http.Handler {
	sub, err := fs.Sub(files, "dist")
	if err != nil {
		return http.NotFoundHandler()
	}
	return spaHandler{fsys: sub}
}

type spaHandler struct {
	fsys fs.FS
}

func (h spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if file, err := h.fsys.Open(path); err == nil {
		_ = file.Close()
		http.FileServer(http.FS(h.fsys)).ServeHTTP(w, r)
		return
	}
	r.URL.Path = "/index.html"
	http.FileServer(http.FS(h.fsys)).ServeHTTP(w, r)
}
