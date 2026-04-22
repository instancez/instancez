// Package config handles YAML loading, env var interpolation, and validation.
package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/saedx1/ultrabase/internal/domain"
	"gopkg.in/yaml.v3"
)

// envVarPattern matches ${VAR} and ${VAR:-default} in strings.
var envVarPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-(.*?))?\}`)

// Load reads a YAML config file, interpolates env vars, and parses it into a Config.
func Load(path string) (*domain.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &domain.ConfigError{Path: path, Message: "cannot read file", Err: err}
	}
	return parseBytes(data, path)
}

// parseBytes runs env var interpolation and YAML parse on raw config bytes.
// origin is used for error messages (a path, URL, or other identifier).
func parseBytes(data []byte, origin string) (*domain.Config, error) {
	interpolated, missing := interpolateEnvVars(string(data))
	if len(missing) > 0 {
		return nil, &domain.MissingEnvError{Vars: missing}
	}

	var cfg domain.Config
	if err := yaml.Unmarshal([]byte(interpolated), &cfg); err != nil {
		return nil, &domain.ConfigError{Path: origin, Message: "invalid YAML", Err: err}
	}

	applyDefaults(&cfg)
	return &cfg, nil
}

// LoadWithDotenv loads a .env file (if present) into the process environment,
// then loads the config. Used by `ultrabase dev`.
func LoadWithDotenv(configPath, dotenvPath string) (*domain.Config, error) {
	if err := loadDotenv(dotenvPath); err != nil {
		// .env is optional — only error if the file exists but is malformed
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("loading .env: %w", err)
		}
	}
	return Load(configPath)
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

	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("line %d: invalid format (expected KEY=VALUE)", i+1)
		}

		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)

		// Strip surrounding quotes
		val = stripQuotes(val)

		// Don't override real env vars
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, val)
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

	// Search config default
	for name, t := range cfg.Tables {
		if len(t.Searchable) > 0 && t.SearchConfig == "" {
			t.SearchConfig = "english"
			cfg.Tables[name] = t
		}
	}

	// Functions: fill defaults and derive ReturnCategory.
	for name, fn := range cfg.Functions {
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
		cfg.Functions[name] = fn
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
