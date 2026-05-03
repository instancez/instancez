package app

import (
	"strings"
	"testing"

	"github.com/saedx1/ultrabase/internal/domain"
)

func TestGenerateRoleDDL_Defaults(t *testing.T) {
	stmts := generateRoleDDL(domain.DefaultRoles())
	joined := strings.Join(stmts, "\n")

	wants := []string{
		"CREATE ROLE anon NOLOGIN",
		"CREATE ROLE authenticated NOLOGIN",
		"CREATE ROLE service_role NOLOGIN BYPASSRLS",
		"GRANT anon, authenticated, service_role TO authenticator",
		"GRANT USAGE ON SCHEMA public TO anon, authenticated, service_role",
		"ALTER DEFAULT PRIVILEGES",
	}
	for _, w := range wants {
		if !strings.Contains(joined, w) {
			t.Errorf("DDL missing %q\n--- DDL ---\n%s", w, joined)
		}
	}

	// Idempotency markers — every CREATE ROLE wrapped in a guard.
	if cnt := strings.Count(joined, "IF NOT EXISTS (SELECT 1 FROM pg_roles"); cnt != 3 {
		t.Errorf("expected 3 idempotent CREATE ROLE blocks, got %d", cnt)
	}
}

func TestGenerateRoleDDL_CustomNames(t *testing.T) {
	r := domain.Roles{
		Authenticator: "rest_login",
		Anon:          "guest",
		Authenticated: "member",
		Service:       "admin_role",
	}
	joined := strings.Join(generateRoleDDL(r), "\n")

	for _, w := range []string{
		"CREATE ROLE guest NOLOGIN",
		"CREATE ROLE member NOLOGIN",
		"CREATE ROLE admin_role NOLOGIN BYPASSRLS",
		"GRANT guest, member, admin_role TO rest_login",
	} {
		if !strings.Contains(joined, w) {
			t.Errorf("custom-roles DDL missing %q", w)
		}
	}
	for _, banned := range []string{"anon", "authenticated", "service_role"} {
		if strings.Contains(joined, banned) {
			t.Errorf("custom-roles DDL leaked default name %q:\n%s", banned, joined)
		}
	}
}
