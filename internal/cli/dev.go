package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	ultrahttp "github.com/saedx1/ultrabase/internal/adapter/http"
	"github.com/saedx1/ultrabase/internal/app"
	"github.com/saedx1/ultrabase/internal/config"
	"github.com/spf13/cobra"
)

func newDevCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dev",
		Short: "Start local development server with hot-reload",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := parseDevFlags(extractCobraArgs(cmd.Flags()), os.Getenv)
			if err != nil {
				return err
			}
			return runDev(opts)
		},
	}

	cmd.Flags().Int("port", 0, "server port (default: from config or 8080)")
	cmd.Flags().String("config", "ultrabase.yaml", "config source (path or s3://bucket/key)")
	cmd.Flags().Bool("no-watch", false, "disable hot-reload (alias for --watch=false)")
	cmd.Flags().Bool("watch", true, "watch the config source for changes")
	cmd.Flags().Duration("watch-interval", 60*time.Second, "S3-watch poll interval; min 10s")
	cmd.Flags().String("dashboard", "readwrite", "dashboard mode: disabled | readonly | readwrite")
	cmd.Flags().Bool("verbose", false, "debug logging")
	return cmd
}

func runDev(opts devOptions) error {
	cfg, err := config.LoadWithDotenv(opts.configPath, ".env")
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

	// Create HTTP server
	httpServer := ultrahttp.NewServer(ultrahttp.ServerDeps{
		Config:     cfg,
		DB:         authDB,
		Logger:     logger,
		DevMode:    true,
		Email:      email,
		Storage:    storage,
		JWTKeys:    app.NewJWTKeyManager(ownerDB),
		ConfigPath: opts.configPath,
	})

	// Create and start engine with HTTP server
	engine := app.NewEngine(cfg, ownerDB, authDB, roles,
		app.WithMode(app.ModeDev),
		app.WithMigrate(true),
		app.WithSeed(true),
		app.WithWatch(opts.watch),
		app.WithConfigPath(opts.configPath),
		app.WithLogger(logger),
		app.WithHTTPServer(httpServer),
	)

	fmt.Printf("\n  API:       http://localhost:%d\n", cfg.Server.Port)
	fmt.Printf("  Dashboard: http://localhost:%d/dashboard\n", cfg.Server.Port)
	fmt.Printf("  Docs:      http://localhost:%d/api/docs\n", cfg.Server.Port)
	fmt.Printf("  OpenAPI:   http://localhost:%d/api/openapi.json\n", cfg.Server.Port)

	if opts.watch {
		fmt.Printf("\n  Watching for changes... (Ctrl+C to stop)\n")
	}

	// Start engine (blocks until shutdown signal)
	return engine.Start(ctx)
}
