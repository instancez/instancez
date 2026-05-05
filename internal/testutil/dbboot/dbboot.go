// Package dbboot provisions the two-login Postgres setup for integration
// tests. It connects as the testcontainer's superuser, creates the
// ultrabase_owner and authenticator login roles, transfers schema
// ownership, and returns wrappers ready for the engine.
package dbboot

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/saedx1/ultrabase/internal/adapter/postgres"
	"github.com/saedx1/ultrabase/internal/domain"
)

// OwnerRole is the login role used for migrations/seeding/replication.
const OwnerRole = "ultrabase_owner"

// AuthenticatorRole is the login role used by the request pool.
const AuthenticatorRole = "authenticator"

// rolePassword is shared across both login roles in tests; not exported
// because production deployments must set their own credentials.
const rolePassword = "ultrabase_test"

// Bootstrap creates the four required roles on the testcontainer's superuser
// connection, transfers database ownership to OwnerRole, and returns the
// owner + request pools.
//
// superURL must point to the testcontainer DB as a superuser. The function
// is idempotent — re-running against an existing setup is a no-op.
//
// authenticator uses default INHERIT so system endpoints (auth/admin) that
// don't wrap their queries in a WithRLS tx still have the union of granted
// privileges. CRUD endpoints explicitly SET LOCAL ROLE per transaction.
//
// The three NOLOGIN API roles (anon/authenticated/service_role) are also
// provisioned here so tests that bypass the Migrator (e.g. pgrupstream
// conformance) can still SET LOCAL ROLE without hitting "role does not
// exist". Public-schema USAGE + default privileges are wired up so tables
// created after Bootstrap are accessible to the API roles automatically.
func Bootstrap(ctx context.Context, superURL string, poolCfg domain.PoolConfig) (domain.OwnerDB, domain.RequestDB, error) {
	conn, err := pgx.Connect(ctx, superURL)
	if err != nil {
		return domain.OwnerDB{}, domain.RequestDB{}, fmt.Errorf("connect superuser: %w", err)
	}
	defer conn.Close(ctx)

	roles := domain.DefaultRoles()
	apiRoles := fmt.Sprintf("%s, %s, %s", roles.Anon, roles.Authenticated, roles.Service)
	stmts := []string{
		fmt.Sprintf(`DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '%s') THEN
				CREATE ROLE %s LOGIN PASSWORD '%s'
					CREATEROLE CREATEDB BYPASSRLS REPLICATION;
			END IF;
		END $$;`, OwnerRole, OwnerRole, rolePassword),
		fmt.Sprintf(`DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '%s') THEN
				CREATE ROLE %s LOGIN PASSWORD '%s' INHERIT;
			END IF;
		END $$;`, AuthenticatorRole, AuthenticatorRole, rolePassword),
		fmt.Sprintf(`DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '%s') THEN
				CREATE ROLE %s NOLOGIN;
			END IF;
		END $$;`, roles.Anon, roles.Anon),
		fmt.Sprintf(`DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '%s') THEN
				CREATE ROLE %s NOLOGIN;
			END IF;
		END $$;`, roles.Authenticated, roles.Authenticated),
		fmt.Sprintf(`DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '%s') THEN
				CREATE ROLE %s NOLOGIN BYPASSRLS;
			END IF;
		END $$;`, roles.Service, roles.Service),
		fmt.Sprintf(`GRANT %s TO %s;`, apiRoles, AuthenticatorRole),
		// WITH ADMIN OPTION lets OwnerRole re-issue these GRANTs
		// idempotently when the Migrator's generateRoleDDL runs (which
		// it does in tests that exercise the full migration path).
		fmt.Sprintf(`GRANT %s TO %s WITH ADMIN OPTION;`, apiRoles, OwnerRole),
		fmt.Sprintf(`ALTER DATABASE %s OWNER TO %s;`, currentDB(superURL), OwnerRole),
		fmt.Sprintf(`ALTER SCHEMA public OWNER TO %s;`, OwnerRole),
		fmt.Sprintf(`GRANT ALL ON SCHEMA public TO %s;`, OwnerRole),
		fmt.Sprintf(`GRANT USAGE ON SCHEMA public TO %s;`, apiRoles),
		// ALTER DEFAULT PRIVILEGES is per-grantor; FOR ROLE pins it to
		// OwnerRole so any tables/sequences the owner creates afterward
		// become visible to anon/authenticated/service_role automatically.
		fmt.Sprintf(`ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %s;`, OwnerRole, apiRoles),
		fmt.Sprintf(`ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO %s;`, OwnerRole, apiRoles),
		fmt.Sprintf(`ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA public GRANT EXECUTE ON FUNCTIONS TO %s;`, OwnerRole, apiRoles),
	}
	for _, s := range stmts {
		if _, err := conn.Exec(ctx, s); err != nil {
			return domain.OwnerDB{}, domain.RequestDB{}, fmt.Errorf("bootstrap (%s): %w", firstLine(s), err)
		}
	}

	owner, err := postgres.NewOwner(ctx, withUserPass(superURL, OwnerRole, rolePassword), poolCfg)
	if err != nil {
		return domain.OwnerDB{}, domain.RequestDB{}, fmt.Errorf("owner pool: %w", err)
	}
	auth, err := postgres.NewRequest(ctx, withUserPass(superURL, AuthenticatorRole, rolePassword), poolCfg, roles)
	if err != nil {
		owner.Close()
		return domain.OwnerDB{}, domain.RequestDB{}, fmt.Errorf("auth pool: %w", err)
	}
	return owner, auth, nil
}

func withUserPass(rawURL, user, pass string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.User = url.UserPassword(user, pass)
	return u.String()
}

func currentDB(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(u.Path, "/")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
