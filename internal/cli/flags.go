package cli

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	ultrahttp "github.com/saedx1/ultrabase/internal/adapter/http"
	"github.com/spf13/pflag"
)

// DashboardMode controls whether and how the dashboard SPA + config-mutation
// endpoints are served. Set via --dashboard or ULTRABASE_DASHBOARD env var.
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
func (m DashboardMode) HTTP() ultrahttp.DashboardMode {
	switch m {
	case DashboardReadonly:
		return ultrahttp.DashboardReadonly
	case DashboardReadwrite:
		return ultrahttp.DashboardReadwrite
	default:
		return ultrahttp.DashboardDisabled
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
// helpful error that points users at `ultra init`. s3:// sources skip the
// check (the s3 client validates existence at fetch time, and we don't want
// to make HEAD calls just to produce a nicer error message).
func requireConfigFile(path string) error {
	if !isFileSpec(path) {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no %s in this directory; run `ultra init` first", path)
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

// configEnvAliases is the env-var precedence list backing the --config flag:
// the new ULTRABASE_CONFIG_SOURCE name first, the legacy ULTRABASE_CONFIG second.
var configEnvAliases = []string{"ULTRABASE_CONFIG_SOURCE", "ULTRABASE_CONFIG"}

// serveEnvAliases / devEnvAliases give the env-var names that back each flag.
// A flag absent from the map uses the generic ULTRABASE_<FLAG_UPPER_SNAKE>
// rule; a flag mapped to an empty slice has no env binding at all.
var (
	serveEnvAliases = map[string][]string{
		"config":         configEnvAliases,
		"watch":          {"ULTRABASE_CONFIG_WATCH"},
		"watch-interval": {"ULTRABASE_CONFIG_WATCH_INTERVAL"},
		"dashboard":      {"ULTRABASE_DASHBOARD"},
	}
	devEnvAliases = map[string][]string{
		"config":         configEnvAliases,
		"watch":          {"ULTRABASE_CONFIG_WATCH"},
		"watch-interval": {"ULTRABASE_CONFIG_WATCH_INTERVAL"},
		"dashboard":      {"ULTRABASE_DASHBOARD"},
		"no-watch":       {},
		"verbose":        {},
	}
)

// applyEnvDefaults is the single env-var fallback mechanism for the whole CLI.
// For every flag the user did NOT pass explicitly, it sets the flag from the
// first non-empty env var that backs it (see the alias maps above), letting
// pflag do the type parsing. It returns flag-name → env-var-name for the flags
// it set, so callers can attribute downstream validation errors to the env var.
func applyEnvDefaults(fs *pflag.FlagSet, aliases map[string][]string, lookup func(string) string) (map[string]string, error) {
	setBy := map[string]string{}
	var ferr error
	fs.VisitAll(func(f *pflag.Flag) {
		if ferr != nil || f.Changed {
			return
		}
		names, ok := aliases[f.Name]
		if !ok {
			names = []string{"ULTRABASE_" + strings.ToUpper(strings.ReplaceAll(f.Name, "-", "_"))}
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
	loadData         bool
	migrate          bool
	allowDestructive bool

	watch         bool
	watchInterval time.Duration
	dashboard     DashboardMode
}

// serveFlagSet owns the single definition of serve's flags. The cobra command
// registers it via AddFlagSet, and unit tests parse it directly — there is no
// second flag definition to drift against.
type serveFlagSet struct {
	flags *pflag.FlagSet

	port             int
	configPath       string
	loadData         bool
	migrate          bool
	allowDestructive bool
	watch            bool
	watchInterval    time.Duration
	dashboard        string
}

func newServeFlagSet() *serveFlagSet {
	fs := &serveFlagSet{flags: pflag.NewFlagSet("serve", pflag.ContinueOnError)}
	fs.flags.IntVar(&fs.port, "port", 0, "server port (default: from config or 8080)")
	fs.flags.StringVar(&fs.configPath, "config", "ultrabase.yaml", "config source (file path or s3://bucket/key; env: ULTRABASE_CONFIG_SOURCE or ULTRABASE_CONFIG)")
	fs.flags.BoolVar(&fs.loadData, "data", false, "apply CSV data imports on startup")
	fs.flags.BoolVar(&fs.migrate, "migrate", false, "run pending migrations on startup")
	fs.flags.BoolVar(&fs.allowDestructive, "allow-destructive", false, "permit DROP TABLE/COLUMN in migrations")
	fs.flags.BoolVar(&fs.watch, "watch", false, "watch the config source for changes (env: ULTRABASE_CONFIG_WATCH)")
	fs.flags.DurationVar(&fs.watchInterval, "watch-interval", 60*time.Second, "S3-watch poll interval; min 10s (env: ULTRABASE_CONFIG_WATCH_INTERVAL)")
	fs.flags.StringVar(&fs.dashboard, "dashboard", "disabled", "dashboard mode: disabled | readonly | readwrite (env: ULTRABASE_DASHBOARD)")
	fs.flags.SetOutput(io.Discard)
	return fs
}

// resolveServeFlags applies env-var fallbacks to an already-parsed flag set
// and validates the result. It is the single resolution path: the cobra
// command and parseServeFlags (tests) both funnel through here.
func resolveServeFlags(fs *serveFlagSet, lookup func(string) string) (serveOptions, error) {
	setBy, err := applyEnvDefaults(fs.flags, serveEnvAliases, lookup)
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

	if err := checkConfigBackend(fs.configPath); err != nil {
		return serveOptions{}, err
	}

	return serveOptions{
		port:             fs.port,
		configPath:       fs.configPath,
		loadData:         fs.loadData,
		migrate:          fs.migrate,
		allowDestructive: fs.allowDestructive,
		watch:            fs.watch,
		watchInterval:    fs.watchInterval,
		dashboard:        mode,
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

// DevDBSource picks which data-source path `ultra dev` takes.
type DevDBSource int

const (
	DevDBSourceUnset DevDBSource = iota
	DevDBSourceDSN
	DevDBSourceCloud
)

// devOptions are the parsed values for runDev. Same as serveOptions but
// with dev-friendly defaults already filled in.
type devOptions struct {
	serveOptions
	noWatch bool
	verbose bool
	dbSrc   DevDBSource
}

type devFlagSet struct {
	flags         *pflag.FlagSet
	port          int
	configPath    string
	noWatch       bool
	watch         bool
	watchInterval time.Duration
	dashboard     string
	verbose       bool

	useDSN   bool
	useCloud bool
}

func newDevFlagSet() *devFlagSet {
	fs := &devFlagSet{flags: pflag.NewFlagSet("dev", pflag.ContinueOnError)}
	fs.flags.IntVar(&fs.port, "port", 0, "server port (default: from config or 8080)")
	fs.flags.StringVar(&fs.configPath, "config", "ultrabase.yaml", "config source (path or s3://bucket/key)")
	fs.flags.BoolVar(&fs.noWatch, "no-watch", false, "disable hot-reload (alias for --watch=false)")
	fs.flags.BoolVar(&fs.watch, "watch", true, "watch the config source for changes")
	fs.flags.DurationVar(&fs.watchInterval, "watch-interval", 60*time.Second, "S3-watch poll interval; min 10s")
	fs.flags.StringVar(&fs.dashboard, "dashboard", "readwrite", "dashboard mode: disabled | readonly | readwrite")
	fs.flags.BoolVar(&fs.verbose, "verbose", false, "debug logging")
	fs.flags.BoolVar(&fs.useDSN, "use-dsn", false, "deprecated no-op; dev uses the DSN by default")
	_ = fs.flags.MarkHidden("use-dsn")
	_ = fs.flags.MarkDeprecated("use-dsn", "dev now uses the DSN by default; flag is a no-op")
	fs.flags.BoolVar(&fs.useCloud, "use-cloud", false, "run against the cloud project's draft database (requires `ultra init --with-cloud`)")
	fs.flags.SetOutput(io.Discard)
	return fs
}

// resolveDevFlags mirrors resolveServeFlags but with dev defaults: watch on,
// dashboard readwrite, migrate+seed always on, plus the --no-watch alias.
// When no --use-* flag is given, dev defaults to the DSN path.
func resolveDevFlags(fs *devFlagSet, lookup func(string) string) (devOptions, error) {
	setBy, err := applyEnvDefaults(fs.flags, devEnvAliases, lookup)
	if err != nil {
		return devOptions{}, err
	}

	dbSrc := DevDBSourceDSN
	if fs.useCloud {
		dbSrc = DevDBSourceCloud
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
			port:          fs.port,
			configPath:    fs.configPath,
			migrate:       true, // dev always migrates
			loadData:      true, // dev always seeds
			watch:         watch,
			watchInterval: fs.watchInterval,
			dashboard:     mode,
		},
		noWatch: fs.noWatch,
		verbose: fs.verbose,
		dbSrc:   dbSrc,
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
