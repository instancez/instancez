package cli

import (
	"os"

	"github.com/instancez/instancez/internal/cli/preflight"
	"github.com/instancez/instancez/internal/config"
	"github.com/spf13/cobra"
)

func newDoctorCmd() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check prerequisites for running instancez locally",
		Long: `Run a set of preflight checks that verify the environment is ready for
inz dev: config file validity, the INSTANCEZ_DATABASE_URL superuser DSN,
a live database connection, and the required Postgres role layout.

Exits non-zero if any check fails.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor(configPath)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "instancez.yaml", "path to instancez.yaml")
	return cmd
}

func runDoctor(configPath string) error {
	// Load the development dotenv before querying env vars so DSN checks
	// reflect the same environment that `inz dev` would see.
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
//
// Dependency order: config → superuser DSN present → connect → role layout.
// RunAll runs all checks and collects every failure so the user sees the full
// picture at once.
func doctorChecks(configPath string, lookup func(string) string) []preflight.Check {
	superuserDSN := lookup(preflight.EnvSuperuserDSN)
	return []preflight.Check{
		preflight.ConfigValidCheck(configPath),
		preflight.SuperuserDSNPresentCheck(lookup),
		preflight.ConnectCheck("database connect", superuserDSN),
		preflight.RoleLayoutCheck(preflight.PostgresRoleReporter(superuserDSN)),
	}
}
