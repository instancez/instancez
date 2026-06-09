package cli

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/saedx1/ultrabase/internal/domain"
)

// ownerRole and authenticatorRole are the default login role names provisioned
// by `ultra dev` (via ensureRoles) when bootstrapping from a superuser DSN. They
// match the names dbboot uses for integration tests so a dev-bootstrapped
// project behaves identically to a test-container project.
const (
	ownerRole         = "ultrabase_owner"
	authenticatorRole = "authenticator"
)

// bootstrapDB connects to privilegedDSN as a CREATEROLE-capable login, ensures
// the ultrabase role layout exists (ultrabase_owner + authenticator + the three
// API roles), and returns DSNs derived from privilegedDSN pointing at the two
// login roles with freshly generated passwords.
//
// Idempotent for role creation (IF NOT EXISTS), but always resets passwords
// via ALTER ROLE so the returned DSNs are guaranteed to work — re-running
// against an existing setup rotates credentials.
func bootstrapDB(ctx context.Context, privilegedDSN string) (ownerDSN, authDSN string, err error) {
	ownerPass, err := randomPassword()
	if err != nil {
		return "", "", fmt.Errorf("generate owner password: %w", err)
	}
	authPass, err := randomPassword()
	if err != nil {
		return "", "", fmt.Errorf("generate authenticator password: %w", err)
	}

	conn, err := pgx.Connect(ctx, privilegedDSN)
	if err != nil {
		return "", "", fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	// Ask Postgres for the connected database rather than parsing it out of the
	// DSN — libpq accepts URLs without a path, URLs with just "/", and keyword
	// DSNs like "host=… dbname=…", all of which url.Parse leaves with an empty
	// Path. current_database() works for every shape.
	var dbName string
	if err := conn.QueryRow(ctx, `SELECT current_database()`).Scan(&dbName); err != nil {
		return "", "", fmt.Errorf("resolve current database: %w", err)
	}
	dbIdent := pgx.Identifier{dbName}.Sanitize()

	roles := domain.DefaultRoles()
	apiRoles := fmt.Sprintf("%s, %s, %s", roles.Anon, roles.Authenticated, roles.Service)

	// Quoting passwords with a literal " requires escaping; rand-generated
	// passwords are base64 (no quote chars), so a single-quoted SQL literal
	// is safe here.
	stmts := []string{
		fmt.Sprintf(`DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '%s') THEN
				CREATE ROLE %s LOGIN PASSWORD '%s'
					CREATEROLE CREATEDB BYPASSRLS REPLICATION;
			ELSE
				ALTER ROLE %s WITH LOGIN PASSWORD '%s';
			END IF;
		END $$;`, ownerRole, ownerRole, ownerPass, ownerRole, ownerPass),
		fmt.Sprintf(`DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '%s') THEN
				CREATE ROLE %s LOGIN PASSWORD '%s' NOINHERIT;
			ELSE
				ALTER ROLE %s WITH LOGIN PASSWORD '%s' NOINHERIT;
			END IF;
		END $$;`, authenticatorRole, authenticatorRole, authPass, authenticatorRole, authPass),
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
		fmt.Sprintf(`GRANT %s TO %s;`, apiRoles, authenticatorRole),
		fmt.Sprintf(`ALTER DATABASE %s OWNER TO %s;`, dbIdent, ownerRole),
		fmt.Sprintf(`ALTER SCHEMA public OWNER TO %s;`, ownerRole),
		fmt.Sprintf(`GRANT ALL ON SCHEMA public TO %s;`, ownerRole),
		fmt.Sprintf(`GRANT USAGE ON SCHEMA public TO %s;`, apiRoles),
		fmt.Sprintf(`ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %s;`, ownerRole, apiRoles),
		fmt.Sprintf(`ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO %s;`, ownerRole, apiRoles),
		fmt.Sprintf(`ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA public GRANT EXECUTE ON FUNCTIONS TO %s;`, ownerRole, apiRoles),
	}
	for _, s := range stmts {
		if _, err := conn.Exec(ctx, s); err != nil {
			return "", "", fmt.Errorf("bootstrap (%s): %w", firstSQLLine(s), err)
		}
	}

	ownerDSN, err = withUserPass(privilegedDSN, ownerRole, ownerPass, dbName)
	if err != nil {
		return "", "", err
	}
	authDSN, err = withUserPass(privilegedDSN, authenticatorRole, authPass, dbName)
	if err != nil {
		return "", "", err
	}
	return ownerDSN, authDSN, nil
}

// randomPassword returns a 24-byte base64-url-encoded password (~32 chars).
// base64-url avoids the +/=/" chars that would need SQL escaping.
func randomPassword() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// withUserPass returns rawURL rewritten with the given userinfo and explicit
// /dbname path. The dbname injection is load-bearing: a URL DSN with no path
// (e.g. "postgres://postgres@host:5432") causes pgx to default the database to
// the username at connect time, so the generated authenticator DSN would try
// to connect to a "authenticator" database that doesn't exist. We pin the
// resolved current_database() into the path so the persisted DSNs are
// connect-target-explicit regardless of how the privileged DSN was shaped.
func withUserPass(rawURL, user, pass, dbName string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse DSN: %w", err)
	}
	u.User = url.UserPassword(user, pass)
	u.Path = "/" + dbName
	return u.String(), nil
}

func firstSQLLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
