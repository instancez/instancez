package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/saedx1/ultrabase/internal/config"
	"github.com/saedx1/ultrabase/internal/domain"
)

// ConfigWatcher watches ultrabase.yaml for changes and triggers re-migration.
type ConfigWatcher struct {
	configPath string
	db         domain.Database
	logger     *slog.Logger
	onChange   func(cfg *domain.Config) // callback after successful reload
}

// NewConfigWatcher creates a file watcher for the config file.
func NewConfigWatcher(configPath string, db domain.Database, logger *slog.Logger, onChange func(cfg *domain.Config)) *ConfigWatcher {
	return &ConfigWatcher{
		configPath: configPath,
		db:         db,
		logger:     logger,
		onChange:   onChange,
	}
}

// Watch starts watching the config file. Blocks until ctx is cancelled.
func (w *ConfigWatcher) Watch(ctx context.Context) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create file watcher: %w", err)
	}
	defer watcher.Close()

	if err := watcher.Add(w.configPath); err != nil {
		return fmt.Errorf("watch %s: %w", w.configPath, err)
	}

	w.logger.Info("watching for config changes", "file", w.configPath)

	// Debounce timer to avoid multiple rapid reloads
	var debounce *time.Timer

	for {
		select {
		case <-ctx.Done():
			return nil

		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if event.Op&(fsnotify.Write|fsnotify.Create) == 0 {
				continue
			}

			// Debounce: wait 500ms after last change before reloading
			if debounce != nil {
				debounce.Stop()
			}
			debounce = time.AfterFunc(500*time.Millisecond, func() {
				w.reload(ctx)
			})

		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			w.logger.Error("watcher error", "error", err)
		}
	}
}

func (w *ConfigWatcher) reload(ctx context.Context) {
	w.logger.Info("config change detected, reloading...")

	// Load and validate new config
	cfg, err := config.Load(w.configPath)
	if err != nil {
		w.logger.Error("config reload failed: invalid YAML", "error", err)
		return
	}

	errs := config.Validate(cfg)
	if errs != nil {
		w.logger.Error("config reload failed: validation errors", "count", len(errs))
		for _, e := range errs {
			w.logger.Error("  validation", "path", e.Path, "message", e.Message)
		}
		return
	}

	// Re-run migrations
	migrator := NewMigrator(w.db)
	if err := migrator.Apply(ctx, cfg); err != nil {
		w.logger.Error("migration failed after config reload", "error", err)
		return
	}

	w.logger.Info("config reloaded successfully",
		"tables", len(cfg.Tables),
		"triggers", len(cfg.On))

	if w.onChange != nil {
		w.onChange(cfg)
	}
}
