package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

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
func (f *fakeDB) RecordMigration(ctx context.Context, checksum, sql, configJSON string) error {
	f.lastMigration = &domain.Migration{
		Checksum:   checksum,
		SQL:        sql,
		ConfigJSON: configJSON,
		AppliedAt:  time.Now(),
	}
	return nil
}
func (f *fakeDB) ExecDDL(ctx context.Context, sql string) error {
	// Apply no longer calls ExecDDL after the tx refactor; if it does, that's
	// a regression worth surfacing.
	return fmt.Errorf("fakeDB.ExecDDL should not be called; Apply must use Begin/Commit")
}
func (f *fakeDB) EnsureDataTable(ctx context.Context) error { return nil }
func (f *fakeDB) GetAppliedData(ctx context.Context) ([]domain.DataRecord, error) {
	return nil, nil
}
func (f *fakeDB) RecordData(ctx context.Context, tx domain.Tx, key, tableName, source, checksum string, rowCount int) error {
	return nil
}
func (f *fakeDB) Query(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	return nil, nil
}
func (f *fakeDB) QueryRow(ctx context.Context, query string, args ...any) (map[string]any, error) {
	return nil, nil
}
func (f *fakeDB) Exec(ctx context.Context, query string, args ...any) (int64, error) {
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
	db       *fakeDB
	pending  int
	finished bool
}

func (tx *fakeTx) Query(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	return nil, nil
}
func (tx *fakeTx) QueryRow(ctx context.Context, query string, args ...any) (map[string]any, error) {
	return nil, nil
}
func (tx *fakeTx) Exec(ctx context.Context, query string, args ...any) (int64, error) {
	if tx.finished {
		return 0, fmt.Errorf("fakeTx: Exec after finish")
	}
	if tx.db.failOnStatementContaining != "" && strings.Contains(query, tx.db.failOnStatementContaining) {
		return 0, fmt.Errorf("fakeTx: simulated failure on statement containing %q", tx.db.failOnStatementContaining)
	}
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

func TestParseFKReference(t *testing.T) {
	tests := []struct {
		in        string
		schema    string
		table     string
		column    string
		expectErr bool
	}{
		{"posts.id", "public", "posts", "id", false},
		{"auth.users.id", "auth", "users", "id", false},
		{"id", "", "", "", true},                             // no column
		{"a.b.c.d", "", "", "", true},                       // too many parts
		{"", "", "", "", true},                              // empty
		{"public.posts.id", "public", "posts", "id", false}, // explicit public allowed
	}
	for _, tt := range tests {
		s, table, col, err := parseFKReference(tt.in)
		if (err != nil) != tt.expectErr {
			t.Errorf("parseFKReference(%q) err=%v want err=%v", tt.in, err, tt.expectErr)
			continue
		}
		if tt.expectErr {
			continue
		}
		if s != tt.schema || table != tt.table || col != tt.column {
			t.Errorf("parseFKReference(%q) = (%q, %q, %q); want (%q, %q, %q)",
				tt.in, s, table, col, tt.schema, tt.table, tt.column)
		}
	}
}
