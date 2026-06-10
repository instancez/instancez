package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saedx1/instancez/internal/config"
	"github.com/saedx1/instancez/internal/domain"
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

func TestShouldBootstrap(t *testing.T) {
	cases := []struct {
		name                   string
		owner, auth, superuser string
		want                   bool
	}{
		{"both role DSNs set → skip even with superuser", "o", "a", "super", false},
		{"both role DSNs set, no superuser → skip", "o", "a", "", false},
		{"only owner set + superuser → bootstrap", "o", "", "super", true},
		{"neither role DSN, superuser set → bootstrap", "", "", "super", true},
		{"neither role DSN, no superuser → skip", "", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldBootstrap(tc.owner, tc.auth, tc.superuser); got != tc.want {
				t.Errorf("shouldBootstrap(%q,%q,%q) = %v, want %v", tc.owner, tc.auth, tc.superuser, got, tc.want)
			}
		})
	}
}

func TestEnsureRolesSkipsWhenRoleDSNsPresent(t *testing.T) {
	// Both role DSNs set → ensureRoles must be a no-op and never touch a DB.
	t.Setenv("INSTANCEZ_OWNER_DATABASE_URL", "postgres://owner@localhost/db")
	t.Setenv("INSTANCEZ_AUTH_DATABASE_URL", "postgres://auth@localhost/db")

	res, err := ensureRoles(context.Background(), "postgres://super@localhost/db", filepath.Join(t.TempDir(), ".development.env"))
	if err != nil {
		t.Fatalf("ensureRoles: %v", err)
	}
	if res.Ran {
		t.Error("ensureRoles ran bootstrap despite both role DSNs being set")
	}
}

// TestEnsureRolesSkipsAfterDotenvPersist exercises the real "second run" flow
// end-to-end: a prior run persisted both role DSNs to .development.env, so
// loading that dotenv populates the env and ensureRoles must skip bootstrap —
// even with a superuser DSN still available — and never touch a database. This
// closes the round-trip (LoadDotenv → skip) that the env-only skip test doesn't
// cover, and guards against a re-bootstrap-every-run regression.
func TestEnsureRolesSkipsAfterDotenvPersist(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".development.env")
	if err := os.WriteFile(envFile, []byte(
		"INSTANCEZ_OWNER_DATABASE_URL=postgres://owner@h/db\n"+
			"INSTANCEZ_AUTH_DATABASE_URL=postgres://auth@h/db\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Register restoration of any ambient values, then unset so LoadDotenv (which
	// never overrides a real env var) actually populates from the file.
	t.Setenv("INSTANCEZ_OWNER_DATABASE_URL", "")
	t.Setenv("INSTANCEZ_AUTH_DATABASE_URL", "")
	os.Unsetenv("INSTANCEZ_OWNER_DATABASE_URL")
	os.Unsetenv("INSTANCEZ_AUTH_DATABASE_URL")

	if err := config.LoadDotenv(envFile); err != nil {
		t.Fatalf("LoadDotenv: %v", err)
	}

	// A superuser DSN is present but must be ignored: both role DSNs are set, so
	// ensureRoles returns before any bootstrapDB call (the bogus DSN proves no
	// connection is attempted).
	res, err := ensureRoles(context.Background(), "postgres://super@h/db", envFile)
	if err != nil {
		t.Fatalf("ensureRoles: %v", err)
	}
	if res.Ran {
		t.Error("ensureRoles re-bootstrapped on a second run despite persisted role DSNs")
	}
}

func TestEnsureRolesNoopWhenNothingToDo(t *testing.T) {
	// No role DSNs and no superuser → no-op (the caller's missing-DSN path fires).
	t.Setenv("INSTANCEZ_OWNER_DATABASE_URL", "")
	t.Setenv("INSTANCEZ_AUTH_DATABASE_URL", "")

	res, err := ensureRoles(context.Background(), "", filepath.Join(t.TempDir(), ".development.env"))
	if err != nil {
		t.Fatalf("ensureRoles: %v", err)
	}
	if res.Ran {
		t.Error("ensureRoles ran bootstrap with no superuser DSN")
	}
}

func TestPersistDSNsCreatesAndMerges(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".development.env")

	// Create-from-empty path.
	adminKey, err := persistDSNs(envFile, "postgres://owner@h/db", "postgres://auth@h/db")
	if err != nil {
		t.Fatalf("persistDSNs (create): %v", err)
	}
	if adminKey == "" {
		t.Error("create path did not generate an admin key")
	}
	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{
		"INSTANCEZ_OWNER_DATABASE_URL=postgres://owner@h/db",
		"INSTANCEZ_AUTH_DATABASE_URL=postgres://auth@h/db",
		"INSTANCEZ_ADMIN_KEY=" + adminKey,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("created env file missing %q\n--- got ---\n%s", want, got)
		}
	}

	// Merge path: a user-added line survives; the DSNs are updated in place; and
	// an admin key is appended since the file has none.
	if err := os.WriteFile(envFile, []byte("MY_CUSTOM=keep\nINSTANCEZ_OWNER_DATABASE_URL=old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	adminKey, err = persistDSNs(envFile, "postgres://owner@h/db2", "postgres://auth@h/db2")
	if err != nil {
		t.Fatalf("persistDSNs (merge): %v", err)
	}
	if adminKey == "" {
		t.Error("merge path did not generate an admin key when none present")
	}
	data, _ = os.ReadFile(envFile)
	got = string(data)
	if !strings.Contains(got, "MY_CUSTOM=keep") {
		t.Errorf("merge dropped user line:\n%s", got)
	}
	if !strings.Contains(got, "INSTANCEZ_OWNER_DATABASE_URL=postgres://owner@h/db2") {
		t.Errorf("merge did not update owner DSN:\n%s", got)
	}
	if !strings.Contains(got, "INSTANCEZ_AUTH_DATABASE_URL=postgres://auth@h/db2") {
		t.Errorf("merge did not append auth DSN:\n%s", got)
	}
	if !strings.Contains(got, "INSTANCEZ_ADMIN_KEY="+adminKey) {
		t.Errorf("merge did not append admin key:\n%s", got)
	}

	// Idempotent merge: a file that already declares an admin key keeps it
	// untouched, and no new key is generated.
	if err := os.WriteFile(envFile, []byte("INSTANCEZ_ADMIN_KEY=mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	adminKey, err = persistDSNs(envFile, "postgres://owner@h/db3", "postgres://auth@h/db3")
	if err != nil {
		t.Fatalf("persistDSNs (idempotent): %v", err)
	}
	if adminKey != "" {
		t.Errorf("persistDSNs regenerated admin key when one already present: %q", adminKey)
	}
	data, _ = os.ReadFile(envFile)
	got = string(data)
	if !strings.Contains(got, "INSTANCEZ_ADMIN_KEY=mine") {
		t.Errorf("persistDSNs overwrote existing admin key:\n%s", got)
	}
	if strings.Count(got, "INSTANCEZ_ADMIN_KEY=") != 1 {
		t.Errorf("expected exactly one admin key line:\n%s", got)
	}
}
