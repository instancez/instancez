//go:build integration

package cli

import (
	"context"
	"fmt"
	"net/url"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/instancez/instancez/internal/domain"
	"github.com/instancez/instancez/internal/testutil/dbboot"
)

func TestDBConnectionsProvisionRoles(t *testing.T) {
	ctx := context.Background()
	dsn := dbboot.StartRawContainer(t)

	t.Setenv("INSTANCEZ_DATABASE_URL", dsn)

	owner, auth, roles, err := dbConnections(ctx, domain.PoolConfig{Max: 2}, "")
	if err != nil {
		t.Fatalf("dbConnections: %v", err)
	}
	defer owner.Close()
	defer auth.Close()

	// Verify the owner role exists and can connect
	ownerRow, err := owner.QueryRow(ctx, "SELECT current_user")
	if err != nil {
		t.Fatalf("owner query: %v", err)
	}
	ownerName, _ := ownerRow["current_user"].(string)
	if ownerName != "instancez_owner" {
		t.Errorf("owner user = %q, want instancez_owner", ownerName)
	}

	// Verify the authenticator role exists and can connect
	authRow, err := auth.QueryRow(ctx, "SELECT session_user")
	if err != nil {
		t.Fatalf("auth query: %v", err)
	}
	authName, _ := authRow["session_user"].(string)
	if authName != roles.Authenticator {
		t.Errorf("auth user = %q, want %q", authName, roles.Authenticator)
	}
}

func TestDBConnectionsIdempotent(t *testing.T) {
	ctx := context.Background()
	dsn := dbboot.StartRawContainer(t)

	// First startup — provisions roles
	t.Setenv("INSTANCEZ_DATABASE_URL", dsn)
	owner1, auth1, _, err := dbConnections(ctx, domain.PoolConfig{Max: 2}, "")
	if err != nil {
		t.Fatalf("first dbConnections: %v", err)
	}
	owner1.Close()
	auth1.Close()

	// Second startup — roles already exist, bootstrapDB must be a no-op
	owner2, auth2, _, err := dbConnections(ctx, domain.PoolConfig{Max: 2}, "")
	if err != nil {
		t.Fatalf("second dbConnections (idempotency check): %v", err)
	}
	defer owner2.Close()
	defer auth2.Close()

	row, err := owner2.QueryRow(ctx, "SELECT current_user")
	if err != nil {
		t.Fatalf("second owner query: %v", err)
	}
	u, _ := row["current_user"].(string)
	if u != "instancez_owner" {
		t.Errorf("second owner user = %q, want instancez_owner", u)
	}
}

func TestDBConnectionsPasswordRotation(t *testing.T) {
	ctx := context.Background()
	dsn := dbboot.StartRawContainer(t)

	// First startup — provisions roles with original password
	t.Setenv("INSTANCEZ_DATABASE_URL", dsn)
	owner1, auth1, _, err := dbConnections(ctx, domain.PoolConfig{Max: 2}, "")
	if err != nil {
		t.Fatalf("first dbConnections: %v", err)
	}
	owner1.Close()
	auth1.Close()

	// Simulate password rotation: change the superuser password, then
	// call dbConnections again — bootstrapDB should re-sync all role passwords.
	// We do this by connecting as superuser and changing its own password,
	// then constructing a new DSN with the new password.
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect as superuser: %v", err)
	}
	newPass := "rotated_password_123"
	superuserName := conn.Config().User
	if _, err := conn.Exec(ctx, fmt.Sprintf("ALTER USER %s WITH PASSWORD '%s'", superuserName, newPass)); err != nil {
		conn.Close(ctx)
		t.Fatalf("rotate superuser password: %v", err)
	}
	conn.Close(ctx)

	// Build new DSN with rotated password
	u, _ := url.Parse(dsn)
	u.User = url.UserPassword(superuserName, newPass)
	rotatedDSN := u.String()
	t.Setenv("INSTANCEZ_DATABASE_URL", rotatedDSN)

	// Second startup — bootstrapDB should update role passwords to match new superuser password
	owner2, auth2, roles, err := dbConnections(ctx, domain.PoolConfig{Max: 2}, "")
	if err != nil {
		t.Fatalf("second dbConnections after password rotation: %v", err)
	}
	defer owner2.Close()
	defer auth2.Close()

	// Both pools should connect successfully — proving password was synced
	ownerRow, err := owner2.QueryRow(ctx, "SELECT current_user")
	if err != nil {
		t.Fatalf("owner query after rotation: %v", err)
	}
	ownerUser, _ := ownerRow["current_user"].(string)
	_ = ownerUser
	_ = roles
	authRow, err := auth2.QueryRow(ctx, "SELECT current_user")
	if err != nil {
		t.Fatalf("auth query after rotation: %v", err)
	}
	authUser, _ := authRow["current_user"].(string)
	_ = authUser
}
