package app

import (
	"strings"
	"testing"

	"github.com/saedx1/ultrabase/internal/domain"
)

func TestGenerateRoleDDL_Defaults(t *testing.T) {
	joined := strings.Join(generateRoleDDL(domain.DefaultRoles()), "\n")

	for _, w := range []string{
		"CREATE ROLE anon NOLOGIN",
		"CREATE ROLE authenticated NOLOGIN",
		"CREATE ROLE service_role NOLOGIN BYPASSRLS",
		"GRANT anon, authenticated, service_role TO authenticator",
	} {
		if !strings.Contains(joined, w) {
			t.Errorf("DDL missing %q\n--- DDL ---\n%s", w, joined)
		}
	}
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

func TestGenerateSchemaGrants_PerSchema(t *testing.T) {
	got := strings.Join(generateSchemaGrants([]string{"public", "private"}, domain.DefaultRoles()), "\n")

	for _, w := range []string{
		"CREATE SCHEMA IF NOT EXISTS public;",
		"GRANT USAGE ON SCHEMA public TO anon, authenticated, service_role;",
		"ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO anon, authenticated, service_role;",
		"CREATE SCHEMA IF NOT EXISTS private;",
		"GRANT USAGE ON SCHEMA private TO anon, authenticated, service_role;",
		"ALTER DEFAULT PRIVILEGES IN SCHEMA private GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO anon, authenticated, service_role;",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("schema grants missing %q\n%s", w, got)
		}
	}
}

func TestOrderedSchemas_PublicFirstThenReferenced(t *testing.T) {
	cfg := &domain.Config{Tables: map[string]domain.Table{
		"a": {Schema: "private"},
		"b": {Schema: "public"},
		"c": {Schema: "private"}, // dup; must dedupe
		"d": {Schema: "analytics"},
	}}
	got := orderedSchemas(cfg)
	if got[0] != "public" {
		t.Errorf("public must come first, got %v", got)
	}
	seen := map[string]int{}
	for _, s := range got {
		seen[s]++
	}
	for s, n := range seen {
		if n > 1 {
			t.Errorf("schema %q appears %d times", s, n)
		}
	}
	for _, want := range []string{"public", "private", "analytics"} {
		if seen[want] != 1 {
			t.Errorf("missing schema %q in %v", want, got)
		}
	}
}
