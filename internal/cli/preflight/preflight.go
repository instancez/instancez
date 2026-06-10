// Package preflight provides composable health checks for the instancez CLI.
// Checks are constructed with injected dependencies so the decision logic can
// be unit-tested without a live database or real environment.
package preflight

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/instancez/instancez/internal/config"
	"github.com/instancez/instancez/internal/domain"
)

// DSN env-var names used by dbConnections.  Exported so tests can reference
// the exact names without duplicating the string literals.
const (
	EnvOwnerDSN = "INSTANCEZ_OWNER_DATABASE_URL"
	EnvAuthDSN  = "INSTANCEZ_AUTH_DATABASE_URL"
)

// expectedRoleNames returns the full set of Postgres role names that an
// instancez-bootstrapped database must contain. The owner login role is the
// hard-coded const from bootstrap.go; the remaining three come from
// domain.DefaultRoles() (overrides via INSTANCEZ_DB_* env vars are not
// reflected here — the check targets the defaults used by `inz init`).
func expectedRoleNames() []string {
	r := domain.DefaultRoles()
	return []string{
		"instancez_owner", // bootstrap.go ownerRole const
		r.Authenticator,
		r.Anon,
		r.Authenticated,
		r.Service,
	}
}

// Result is the outcome of a single preflight check.
type Result struct {
	Name    string
	OK      bool
	Detail  string
	FixHint string
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
				FixHint: "set INSTANCEZ_DATABASE_URL (a superuser DSN) in .development.env and run `inz dev`, or set " + EnvOwnerDSN + " + " + EnvAuthDSN + " directly",
			}
		case owner == "":
			return Result{
				Name:    "DSN env vars present",
				OK:      false,
				Detail:  EnvOwnerDSN + " is not set",
				FixHint: "set INSTANCEZ_DATABASE_URL (a superuser DSN) and run `inz dev`, or set " + EnvOwnerDSN + " in .development.env",
			}
		case auth == "":
			return Result{
				Name:    "DSN env vars present",
				OK:      false,
				Detail:  EnvAuthDSN + " is not set",
				FixHint: "set INSTANCEZ_DATABASE_URL (a superuser DSN) and run `inz dev`, or set " + EnvAuthDSN + " in .development.env",
			}
		default:
			return Result{Name: "DSN env vars present", OK: true}
		}
	}
}

// ---------------------------------------------------------------------------
// ConfigValidCheck
// ---------------------------------------------------------------------------

// configReadTimeout bounds the config fetch so a hanging S3 backend cannot
// stall fail-fast preflight. Local file reads return well within this.
const configReadTimeout = 10 * time.Second

// ConfigSource is the minimal read surface ConfigValidCheck needs. Both
// *config.FileSource and *config.S3Source satisfy it, so the check works
// against local files and s3:// objects alike.
type ConfigSource interface {
	Read(ctx context.Context) ([]byte, string, error)
	Describe() string
}

// ConfigValidCheck returns a Check that reads the config at configPath,
// parses it with lenient env-var interpolation (missing ${VAR} references are
// substituted with a placeholder rather than causing a hard error), and runs
// config.Validate over the result.  Using the lenient parser lets the check
// run safely at the very top of runDev/runServe — before dotenv files are
// loaded — without false-failing on DSN placeholders.
//
// configPath may be a local file path or an s3://bucket/key URI; it is routed
// through config.NewSource so both backends are read the same way.
func ConfigValidCheck(configPath string) Check {
	return func() Result {
		src, err := config.NewSource(configPath)
		if err != nil {
			return Result{
				Name:    "config file valid",
				OK:      false,
				Detail:  err.Error(),
				FixHint: "run `inz init` to create instancez.yaml",
			}
		}
		return ConfigValidCheckSource(src)()
	}
}

// ConfigValidCheckSource is ConfigValidCheck for a pre-built source. Exposed so
// callers that already hold a config.Source (and tests with a fake source) can
// validate without re-deriving the source from a path string.
func ConfigValidCheckSource(src ConfigSource) Check {
	return func() Result {
		ctx, cancel := context.WithTimeout(context.Background(), configReadTimeout)
		defer cancel()

		data, _, err := src.Read(ctx)
		if err != nil {
			fixHint := "run `inz init` to create instancez.yaml"
			if strings.HasPrefix(src.Describe(), "s3://") {
				fixHint = "verify the s3:// object exists and credentials grant s3:GetObject"
			}
			return Result{
				Name:    "config file valid",
				OK:      false,
				Detail:  err.Error(),
				FixHint: fixHint,
			}
		}
		cfg, err := config.ParseBytesLenient(data, src.Describe())
		if err != nil {
			return Result{
				Name:    "config file valid",
				OK:      false,
				Detail:  err.Error(),
				FixHint: "check instancez.yaml for YAML syntax errors",
			}
		}
		if errs := config.Validate(cfg); errs != nil {
			return Result{
				Name:    "config file valid",
				OK:      false,
				Detail:  errs[0].Message,
				FixHint: "run `inz validate` for a full error report",
			}
		}
		return Result{Name: "config file valid", OK: true}
	}
}

// ---------------------------------------------------------------------------
// ConnectCheck
// ---------------------------------------------------------------------------

// connectTimeout is the maximum time allowed when opening a pool + pinging
// the database during a preflight check.
const connectTimeout = 5 * time.Second

// ConnectCheck returns a Check that opens a pgxpool connection to the given
// DSN and verifies it is reachable via Ping.  name is a short label used in
// the Result (e.g. "owner DB connect").  The pool is opened lazily by pgx but
// Ping forces an actual round-trip so a dead database is caught immediately.
//
// If dsn is empty the check fails immediately without attempting a dial — this
// keeps the check fast when called against an environment with no DSNs set
// (e.g. in the doctorChecks membership guard test).
func ConnectCheck(name, dsn string) Check {
	return func() Result {
		if dsn == "" {
			return Result{
				Name:    name,
				OK:      false,
				Detail:  "DSN is empty",
				FixHint: "set INSTANCEZ_DATABASE_URL (a superuser DSN) and run `inz dev`, or set the role DSNs in .development.env",
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
		defer cancel()
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			return Result{
				Name:    name,
				OK:      false,
				Detail:  "could not open pool: " + err.Error(),
				FixHint: "set INSTANCEZ_DATABASE_URL (a superuser DSN) so `inz dev` provisions the database",
			}
		}
		defer pool.Close()
		if err := pool.Ping(ctx); err != nil {
			return Result{
				Name:    name,
				OK:      false,
				Detail:  "could not reach database: " + err.Error(),
				FixHint: "check that Postgres is running and the DSN is correct",
			}
		}
		return Result{Name: name, OK: true}
	}
}

// OwnerConnectCheck is ConnectCheck pre-labelled for the owner DSN.
func OwnerConnectCheck(dsn string) Check {
	return ConnectCheck("owner DB connect", dsn)
}

// AuthConnectCheck is ConnectCheck pre-labelled for the authenticator DSN.
func AuthConnectCheck(dsn string) Check {
	return ConnectCheck("auth DB connect", dsn)
}

// ---------------------------------------------------------------------------
// RoleLayoutCheck
// ---------------------------------------------------------------------------

// RoleReporter is implemented by anything that can report which Postgres roles
// currently exist and which roles have been granted to the authenticator login
// role.  The real implementation queries pg_roles / pg_auth_members; tests
// inject a fake.
type RoleReporter interface {
	ExistingRoles() (map[string]bool, error)
	// AuthenticatorGrants returns the set of role names that have been
	// granted TO the authenticator login role (i.e. roles the authenticator
	// can SET ROLE to).
	AuthenticatorGrants() (map[string]bool, error)
}

// PostgresRoleReporter returns a RoleReporter that queries a live Postgres
// instance reachable via dsn.  Each method opens its own short-lived pool so
// the reporter has no connection state.  Integration-only — not unit-tested.
func PostgresRoleReporter(dsn string) RoleReporter {
	return &pgRoleReporter{dsn: dsn}
}

type pgRoleReporter struct{ dsn string }

func (r *pgRoleReporter) ExistingRoles() (map[string]bool, error) {
	if r.dsn == "" {
		return nil, fmt.Errorf("owner DSN is empty")
	}
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	pool, err := pgxpool.New(ctx, r.dsn)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	defer pool.Close()

	rows, err := pool.Query(ctx, `SELECT rolname FROM pg_roles`)
	if err != nil {
		return nil, fmt.Errorf("query pg_roles: %w", err)
	}
	defer rows.Close()
	result := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan rolname: %w", err)
		}
		result[name] = true
	}
	return result, rows.Err()
}

func (r *pgRoleReporter) AuthenticatorGrants() (map[string]bool, error) {
	if r.dsn == "" {
		return nil, fmt.Errorf("owner DSN is empty")
	}
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	pool, err := pgxpool.New(ctx, r.dsn)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	defer pool.Close()

	// Returns the roles granted TO the 'authenticator' login role.
	const q = `
		SELECT r.rolname
		FROM pg_auth_members m
		JOIN pg_roles r ON r.oid = m.roleid
		JOIN pg_roles a ON a.oid = m.member
		WHERE a.rolname = 'authenticator'`
	rows, err := pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("query pg_auth_members: %w", err)
	}
	defer rows.Close()
	grants := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan rolname: %w", err)
		}
		grants[name] = true
	}
	return grants, rows.Err()
}

// roleLayoutDecision contains the pure decision logic for RoleLayoutCheck.
// It is extracted so it can be unit-tested with injected maps without touching
// a database.  Returns the Check Result for the "role layout" check.
func roleLayoutDecision(existing, grants map[string]bool) Result {
	// Step 1: all five roles must exist.
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
			FixHint: "set INSTANCEZ_DATABASE_URL (a superuser DSN) and run `inz dev` to provision the roles",
		}
	}

	// Step 2: authenticator must be granted the three API roles.
	r := domain.DefaultRoles()
	apiRoles := []string{r.Anon, r.Authenticated, r.Service}
	var notGranted []string
	for _, name := range apiRoles {
		if !grants[name] {
			notGranted = append(notGranted, name)
		}
	}
	if len(notGranted) > 0 {
		detail := "authenticator not granted: "
		for i, g := range notGranted {
			if i > 0 {
				detail += ", "
			}
			detail += g
		}
		return Result{
			Name:    "role layout",
			OK:      false,
			Detail:  detail,
			FixHint: "set INSTANCEZ_DATABASE_URL (a superuser DSN) and run `inz dev` to provision the roles",
		}
	}

	return Result{Name: "role layout", OK: true}
}

// RoleLayoutCheck returns a Check that verifies the five instancez roles
// (instancez_owner, authenticator, anon, authenticated, service_role) exist
// in the database, and that authenticator has been granted the three API roles.
// It uses the injected RoleReporter so the decision logic can be exercised
// without a real database connection.
func RoleLayoutCheck(roles RoleReporter) Check {
	return func() Result {
		existing, err := roles.ExistingRoles()
		if err != nil {
			return Result{
				Name:    "role layout",
				OK:      false,
				Detail:  "could not query pg_roles: " + err.Error(),
				FixHint: "set INSTANCEZ_DATABASE_URL (a superuser DSN) and run `inz dev` to provision the roles",
			}
		}
		grants, err := roles.AuthenticatorGrants()
		if err != nil {
			return Result{
				Name:    "role layout",
				OK:      false,
				Detail:  "could not query pg_auth_members: " + err.Error(),
				FixHint: "set INSTANCEZ_DATABASE_URL (a superuser DSN) and run `inz dev` to provision the roles",
			}
		}
		return roleLayoutDecision(existing, grants)
	}
}
