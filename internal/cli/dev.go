package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/saedx1/instancez/dashboard"
	instancezhttp "github.com/saedx1/instancez/internal/adapter/http"
	"github.com/saedx1/instancez/internal/app"
	"github.com/saedx1/instancez/internal/cli/preflight"
	"github.com/saedx1/instancez/internal/config"
	"github.com/saedx1/instancez/internal/domain"
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

	// Preflight: load dev dotenv first so DSN env vars are visible, then run
	// fail-fast checks before any expensive work.  We include connectivity
	// and role-layout checks because dev needs a working, fully-bootstrapped
	// database to start successfully.
	_ = config.LoadDotenv(".development.env")

	// Bootstrap the role layout from a superuser DSN when the two role DSNs are
	// not already present. This runs BEFORE preflight: on a fresh superuser-only
	// database the DSN/connect/role checks would otherwise all fail. ensureRoles
	// sets the derived DSNs into the env (so the checks below read them) and
	// persists them to .development.env for the next run.
	if res, err := ensureRoles(context.Background(), os.Getenv("ULTRABASE_DATABASE_URL"), ".development.env"); err != nil {
		return fmt.Errorf("bootstrap roles: %w", err)
	} else if res.Ran {
		fmt.Println("  ✓ Provisioned roles from ULTRABASE_DATABASE_URL (ultrabase_owner + authenticator + anon/authenticated/service_role)")
		fmt.Printf("  ✓ Wrote derived owner + authenticator DSNs to %s\n", res.EnvFile)
		if res.AdminKey != "" {
			fmt.Printf("  ✓ Generated a random admin key for dashboard login (see ULTRABASE_ADMIN_KEY in %s)\n", res.EnvFile)
		}
	}

	if r, failed := preflight.RunUntilFail([]preflight.Check{
		preflight.ConfigValidCheck(opts.configPath),
		preflight.DSNPresentCheck(os.Getenv),
		preflight.OwnerConnectCheck(os.Getenv(preflight.EnvOwnerDSN)),
		preflight.AuthConnectCheck(os.Getenv(preflight.EnvAuthDSN)),
		preflight.RoleLayoutCheck(preflight.PostgresRoleReporter(os.Getenv(preflight.EnvOwnerDSN))),
	}); failed {
		fmt.Fprintf(os.Stderr, "  ✗ %s — %s\n    hint: %s\n", r.Name, r.Detail, r.FixHint)
		return errReported
	}

	switch opts.dbSrc {
	case DevDBSourceCloud:
		return fmt.Errorf("--use-cloud is not yet implemented in this build; omit it to use the DSN")
	case DevDBSourceDSN:
		// default path — env-var DSN
	default:
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

	km := app.NewJWTKeyManager(ownerDB)

	// Function runtime (dev builds: `npm ci` runs in functions/ when a
	// package.json is present, then a worker pool is spawned pointing at the
	// project tree). Nil when no functions are declared.
	var funcRuntime domain.FunctionRuntime
	funcRT, err := buildDevFuncRuntime(ctx, cfg, opts.configPath, km, logger)
	if err != nil {
		return err
	}
	if funcRT != nil {
		funcRuntime = funcRT
		defer funcRT.Close()
		fmt.Printf("  ✓ Functions: %d (runtime ready)\n", len(cfg.Functions))
	}

	// Create HTTP server. The Drift/Config closures capture `engine` (declared
	// below) so handlers see live engine state once Start has run; before
	// Start they fall back to nil/cfg.
	var engine *app.Engine
	httpServer := instancezhttp.NewServer(instancezhttp.ServerDeps{
		Config:          cfg,
		DB:              authDB,
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
