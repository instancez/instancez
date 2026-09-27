//go:build integration

package app_test

import (
	"context"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
	"github.com/instancez/instancez/internal/testutil/dbboot"
)

func rlsCfg(enabled *bool, policies ...domain.RLSPolicy) *domain.Config {
	return &domain.Config{Version: 1, Tables: map[string]domain.Table{"secrets": {
		RLSEnabled: enabled,
		Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "body", Type: "text", Required: true},
		},
		RLS: policies,
	}}}
}

// asRole runs fn inside a request tx under role, like the HTTP layer does.
func asRole(t *testing.T, req domain.RequestDB, role string, fn func(ctx context.Context, tx domain.Tx) error) error {
	t.Helper()
	ctx, err := req.WithRLS(context.Background(), domain.Session{Role: role})
	if err != nil {
		t.Fatalf("WithRLS(%s): %v", role, err)
	}
	tx, err := req.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	return fn(ctx, tx)
}

func visibleRows(t *testing.T, req domain.RequestDB, role string) int {
	t.Helper()
	var n int
	if err := asRole(t, req, role, func(ctx context.Context, tx domain.Tx) error {
		rows, err := tx.Query(ctx, "SELECT id FROM secrets")
		n = len(rows)
		return err
	}); err != nil {
		t.Fatalf("%s select: %v", role, err)
	}
	return n
}

func TestIntegration_RLSEnabledZeroPoliciesDeniesAndSurvivesPolicyRemoval(t *testing.T) {
	owner, req := dbboot.StartContainer(t)
	ctx := context.Background()
	on := true
	m := app.NewMigrator(owner)

	// v1: rls_enabled true + a public-read policy.
	if err := m.Apply(ctx, rlsCfg(&on, domain.RLSPolicy{Operations: []string{"select"}, Using: "true"})); err != nil {
		t.Fatalf("v1: %v", err)
	}
	if _, err := owner.Exec(ctx, "INSERT INTO secrets (body) VALUES ('hidden')"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if n := visibleRows(t, req, "anon"); n != 1 {
		t.Fatalf("v1 anon sees %d rows, want 1", n)
	}

	// v2: drop the last policy, keep rls_enabled true: deny-all, RLS stays on.
	if err := m.Apply(ctx, rlsCfg(&on)); err != nil {
		t.Fatalf("v2: %v", err)
	}
	if en, forced := rlsEnabled(t, owner, "secrets"); !en || !forced {
		t.Fatalf("v2 relrowsecurity=%v relforcerowsecurity=%v, want both true", en, forced)
	}
	for _, role := range []string{"anon", "authenticated"} {
		if n := visibleRows(t, req, role); n != 0 {
			t.Fatalf("v2 %s sees %d rows, want 0 (deny-all)", role, n)
		}
	}
	for _, role := range []string{"anon", "authenticated"} {
		err := asRole(t, req, role, func(ctx context.Context, tx domain.Tx) error {
			_, err := tx.Exec(ctx, "INSERT INTO secrets (body) VALUES ('nope')")
			return err
		})
		if err == nil || !strings.Contains(err.Error(), "row-level security") {
			t.Fatalf("v2 %s insert = %v, want a row-level security violation", role, err)
		}
	}
	if n := visibleRows(t, req, "service_role"); n != 1 {
		t.Fatalf("service_role must bypass RLS, sees %d rows", n)
	}
}

func TestIntegration_RLSEnabledFreshTableZeroPolicies(t *testing.T) {
	owner, req := dbboot.StartContainer(t)
	on := true
	if err := app.NewMigrator(owner).Apply(context.Background(), rlsCfg(&on)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := owner.Exec(context.Background(), "INSERT INTO secrets (body) VALUES ('x')"); err != nil {
		t.Fatal(err)
	}
	if n := visibleRows(t, req, "anon"); n != 0 {
		t.Fatalf("anon sees %d rows on a zero-policy rls_enabled table, want 0", n)
	}
}

func TestIntegration_RLSEnabledTrueToFalseDisables(t *testing.T) {
	owner, req := dbboot.StartContainer(t)
	ctx := context.Background()
	on, off := true, false
	m := app.NewMigrator(owner)
	if err := m.Apply(ctx, rlsCfg(&on)); err != nil {
		t.Fatalf("v1: %v", err)
	}
	if _, err := owner.Exec(ctx, "INSERT INTO secrets (body) VALUES ('x')"); err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(ctx, rlsCfg(&off)); err != nil {
		t.Fatalf("v2: %v", err)
	}
	if en, _ := rlsEnabled(t, owner, "secrets"); en {
		t.Fatal("rls_enabled false must DISABLE row level security")
	}
	if n := visibleRows(t, req, "anon"); n != 1 {
		t.Fatalf("anon sees %d rows after disabling RLS, want 1", n)
	}
	if err := asRole(t, req, "anon", func(ctx context.Context, tx domain.Tx) error {
		_, err := tx.Exec(ctx, "INSERT INTO secrets (body) VALUES ('open')")
		return err
	}); err != nil {
		t.Fatalf("anon insert after disabling RLS: %v, want grants to apply", err)
	}
	// false -> true re-enables on the next migration.
	if err := m.Apply(ctx, rlsCfg(&on)); err != nil {
		t.Fatalf("v3: %v", err)
	}
	if en, forced := rlsEnabled(t, owner, "secrets"); !en || !forced {
		t.Fatalf("v3 relrowsecurity=%v relforcerowsecurity=%v, want both true", en, forced)
	}
}

// A legacy app adding rls_enabled: true while dropping policies must stay locked.
func TestIntegration_LegacyAppAddsRLSEnabledKeepsDeny(t *testing.T) {
	owner, req := dbboot.StartContainer(t)
	ctx := context.Background()
	m := app.NewMigrator(owner)
	if err := m.Apply(ctx, rlsCfg(nil, domain.RLSPolicy{Operations: []string{"select"}, Using: "false"})); err != nil {
		t.Fatalf("legacy v1: %v", err)
	}
	on := true
	if err := m.Apply(ctx, rlsCfg(&on)); err != nil {
		t.Fatalf("v2: %v", err)
	}
	if en, _ := rlsEnabled(t, owner, "secrets"); !en {
		t.Fatal("RLS disabled after legacy -> rls_enabled: true")
	}
	if _, err := owner.Exec(ctx, "INSERT INTO secrets (body) VALUES ('x')"); err != nil {
		t.Fatal(err)
	}
	if n := visibleRows(t, req, "anon"); n != 0 {
		t.Fatalf("anon sees %d rows, want 0", n)
	}
}
