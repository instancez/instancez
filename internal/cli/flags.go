package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	instancezhttp "github.com/instancez/instancez/internal/adapter/http"
	"github.com/spf13/pflag"
)

// DashboardMode controls whether and how the dashboard SPA + config-mutation
// endpoints are served. Set via --dashboard or INSTANCEZ_DASHBOARD env var.
type DashboardMode int

const (
	DashboardDisabled DashboardMode = iota
	DashboardReadonly
	DashboardReadwrite
)

func (m DashboardMode) String() string {
	switch m {
	case DashboardReadonly:
		return "readonly"
	case DashboardReadwrite:
		return "readwrite"
	default:
		return "disabled"
	}
}

// HTTP returns the http-package equivalent of the dashboard mode (avoids
// an import cycle because http does not import cli).
func (m DashboardMode) HTTP() instancezhttp.DashboardMode {
	switch m {
	case DashboardReadonly:
		return instancezhttp.DashboardReadonly
	case DashboardReadwrite:
		return instancezhttp.DashboardReadwrite
	default:
		return instancezhttp.DashboardDisabled
	}
}

func parseDashboardMode(s string) (DashboardMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "disabled":
		return DashboardDisabled, nil
	case "readonly":
		return DashboardReadonly, nil
	case "readwrite":
		return DashboardReadwrite, nil
	default:
		return DashboardDisabled, fmt.Errorf("--dashboard must be one of: disabled, readonly, readwrite (got %q)", s)
	}
}

const minWatchInterval = 10 * time.Second

func parseBool(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "t", "true", "yes", "on":
		return true, nil
	case "0", "f", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean: %q", s)
	}
}

func isFileSpec(s string) bool {
	return !strings.Contains(s, "://")
}

func checkConfigBackend(path string) error {
	if !isFileSpec(path) && !strings.HasPrefix(path, "s3://") {
		return fmt.Errorf("unsupported config backend: %s (only file paths and s3:// URIs are supported)", path)
	}
	return nil
}

// requireConfigFile asserts that a local config path exists, returning a
// helpful error that points users at `inz init`. s3:// sources skip the
// check (the s3 client validates existence at fetch time, and we don't want
// to make HEAD calls just to produce a nicer error message).
func requireConfigFile(path string) error {
	if !isFileSpec(path) {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no %s in this directory; run `inz init` first", path)
		}
		return fmt.Errorf("stat %s: %w", path, err)
	}
	return nil
}

// requireLocalConfig is requireConfigFile for commands that only read config
// from the local filesystem (dev, deploy, status, validate). Unlike
// requireConfigFile — which skips remote specs because serve fetches them via
// the s3 client — this rejects s3:// (and any other non-file) spec up front
// with a clear message, instead of letting the command fall through to
// os.ReadFile and fail later with a confusing ENOENT.
func requireLocalConfig(path string) error {
	if !isFileSpec(path) {
		return fmt.Errorf("this command reads config from the local filesystem and does not support remote sources like %q; pass a local file path", path)
	}
	return requireConfigFile(path)
}

// devNoEnvBinding lists dev flags that intentionally have NO env-var binding:
// no-watch is pure CLI sugar (the env way to disable watching is
// INSTANCEZ_WATCH=false); use-dsn is a deprecated no-op; reset-pg is a
// destructive one-shot action that must always be explicit — an env-var
// binding would risk data loss on every restart. Every other flag resolves
// through applyEnvDefaults' generic INSTANCEZ_<FLAG_UPPER_SNAKE> rule.
var devNoEnvBinding = map[string][]string{
	"no-watch": {},
	"use-dsn":  {},
	"reset-pg": {},
}

// applyEnvDefaults is the single env-var fallback mechanism for the whole CLI.
// For every flag the user did NOT pass explicitly, it sets the flag from the
// first non-empty env var that backs it — either an entry in the aliases map
// argument, or the generic INSTANCEZ_<FLAG_UPPER_SNAKE> name when aliases has
// no entry for the flag (or is nil) — letting pflag do the type parsing. It
// returns flag-name → env-var-name for the flags it set, so callers can
// attribute downstream validation errors to the env var.
func applyEnvDefaults(fs *pflag.FlagSet, aliases map[string][]string, lookup func(string) string) (map[string]string, error) {
	setBy := map[string]string{}
	var ferr error
	fs.VisitAll(func(f *pflag.Flag) {
		if ferr != nil || f.Changed {
			return
		}
		names, ok := aliases[f.Name]
		if !ok {
			names = []string{"INSTANCEZ_" + strings.ToUpper(strings.ReplaceAll(f.Name, "-", "_"))}
		}
		for _, n := range names {
			v := lookup(n)
			if v == "" {
				continue
			}
			// pflag's bool parser is stricter than parseBool; normalize first
			// so env values like "yes"/"on"/"off" keep working.
			if f.Value.Type() == "bool" {
				b, err := parseBool(v)
				if err != nil {
					ferr = fmt.Errorf("%s: %w", n, err)
					return
				}
				v = strconv.FormatBool(b)
			}
			if err := fs.Set(f.Name, v); err != nil {
				ferr = fmt.Errorf("%s: %w", n, err)
				return
			}
			setBy[f.Name] = n
			return
		}
	})
	return setBy, ferr
}

// serveOptions holds the parsed values that runServe needs.
type serveOptions struct {
	port             int
	configPath       string
	migrate          bool
	allowDestructive bool

	watch         bool
	watchInterval time.Duration
	dashboard     DashboardMode

	dotenvWritable bool
	dotenvPath     string
}

// serveFlagSet owns the single definition of serve's flags. The cobra command
// registers it via AddFlagSet, and unit tests parse it directly — there is no
// second flag definition to drift against.
type serveFlagSet struct {
	flags *pflag.FlagSet

	port             int
	configPath       string
	migrate          bool
	allowDestructive bool
	watch            bool
	watchInterval    time.Duration
	dashboard        string
	dotenvWritable   bool
	dotenvPath       string
}

func newServeFlagSet() *serveFlagSet {
	fs := &serveFlagSet{flags: pflag.NewFlagSet("serve", pflag.ContinueOnError)}
	fs.flags.IntVar(&fs.port, "port", 0, "server port (default: from config or 8080)")
	fs.flags.StringVar(&fs.configPath, "config", "instancez.yaml", "config source (file path or s3://bucket/key; env: INSTANCEZ_CONFIG)")
	fs.flags.BoolVar(&fs.migrate, "migrate", false, "run pending migrations on startup")
	fs.flags.BoolVar(&fs.allowDestructive, "allow-destructive", false, "permit DROP TABLE/COLUMN in migrations")
	fs.flags.BoolVar(&fs.watch, "watch", false, "watch the config source for changes (env: INSTANCEZ_WATCH)")
	fs.flags.DurationVar(&fs.watchInterval, "watch-interval", 60*time.Second, "S3-watch poll interval; min 10s (env: INSTANCEZ_WATCH_INTERVAL)")
	fs.flags.StringVar(&fs.dashboard, "dashboard", "disabled", "dashboard mode: disabled | readonly | readwrite (env: INSTANCEZ_DASHBOARD)")
	fs.flags.BoolVar(&fs.dotenvWritable, "dashboard-write-dotenv", false, "allow dashboard to write secrets to a .env file (env: INSTANCEZ_DASHBOARD_WRITE_DOTENV)")
	fs.flags.StringVar(&fs.dotenvPath, "dotenv-path", "", "path to .env file when --dashboard-write-dotenv is set (env: INSTANCEZ_DOTENV_PATH)")
	fs.flags.SetOutput(io.Discard)
	return fs
}

// resolveServeFlags applies env-var fallbacks to an already-parsed flag set
// and validates the result. It is the single resolution path: the cobra
// command and parseServeFlags (tests) both funnel through here.
func resolveServeFlags(fs *serveFlagSet, lookup func(string) string) (serveOptions, error) {
	setBy, err := applyEnvDefaults(fs.flags, nil, lookup)
	if err != nil {
		return serveOptions{}, err
	}

	if fs.watchInterval < minWatchInterval {
		return serveOptions{}, fmt.Errorf("%s must be at least %s", source(setBy, "watch-interval", "--watch-interval"), minWatchInterval)
	}

	mode, err := parseDashboardMode(fs.dashboard)
	if err != nil {
		if s, ok := setBy["dashboard"]; ok {
			return serveOptions{}, fmt.Errorf("%s: %w", s, err)
		}
		return serveOptions{}, err
	}

	if fs.dotenvWritable && fs.dotenvPath == "" {
		return serveOptions{}, fmt.Errorf("--dotenv-path is required when --dashboard-write-dotenv is set")
	}

	if err := checkConfigBackend(fs.configPath); err != nil {
		return serveOptions{}, err
	}

	return serveOptions{
		port:             fs.port,
		configPath:       fs.configPath,
		migrate:          fs.migrate,
		allowDestructive: fs.allowDestructive,
		watch:            fs.watch,
		watchInterval:    fs.watchInterval,
		dashboard:        mode,
		dotenvWritable:   fs.dotenvWritable,
		dotenvPath:       fs.dotenvPath,
	}, nil
}

// parseServeFlags parses args (no leading "serve") then resolves env-var
// fallbacks. envLookup is normally os.Getenv; tests pass a map-backed function.
func parseServeFlags(args []string, envLookup func(string) string) (serveOptions, error) {
	fs := newServeFlagSet()
	if err := fs.flags.Parse(args); err != nil {
		return serveOptions{}, err
	}
	return resolveServeFlags(fs, envLookup)
}

// DevDBSource picks which data-source path `inz dev` takes.
type DevDBSource int

const (
	DevDBSourceUnset DevDBSource = iota
	DevDBSourceDSN
	DevDBSourceEmbedded
)

// devOptions are the parsed values for runDev. Same as serveOptions but
// with dev-friendly defaults already filled in.
type devOptions struct {
	serveOptions
	noWatch   bool
	verbose   bool
	dbSrc     DevDBSource
	pgDataDir string
	resetPG   bool
}

type devFlagSet struct {
	flags          *pflag.FlagSet
	port           int
	configPath     string
	noWatch        bool
	watch          bool
	watchInterval  time.Duration
	dashboard      string
	verbose        bool
	dotenvWritable bool
	dotenvPath     string

	useDSN     bool
	embeddedPG bool
	resetPG    bool
}

func newDevFlagSet() *devFlagSet {
	fs := &devFlagSet{flags: pflag.NewFlagSet("dev", pflag.ContinueOnError)}
	fs.flags.IntVar(&fs.port, "port", 0, "server port (default: from config or 8080)")
	fs.flags.StringVar(&fs.configPath, "config", "instancez.yaml", "config source (file path or s3://bucket/key; env: INSTANCEZ_CONFIG)")
	fs.flags.BoolVar(&fs.noWatch, "no-watch", false, "disable hot-reload (alias for --watch=false)")
	fs.flags.BoolVar(&fs.watch, "watch", true, "watch the config source for changes")
	fs.flags.DurationVar(&fs.watchInterval, "watch-interval", 60*time.Second, "S3-watch poll interval; min 10s")
	fs.flags.StringVar(&fs.dashboard, "dashboard", "readwrite", "dashboard mode: disabled | readonly | readwrite")
	fs.flags.BoolVar(&fs.verbose, "verbose", false, "debug logging")
	fs.flags.BoolVar(&fs.dotenvWritable, "dashboard-write-dotenv", true, "allow dashboard to write secrets to .development.env")
	fs.flags.StringVar(&fs.dotenvPath, "dotenv-path", ".development.env", "path to .env file for dashboard secret writing")
	fs.flags.BoolVar(&fs.useDSN, "use-dsn", false, "deprecated no-op; dev uses the DSN by default")
	_ = fs.flags.MarkHidden("use-dsn")
	_ = fs.flags.MarkDeprecated("use-dsn", "dev now uses the DSN by default; flag is a no-op")
	fs.flags.BoolVar(&fs.embeddedPG, "embedded-pg", false, "start an embedded Postgres 16 (data at ./pgdata/); no external DB needed")
	fs.flags.BoolVar(&fs.resetPG, "reset-pg", false, "wipe ./pgdata/ before starting (requires --embedded-pg)")
	fs.flags.SetOutput(io.Discard)
	return fs
}

// resolveDevFlags mirrors resolveServeFlags but with dev defaults: watch on,
// dashboard readwrite, migrate always on, plus the --no-watch alias.
// When no --use-* flag is given, dev defaults to the DSN path.
func resolveDevFlags(fs *devFlagSet, lookup func(string) string) (devOptions, error) {
	setBy, err := applyEnvDefaults(fs.flags, devNoEnvBinding, lookup)
	if err != nil {
		return devOptions{}, err
	}

	dbSrc := DevDBSourceDSN
	if fs.embeddedPG {
		dbSrc = DevDBSourceEmbedded
	}

	if fs.resetPG && !fs.embeddedPG {
		return devOptions{}, fmt.Errorf("--reset-pg requires --embedded-pg")
	}

	// --no-watch, when explicitly passed, wins over --watch / the env default.
	watch := fs.watch
	if fs.flags.Changed("no-watch") && fs.noWatch {
		watch = false
	}

	if fs.watchInterval < minWatchInterval {
		return devOptions{}, fmt.Errorf("%s must be at least %s", source(setBy, "watch-interval", "--watch-interval"), minWatchInterval)
	}

	mode, err := parseDashboardMode(fs.dashboard)
	if err != nil {
		if s, ok := setBy["dashboard"]; ok {
			return devOptions{}, fmt.Errorf("%s: %w", s, err)
		}
		return devOptions{}, err
	}

	if err := checkConfigBackend(fs.configPath); err != nil {
		return devOptions{}, err
	}

	return devOptions{
		serveOptions: serveOptions{
			port:           fs.port,
			configPath:     fs.configPath,
			migrate:        true, // dev always migrates
			watch:          watch,
			watchInterval:  fs.watchInterval,
			dashboard:      mode,
			dotenvWritable: fs.dotenvWritable,
			dotenvPath:     fs.dotenvPath,
		},
		noWatch:   fs.noWatch,
		verbose:   fs.verbose,
		dbSrc:     dbSrc,
		pgDataDir: func() string {
			if fs.embeddedPG {
				return filepath.Join(filepath.Dir(fs.configPath), "pgdata")
			}
			return ""
		}(),
		resetPG:   fs.resetPG,
	}, nil
}

// parseDevFlags parses dev's flag surface then resolves env-var fallbacks.
func parseDevFlags(args []string, envLookup func(string) string) (devOptions, error) {
	fs := newDevFlagSet()
	if err := fs.flags.Parse(args); err != nil {
		return devOptions{}, err
	}
	return resolveDevFlags(fs, envLookup)
}

// source names the origin of a flag value for error messages: the env var
// that set it, or the CLI flag name when it came from args or the default.
func source(setBy map[string]string, flag, flagName string) string {
	if s, ok := setBy[flag]; ok {
		return s
	}
	return flagName
}
