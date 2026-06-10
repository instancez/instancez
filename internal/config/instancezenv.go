package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const instancezEnvPrefix = "INSTANCEZ_ENV_"

// parseDotenvBytes parses .env file bytes and returns a map of KEY -> VALUE.
// It does NOT set process environment variables (pure parser).
// Lines starting with # or empty lines are skipped.
// An optional "export " prefix on a line is stripped before parsing.
// Values may be surrounded by single or double quotes (which are stripped).
// Returns an error if any non-blank, non-comment line is missing an '=' (malformed).
// This is the single shared parser used by both loadDotenv and LoadInstancezEnv.
func parseDotenvBytes(data []byte) (map[string]string, error) {
	out := make(map[string]string)
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Strip optional "export " prefix (shell-compatible syntax).
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: invalid format (expected KEY=VALUE)", i+1)
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		val = stripQuotes(val)
		out[key] = val
	}
	return out, nil
}

// LoadInstancezEnv builds the in-memory INSTANCEZ_ENV_ namespace from:
//  1. <dir>/.env      — base file, loaded first (missing = not an error)
//  2. <dir>/.<mode>.env — mode-specific overlay (missing = not an error)
//  3. INSTANCEZ_ENV_* keys from os.Environ() — highest precedence
//
// Only keys with the "INSTANCEZ_ENV_" prefix are included in the returned map.
// This function NEVER calls os.Setenv — the map is purely in-memory.
// A malformed line (missing '=') in a .env file is an error (consistent with loadDotenv).
func LoadInstancezEnv(dir, mode string) (map[string]string, error) {
	result := make(map[string]string)

	// Helper: parse a dotenv file and overlay INSTANCEZ_ENV_* keys.
	overlay := func(path string) error {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil // missing file is not an error
			}
			return err
		}
		parsed, err := parseDotenvBytes(data)
		if err != nil {
			return err
		}
		for k, v := range parsed {
			if strings.HasPrefix(k, instancezEnvPrefix) {
				result[k] = v
			}
		}
		return nil
	}

	// 1. Base .env file
	if err := overlay(filepath.Join(dir, ".env")); err != nil {
		return nil, err
	}

	// 2. Mode-specific .env file (e.g. .development.env)
	if mode != "" {
		if err := overlay(filepath.Join(dir, "."+mode+".env")); err != nil {
			return nil, err
		}
	}

	// 3. Process environment — highest precedence
	for _, kv := range os.Environ() {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if strings.HasPrefix(k, instancezEnvPrefix) {
			result[k] = v
		}
	}

	return result, nil
}
