// Package http implements the HTTP adapter (handlers, middleware, routing).
package http

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/config"
	"github.com/instancez/instancez/internal/domain"
)

// Server wraps the gin engine and HTTP server.
type Server struct {
	engine     *gin.Engine
	httpServer *http.Server
	cfg        *domain.Config
	logger     *slog.Logger
	db         domain.Database
}

// ServerDeps holds all dependencies for the HTTP server.
//
// DB must be a domain.RequestDB (the per-request authenticator pool). The
// distinct named type prevents callers from accidentally passing the
// privileged owner pool — that's a compile error.
type ServerDeps struct {
	Config          *domain.Config
	DB              domain.RequestDB
	OwnerDB         domain.OwnerDB // privileged pool used for migrations from the dashboard
	Logger          *slog.Logger
	DevMode         bool
	Email           domain.EmailSender
	Storage         domain.ObjectStore
	JWTKeys         *app.JWTKeyManager // signing/verification keys (managed in DB)
	ConfigPath      string             // path to instancez.yaml (for dashboard config editing)
	DashboardAssets fs.FS              // embedded SPA assets (nil in dev mode)

	DashboardMode  DashboardMode            // disabled | readonly | readwrite
	ConfigSource   config.Source            // for the watch + admin PUT
	DriftFn        func() *app.DriftTracker // engine drift state (live closure; nil before Start)
	ConfigFn       func() *domain.Config    // engine running config (lastGood when drifted)
	UpdateConfigFn func(*domain.Config)     // called after a successful PUT so GET reflects it immediately

	FunctionRuntime domain.FunctionRuntime // nil → /functions/v1 returns 501
}

// NewServer creates a new HTTP server with all routes mounted.
func NewServer(deps ServerDeps) *Server {
	if deps.DevMode {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(requestIDMiddleware())
	r.Use(requestLogger(deps.Logger))

	s := &Server{
		engine: r,
		cfg:    deps.Config,
		logger: deps.Logger,
		db:     deps.DB.Database,
	}

	// Metrics middleware
	r.Use(metricsMiddleware())

	// CORS
	r.Use(corsMiddleware(deps.Config.Server.CORS, deps.DevMode))

	// Body size limit
	r.Use(bodySizeLimit(deps.Config.Server.MaxBodySize))

	// Health + observability endpoints (no auth)
	r.GET("/live", s.handleLive)
	r.GET("/health", s.handleHealth)
	r.GET("/ready", s.handleReady)
	r.GET("/metrics", handleMetrics)

	// Root router group — used by Supabase-compatible handlers that mount
	// on absolute paths like /auth/v1 and /rest/v1.
	root := r.Group("")

	// API group — retained for internal-only endpoints that don't need
	// supabase-js compatibility (admin dashboard, functions, openapi, docs).
	api := r.Group("/api")

	// Auth endpoints at /auth/v1/* (GoTrue-compatible, consumed by supabase-js)
	if deps.Config.Auth != nil {
		authHandler := NewAuthHandler(deps)
		authHandler.Mount(root)
	}

	// OpenAPI + docs — mounted on root so proxy-stripped deployments reach
	// them at /openapi.json and /docs rather than /api/openapi.json and /api/docs.
	// Also keep the /api/* aliases for direct (non-proxied) access.
	r.GET("/openapi.json", s.handleOpenAPI)
	api.GET("/openapi.json", s.handleOpenAPI)
	if s.docsEnabled(deps.DevMode) {
		r.GET("/docs", s.handleDocs)
		api.GET("/docs", s.handleDocs)
	}

	// Table CRUD at /rest/v1/* (PostgREST-compatible)
	crudHandler := NewCRUDHandler(deps)
	crudHandler.Mount(root)

	// Code functions at /functions/v1/:name — same JWT gate as /rest/v1.
	// FunctionRuntime may be nil (returns 501) so existing call sites that
	// do not set FunctionRuntime continue to compile and work unchanged.
	functionsV1 := root.Group("/functions/v1")
	functionsV1.Use(jwtAuth(deps.JWTKeys, false))
	NewFunctionsHandler(deps.FunctionRuntime).Mount(functionsV1)

	// Storage endpoints — supabase-js compatible at /storage/v1/*,
	// plus serverless-friendly presigned URL endpoints at /api/storage/*.
	if len(deps.Config.Storage) > 0 && deps.Storage != nil {
		storageV1 := NewStorageV1Handler(deps)
		storageV1.Mount(root)

		storageHandler := NewStorageHandler(deps)
		storageHandler.Mount(api)
	}

	// Admin endpoints
	adminHandler := NewAdminHandler(deps)
	adminHandler.Mount(api)

	// Dashboard SPA
	MountDashboard(r, deps.DashboardAssets, deps.DevMode, deps.DashboardMode)

	return s
}

// Handler returns the underlying gin engine as an http.Handler so callers
// (typically tests) can serve it with httptest.NewServer.
func (s *Server) Handler() http.Handler {
	return s.engine
}

// buildHTTPServer constructs the net/http server with connection timeouts.
// IdleTimeout must stay below 350s: instancez deploys behind L4 load
// balancers (AWS NLB) that silently expire idle flows at 350s, and the
// server closing first avoids client-visible RSTs on keepalive reuse.
// ReadHeaderTimeout bounds slowloris-style partial-header holds, which an
// L4 balancer passes straight through.
func buildHTTPServer(port int, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           handler,
		IdleTimeout:       300 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

// Start begins listening. Blocks until the server is stopped.
func (s *Server) Start() error {
	s.httpServer = buildHTTPServer(s.cfg.Server.Port, s.engine)

	s.logger.Info("HTTP server listening", "addr", s.httpServer.Addr)
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("http server: %w", err)
	}
	return nil
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer == nil {
		return nil
	}
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) docsEnabled(devMode bool) bool {
	if s.cfg.Server.DocsUI != nil {
		return *s.cfg.Server.DocsUI
	}
	return devMode
}

func (s *Server) handleLive(c *gin.Context) {
	c.JSON(200, gin.H{"status": "ok"})
}

func (s *Server) handleHealth(c *gin.Context) {
	c.JSON(200, gin.H{"status": "ok"})
}

func (s *Server) handleReady(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	if err := s.db.Ping(ctx); err != nil {
		c.JSON(503, gin.H{"status": "unavailable", "detail": "database not reachable"})
		return
	}
	c.JSON(200, gin.H{"status": "ok"})
}

func (s *Server) handleOpenAPI(c *gin.Context) {
	spec := GenerateOpenAPI(s.cfg)
	c.JSON(200, spec)
}

func (s *Server) handleDocs(c *gin.Context) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	// Reference openapi.json with a path *relative* to the docs page so the
	// browser resolves it against the public URL it loaded, not the path the
	// backend sees. This is what makes it survive a prefix-stripping proxy:
	// when docs is published at /api/xyz/docs but the ingress forwards /docs
	// to us, the browser resolves "openapi.json" to /api/xyz/openapi.json
	// (correct) whereas a root-absolute /openapi.json would 404. For the
	// direct mounts it still resolves correctly: /docs -> /openapi.json and
	// /api/docs -> /api/openapi.json.
	c.String(200, scalarDocsHTML(s.cfg.Project.Name, "openapi.json"))
}

func scalarDocsHTML(title, openAPIURL string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
  <title>%s - API Docs</title>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
</head>
<body>
  <script id="api-reference" data-url="%s"></script>
  <script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference"></script>
</body>
</html>`, title, openAPIURL)
}
