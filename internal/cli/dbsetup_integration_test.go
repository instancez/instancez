//go:build integration

package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/saedx1/instancez/internal/testutil/dbboot"
)

// TestEnsureRolesBootstrapsFromSuperuser drives the full dev bootstrap path:
// against a raw superuser container with no ultrabase roles, ensureRoles must
// provision the layout, set the derived DSNs into the env, persist them, and
// leave a database the owner DSN can connect to with all five roles present.
func TestEnsureRolesBootstrapsFromSuperuser(t *testing.T) {
	superURL := dbboot.StartRawContainer(t)

	// No role DSNs in env → ensureRoles must bootstrap.
	t.Setenv("INSTANCEZ_OWNER_DATABASE_URL", "")
	t.Setenv("INSTANCEZ_AUTH_DATABASE_URL", "")

	envFile := filepath.Join(t.TempDir(), ".development.env")
	res, err := ensureRoles(context.Background(), superURL, envFile)
	if err != nil {
		t.Fatalf("ensureRoles: %v", err)
	}
	if !res.Ran {
		t.Fatal("ensureRoles did not run bootstrap on a fresh superuser DB")
	}

	// Derived DSNs were exported into the env.
	ownerDSN := os.Getenv("INSTANCEZ_OWNER_DATABASE_URL")
	if ownerDSN == "" || os.Getenv("INSTANCEZ_AUTH_DATABASE_URL") == "" {
		t.Fatal("ensureRoles did not set the derived DSNs in the env")
	}

	// And persisted to the env file.
	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("read persisted env file: %v", err)
	}
	if !strings.Contains(string(data), "INSTANCEZ_OWNER_DATABASE_URL=") ||
		!strings.Contains(string(data), "INSTANCEZ_AUTH_DATABASE_URL=") {
		t.Fatalf("env file missing derived DSNs:\n%s", data)
	}

	// The owner DSN connects and all five roles exist.
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, ownerDSN)
	if err != nil {
		t.Fatalf("connect as derived owner: %v", err)
	}
	defer conn.Close(ctx)

	for _, role := range []string{"instancez_owner", "authenticator", "anon", "authenticated", "service_role"} {
		var exists bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, role).Scan(&exists); err != nil {
			t.Fatalf("query role %s: %v", role, err)
		}
		if !exists {
			t.Errorf("role %q was not provisioned", role)
		}
	}
}
