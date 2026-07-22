package webui

import (
	"bytes"
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed all:dist
var embedded embed.FS

type handler struct {
	files fs.FS
	index []byte
}

var timeZero time.Time

func NewHandler() http.Handler {
	files, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic("web UI assets are unavailable: " + err.Error())
	}
	index, err := fs.ReadFile(files, "index.html")
	if err != nil {
		panic("web UI index is unavailable: " + err.Error())
	}
	return handler{files: files, index: index}
}

func (h handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name != "" {
		if data, err := fs.ReadFile(h.files, name); err == nil {
			if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
				w.Header().Set("Content-Type", contentType)
			}
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			http.ServeContent(w, r, path.Base(name), timeZero, bytes.NewReader(data))
			return
		}
		if path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "index.html", timeZero, bytes.NewReader(h.index))
}
