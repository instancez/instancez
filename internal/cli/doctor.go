package cli

import (
	"os"

	"github.com/saedx1/ultrabase/internal/cli/preflight"
	"github.com/saedx1/ultrabase/internal/config"
	"github.com/spf13/cobra"
)

func newDoctorCmd() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check prerequisites for running ultrabase locally",
		Long: `Run a set of preflight checks that verify the environment is ready for
ultra dev and ultra serve: config file validity, database DSNs, and the
required Postgres role layout.

Exits non-zero if any check fails.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor(configPath)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "ultrabase.yaml", "path to ultrabase.yaml")
	return cmd
}

func runDoctor(configPath string) error {
	// Load the development dotenv before querying env vars so DSN checks
	// reflect the same environment that `ultra dev` would see.
	_ = config.LoadDotenv(".development.env")

	checks := doctorChecks(configPath, os.Getenv)
	results := preflight.RunAll(checks)
	preflight.Render(os.Stdout, results)

	if preflight.AnyFailed(results) {
		return errReported
	}
	return nil
}

// doctorChecks builds the full check list used by both runDoctor and
// doctor_test.go (where it is called with injected dependencies).
func doctorChecks(configPath string, lookup func(string) string) []preflight.Check {
	return []preflight.Check{
		preflight.ConfigValidCheck(configPath),
		preflight.DSNPresentCheck(lookup),
	}
}
