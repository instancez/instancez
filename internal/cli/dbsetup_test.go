package cli

import (
	"context"
	"os"
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
		"INSTANCEZ_DB_SEED_ROLE",
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
	t.Setenv("INSTANCEZ_DB_SEED_ROLE", "")

	got := rolesFromEnv()
	want := domain.Roles{
		Authenticator: "rest_login",
		Anon:          "guest",
		Authenticated: "member",
		Service:       "admin_role",
		Seed:          "",
	}
	if got != want {
		t.Fatalf("rolesFromEnv = %+v, want %+v", got, want)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("custom roles failed validation: %v", err)
	}
}

func TestRolesFromEnv_SeedRole(t *testing.T) {
	// Unset seed role yields empty string (no seed grants).
	t.Setenv("INSTANCEZ_DB_SEED_ROLE", "")
	got := rolesFromEnv()
	if got.Seed != "" {
		t.Errorf("Seed should be empty when INSTANCEZ_DB_SEED_ROLE is unset, got %q", got.Seed)
	}

	// Set seed role is forwarded.
	t.Setenv("INSTANCEZ_DB_SEED_ROLE", "app_x_seed")
	got = rolesFromEnv()
	if got.Seed != "app_x_seed" {
		t.Errorf("Seed = %q, want %q", got.Seed, "app_x_seed")
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
	// Clear every DSN var so the resolver has no input at all.
	t.Setenv("INSTANCEZ_DATABASE_URL", "")
	t.Setenv("INSTANCEZ_OWNER_DATABASE_URL", "")
	t.Setenv("INSTANCEZ_AUTH_DATABASE_URL", "")

	_, _, _, err := dbConnections(context.Background(), domain.PoolConfig{Max: 1}, "")
	if err == nil {
		t.Fatal("expected error when no DSN is set")
	}
	if !strings.Contains(err.Error(), "INSTANCEZ_DATABASE_URL") {
		t.Errorf("error should mention env var name, got: %v", err)
	}
}

// TestResolveDBSourceOverrideIgnoresEnv is the embedded-Postgres regression
// guard: when a superuser DSN is supplied directly (the embedded path), leftover
// scoped DSN env vars from a prior external-Postgres setup must not redirect the
// instance at a different — usually absent — database.
func TestResolveDBSourceOverrideIgnoresEnv(t *testing.T) {
	env := map[string]string{
		"INSTANCEZ_OWNER_DATABASE_URL": "postgres://instancez_owner@localhost:5432/postgres",
		"INSTANCEZ_AUTH_DATABASE_URL":  "postgres://authenticator@localhost:5432/postgres",
		"INSTANCEZ_DATABASE_URL":       "postgres://postgres@localhost:5432/postgres",
	}
	getenv := func(k string) string { return env[k] }

	const embedded = "postgres://postgres:postgres@localhost:54999/postgres?sslmode=disable"
	src, err := resolveDBSource(getenv, embedded)
	if err != nil {
		t.Fatalf("resolveDBSource: %v", err)
	}
	if src.superuserURL != embedded {
		t.Errorf("superuserURL = %q, want the supplied embedded DSN %q", src.superuserURL, embedded)
	}
	if src.ownerURL != "" || src.authURL != "" {
		t.Errorf("scoped DSNs must be ignored under override, got owner=%q auth=%q", src.ownerURL, src.authURL)
	}
}

func TestResolveDBSourcePrefersScopedPair(t *testing.T) {
	env := map[string]string{
		"INSTANCEZ_OWNER_DATABASE_URL": "owner-dsn",
		"INSTANCEZ_AUTH_DATABASE_URL":  "auth-dsn",
		"INSTANCEZ_DATABASE_URL":       "superuser-dsn",
	}
	src, err := resolveDBSource(func(k string) string { return env[k] }, "")
	if err != nil {
		t.Fatalf("resolveDBSource: %v", err)
	}
	if src.ownerURL != "owner-dsn" || src.authURL != "auth-dsn" {
		t.Errorf("expected scoped pair, got owner=%q auth=%q", src.ownerURL, src.authURL)
	}
	if src.superuserURL != "" {
		t.Errorf("superuserURL should be empty when scoped pair wins, got %q", src.superuserURL)
	}
}

func TestResolveDBSourceFallsBackToSuperuser(t *testing.T) {
	// Only one half of the scoped pair is set, so it falls through to superuser.
	env := map[string]string{
		"INSTANCEZ_OWNER_DATABASE_URL": "owner-dsn",
		"INSTANCEZ_DATABASE_URL":       "superuser-dsn",
	}
	src, err := resolveDBSource(func(k string) string { return env[k] }, "")
	if err != nil {
		t.Fatalf("resolveDBSource: %v", err)
	}
	if src.superuserURL != "superuser-dsn" {
		t.Errorf("superuserURL = %q, want superuser-dsn", src.superuserURL)
	}
	if src.ownerURL != "" || src.authURL != "" {
		t.Errorf("incomplete scoped pair must not be used, got owner=%q auth=%q", src.ownerURL, src.authURL)
	}
}

func TestResolveDBSourceErrorsWhenEmpty(t *testing.T) {
	if _, err := resolveDBSource(func(string) string { return "" }, ""); err == nil {
		t.Fatal("expected error when no DSN is available")
	}
}

func TestEnsureAPIKeysGeneratesWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".development.env")

	// No file — should generate both keys
	generated, err := ensureAPIKeys(envFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !generated {
		t.Error("expected generated=true on first call")
	}
	data, _ := os.ReadFile(envFile)
	for _, want := range []string{"INSTANCEZ_PUBLISHABLE_KEY", "INSTANCEZ_SECRET_KEY"} {
		if !hasActiveEnvKey(string(data), want) {
			t.Errorf("expected active %s after generation", want)
		}
	}
	// The generated values carry their self-describing prefix.
	if s := string(data); !strings.Contains(s, "inz_publishable_") || !strings.Contains(s, "inz_secret_") {
		t.Errorf("expected inz_publishable_/inz_secret_ prefixes in:\n%s", s)
	}

	// Both present now — should be idempotent
	generated, err = ensureAPIKeys(envFile)
	if err != nil {
		t.Fatalf("unexpected error on second call: %v", err)
	}
	if generated {
		t.Error("expected generated=false on second call (keys already exist)")
	}
}

func TestEnsureAPIKeysFillsMissingKey(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".development.env")

	// Publishable key present; secret key only commented out.
	content := "INSTANCEZ_PUBLISHABLE_KEY=inz_publishable_existing\n# INSTANCEZ_SECRET_KEY=someoldvalue\nOTHER_VAR=foo\n"
	if err := os.WriteFile(envFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	generated, err := ensureAPIKeys(envFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !generated {
		t.Error("expected generated=true when the secret key is only commented out")
	}

	data, _ := os.ReadFile(envFile)
	if !hasActiveEnvKey(string(data), "INSTANCEZ_SECRET_KEY") {
		t.Error("expected active INSTANCEZ_SECRET_KEY after generation")
	}
	// The pre-existing publishable key must be left untouched.
	if !strings.Contains(string(data), "inz_publishable_existing") {
		t.Errorf("expected existing publishable key preserved:\n%s", string(data))
	}
}
