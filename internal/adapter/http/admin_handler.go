package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/config"
	"github.com/instancez/instancez/internal/domain"
	"gopkg.in/yaml.v3"
)

// AdminHandler serves /api/_admin/* endpoints.
type AdminHandler struct {
	cfg            *domain.Config
	configFn       func() *domain.Config // returns the LIVE engine config (lastGood when drifted)
	updateConfigFn func(*domain.Config)  // updates engine config immediately after PUT
	db             domain.Database
	ownerDB        domain.OwnerDB // privileged pool for migrations (nil in tests that don't wire it)
	logger         *slog.Logger
	configSource   config.Source
	configPath     string // path to instancez.yaml; used to derive functions dir
	dashboardMode  DashboardMode
	driftFn        func() *app.DriftTracker
	jwtKeys        *app.JWTKeyManager
}

func NewAdminHandler(deps ServerDeps) *AdminHandler {
	return &AdminHandler{
		cfg:            deps.Config,
		configFn:       deps.ConfigFn,
		updateConfigFn: deps.UpdateConfigFn,
		db:             deps.DB.Database,
		ownerDB:        deps.OwnerDB,
		logger:         deps.Logger,
		configSource:   deps.ConfigSource,
		configPath:     deps.ConfigPath,
		dashboardMode:  deps.DashboardMode,
		driftFn:        deps.DriftFn,
		jwtKeys:        deps.JWTKeys,
	}
}

// migrationDB returns the privileged owner DB when available, falling back to
// the request DB for test paths that don't wire an owner pool.
func (h *AdminHandler) migrationDB() domain.Database {
	if h.ownerDB.Database != nil {
		return h.ownerDB.Database
	}
	return h.db
}

// liveConfig returns the engine's current running config. Falls back to the
// boot-time cfg if no closure was supplied (test paths).
func (h *AdminHandler) liveConfig() *domain.Config {
	if h.configFn != nil {
		if c := h.configFn(); c != nil {
			return c
		}
	}
	return h.cfg
}

func (h *AdminHandler) sourceDescribe() string {
	if h.configSource == nil {
		return ""
	}
	return h.configSource.Describe()
}

func (h *AdminHandler) Mount(api *gin.RouterGroup) {
	admin := api.Group("/_admin")
	admin.Use(adminKeyAuth())

	// Migrations
	admin.GET("/migrations", h.handleListMigrations)

	// Users
	admin.GET("/users", h.handleListUsers)
	admin.POST("/users/:id/disable", h.handleDisableUser)
	admin.POST("/users/:id/reset-password", h.handleAdminResetPassword)

	// Status
	admin.GET("/status", h.handleStatus)
	admin.GET("/schema", h.handleSchema)

	// Config (dashboard)
	admin.GET("/config", h.handleGetConfig)
	admin.PUT("/config", h.handlePutConfig)
	admin.GET("/config/status", h.handleConfigStatus)
	admin.GET("/config/diff", h.handleConfigDiff)
	admin.GET("/stats", h.handleStats)

	// API keys (dashboard Settings → API equivalent). The admin key itself is
	// never echoed back — the dashboard already holds it from login.
	admin.GET("/keys", h.handleKeys)

	// Function code (dev / readwrite mode only)
	admin.GET("/functions/:name/code", h.handleGetFunctionCode)
	admin.PUT("/functions/:name/code", h.handlePutFunctionCode)

	// npm dependencies (shared across all functions)
	admin.GET("/functions/deps", h.handleGetFunctionDeps)
	admin.POST("/functions/deps", h.handlePostFunctionDeps)
}

// handleKeys returns the project's publishable anon key. The token is
// deterministic for the active signing key (see app.MintStableAnonKey), so
// the dashboard can present it as a stable value like Supabase's anon key.
func (h *AdminHandler) handleKeys(c *gin.Context) {
	if h.jwtKeys == nil {
		c.JSON(501, gin.H{"error": "not_implemented", "message": "JWT key manager not configured"})
		return
	}
	anonKey, err := app.MintStableAnonKey(c.Request.Context(), h.jwtKeys)
	if err != nil {
		h.logger.Error("mint anon key", "error", err)
		c.JSON(500, gin.H{"error": "internal", "message": "failed to mint anon key"})
		return
	}
	c.JSON(200, gin.H{"anon_key": anonKey})
}

func (h *AdminHandler) handleListMigrations(c *gin.Context) {
	ctx := c.Request.Context()
	rows, err := h.db.Query(ctx,
		"SELECT id, checksum, applied_at FROM _instancez_migrations ORDER BY id DESC LIMIT 100")
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to query migrations")
		return
	}
	c.JSON(200, rows)
}

func (h *AdminHandler) handleListUsers(c *gin.Context) {
	ctx := c.Request.Context()
	rows, err := h.db.Query(ctx,
		"SELECT id, email, email_verified, created_at FROM auth.users ORDER BY id LIMIT 100")
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to query users")
		return
	}
	c.JSON(200, rows)
}

func (h *AdminHandler) handleDisableUser(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()

	// Delete refresh tokens to force logout
	if _, err := h.db.Exec(ctx, "DELETE FROM auth.refresh_tokens WHERE user_id = $1", id); err != nil {
		c.JSON(500, gin.H{"error": "internal", "message": "Failed to revoke sessions"})
		return
	}

	c.JSON(200, gin.H{"message": "User disabled", "user_id": id})
}

func (h *AdminHandler) handleAdminResetPassword(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()

	// Generate a temporary token
	token := generateRandomToken()
	if _, err := h.db.Exec(ctx,
		"INSERT INTO auth.one_time_tokens (user_id, token, expires_at) VALUES ($1, $2, NOW() + INTERVAL '24 hours')",
		id, token); err != nil {
		c.JSON(500, gin.H{"error": "internal", "message": "Failed to initiate password reset"})
		return
	}

	c.JSON(200, gin.H{"message": "Password reset initiated", "token": token})
}

func (h *AdminHandler) handleStatus(c *gin.Context) {
	ctx := c.Request.Context()

	// DB pool stats
	status := gin.H{
		"status":  "ok",
		"tables":  len(h.cfg.Tables),
		"storage": len(h.cfg.Storage),
	}

	// Check DB connectivity
	if err := h.db.Ping(ctx); err != nil {
		status["database"] = "unavailable"
	} else {
		status["database"] = "connected"
	}

	c.JSON(200, status)
}

func (h *AdminHandler) handleSchema(c *gin.Context) {
	// Return the current config as a schema snapshot (without secrets)
	schema := gin.H{
		"version":    h.cfg.Version,
		"project":    h.cfg.Project,
		"extensions": h.cfg.Extensions,
		"tables":     h.cfg.Tables,
		"storage":    h.cfg.Storage,
	}
	c.JSON(200, schema)
}

// handleGetConfig returns the full parsed config as JSON. The shape mirrors
// the running engine config (drift-aware via configFn) and includes a
// `_checksum` field carrying the source's current version token, which clients
// can echo back on PUT via `If-Match` for optimistic concurrency.
func (h *AdminHandler) handleGetConfig(c *gin.Context) {
	// Read raw bytes from source so ${VAR} refs are preserved — secret values
	// must never transit the dashboard API.
	var result map[string]any
	if h.configSource != nil {
		raw, ver, err := h.configSource.Read(c.Request.Context())
		if err != nil {
			problemJSON(c, 500, "internal", "Failed to read config source: "+err.Error())
			return
		}
		cfg, err := config.ParseBytesRaw(raw, h.sourceDescribe())
		if err != nil {
			problemJSON(c, 500, "internal", "Failed to parse config: "+err.Error())
			return
		}
		jsonData, err := json.Marshal(cfg)
		if err != nil {
			problemJSON(c, 500, "internal", "Failed to serialize config")
			return
		}
		if err := json.Unmarshal(jsonData, &result); err != nil {
			problemJSON(c, 500, "internal", "Failed to round-trip config")
			return
		}
		result["_checksum"] = ver
	} else {
		// Test path: no source wired, fall back to live config (already resolved).
		cfg := h.liveConfig()
		jsonData, err := json.Marshal(cfg)
		if err != nil {
			problemJSON(c, 500, "internal", "Failed to serialize config")
			return
		}
		if err := json.Unmarshal(jsonData, &result); err != nil {
			problemJSON(c, 500, "internal", "Failed to round-trip config")
			return
		}
	}
	c.JSON(200, result)
}

// handlePutConfig validates and writes config through the configured Source.
// Behavior is gated on the dashboard mode: disabled returns 403
// "dashboard_disabled", readonly returns 403 "dashboard_readonly", and
// readwrite runs the migration first and only writes the YAML to the source
// after the migration commits. Optimistic concurrency uses the source's
// version token via the `If-Match` request header.
func (h *AdminHandler) handlePutConfig(c *gin.Context) {
	switch h.dashboardMode {
	case DashboardDisabled:
		c.JSON(403, gin.H{
			"error":         "dashboard_disabled",
			"message":       "The dashboard is disabled. To change the configuration, update the source and restart.",
			"config_source": h.sourceDescribe(),
		})
		return
	case DashboardReadonly:
		c.JSON(403, gin.H{
			"error":         "dashboard_readonly",
			"message":       "This deployment is GitOps-managed. To change the configuration, update the source and redeploy.",
			"config_source": h.sourceDescribe(),
		})
		return
	}

	if h.configSource == nil {
		problemJSON(c, 501, "not_implemented", "Config source not available")
		return
	}

	// Read current version (for optimistic concurrency). The bytes are
	// discarded — clients echo `_checksum` (the source's version token) on
	// PUT via `If-Match`; the source's own ETag/mtime is the only token we
	// surface.
	_, currentVersion, err := h.configSource.Read(c.Request.Context())
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to read current config: "+err.Error())
		return
	}

	// If-Match check (optional — clients can send the version they're editing).
	if ifMatch := c.GetHeader("If-Match"); ifMatch != "" && ifMatch != currentVersion {
		c.JSON(409, gin.H{
			"error":           "conflict",
			"current_version": currentVersion,
		})
		return
	}

	// Parse + validate the proposed config.
	var newCfg domain.Config
	if err := c.ShouldBindJSON(&newCfg); err != nil {
		problemJSON(c, 400, "invalid_body", "Invalid JSON body")
		return
	}
	if errs := config.Validate(&newCfg); errs != nil {
		var errList []gin.H
		for _, e := range errs {
			item := gin.H{"path": e.Path, "message": e.Message}
			if e.Suggestion != "" {
				item["suggestion"] = e.Suggestion
			}
			errList = append(errList, item)
		}
		c.JSON(400, gin.H{"errors": errList})
		return
	}

	// Migration first: run via the migrator. If it fails, leave the backend
	// untouched. Migrator failures are infrastructure errors at this point —
	// user-fixable validation already ran above — so surface as 500.
	migrator := app.NewMigrator(h.migrationDB())
	if err := migrator.Apply(c.Request.Context(), &newCfg); err != nil {
		h.logger.Error("migration failed",
			"source", h.sourceDescribe(),
			"error", err.Error())
		problemJSON(c, 500, "migration_failed", err.Error())
		return
	}

	// Migration committed; now write the YAML to the backend.
	yamlData, err := yaml.Marshal(&newCfg)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to serialize config to YAML")
		return
	}
	newVersion, err := h.configSource.Write(c.Request.Context(), yamlData, currentVersion)
	if err != nil {
		if errors.Is(err, config.ErrConfigVersionMismatch) {
			// DB has been migrated; source advanced concurrently between our
			// Read above and this Write. Surface as 409 so the dashboard can
			// show the conflict UI. The DB is now at the new schema; the
			// source still reflects whatever the concurrent writer landed.
			h.logger.Warn("config source advanced during migration; DB migrated, source not written",
				"source", h.sourceDescribe(),
				"expected_version", currentVersion)
			c.JSON(409, gin.H{
				"error":            "source_advanced_during_migration",
				"message":          "Source was modified during migration. DB has been migrated; please re-fetch and re-submit.",
				"expected_version": currentVersion,
				"db_migrated":      true,
			})
			return
		}
		// The DB has been migrated but the source write failed for some other
		// reason. Log loudly; the source still reflects the old version. The
		// next boot will re-read the (still old) source and attempt to diff
		// against last_migration.config_json (which is now the new config).
		// Operator must reconcile by exporting the new config to the source
		// manually before next boot.
		h.logger.Error("config source write failed after successful migration",
			"source", h.sourceDescribe(),
			"error", err.Error())
		c.JSON(500, gin.H{
			"error":         "source_write_failed",
			"message":       "Migration committed but source write failed. The DB is at the new schema; the source is still at the old version. Export and reconcile manually before the next deploy.",
			"config_source": h.sourceDescribe(),
			"db_migrated":   true,
			"detail":        err.Error(),
		})
		return
	}

	// Re-parse the written YAML with env var resolution so the live runtime
	// config reflects the change immediately.
	if h.updateConfigFn != nil {
		if resolved, err := config.ParseBytesLenient(yamlData, h.sourceDescribe()); err == nil {
			h.updateConfigFn(resolved)
		}
	}

	c.JSON(200, gin.H{
		"message":       "Config saved",
		"config_source": h.sourceDescribe(),
		"new_version":   newVersion,
	})
}

// handleConfigDiff returns DDL migration diff for current config.
func (h *AdminHandler) handleConfigDiff(c *gin.Context) {
	ctx := c.Request.Context()

	migrator := app.NewMigrator(h.migrationDB())
	sql, err := migrator.Plan(ctx, nil, h.cfg)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to generate migration diff")
		return
	}

	var statements []string
	if sql != "" {
		for _, stmt := range splitStatements(sql) {
			stmt = trimSpace(stmt)
			if stmt != "" {
				statements = append(statements, stmt)
			}
		}
	}

	if statements == nil {
		statements = []string{}
	}

	// Check for destructive operations
	isDestructive := false
	for _, stmt := range statements {
		if containsDestructive(stmt) {
			isDestructive = true
			break
		}
	}

	c.JSON(200, gin.H{
		"statements":     statements,
		"is_destructive": isDestructive,
	})
}

// handleStats returns aggregate stats for the overview page.
func (h *AdminHandler) handleStats(c *gin.Context) {
	ctx := c.Request.Context()

	result := gin.H{}

	// Table row counts
	tables := gin.H{}
	for name := range h.cfg.Tables {
		rows, err := h.db.QueryRow(ctx,
			fmt.Sprintf("SELECT reltuples::BIGINT AS count FROM pg_class WHERE relname = '%s'", name))
		if err == nil && rows != nil {
			tables[name] = gin.H{"row_count": rows["count"]}
		} else {
			tables[name] = gin.H{"row_count": 0}
		}
	}
	result["tables"] = tables

	// Storage stats
	storage := gin.H{}
	if len(h.cfg.Storage) > 0 {
		for name := range h.cfg.Storage {
			row, err := h.db.QueryRow(ctx,
				`SELECT COUNT(*)::INTEGER AS object_count, COALESCE(SUM(size), 0)::BIGINT AS total_bytes
				 FROM storage.objects WHERE bucket_id = $1`, name)
			if err == nil && row != nil {
				storage[name] = gin.H{
					"object_count": row["object_count"],
					"total_bytes":  row["total_bytes"],
				}
			} else {
				storage[name] = gin.H{"object_count": 0, "total_bytes": 0}
			}
		}
	}
	result["storage"] = storage

	c.JSON(200, result)
}

// handleGetFunctionCode reads the source file for a declared code function.
func (h *AdminHandler) handleGetFunctionCode(c *gin.Context) {
	name := c.Param("name")
	cfg := h.liveConfig()
	fn, ok := cfg.Functions[name]
	if !ok {
		problemJSON(c, 404, "not_found", "Function not found")
		return
	}
	if h.configPath == "" {
		problemJSON(c, 501, "not_implemented", "Config path not available")
		return
	}
	absPath := filepath.Join(filepath.Dir(h.configPath), fn.File)
	data, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Return empty content so the editor can create the file.
			c.JSON(200, gin.H{"content": "", "file": fn.File})
			return
		}
		problemJSON(c, 500, "internal", "Failed to read function file")
		return
	}
	c.JSON(200, gin.H{"content": string(data), "file": fn.File})
}

// handlePutFunctionCode writes the source file for a declared code function.
// Triggers the functions-dir file watcher which hot-reloads the workers.
func (h *AdminHandler) handlePutFunctionCode(c *gin.Context) {
	if h.dashboardMode != DashboardReadwrite {
		c.JSON(403, gin.H{
			"error":   "dashboard_readonly",
			"message": "Function code editing requires readwrite dashboard mode.",
		})
		return
	}
	name := c.Param("name")
	cfg := h.liveConfig()
	fn, ok := cfg.Functions[name]
	if !ok {
		problemJSON(c, 404, "not_found", "Function not found")
		return
	}
	if h.configPath == "" {
		problemJSON(c, 501, "not_implemented", "Config path not available")
		return
	}

	var body struct {
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		problemJSON(c, 400, "invalid_body", "Expected {\"content\": \"...\"}")
		return
	}

	absPath := filepath.Join(filepath.Dir(h.configPath), fn.File)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		problemJSON(c, 500, "internal", "Failed to create function directory")
		return
	}
	if err := os.WriteFile(absPath, []byte(body.Content), 0o644); err != nil {
		problemJSON(c, 500, "internal", "Failed to write function file")
		return
	}
	c.JSON(200, gin.H{"message": "Function code saved", "file": fn.File})
}

// handleGetFunctionDeps returns npm dependencies from functions/package.json.
// Works in all dashboard modes — writes are gated in handlePostFunctionDeps.
func (h *AdminHandler) handleGetFunctionDeps(c *gin.Context) {
	if h.configPath == "" {
		problemJSON(c, 501, "not_implemented", "Config path not available")
		return
	}
	functionsDir := filepath.Join(filepath.Dir(h.configPath), "functions")
	pkgPath := filepath.Join(functionsDir, "package.json")

	readonly := h.dashboardMode != DashboardReadwrite

	data, err := os.ReadFile(pkgPath)
	if os.IsNotExist(err) {
		c.JSON(200, gin.H{"dependencies": map[string]any{}, "has_lock": false, "readonly": readonly})
		return
	}
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to read package.json")
		return
	}

	var pkg map[string]any
	if err := json.Unmarshal(data, &pkg); err != nil {
		problemJSON(c, 500, "internal", "Failed to parse package.json")
		return
	}

	deps, _ := pkg["dependencies"].(map[string]any)
	if deps == nil {
		deps = map[string]any{}
	}

	_, lockErr := os.Stat(filepath.Join(functionsDir, "package-lock.json"))
	c.JSON(200, gin.H{
		"dependencies": deps,
		"has_lock":     lockErr == nil,
		"readonly":     readonly,
	})
}

// handlePostFunctionDeps installs or removes npm packages via the npm CLI.
// Requires readwrite dashboard mode. Runs npm in a background context so a
// client disconnect does not kill a long-running install.
func (h *AdminHandler) handlePostFunctionDeps(c *gin.Context) {
	if h.dashboardMode != DashboardReadwrite {
		c.JSON(403, gin.H{"error": "dashboard_readonly", "message": "Requires readwrite dashboard mode."})
		return
	}
	if h.configPath == "" {
		problemJSON(c, 501, "not_implemented", "Config path not available")
		return
	}

	var body struct {
		Add    []string `json:"add"`
		Remove []string `json:"remove"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		problemJSON(c, 400, "invalid_body", "Expected {add?: string[], remove?: string[]}")
		return
	}
	if len(body.Add) == 0 && len(body.Remove) == 0 {
		problemJSON(c, 400, "invalid_body", "Provide at least one package in add or remove")
		return
	}

	functionsDir := filepath.Join(filepath.Dir(h.configPath), "functions")
	if err := os.MkdirAll(functionsDir, 0o755); err != nil {
		problemJSON(c, 500, "internal", "Failed to create functions directory")
		return
	}

	pkgPath := filepath.Join(functionsDir, "package.json")
	if _, err := os.Stat(pkgPath); os.IsNotExist(err) {
		init := []byte(`{"name":"functions","version":"1.0.0","dependencies":{}}` + "\n")
		if err := os.WriteFile(pkgPath, init, 0o644); err != nil {
			problemJSON(c, 500, "internal", "Failed to create package.json")
			return
		}
	}

	// Use a background context so the install is not aborted if the HTTP
	// client disconnects mid-flight (npm install can take tens of seconds).
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	if len(body.Remove) > 0 {
		args := append([]string{"uninstall"}, body.Remove...)
		cmd := exec.CommandContext(ctx, "npm", args...)
		cmd.Dir = functionsDir
		if out, err := cmd.CombinedOutput(); err != nil {
			c.JSON(500, gin.H{"error": "npm_error", "message": "npm uninstall failed", "detail": string(out)})
			return
		}
	}

	if len(body.Add) > 0 {
		args := append([]string{"install"}, body.Add...)
		cmd := exec.CommandContext(ctx, "npm", args...)
		cmd.Dir = functionsDir
		if out, err := cmd.CombinedOutput(); err != nil {
			c.JSON(500, gin.H{"error": "npm_error", "message": "npm install failed", "detail": string(out)})
			return
		}
	}

	// Return the updated state so the UI can refresh without a separate GET.
	data, err := os.ReadFile(pkgPath)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to read updated package.json")
		return
	}
	var pkg map[string]any
	if err := json.Unmarshal(data, &pkg); err != nil {
		problemJSON(c, 500, "internal", "Failed to parse updated package.json")
		return
	}
	deps, _ := pkg["dependencies"].(map[string]any)
	if deps == nil {
		deps = map[string]any{}
	}
	_, lockErr := os.Stat(filepath.Join(functionsDir, "package-lock.json"))
	c.JSON(200, gin.H{
		"dependencies": deps,
		"has_lock":     lockErr == nil,
		"readonly":     false,
	})
}

// splitStatements splits SQL on semicolons (simple split, not a full parser).
func splitStatements(sql string) []string {
	var stmts []string
	for _, s := range splitOnSemicolon(sql) {
		s = trimSpace(s)
		if s != "" {
			stmts = append(stmts, s+";")
		}
	}
	return stmts
}

func splitOnSemicolon(s string) []string {
	var result []string
	current := ""
	inDollarQuote := false
	for i := 0; i < len(s); i++ {
		if i+1 < len(s) && s[i] == '$' && s[i+1] == '$' {
			inDollarQuote = !inDollarQuote
			current += "$$"
			i++
			continue
		}
		if s[i] == ';' && !inDollarQuote {
			result = append(result, current)
			current = ""
			continue
		}
		current += string(s[i])
	}
	if trimSpace(current) != "" {
		result = append(result, current)
	}
	return result
}

func trimSpace(s string) string {
	// Simple trim for leading/trailing whitespace
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\n' || s[start] == '\r' || s[start] == '\t') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\n' || s[end-1] == '\r' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}

func containsDestructive(stmt string) bool {
	upper := ""
	for _, c := range stmt {
		if c >= 'a' && c <= 'z' {
			upper += string(c - 32)
		} else {
			upper += string(c)
		}
	}
	return len(upper) > 0 && (contains(upper, "DROP ") || contains(upper, "DELETE ") || contains(upper, "TRUNCATE "))
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
