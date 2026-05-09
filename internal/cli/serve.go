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
	"github.com/saedx1/ultrabase/internal/domain"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func newServeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start production server",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := parseServeFlags(extractCobraArgs(cmd.Flags()), os.Getenv)
			if err != nil {
				return err
			}
			return runServe(opts)
		},
	}

	cmd.Flags().Int("port", 0, "server port (default: from config or 8080)")
	cmd.Flags().String("config", "ultrabase.yaml", "config source (file path or s3://bucket/key; env: ULTRABASE_CONFIG_SOURCE or ULTRABASE_CONFIG)")
	cmd.Flags().Bool("data", false, "apply CSV data imports on startup")
	cmd.Flags().Bool("migrate", false, "run pending migrations on startup")
	cmd.Flags().Bool("allow-destructive", false, "permit DROP TABLE/COLUMN in migrations")
	cmd.Flags().Bool("watch", false, "watch the config source for changes (env: ULTRABASE_CONFIG_WATCH)")
	cmd.Flags().Duration("watch-interval", 60*time.Second, "S3-watch poll interval; min 10s (env: ULTRABASE_CONFIG_WATCH_INTERVAL)")
	cmd.Flags().String("dashboard", "disabled", "dashboard mode: disabled | readonly | readwrite (env: ULTRABASE_DASHBOARD)")
	return cmd
}

// extractCobraArgs reproduces only the flags the user explicitly passed,
// so parseServeFlags' env-fallback logic kicks in for unset flags.
func extractCobraArgs(fs *pflag.FlagSet) []string {
	var out []string
	fs.Visit(func(f *pflag.Flag) {
		if f.Value.Type() == "bool" {
			if f.Value.String() == "true" {
				out = append(out, "--"+f.Name)
			} else {
				out = append(out, "--"+f.Name+"=false")
			}
			return
		}
		out = append(out, "--"+f.Name, f.Value.String())
	})
	return out
}

func runServe(opts serveOptions) error {
	ctx := context.Background()

	source, err := config.NewSource(opts.configPath)
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

	if opts.port > 0 {
		cfg.Server.Port = opts.port
	}

	// Structured JSON logger for production
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	fmt.Printf("  Ultrabase v%s (production)\n\n", version)
	fmt.Printf("  Config source: %s\n", source.Describe())
	fmt.Printf("  ✓ Schema valid\n")
	fmt.Printf("  Watch:     %v\n", opts.watch)
	fmt.Printf("  Watch interval: %s\n", opts.watchInterval)
	fmt.Printf("  Dashboard: %s\n", opts.dashboard)

	logger.Warn("ultrabase is designed for single-replica deployments; multi-replica support is planned")

	// Connect to database (owner + authenticator pools).
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
		fmt.Printf("  ✓ Storage buckets verified\n")
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
		Config:        cfg,
		DB:            authDB,
		Logger:        logger,
		DevMode:       false,
		Email:         email,
		Storage:       storage,
		JWTKeys:       app.NewJWTKeyManager(ownerDB),
		ConfigPath:    adminConfigPath,
		DashboardMode: opts.dashboard.HTTP(),
		ConfigSource:  source,
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

	fmt.Printf("\n  Listening on :%d\n", cfg.Server.Port)

	return engine.Start(ctx)
}
