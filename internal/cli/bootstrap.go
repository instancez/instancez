package cli

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/instancez/instancez/internal/domain"
)

const ownerRole = "instancez_owner"

// passwordFromDSN extracts the password component from a Postgres DSN URL.
// Returns an empty string (no error) when no password is present.
func passwordFromDSN(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse DSN: %w", err)
	}
	pass, _ := u.User.Password()
	return pass, nil
}

// bootstrapDB connects to privilegedDSN as a CREATEROLE-capable login, ensures
// the instancez role layout exists, and returns DSNs derived from privilegedDSN
// for the owner and authenticator roles. All provisioned login roles receive the
// same password as the superuser — rotation is a single-credential operation.
//
// Idempotent: CREATE ROLE IF NOT EXISTS + ALTER ROLE on every call so the
// password is always synced to whatever the superuser URL carries.
func bootstrapDB(ctx context.Context, privilegedDSN string, roles domain.Roles) (ownerDSN, authDSN string, err error) {
	sharedPass, err := passwordFromDSN(privilegedDSN)
	if err != nil {
		return "", "", err
	}

	conn, err := pgx.Connect(ctx, privilegedDSN)
	if err != nil {
		return "", "", fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	var dbName string
	if err := conn.QueryRow(ctx, `SELECT current_database()`).Scan(&dbName); err != nil {
		return "", "", fmt.Errorf("resolve current database: %w", err)
	}
	dbIdent := pgx.Identifier{dbName}.Sanitize()

	apiRoles := fmt.Sprintf("%s, %s, %s", roles.Anon, roles.Authenticated, roles.Service)

	stmts := []string{
		fmt.Sprintf(`DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '%s') THEN
				CREATE ROLE %s LOGIN PASSWORD '%s'
					CREATEROLE CREATEDB BYPASSRLS REPLICATION;
			ELSE
				ALTER ROLE %s WITH LOGIN PASSWORD '%s';
			END IF;
		END $$;`, ownerRole, ownerRole, sharedPass, ownerRole, sharedPass),
		fmt.Sprintf(`DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '%s') THEN
				CREATE ROLE %s LOGIN PASSWORD '%s' NOINHERIT;
			ELSE
				ALTER ROLE %s WITH LOGIN PASSWORD '%s' NOINHERIT;
			END IF;
		END $$;`, roles.Authenticator, roles.Authenticator, sharedPass, roles.Authenticator, sharedPass),
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
		fmt.Sprintf(`GRANT %s TO %s;`, apiRoles, roles.Authenticator),
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

	ownerDSN, err = withUserPass(privilegedDSN, ownerRole, sharedPass, dbName)
	if err != nil {
		return "", "", err
	}
	authDSN, err = withUserPass(privilegedDSN, roles.Authenticator, sharedPass, dbName)
	if err != nil {
		return "", "", err
	}
	return ownerDSN, authDSN, nil
}

// randomPassword returns a 24-byte base64-url-encoded password (~32 chars).
func randomPassword() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// withUserPass returns rawURL rewritten with the given userinfo and explicit
// /dbname path.
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
