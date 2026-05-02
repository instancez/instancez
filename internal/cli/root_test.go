package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestApplyEnvFlags(t *testing.T) {
	t.Run("sets string flag from env", func(t *testing.T) {
		cmd := &cobra.Command{}
		var config string
		cmd.Flags().StringVar(&config, "config", "default.yaml", "")

		t.Setenv("ULTRABASE_CONFIG", "from-env.yaml")
		applyEnvFlags(cmd)

		if config != "from-env.yaml" {
			t.Errorf("got %q, want %q", config, "from-env.yaml")
		}
	})

	t.Run("explicit flag wins over env", func(t *testing.T) {
		cmd := &cobra.Command{}
		var config string
		cmd.Flags().StringVar(&config, "config", "default.yaml", "")
		_ = cmd.Flags().Set("config", "explicit.yaml") // marks Changed=true

		t.Setenv("ULTRABASE_CONFIG", "from-env.yaml")
		applyEnvFlags(cmd)

		if config != "explicit.yaml" {
			t.Errorf("got %q, want %q", config, "explicit.yaml")
		}
	})

	t.Run("keeps default when env not set", func(t *testing.T) {
		cmd := &cobra.Command{}
		var config string
		cmd.Flags().StringVar(&config, "config", "default.yaml", "")

		t.Setenv("ULTRABASE_CONFIG", "")
		applyEnvFlags(cmd)

		if config != "default.yaml" {
			t.Errorf("got %q, want %q", config, "default.yaml")
		}
	})

	t.Run("hyphenated flag maps to underscored env var", func(t *testing.T) {
		cmd := &cobra.Command{}
		var allow bool
		cmd.Flags().BoolVar(&allow, "allow-destructive", false, "")

		t.Setenv("ULTRABASE_ALLOW_DESTRUCTIVE", "true")
		applyEnvFlags(cmd)

		if !allow {
			t.Error("expected allow-destructive=true from env")
		}
	})

	t.Run("sets int flag from env", func(t *testing.T) {
		cmd := &cobra.Command{}
		var port int
		cmd.Flags().IntVar(&port, "port", 0, "")

		t.Setenv("ULTRABASE_PORT", "9090")
		applyEnvFlags(cmd)

		if port != 9090 {
			t.Errorf("got %d, want 9090", port)
		}
	})
}
