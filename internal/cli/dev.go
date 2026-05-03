package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	ultrahttp "github.com/saedx1/ultrabase/internal/adapter/http"
	"github.com/saedx1/ultrabase/internal/app"
	"github.com/saedx1/ultrabase/internal/config"
	"github.com/spf13/cobra"
)

func newDevCmd() *cobra.Command {
	var (
		port       int
		configPath string
		noWatch    bool
		verbose    bool
	)

	cmd := &cobra.Command{
		Use:   "dev",
		Short: "Start local development server with hot-reload",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDev(port, configPath, noWatch, verbose)
		},
	}

	cmd.Flags().IntVar(&port, "port", 0, "server port (default: from config or 8080)")
	cmd.Flags().StringVar(&configPath, "config", "ultrabase.yaml", "config file path")
	cmd.Flags().BoolVar(&noWatch, "no-watch", false, "disable hot-reload")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "debug logging")
	return cmd
}

func runDev(port int, configPath string, noWatch, verbose bool) error {
	cfg, err := config.LoadWithDotenv(configPath, ".env")
	if err != nil {
		return err
	}

	errs := config.Validate(cfg)
	if errs != nil {
		return printPrettyErrors(errs)
	}

	if port > 0 {
		cfg.Server.Port = port
	}

	// Set up logger
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	fmt.Printf("  Ultrabase v%s\n\n", version)
	fmt.Printf("  \u2713 Schema valid\n")

	ctx := context.Background()
	ownerDB, authDB, roles, err := dbConnections(ctx, cfg.Database.Pool)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	fmt.Printf("  \u2713 Connected to PostgreSQL (owner + authenticator)\n")

	// Initialize providers
	email, storage, err := initProviders(ctx, cfg)
	if err != nil {
		return err
	}
	if email != nil {
		fmt.Printf("  \u2713 Email provider: %s\n", cfg.Providers.Email.Type)
	}
	if storage != nil {
		fmt.Printf("  \u2713 Storage provider: %s\n", cfg.Providers.Storage.Type)
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
		ConfigPath: configPath,
	})

	// Create and start engine with HTTP server
	engine := app.NewEngine(cfg, ownerDB, authDB, roles,
		app.WithMode(app.ModeDev),
		app.WithMigrate(true),
		app.WithSeed(true),
		app.WithWatch(!noWatch),
		app.WithConfigPath(configPath),
		app.WithLogger(logger),
		app.WithHTTPServer(httpServer),
	)

	fmt.Printf("\n  API:       http://localhost:%d\n", cfg.Server.Port)
	fmt.Printf("  Dashboard: http://localhost:%d/dashboard\n", cfg.Server.Port)
	fmt.Printf("  Docs:      http://localhost:%d/api/docs\n", cfg.Server.Port)
	fmt.Printf("  OpenAPI:   http://localhost:%d/api/openapi.json\n", cfg.Server.Port)

	if !noWatch {
		fmt.Printf("\n  Watching for changes... (Ctrl+C to stop)\n")
	}

	// Start engine (blocks until shutdown signal)
	return engine.Start(ctx)
}
