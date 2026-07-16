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
	devMode    bool
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

	DotenvWritable bool   // true → PUT /config/dotenv is enabled
	DotenvPath     string // path to the .env file to write

	FunctionRuntime domain.FunctionRuntime // nil → /functions/v1 returns 501
}

// NewServer creates a new HTTP server with all routes mounted.
func NewServer(deps ServerDeps) *Server {
	// ReleaseMode even in dev: DebugMode's only real payload is the ~140-line
	// route table it dumps to stderr at startup, which drowns the dev banner.
	// We run our own requestLogger, so gin's debug logging buys us nothing.
	// ponytail: if the route table is ever wanted back, flip this on --verbose.
	gin.SetMode(gin.ReleaseMode)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(requestIDMiddleware())
	r.Use(requestLogger(deps.Logger, deps.DevMode))

	s := &Server{
		engine:  r,
		cfg:     deps.Config,
		logger:  deps.Logger,
		db:      deps.DB.Database,
		devMode: deps.DevMode,
	}

	// Metrics middleware
	r.Use(metricsMiddleware())

	// CORS
	r.Use(corsMiddleware(deps.Config.Server.CORS, deps.DevMode))

	// Body size limit
	r.Use(bodySizeLimit(deps.Config.Server.MaxBodySize))

	// Health endpoints are unauthenticated (load balancer / orchestrator
	// probes). /metrics exposes request/latency internals, so it sits behind
	// the same admin key as /_admin/*.
	r.GET("/live", s.handleLive)
	r.GET("/health", s.handleHealth)
	r.GET("/ready", s.handleReady)
	r.GET("/metrics", adminKeyAuth(), handleMetrics)

	// Root router group — used by Supabase-compatible handlers that mount
	// on absolute paths like /auth/v1 and /rest/v1.
	root := r.Group("")

	// API group — retained for internal-only endpoints that don't need
	// supabase-js compatibility (admin dashboard, functions).
	api := r.Group("/api")

	// Auth endpoints at /auth/v1/* (GoTrue-compatible, consumed by supabase-js)
	if deps.Config.Auth != nil {
		authHandler := NewAuthHandler(deps)
		authHandler.Mount(root)
	}

	// Table CRUD at /rest/v1/* (PostgREST-compatible)
	crudHandler := NewCRUDHandler(deps)
	crudHandler.Mount(root)

	// Code functions at /functions/v1/:name — same JWT gate as /rest/v1.
	// FunctionRuntime may be nil (returns 501) so existing call sites that
	// do not set FunctionRuntime continue to compile and work unchanged.
	functionsV1 := root.Group("/functions/v1")
	functionsV1.Use(apiKeyGuard(deps.JWTKeys))
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

	// Admin endpoints — mounted on both /api and root so that the /_admin/*
	// paths survive the platform's Traefik middleware that strips the /api
	// prefix before forwarding to Lambda (Host && PathPrefix(`/api`) → strip).
	// The same handler set (including adminKeyAuth) is registered on both
	// groups; no handler logic is duplicated.
	adminHandler := NewAdminHandler(deps)
	adminHandler.Mount(api)  // /api/_admin/* — direct (non-proxied) access
	adminHandler.Mount(root) // /_admin/*    — proxy-stripped access

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

	// In dev the human banner already prints the API URL, so keep this to the
	// debug stream; in prod it's part of the single JSON startup record.
	if s.devMode {
		s.logger.Debug("HTTP server listening", "addr", s.httpServer.Addr)
	} else {
		s.logger.Info("HTTP server listening", "addr", s.httpServer.Addr)
	}
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
