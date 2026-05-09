package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

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

// serveFlagSet is split out for unit-testable flag parsing without invoking cobra.
type serveFlagSet struct {
	flags *pflag.FlagSet

	port             int
	configPath       string
	loadData         bool
	migrate          bool
	allowDestructive bool

	watch         bool
	watchInterval time.Duration
	dashboard     string
}

func newServeFlagSet() *serveFlagSet {
	fs := &serveFlagSet{flags: pflag.NewFlagSet("serve", pflag.ContinueOnError)}
	fs.flags.IntVar(&fs.port, "port", 0, "")
	fs.flags.StringVar(&fs.configPath, "config", "ultrabase.yaml", "")
	fs.flags.BoolVar(&fs.loadData, "data", false, "")
	fs.flags.BoolVar(&fs.migrate, "migrate", false, "")
	fs.flags.BoolVar(&fs.allowDestructive, "allow-destructive", false, "")
	fs.flags.BoolVar(&fs.watch, "watch", false, "")
	fs.flags.DurationVar(&fs.watchInterval, "watch-interval", 60*time.Second, "")
	fs.flags.StringVar(&fs.dashboard, "dashboard", "disabled", "")
	fs.flags.SetOutput(io.Discard)
	return fs
}

// parseServeFlags parses args (no leading "serve") + applies env-var
// fallbacks for any flag the caller did NOT pass. envLookup is normally
// os.Getenv; tests pass a map-backed function.
func parseServeFlags(args []string, envLookup func(string) string) (serveOptions, error) {
	fs := newServeFlagSet()
	if err := fs.flags.Parse(args); err != nil {
		return serveOptions{}, err
	}

	opts := serveOptions{
		port:             fs.port,
		configPath:       fs.configPath,
		loadData:         fs.loadData,
		migrate:          fs.migrate,
		allowDestructive: fs.allowDestructive,
		watchInterval:    60 * time.Second,
		dashboard:        DashboardDisabled,
	}

	// --config can fall back to ULTRABASE_CONFIG (existing) or ULTRABASE_CONFIG_SOURCE (new).
	if !fs.flags.Changed("config") {
		if v := envLookup("ULTRABASE_CONFIG_SOURCE"); v != "" {
			opts.configPath = v
		} else if v := envLookup("ULTRABASE_CONFIG"); v != "" {
			opts.configPath = v
		}
	}

	if fs.flags.Changed("watch") {
		opts.watch = fs.watch
	} else if v := envLookup("ULTRABASE_CONFIG_WATCH"); v != "" {
		b, err := parseBool(v)
		if err != nil {
			return opts, fmt.Errorf("ULTRABASE_CONFIG_WATCH: %w", err)
		}
		opts.watch = b
	}

	if fs.flags.Changed("watch-interval") {
		opts.watchInterval = fs.watchInterval
	} else if v := envLookup("ULTRABASE_CONFIG_WATCH_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return opts, fmt.Errorf("ULTRABASE_CONFIG_WATCH_INTERVAL: %w", err)
		}
		opts.watchInterval = d
	}
	if opts.watchInterval < minWatchInterval {
		return opts, fmt.Errorf("--watch-interval must be at least %s", minWatchInterval)
	}

	if fs.flags.Changed("dashboard") {
		m, err := parseDashboardMode(fs.dashboard)
		if err != nil {
			return opts, err
		}
		opts.dashboard = m
	} else if v := envLookup("ULTRABASE_DASHBOARD"); v != "" {
		m, err := parseDashboardMode(v)
		if err != nil {
			return opts, fmt.Errorf("ULTRABASE_DASHBOARD: %w", err)
		}
		opts.dashboard = m
	}

	if !isFileSpec(opts.configPath) && !strings.HasPrefix(opts.configPath, "s3://") {
		return opts, fmt.Errorf("unsupported config backend: %s (only file paths and s3:// URIs are supported)", opts.configPath)
	}

	return opts, nil
}

func isFileSpec(s string) bool {
	if strings.Contains(s, "://") {
		return false
	}
	return true
}

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
