package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/gin-gonic/gin"
	"github.com/saedx1/instancez/internal/app"
	"github.com/saedx1/instancez/internal/config"
	"github.com/saedx1/instancez/internal/domain"
	"gopkg.in/yaml.v3"
)

// AdminHandler serves /api/_admin/* endpoints.
type AdminHandler struct {
	cfg           *domain.Config
	configFn      func() *domain.Config // returns the LIVE engine config (lastGood when drifted)
	db            domain.Database
	logger        *slog.Logger
	configSource  config.Source
	dashboardMode DashboardMode
	driftFn       func() *app.DriftTracker
}

func NewAdminHandler(deps ServerDeps) *AdminHandler {
	return &AdminHandler{
		cfg:           deps.Config,
		configFn:      deps.ConfigFn,
		db:            deps.DB.Database,
		logger:        deps.Logger,
		configSource:  deps.ConfigSource,
		dashboardMode: deps.DashboardMode,
		driftFn:       deps.DriftFn,
	}
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
}

func (h *AdminHandler) handleListMigrations(c *gin.Context) {
	ctx := c.Request.Context()
	rows, err := h.db.Query(ctx,
		"SELECT id, checksum, applied_at FROM _ultrabase_migrations ORDER BY id DESC LIMIT 100")
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
	h.db.Exec(ctx, "DELETE FROM auth.refresh_tokens WHERE user_id = $1", id)

	c.JSON(200, gin.H{"message": "User disabled", "user_id": id})
}

func (h *AdminHandler) handleAdminResetPassword(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()

	// Generate a temporary token
	token := generateRandomToken()
	h.db.Exec(ctx,
		"INSERT INTO auth.one_time_tokens (user_id, token, expires_at) VALUES ($1, $2, NOW() + INTERVAL '24 hours')",
		id, token)

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
	cfg := h.liveConfig()
	jsonData, err := json.Marshal(cfg)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to serialize config")
		return
	}
	var result map[string]any
	if err := json.Unmarshal(jsonData, &result); err != nil {
		problemJSON(c, 500, "internal", "Failed to round-trip config")
		return
	}

	// Surface the source's current version token as `_checksum` so PUT can
	// pass it back via If-Match. When no source is wired (test path) the
	// field is omitted.
	if h.configSource != nil {
		if _, ver, err := h.configSource.Read(c.Request.Context()); err == nil {
			result["_checksum"] = ver
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
	migrator := app.NewMigrator(h.db)
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

	c.JSON(200, gin.H{
		"message":       "Config saved",
		"config_source": h.sourceDescribe(),
		"new_version":   newVersion,
	})
}

// handleConfigDiff returns DDL migration diff for current config.
func (h *AdminHandler) handleConfigDiff(c *gin.Context) {
	ctx := c.Request.Context()

	migrator := app.NewMigrator(h.db)
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
