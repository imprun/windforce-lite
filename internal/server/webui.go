package server

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/imprun/windforce-lite/internal/webui"
)

var webUIAssets = http.FileServer(http.FS(mustWebUIAssets()))

func mustWebUIAssets() fs.FS {
	assets, err := fs.Sub(webui.FS, "assets")
	if err != nil {
		panic(err)
	}
	return assets
}

func (h *Handler) handleWebUI(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	switch r.URL.Path {
	case "/":
		http.Redirect(w, r, "/ui/", http.StatusFound)
		return true
	case "/ui":
		http.Redirect(w, r, "/ui/", http.StatusMovedPermanently)
		return true
	}
	if !strings.HasPrefix(r.URL.Path, "/ui/") {
		return false
	}
	http.StripPrefix("/ui/", webUIAssets).ServeHTTP(w, r)
	return true
}
