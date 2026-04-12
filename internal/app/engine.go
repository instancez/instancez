package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/saedx1/ultrabase/internal/domain"
	"golang.org/x/crypto/bcrypt"
)

// HTTPServer is the interface for the HTTP server managed by the engine.
type HTTPServer interface {
	Start() error
	Shutdown(ctx context.Context) error
}

// Engine orchestrates the full Ultrabase lifecycle.
type Engine struct {
	cfg      *domain.Config
	db       domain.Database
	migrator *Migrator
	logger   *slog.Logger

	// Managed components
	httpServer  HTTPServer
	walConsumer domain.WALConsumer
	eventWorker *EventWorker

	// Options
	mode             Mode
	migrate          bool
	seed             bool
	allowDestructive bool
	watch            bool
	configPath       string // for hot-reload watcher
}

type Mode int

const (
	ModeDev  Mode = iota
	ModeProd
)

type EngineOption func(*Engine)

func WithMode(m Mode) EngineOption         { return func(e *Engine) { e.mode = m } }
func WithMigrate(v bool) EngineOption      { return func(e *Engine) { e.migrate = v } }
func WithSeed(v bool) EngineOption         { return func(e *Engine) { e.seed = v } }
func WithAllowDestructive(v bool) EngineOption { return func(e *Engine) { e.allowDestructive = v } }
func WithWatch(v bool) EngineOption              { return func(e *Engine) { e.watch = v } }
func WithLogger(l *slog.Logger) EngineOption     { return func(e *Engine) { e.logger = l } }
func WithHTTPServer(s HTTPServer) EngineOption   { return func(e *Engine) { e.httpServer = s } }
func WithWALConsumer(w domain.WALConsumer) EngineOption { return func(e *Engine) { e.walConsumer = w } }
func WithEventWorker(w *EventWorker) EngineOption       { return func(e *Engine) { e.eventWorker = w } }
func WithConfigPath(p string) EngineOption { return func(e *Engine) { e.configPath = p } }

func NewEngine(cfg *domain.Config, db domain.Database, opts ...EngineOption) *Engine {
	e := &Engine{
		cfg:     cfg,
		db:      db,
		migrator: NewMigrator(db),
		logger:  slog.Default(),
		mode:    ModeDev,
		migrate: true,
		seed:    true,
		watch:   true,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Start runs the full startup sequence and blocks until shutdown.
func (e *Engine) Start(ctx context.Context) error {
	start := time.Now()

	e.logger.Info("starting ultrabase", "mode", e.modeStr())

	// 1. Migrate
	if e.migrate {
		t := time.Now()
		if err := e.migrator.Apply(ctx, e.cfg); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
		e.logger.Info("migrations applied", "duration", time.Since(t).Round(time.Millisecond))
	}

	// 2. Seed
	if e.seed && len(e.cfg.Seeds) > 0 {
		t := time.Now()
		if err := e.applySeeds(ctx); err != nil {
			return fmt.Errorf("seeds failed: %w", err)
		}
		e.logger.Info("seeds applied", "duration", time.Since(t).Round(time.Millisecond))
	}

	// 3. Start HTTP server
	if e.httpServer != nil {
		go func() {
			if err := e.httpServer.Start(); err != nil {
				e.logger.Error("HTTP server error", "error", err)
			}
		}()
	}

	// 4. Start WAL consumer
	if e.walConsumer != nil {
		go func() {
			if err := e.walConsumer.Start(ctx); err != nil {
				e.logger.Error("WAL consumer error", "error", err)
			}
		}()
	}

	// 4b. Start event worker (outbox deliverer)
	if e.eventWorker != nil {
		go func() {
			if err := e.eventWorker.Start(ctx); err != nil {
				e.logger.Error("event worker error", "error", err)
			}
		}()
	}

	// 5. Start config file watcher (dev mode only)
	if e.watch && e.configPath != "" {
		watcher := NewConfigWatcher(e.configPath, e.db, e.logger, func(newCfg *domain.Config) {
			e.cfg = newCfg
		})
		go func() {
			if err := watcher.Watch(ctx); err != nil {
				e.logger.Error("config watcher error", "error", err)
			}
		}()
	}

	e.logger.Info("startup complete",
		"port", e.cfg.Server.Port,
		"tables", len(e.cfg.Tables),
		"duration", time.Since(start).Round(time.Millisecond))

	// Block until signal
	return e.waitForShutdown(ctx)
}

func (e *Engine) applySeeds(ctx context.Context) error {
	ordered := orderSeedTables(e.cfg)
	for _, tableName := range ordered {
		rows := e.cfg.Seeds[tableName]
		for _, row := range rows {
			// Special handling for users table: hash the password field
			if tableName == "users" {
				if pwd, ok := row["password"]; ok {
					if pwdStr, ok := pwd.(string); ok {
						hash, err := bcrypt.GenerateFromPassword([]byte(pwdStr), bcrypt.DefaultCost)
						if err != nil {
							return fmt.Errorf("seed users: hash password: %w", err)
						}
						row["password_hash"] = string(hash)
						delete(row, "password")
					}
				}
			}
			if err := e.upsertSeedRow(ctx, tableName, row); err != nil {
				return fmt.Errorf("seed %s: %w", tableName, err)
			}
		}
	}
	return nil
}

func (e *Engine) upsertSeedRow(ctx context.Context, tableName string, row map[string]any) error {
	if len(row) == 0 {
		return nil
	}

	// Build column list and values
	cols := sortedKeys(row)
	placeholders := make([]string, len(cols))
	values := make([]any, len(cols))
	for i, col := range cols {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		values[i] = row[col]
	}

	// Find primary key for ON CONFLICT
	pk := "id" // conventional default
	if table, ok := e.cfg.Tables[tableName]; ok {
		for fname, field := range table.Fields {
			if field.PrimaryKey {
				pk = fname
				break
			}
		}
	}

	// Build upsert: INSERT ... ON CONFLICT (pk) DO UPDATE SET ...
	setClause := make([]string, 0, len(cols))
	for _, col := range cols {
		if col != pk {
			setClause = append(setClause, fmt.Sprintf("%s = EXCLUDED.%s", col, col))
		}
	}

	query := fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (%s) DO UPDATE SET %s",
		tableName,
		joinStrings(cols, ", "),
		joinStrings(placeholders, ", "),
		pk,
		joinStrings(setClause, ", "),
	)

	_, err := e.db.Exec(ctx, query, values...)
	return err
}

func (e *Engine) waitForShutdown(ctx context.Context) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		e.logger.Info("received signal, shutting down", "signal", sig)
	case <-ctx.Done():
		e.logger.Info("context cancelled, shutting down")
	}

	return e.shutdown()
}

func (e *Engine) shutdown() error {
	e.logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Stop HTTP server (drain active requests)
	if e.httpServer != nil {
		if err := e.httpServer.Shutdown(shutdownCtx); err != nil {
			e.logger.Error("error shutting down HTTP server", "error", err)
		}
	}

	// Stop WAL consumer
	if e.walConsumer != nil {
		if err := e.walConsumer.Stop(shutdownCtx); err != nil {
			e.logger.Error("error stopping WAL consumer", "error", err)
		}
	}

	// Close database
	if err := e.db.Close(); err != nil {
		e.logger.Error("error closing database", "error", err)
	}

	e.logger.Info("shutdown complete")
	return nil
}

func (e *Engine) modeStr() string {
	if e.mode == ModeDev {
		return "dev"
	}
	return "production"
}

// orderSeedTables returns seed table names in a safe insertion order.
// "users" always comes first (auth table), then tables ordered by FK deps.
func orderSeedTables(cfg *domain.Config) []string {
	var result []string
	if _, ok := cfg.Seeds["users"]; ok {
		result = append(result, "users")
	}
	ordered := orderTables(cfg.Tables)
	for _, name := range ordered {
		if _, ok := cfg.Seeds[name]; ok {
			result = append(result, name)
		}
	}
	return result
}

func joinStrings(strs []string, sep string) string {
	result := ""
	for i, s := range strs {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}
