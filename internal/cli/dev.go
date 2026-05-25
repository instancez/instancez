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

func newDevCmd() *cobra.Command {
	fs := newDevFlagSet()
	cmd := &cobra.Command{
		Use:   "dev",
		Short: "Start local development server with hot-reload",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := resolveDevFlags(fs, os.Getenv)
			if err != nil {
				return err
			}
			return runDev(opts)
		},
	}
	cmd.Flags().AddFlagSet(fs.flags)
	return cmd
}

func runDev(opts devOptions) error {
	if err := requireConfigFile(opts.configPath); err != nil {
		return err
	}

	switch opts.dbSrc {
	case DevDBSourceDocker:
		return fmt.Errorf("--use-docker is not yet implemented in this build; use --use-dsn for now")
	case DevDBSourceCloudEphemeral:
		return fmt.Errorf("--use-cloud-ephemeral is not yet implemented in this build; use --use-dsn for now")
	case DevDBSourceDSN:
		// fall through — env-var DSN is the only wired path right now.
	default:
		// Defensive: resolveDevFlags should have caught this.
		return fmt.Errorf("internal: dev data source unset")
	}

	cfg, err := config.LoadWithDotenv(opts.configPath, ".development.env")
	if err != nil {
		return err
	}

	errs := config.Validate(cfg)
	if errs != nil {
		return printPrettyErrors(errs)
	}

	// Build a Source handle for the engine + http deps (Tasks 10/11/12 use it
	// for the watch loop and the admin PUT endpoint).
	source, err := config.NewSource(opts.configPath)
	if err != nil {
		return err
	}

	if opts.port > 0 {
		cfg.Server.Port = opts.port
	}

	// Set up logger
	level := slog.LevelInfo
	if opts.verbose {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	fmt.Printf("  Ultrabase v%s\n\n", version)
	fmt.Printf("  ✓ Schema valid\n")

	if opts.dashboard != DashboardReadwrite {
		fmt.Printf("  Dashboard: %s\n", opts.dashboard)
	}
	if !opts.watch {
		fmt.Printf("  Watch:     disabled\n")
	}

	ctx := context.Background()
	ownerDB, authDB, roles, err := dbConnections(ctx, cfg.Database.Pool)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	fmt.Printf("  ✓ Connected to PostgreSQL (owner + authenticator)\n")

	// Initialize providers
	email, storage, err := initProviders(ctx, cfg)
	if err != nil {
		return err
	}
	if email != nil {
		fmt.Printf("  ✓ Email provider: %s\n", cfg.Providers.Email.Type)
	}
	if storage != nil {
		fmt.Printf("  ✓ Storage provider: %s\n", cfg.Providers.Storage.Type)
	}

	// Create HTTP server. The Drift/Config closures capture `engine` (declared
	// below) so handlers see live engine state once Start has run; before
	// Start they fall back to nil/cfg.
	var engine *app.Engine
	httpServer := ultrahttp.NewServer(ultrahttp.ServerDeps{
		Config:          cfg,
		DB:              authDB,
		Logger:          logger,
		DevMode:         true,
		Email:           email,
		Storage:         storage,
		JWTKeys:         app.NewJWTKeyManager(ownerDB),
		ConfigPath:      opts.configPath,
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

	// Create and start engine with HTTP server
	engine = app.NewEngine(cfg, ownerDB, authDB, roles,
		app.WithMode(app.ModeDev),
		app.WithMigrate(true),
		app.WithSeed(true),
		app.WithWatch(opts.watch),
		app.WithWatchInterval(opts.watchInterval),
		app.WithConfigPath(opts.configPath),
		app.WithConfigSource(source),
		app.WithLogger(logger),
		app.WithHTTPServer(httpServer),
	)

	fmt.Printf("\n  API:       http://localhost:%d\n", cfg.Server.Port)
	if opts.dashboard != DashboardDisabled {
		fmt.Printf("  Dashboard: http://localhost:%d/dashboard\n", cfg.Server.Port)
	}
	fmt.Printf("  Docs:      http://localhost:%d/api/docs\n", cfg.Server.Port)
	fmt.Printf("  OpenAPI:   http://localhost:%d/api/openapi.json\n", cfg.Server.Port)

	if opts.watch {
		fmt.Printf("\n  Watching for changes... (Ctrl+C to stop)\n")
	}

	// Start engine (blocks until shutdown signal)
	return engine.Start(ctx)
}
