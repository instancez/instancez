package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/instancez/instancez/dashboard"
	instancezhttp "github.com/instancez/instancez/internal/adapter/http"
	"github.com/instancez/instancez/internal/adapter/funcs"
	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/cli/preflight"
	"github.com/instancez/instancez/internal/config"
	"github.com/instancez/instancez/internal/domain"
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
	// dev runs against a local file + local DB; reject remote config sources
	// before any preflight work so the failure is clear and immediate.
	if err := requireLocalConfig(opts.configPath); err != nil {
		return err
	}

	// Embedded Postgres: start before LoadDotenv so INSTANCEZ_DATABASE_URL is
	// already set when loadDotenv runs (loadDotenv skips keys already in env).
	if opts.dbSrc == DevDBSourceEmbedded {
		fmt.Println("  Starting embedded Postgres 16...")
		stop, superuserDSN, err := startEmbeddedPostgres(opts)
		if err != nil {
			return err
		}
		defer stop()
		if err := os.Setenv("INSTANCEZ_DATABASE_URL", superuserDSN); err != nil {
			return fmt.Errorf("set INSTANCEZ_DATABASE_URL: %w", err)
		}
		fmt.Println("  ✓ Embedded Postgres ready")
	}

	// Preflight: load dev dotenv first so DSN env vars are visible, then run
	// fail-fast checks before any expensive work.
	_ = config.LoadDotenv(".development.env")

	if generated, err := ensureAdminKey(".development.env"); err != nil {
		return fmt.Errorf("ensure admin key: %w", err)
	} else if generated {
		fmt.Println("generated INSTANCEZ_ADMIN_KEY — see .development.env")
	}

	if r, failed := preflight.RunUntilFail([]preflight.Check{
		preflight.ConfigValidCheck(opts.configPath),
		preflight.SuperuserDSNPresentCheck(os.Getenv),
	}); failed {
		fmt.Fprintf(os.Stderr, "  ✗ %s — %s\n    hint: %s\n", r.Name, r.Detail, r.FixHint)
		return errReported
	}

	cfg, err := config.LoadWithDotenv(opts.configPath, ".development.env")
	if err != nil {
		return err
	}

	errs := config.Validate(cfg)
	if errs != nil {
		return printPrettyErrors(errs)
	}

	// Build a Source handle for the engine + http deps.
	// In dev mode we also watch .development.env so changes to env vars are
	// picked up without a server restart.
	source := config.NewFileSourceWithEnv(opts.configPath, ".development.env")

	if opts.port > 0 {
		cfg.Server.Port = opts.port
	}

	// Set up logger
	level := slog.LevelInfo
	if opts.verbose {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	fmt.Printf("  instancez v%s\n\n", version)
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

	km := app.NewJWTKeyManager(ownerDB)

	// Function runtime (dev builds: `npm ci` runs in functions/ when a
	// package.json is present, then a worker pool is spawned pointing at the
	// project tree). Nil when no functions are declared.
	funcRT, err := buildDevFuncRuntime(ctx, cfg, opts.configPath, km, logger)
	if err != nil {
		return err
	}
	swap := funcs.NewSwapRuntime(funcRT)
	defer func() { _ = swap.Close() }()
	var funcRuntime domain.FunctionRuntime = swap
	if funcRT != nil {
		fmt.Printf("  ✓ Functions: %d (runtime ready)\n", len(cfg.Functions))
	}

	// reloadFuncs rebuilds workers from updated code/config without npm ci.
	reloadFuncs := func(newCfg *domain.Config) {
		rt, err := buildDevFuncRuntimeFast(ctx, newCfg, opts.configPath, km, logger)
		if err != nil {
			logger.Error("functions: reload failed", "error", err)
			return
		}
		prev := swap.Swap(rt)
		if prev != nil {
			_ = prev.Close()
		}
		logger.Info("functions: reloaded", "count", len(newCfg.Functions))
	}

	// Create HTTP server. The Drift/Config closures capture `engine` (declared
	// below) so handlers see live engine state once Start has run; before
	// Start they fall back to nil/cfg.
	var engine *app.Engine
	httpServer := instancezhttp.NewServer(instancezhttp.ServerDeps{
		Config:          cfg,
		DB:              authDB,
		OwnerDB:         ownerDB,
		Logger:          logger,
		DevMode:         true,
		Email:           email,
		Storage:         storage,
		JWTKeys:         km,
		FunctionRuntime: funcRuntime,
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
		UpdateConfigFn: func(c *domain.Config) {
			if engine != nil {
				engine.SetConfig(c)
			}
		},
		DotenvWritable: opts.dotenvWritable,
		DotenvPath:     opts.dotenvPath,
	})

	// Create and start engine with HTTP server
	engine = app.NewEngine(cfg, ownerDB, authDB, roles,
		app.WithMode(app.ModeDev),
		app.WithMigrate(true),
		app.WithWatch(opts.watch),
		app.WithWatchInterval(opts.watchInterval),
		app.WithConfigPath(opts.configPath),
		app.WithConfigSource(source),
		app.WithLogger(logger),
		app.WithHTTPServer(httpServer),
		app.WithFunctionReload(reloadFuncs),
	)

	fmt.Printf("\n  API:       http://localhost:%d\n", cfg.Server.Port)
	if opts.dashboard != DashboardDisabled {
		fmt.Printf("  Dashboard: http://localhost:%d/dashboard\n", cfg.Server.Port)
	}
	fmt.Printf("  Docs:      http://localhost:%d/api/docs\n", cfg.Server.Port)
	fmt.Printf("  OpenAPI:   http://localhost:%d/api/openapi.json\n", cfg.Server.Port)

	if opts.watch {
		fmt.Printf("\n  Watching for changes... (Ctrl+C to stop)\n")
		// Watch the functions/ directory for JS/TS code edits that don't
		// touch instancez.yaml — those won't trigger the config watcher.
		functionsDir := filepath.Join(filepath.Dir(opts.configPath), "functions")
		if _, err := os.Stat(functionsDir); err == nil {
			go watchFunctionsDir(ctx, functionsDir, cfg, reloadFuncs, logger)
		}
	}

	// Start engine (blocks until shutdown signal)
	return engine.Start(ctx)
}

// watchFunctionsDir watches a functions directory and calls reload whenever
// any file inside it changes. It uses a 300ms debounce to coalesce saves.
func watchFunctionsDir(
	ctx context.Context,
	dir string,
	cfg *domain.Config,
	reload func(*domain.Config),
	logger *slog.Logger,
) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		logger.Warn("functions watcher: init failed", "error", err)
		return
	}
	defer func() { _ = w.Close() }()

	if err := w.Add(dir); err != nil {
		logger.Warn("functions watcher: watch failed", "dir", dir, "error", err)
		return
	}

	var debounce *time.Timer
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}
			if debounce != nil {
				debounce.Stop()
			}
			debounce = time.AfterFunc(300*time.Millisecond, func() {
				logger.Info("functions: code change detected, reloading", "file", filepath.Base(ev.Name))
				reload(cfg)
			})
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			logger.Warn("functions watcher: error", "error", err)
		}
	}
}
