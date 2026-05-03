package cli

import (
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
