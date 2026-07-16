package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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

	// Embedded Postgres owns a throwaway local database; its superuser DSN is
	// threaded straight into dbConnections below. We deliberately do not read or
	// write any DSN env var for it. Scoped DSNs (or a stale superuser DSN) left
	// in the shell or .development.env from an earlier external-Postgres setup
	// must not redirect the instance at the wrong database.
	var embeddedSuperuserDSN string
	if opts.dbSrc == DevDBSourceEmbedded {
		fmt.Printf("  Starting embedded Postgres 16...\n")
		stop, superuserDSN, err := startEmbeddedPostgres(opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ embedded Postgres failed to start: %v\n    hint: %s\n", err, embeddedPGHint(err))
			return errReported
		}
		defer stop()
		embeddedSuperuserDSN = superuserDSN
	}

	// Preflight: load dev dotenv first so DSN env vars are visible, then run
	// fail-fast checks before any expensive work.
	_ = config.LoadDotenv(".development.env")

	if generated, err := ensureAPIKeys(".development.env"); err != nil {
		return fmt.Errorf("ensure api keys: %w", err)
	} else if generated {
		fmt.Println("generated INSTANCEZ_PUBLISHABLE_KEY + INSTANCEZ_SECRET_KEY — see .development.env")
	}

	// The superuser-DSN presence check only applies to the env-driven DSN path;
	// embedded Postgres supplies its own DSN directly.
	checks := []preflight.Check{preflight.ConfigValidCheck(opts.configPath)}
	if embeddedSuperuserDSN == "" {
		checks = append(checks, preflight.SuperuserDSNPresentCheck(os.Getenv))
	}
	if r, failed := preflight.RunUntilFail(checks); failed {
		fmt.Fprintf(os.Stderr, "  ✗ %s — %s\n    hint: %s\n", r.Name, r.Detail, r.FixHint)
		return errReported
	}

	cfg, err := config.LoadWithDotenv(opts.configPath, ".development.env")
	if err != nil {
		return err
	}

	raw, _ := os.ReadFile(opts.configPath)
	errs := config.Validate(cfg)
	errs = append(errs, config.ValidateEnvNamespace(raw)...)
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

	// Gather startup facts for the sectioned banner printed just before serving.
	banner := devBanner{embedded: embeddedSuperuserDSN != "", functions: -1, cfg: cfg, opts: opts}

	ctx := context.Background()
	ownerDB, authDB, roles, err := dbConnections(ctx, cfg.Database.Pool, embeddedSuperuserDSN)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	// Initialize providers
	email, storage, err := initProviders(ctx, cfg)
	if err != nil {
		return err
	}
	if email != nil {
		banner.email = cfg.Providers.Email.Type
	}
	if storage != nil {
		banner.storage = cfg.Providers.Storage.Type
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
		banner.functions = len(cfg.Functions)
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

	banner.print()

	if opts.watch {
		// Watch the functions/ directory for JS/TS code edits that don't
		// touch instancez.yaml — those won't trigger the config watcher.
		// Create it if absent so the watcher arms even for a project that
		// starts with no functions: the dashboard can add one at runtime, and
		// its .js write must be seen. Reload against the engine's live config
		// (not the boot snapshot) so that added function is picked up: the
		// create path adds the config entry via PUT /config before the write.
		functionsDir := filepath.Join(filepath.Dir(opts.configPath), "functions")
		if err := os.MkdirAll(functionsDir, 0o755); err != nil {
			logger.Warn("functions watcher: could not ensure dir", "dir", functionsDir, "error", err)
		} else {
			go watchFunctionsDir(ctx, functionsDir, engine.Config, reloadFuncs, logger)
		}
	}

	// Start engine (blocks until shutdown signal)
	return engine.Start(ctx)
}

// devBanner holds the facts gathered during startup so the human-facing
// summary can be printed as one grouped, sectioned block rather than a running
// commentary interleaved with the structured logger.
type devBanner struct {
	embedded  bool   // embedded Postgres was started
	email     string // email provider type, "" if none
	storage   string // storage provider type, "" if none
	functions int    // code functions loaded, -1 if the runtime is absent
	cfg       *domain.Config
	opts      devOptions
}

// print writes the sectioned dev startup summary to stdout.
func (b devBanner) print() { b.write(os.Stdout) }

// write renders the sectioned dev startup summary to w.
func (b devBanner) write(w io.Writer) {
	port := b.cfg.Server.Port
	// The banner is a best-effort write to stdout; a write error here isn't
	// actionable, so swallow it once here rather than at every line.
	pf := func(format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }

	pf("\n  instancez v%s\n", version)

	pf("\n  Database\n")
	if b.embedded {
		pf("    ✓ Embedded Postgres 16 ready\n")
	}
	pf("    ✓ Connected (owner + authenticator)\n")

	pf("\n  Schema\n")
	pf("    ✓ Valid\n")
	pf("    Tables           %d\n", len(b.cfg.Tables))
	pf("    Storage buckets  %d\n", len(b.cfg.Storage))
	pf("    RPC functions    %d\n", len(b.cfg.RPC))
	if b.cfg.Auth != nil {
		pf("    Auth             enabled\n")
	} else {
		pf("    Auth             disabled\n")
	}

	if b.storage != "" || b.email != "" {
		pf("\n  Providers\n")
		if b.storage != "" {
			pf("    ✓ Storage: %s\n", b.storage)
		}
		if b.email != "" {
			pf("    ✓ Email: %s\n", b.email)
		}
	}

	if b.functions >= 0 {
		pf("\n  Functions\n")
		pf("    ✓ %d function(s) · runtime ready\n", b.functions)
	}

	pf("\n  API Keys\n")
	pf("    Publishable  %s\n", os.Getenv("INSTANCEZ_PUBLISHABLE_KEY"))
	pf("    Secret       %s\n", os.Getenv("INSTANCEZ_SECRET_KEY"))

	pf("\n  Server\n")
	pf("    API        http://localhost:%d\n", port)
	if b.opts.dashboard != DashboardDisabled {
		pf("    Dashboard  http://localhost:%d/dashboard\n", port)
	}
	if b.opts.dashboard != DashboardReadwrite {
		pf("    Dashboard mode: %s\n", b.opts.dashboard)
	}

	if b.opts.watch {
		pf("\n  Watching for changes… (Ctrl+C to stop)\n")
	} else {
		pf("\n  Watch disabled\n")
	}
}

// isFunctionRuntimeShim reports whether name is a runtime worker shim
// (".inz-worker-<rand>.mjs"). The runtime writes a fresh shim into functions/
// on every reload, so the dev watcher must ignore it; otherwise each reload
// triggers another. Mirrors the skip in bundle.go.
func isFunctionRuntimeShim(name string) bool {
	return strings.HasPrefix(name, ".inz-worker-") && strings.HasSuffix(name, ".mjs")
}

// watchFunctionsDir watches a functions directory and calls reload whenever a
// source file inside it changes. getConfig supplies the live engine config so a
// function added at runtime is picked up. A 300ms debounce coalesces saves.
func watchFunctionsDir(
	ctx context.Context,
	dir string,
	getConfig func() *domain.Config,
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
			if isFunctionRuntimeShim(filepath.Base(ev.Name)) {
				continue
			}
			if debounce != nil {
				debounce.Stop()
			}
			debounce = time.AfterFunc(300*time.Millisecond, func() {
				logger.Info("functions: code change detected, reloading", "file", filepath.Base(ev.Name))
				reload(getConfig())
			})
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			logger.Warn("functions watcher: error", "error", err)
		}
	}
}
