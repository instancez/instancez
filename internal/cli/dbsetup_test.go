package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

func TestRolesFromEnv_Defaults(t *testing.T) {
	for _, k := range []string{
		"INSTANCEZ_DB_AUTHENTICATOR_ROLE",
		"INSTANCEZ_DB_ANON_ROLE",
		"INSTANCEZ_DB_AUTHENTICATED_ROLE",
		"INSTANCEZ_DB_SERVICE_ROLE",
	} {
		t.Setenv(k, "")
	}
	got := rolesFromEnv()
	if got != domain.DefaultRoles() {
		t.Fatalf("rolesFromEnv with no env = %+v, want defaults %+v", got, domain.DefaultRoles())
	}
}

func TestRolesFromEnv_Overrides(t *testing.T) {
	t.Setenv("INSTANCEZ_DB_AUTHENTICATOR_ROLE", "rest_login")
	t.Setenv("INSTANCEZ_DB_ANON_ROLE", "guest")
	t.Setenv("INSTANCEZ_DB_AUTHENTICATED_ROLE", "member")
	t.Setenv("INSTANCEZ_DB_SERVICE_ROLE", "admin_role")

	got := rolesFromEnv()
	want := domain.Roles{
		Authenticator: "rest_login",
		Anon:          "guest",
		Authenticated: "member",
		Service:       "admin_role",
	}
	if got != want {
		t.Fatalf("rolesFromEnv = %+v, want %+v", got, want)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("custom roles failed validation: %v", err)
	}
}

func TestRolesFromEnv_PartialOverride(t *testing.T) {
	t.Setenv("INSTANCEZ_DB_AUTHENTICATOR_ROLE", "")
	t.Setenv("INSTANCEZ_DB_ANON_ROLE", "guest")
	t.Setenv("INSTANCEZ_DB_AUTHENTICATED_ROLE", "")
	t.Setenv("INSTANCEZ_DB_SERVICE_ROLE", "")

	got := rolesFromEnv()
	if got.Anon != "guest" {
		t.Errorf("anon override lost: %q", got.Anon)
	}
	if got.Authenticator != "authenticator" || got.Authenticated != "authenticated" || got.Service != "service_role" {
		t.Errorf("unset overrides should keep defaults: %+v", got)
	}
}

func TestOwnerPoolConfigShrinksPool(t *testing.T) {
	got := ownerPoolConfig(domain.PoolConfig{Max: 20, Min: 5, IdleTimeout: "300s"})

	if got.Max != 2 {
		t.Errorf("Max = %d, want 2", got.Max)
	}
	if got.Min != 0 {
		t.Errorf("Min = %d, want 0", got.Min)
	}
	if got.IdleTimeout != "300s" {
		t.Errorf("IdleTimeout = %q, want %q (inherited)", got.IdleTimeout, "300s")
	}
}

func TestOwnerPoolConfigRespectsSmallerUserMax(t *testing.T) {
	got := ownerPoolConfig(domain.PoolConfig{Max: 1, Min: 1})
	if got.Max != 1 {
		t.Errorf("Max = %d, want 1 (never exceed the configured pool max)", got.Max)
	}
}

func TestDBConnectionsRequiresSuperuserURL(t *testing.T) {
	t.Setenv("INSTANCEZ_DATABASE_URL", "")

	_, _, _, err := dbConnections(context.Background(), domain.PoolConfig{Max: 1})
	if err == nil {
		t.Fatal("expected error when INSTANCEZ_DATABASE_URL is empty")
	}
	if !strings.Contains(err.Error(), "INSTANCEZ_DATABASE_URL") {
		t.Errorf("error should mention env var name, got: %v", err)
	}
}

func TestEnsureAdminKeyGeneratesWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".development.env")

	// No file — should generate
	generated, err := ensureAdminKey(envFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !generated {
		t.Error("expected generated=true on first call")
	}

	// File now exists with key — should be idempotent
	generated, err = ensureAdminKey(envFile)
	if err != nil {
		t.Fatalf("unexpected error on second call: %v", err)
	}
	if generated {
		t.Error("expected generated=false on second call (key already exists)")
	}
}
