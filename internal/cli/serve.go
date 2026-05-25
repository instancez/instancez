package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/saedx1/ultrabase/dashboard"
	ultrahttp "github.com/saedx1/ultrabase/internal/adapter/http"
	"github.com/saedx1/ultrabase/internal/app"
	"github.com/saedx1/ultrabase/internal/config"
	"github.com/saedx1/ultrabase/internal/domain"
	"github.com/spf13/cobra"
)

func newServeCmd() *cobra.Command {
	fs := newServeFlagSet()
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start production server",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := resolveServeFlags(fs, os.Getenv)
			if err != nil {
				return err
			}
			return runServe(opts)
		},
	}
	cmd.Flags().AddFlagSet(fs.flags)
	return cmd
}

func runServe(opts serveOptions) error {
	ctx := context.Background()

	if err := requireConfigFile(opts.configPath); err != nil {
		return err
	}

	source, err := config.NewSource(opts.configPath)
	if err != nil {
		return err
	}

	// serve loads .production.env when running against a local file source;
	// shell env vars always take precedence. Skipped for s3:// sources
	// since prod env vars come from the orchestrator (ConfigMap, secrets,
	// etc.) in that deployment shape.
	if _, ok := source.(*config.FileSource); ok {
		if err := config.LoadDotenv(".production.env"); err != nil {
			return err
		}
	}
	cfg, err := source.Load(ctx)
	if err != nil {
		return err
	}

	errs := config.Validate(cfg)
	if errs != nil {
		return printPrettyErrors(errs)
	}

	if opts.port > 0 {
		cfg.Server.Port = opts.port
	}

	// Structured JSON logger for production. All startup output goes through
	// the logger so prod stdout stays a single parseable JSON stream.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	logger.Info("starting ultrabase",
		"version", version,
		"mode", "production",
		"config_source", source.Describe())
	logger.Info("schema valid")
	logger.Info("config resolved",
		"watch", opts.watch,
		"watch_interval", opts.watchInterval.String(),
		"dashboard", opts.dashboard.String())

	logger.Warn("ultrabase is designed for single-replica deployments; multi-replica support is planned")

	// Connect to database (owner + authenticator pools).
	ownerDB, authDB, roles, err := dbConnections(ctx, cfg.Database.Pool)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	logger.Info("connected to postgres", "pools", "owner+authenticator")

	// Initialize providers
	email, storage, err := initProviders(ctx, cfg)
	if err != nil {
		return err
	}

	// Validate required providers
	if len(cfg.Storage) > 0 && storage == nil {
		return fmt.Errorf("storage buckets configured but no storage provider set in providers.storage")
	}
	if cfg.Auth != nil && cfg.Auth.Email != nil && email == nil {
		return fmt.Errorf("auth email configured but no email provider set in providers.email")
	}

	// Provider health checks
	if storage != nil && len(cfg.Storage) > 0 {
		if err := checkStorageHealth(ctx, storage, cfg, logger); err != nil {
			return fmt.Errorf("storage health: %w", err)
		}
		logger.Info("storage buckets verified")
	}

	// Only expose a local config path to the admin handler when the source is
	// a local file. For S3 sources the admin config endpoints return 501.
	var adminConfigPath string
	if fs, ok := source.(*config.FileSource); ok {
		adminConfigPath = fs.Path
	}

	// Create HTTP server. The Drift/Config closures capture `engine` (declared
	// below) so the handlers see live engine state once Start has run; before
	// Start they fall back to nil/cfg.
	var engine *app.Engine
	httpServer := ultrahttp.NewServer(ultrahttp.ServerDeps{
		Config:          cfg,
		DB:              authDB,
		Logger:          logger,
		DevMode:         false,
		Email:           email,
		Storage:         storage,
		JWTKeys:         app.NewJWTKeyManager(ownerDB),
		ConfigPath:      adminConfigPath,
		DashboardMode:   opts.dashboard.HTTP(),
		DashboardAssets: dashboard.Assets(),
		ConfigSource:    source,
		DriftFn: func() *app.DriftTracker {
			if engine == nil {
				return nil
			}
			return engine.Drift()
		},
		ConfigFn: func() *domain.Config {
			if engine == nil {
				return cfg
			}
			return engine.Config()
		},
	})

	// Create engine with HTTP server
	engine = app.NewEngine(cfg, ownerDB, authDB, roles,
		app.WithMode(app.ModeProd),
		app.WithMigrate(opts.migrate),
		app.WithSeed(opts.loadData),
		app.WithAllowDestructive(opts.allowDestructive),
		app.WithWatch(opts.watch),
		app.WithWatchInterval(opts.watchInterval),
		app.WithLogger(logger),
		app.WithHTTPServer(httpServer),
		app.WithConfigSource(source),
	)

	logger.Info("listening", "port", cfg.Server.Port)

	return engine.Start(ctx)
}
