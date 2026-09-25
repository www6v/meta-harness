package console

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// NewStaticHandler serves a Vite/React build with index.html SPA fallback.
func NewStaticHandler(root string) http.Handler {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		absRoot = root
	}
	indexPath := filepath.Join(absRoot, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}

		serveIndex := func() {
			// Always revalidate the SPA shell so deploys/rebuilds are not
			// stuck behind a cached index.html pointing at stale hashed assets.
			w.Header().Set("Cache-Control", "no-cache")
			// X-Deploy-Version lets the frontend detect when the server
			// has been rebuilt/restarted with new assets.  The background
			// deploy-check script in index.html fetches "/" with
			// cache-busting and compares this header to the DEPLOY version
			// baked into the HTML.  If they differ, the script forces a
			// full page reload — essential for browsers that ignore
			// Cache-Control headers (e.g. WeChat built-in browser).
			if info, err := os.Stat(indexPath); err == nil {
				w.Header().Set("X-Deploy-Version",
					fmt.Sprintf("%d", info.ModTime().Unix()))
			}
			http.ServeFile(w, r, indexPath)
		}

		rel := strings.TrimPrefix(r.URL.Path, "/")
		if rel == "" || rel == "." {
			serveIndex()
			return
		}

		clean := filepath.Clean(rel)
		if clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
			http.NotFound(w, r)
			return
		}

		full := filepath.Join(absRoot, clean)
		if !strings.HasPrefix(full, absRoot+string(os.PathSeparator)) && full != absRoot {
			http.NotFound(w, r)
			return
		}

		info, err := os.Stat(full)
		if err != nil || info.IsDir() {
			serveIndex()
			return
		}

		// Content-hashed Vite assets can be cached aggressively; the shell
		// (index.html) above stays no-cache so clients discover new hashes.
		// During development the hash may not change between rebuilds, so we
		// use a short max-age and omit "immutable" to let the browser
		// revalidate and pick up fresh content.
		if strings.HasPrefix(clean, "assets"+string(os.PathSeparator)) ||
			strings.HasPrefix(clean, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
		}
		http.ServeFile(w, r, full)
	})
}
