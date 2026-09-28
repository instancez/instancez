package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/instancez/instancez/internal/domain"
)

func TestGenerateTable_Basic(t *testing.T) {
	table := domain.Table{
		Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "title", Type: "text", Required: true},
		},
	}
	ddl := generateTable("todos", table, nil)
	joined := strings.Join(ddl, "\n")

	mustContain(t, joined, "CREATE TABLE IF NOT EXISTS todos")
	mustContain(t, joined, "id bigserial PRIMARY KEY")
	mustContain(t, joined, "title text NOT NULL")
}

func TestGenerateTable_ForeignKey(t *testing.T) {
	table := domain.Table{
		Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "user_id", ForeignKey: &domain.ForeignKey{References: "users.id", OnDelete: "cascade"}},
		},
	}
	ddl := generateTable("todos", table, nil)
	joined := strings.Join(ddl, "\n")

	// FK referencing users.id (a user-defined public.users table) infers BIGINT.
	// Only auth.users.id (3-part reference) auto-infers UUID.
	mustContain(t, joined, "user_id BIGINT")
	mustContain(t, joined, "FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE")
}

func TestGenerateTable_FKDefaultRestrict(t *testing.T) {
	table := domain.Table{
		Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "team_id", ForeignKey: &domain.ForeignKey{References: "teams.id"}},
		},
	}
	ddl := generateTable("members", table, nil)
	joined := strings.Join(ddl, "\n")

	mustContain(t, joined, "ON DELETE RESTRICT")
}

func TestGenerateTable_Enum(t *testing.T) {
	table := domain.Table{
		Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "status", Type: "text", Enum: []string{"pending", "active", "done"}},
		},
	}
	ddl := generateTable("todos", table, nil)
	joined := strings.Join(ddl, "\n")

	mustContain(t, joined, "CHECK (status IN ('pending', 'active', 'done'))")
}

func TestGenerateTable_MinMax(t *testing.T) {
	min := float64(0)
	max := float64(5)
	table := domain.Table{
		Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "priority", Type: "integer", Min: &min, Max: &max},
		},
	}
	ddl := generateTable("todos", table, nil)
	joined := strings.Join(ddl, "\n")

	mustContain(t, joined, "CHECK (priority >= 0)")
	mustContain(t, joined, "CHECK (priority <= 5)")
}

func TestGenerateTable_Pattern(t *testing.T) {
	table := domain.Table{
		Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "email", Type: "text", Pattern: "^.+@.+$"},
		},
	}
	ddl := generateTable("contacts", table, nil)
	joined := strings.Join(ddl, "\n")

	mustContain(t, joined, "CHECK (email ~ '^.+@.+$')")
}

func TestGenerateTable_Unique(t *testing.T) {
	table := domain.Table{
		Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "slug", Type: "text", Unique: true},
		},
	}
	ddl := generateTable("teams", table, nil)
	joined := strings.Join(ddl, "\n")

	mustContain(t, joined, "UNIQUE (slug)")
}

func TestGenerateTable_Indexes(t *testing.T) {
	table := domain.Table{
		Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "team_id", Type: "bigint"},
			{Name: "status", Type: "text"},
		},
		Indexes: []domain.Index{
			{Columns: []string{"team_id", "status"}},
			{Columns: []string{"status"}, Unique: true},
			{Columns: []string{"team_id"}, Where: "status != 'done'"},
		},
	}
	ddl := generateIndexes("todos", table)
	joined := strings.Join(ddl, "\n")

	mustContain(t, joined, "CREATE INDEX IF NOT EXISTS idx_todos_team_id_status ON todos (team_id, status)")
	mustContain(t, joined, "CREATE UNIQUE INDEX IF NOT EXISTS idx_todos_status ON todos (status)")
	mustContain(t, joined, "WHERE status != 'done'")
}

func TestGenerateTable_Default(t *testing.T) {
	table := domain.Table{
		Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "status", Type: "text", Default: "pending"},
			{Name: "created_at", Type: "timestamptz", Default: "now()"},
			{Name: "priority", Type: "integer", Default: 0},
			{Name: "active", Type: "boolean", Default: false},
		},
	}
	ddl := generateTable("todos", table, nil)
	joined := strings.Join(ddl, "\n")

	mustContain(t, joined, "DEFAULT 'pending'")
	mustContain(t, joined, "DEFAULT now()")
	mustContain(t, joined, "DEFAULT 0")
	mustContain(t, joined, "DEFAULT FALSE")
}

func TestGenerateAuthTables(t *testing.T) {
	auth := &domain.Auth{
		Email: &domain.AuthEmail{VerifyEmail: true},
	}
	ddl := generateAuthTables(auth)
	joined := strings.Join(ddl, "\n")

	mustContain(t, joined, "CREATE SCHEMA IF NOT EXISTS auth")
	mustContain(t, joined, "CREATE TABLE IF NOT EXISTS auth.users")
	mustContain(t, joined, "email TEXT UNIQUE")
	mustContain(t, joined, "password_hash TEXT")
	mustContain(t, joined, "email_verified BOOLEAN")
	mustContain(t, joined, "is_anonymous BOOLEAN NOT NULL DEFAULT FALSE")
	mustContain(t, joined, "CREATE TABLE IF NOT EXISTS auth.identities")
	mustContain(t, joined, "CREATE TABLE IF NOT EXISTS auth.refresh_tokens")
	mustContain(t, joined, "CREATE TABLE IF NOT EXISTS auth.one_time_tokens")
	mustContain(t, joined, "CREATE TABLE IF NOT EXISTS auth.mfa_factors")
	mustContain(t, joined, "CREATE TABLE IF NOT EXISTS auth.mfa_challenges")
	mustContain(t, joined, "CREATE INDEX IF NOT EXISTS idx_one_time_tokens_email_code ON auth.one_time_tokens")
	mustContain(t, joined, "CREATE INDEX IF NOT EXISTS idx_mfa_factors_user ON auth.mfa_factors")
	mustContain(t, joined, "CREATE TABLE IF NOT EXISTS auth.flow_state")
}

func TestGenerateAuthTables_AlwaysIncludesRefreshTokens(t *testing.T) {
	ddl := generateAuthTables(&domain.Auth{})
	joined := strings.Join(ddl, "\n")
	if !strings.Contains(joined, "CREATE TABLE IF NOT EXISTS auth.refresh_tokens") {
		t.Fatal("auth.refresh_tokens must always be created (refresh tokens are always-on)")
	}
}

// A config without an auth block must still create auth.jwt_keys: the migrator
// emits it for every app so the JWKS endpoint and user-token verification work
// even with user-facing auth disabled. Regression for the "relation
// auth.jwt_keys does not exist" invoke failure on function-only apps.
func TestPlanFromScratch_JWTKeysWithoutAuth(t *testing.T) {
	cfg := &domain.Config{} // no Auth
	joined := strings.Join(planFromScratchStatements(cfg, domain.DefaultRoles()), "\n")

	mustContain(t, joined, "CREATE TABLE IF NOT EXISTS auth.jwt_keys")
	if strings.Contains(joined, "CREATE TABLE IF NOT EXISTS auth.users") {
		t.Fatal("auth.users should not be created when auth is disabled")
	}
}

// The update path must re-emit auth.jwt_keys unconditionally so an app that was
// already deployed without it heals whenever its config next changes.
func TestPlanUpdate_ReassertsJWTKeys(t *testing.T) {
	oldCfg := &domain.Config{}
	newCfg := &domain.Config{Version: 1} // any change
	joined := strings.Join(planUpdateStatements(oldCfg, newCfg, domain.DefaultRoles()), "\n")

	mustContain(t, joined, "CREATE TABLE IF NOT EXISTS auth.jwt_keys")
}

func TestGenerateRLSPolicies(t *testing.T) {
	table := domain.Table{
		RLS: []domain.RLSPolicy{
			{Operations: []string{"select"}, Using: "user_id = auth.uid()"},
			{Operations: []string{"insert"}, WithCheck: "auth.is_authenticated()"},
		},
	}
	ddl := generateRLSPolicies("todos", table)
	joined := strings.Join(ddl, "\n")

	mustContain(t, joined, "ENABLE ROW LEVEL SECURITY")
	mustContain(t, joined, "FORCE ROW LEVEL SECURITY")
	mustContain(t, joined, "DROP POLICY IF EXISTS todos_select_0 ON todos")
	mustContain(t, joined, "FOR SELECT USING (user_id = auth.uid())")
	mustContain(t, joined, "DROP POLICY IF EXISTS todos_insert_1 ON todos")
	mustContain(t, joined, "FOR INSERT WITH CHECK (auth.is_authenticated())")
}

// TestGenerateRLSPolicies_UpdateDivergentUsingWithCheck pins the feature this
// whole change exists for: an update policy where "which rows you can touch"
// and "what the row can become" are genuinely different expressions.
func TestGenerateRLSPolicies_UpdateDivergentUsingWithCheck(t *testing.T) {
	table := domain.Table{
		RLS: []domain.RLSPolicy{
			{
				Operations: []string{"update"},
				Using:      "owner_id = auth.uid()",
				WithCheck:  "owner_id = auth.uid() AND status <> 'locked'",
			},
		},
	}
	ddl := generateRLSPolicies("orders", table)
	joined := strings.Join(ddl, "\n")

	mustContain(t, joined, "FOR UPDATE USING (owner_id = auth.uid()) WITH CHECK (owner_id = auth.uid() AND status <> 'locked')")
}

// TestGenerateRLSPolicies_SelectDeleteNeverEmitWithCheck locks in the Postgres
// syntax constraint: WITH CHECK is illegal on SELECT/DELETE policies, so even
// if with_check were set on a select/delete-only entry (validation would
// reject this — this test exercises the DDL layer in isolation), it must
// never appear in the generated statement.
func TestGenerateRLSPolicies_SelectDeleteNeverEmitWithCheck(t *testing.T) {
	table := domain.Table{
		RLS: []domain.RLSPolicy{
			{Operations: []string{"select", "delete"}, Using: "true"},
		},
	}
	ddl := generateRLSPolicies("todos", table)
	joined := strings.Join(ddl, "\n")

	mustNotContain(t, joined, "WITH CHECK")
	mustContain(t, joined, "FOR SELECT USING (true)")
	mustContain(t, joined, "FOR DELETE USING (true)")
}

func TestGenerateRLSPolicies_NoRLS(t *testing.T) {
	table := domain.Table{}
	ddl := generateRLSPolicies("todos", table)
	if len(ddl) != 0 {
		t.Errorf("expected no DDL for table without RLS, got %d statements", len(ddl))
	}
}

func TestGenerateStorageTablesUsesStorageSchema(t *testing.T) {
	cfg := &domain.Config{
		Storage: map[string]domain.Bucket{"avatars": {}},
	}
	ddl := strings.Join(generateStorageTables(cfg), "\n")
	mustContain(t, ddl, "CREATE SCHEMA IF NOT EXISTS storage;")
	mustContain(t, ddl, "CREATE TABLE IF NOT EXISTS storage.objects")
	mustContain(t, ddl, "REFERENCES auth.users(id)")
	mustNotContain(t, ddl, "CREATE TABLE IF NOT EXISTS _objects")
}

func TestGenerateStorageTablesEmptyWhenNoBuckets(t *testing.T) {
	cfg := &domain.Config{}
	ddl := generateStorageTables(cfg)
	if len(ddl) != 0 {
		t.Errorf("expected no DDL when no buckets configured, got: %v", ddl)
	}
}

func TestStorageTablesIncludeUserMetadata(t *testing.T) {
	cfg := &domain.Config{Storage: map[string]domain.Bucket{"avatars": {}}}
	stmts := generateStorageTables(cfg)
	joined := strings.Join(stmts, "\n")
	if !strings.Contains(joined, "user_metadata") {
		t.Fatalf("expected storage.objects DDL to define user_metadata, got:\n%s", joined)
	}
}

func TestGenerateStorageRLS_Public(t *testing.T) {
	bucket := domain.Bucket{
		Public: true,
		RLS: []domain.RLSPolicy{
			{Operations: []string{"insert"}, WithCheck: "uploaded_by = auth.uid()"},
		},
	}
	ddl := generateStorageRLS("avatars", bucket)
	joined := strings.Join(ddl, "\n")

	mustNotContain(t, joined, "avatars_public_select")
	mustContain(t, joined, "bucket_id = 'avatars'")
	mustContain(t, joined, "DROP POLICY IF EXISTS storage_avatars_insert_0 ON storage.objects")
	mustContain(t, joined, "FOR INSERT WITH CHECK")
}

// No bucket grants a public SELECT; the legacy policy is dropped for every bucket, public or not.
func TestGenerateStorageRLSAll_PublicBucketDropsLegacySelect(t *testing.T) {
	cases := map[string]map[string]domain.Bucket{
		"no rls anywhere": {"avatars": {Public: true}, "documents": {}},
		"rls elsewhere":   {"avatars": {Public: true}, "documents": {RLS: []domain.RLSPolicy{{Operations: []string{"select"}, Using: "true"}}}},
		"rls on public":   {"avatars": {Public: true, RLS: []domain.RLSPolicy{{Operations: []string{"select"}, Using: "auth.role() = 'authenticated'"}}}, "documents": {}},
	}
	for name, storage := range cases {
		t.Run(name, func(t *testing.T) {
			joined := strings.Join(generateStorageRLSAll(storage), "\n")
			mustContain(t, joined, "DROP POLICY IF EXISTS avatars_public_select ON storage.objects;")
			mustNotContain(t, joined, "CREATE POLICY avatars_public_select")
			mustContain(t, joined, "DROP POLICY IF EXISTS documents_public_select ON storage.objects;")
		})
	}
}

func TestHealStorageRLS(t *testing.T) {
	if got := healStorageRLS(nil); got != nil {
		t.Fatalf("nil storage: %v", got)
	}
	got := healStorageRLS(map[string]domain.Bucket{"b_pub": {Public: true}, "a_priv": {}})
	if len(got) != 1 {
		t.Fatalf("want one guarded statement, got %d: %v", len(got), got)
	}
	mustContain(t, got[0], "policyname = 'a_priv_public_select') THEN\nDROP POLICY a_priv_public_select ON storage.objects;\nEND IF;")
	mustContain(t, got[0], "policyname = 'b_pub_public_select') THEN\nDROP POLICY b_pub_public_select ON storage.objects;\nEND IF;")
	if strings.Index(got[0], "a_priv_public_select") > strings.Index(got[0], "b_pub_public_select") {
		t.Fatalf("heal must iterate buckets in sorted order: %s", got[0])
	}
	mustNotContain(t, got[0], "CREATE POLICY")

	both := healStorageRLS(map[string]domain.Bucket{
		"pub": {Public: true, RLS: []domain.RLSPolicy{
			{Operations: []string{"select"}, Using: "true"},
			{Operations: []string{"select", "update"}, Using: "name <> ''", Type: "restrictive"},
		}},
	})
	if len(both) != 2 {
		t.Fatalf("public+restrictive: want drop and re-emit statements, got %d: %v", len(both), both)
	}
	mustContain(t, both[1], "IF to_regclass('storage.objects') IS NOT NULL THEN")
	mustContain(t, both[1], "policyname IN ('storage_pub_select_1', 'storage_pub_update_1')")
	mustContain(t, both[1], "position('bucket_id <> ''pub''' IN qual) = 0")
	mustNotContain(t, both[1], "'storage_pub_select_0'")
	mustNotContain(t, both[1], "pub_public_select")
}

// TestGenerateStorageRLS_UpdateDivergentUsingWithCheck mirrors the table-path
// test in TestGenerateRLSPolicies_UpdateDivergentUsingWithCheck, confirming
// the bucket_id scoping (scopeToBucket) is applied to using and with_check
// independently rather than only to a single shared expression.
func TestGenerateStorageRLS_UpdateDivergentUsingWithCheck(t *testing.T) {
	bucket := domain.Bucket{
		RLS: []domain.RLSPolicy{
			{
				Operations: []string{"update"},
				Using:      "uploaded_by = auth.uid()",
				WithCheck:  "uploaded_by = auth.uid() AND name LIKE 'mine/%'",
			},
		},
	}
	ddl := generateStorageRLS("documents", bucket)
	joined := strings.Join(ddl, "\n")

	mustContain(t, joined, "FOR UPDATE USING (bucket_id = 'documents' AND (uploaded_by = auth.uid())) WITH CHECK (bucket_id = 'documents' AND (uploaded_by = auth.uid() AND name LIKE 'mine/%'))")
}

// T6: a restrictive policy must scope with OR-negation, not AND, or it denies every other bucket.
func TestGenerateStorageRLS_RestrictiveScopedByOr(t *testing.T) {
	bucket := domain.Bucket{
		RLS: []domain.RLSPolicy{
			{Operations: []string{"select"}, Using: "name LIKE 'ok/%'", Type: "restrictive"},
			{Operations: []string{"update"}, WithCheck: "name LIKE 'ok/%'", Type: "restrictive"},
		},
	}
	ddl := generateStorageRLS("secrets", bucket)
	joined := strings.Join(ddl, "\n")

	mustContain(t, joined, "FOR SELECT USING (bucket_id <> 'secrets' OR (name LIKE 'ok/%'))")
	mustContain(t, joined, "FOR UPDATE WITH CHECK (bucket_id <> 'secrets' OR (name LIKE 'ok/%'))")
	if strings.Contains(joined, "bucket_id = 'secrets' AND") {
		t.Fatalf("restrictive policy must not use AND-scoping, got:\n%s", joined)
	}
}

// An empty using/with_check must stay empty, even for a restrictive policy.
func TestGenerateStorageRLS_RestrictiveEmptyExprStaysEmpty(t *testing.T) {
	bucket := domain.Bucket{
		RLS: []domain.RLSPolicy{
			{Operations: []string{"insert"}, WithCheck: "name LIKE 'ok/%'", Type: "restrictive"},
		},
	}
	ddl := generateStorageRLS("secrets", bucket)
	joined := strings.Join(ddl, "\n")

	mustContain(t, joined, "FOR INSERT WITH CHECK (bucket_id <> 'secrets' OR (name LIKE 'ok/%'))")
	if strings.Contains(joined, "USING") {
		t.Fatalf("insert-only policy must not emit a USING clause, got:\n%s", joined)
	}
}

// TestGenerateStorageRLSAll_GatingModel locks in the storage authorization
// model: RLS is only enabled on storage.objects when a bucket opts in by
// declaring policies, and opt-out buckets stay open via a default-allow policy.
func TestGenerateStorageRLSAll_GatingModel(t *testing.T) {
	// No bucket declares RLS → RLS stays disabled (open behaviour preserved).
	noRLS := map[string]domain.Bucket{
		"avatars":   {Public: true},
		"documents": {Public: false},
	}
	joined := strings.Join(generateStorageRLSAll(noRLS), "\n")
	if strings.Contains(joined, "ENABLE ROW LEVEL SECURITY") {
		t.Errorf("RLS must not be enabled when no bucket declares policies:\n%s", joined)
	}

	// One bucket opts in → RLS enabled table-wide; the opt-out bucket gets a
	// permissive default so it remains open; the opt-in bucket is enforced.
	withRLS := map[string]domain.Bucket{
		"secrets": {RLS: []domain.RLSPolicy{
			{Operations: []string{"select"}, Using: "uploaded_by = auth.uid()"},
		}},
		"documents": {Public: false},
	}
	joined = strings.Join(generateStorageRLSAll(withRLS), "\n")
	mustContain(t, joined, "ALTER TABLE storage.objects ENABLE ROW LEVEL SECURITY")
	mustContain(t, joined, "ALTER TABLE storage.objects FORCE ROW LEVEL SECURITY")
	mustContain(t, joined, "storage_secrets_select_0") // enforced policy
	mustContain(t, joined, "documents_default_all")    // opt-out stays open
	mustContain(t, joined, "uploaded_by = auth.uid()")
}

func TestOrderTables_NoDeps(t *testing.T) {
	tables := map[string]domain.Table{
		"a": {Fields: []domain.Field{{Name: "id", Type: "bigserial"}}},
		"b": {Fields: []domain.Field{{Name: "id", Type: "bigserial"}}},
	}
	order := orderTables(tables)
	if len(order) != 2 {
		t.Fatalf("expected 2 tables, got %d", len(order))
	}
	// Should be alphabetical when no deps
	if order[0] != "a" || order[1] != "b" {
		t.Errorf("expected [a, b], got %v", order)
	}
}

func TestOrderTables_WithDeps(t *testing.T) {
	tables := map[string]domain.Table{
		"todos": {Fields: []domain.Field{
			{Name: "id", Type: "bigserial"},
			{Name: "team_id", ForeignKey: &domain.ForeignKey{References: "teams.id"}},
		}},
		"teams": {Fields: []domain.Field{
			{Name: "id", Type: "bigserial"},
		}},
	}
	order := orderTables(tables)
	teamIdx, todoIdx := -1, -1
	for i, name := range order {
		switch name {
		case "teams":
			teamIdx = i
		case "todos":
			todoIdx = i
		}
	}
	if teamIdx >= todoIdx {
		t.Errorf("teams (idx %d) should come before todos (idx %d)", teamIdx, todoIdx)
	}
}

func TestFormatDefault_SQLFunctions(t *testing.T) {
	tests := []struct {
		val  any
		typ  string
		want string
	}{
		{"now()", "timestamptz", "now()"},
		{"uuid_v7()", "uuid", "gen_random_uuid()"},
		{"uuid_v4()", "uuid", "gen_random_uuid()"},
		{"current_date", "date", "current_date"},
		{"pending", "text", "'pending'"},
		{42, "integer", "42"},
		{true, "boolean", "TRUE"},
		{false, "boolean", "FALSE"},
		{3.14, "numeric", "3.14"},
	}
	for _, tt := range tests {
		t.Run(strings.ReplaceAll(tt.want, "'", ""), func(t *testing.T) {
			got := formatDefault(tt.val, tt.typ)
			if got != tt.want {
				t.Errorf("formatDefault(%v, %q) = %q, want %q", tt.val, tt.typ, got, tt.want)
			}
		})
	}
}

func TestEffectiveTypeAutoUUIDForAuthUsers(t *testing.T) {
	cases := []struct {
		name string
		f    domain.Field
		want string
	}{
		{"auth.users.id → UUID", domain.Field{ForeignKey: &domain.ForeignKey{References: "auth.users.id"}}, "UUID"},
		{"posts.id → BIGINT", domain.Field{ForeignKey: &domain.ForeignKey{References: "posts.id"}}, "BIGINT"},
		{"explicit type wins", domain.Field{Type: "TEXT", ForeignKey: &domain.ForeignKey{References: "auth.users.id"}}, "TEXT"},
		{"legacy users.id no longer auto-uuids", domain.Field{ForeignKey: &domain.ForeignKey{References: "users.id"}}, "BIGINT"},
	}
	for _, tt := range cases {
		got := effectiveType(tt.f, nil)
		if got != tt.want {
			t.Errorf("%s: got %q want %q", tt.name, got, tt.want)
		}
	}
}

func TestGenerateTable_UntypedFKInheritsUUID(t *testing.T) {
	tables := map[string]domain.Table{
		"school_accounts": {Fields: []domain.Field{{Name: "id", Type: "uuid", PrimaryKey: true}}},
		"schedule_workspaces": {Fields: []domain.Field{
			{Name: "id", Type: "uuid", PrimaryKey: true},
			{Name: "school_id", Required: true, ForeignKey: &domain.ForeignKey{References: "school_accounts.id", OnDelete: "cascade"}},
		}},
	}
	ddl := strings.Join(generateTable("schedule_workspaces", tables["schedule_workspaces"], tables), "\n")
	if !strings.Contains(ddl, "school_id uuid") {
		t.Fatalf("expected school_id uuid, got:\n%s", ddl)
	}
}

func TestGenerateTable_UntypedFKToBigserialStaysBigint(t *testing.T) {
	// Parity: gearstore-shaped config (bigserial PK, untyped FK) must still emit BIGINT.
	tables := map[string]domain.Table{
		"categories": {Fields: []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}}},
		"products": {Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "category_id", ForeignKey: &domain.ForeignKey{References: "categories.id"}},
		}},
	}
	ddl := strings.Join(generateTable("products", tables["products"], tables), "\n")
	if !strings.Contains(ddl, "category_id BIGINT") {
		t.Fatalf("expected category_id BIGINT (parity), got:\n%s", ddl)
	}
	if strings.Contains(strings.ToLower(ddl), "category_id bigserial") {
		t.Fatalf("FK column must not be sequence-backed bigserial:\n%s", ddl)
	}
}

func mustContain(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("expected output to contain %q, got:\n%s", substr, s)
	}
}

func mustNotContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Fatalf("expected DDL to NOT contain %q, but it did:\n%s", needle, haystack)
	}
}

func TestGenerateAuthFlowState(t *testing.T) {
	ddl := strings.Join(generateAuthTables(&domain.Auth{}), "\n")
	mustContain(t, ddl, "CREATE TABLE IF NOT EXISTS auth.flow_state")
	mustContain(t, ddl, "auth_code TEXT")
	mustContain(t, ddl, "code_challenge TEXT")
	mustContain(t, ddl, "code_challenge_method TEXT")
	mustContain(t, ddl, "provider_type TEXT NOT NULL")
	mustContain(t, ddl, "provider_access_token TEXT")
	mustContain(t, ddl, "provider_refresh_token TEXT")
	mustContain(t, ddl, "authentication_method TEXT")
	mustContain(t, ddl, "redirect_to TEXT")
	mustContain(t, ddl, "linking_user_id TEXT")
	mustContain(t, ddl, "auth_code_issued_at TIMESTAMPTZ")
	mustContain(t, ddl, "CREATE UNIQUE INDEX IF NOT EXISTS idx_flow_state_auth_code ON auth.flow_state (auth_code) WHERE auth_code IS NOT NULL")
	mustContain(t, ddl, "CREATE INDEX IF NOT EXISTS idx_flow_state_user_id_auth_method ON auth.flow_state (user_id, authentication_method)")

	// And the old tables must NOT be emitted.
	mustNotContain(t, ddl, "_oauth_states")
	mustNotContain(t, ddl, "_auth_codes")
}

// TestGenerateRPCFunction_Signature verifies the DDL shape for a function
// with ordered args, a default, a non-default language and security
// clause, and a scalar return. The full statement is compared so any
// drift in quoting, spacing, or clause order surfaces immediately.
func TestGenerateRPCFunction_Signature(t *testing.T) {
	fn := domain.Function{
		Language:   "sql",
		Volatility: "stable",
		Security:   "definer",
		Returns:    domain.FuncReturn{Type: "int"},
		Body:       "SELECT $1 + $2;",
		Args: []domain.FuncArg{
			{Name: "a", Type: "int"},
			{Name: "b", Type: "int", Default: 10},
		},
	}
	ddl := generateRPCFunction("add", fn)

	mustContain(t, ddl, `CREATE OR REPLACE FUNCTION public."add"`)
	mustContain(t, ddl, `"a" int`)
	mustContain(t, ddl, `"b" int DEFAULT 10`)
	mustContain(t, ddl, "RETURNS int")
	mustContain(t, ddl, "LANGUAGE sql")
	mustContain(t, ddl, "STABLE")
	mustContain(t, ddl, "SECURITY DEFINER")
	mustContain(t, ddl, "AS $ub$SELECT $1 + $2;$ub$")
}

// TestGenerateRPCFunction_VoidDefaults: a minimal function should pick
// up plpgsql/volatile/invoker defaults applied by the config loader,
// so we pre-populate them here and assert the invoker clause lands.
func TestGenerateRPCFunction_VoidDefaults(t *testing.T) {
	fn := domain.Function{
		Language:   "plpgsql",
		Volatility: "volatile",
		Security:   "invoker",
		Returns:    domain.FuncReturn{Type: "void"},
		Body:       "BEGIN END;",
	}
	ddl := generateRPCFunction("noop", fn)
	mustContain(t, ddl, "LANGUAGE plpgsql")
	mustContain(t, ddl, "VOLATILE")
	mustContain(t, ddl, "SECURITY INVOKER")
	mustContain(t, ddl, "RETURNS void")
}

// --- Fake DB for Apply tests ---
//
// fakeDB is a minimal in-memory stand-in for domain.Database used to exercise
// the Migrator.Apply transaction path. Only the methods Apply touches are
// implemented with real behavior; everything else returns zero values.
//
// failOnStatementContaining: when set, fakeTx.Exec returns an error for any
// statement whose text contains the substring, simulating a mid-migration
// failure inside the transaction.
//
// committedStatements: the running total of statements that have been
// committed across all transactions. fakeTx.Commit adds its per-tx counter
// here; Rollback discards it.
//
// committedStatementsAfterFirst: snapshotted by tests after a first
// successful Apply so subsequent assertions can measure only the delta added
// by a later (failing) Apply.
type fakeDB struct {
	migrationsTableEnsured        bool
	lastMigration                 *domain.Migration
	failOnStatementContaining     string
	committedStatements           int
	committedStatementsAfterFirst int
	execs                         []string
}

func newFakeDB(t *testing.T) *fakeDB {
	t.Helper()
	return &fakeDB{}
}

func (f *fakeDB) Close() error                   { return nil }
func (f *fakeDB) Ping(ctx context.Context) error { return nil }
func (f *fakeDB) EnsureMigrationsTable(ctx context.Context) error {
	f.migrationsTableEnsured = true
	return nil
}
func (f *fakeDB) GetLastMigration(ctx context.Context) (*domain.Migration, error) {
	return f.lastMigration, nil
}
func (f *fakeDB) ExecDDL(ctx context.Context, sql string) error {
	// Apply no longer calls ExecDDL after the tx refactor; if it does, that's
	// a regression worth surfacing.
	return fmt.Errorf("fakeDB.ExecDDL should not be called; Apply must use Begin/Commit")
}
func (f *fakeDB) Query(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	return nil, nil
}
func (f *fakeDB) QueryRow(ctx context.Context, query string, args ...any) (map[string]any, error) {
	return nil, nil
}
func (f *fakeDB) Exec(ctx context.Context, query string, args ...any) (int64, error) {
	f.execs = append(f.execs, query)
	return 0, nil
}
func (f *fakeDB) WithRLS(ctx context.Context, session domain.Session) (context.Context, error) {
	return ctx, nil
}
func (f *fakeDB) Begin(ctx context.Context) (domain.Tx, error) {
	return &fakeTx{db: f}, nil
}

// fakeTx tracks statements executed inside a single transaction. They only
// roll up into fakeDB.committedStatements when Commit is called; Rollback
// drops the per-tx counter on the floor, mirroring real transactional
// semantics.
type fakeTx struct {
	db            *fakeDB
	pending       int
	finished      bool
	pendingRecord *domain.Migration // the history row, promoted to db.lastMigration on Commit
}

func (tx *fakeTx) Query(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	return nil, nil
}
func (tx *fakeTx) QueryRow(ctx context.Context, query string, args ...any) (map[string]any, error) {
	if last := tx.db.lastMigration; last != nil && strings.Contains(query, "_instancez_migrations") {
		return map[string]any{"checksum": last.Checksum, "config_json": last.ConfigJSON}, nil
	}
	return nil, nil
}
func (tx *fakeTx) Exec(ctx context.Context, query string, args ...any) (int64, error) {
	if tx.finished {
		return 0, fmt.Errorf("fakeTx: Exec after finish")
	}
	if tx.db.failOnStatementContaining != "" && strings.Contains(query, tx.db.failOnStatementContaining) {
		return 0, fmt.Errorf("fakeTx: simulated failure on statement containing %q", tx.db.failOnStatementContaining)
	}
	// Apply records the migration with an INSERT inside the tx; stash it so
	// Commit can publish it (and Rollback can drop it), mirroring atomicity.
	if strings.Contains(query, "_instancez_migrations") && len(args) == 3 {
		tx.pendingRecord = &domain.Migration{
			Checksum:   args[0].(string),
			SQL:        args[1].(string),
			ConfigJSON: args[2].(string),
			AppliedAt:  time.Now(),
		}
	}
	tx.db.execs = append(tx.db.execs, query)
	tx.pending++
	return 0, nil
}
func (tx *fakeTx) Commit(ctx context.Context) error {
	if tx.finished {
		return nil
	}
	tx.finished = true
	tx.db.committedStatements += tx.pending
	tx.pending = 0
	if tx.pendingRecord != nil {
		tx.db.lastMigration = tx.pendingRecord
	}
	return nil
}
func (tx *fakeTx) Rollback(ctx context.Context) error {
	if tx.finished {
		return nil
	}
	tx.finished = true
	tx.pending = 0
	return nil
}

func TestApplyRollsBackOnFailure(t *testing.T) {
	db := newFakeDB(t)
	m := NewMigrator(db)

	// First config applies cleanly: one table.
	cfg1 := &domain.Config{
		Tables: map[string]domain.Table{
			"a": {Fields: []domain.Field{{Name: "id", Type: "BIGINT", PrimaryKey: true}}},
		},
	}
	if err := m.Apply(context.Background(), cfg1); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	db.committedStatementsAfterFirst = db.committedStatements

	// Second config: add table "b" with a column type the fake DB rejects on
	// the second statement to simulate a mid-migration failure.
	db.failOnStatementContaining = "CREATE TABLE IF NOT EXISTS b"
	cfg2 := &domain.Config{
		Tables: map[string]domain.Table{
			"a": cfg1.Tables["a"],
			"b": {Fields: []domain.Field{{Name: "id", Type: "BIGINT", PrimaryKey: true}}},
		},
	}
	err := m.Apply(context.Background(), cfg2)
	if err == nil {
		t.Fatalf("expected migration to fail")
	}

	// Critical: nothing from the failing migration should have committed.
	if db.committedStatements != db.committedStatementsAfterFirst {
		t.Fatalf("expected rollback, but %d new statements committed",
			db.committedStatements-db.committedStatementsAfterFirst)
	}
	// And the migration history must NOT have been updated.
	last, _ := db.GetLastMigration(context.Background())
	if last == nil || last.ConfigJSON == "" {
		t.Fatalf("history wiped; expected first migration to survive")
	}
	var lastCfg domain.Config
	if err := json.Unmarshal([]byte(last.ConfigJSON), &lastCfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, hasB := lastCfg.Tables["b"]; hasB {
		t.Fatalf("history shows b table; should have rolled back")
	}
}

func TestQualifiedTableName(t *testing.T) {
	cases := []struct {
		name  string
		table domain.Table
		want  string
	}{
		{"public default", domain.Table{}, "posts"},
		{"explicit public", domain.Table{Schema: "public"}, "posts"},
		{"non-default", domain.Table{Schema: "analytics"}, "analytics.posts"},
	}
	for _, tt := range cases {
		got := qualifiedTableName("posts", tt.table)
		if got != tt.want {
			t.Errorf("%s: got %q want %q", tt.name, got, tt.want)
		}
	}
}

func TestRLSAndIndexesUseQualifiedNames(t *testing.T) {
	tbl := domain.Table{
		Schema:  "analytics",
		Fields:  []domain.Field{{Name: "id", Type: "BIGINT", PrimaryKey: true}},
		Indexes: []domain.Index{{Columns: []string{"id"}}},
		RLS: []domain.RLSPolicy{
			{Operations: []string{"select"}, Using: "true"},
		},
	}
	idx := strings.Join(generateIndexes("posts", tbl), "\n")
	if !strings.Contains(idx, "ON analytics.posts (") {
		t.Errorf("expected schema-qualified index DDL, got: %s", idx)
	}
	rls := strings.Join(generateRLSPolicies("posts", tbl), "\n")
	if !strings.Contains(rls, "ALTER TABLE analytics.posts") {
		t.Errorf("expected schema-qualified RLS DDL, got: %s", rls)
	}
}

func TestOrderedSchemasIncludesAuthAndStorage(t *testing.T) {
	cfg := &domain.Config{
		Auth:    &domain.Auth{},
		Storage: map[string]domain.Bucket{"avatars": {}},
		Tables: map[string]domain.Table{
			"posts": {Schema: "analytics"},
		},
	}
	got := orderedSchemas(cfg)
	want := []string{"public", "auth", "storage", "analytics"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("orderedSchemas: got %v, want %v", got, want)
	}
}

func TestOrderedSchemasOmitsAuthWhenUnconfigured(t *testing.T) {
	cfg := &domain.Config{}
	got := orderedSchemas(cfg)
	if len(got) != 1 || got[0] != "public" {
		t.Errorf("orderedSchemas with no auth/storage: got %v, want [public]", got)
	}
}

func TestGenerateTable_ByteParity_UntypedIntegerFK(t *testing.T) {
	tables := map[string]domain.Table{
		"categories": {Fields: []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}}},
		"products": {Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "category_id", ForeignKey: &domain.ForeignKey{References: "categories.id"}},
		}},
	}
	got := strings.Join(generateTable("products", tables["products"], tables), "\n")
	want := `CREATE TABLE IF NOT EXISTS products (
  id bigserial PRIMARY KEY,
  category_id BIGINT,
  FOREIGN KEY (category_id) REFERENCES public.categories(id) ON DELETE RESTRICT
);`
	if got != want {
		t.Fatalf("DDL drifted:\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestGenerateTable_EnumAndPatternEscapeQuotes(t *testing.T) {
	table := domain.Table{Fields: []domain.Field{
		{Name: "id", Type: "bigserial", PrimaryKey: true},
		{Name: "owner", Type: "text", Enum: []string{"O'Brien", "", "ünï"}},
		{Name: "code", Type: "text", Pattern: `^[a-z']+\d$`},
	}}
	joined := strings.Join(generateTable("people", table, nil), "\n")
	mustContain(t, joined, "CHECK (owner IN ('O''Brien', '', 'ünï'))")
	mustContain(t, joined, `CHECK (code ~ '^[a-z'']+\d$')`)
}

func TestHarden_LocksThenRevokesInOneTx(t *testing.T) {
	for _, cfg := range []*domain.Config{nil, {}, {Auth: &domain.Auth{}}} {
		db := newFakeDB(t)
		if err := NewMigrator(db).Harden(context.Background(), cfg); err != nil {
			t.Fatalf("Harden(%+v): %v", cfg, err)
		}
		revoke := slices.IndexFunc(db.execs, func(q string) bool { return strings.Contains(q, "REVOKE ALL ON ALL TABLES IN SCHEMA auth") })
		if revoke < 3 || !slices.ContainsFunc(db.execs[:revoke], func(q string) bool { return strings.Contains(q, "pg_advisory_xact_lock") }) ||
			strings.Contains(strings.Join(db.execs[revoke-3:revoke], ""), "DO $$") {
			t.Fatalf("advisory lock must open the revoke tx: %v", db.execs)
		}
		if !db.migrationsTableEnsured {
			t.Fatal("Harden must ensure _instancez_migrations exists before revoking on it")
		}
		joined := strings.Join(db.execs, "\n")
		mustContain(t, joined, "CREATE TABLE IF NOT EXISTS auth.jwt_keys")
		mustContain(t, joined, "REVOKE ALL ON ALL TABLES IN SCHEMA auth FROM anon, authenticated;")
		mustContain(t, joined, "REVOKE ALL ON _instancez_migrations FROM anon, authenticated, service_role;")
		if db.committedStatements != len(db.execs) {
			t.Fatalf("committed %d of %d statements", db.committedStatements, len(db.execs))
		}
	}
}

func TestHarden_RevokesFromCustomRoles(t *testing.T) {
	db := newFakeDB(t)
	roles := domain.Roles{Anon: "web_anon", Authenticated: "web_user", Service: "web_admin", Seed: "seeder"}
	if err := NewMigrator(db, roles).Harden(context.Background(), nil); err != nil {
		t.Fatalf("Harden: %v", err)
	}
	joined := strings.Join(db.execs, "\n")
	mustContain(t, joined, "REVOKE ALL ON auth.jwt_keys FROM web_anon, web_user, web_admin;")
	mustContain(t, joined, "REVOKE ALL ON _instancez_migrations FROM web_anon, web_user, web_admin, seeder;")
}

func TestHarden_RollsBackOnFailure(t *testing.T) {
	db := newFakeDB(t)
	db.failOnStatementContaining = "REVOKE ALL ON auth.jwt_keys"
	if err := NewMigrator(db).Harden(context.Background(), nil); err == nil {
		t.Fatal("expected error")
	}
	if db.committedStatements != 0 {
		t.Fatalf("partial harden committed %d statements, want none", db.committedStatements)
	}
}

func TestAuthHealDDL_EmittedOnFreshAndDiff(t *testing.T) {
	auth := &domain.Auth{Email: &domain.AuthEmail{}}
	fresh := strings.Join(generateAuthTables(auth), "\n")
	diff := strings.Join(diffNewAuth(&domain.Config{Auth: auth}, &domain.Config{Auth: auth}), "\n")
	if len(authHealDDL) == 0 {
		t.Fatal("authHealDDL is empty")
	}
	for _, stmt := range authHealDDL {
		mustContain(t, fresh, stmt)
		mustContain(t, diff, stmt)
	}
}

func TestAuthHealDDL_ChecksCatalogBeforeTouchingTables(t *testing.T) {
	joined := strings.Join(authHealDDL, "\n")
	for _, want := range []string{"refresh_tokens", "revoked_at", "aal", "amr", "idx_refresh_tokens_session",
		"mfa_factors", "last_totp_step", "mfa_challenges", "idx_mfa_challenges_factor_created", "one_time_tokens", "idx_users_email_lower", "lower(email)"} {
		mustContain(t, joined, want)
	}
	for _, stmt := range authHealDDL {
		if !strings.HasPrefix(stmt, "DO $$") || !strings.Contains(stmt, "to_regclass('auth.") {
			t.Errorf("heal stmt must skip missing tables and existing objects: %s", stmt)
		}
		if strings.Contains(stmt, "IF NOT EXISTS") {
			t.Errorf("IF NOT EXISTS takes the table lock before checking; use the catalog guard: %s", stmt)
		}
	}
}

// Index builds must not run while the refresh_tokens heal holds its table lock.
func TestAuthHealDDL_IndexesBeforeRefreshTokenAlters(t *testing.T) {
	firstAlter := slices.IndexFunc(authHealDDL, func(s string) bool { return strings.Contains(s, "ALTER TABLE auth.refresh_tokens") })
	if firstAlter < 0 {
		t.Fatal("no refresh_tokens ALTER in authHealDDL")
	}
	for _, idx := range []string{"idx_users_email_lower", "idx_mfa_challenges_factor_created"} {
		i := slices.IndexFunc(authHealDDL, func(s string) bool { return strings.Contains(s, idx) })
		if i < 0 || i > firstAlter {
			t.Errorf("%s at %d, want before the first refresh_tokens ALTER at %d", idx, i, firstAlter)
		}
	}
}

func TestDiffNewAuth_OneTimeTokensHasAttempts(t *testing.T) {
	old := &domain.Config{Auth: &domain.Auth{}}
	nw := &domain.Config{Auth: &domain.Auth{Email: &domain.AuthEmail{}}}
	joined := strings.Join(diffNewAuth(old, nw), "\n")
	mustContain(t, joined, "code TEXT,\n  attempts INT NOT NULL DEFAULT 0,")
}

func TestHarden_HealsAuthColumnsOnlyWhenAuthConfigured(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  *domain.Config
		heal bool
	}{{"nil", nil, false}, {"no auth", &domain.Config{}, false}, {"auth", &domain.Config{Auth: &domain.Auth{}}, true}} {
		db := newFakeDB(t)
		if err := NewMigrator(db).Harden(context.Background(), tc.cfg); err != nil {
			t.Fatalf("%s: Harden: %v", tc.name, err)
		}
		joined := strings.Join(db.execs, "\n")
		for _, stmt := range authHealDDL {
			if got := strings.Contains(joined, stmt); got != tc.heal {
				t.Errorf("%s: heal stmt present=%v, want %v: %s", tc.name, got, tc.heal, stmt)
			}
		}
		if db.committedStatements != len(db.execs) {
			t.Fatalf("committed %d of %d statements", db.committedStatements, len(db.execs))
		}
	}
}

// T6 heal: Harden must re-emit a bucket's storage RLS when it has a restrictive policy, so an existing DB upgrades off the old AND-scoping without a config change.
func TestHarden_RestrictiveBucketPolicy_ReemitsStorageRLS(t *testing.T) {
	cfg := &domain.Config{Storage: map[string]domain.Bucket{
		"secrets": {RLS: []domain.RLSPolicy{
			{Operations: []string{"select"}, Using: "name LIKE 'ok/%'", Type: "restrictive"},
		}},
	}}
	db := dbAppliedWith(t, cfg)
	if err := NewMigrator(db).Harden(context.Background(), &domain.Config{}); err != nil {
		t.Fatalf("Harden: %v", err)
	}
	joined := strings.Join(db.execs, "\n")
	mustContain(t, joined, "to_regclass('storage.objects') IS NOT NULL")
	mustContain(t, joined, "DROP POLICY IF EXISTS storage_secrets_select_0 ON storage.objects;")
	mustContain(t, joined, "bucket_id <> 'secrets' OR (name LIKE 'ok/%')")
}

// No restrictive bucket policies: Harden only touches storage.objects behind a pg_policies check, taking no lock on it.
func TestHarden_NoRestrictiveBucketPolicy_TakesNoStorageLock(t *testing.T) {
	cfg := &domain.Config{Storage: map[string]domain.Bucket{
		"avatars": {Public: true},
		"docs": {RLS: []domain.RLSPolicy{
			{Operations: []string{"select"}, Using: "auth.uid() IS NOT NULL"},
		}},
	}}
	db := dbAppliedWith(t, cfg)
	if err := NewMigrator(db).Harden(context.Background(), cfg); err != nil {
		t.Fatalf("Harden: %v", err)
	}
	for _, stmt := range db.execs {
		if strings.Contains(stmt, "storage.objects") && !strings.Contains(stmt, "FROM pg_policies") && !strings.Contains(stmt, "FROM pg_indexes") {
			t.Fatalf("Harden with no restrictive bucket policy must only touch storage.objects behind a pg_policies check, got: %s", stmt)
		}
	}
}

func dbAppliedWith(t *testing.T, cfg *domain.Config) *fakeDB {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	db := newFakeDB(t)
	db.lastMigration = &domain.Migration{ConfigJSON: string(raw)}
	return db
}

// Storage heals come from the applied config; a pending one can reference objects that don't exist yet.
func TestHarden_StorageHealUsesAppliedConfigOnly(t *testing.T) {
	pending := &domain.Config{Storage: map[string]domain.Bucket{
		"new": {RLS: []domain.RLSPolicy{{Operations: []string{"select"}, Using: "missing_fn()", Type: "restrictive"}}},
	}}
	for name, db := range map[string]*fakeDB{
		"no migration":  newFakeDB(t),
		"empty config":  {lastMigration: &domain.Migration{ConfigJSON: "{}"}},
		"unparseable":   {lastMigration: &domain.Migration{ConfigJSON: "not json"}},
		"other buckets": dbAppliedWith(t, &domain.Config{Storage: map[string]domain.Bucket{"old": {}}}),
	} {
		if err := NewMigrator(db).Harden(context.Background(), pending); err != nil {
			t.Fatalf("%s: Harden: %v", name, err)
		}
		joined := strings.Join(db.execs, "\n")
		mustNotContain(t, joined, "missing_fn")
		mustNotContain(t, joined, "new_public_select")
	}
}

func TestGenerateRLSPolicies_EnabledWithZeroPoliciesIsDenyAll(t *testing.T) {
	on := true
	ddl := generateRLSPolicies("todos", domain.Table{RLSEnabled: &on})
	want := []string{
		"ALTER TABLE todos ENABLE ROW LEVEL SECURITY;",
		"ALTER TABLE todos FORCE ROW LEVEL SECURITY;",
	}
	if !slices.Equal(ddl, want) {
		t.Fatalf("got %q, want %q", ddl, want)
	}
}

func TestGenerateRLSPolicies_ExplicitFalseEmitsNothing(t *testing.T) {
	off := false
	if ddl := generateRLSPolicies("todos", domain.Table{RLSEnabled: &off}); len(ddl) != 0 {
		t.Fatalf("rls_enabled: false must emit no RLS DDL, got %q", ddl)
	}
}

func TestGenerateRLSPolicies_EnabledSchemaQualified(t *testing.T) {
	on := true
	joined := strings.Join(generateRLSPolicies("notes", domain.Table{Schema: "reporting", RLSEnabled: &on}), "\n")
	mustContain(t, joined, "ALTER TABLE reporting.notes ENABLE ROW LEVEL SECURITY;")
	mustContain(t, joined, "ALTER TABLE reporting.notes FORCE ROW LEVEL SECURITY;")
}

func TestHarden_CorruptAppliedConfigWarns(t *testing.T) {
	var logs bytes.Buffer
	m := NewMigrator(&fakeDB{lastMigration: &domain.Migration{ConfigJSON: "not json"}})
	m.logger = slog.New(slog.NewTextHandler(&logs, nil))
	if err := m.Harden(context.Background(), nil); err != nil {
		t.Fatalf("Harden: %v", err)
	}
	mustContain(t, logs.String(), "level=WARN")
	mustContain(t, logs.String(), "invalid character")
}

type gateDB struct {
	*fakeDB
	row map[string]any
}

func (g gateDB) QueryRow(ctx context.Context, query string, args ...any) (map[string]any, error) {
	return g.row, nil
}

func healExecs(db *fakeDB) (n int, joined string) {
	for _, q := range db.execs {
		if strings.Contains(q, "objects_bucket_name") {
			n++
		}
	}
	return n, strings.Join(db.execs, "\n")
}

func TestHarden_StorageListHealGate(t *testing.T) {
	const limit = 1 << 20
	cases := []struct {
		name      string
		row       map[string]any
		wantSteps int
		wantWarn  bool
	}{
		{"nothing missing", map[string]any{"need_name_index": false, "need_column": false, "bytes": int64(limit + 1)}, 0, false},
		{"no storage table", nil, 0, false},
		{"both missing under the gate", map[string]any{"need_name_index": true, "need_column": true, "bytes": int64(limit)}, 2, false},
		{"only the column missing", map[string]any{"need_name_index": false, "need_column": true, "bytes": int64(0)}, 1, false},
		{"oversize skips the index too", map[string]any{"need_name_index": true, "need_column": true, "bytes": int64(limit + 1)}, 0, true},
		{"oversize with only the index missing", map[string]any{"need_name_index": true, "need_column": false, "bytes": int64(limit + 1)}, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			fake := newFakeDB(t)
			m := NewMigrator(gateDB{fake, tc.row}).StorageHealLimits(3*time.Second, limit)
			m.logger = slog.New(slog.NewTextHandler(&logs, nil))
			if err := m.Harden(context.Background(), nil); err != nil {
				t.Fatal(err)
			}
			steps, joined := healExecs(fake)
			if steps != tc.wantSteps {
				t.Fatalf("ran %d heal steps, want %d", steps, tc.wantSteps)
			}
			if tc.wantSteps > 0 {
				mustContain(t, joined, "statement_timeout")
			}
			if got := strings.Contains(logs.String(), "CREATE INDEX CONCURRENTLY"); got != tc.wantWarn {
				t.Fatalf("manual SQL in warning = %v, want %v: %s", got, tc.wantWarn, logs.String())
			}
		})
	}
}

// Heal DDL leaks into the fresh plan, ProvisionIdempotent and diffNewStorage, so none of them may alter or index a table that has rows.
func TestGenerateStorageTables_HasNoUnboundedHeal(t *testing.T) {
	stmts := generateStorageTables(&domain.Config{Storage: map[string]domain.Bucket{"b": {}}})
	joined := strings.Join(stmts, "\n")
	mustContain(t, joined, `name_lower TEXT COLLATE "C" GENERATED ALWAYS AS (lower(name)) STORED,`)
	mustNotContain(t, joined, "ADD COLUMN name_lower")
	for _, stmt := range stmts {
		if strings.Contains(stmt, "CREATE INDEX") && !strings.Contains(stmt, "IF NOT EXISTS (SELECT 1 FROM storage.objects LIMIT 1)") {
			t.Errorf("index build must be guarded by an empty-table check: %s", stmt)
		}
	}
	mustContain(t, strings.Join(diffNewStorage(&domain.Config{}, &domain.Config{Storage: map[string]domain.Bucket{"b": {}}}), "\n"), "name_lower")
}

func TestStorageHealDDL_TreatsInvalidIndexAsMissing(t *testing.T) {
	for _, ddl := range []string{storageHealGate, storageNameIndexHeal, storageListColumnHeal, storageIndexesWhenEmpty} {
		mustContain(t, ddl, "indisvalid")
	}
	mustContain(t, storageNameIndexHeal, "DROP INDEX IF EXISTS storage.objects_bucket_name_c_idx;")
	mustNotContain(t, storageNameIndexHeal+storageListColumnHeal, "CASCADE")
}

// budgetTx makes every heal tx (the ones that set statement_timeout) lose its lock wait after a pause.
type budgetTx struct {
	domain.Tx
	rec     *[]string
	healing bool
}

func (b *budgetTx) Exec(ctx context.Context, query string, args ...any) (int64, error) {
	if strings.Contains(query, "set_config('statement_timeout'") {
		b.healing = true
	}
	if strings.Contains(query, "set_config('lock_timeout'") && b.healing {
		*b.rec = append(*b.rec, args[0].(string))
	}
	if b.healing && strings.Contains(query, "pg_advisory_xact_lock") {
		time.Sleep(60 * time.Millisecond)
		return 0, errors.New("lock timeout")
	}
	return b.Tx.Exec(ctx, query, args...)
}

type budgetDB struct {
	gateDB
	rec *[]string
}

func (b budgetDB) Begin(ctx context.Context) (domain.Tx, error) {
	tx, err := b.gateDB.Begin(ctx)
	return &budgetTx{Tx: tx, rec: b.rec}, err
}

// Both heal txs share one lock-wait budget, so a boot never waits it twice.
func TestHarden_StorageListHealStepsShareTheLockBudget(t *testing.T) {
	var rec []string
	row := map[string]any{"need_name_index": true, "need_column": true, "bytes": int64(0)}
	db := budgetDB{gateDB{newFakeDB(t), row}, &rec}
	if err := NewMigrator(db).LockTimeout(500 * time.Millisecond).Harden(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if len(rec) != 2 || rec[0] != "500ms" {
		t.Fatalf("lock_timeout per heal tx = %v, want the full budget first", rec)
	}
	if got, err := strconv.Atoi(strings.TrimSuffix(rec[1], "ms")); err != nil || got > 440 || got < 1 {
		t.Fatalf("second heal tx must only get what the first left (<=440ms), got %q", rec[1])
	}
}
