package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/instancez/instancez/internal/config"
	"github.com/instancez/instancez/internal/domain"
)

// HTTPServer is the interface for the HTTP server managed by the engine.
type HTTPServer interface {
	Start() error
	Shutdown(ctx context.Context) error
}

// Engine orchestrates the full Instancez lifecycle.
type Engine struct {
	// mu guards cfg and drift, both of which are reassigned by the watcher
	// goroutine on reload while the request path and admin endpoints read
	// them concurrently. The DriftTracker itself is internally synchronized;
	// the lock here only protects pointer reassignment.
	mu       sync.RWMutex
	cfg      *domain.Config
	drift    *DriftTracker
	ownerDB  domain.OwnerDB   // privileged: migrations and DDL
	authDB   domain.RequestDB // request path; SET LOCAL ROLE per tx
	migrator *Migrator
	logger   *slog.Logger

	// Managed components
	httpServer HTTPServer

	// Options
	mode             Mode
	migrate          bool
	allowDestructive bool
	watch            bool
	watchInterval    time.Duration
	configPath       string // for hot-reload watcher

	// Config-source state populated during Start.
	source config.Source

	// onFunctionReload, when set, is invoked by the config watcher after a
	// successful reload with the newly-applied config. serve uses it to hot-swap
	// the function runtime when the functions bundle version changes. It runs on
	// the watcher goroutine and must not block for long.
	onFunctionReload func(*domain.Config)
}

type Mode int

const (
	ModeDev Mode = iota
	ModeProd
)

type EngineOption func(*Engine)

func WithMode(m Mode) EngineOption                   { return func(e *Engine) { e.mode = m } }
func WithMigrate(v bool) EngineOption                { return func(e *Engine) { e.migrate = v } }
func WithAllowDestructive(v bool) EngineOption       { return func(e *Engine) { e.allowDestructive = v } }
func WithWatch(v bool) EngineOption                  { return func(e *Engine) { e.watch = v } }
func WithLogger(l *slog.Logger) EngineOption         { return func(e *Engine) { e.logger = l } }
func WithHTTPServer(s HTTPServer) EngineOption       { return func(e *Engine) { e.httpServer = s } }
func WithConfigPath(p string) EngineOption           { return func(e *Engine) { e.configPath = p } }
func WithConfigSource(s config.Source) EngineOption  { return func(e *Engine) { e.source = s } }
func WithWatchInterval(d time.Duration) EngineOption { return func(e *Engine) { e.watchInterval = d } }

// WithFunctionReload registers a callback invoked by the config watcher after a
// successful reload, with the newly-applied config. Used by serve to hot-swap
// the function runtime on a bundle version change.
func WithFunctionReload(fn func(*domain.Config)) EngineOption {
	return func(e *Engine) { e.onFunctionReload = fn }
}

func NewEngine(cfg *domain.Config, ownerDB domain.OwnerDB, authDB domain.RequestDB, roles domain.Roles, opts ...EngineOption) *Engine {
	e := &Engine{
		cfg:      cfg,
		ownerDB:  ownerDB,
		authDB:   authDB,
		migrator: NewMigrator(ownerDB, roles),
		logger:   slog.Default(),
		mode:    ModeDev,
		migrate: true,
		watch:   true,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Drift returns the engine's drift tracker (or nil before Start has run).
func (e *Engine) Drift() *DriftTracker {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.drift
}

// Config returns the live engine config (lastGood when drifted).
func (e *Engine) Config() *domain.Config {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cfg
}

// SetConfig replaces the live config immediately (used by the dashboard PUT
// path so GET /config reflects the change before the file watcher fires).
func (e *Engine) SetConfig(cfg *domain.Config) {
	e.mu.Lock()
	e.cfg = cfg
	e.mu.Unlock()
}

// sourceDescription returns a stable, human-readable identifier for the
// active config source for use in logs and the drift snapshot.
func (e *Engine) sourceDescription() string {
	if e.source != nil {
		return e.source.Describe()
	}
	if e.configPath != "" {
		return e.configPath
	}
	return "unknown"
}

// applyMigrationsWithFallback runs migrations against e.cfg. On success it
// returns a DriftTracker in OK state. On failure, it attempts to load the
// last-known-good config from _instancez_migrations.config_json and
// continues running with that config, returning a DriftTracker in drift
// state. If no last-known-good exists (first boot), it returns the original
// migration error so the caller can fail fast.
func (e *Engine) applyMigrationsWithFallback(ctx context.Context) (*DriftTracker, error) {
	tracker := NewDriftTracker(e.sourceDescription())

	configJSON, _ := json.Marshal(e.cfg)
	checksum := fmt.Sprintf("%x", sha256.Sum256(configJSON))

	if err := e.migrator.Apply(ctx, e.cfg); err == nil {
		tracker.MarkOK(checksum, time.Now())
		return tracker, nil
	} else {
		applyErr := err

		last, lastErr := e.ownerDB.GetLastMigration(ctx)
		if lastErr != nil {
			return nil, fmt.Errorf("migration failed (%w) and could not load last-known-good: %v", applyErr, lastErr)
		}
		if last == nil || last.ConfigJSON == "" || last.ConfigJSON == "{}" {
			return nil, fmt.Errorf("first-boot migration failed: %w", applyErr)
		}

		var goodCfg domain.Config
		if err := json.Unmarshal([]byte(last.ConfigJSON), &goodCfg); err != nil {
			return nil, fmt.Errorf("migration failed and last-known-good is unparseable: %v (apply error: %w)", err, applyErr)
		}

		e.logger.Error("config drift: source has unapplied changes",
			"source", e.sourceDescription(),
			"reason", applyErr.Error(),
			"running_applied_at", last.AppliedAt,
		)
		e.mu.Lock()
		e.cfg = &goodCfg
		e.mu.Unlock()
		// Order matters: MarkOK seeds running_*, then MarkDrift overlays
		// source_* and LastError without clobbering running_*. Reversed,
		// MarkOK would clear the just-recorded source/error fields.
		tracker.MarkOK(last.Checksum, last.AppliedAt)
		tracker.MarkDrift(checksum, applyErr.Error(), time.Now())
		return tracker, nil
	}
}

// versionChecksum returns the sha256 hex digest of the supplied bytes. Used
// to stamp drift events keyed off raw source bytes (where we don't have a
// parsed Config to JSON-encode).
func versionChecksum(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// runWatcher subscribes to source change events and reloads the engine on
// each one, going through the same applyMigrationsWithFallback path as boot
// so failures degrade to drift instead of crashing.
func (e *Engine) runWatcher(ctx context.Context, interval time.Duration) {
	if e.source == nil {
		e.logger.Warn("watcher requested but no config source configured")
		return
	}
	ch, err := e.source.Watch(ctx, interval)
	if err != nil {
		e.logger.Error("config watcher failed to start", "error", err)
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if ev.Err != nil {
				e.logger.Warn("config watch transient error", "error", ev.Err)
				continue
			}
			cfg, err := config.ParseBytes(ev.Data, e.source.Describe())
			if err != nil {
				e.logger.Error("config reload: parse failed", "error", err)
				if d := e.Drift(); d != nil {
					d.MarkDrift(versionChecksum(ev.Data), err.Error(), time.Now())
				}
				continue
			}
			if errs := config.Validate(cfg); errs != nil {
				e.logger.Error("config reload: validation failed", "count", len(errs))
				if d := e.Drift(); d != nil {
					msg := errs[0].Path + ": " + errs[0].Message
					d.MarkDrift(versionChecksum(ev.Data), msg, time.Now())
				}
				continue
			}
			e.mu.Lock()
			e.cfg = cfg
			e.mu.Unlock()
			tracker, err := e.applyMigrationsWithFallback(ctx)
			if err != nil {
				e.logger.Error("config reload: migration unrecoverable", "error", err)
				continue
			}
			e.mu.Lock()
			e.drift = tracker
			e.mu.Unlock()
			e.logger.Info("config reloaded successfully", "tables", len(cfg.Tables))
			if e.onFunctionReload != nil {
				e.onFunctionReload(cfg)
			}
		}
	}
}

// lifecycle logs routine startup/shutdown narration. In dev the human banner
// owns this story, so keep the structured logger quiet (Debug); in prod these
// lines are the only startup record, so they stay at Info.
func (e *Engine) lifecycle(msg string, args ...any) {
	if e.mode == ModeDev {
		e.logger.Debug(msg, args...)
		return
	}
	e.logger.Info(msg, args...)
}

// Start runs the full startup sequence and blocks until shutdown.
func (e *Engine) Start(ctx context.Context) error {
	start := time.Now()

	e.lifecycle("starting instancez", "mode", e.modeStr())

	// 1. Migrate (with last-known-good fallback)
	t := time.Now()
	if e.mode == ModeDev || e.migrate {
		tracker, err := e.applyMigrationsWithFallback(ctx)
		if err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
		e.drift = tracker
		e.lifecycle("migrations applied", "duration", time.Since(t).Round(time.Millisecond))
	} else {
		// migrate=false on serve: only check, never mutate the schema.
		if err := e.ownerDB.EnsureMigrationsTable(ctx); err != nil {
			return fmt.Errorf("migration check: %w", err)
		}
		last, err := e.ownerDB.GetLastMigration(ctx)
		if err != nil {
			return fmt.Errorf("migration check: %w", err)
		}
		if last == nil {
			tracker, err := e.applyMigrationsWithFallback(ctx)
			if err != nil {
				return fmt.Errorf("initial migration failed: %w", err)
			}
			e.drift = tracker
			e.lifecycle("initial migration applied", "duration", time.Since(t).Round(time.Millisecond))
		} else {
			configJSON, _ := json.Marshal(e.cfg)
			checksum := fmt.Sprintf("%x", sha256.Sum256(configJSON))
			tracker := NewDriftTracker(e.sourceDescription())
			if last.Checksum == checksum {
				tracker.MarkOK(last.Checksum, last.AppliedAt)
			} else {
				e.logger.Warn("config has changed since last migration; run with --migrate to apply pending changes")
				// Order matters: MarkOK before MarkDrift (see applyMigrationsWithFallback).
				tracker.MarkOK(last.Checksum, last.AppliedAt)
				tracker.MarkDrift(checksum, "config changed but --migrate not set", time.Now())
			}
			e.drift = tracker
		}
	}

	// 1b. Drift heartbeat (only meaningful when in drift)
	if e.drift != nil {
		go runDriftHeartbeat(ctx, e.drift, e.logger, 10*time.Minute, nil)
	}

	// 2. Start HTTP server
	srvErrCh := make(chan error, 1)
	if e.httpServer != nil {
		go func() {
			if err := e.httpServer.Start(); err != nil {
				e.logger.Error("HTTP server error", "error", err)
				srvErrCh <- err
			}
		}()
	}

	// 4. Start config source watcher
	if e.watch && e.source != nil {
		go e.runWatcher(ctx, e.watchInterval)
	}

	e.lifecycle("startup complete",
		"port", e.cfg.Server.Port,
		"tables", len(e.cfg.Tables),
		"duration", time.Since(start).Round(time.Millisecond))

	// Block until signal
	return e.waitForShutdown(ctx, srvErrCh)
}


func (e *Engine) waitForShutdown(ctx context.Context, srvErrCh <-chan error) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		e.lifecycle("received signal, shutting down", "signal", sig)
	case <-ctx.Done():
		e.lifecycle("context cancelled, shutting down")
	case err := <-srvErrCh:
		e.shutdown()
		return err
	}

	return e.shutdown()
}

func (e *Engine) shutdown() error {
	e.lifecycle("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Stop HTTP server (drain active requests)
	if e.httpServer != nil {
		if err := e.httpServer.Shutdown(shutdownCtx); err != nil {
			e.logger.Error("error shutting down HTTP server", "error", err)
		}
	}

	// Close database
	if err := e.ownerDB.Close(); err != nil {
		e.logger.Error("error closing database", "error", err)
	}

	e.lifecycle("shutdown complete")
	return nil
}

func (e *Engine) modeStr() string {
	if e.mode == ModeDev {
		return "dev"
	}
	return "production"
}


// runDriftHeartbeat logs a loud error periodically while the tracker shows
// drift, so the failure mode doesn't get buried in log volume. Returns when
// ctx is cancelled. The onTick callback is for tests; nil in production.
func runDriftHeartbeat(ctx context.Context, tracker *DriftTracker, logger *slog.Logger, interval time.Duration, onTick func()) {
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			state := tracker.Snapshot()
			if state.Status != DriftStatusDrift {
				continue
			}
			logger.Error("config drift",
				"source", state.ConfigSource,
				"reason", state.LastError,
				"running_applied_at", state.RunningAppliedAt,
				"source_seen_at", state.SourceLastSeenAt,
			)
			if onTick != nil {
				onTick()
			}
		}
	}
}
