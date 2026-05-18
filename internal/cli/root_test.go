package cli

import (
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

func lookupFrom(env map[string]string) func(string) string {
	return func(k string) string { return env[k] }
}

func TestApplyEnvDefaults(t *testing.T) {
	t.Run("sets string flag from generic env", func(t *testing.T) {
		fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
		var config string
		fs.StringVar(&config, "config", "default.yaml", "")

		if _, err := applyEnvDefaults(fs, nil, lookupFrom(map[string]string{"ULTRABASE_CONFIG": "from-env.yaml"})); err != nil {
			t.Fatalf("applyEnvDefaults: %v", err)
		}
		if config != "from-env.yaml" {
			t.Errorf("got %q, want %q", config, "from-env.yaml")
		}
	})

	t.Run("explicit flag wins over env", func(t *testing.T) {
		fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
		var config string
		fs.StringVar(&config, "config", "default.yaml", "")
		_ = fs.Set("config", "explicit.yaml") // marks Changed=true

		if _, err := applyEnvDefaults(fs, nil, lookupFrom(map[string]string{"ULTRABASE_CONFIG": "from-env.yaml"})); err != nil {
			t.Fatalf("applyEnvDefaults: %v", err)
		}
		if config != "explicit.yaml" {
			t.Errorf("got %q, want %q", config, "explicit.yaml")
		}
	})

	t.Run("keeps default when env not set", func(t *testing.T) {
		fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
		var config string
		fs.StringVar(&config, "config", "default.yaml", "")

		if _, err := applyEnvDefaults(fs, nil, lookupFrom(nil)); err != nil {
			t.Fatalf("applyEnvDefaults: %v", err)
		}
		if config != "default.yaml" {
			t.Errorf("got %q, want %q", config, "default.yaml")
		}
	})

	t.Run("hyphenated flag maps to underscored env var", func(t *testing.T) {
		fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
		var allow bool
		fs.BoolVar(&allow, "allow-destructive", false, "")

		if _, err := applyEnvDefaults(fs, nil, lookupFrom(map[string]string{"ULTRABASE_ALLOW_DESTRUCTIVE": "true"})); err != nil {
			t.Fatalf("applyEnvDefaults: %v", err)
		}
		if !allow {
			t.Error("expected allow-destructive=true from env")
		}
	})

	t.Run("sets int flag from env", func(t *testing.T) {
		fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
		var port int
		fs.IntVar(&port, "port", 0, "")

		if _, err := applyEnvDefaults(fs, nil, lookupFrom(map[string]string{"ULTRABASE_PORT": "9090"})); err != nil {
			t.Fatalf("applyEnvDefaults: %v", err)
		}
		if port != 9090 {
			t.Errorf("got %d, want 9090", port)
		}
	})

	t.Run("alias precedence: first non-empty wins", func(t *testing.T) {
		fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
		var config string
		fs.StringVar(&config, "config", "default.yaml", "")

		setBy, err := applyEnvDefaults(fs,
			map[string][]string{"config": {"ULTRABASE_CONFIG_SOURCE", "ULTRABASE_CONFIG"}},
			lookupFrom(map[string]string{
				"ULTRABASE_CONFIG_SOURCE": "primary.yaml",
				"ULTRABASE_CONFIG":        "legacy.yaml",
			}))
		if err != nil {
			t.Fatalf("applyEnvDefaults: %v", err)
		}
		if config != "primary.yaml" {
			t.Errorf("got %q, want primary.yaml", config)
		}
		if setBy["config"] != "ULTRABASE_CONFIG_SOURCE" {
			t.Errorf("setBy[config] = %q, want ULTRABASE_CONFIG_SOURCE", setBy["config"])
		}
	})

	t.Run("empty alias slice disables env binding", func(t *testing.T) {
		fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
		var verbose bool
		fs.BoolVar(&verbose, "verbose", false, "")

		if _, err := applyEnvDefaults(fs,
			map[string][]string{"verbose": {}},
			lookupFrom(map[string]string{"ULTRABASE_VERBOSE": "true"})); err != nil {
			t.Fatalf("applyEnvDefaults: %v", err)
		}
		if verbose {
			t.Error("verbose should stay false: env binding disabled")
		}
	})

	t.Run("invalid bool env reports the env var name", func(t *testing.T) {
		fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
		var watch bool
		fs.BoolVar(&watch, "watch", false, "")

		_, err := applyEnvDefaults(fs,
			map[string][]string{"watch": {"ULTRABASE_CONFIG_WATCH"}},
			lookupFrom(map[string]string{"ULTRABASE_CONFIG_WATCH": "garbage"}))
		if err == nil {
			t.Fatal("expected error for invalid bool")
		}
		if !strings.Contains(err.Error(), "ULTRABASE_CONFIG_WATCH") {
			t.Errorf("error %q should name ULTRABASE_CONFIG_WATCH", err.Error())
		}
	})
}
