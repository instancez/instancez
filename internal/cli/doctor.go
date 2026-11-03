package cli

import (
	"io"
	"os"

	"github.com/instancez/instancez/internal/cli/preflight"
	"github.com/instancez/instancez/internal/config"
	"github.com/spf13/cobra"
)

func newDoctorCmd() *cobra.Command {
	var configPath string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check prerequisites for running instancez locally",
		Long: `Run a set of preflight checks that verify the environment is ready for
inz dev: config file validity, the INSTANCEZ_DATABASE_URL superuser DSN,
a live database connection, and the required Postgres role layout.
Database checks are skipped when no DSN is set; "inz dev --embedded-pg" needs none.

Exits non-zero if any check fails.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor(cmd.OutOrStdout(), configPath, jsonOut)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "instancez.yaml", "path to instancez.yaml")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print results as JSON")
	return cmd
}

func runDoctor(w io.Writer, configPath string, jsonOut bool) error {
	// Load the development dotenv before querying env vars so DSN checks
	// reflect the same environment that `inz dev` would see.
	_ = config.LoadDotenv(".development.env")

	checks := doctorChecks(configPath, os.Getenv)
	results := preflight.RunAll(checks)
	if jsonOut {
		if err := renderDoctorJSON(w, results); err != nil {
			return err
		}
	} else {
		preflight.Render(w, results)
	}

	if preflight.AnyFailed(results) {
		return reported("doctor")
	}
	return nil
}

// doctorChecks builds the full check list used by both runDoctor and
// doctor_test.go (where it is called with injected dependencies).
//
// No DSN skips DB checks; role layout runs only after connect succeeds.
func doctorChecks(configPath string, lookup func(string) string) []preflight.Check {
	superuserDSN := lookup(preflight.EnvSuperuserDSN)
	checks := []preflight.Check{
		preflight.ConfigValidCheck(configPath),
		preflight.SuperuserDSNPresentCheck(lookup),
	}
	if superuserDSN == "" {
		return checks
	}
	connected := preflight.ConnectCheck("database connect", superuserDSN)()
	checks = append(checks, func() preflight.Result { return connected })
	if connected.OK {
		checks = append(checks, preflight.RoleLayoutCheck(preflight.PostgresRoleReporter(superuserDSN)))
	}
	return checks
}
