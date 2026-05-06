//go:build integration

package postgres_test

import (
	"context"
	"strings"
	"testing"

	"github.com/saedx1/ultrabase/internal/domain"
	"github.com/saedx1/ultrabase/internal/testutil/dbboot"
)

// TestRequestPool_SetLocalRole verifies that the request-pool Begin issues
// SET LOCAL ROLE based on the inbound session, so current_user inside the
// transaction is the assumed role rather than the authenticator login itself.
func TestRequestPool_SetLocalRole(t *testing.T) {
	_, auth := dbboot.StartContainer(t)
	ctx := context.Background()

	cases := []struct{ role, ident string }{
		{"anon", "anon"},
		{"authenticated", "authenticated"},
		{"service_role", "service_role"},
	}
	for _, c := range cases {
		t.Run(c.role, func(t *testing.T) {
			rctx, err := auth.WithRLS(ctx, domain.Session{Role: c.role, IsAuthenticated: c.role != "anon"})
			if err != nil {
				t.Fatalf("WithRLS: %v", err)
			}
			tx, err := auth.Begin(rctx)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer tx.Rollback(rctx)

			row, err := tx.QueryRow(rctx, "SELECT current_user")
			if err != nil {
				t.Fatalf("query current_user: %v", err)
			}
			if row["current_user"] != c.ident {
				t.Fatalf("current_user = %v, want %s", row["current_user"], c.ident)
			}
		})
	}
}

// TestOwnerPool_NoRoleSwitching confirms the owner pool stays as
// ultrabase_owner — no SET LOCAL ROLE is ever issued on it.
func TestOwnerPool_NoRoleSwitching(t *testing.T) {
	owner, _ := dbboot.StartContainer(t)
	ctx := context.Background()

	tx, err := owner.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	row, err := tx.QueryRow(ctx, "SELECT current_user")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if row["current_user"] != dbboot.OwnerRole {
		t.Fatalf("owner pool current_user = %v, want %s", row["current_user"], dbboot.OwnerRole)
	}
}

// TestSeedBypassesForceRLS asserts that the owner pool can insert into a
// FORCE-RLS table with a deny-all policy (BYPASSRLS), while the request
// pool acting as anon is denied.
func TestSeedBypassesForceRLS(t *testing.T) {
	owner, auth := dbboot.StartContainer(t)
	ctx := context.Background()

	roles := domain.DefaultRoles()
	setup := strings.Join([]string{
		"CREATE TABLE locked (id int PRIMARY KEY, label text);",
		"GRANT INSERT ON locked TO " + roles.Anon + ", " + roles.Authenticated + ", " + roles.Service + ";",
		"ALTER TABLE locked ENABLE ROW LEVEL SECURITY;",
		"ALTER TABLE locked FORCE ROW LEVEL SECURITY;",
		"CREATE POLICY deny_all ON locked FOR ALL USING (false) WITH CHECK (false);",
	}, "\n")
	if err := owner.ExecDDL(ctx, setup); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if _, err := owner.Exec(ctx, "INSERT INTO locked (id, label) VALUES (1, 'seeded')"); err != nil {
		t.Fatalf("owner insert into FORCE RLS table failed: %v", err)
	}

	rctx, _ := auth.WithRLS(ctx, domain.Session{Role: "anon"})
	tx, err := auth.Begin(rctx)
	if err != nil {
		t.Fatalf("auth begin: %v", err)
	}
	if _, err := tx.Exec(rctx, "INSERT INTO locked (id, label) VALUES (2, 'should-fail')"); err == nil {
		t.Fatalf("anon insert should have been denied by RLS")
	}
	tx.Rollback(rctx)
}
