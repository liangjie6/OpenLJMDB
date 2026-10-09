package backend

import (
	"io/fs"
	"net/http"
	"strings"
)

func (a *App) staticOrNotFound(w http.ResponseWriter, r *http.Request) {
	if a.FrontendFS == nil || strings.HasPrefix(r.URL.Path, "/api/") || (r.Method != "GET" && r.Method != "HEAD") {
		WriteError(w, r, Err(404, "NOT_FOUND", "接口不存在", nil))
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "" {
		name = "index.html"
	}
	if !fs.ValidPath(name) {
		WriteError(w, r, Err(404, "NOT_FOUND", "资源不存在", nil))
		return
	}
	if info, err := fs.Stat(a.FrontendFS, name); err == nil && !info.IsDir() {
		http.FileServerFS(a.FrontendFS).ServeHTTP(w, r)
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "text/html") || strings.HasPrefix(r.URL.Path, "/documents/") {
		index, err := fs.ReadFile(a.FrontendFS, "index.html")
		if err == nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(200)
			if r.Method != "HEAD" {
				w.Write(index)
			}
			return
		}
	}
	WriteError(w, r, Err(404, "NOT_FOUND", "资源不存在", nil))
}
