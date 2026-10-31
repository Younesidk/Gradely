package server

import (
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/younesidk/gradely/internal/web"
)

// staticHandler serves the built Svelte app. Unknown paths fall back to index.html so client-side routes work.
// Files are written directly (not through http.FileServer, which redirects /index.html to ./).
func staticHandler() gin.HandlerFunc {
	files := web.FS()
	return func(c *gin.Context) {
		p := c.Request.URL.Path
		if strings.HasPrefix(p, "/api/") || p == "/api" {
			c.JSON(http.StatusNotFound, gin.H{"error": "Not found."})
			return
		}
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.Status(http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+p), "/")
		if name == "" {
			name = "index.html"
		}
		if st, err := fs.Stat(files, name); err != nil || st.IsDir() {
			if path.Ext(name) != "" { // a missing asset is a real 404, not the app shell
				c.Status(http.StatusNotFound)
				return
			}
			name = "index.html"
		}
		data, err := fs.ReadFile(files, name)
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		if strings.HasPrefix(name, "assets/") {
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			c.Header("Cache-Control", "no-cache")
		}
		contentType := mime.TypeByExtension(path.Ext(name))
		if contentType == "" {
			contentType = http.DetectContentType(data)
		}
		c.Data(http.StatusOK, contentType, data)
	}
}
