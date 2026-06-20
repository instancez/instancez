// Package config handles YAML loading, env var interpolation, and validation.
package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/instancez/instancez/internal/domain"
)

// envVarPattern matches ${VAR} and ${VAR:-default} in strings.
var envVarPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-(.*?))?\}`)

// Load reads a YAML config file, interpolates env vars, and parses it into a Config.
func Load(path string) (*domain.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &domain.ConfigError{Path: path, Message: "cannot read file", Err: err}
	}
	return ParseBytes(data, path)
}

// ParseBytes runs env var interpolation and YAML parse on raw config bytes.
// origin is used for error messages (a path, URL, or other identifier).
func ParseBytes(data []byte, origin string) (*domain.Config, error) {
	interpolated, missing := interpolateEnvVars(string(data))
	if len(missing) > 0 {
		return nil, &domain.MissingEnvError{Vars: missing}
	}

	var cfg domain.Config
	if err := strictUnmarshalYAML([]byte(interpolated), &cfg); err != nil {
		return nil, &domain.ConfigError{Path: origin, Message: "invalid YAML", Err: err}
	}

	applyDefaults(&cfg)
	return &cfg, nil
}

// LoadWithDotenv loads a .env file (if present) into the process environment,
// then loads the config. Used by `inz dev` with `.development.env`.
func LoadWithDotenv(configPath, dotenvPath string) (*domain.Config, error) {
	if err := LoadDotenv(dotenvPath); err != nil {
		return nil, err
	}
	return Load(configPath)
}

// LoadDotenv parses the given .env file (if present) and exports its keys
// into the process environment without overriding real env vars. Returns
// nil if the file does not exist; only malformed files surface an error.
func LoadDotenv(path string) error {
	if err := loadDotenv(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("loading %s: %w", path, err)
	}
	return nil
}

// ForceLoadDotenv is like LoadDotenv but unconditionally updates every key
// found in the file, including ones already set. Used by the dev watcher
// when .development.env changes mid-run so the new values are picked up
// on the next config read/interpolation cycle.
func ForceLoadDotenv(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("loading %s: %w", path, err)
	}
	pairs, err := parseDotenvBytes(data)
	if err != nil {
		return fmt.Errorf("loading %s: %w", path, err)
	}
	for key, val := range pairs {
		if err := os.Setenv(key, val); err != nil {
			return fmt.Errorf("set env %s: %w", key, err)
		}
	}
	return nil
}

// ParseBytesLenient is like ParseBytes but substitutes a harmless placeholder
// for any missing ${VAR} instead of returning MissingEnvError. This lets static
// structural validation run without the runtime environment (used by
// pkg/configvalidate). Values that are present in the environment, and ${VAR:-default}
// defaults, are still honored.
func ParseBytesLenient(data []byte, origin string) (*domain.Config, error) {
	interpolated := interpolateEnvVarsLenient(string(data))
	var cfg domain.Config
	if err := strictUnmarshalYAML([]byte(interpolated), &cfg); err != nil {
		return nil, &domain.ConfigError{Path: origin, Message: "invalid YAML", Err: err}
	}
	applyDefaults(&cfg)
	return &cfg, nil
}

// ParseBytesRaw parses YAML without any env var interpolation — ${VAR} and
// ${VAR:-default} references are preserved as-is. Used by GET /config so
// secret values never transit the dashboard API layer.
func ParseBytesRaw(data []byte, origin string) (*domain.Config, error) {
	var cfg domain.Config
	if err := strictUnmarshalYAML(data, &cfg); err != nil {
		return nil, &domain.ConfigError{Path: origin, Message: "invalid YAML", Err: err}
	}
	applyDefaults(&cfg)
	return &cfg, nil
}

// interpolateEnvVarsLenient resolves ${VAR} from the environment, falls back to
// ${VAR:-default}, and substitutes a placeholder for anything still missing so the
// YAML can be parsed and structurally validated.
func interpolateEnvVarsLenient(input string) string {
	return envVarPattern.ReplaceAllStringFunc(input, func(match string) string {
		groups := envVarPattern.FindStringSubmatch(match)
		name := groups[1]
		hasDefault := strings.Contains(match, ":-")
		if val, ok := os.LookupEnv(name); ok {
			return val
		}
		if hasDefault {
			return groups[2]
		}
		return "placeholder" // missing var: keep parse/validate unblocked
	})
}

// EnvRefs returns the unique names of all ${VAR} references in data, extracted
// with the canonical interpolation pattern. Exposed so external policy checks
// scan exactly what instancez interpolates.
func EnvRefs(data []byte) []string {
	var names []string
	seen := map[string]bool{}
	for _, m := range envVarPattern.FindAllStringSubmatch(string(data), -1) {
		name := m[1]
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

// interpolateEnvVars replaces ${VAR} and ${VAR:-default} references.
// Returns the interpolated string and a list of missing required variables.
func interpolateEnvVars(input string) (string, []string) {
	var missing []string
	seen := make(map[string]bool)

	result := envVarPattern.ReplaceAllStringFunc(input, func(match string) string {
		groups := envVarPattern.FindStringSubmatch(match)
		name := groups[1]
		defaultVal := groups[2]
		hasDefault := strings.Contains(match, ":-")

		val, ok := os.LookupEnv(name)
		if ok {
			return val
		}
		if hasDefault {
			return defaultVal
		}
		if !seen[name] {
			missing = append(missing, name)
			seen[name] = true
		}
		return match // leave as-is so YAML parse can proceed for error reporting
	})

	return result, missing
}

// loadDotenv parses a simple .env file and sets env vars that aren't already set.
// Real env vars always take priority.
func loadDotenv(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	pairs, err := parseDotenvBytes(data)
	if err != nil {
		return err
	}

	for key, val := range pairs {
		// Don't override real env vars
		if _, exists := os.LookupEnv(key); !exists {
			if err := os.Setenv(key, val); err != nil {
				return fmt.Errorf("set env %s: %w", key, err)
			}
		}
	}

	return nil
}

func stripQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// ApplyDefaults fills sensible defaults on a Config that was decoded directly
// (e.g. JSON-unmarshalled by pkg/configvalidate) rather than through one of the
// ParseBytes* loaders. Callers that validate such a Config must run this first
// so Validate sees the same defaulted shape every ParseBytes* path produces —
// otherwise defaultable fields like rpc.security read as "" and are rejected.
func ApplyDefaults(cfg *domain.Config) { applyDefaults(cfg) }

// applyDefaults sets sensible defaults on a parsed Config.
func applyDefaults(cfg *domain.Config) {
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}
	if cfg.Server.MaxBodySize == "" {
		cfg.Server.MaxBodySize = "1MB"
	}
	if cfg.Server.MaxLimit == 0 {
		cfg.Server.MaxLimit = 100
	}
	if cfg.Server.Timeouts.Request == "" {
		cfg.Server.Timeouts.Request = "30s"
	}
	if cfg.Server.Timeouts.DBQuery == "" {
		cfg.Server.Timeouts.DBQuery = "10s"
	}
	if cfg.Server.Timeouts.Upload == "" {
		cfg.Server.Timeouts.Upload = "5m"
	}
	if cfg.Server.Timeouts.Shutdown == "" {
		cfg.Server.Timeouts.Shutdown = "30s"
	}
	if cfg.Database.Pool.Max == 0 {
		cfg.Database.Pool.Max = 20
	}
	if cfg.Database.Pool.Min == 0 {
		cfg.Database.Pool.Min = 5
	}
	if cfg.Database.Pool.IdleTimeout == "" {
		cfg.Database.Pool.IdleTimeout = "300s"
	}

	// Auth defaults
	if cfg.Auth != nil {
		if cfg.Auth.JWTExpiry == "" {
			cfg.Auth.JWTExpiry = "15m"
		}
		if cfg.Auth.RefreshTokenExpiry == "" && cfg.Auth.RefreshTokens {
			cfg.Auth.RefreshTokenExpiry = "7d"
		}
	}

	// RPC: fill defaults and derive ReturnCategory.
	for name, fn := range cfg.RPC {
		if fn.Language == "" {
			fn.Language = "plpgsql"
		}
		if fn.Volatility == "" {
			fn.Volatility = "volatile"
		}
		if fn.Security == "" {
			fn.Security = "invoker"
		}
		fn.ReturnCategory = classifyRPCReturn(fn.Returns.Type)
		cfg.RPC[name] = fn
	}
}

// classifyRPCReturn reduces a raw SQL return type to one of
// {"void", "setof", "scalar"} so the RPC handler can pick the right
// dispatch shape at request time without reparsing strings.
func classifyRPCReturn(raw string) string {
	t := strings.TrimSpace(strings.ToLower(raw))
	if t == "" || t == "void" {
		return "void"
	}
	if strings.HasPrefix(t, "setof ") || strings.HasPrefix(t, "table(") || strings.HasPrefix(t, "table (") {
		return "setof"
	}
	return "scalar"
}
