package app

import (
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
	"github.com/stretchr/testify/assert"
)

// TestPlanFromScratch_NoRoleDDL asserts the migration does not emit
// CREATE ROLE / GRANT … TO authenticator statements. Roles are infrastructure
// (provisioned by the CLI startup sequence (bootstrapDB) on every startup); the
// migration must not touch them.
func TestPlanFromScratch_NoRoleDDL(t *testing.T) {
	plan := planFromScratch(&domain.Config{}, domain.DefaultRoles())

	for _, banned := range []string{
		"CREATE ROLE",
		"GRANT anon, authenticated, service_role TO",
		"pg_has_role",
	} {
		if strings.Contains(plan, banned) {
			t.Errorf("plan must not contain %q (roles are infrastructure)\n--- plan ---\n%s", banned, plan)
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

func TestGenerateSchemaGrants_SeedRole(t *testing.T) {
	roles := domain.Roles{Anon: "anon", Authenticated: "authenticated", Service: "service_role", Seed: "app_x_seed"}
	ddl := strings.Join(generateSchemaGrants([]string{"public", "auth", "storage", "app"}, roles), "\n")

	// seed role granted on user schemas
	assert.Contains(t, ddl, "ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO app_x_seed;")
	assert.Contains(t, ddl, "ALTER DEFAULT PRIVILEGES IN SCHEMA app GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO app_x_seed;")
	assert.Contains(t, ddl, "GRANT USAGE ON SCHEMA public TO app_x_seed;")
	// seed role NOT granted on reserved schemas
	assert.NotContains(t, ddl, "SCHEMA auth GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO app_x_seed")
	assert.NotContains(t, ddl, "SCHEMA storage GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO app_x_seed")

	// empty Seed: no seed grants at all (back-compat during rollout)
	noSeed := domain.Roles{Anon: "anon", Authenticated: "authenticated", Service: "service_role"}
	ddl2 := strings.Join(generateSchemaGrants([]string{"public", "auth"}, noSeed), "\n")
	assert.NotContains(t, ddl2, "_seed")
}

func TestGenerateExistingObjectGrants_SeedRevokesFrameworkTable(t *testing.T) {
	roles := domain.Roles{Anon: "anon", Authenticated: "authenticated", Service: "service_role", Seed: "app_x_seed"}
	ddl := strings.Join(generateExistingObjectGrants([]string{"public"}, roles), "\n")
	assert.Contains(t, ddl, "GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO app_x_seed;")
	assert.Contains(t, ddl, "REVOKE ALL ON _instancez_migrations FROM app_x_seed;")
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
