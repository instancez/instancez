package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saedx1/ultrabase/internal/domain"
)

func TestRolesFromEnv_Defaults(t *testing.T) {
	for _, k := range []string{
		"ULTRABASE_DB_AUTHENTICATOR_ROLE",
		"ULTRABASE_DB_ANON_ROLE",
		"ULTRABASE_DB_AUTHENTICATED_ROLE",
		"ULTRABASE_DB_SERVICE_ROLE",
	} {
		t.Setenv(k, "")
	}
	got := rolesFromEnv()
	if got != domain.DefaultRoles() {
		t.Fatalf("rolesFromEnv with no env = %+v, want defaults %+v", got, domain.DefaultRoles())
	}
}

func TestRolesFromEnv_Overrides(t *testing.T) {
	t.Setenv("ULTRABASE_DB_AUTHENTICATOR_ROLE", "rest_login")
	t.Setenv("ULTRABASE_DB_ANON_ROLE", "guest")
	t.Setenv("ULTRABASE_DB_AUTHENTICATED_ROLE", "member")
	t.Setenv("ULTRABASE_DB_SERVICE_ROLE", "admin_role")

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
	t.Setenv("ULTRABASE_DB_AUTHENTICATOR_ROLE", "")
	t.Setenv("ULTRABASE_DB_ANON_ROLE", "guest")
	t.Setenv("ULTRABASE_DB_AUTHENTICATED_ROLE", "")
	t.Setenv("ULTRABASE_DB_SERVICE_ROLE", "")

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
	t.Setenv("ULTRABASE_OWNER_DATABASE_URL", "postgres://owner@localhost/db")
	t.Setenv("ULTRABASE_AUTH_DATABASE_URL", "postgres://auth@localhost/db")

	res, err := ensureRoles(context.Background(), "postgres://super@localhost/db", filepath.Join(t.TempDir(), ".development.env"))
	if err != nil {
		t.Fatalf("ensureRoles: %v", err)
	}
	if res.Ran {
		t.Error("ensureRoles ran bootstrap despite both role DSNs being set")
	}
}

func TestEnsureRolesNoopWhenNothingToDo(t *testing.T) {
	// No role DSNs and no superuser → no-op (the caller's missing-DSN path fires).
	t.Setenv("ULTRABASE_OWNER_DATABASE_URL", "")
	t.Setenv("ULTRABASE_AUTH_DATABASE_URL", "")

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
	if err := persistDSNs(envFile, "postgres://owner@h/db", "postgres://auth@h/db"); err != nil {
		t.Fatalf("persistDSNs (create): %v", err)
	}
	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{
		"ULTRABASE_OWNER_DATABASE_URL=postgres://owner@h/db",
		"ULTRABASE_AUTH_DATABASE_URL=postgres://auth@h/db",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("created env file missing %q\n--- got ---\n%s", want, got)
		}
	}

	// Merge path: a user-added line survives; the DSNs are updated in place.
	if err := os.WriteFile(envFile, []byte("MY_CUSTOM=keep\nULTRABASE_OWNER_DATABASE_URL=old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := persistDSNs(envFile, "postgres://owner@h/db2", "postgres://auth@h/db2"); err != nil {
		t.Fatalf("persistDSNs (merge): %v", err)
	}
	data, _ = os.ReadFile(envFile)
	got = string(data)
	if !strings.Contains(got, "MY_CUSTOM=keep") {
		t.Errorf("merge dropped user line:\n%s", got)
	}
	if !strings.Contains(got, "ULTRABASE_OWNER_DATABASE_URL=postgres://owner@h/db2") {
		t.Errorf("merge did not update owner DSN:\n%s", got)
	}
	if !strings.Contains(got, "ULTRABASE_AUTH_DATABASE_URL=postgres://auth@h/db2") {
		t.Errorf("merge did not append auth DSN:\n%s", got)
	}
}
