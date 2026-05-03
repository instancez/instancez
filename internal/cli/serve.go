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

func newServeCmd() *cobra.Command {
	var (
		port             int
		configPath       string
		loadData         bool
		migrate          bool
		allowDestructive bool
	)

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start production server",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(port, configPath, loadData, migrate, allowDestructive)
		},
	}

	configDefault := "ultrabase.yaml"
	if v := os.Getenv("ULTRABASE_CONFIG"); v != "" {
		configDefault = v
	}

	cmd.Flags().IntVar(&port, "port", 0, "server port (default: from config or 8080)")
	cmd.Flags().StringVar(&configPath, "config", configDefault, "config source (file path, s3://bucket/key, or $ULTRABASE_CONFIG)")
	cmd.Flags().BoolVar(&loadData, "data", false, "apply CSV data imports on startup")
	cmd.Flags().BoolVar(&migrate, "migrate", false, "run pending migrations on startup")
	cmd.Flags().BoolVar(&allowDestructive, "allow-destructive", false, "permit DROP TABLE/COLUMN in migrations")
	return cmd
}

func runServe(port int, configPath string, loadData, migrate, allowDestructive bool) error {
	ctx := context.Background()

	source, err := config.NewSource(configPath)
	if err != nil {
		return err
	}

	// serve does NOT load .env (12-factor compliance)
	cfg, err := source.Load(ctx)
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

	// Structured JSON logger for production
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	fmt.Printf("  Ultrabase v%s (production)\n\n", version)
	fmt.Printf("  Config source: %s\n", source.Describe())
	fmt.Printf("  \u2713 Schema valid\n")

	logger.Warn("ultrabase is designed for single-replica deployments; multi-replica support is planned")

	// Connect to database (owner + authenticator pools).
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
		fmt.Printf("  \u2713 Storage buckets verified\n")
	}

	// Only expose a local config path to the admin handler when the source is
	// a local file. For S3 sources the admin config endpoints return 501.
	var adminConfigPath string
	if fs, ok := source.(*config.FileSource); ok {
		adminConfigPath = fs.Path
	}

	// Create HTTP server
	httpServer := ultrahttp.NewServer(ultrahttp.ServerDeps{
		Config:     cfg,
		DB:         authDB,
		Logger:     logger,
		DevMode:    false,
		Email:      email,
		Storage:    storage,
		JWTKeys:    app.NewJWTKeyManager(ownerDB),
		ConfigPath: adminConfigPath,
	})

	// Create engine with HTTP server
	engine := app.NewEngine(cfg, ownerDB, authDB, roles,
		app.WithMode(app.ModeProd),
		app.WithMigrate(migrate),
		app.WithSeed(loadData),
		app.WithAllowDestructive(allowDestructive),
		app.WithWatch(false),
		app.WithLogger(logger),
		app.WithHTTPServer(httpServer),
	)

	fmt.Printf("\n  Listening on :%d\n", cfg.Server.Port)

	return engine.Start(ctx)
}
