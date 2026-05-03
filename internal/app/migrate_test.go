package app

import (
	"strings"
	"testing"

	"github.com/saedx1/ultrabase/internal/domain"
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
	mustContain(t, joined, "REPLICA IDENTITY FULL")
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

	// FK referencing users.id now infers UUID to match the GoTrue schema.
	mustContain(t, joined, "user_id UUID")
	mustContain(t, joined, "FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE")
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

func TestGenerateUsersTable(t *testing.T) {
	auth := &domain.Auth{
		RefreshTokens: true,
		Email:         &domain.AuthEmail{VerifyEmail: true},
	}
	extraFields := []domain.Field{
		{Name: "display_name", Type: "text", Required: true},
	}
	ddl := generateUsersTable(auth, extraFields)
	joined := strings.Join(ddl, "\n")

	mustContain(t, joined, "CREATE TABLE IF NOT EXISTS users")
	mustContain(t, joined, "email TEXT NOT NULL UNIQUE")
	mustContain(t, joined, "password_hash TEXT")
	mustContain(t, joined, "email_verified BOOLEAN")
	mustContain(t, joined, "display_name text NOT NULL")
	mustContain(t, joined, "CREATE TABLE IF NOT EXISTS _user_identities")
	mustContain(t, joined, "CREATE TABLE IF NOT EXISTS _refresh_tokens")
	mustContain(t, joined, "CREATE TABLE IF NOT EXISTS _auth_email_verifications")
}

func TestGenerateUsersTable_NoRefreshTokens(t *testing.T) {
	auth := &domain.Auth{
		RefreshTokens: false,
	}
	ddl := generateUsersTable(auth, nil)
	joined := strings.Join(ddl, "\n")

	if strings.Contains(joined, "_refresh_tokens") {
		t.Error("should not create _refresh_tokens when refresh_tokens is false")
	}
	if strings.Contains(joined, "_auth_email_verifications") {
		t.Error("should not create _auth_email_verifications when email is nil")
	}
}

func TestGenerateRLSPolicies(t *testing.T) {
	table := domain.Table{
		RLS: []domain.RLSPolicy{
			{Operations: []string{"select"}, Check: "user_id = auth.uid()"},
			{Operations: []string{"insert"}, Check: "auth.is_authenticated()"},
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

func TestGenerateRLSPolicies_NoRLS(t *testing.T) {
	table := domain.Table{}
	ddl := generateRLSPolicies("todos", table)
	if len(ddl) != 0 {
		t.Errorf("expected no DDL for table without RLS, got %d statements", len(ddl))
	}
}

func TestGenerateSearch(t *testing.T) {
	table := domain.Table{
		Searchable:   []string{"title", "body"},
		SearchConfig: "english",
	}
	ddl := generateSearch("todos", table)
	joined := strings.Join(ddl, "\n")

	mustContain(t, joined, "ADD COLUMN IF NOT EXISTS _tsv TSVECTOR")
	mustContain(t, joined, "to_tsvector('english', COALESCE(title, ''))")
	mustContain(t, joined, "to_tsvector('english', COALESCE(body, ''))")
	mustContain(t, joined, "USING GIN")
}

func TestGenerateObjectsTable(t *testing.T) {
	ddl := generateObjectsTable()
	joined := strings.Join(ddl, "\n")

	mustContain(t, joined, "CREATE TABLE IF NOT EXISTS _objects")
	mustContain(t, joined, "bucket_id TEXT NOT NULL")
	mustContain(t, joined, "uploaded_by UUID REFERENCES users(id)")
}

func TestGenerateEventsTable(t *testing.T) {
	ddl := generateEventsTable()
	joined := strings.Join(ddl, "\n")

	mustContain(t, joined, "CREATE TABLE IF NOT EXISTS _events")
	mustContain(t, joined, "event_name TEXT NOT NULL")
	mustContain(t, joined, "status TEXT NOT NULL DEFAULT 'pending'")
}

func TestGenerateStorageRLS_Public(t *testing.T) {
	bucket := domain.Bucket{
		Public: true,
		RLS: []domain.RLSPolicy{
			{Operations: []string{"insert"}, Check: "uploaded_by = auth.uid()"},
		},
	}
	ddl := generateStorageRLS("avatars", bucket)
	joined := strings.Join(ddl, "\n")

	mustContain(t, joined, "DROP POLICY IF EXISTS avatars_public_select ON _objects")
	mustContain(t, joined, "avatars_public_select")
	mustContain(t, joined, "bucket_id = 'avatars'")
	mustContain(t, joined, "DROP POLICY IF EXISTS storage_avatars_insert_0 ON _objects")
	mustContain(t, joined, "FOR INSERT WITH CHECK")
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

func mustContain(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("expected output to contain %q, got:\n%s", substr, s)
	}
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
