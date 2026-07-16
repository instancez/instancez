package http

import (
	"io"
	"io/fs"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// MountDashboard serves the embedded SPA assets at /dashboard/*.
// In dev mode with no embedded assets, it returns a placeholder page pointing to the Vite dev server.
//
// When mode is DashboardDisabled, no routes are registered, so requests to
// /dashboard fall through to gin's default 404 handler. Readonly and readwrite
// both mount the SPA identically — the read/write distinction is enforced at
// the API layer (config-mutation endpoints), not at the SPA mount.
func MountDashboard(r *gin.Engine, assets fs.FS, devMode bool, mode DashboardMode) {
	if mode == DashboardDisabled {
		return // routes not registered → gin returns 404 from its default handler
	}

	if assets == nil && devMode {
		// Dev mode: serve a redirect/placeholder to the Vite dev server
		r.GET("/dashboard", func(c *gin.Context) {
			c.Header("Content-Type", "text/html; charset=utf-8")
			c.String(200, devDashboardHTML)
		})
		r.GET("/dashboard/*path", func(c *gin.Context) {
			c.Header("Content-Type", "text/html; charset=utf-8")
			c.String(200, devDashboardHTML)
		})
		return
	}

	if assets == nil {
		return
	}

	fileServer := http.FileServer(http.FS(assets))

	// serveIndex writes dist/index.html directly rather than routing it
	// through fileServer — http.FileServer 301-redirects any request whose
	// path ends in "/index.html" to its parent directory, which would turn
	// the SPA fallback into a redirect to "/" and break the dashboard.
	serveIndex := func(c *gin.Context) {
		f, err := assets.Open("index.html")
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		defer func() { _ = f.Close() }()
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.Status(http.StatusOK)
		_, _ = io.Copy(c.Writer, f)
	}

	handler := func(c *gin.Context) {
		path := c.Request.URL.Path
		// Strip /dashboard prefix for asset lookup
		assetPath := strings.TrimPrefix(path, "/dashboard")
		if assetPath == "" || assetPath == "/" {
			serveIndex(c)
			return
		}

		// Try to serve the file directly
		trimmed := strings.TrimPrefix(assetPath, "/")
		if f, err := assets.Open(trimmed); err == nil {
			_ = f.Close()
			c.Request.URL.Path = assetPath
			fileServer.ServeHTTP(c.Writer, c.Request)
			c.Request.URL.Path = path // restore
			return
		}

		// SPA client-side route fallback — serve index.html.
		serveIndex(c)
	}

	r.GET("/dashboard", handler)
	r.GET("/dashboard/*path", handler)
}

const devDashboardHTML = `<!DOCTYPE html>
<html>
<head>
  <title>instancez Dashboard (Dev)</title>
  <meta charset="utf-8" />
  <meta http-equiv="refresh" content="0;url=http://localhost:5173" />
</head>
<body>
  <p>Redirecting to <a href="http://localhost:5173">Vite dev server</a>...</p>
</body>
</html>`
