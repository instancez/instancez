//go:build integration

package postgres_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
	"github.com/instancez/instancez/internal/testutil/dbboot"
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

// TestRequestPool_ServiceRoleUserIDIsNull verifies that for a service_role
// session the per-request setup leaves app.user_id empty, so auth.uid()
// (NULLIF(current_setting('app.user_id',true),”)::uuid) resolves to NULL —
// matching Supabase, where service_role tokens have no subject. For an
// authenticated session the GUC is populated with the user's UUID.
func TestRequestPool_ServiceRoleUserIDIsNull(t *testing.T) {
	_, auth := dbboot.StartContainer(t)
	ctx := context.Background()

	const uid = "11111111-1111-1111-1111-111111111111"

	// service_role: app.user_id must be empty even though the minted token may
	// carry a synthetic sub.
	t.Run("service_role", func(t *testing.T) {
		rctx, err := auth.WithRLS(ctx, domain.Session{
			Role: "service_role", UserID: "00000000-0000-0000-0000-000000000000", IsAuthenticated: true,
		})
		if err != nil {
			t.Fatalf("WithRLS: %v", err)
		}
		tx, err := auth.Begin(rctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer tx.Rollback(rctx)

		row, err := tx.QueryRow(rctx,
			"SELECT NULLIF(current_setting('app.user_id', true), '') AS uid")
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		if row["uid"] != nil {
			t.Fatalf("service_role app.user_id = %v, want NULL", row["uid"])
		}
	})

	// authenticated: app.user_id is set to the user's UUID.
	t.Run("authenticated", func(t *testing.T) {
		rctx, err := auth.WithRLS(ctx, domain.Session{
			Role: "authenticated", UserID: uid, IsAuthenticated: true,
		})
		if err != nil {
			t.Fatalf("WithRLS: %v", err)
		}
		tx, err := auth.Begin(rctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer tx.Rollback(rctx)

		row, err := tx.QueryRow(rctx,
			"SELECT NULLIF(current_setting('app.user_id', true), '') AS uid")
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		if row["uid"] != uid {
			t.Fatalf("authenticated app.user_id = %v, want %s", row["uid"], uid)
		}
	})
}

// TestOwnerPool_NoRoleSwitching confirms the owner pool stays as
// instancez_owner — no SET LOCAL ROLE is ever issued on it.
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

// TestAuthenticatorWithoutRoleSwitchCannotReadAuthOrStorage asserts the
// load-bearing NOINHERIT guarantee: the authenticator login role, when used
// without issuing SET LOCAL ROLE, is denied access to auth.users and
// storage.objects. This regression test enforces the CLAUDE.md architecture
// contract — NOINHERIT on the authenticator is what makes a missing role-switch
// surface as an error rather than silently running as authenticator.
func TestAuthenticatorWithoutRoleSwitchCannotReadAuthOrStorage(t *testing.T) {
	owner, _, rawAuth := dbboot.StartContainerWithRawAuth(t)
	ctx := context.Background()

	cfg := &domain.Config{
		Version: 1,
		Auth:    &domain.Auth{},
		Storage: map[string]domain.Bucket{"test": {Public: true}},
	}
	if err := app.NewMigrator(owner).Apply(ctx, cfg); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for _, fqn := range []string{"auth.users", "storage.objects"} {
		// rawAuth is the authenticator login without SET LOCAL ROLE — NOINHERIT
		// means the granted API-role privileges are not in effect.
		_, err := rawAuth.Query(ctx, fmt.Sprintf("SELECT 1 FROM %s LIMIT 1", fqn))
		if err == nil {
			t.Errorf("expected authenticator without SET LOCAL ROLE to be denied on %s", fqn)
		}
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
