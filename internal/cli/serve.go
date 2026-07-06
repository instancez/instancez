package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/instancez/instancez/dashboard"
	"github.com/instancez/instancez/internal/adapter/funcs"
	instancezhttp "github.com/instancez/instancez/internal/adapter/http"
	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/cli/preflight"
	"github.com/instancez/instancez/internal/config"
	"github.com/instancez/instancez/internal/domain"
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

	extractParent := filepath.Join(os.TempDir(), "instancez-functions")

	// --- config source ---
	//
	// Bundle mode (--bundle): config comes from the bundle archive. A single
	// S3 object is both config source and function code, eliminating the
	// ordering race between config and bundle files. No local instancez.yaml
	// required.
	//
	// Standard mode (--config): the existing file/S3 source path.

	var bundleSource *BundleSource
	var source config.Source

	if opts.bundlePath != "" {
		bundleSource = NewBundleSource(opts.bundlePath, extractParent)
		source = bundleSource
	} else {
		if r, failed := preflight.RunUntilFail([]preflight.Check{
			preflight.ConfigValidCheck(opts.configPath),
		}); failed {
			fmt.Fprintf(os.Stderr, "  ✗ %s — %s\n    hint: %s\n", r.Name, r.Detail, r.FixHint)
			return errReported
		}

		if err := requireConfigFile(opts.configPath); err != nil {
			return err
		}

		var err error
		source, err = config.NewSource(opts.configPath)
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
	}

	cfg, err := source.Load(ctx)
	if err != nil {
		return err
	}

	// Enforce the INSTANCEZ_ENV_ namespace only for local (user-authored)
	// configs; s3:// sources are generator-produced and reference platform vars.
	if _, ok := source.(*config.FileSource); ok {
		if raw, err := os.ReadFile(opts.configPath); err == nil {
			if errs := config.ValidateEnvNamespace(raw); errs != nil {
				return printPrettyErrors(errs)
			}
		}
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

	logger.Info("starting instancez",
		"version", version,
		"mode", "production",
		"config_source", source.Describe())
	logger.Info("schema valid")
	logger.Info("config resolved",
		"watch", opts.watch,
		"watch_interval", opts.watchInterval.String(),
		"dashboard", opts.dashboard.String())

	logger.Warn("instancez is designed for single-replica deployments; multi-replica support is planned")

	// Connect to database (owner + authenticator pools).
	ownerDB, authDB, roles, err := dbConnections(ctx, cfg.Database.Pool, "")
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	logger.Info("connected to postgres", "pools", "owner+authenticator")

	// Initialize providers
	email, storage, err := initProviders(ctx, cfg)
	if err != nil {
		return err
	}

	// Provider health checks
	if storage != nil && len(cfg.Storage) > 0 {
		if err := checkStorageHealth(ctx, storage, cfg, logger); err != nil {
			return fmt.Errorf("storage health: %w", err)
		}
		logger.Info("storage buckets verified")
	}

	// Only expose a local config path to the admin handler when the source is
	// a local file. For S3 and bundle sources the admin config endpoints return
	// 501.
	var adminConfigPath string
	if fs, ok := source.(*config.FileSource); ok {
		adminConfigPath = fs.Path
	}

	km := app.NewJWTKeyManager(ownerDB)

	// Function runtime (serve consumes the pre-built bundle; it NEVER builds).
	// The runtime is wrapped in a SwapRuntime so the config watcher can
	// hot-swap a new bundle version without recreating the HTTP handler.
	var funcRuntime domain.FunctionRuntime
	var swapRT *funcs.SwapRuntime
	if bundleSource != nil {
		// Bundle mode: always create a SwapRuntime even when the initial bundle
		// has no functions, so a reload that adds functions can start the runtime
		// without requiring a restart.
		var rt *funcs.Runtime
		if len(cfg.Functions) > 0 {
			bundleDir := bundleSource.ExtractedDir()
			rt, err = buildBundleFuncRuntime(ctx, cfg, bundleDir, km, logger)
			if err != nil {
				return err
			}
			logger.Info("function runtime ready", "functions", len(cfg.Functions), "bundle_dir", bundleDir)
		}
		swapRT = funcs.NewSwapRuntime(rt)
		funcRuntime = swapRT
		defer func() { _ = swapRT.Close() }()
	} else if len(cfg.Functions) > 0 {
		var rt *funcs.Runtime
		rt, _, err = buildServeFuncRuntime(ctx, cfg, filepath.Dir(adminConfigPath), extractParent, km, logger)
		if err != nil {
			return err
		}
		logger.Info("function runtime ready", "functions", len(cfg.Functions), "bundle", cfg.FunctionsBundle)
		swapRT = funcs.NewSwapRuntime(rt)
		funcRuntime = swapRT
		defer func() { _ = swapRT.Close() }()
	}

	// Create HTTP server. The Drift/Config closures capture `engine` (declared
	// below) so the handlers see live engine state once Start has run; before
	// Start they fall back to nil/cfg.
	var engine *app.Engine
	httpServer := instancezhttp.NewServer(instancezhttp.ServerDeps{
		Config:          cfg,
		DB:              authDB,
		OwnerDB:         ownerDB,
		Logger:          logger,
		DevMode:         false,
		Email:           email,
		Storage:         storage,
		JWTKeys:         km,
		FunctionRuntime: funcRuntime,
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
		UpdateConfigFn: func(c *domain.Config) {
			if engine != nil {
				engine.SetConfig(c)
			}
		},
		DotenvWritable: opts.dotenvWritable,
		DotenvPath:     opts.dotenvPath,
	})

	engineOpts := []app.EngineOption{
		app.WithMode(app.ModeProd),
		app.WithMigrate(opts.migrate),
		app.WithAllowDestructive(opts.allowDestructive),
		app.WithWatch(opts.watch),
		app.WithWatchInterval(opts.watchInterval),
		app.WithLogger(logger),
		app.WithHTTPServer(httpServer),
		app.WithConfigSource(source),
	}

	// Hot-reload: when the watcher detects a new bundle version, rebuild the
	// function runtime and atomically swap it in.
	if swapRT != nil {
		if bundleSource != nil {
			// Bundle mode: Watch has already extracted the new bundle before
			// emitting the WatchEvent, so ExtractedDir() points at the new tree
			// by the time onFunctionReload is called.
			engineOpts = append(engineOpts, app.WithFunctionReload(func(newCfg *domain.Config) {
				newDir := bundleSource.ExtractedDir()
				logger.Info("function bundle changed; reloading runtime", "new_dir", newDir)
				newRT, rerr := buildBundleFuncRuntime(ctx, newCfg, newDir, km, logger)
				if rerr != nil {
					logger.Error("function bundle reload failed; keeping previous runtime", "error", rerr)
					return
				}
				if old := swapRT.Swap(newRT); old != nil {
					if cerr := old.Close(); cerr != nil {
						logger.Warn("closing previous function runtime", "error", cerr)
					}
				}
				logger.Info("function runtime reloaded", "bundle_dir", newDir)
			}))
		} else {
			// Standard mode: the functions bundle pointer is embedded in the config.
			// Reload when the pointer changes.
			lastBundle := cfg.FunctionsBundle
			engineOpts = append(engineOpts, app.WithFunctionReload(func(newCfg *domain.Config) {
				if newCfg.FunctionsBundle == "" || newCfg.FunctionsBundle == lastBundle {
					return
				}
				logger.Info("function bundle changed; reloading", "old", lastBundle, "new", newCfg.FunctionsBundle)
				newRT, _, rerr := buildServeFuncRuntime(ctx, newCfg, filepath.Dir(adminConfigPath), extractParent, km, logger)
				if rerr != nil {
					logger.Error("function bundle reload failed; keeping previous runtime", "error", rerr)
					return
				}
				lastBundle = newCfg.FunctionsBundle
				if old := swapRT.Swap(newRT); old != nil {
					if cerr := old.Close(); cerr != nil {
						logger.Warn("closing previous function runtime", "error", cerr)
					}
				}
				logger.Info("function runtime reloaded", "bundle", newCfg.FunctionsBundle)
			}))
		}
	}

	// Create engine with HTTP server
	engine = app.NewEngine(cfg, ownerDB, authDB, roles, engineOpts...)

	logger.Info("listening", "port", cfg.Server.Port)

	return engine.Start(ctx)
}
