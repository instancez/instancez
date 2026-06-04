// Package preflight provides composable health checks for the ultrabase CLI.
// Checks are constructed with injected dependencies so the decision logic can
// be unit-tested without a live database or real environment.
package preflight

import (
	"fmt"
	"io"
	"os"

	"github.com/saedx1/ultrabase/internal/config"
	"github.com/saedx1/ultrabase/internal/domain"
)

// DSN env-var names used by dbConnections.  Exported so tests can reference
// the exact names without duplicating the string literals.
const (
	EnvOwnerDSN = "ULTRABASE_OWNER_DATABASE_URL"
	EnvAuthDSN  = "ULTRABASE_AUTH_DATABASE_URL"
)

// expectedRoleNames returns the full set of Postgres role names that an
// ultrabase-bootstrapped database must contain. The owner login role is the
// hard-coded const from bootstrap.go; the remaining three come from
// domain.DefaultRoles() (overrides via ULTRABASE_DB_* env vars are not
// reflected here — the check targets the defaults used by `ultra init`).
func expectedRoleNames() []string {
	r := domain.DefaultRoles()
	return []string{
		"ultrabase_owner", // bootstrap.go ownerRole const
		r.Authenticator,
		r.Anon,
		r.Authenticated,
		r.Service,
	}
}

// Result is the outcome of a single preflight check.
type Result struct {
	Name     string
	OK       bool
	Detail   string
	FixHint  string
}

// Check is a function that performs one preflight check and returns its Result.
type Check func() Result

// RunAll runs every check in order and returns all results.  It never
// short-circuits: even if an early check fails, all subsequent checks are
// executed so the user sees the full picture at once.
func RunAll(checks []Check) []Result {
	results := make([]Result, len(checks))
	for i, c := range checks {
		results[i] = c()
	}
	return results
}

// RunUntilFail runs checks in order and stops at the first failure.  It
// returns (firstFailure, true) when any check fails, or (zero, false) when
// all checks pass.
func RunUntilFail(checks []Check) (Result, bool) {
	for _, c := range checks {
		r := c()
		if !r.OK {
			return r, true
		}
	}
	return Result{}, false
}

// AnyFailed returns true if at least one result is not OK.
func AnyFailed(results []Result) bool {
	for _, r := range results {
		if !r.OK {
			return true
		}
	}
	return false
}

// Render writes a human-readable summary to w.  Passing checks print a tick,
// failing checks print a cross, their detail, and the fix hint on the next
// line.
func Render(w io.Writer, results []Result) {
	for _, r := range results {
		if r.OK {
			fmt.Fprintf(w, "  ✓ %s\n", r.Name)
		} else {
			fmt.Fprintf(w, "  ✗ %s", r.Name)
			if r.Detail != "" {
				fmt.Fprintf(w, " — %s", r.Detail)
			}
			fmt.Fprintln(w)
			if r.FixHint != "" {
				fmt.Fprintf(w, "    hint: %s\n", r.FixHint)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// DSNPresentCheck
// ---------------------------------------------------------------------------

// DSNPresentCheck returns a Check that verifies the owner and authenticator
// DSN environment variables are non-empty.  The lookup function is injected
// so tests can supply a fake env without os.Setenv.
func DSNPresentCheck(lookup func(string) string) Check {
	return func() Result {
		owner := lookup(EnvOwnerDSN)
		auth := lookup(EnvAuthDSN)
		switch {
		case owner == "" && auth == "":
			return Result{
				Name:    "DSN env vars present",
				OK:      false,
				Detail:  EnvOwnerDSN + " and " + EnvAuthDSN + " are not set",
				FixHint: "run `ultra init --with-dsn <dsn>` or set the env vars in .development.env",
			}
		case owner == "":
			return Result{
				Name:    "DSN env vars present",
				OK:      false,
				Detail:  EnvOwnerDSN + " is not set",
				FixHint: "run `ultra init --with-dsn <dsn>` or set " + EnvOwnerDSN + " in .development.env",
			}
		case auth == "":
			return Result{
				Name:    "DSN env vars present",
				OK:      false,
				Detail:  EnvAuthDSN + " is not set",
				FixHint: "run `ultra init --with-dsn <dsn>` or set " + EnvAuthDSN + " in .development.env",
			}
		default:
			return Result{Name: "DSN env vars present", OK: true}
		}
	}
}

// ---------------------------------------------------------------------------
// ConfigValidCheck
// ---------------------------------------------------------------------------

// ConfigValidCheck returns a Check that reads the config file at configPath,
// parses it with lenient env-var interpolation (missing ${VAR} references are
// substituted with a placeholder rather than causing a hard error), and runs
// config.Validate over the result.  Using the lenient parser lets the check
// run safely at the very top of runDev/runServe — before dotenv files are
// loaded — without false-failing on DSN placeholders.
func ConfigValidCheck(configPath string) Check {
	return func() Result {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return Result{
				Name:    "config file valid",
				OK:      false,
				Detail:  err.Error(),
				FixHint: "run `ultra init` to create ultrabase.yaml",
			}
		}
		cfg, err := config.ParseBytesLenient(data, configPath)
		if err != nil {
			return Result{
				Name:    "config file valid",
				OK:      false,
				Detail:  err.Error(),
				FixHint: "check ultrabase.yaml for YAML syntax errors",
			}
		}
		if errs := config.Validate(cfg); errs != nil {
			return Result{
				Name:    "config file valid",
				OK:      false,
				Detail:  errs[0].Message,
				FixHint: "run `ultra validate` for a full error report",
			}
		}
		return Result{Name: "config file valid", OK: true}
	}
}

// ---------------------------------------------------------------------------
// RoleLayoutCheck
// ---------------------------------------------------------------------------

// RoleReporter is implemented by anything that can report which Postgres roles
// currently exist.  The real implementation queries pg_roles; tests inject a
// fake.
type RoleReporter interface {
	ExistingRoles() (map[string]bool, error)
}

// RoleLayoutCheck returns a Check that verifies the five ultrabase roles
// (ultrabase_owner, authenticator, anon, authenticated, service_role) exist
// in the database.  It uses the injected RoleReporter so the decision logic
// can be exercised without a real database connection.
func RoleLayoutCheck(roles RoleReporter) Check {
	return func() Result {
		existing, err := roles.ExistingRoles()
		if err != nil {
			return Result{
				Name:    "role layout",
				OK:      false,
				Detail:  "could not query pg_roles: " + err.Error(),
				FixHint: "run `ultra init --with-dsn <dsn>` to bootstrap the database",
			}
		}
		var missing []string
		for _, name := range expectedRoleNames() {
			if !existing[name] {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			detail := "missing roles: "
			for i, m := range missing {
				if i > 0 {
					detail += ", "
				}
				detail += m
			}
			return Result{
				Name:    "role layout",
				OK:      false,
				Detail:  detail,
				FixHint: "run `ultra init --with-dsn <dsn>`",
			}
		}
		return Result{Name: "role layout", OK: true}
	}
}
