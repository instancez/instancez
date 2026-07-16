package domain

import (
	"context"
	"fmt"
	"regexp"
)

// JWT "role" claim values accepted on the wire (Supabase contract). These
// are wire-format tokens, not Postgres role identifiers — the request pool
// maps each to the corresponding Roles.* identifier when issuing SET LOCAL
// ROLE. Do not rename these to track configurable Roles values.
const (
	JWTRoleAnon          = "anon"
	JWTRoleAuthenticated = "authenticated"
	JWTRoleService       = "service_role"
)

// Roles holds the configurable Postgres role names. Defaults match Supabase
// for supabase-js compatibility. The owner login role is implicit in the
// owner connection URL and not represented here.
type Roles struct {
	Authenticator string // login role used by the request pool; appears in GRANT ... TO
	Anon          string // RLS-enforced role for unauthenticated requests
	Authenticated string // RLS-enforced role for logged-in users
	Service       string // BYPASSRLS role for trusted backend / admin-key requests
	Seed          string // BYPASSRLS role for run_sql seeding; DML on user schemas only. Empty = unset.
}

// DefaultRoles returns the Supabase-compatible defaults.
func DefaultRoles() Roles {
	return Roles{
		Authenticator: "authenticator",
		Anon:          JWTRoleAnon,
		Authenticated: JWTRoleAuthenticated,
		Service:       JWTRoleService,
	}
}

var roleIdentRE = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,62}$`)

// Validate ensures all role names are safe Postgres identifiers and distinct.
func (r Roles) Validate() error {
	pairs := []struct{ name, val string }{
		{"authenticator", r.Authenticator},
		{"anon", r.Anon},
		{"authenticated", r.Authenticated},
		{"service", r.Service},
	}
	seen := make(map[string]string, len(pairs))
	for _, p := range pairs {
		if p.val == "" {
			return fmt.Errorf("role %s is empty", p.name)
		}
		if !roleIdentRE.MatchString(p.val) {
			return fmt.Errorf("role %s = %q is not a valid Postgres identifier", p.name, p.val)
		}
		if other, ok := seen[p.val]; ok {
			return fmt.Errorf("role %s = %q duplicates role %s", p.name, p.val, other)
		}
		seen[p.val] = p.name
	}
	return nil
}

// AssumableFromSession returns the role name that a request transaction
// should SET LOCAL ROLE to, based on the inbound session. The case labels
// accept both the wire-format JWT claim values (Supabase contract) and the
// configured Roles.* identifiers, which differ when ops rename them.
func (r Roles) AssumableFromSession(s Session) string {
	switch s.Role {
	case JWTRoleService, r.Service:
		return r.Service
	case JWTRoleAuthenticated, r.Authenticated:
		return r.Authenticated
	case JWTRoleAnon, r.Anon, "":
		if s.IsAuthenticated {
			return r.Authenticated
		}
		return r.Anon
	default:
		return r.Anon
	}
}

// Database is the port for all database operations.
type Database interface {
	// Lifecycle
	Close() error
	Ping(ctx context.Context) error

	// Migrations
	EnsureMigrationsTable(ctx context.Context) error
	GetLastMigration(ctx context.Context) (*Migration, error)
	RecordMigration(ctx context.Context, checksum, sql, configJSON string) error
	ExecDDL(ctx context.Context, sql string) error

	// CRUD (called by PostgREST-compatible query engine)
	Query(ctx context.Context, query string, args ...any) ([]map[string]any, error)
	QueryRow(ctx context.Context, query string, args ...any) (map[string]any, error)
	Exec(ctx context.Context, query string, args ...any) (int64, error) // returns affected rows

	// RLS context — sets session variables before query execution
	WithRLS(ctx context.Context, session Session) (context.Context, error)

	// Transactions
	Begin(ctx context.Context) (Tx, error)
}

// OwnerDB is the privileged database connection used for migrations,
// replication slot creation, and extension installs. It MUST NOT
// be passed into HTTP request handling — the distinct named type makes
// such a substitution a compile error at every API boundary.
type OwnerDB struct{ Database }

// RequestDB is the per-request database connection used by HTTP handlers.
// Each transaction issues SET LOCAL ROLE to anon/authenticated/service_role
// based on the inbound session. It MUST NOT be used for DDL.
type RequestDB struct{ Database }

// Tx represents a database transaction.
type Tx interface {
	Query(ctx context.Context, query string, args ...any) ([]map[string]any, error)
	QueryRow(ctx context.Context, query string, args ...any) (map[string]any, error)
	Exec(ctx context.Context, query string, args ...any) (int64, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}
