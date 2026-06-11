package app

import (
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

func TestDiffConfigs_NilOld(t *testing.T) {
	newCfg := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{{Name: "id", Type: "bigserial"}}},
		},
	}
	diff := diffConfigs(nil, newCfg)
	if len(diff.Removals) != 0 {
		t.Errorf("expected no removals for nil old config, got %d", len(diff.Removals))
	}
}

func TestDiffConfigs_RemovedTable(t *testing.T) {
	old := &domain.Config{
		Tables: map[string]domain.Table{
			"todos":    {Fields: []domain.Field{{Name: "id", Type: "bigserial"}}},
			"comments": {Fields: []domain.Field{{Name: "id", Type: "bigserial"}}},
		},
	}
	new := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{{Name: "id", Type: "bigserial"}}},
		},
	}

	diff := diffConfigs(old, new)
	joined := strings.Join(diff.Removals, "\n")
	mustContain(t, joined, "DROP TABLE IF EXISTS comments CASCADE")
}

func TestDiffConfigs_RemovedColumn(t *testing.T) {
	old := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial"},
				{Name: "title", Type: "text"},
				{Name: "status", Type: "text"},
			}},
		},
	}
	new := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial"},
				{Name: "title", Type: "text"},
			}},
		},
	}

	diff := diffConfigs(old, new)
	joined := strings.Join(diff.Removals, "\n")
	mustContain(t, joined, "ALTER TABLE todos DROP COLUMN IF EXISTS status")

	if strings.Contains(joined, "DROP TABLE") {
		t.Error("should not drop table when only columns are removed")
	}
}

func TestDiffConfigs_RemovedIndex(t *testing.T) {
	old := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {
				Fields: []domain.Field{{Name: "id", Type: "bigserial"}},
				Indexes: []domain.Index{
					{Columns: []string{"team_id", "status"}},
					{Columns: []string{"status"}},
				},
			},
		},
	}
	new := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {
				Fields: []domain.Field{{Name: "id", Type: "bigserial"}},
				Indexes: []domain.Index{
					{Columns: []string{"team_id", "status"}},
				},
			},
		},
	}

	diff := diffConfigs(old, new)
	joined := strings.Join(diff.Removals, "\n")
	mustContain(t, joined, "DROP INDEX IF EXISTS idx_todos_status")

	if strings.Contains(joined, "idx_todos_team_id_status") {
		t.Error("should not drop index that still exists")
	}
}

func TestDiffConfigs_RemovedRLSPolicy(t *testing.T) {
	old := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {
				Fields: []domain.Field{{Name: "id", Type: "bigserial"}},
				RLS: []domain.RLSPolicy{
					{Operations: []string{"select"}, Check: "true"},
					{Operations: []string{"insert"}, Check: "true"},
				},
			},
		},
	}
	new := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {
				Fields: []domain.Field{{Name: "id", Type: "bigserial"}},
				RLS: []domain.RLSPolicy{
					{Operations: []string{"select"}, Check: "true"},
				},
			},
		},
	}

	diff := diffConfigs(old, new)
	joined := strings.Join(diff.Removals, "\n")
	mustContain(t, joined, "DROP POLICY IF EXISTS todos_insert_1 ON todos")

	if strings.Contains(joined, "todos_select_0") {
		t.Error("should not drop policy that still exists")
	}
}

func TestDiffConfigs_AllRLSRemoved_DisablesRLS(t *testing.T) {
	old := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {
				Fields: []domain.Field{{Name: "id", Type: "bigserial"}},
				RLS: []domain.RLSPolicy{
					{Operations: []string{"select"}, Check: "true"},
				},
			},
		},
	}
	new := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {
				Fields: []domain.Field{{Name: "id", Type: "bigserial"}},
			},
		},
	}

	diff := diffConfigs(old, new)
	joined := strings.Join(diff.Removals, "\n")
	mustContain(t, joined, "DISABLE ROW LEVEL SECURITY")
}

func TestDiffConfigs_RemovedRPCFunction(t *testing.T) {
	old := &domain.Config{
		RPC: map[string]domain.Function{
			"add": {
				Returns: domain.FuncReturn{Type: "int"},
				Args: []domain.FuncArg{
					{Name: "a", Type: "int"},
					{Name: "b", Type: "int"},
				},
			},
			"noop": {Returns: domain.FuncReturn{Type: "void"}},
		},
	}
	new := &domain.Config{
		RPC: map[string]domain.Function{
			"add": {
				Returns: domain.FuncReturn{Type: "int"},
				Args: []domain.FuncArg{
					{Name: "a", Type: "int"},
					{Name: "b", Type: "int"},
				},
			},
		},
	}

	diff := diffConfigs(old, new)
	joined := strings.Join(diff.Removals, "\n")
	mustContain(t, joined, `DROP FUNCTION IF EXISTS public."noop"()`)

	if strings.Contains(joined, `"add"`) {
		t.Error("should not drop function that still exists")
	}
}

func TestDiffConfigs_RemovedStorageRLS(t *testing.T) {
	old := &domain.Config{
		Storage: map[string]domain.Bucket{
			"avatars": {
				Public: true,
				RLS: []domain.RLSPolicy{
					{Operations: []string{"insert"}, Check: "true"},
				},
			},
		},
	}
	new := &domain.Config{
		Storage: map[string]domain.Bucket{
			"avatars": {Public: false},
		},
	}

	diff := diffConfigs(old, new)
	joined := strings.Join(diff.Removals, "\n")
	mustContain(t, joined, "DROP POLICY IF EXISTS avatars_public_select ON storage.objects")
	mustContain(t, joined, "DROP POLICY IF EXISTS storage_avatars_insert_0 ON storage.objects")
}

func TestDiffConfigs_NoChanges(t *testing.T) {
	cfg := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {
				Fields:  []domain.Field{{Name: "id", Type: "bigserial"}},
				Indexes: []domain.Index{{Columns: []string{"id"}}},
				RLS:     []domain.RLSPolicy{{Operations: []string{"select"}, Check: "true"}},
			},
		},
	}

	diff := diffConfigs(cfg, cfg)
	if len(diff.Removals) != 0 {
		t.Errorf("expected no removals for identical configs, got %d: %v", len(diff.Removals), diff.Removals)
	}
}

func TestDiffConfigs_RemovedTableSkipsColumnDrops(t *testing.T) {
	old := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial"},
				{Name: "title", Type: "text"},
			}},
		},
	}
	new := &domain.Config{
		Tables: map[string]domain.Table{},
	}

	diff := diffConfigs(old, new)
	joined := strings.Join(diff.Removals, "\n")

	mustContain(t, joined, "DROP TABLE IF EXISTS todos CASCADE")
	if strings.Contains(joined, "DROP COLUMN") {
		t.Error("should not generate DROP COLUMN for a table being fully dropped")
	}
}

func TestDiffConfigs_RemovedBucket(t *testing.T) {
	old := &domain.Config{
		Storage: map[string]domain.Bucket{
			"avatars": {
				Public: true,
				RLS: []domain.RLSPolicy{
					{Operations: []string{"insert", "delete"}, Check: "true"},
				},
			},
		},
	}
	new := &domain.Config{
		Storage: map[string]domain.Bucket{},
	}

	diff := diffConfigs(old, new)
	joined := strings.Join(diff.Removals, "\n")

	mustContain(t, joined, "DROP POLICY IF EXISTS avatars_public_select ON storage.objects")
	mustContain(t, joined, "DROP POLICY IF EXISTS storage_avatars_insert_0 ON storage.objects")
	mustContain(t, joined, "DROP POLICY IF EXISTS storage_avatars_delete_0 ON storage.objects")
}

func TestDiffConfigs_RPCFunctionWithArgs_DropSignature(t *testing.T) {
	old := &domain.Config{
		RPC: map[string]domain.Function{
			"compute": {
				Returns: domain.FuncReturn{Type: "int"},
				Args: []domain.FuncArg{
					{Name: "x", Type: "int"},
					{Name: "y", Type: "text"},
				},
			},
		},
	}
	new := &domain.Config{
		RPC: map[string]domain.Function{},
	}

	diff := diffConfigs(old, new)
	joined := strings.Join(diff.Removals, "\n")

	mustContain(t, joined, `DROP FUNCTION IF EXISTS public."compute"(int, text)`)
}

func TestDiffConfigs_IndexOnDroppedTable(t *testing.T) {
	old := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {
				Fields:  []domain.Field{{Name: "id", Type: "bigserial"}},
				Indexes: []domain.Index{{Columns: []string{"id"}}},
			},
		},
	}
	new := &domain.Config{
		Tables: map[string]domain.Table{},
	}

	diff := diffConfigs(old, new)
	joined := strings.Join(diff.Removals, "\n")

	// Index DROP is still emitted even though table will be dropped with CASCADE.
	// This is safe (IF EXISTS) and keeps the logic simple.
	mustContain(t, joined, "DROP TABLE IF EXISTS todos CASCADE")
}

func TestDiffConfigs_MultiOp_RLSPolicy(t *testing.T) {
	old := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {
				Fields: []domain.Field{{Name: "id", Type: "bigserial"}},
				RLS: []domain.RLSPolicy{
					{Operations: []string{"select", "update", "delete"}, Check: "true"},
				},
			},
		},
	}
	new := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {
				Fields: []domain.Field{{Name: "id", Type: "bigserial"}},
			},
		},
	}

	diff := diffConfigs(old, new)
	joined := strings.Join(diff.Removals, "\n")

	mustContain(t, joined, "DROP POLICY IF EXISTS todos_select_0 ON todos")
	mustContain(t, joined, "DROP POLICY IF EXISTS todos_update_0 ON todos")
	mustContain(t, joined, "DROP POLICY IF EXISTS todos_delete_0 ON todos")
	mustContain(t, joined, "DISABLE ROW LEVEL SECURITY")
}

// --- Addition tests ---

func TestDiffConfigs_NewTable(t *testing.T) {
	old := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}}},
		},
	}
	new := &domain.Config{
		Tables: map[string]domain.Table{
			"todos":    {Fields: []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}}},
			"comments": {Fields: []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}, {Name: "body", Type: "text", Required: true}}},
		},
	}

	diff := diffConfigs(old, new)
	joined := strings.Join(diff.Additions, "\n")

	mustContain(t, joined, "CREATE TABLE IF NOT EXISTS comments")
	mustContain(t, joined, "body text NOT NULL")

	if strings.Contains(joined, "CREATE TABLE IF NOT EXISTS todos") {
		t.Error("should not re-create existing table")
	}
}

func TestDiffConfigs_NewColumn(t *testing.T) {
	old := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "title", Type: "text"},
			}},
		},
	}
	new := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "title", Type: "text"},
				{Name: "status", Type: "text", Default: "pending"},
			}},
		},
	}

	diff := diffConfigs(old, new)
	joined := strings.Join(diff.Additions, "\n")

	mustContain(t, joined, "ALTER TABLE todos ADD COLUMN status text DEFAULT 'pending'")
}

func TestDiffConfigs_NewColumnWithDefault(t *testing.T) {
	old := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
			}},
		},
	}
	new := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "priority", Type: "integer", Default: 0},
			}},
		},
	}

	diff := diffConfigs(old, new)
	joined := strings.Join(diff.Additions, "\n")

	mustContain(t, joined, "ALTER TABLE todos ADD COLUMN priority integer DEFAULT 0")
}

func TestDiffConfigs_NewColumnWithFK(t *testing.T) {
	old := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
			}},
		},
	}
	new := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "user_id", ForeignKey: &domain.ForeignKey{References: "users.id", OnDelete: "cascade"}},
			}},
		},
	}

	diff := diffConfigs(old, new)
	joined := strings.Join(diff.Additions, "\n")

	mustContain(t, joined, "ALTER TABLE todos ADD COLUMN user_id BIGINT REFERENCES public.users(id) ON DELETE CASCADE")
}

func TestDiffConfigs_ColumnTypeChange(t *testing.T) {
	old := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "priority", Type: "integer"},
			}},
		},
	}
	new := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "priority", Type: "bigint"},
			}},
		},
	}

	diff := diffConfigs(old, new)
	joined := strings.Join(diff.Alterations, "\n")

	mustContain(t, joined, "ALTER TABLE todos ALTER COLUMN priority TYPE bigint")
}

func TestDiffConfigs_ColumnTypeChange_SerialNormalization(t *testing.T) {
	old := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "count", Type: "bigserial"},
			}},
		},
	}
	new := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "count", Type: "bigint"},
			}},
		},
	}

	diff := diffConfigs(old, new)
	if len(diff.Alterations) != 0 {
		t.Errorf("bigserial→bigint should not generate ALTER (same underlying type), got: %v", diff.Alterations)
	}
}

func TestDiffConfigs_NullabilityChange_AddNotNull(t *testing.T) {
	old := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "title", Type: "text"},
			}},
		},
	}
	new := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "title", Type: "text", Required: true},
			}},
		},
	}

	diff := diffConfigs(old, new)
	joined := strings.Join(diff.Alterations, "\n")

	mustContain(t, joined, "ALTER TABLE todos ALTER COLUMN title SET NOT NULL")
}

func TestDiffConfigs_NullabilityChange_DropNotNull(t *testing.T) {
	old := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "title", Type: "text", Required: true},
			}},
		},
	}
	new := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "title", Type: "text"},
			}},
		},
	}

	diff := diffConfigs(old, new)
	joined := strings.Join(diff.Alterations, "\n")

	mustContain(t, joined, "ALTER TABLE todos ALTER COLUMN title DROP NOT NULL")
}

func TestDiffConfigs_NewIndex(t *testing.T) {
	old := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {
				Fields: []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}, {Name: "status", Type: "text"}},
			},
		},
	}
	new := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {
				Fields:  []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}, {Name: "status", Type: "text"}},
				Indexes: []domain.Index{{Columns: []string{"status"}}},
			},
		},
	}

	diff := diffConfigs(old, new)
	// New indexes for existing tables are not in Additions (they're re-emitted
	// as idempotent CREATE INDEX IF NOT EXISTS in planUpdate). But for new
	// tables, they ARE in Additions via diffNewTables.
	if len(diff.Removals) != 0 {
		t.Errorf("expected no removals, got: %v", diff.Removals)
	}
}

func TestDiffConfigs_NewUniqueIndex(t *testing.T) {
	old := &domain.Config{
		Tables: map[string]domain.Table{},
	}
	new := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {
				Fields:  []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}, {Name: "code", Type: "text"}},
				Indexes: []domain.Index{{Columns: []string{"code"}, Unique: true}},
			},
		},
	}

	diff := diffConfigs(old, new)
	joined := strings.Join(diff.Additions, "\n")

	mustContain(t, joined, "CREATE TABLE IF NOT EXISTS todos")
	mustContain(t, joined, "CREATE UNIQUE INDEX IF NOT EXISTS idx_todos_code ON todos (code)")
}

func TestDiffConfigs_NewRPCFunction(t *testing.T) {
	old := &domain.Config{
		RPC: map[string]domain.Function{},
	}
	new := &domain.Config{
		RPC: map[string]domain.Function{
			"add": {
				Language:   "sql",
				Volatility: "immutable",
				Security:   "invoker",
				Returns:    domain.FuncReturn{Type: "int"},
				Args:       []domain.FuncArg{{Name: "a", Type: "int"}, {Name: "b", Type: "int"}},
				Body:       "SELECT a + b;",
			},
		},
	}

	// RPC functions are re-emitted as idempotent CREATE OR REPLACE in planUpdate,
	// not tracked in diff.Additions. This test verifies no spurious removals.
	diff := diffConfigs(old, new)
	if len(diff.Removals) != 0 {
		t.Errorf("expected no removals for new function, got: %v", diff.Removals)
	}
}

func TestDiffConfigs_ChangedRPCFunction(t *testing.T) {
	old := &domain.Config{
		RPC: map[string]domain.Function{
			"greet": {Returns: domain.FuncReturn{Type: "text"}, Body: "SELECT 'hello';", Language: "sql", Volatility: "immutable", Security: "invoker"},
		},
	}
	new := &domain.Config{
		RPC: map[string]domain.Function{
			"greet": {Returns: domain.FuncReturn{Type: "text"}, Body: "SELECT 'hi';", Language: "sql", Volatility: "immutable", Security: "invoker"},
		},
	}

	// Changed function should not be dropped (CREATE OR REPLACE handles it)
	diff := diffConfigs(old, new)
	if len(diff.Removals) != 0 {
		t.Errorf("changed function should not generate removals, got: %v", diff.Removals)
	}
}

func TestDiffConfigs_NewExtension(t *testing.T) {
	old := &domain.Config{
		Extensions: []string{"pgcrypto"},
	}
	new := &domain.Config{
		Extensions: []string{"pgcrypto", "pg_trgm"},
	}

	diff := diffConfigs(old, new)
	joined := strings.Join(diff.Additions, "\n")

	mustContain(t, joined, "CREATE EXTENSION IF NOT EXISTS pg_trgm")

	if strings.Contains(joined, "pgcrypto") {
		t.Error("should not re-create existing extension")
	}
}


func TestDiffConfigs_NewStorage(t *testing.T) {
	old := &domain.Config{
		Storage: map[string]domain.Bucket{},
	}
	new := &domain.Config{
		Storage: map[string]domain.Bucket{
			"avatars": {Public: true},
		},
	}

	diff := diffConfigs(old, new)
	joined := strings.Join(diff.Additions, "\n")

	mustContain(t, joined, "CREATE TABLE IF NOT EXISTS storage.objects")
}

func TestDiffConfigs_NewTableWithIndexes(t *testing.T) {
	old := &domain.Config{
		Tables: map[string]domain.Table{},
	}
	new := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {
				Fields:  []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}, {Name: "status", Type: "text"}},
				Indexes: []domain.Index{{Columns: []string{"status"}}},
			},
		},
	}

	diff := diffConfigs(old, new)
	joined := strings.Join(diff.Additions, "\n")

	mustContain(t, joined, "CREATE TABLE IF NOT EXISTS todos")
	mustContain(t, joined, "CREATE INDEX IF NOT EXISTS idx_todos_status ON todos (status)")
}

func TestDiffConfigs_MixedChanges(t *testing.T) {
	old := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "title", Type: "text"},
				{Name: "status", Type: "text"},
			}},
			"comments": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
			}},
		},
	}
	new := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "title", Type: "text", Required: true},
				{Name: "priority", Type: "integer"},
			}},
			"posts": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "body", Type: "text"},
			}},
		},
	}

	diff := diffConfigs(old, new)

	removals := strings.Join(diff.Removals, "\n")
	mustContain(t, removals, "DROP TABLE IF EXISTS comments CASCADE")
	mustContain(t, removals, "ALTER TABLE todos DROP COLUMN IF EXISTS status")

	additions := strings.Join(diff.Additions, "\n")
	mustContain(t, additions, "CREATE TABLE IF NOT EXISTS posts")
	mustContain(t, additions, "ALTER TABLE todos ADD COLUMN priority integer")

	alterations := strings.Join(diff.Alterations, "\n")
	mustContain(t, alterations, "ALTER TABLE todos ALTER COLUMN title SET NOT NULL")
}

func TestDiffConfigs_NewAuth(t *testing.T) {
	old := &domain.Config{}
	new := &domain.Config{
		Auth: &domain.Auth{
			RefreshTokens: true,
			Email:         &domain.AuthEmail{VerifyEmail: true},
		},
		Tables: map[string]domain.Table{
			"users": {Fields: []domain.Field{
				{Name: "display_name", Type: "text"},
			}},
		},
	}

	diff := diffConfigs(old, new)
	joined := strings.Join(diff.Additions, "\n")

	mustContain(t, joined, "CREATE TABLE IF NOT EXISTS auth.users")
	mustContain(t, joined, "CREATE TABLE IF NOT EXISTS auth.refresh_tokens")
	mustContain(t, joined, "CREATE TABLE IF NOT EXISTS auth.one_time_tokens")
}

func TestDiffConfigs_AuthRefreshTokensAdded(t *testing.T) {
	old := &domain.Config{
		Auth: &domain.Auth{
			RefreshTokens: false,
		},
	}
	new := &domain.Config{
		Auth: &domain.Auth{
			RefreshTokens: true,
		},
	}

	diff := diffConfigs(old, new)
	joined := strings.Join(diff.Additions, "\n")

	mustContain(t, joined, "CREATE TABLE IF NOT EXISTS auth.refresh_tokens")
}

func TestDiffConfigs_NormalizeType(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"bigserial", "bigint"},
		{"serial", "integer"},
		{"varchar(255)", "varchar"},
		{"bool", "boolean"},
		{"text", "text"},
		{"integer", "integer"},
		{"timestamptz", "timestamptz"},
		{"int", "integer"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizeType(tt.input)
			if got != tt.want {
				t.Errorf("normalizeType(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
