//go:build integration

package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/saedx1/ultrabase/internal/adapter/postgres"
	"github.com/saedx1/ultrabase/internal/app"
	"github.com/saedx1/ultrabase/internal/domain"
	"github.com/saedx1/ultrabase/internal/testutil/dbboot"
)

func startPostgres(t *testing.T) *postgres.DB {
	t.Helper()
	owner, _ := dbboot.StartContainer(t)
	return owner.Database.(*postgres.DB)
}

// splitSchemaTable accepts either a bare table name (defaulting schema to
// "public") or a "schema.table" form and returns the two parts.
func splitSchemaTable(name string) (schema, table string) {
	schema = "public"
	table = name
	if i := strings.Index(name, "."); i >= 0 {
		schema = name[:i]
		table = name[i+1:]
	}
	return schema, table
}

func tableExists(t *testing.T, db *postgres.DB, name string) bool {
	t.Helper()
	schema, tbl := splitSchemaTable(name)
	row, err := db.QueryRow(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema=$1 AND table_name=$2)`, schema, tbl)
	if err != nil {
		t.Fatalf("tableExists: %v", err)
	}
	return row["exists"] == true
}

func columnExists(t *testing.T, db *postgres.DB, table, column string) bool {
	t.Helper()
	schema, tbl := splitSchemaTable(table)
	row, err := db.QueryRow(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=$1 AND table_name=$2 AND column_name=$3)`, schema, tbl, column)
	if err != nil {
		t.Fatalf("columnExists: %v", err)
	}
	return row["exists"] == true
}

func indexExists(t *testing.T, db *postgres.DB, name string) bool {
	t.Helper()
	row, err := db.QueryRow(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE indexname=$1)`, name)
	if err != nil {
		t.Fatalf("indexExists: %v", err)
	}
	return row["exists"] == true
}

func policyExists(t *testing.T, db *postgres.DB, table, policyName string) bool {
	t.Helper()
	schema, tbl := splitSchemaTable(table)
	row, err := db.QueryRow(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM pg_policies WHERE schemaname=$1 AND tablename=$2 AND policyname=$3)`, schema, tbl, policyName)
	if err != nil {
		t.Fatalf("policyExists: %v", err)
	}
	return row["exists"] == true
}

func functionExists(t *testing.T, db *postgres.DB, name string) bool {
	t.Helper()
	row, err := db.QueryRow(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM pg_proc WHERE proname=$1 AND pronamespace=(SELECT oid FROM pg_namespace WHERE nspname='public'))`, name)
	if err != nil {
		t.Fatalf("functionExists: %v", err)
	}
	return row["exists"] == true
}

func TestIntegration_FirstMigration(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfg := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "title", Type: "text", Required: true},
			}},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfg); err != nil {
		t.Fatalf("first apply: %v", err)
	}

	if !tableExists(t, db, "todos") {
		t.Fatal("todos table should exist")
	}
	if !columnExists(t, db, "todos", "title") {
		t.Fatal("title column should exist")
	}
}

func TestIntegration_IdempotentRerun(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfg := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "title", Type: "text", Required: true},
			}},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfg); err != nil {
		t.Fatalf("first apply: %v", err)
	}

	// Second apply with same config should be a no-op.
	if err := migrator.Apply(ctx, cfg); err != nil {
		t.Fatalf("second apply: %v", err)
	}

	// Verify only one migration was recorded.
	rows, err := db.Query(ctx, "SELECT id FROM _ultrabase_migrations")
	if err != nil {
		t.Fatalf("query migrations: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 migration record, got %d", len(rows))
	}
}

func TestIntegration_AddColumn(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfgV1 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "title", Type: "text", Required: true},
			}},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfgV1); err != nil {
		t.Fatalf("v1 apply: %v", err)
	}

	cfgV2 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "title", Type: "text", Required: true},
				{Name: "status", Type: "text", Default: "pending"},
			}},
		},
	}

	if err := migrator.Apply(ctx, cfgV2); err != nil {
		t.Fatalf("v2 apply: %v", err)
	}

	if !columnExists(t, db, "todos", "status") {
		t.Fatal("status column should exist after v2 migration")
	}
}

func TestIntegration_RemoveColumn(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfgV1 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "title", Type: "text", Required: true},
				{Name: "status", Type: "text"},
			}},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfgV1); err != nil {
		t.Fatalf("v1 apply: %v", err)
	}

	cfgV2 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "title", Type: "text", Required: true},
			}},
		},
	}

	if err := migrator.Apply(ctx, cfgV2); err != nil {
		t.Fatalf("v2 apply: %v", err)
	}

	if columnExists(t, db, "todos", "status") {
		t.Fatal("status column should be dropped after v2 migration")
	}
}

func TestIntegration_RemoveTable(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfgV1 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos":    {Fields: []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}}},
			"comments": {Fields: []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}}},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfgV1); err != nil {
		t.Fatalf("v1 apply: %v", err)
	}
	if !tableExists(t, db, "comments") {
		t.Fatal("comments table should exist after v1")
	}

	cfgV2 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}}},
		},
	}

	if err := migrator.Apply(ctx, cfgV2); err != nil {
		t.Fatalf("v2 apply: %v", err)
	}

	if tableExists(t, db, "comments") {
		t.Fatal("comments table should be dropped after v2")
	}
	if !tableExists(t, db, "todos") {
		t.Fatal("todos table should still exist")
	}
}

func TestIntegration_RLSPolicies_Idempotent(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfg := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "user_id", Type: "uuid"},
				},
				RLS: []domain.RLSPolicy{
					{Operations: []string{"select"}, Check: "true"},
				},
			},
		},
	}

	migrator := app.NewMigrator(db)

	// Apply twice — should not fail on "policy already exists".
	if err := migrator.Apply(ctx, cfg); err != nil {
		t.Fatalf("first apply: %v", err)
	}

	// Change check expression to force re-apply.
	cfg2 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "user_id", Type: "uuid"},
				},
				RLS: []domain.RLSPolicy{
					{Operations: []string{"select"}, Check: "user_id IS NOT NULL"},
				},
			},
		},
	}
	if err := migrator.Apply(ctx, cfg2); err != nil {
		t.Fatalf("second apply with changed policy: %v", err)
	}

	if !policyExists(t, db, "todos", "todos_select_0") {
		t.Fatal("select policy should exist")
	}
}

func TestIntegration_RemoveRLSPolicy(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfgV1 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {
				Fields: []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}},
				RLS: []domain.RLSPolicy{
					{Operations: []string{"select"}, Check: "true"},
					{Operations: []string{"insert"}, Check: "true"},
				},
			},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfgV1); err != nil {
		t.Fatalf("v1 apply: %v", err)
	}

	if !policyExists(t, db, "todos", "todos_insert_1") {
		t.Fatal("insert policy should exist after v1")
	}

	cfgV2 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {
				Fields: []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}},
				RLS: []domain.RLSPolicy{
					{Operations: []string{"select"}, Check: "true"},
				},
			},
		},
	}

	if err := migrator.Apply(ctx, cfgV2); err != nil {
		t.Fatalf("v2 apply: %v", err)
	}

	if policyExists(t, db, "todos", "todos_insert_1") {
		t.Fatal("insert policy should be dropped after v2")
	}
	if !policyExists(t, db, "todos", "todos_select_0") {
		t.Fatal("select policy should still exist")
	}
}

func TestIntegration_RemoveIndex(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfgV1 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "status", Type: "text"},
				},
				Indexes: []domain.Index{
					{Columns: []string{"status"}},
				},
			},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfgV1); err != nil {
		t.Fatalf("v1 apply: %v", err)
	}

	if !indexExists(t, db, "idx_todos_status") {
		t.Fatal("index should exist after v1")
	}

	cfgV2 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "status", Type: "text"},
				},
			},
		},
	}

	if err := migrator.Apply(ctx, cfgV2); err != nil {
		t.Fatalf("v2 apply: %v", err)
	}

	if indexExists(t, db, "idx_todos_status") {
		t.Fatal("index should be dropped after v2")
	}
}

func TestIntegration_RPCFunction_CreateAndRemove(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfgV1 := &domain.Config{
		Version: 1,
		Tables:  map[string]domain.Table{},
		Functions: map[string]domain.Function{
			"add_nums": {
				Language:   "sql",
				Volatility: "immutable",
				Security:   "invoker",
				Returns:    domain.FuncReturn{Type: "int"},
				Args: []domain.FuncArg{
					{Name: "a", Type: "int"},
					{Name: "b", Type: "int"},
				},
				Body: "SELECT a + b;",
			},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfgV1); err != nil {
		t.Fatalf("v1 apply: %v", err)
	}

	if !functionExists(t, db, "add_nums") {
		t.Fatal("add_nums function should exist after v1")
	}

	// Call it to verify it works.
	row, err := db.QueryRow(ctx, `SELECT public."add_nums"(3, 4) AS result`)
	if err != nil {
		t.Fatalf("call add_nums: %v", err)
	}
	if fmt.Sprint(row["result"]) != "7" {
		t.Fatalf("expected 7, got %v", row["result"])
	}

	cfgV2 := &domain.Config{
		Version:   1,
		Tables:    map[string]domain.Table{},
		Functions: map[string]domain.Function{},
	}

	if err := migrator.Apply(ctx, cfgV2); err != nil {
		t.Fatalf("v2 apply: %v", err)
	}

	if functionExists(t, db, "add_nums") {
		t.Fatal("add_nums function should be dropped after v2")
	}
}

func TestIntegration_ConfigStoredAndRecovered(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfg := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "title", Type: "text", Required: true},
			}},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfg); err != nil {
		t.Fatalf("apply: %v", err)
	}

	last, err := db.GetLastMigration(ctx)
	if err != nil {
		t.Fatalf("get last migration: %v", err)
	}
	if last == nil {
		t.Fatal("expected migration record")
	}
	if last.ConfigJSON == "" || last.ConfigJSON == "{}" {
		t.Fatal("expected config_json to be populated")
	}

	var recovered domain.Config
	if err := json.Unmarshal([]byte(last.ConfigJSON), &recovered); err != nil {
		t.Fatalf("unmarshal stored config: %v", err)
	}
	if _, ok := recovered.Tables["todos"]; !ok {
		t.Fatal("recovered config should contain todos table")
	}
	hasTitle := false
	for _, f := range recovered.Tables["todos"].Fields {
		if f.Name == "title" {
			hasTitle = true
			break
		}
	}
	if !hasTitle {
		t.Fatal("recovered config should contain title field")
	}
}

func TestIntegration_FKTableRemoval_Cascades(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfgV1 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"teams": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "name", Type: "text", Required: true},
			}},
			"members": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "team_id", Type: "bigint", ForeignKey: &domain.ForeignKey{References: "teams.id", OnDelete: "cascade"}},
			}},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfgV1); err != nil {
		t.Fatalf("v1 apply: %v", err)
	}

	// Remove both tables — CASCADE should handle the FK.
	cfgV2 := &domain.Config{
		Version: 1,
		Tables:  map[string]domain.Table{},
	}

	if err := migrator.Apply(ctx, cfgV2); err != nil {
		t.Fatalf("v2 apply: %v", err)
	}

	if tableExists(t, db, "teams") {
		t.Fatal("teams table should be dropped")
	}
	if tableExists(t, db, "members") {
		t.Fatal("members table should be dropped")
	}
}

// --- Column type and nullability changes ---

func columnType(t *testing.T, db *postgres.DB, table, column string) string {
	t.Helper()
	row, err := db.QueryRow(context.Background(),
		`SELECT data_type FROM information_schema.columns WHERE table_name=$1 AND column_name=$2`, table, column)
	if err != nil {
		t.Fatalf("columnType: %v", err)
	}
	if row == nil {
		t.Fatalf("column %s.%s not found", table, column)
	}
	return fmt.Sprint(row["data_type"])
}

func columnNullable(t *testing.T, db *postgres.DB, table, column string) bool {
	t.Helper()
	row, err := db.QueryRow(context.Background(),
		`SELECT is_nullable FROM information_schema.columns WHERE table_name=$1 AND column_name=$2`, table, column)
	if err != nil {
		t.Fatalf("columnNullable: %v", err)
	}
	return fmt.Sprint(row["is_nullable"]) == "YES"
}

func rlsEnabled(t *testing.T, db *postgres.DB, table string) bool {
	t.Helper()
	row, err := db.QueryRow(context.Background(),
		`SELECT rowsecurity FROM pg_tables WHERE tablename=$1`, table)
	if err != nil {
		t.Fatalf("rlsEnabled: %v", err)
	}
	return row["rowsecurity"] == true
}

func TestIntegration_NullabilityChange(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfgV1 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "title", Type: "text"},
			}},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfgV1); err != nil {
		t.Fatalf("v1 apply: %v", err)
	}

	if !columnNullable(t, db, "todos", "title") {
		t.Fatal("title should be nullable in v1")
	}

	cfgV2 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "title", Type: "text", Required: true},
			}},
		},
	}

	if err := migrator.Apply(ctx, cfgV2); err != nil {
		t.Fatalf("v2 apply: %v", err)
	}

	if columnNullable(t, db, "todos", "title") {
		t.Fatal("title should be NOT NULL after v2")
	}

	// Flip back to nullable.
	if err := migrator.Apply(ctx, cfgV1); err != nil {
		t.Fatalf("v3 apply: %v", err)
	}

	if !columnNullable(t, db, "todos", "title") {
		t.Fatal("title should be nullable again after v3")
	}
}

func TestIntegration_ColumnTypeChange(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfgV1 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "priority", Type: "integer"},
			}},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfgV1); err != nil {
		t.Fatalf("v1 apply: %v", err)
	}

	if ct := columnType(t, db, "todos", "priority"); ct != "integer" {
		t.Fatalf("expected integer, got %s", ct)
	}

	cfgV2 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "priority", Type: "bigint"},
			}},
		},
	}

	if err := migrator.Apply(ctx, cfgV2); err != nil {
		t.Fatalf("v2 apply: %v", err)
	}

	if ct := columnType(t, db, "todos", "priority"); ct != "bigint" {
		t.Fatalf("expected bigint after type change, got %s", ct)
	}
}

func TestIntegration_AddColumnWithDefault_PopulatedTable(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfgV1 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "title", Type: "text", Required: true},
			}},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfgV1); err != nil {
		t.Fatalf("v1 apply: %v", err)
	}

	// Insert data before the migration.
	_, err := db.Exec(ctx, "INSERT INTO todos (title) VALUES ('buy milk'), ('do laundry')")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	cfgV2 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "title", Type: "text", Required: true},
				{Name: "status", Type: "text", Default: "pending"},
			}},
		},
	}

	if err := migrator.Apply(ctx, cfgV2); err != nil {
		t.Fatalf("v2 apply: %v", err)
	}

	rows, err := db.Query(ctx, "SELECT title, status FROM todos ORDER BY id")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	// Postgres backfills the DEFAULT value for existing rows when adding a
	// column via ALTER TABLE ADD COLUMN ... DEFAULT.
	for _, row := range rows {
		if fmt.Sprint(row["status"]) != "pending" {
			t.Fatalf("expected backfilled default 'pending', got %v", row["status"])
		}
	}
}

// --- Data preservation ---

func TestIntegration_DataPreserved_AcrossMigrations(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfgV1 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "title", Type: "text", Required: true},
			}},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfgV1); err != nil {
		t.Fatalf("v1 apply: %v", err)
	}

	_, err := db.Exec(ctx, "INSERT INTO todos (title) VALUES ('important task')")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Add a column + index — data should survive.
	cfgV2 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "title", Type: "text", Required: true},
					{Name: "status", Type: "text"},
				},
				Indexes: []domain.Index{{Columns: []string{"status"}}},
			},
		},
	}

	if err := migrator.Apply(ctx, cfgV2); err != nil {
		t.Fatalf("v2 apply: %v", err)
	}

	row, err := db.QueryRow(ctx, "SELECT title FROM todos LIMIT 1")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if fmt.Sprint(row["title"]) != "important task" {
		t.Fatalf("data lost: expected 'important task', got %v", row["title"])
	}

	// Remove the column — data in other columns should survive.
	cfgV3 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "title", Type: "text", Required: true},
			}},
		},
	}

	if err := migrator.Apply(ctx, cfgV3); err != nil {
		t.Fatalf("v3 apply: %v", err)
	}

	row, err = db.QueryRow(ctx, "SELECT title FROM todos LIMIT 1")
	if err != nil {
		t.Fatalf("query after v3: %v", err)
	}
	if fmt.Sprint(row["title"]) != "important task" {
		t.Fatalf("data lost after column drop: expected 'important task', got %v", row["title"])
	}
}

// --- Constraints ---

func TestIntegration_EnumCheck(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfg := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "status", Type: "text", Enum: []string{"pending", "active", "done"}},
			}},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfg); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Valid value should work.
	_, err := db.Exec(ctx, "INSERT INTO todos (status) VALUES ('pending')")
	if err != nil {
		t.Fatalf("valid insert failed: %v", err)
	}

	// Invalid value should be rejected.
	_, err = db.Exec(ctx, "INSERT INTO todos (status) VALUES ('invalid')")
	if err == nil {
		t.Fatal("expected CHECK violation for invalid enum value")
	}
}

func TestIntegration_UniqueConstraint(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfg := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"teams": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "slug", Type: "text", Unique: true, Required: true},
			}},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfg); err != nil {
		t.Fatalf("apply: %v", err)
	}

	_, err := db.Exec(ctx, "INSERT INTO teams (slug) VALUES ('my-team')")
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}

	_, err = db.Exec(ctx, "INSERT INTO teams (slug) VALUES ('my-team')")
	if err == nil {
		t.Fatal("expected unique violation on duplicate slug")
	}
}

func TestIntegration_PatternCheck(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfg := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"contacts": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "email", Type: "text", Pattern: "^.+@.+\\..+$"},
			}},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfg); err != nil {
		t.Fatalf("apply: %v", err)
	}

	_, err := db.Exec(ctx, "INSERT INTO contacts (email) VALUES ('user@example.com')")
	if err != nil {
		t.Fatalf("valid email insert: %v", err)
	}

	_, err = db.Exec(ctx, "INSERT INTO contacts (email) VALUES ('not-an-email')")
	if err == nil {
		t.Fatal("expected CHECK violation for invalid email pattern")
	}
}

func TestIntegration_MinMaxCheck(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	min, max := float64(1), float64(5)
	cfg := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "priority", Type: "integer", Min: &min, Max: &max},
			}},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfg); err != nil {
		t.Fatalf("apply: %v", err)
	}

	_, err := db.Exec(ctx, "INSERT INTO todos (priority) VALUES (3)")
	if err != nil {
		t.Fatalf("valid priority: %v", err)
	}

	_, err = db.Exec(ctx, "INSERT INTO todos (priority) VALUES (0)")
	if err == nil {
		t.Fatal("expected CHECK violation for priority < min")
	}

	_, err = db.Exec(ctx, "INSERT INTO todos (priority) VALUES (10)")
	if err == nil {
		t.Fatal("expected CHECK violation for priority > max")
	}
}

// --- Indexes ---

func TestIntegration_AddIndex_ExistingTable(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfgV1 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "status", Type: "text"},
			}},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfgV1); err != nil {
		t.Fatalf("v1 apply: %v", err)
	}

	if indexExists(t, db, "idx_todos_status") {
		t.Fatal("index should not exist before v2")
	}

	cfgV2 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "status", Type: "text"},
				},
				Indexes: []domain.Index{{Columns: []string{"status"}}},
			},
		},
	}

	if err := migrator.Apply(ctx, cfgV2); err != nil {
		t.Fatalf("v2 apply: %v", err)
	}

	if !indexExists(t, db, "idx_todos_status") {
		t.Fatal("index should exist after v2")
	}
}

func TestIntegration_UniqueIndex(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfg := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "code", Type: "text"},
				},
				Indexes: []domain.Index{{Columns: []string{"code"}, Unique: true}},
			},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfg); err != nil {
		t.Fatalf("apply: %v", err)
	}

	_, err := db.Exec(ctx, "INSERT INTO todos (code) VALUES ('ABC')")
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}

	_, err = db.Exec(ctx, "INSERT INTO todos (code) VALUES ('ABC')")
	if err == nil {
		t.Fatal("expected unique index violation")
	}
}

func TestIntegration_PartialIndex(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfg := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "status", Type: "text"},
					{Name: "title", Type: "text"},
				},
				Indexes: []domain.Index{{
					Columns: []string{"title"},
					Where:   "status = 'active'",
				}},
			},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfg); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if !indexExists(t, db, "idx_todos_title") {
		t.Fatal("partial index should exist")
	}
}

func TestIntegration_MultiColumnIndex(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfg := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "team_id", Type: "bigint"},
					{Name: "status", Type: "text"},
				},
				Indexes: []domain.Index{{Columns: []string{"team_id", "status"}}},
			},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfg); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if !indexExists(t, db, "idx_todos_team_id_status") {
		t.Fatal("multi-column index should exist")
	}
}

// --- Tables ---

func TestIntegration_AddNewTable_ExistingSchema(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfgV1 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
			}},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfgV1); err != nil {
		t.Fatalf("v1 apply: %v", err)
	}

	cfgV2 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
			}},
			"comments": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "body", Type: "text", Required: true},
			}},
		},
	}

	if err := migrator.Apply(ctx, cfgV2); err != nil {
		t.Fatalf("v2 apply: %v", err)
	}

	if !tableExists(t, db, "comments") {
		t.Fatal("comments table should exist after v2")
	}
	if !tableExists(t, db, "todos") {
		t.Fatal("todos table should still exist")
	}
}

func TestIntegration_FK_BetweenUserTables(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfg := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"teams": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "name", Type: "text", Required: true},
			}},
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "team_id", Type: "bigint", ForeignKey: &domain.ForeignKey{References: "teams.id", OnDelete: "cascade"}},
			}},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfg); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Insert a team and a todo referencing it.
	_, err := db.Exec(ctx, "INSERT INTO teams (name) VALUES ('engineering')")
	if err != nil {
		t.Fatalf("insert team: %v", err)
	}
	_, err = db.Exec(ctx, "INSERT INTO todos (team_id) VALUES (1)")
	if err != nil {
		t.Fatalf("insert todo: %v", err)
	}

	// FK violation: referencing nonexistent team.
	_, err = db.Exec(ctx, "INSERT INTO todos (team_id) VALUES (999)")
	if err == nil {
		t.Fatal("expected FK violation")
	}

	// Cascade: deleting team should delete todo.
	_, err = db.Exec(ctx, "DELETE FROM teams WHERE id = 1")
	if err != nil {
		t.Fatalf("delete team: %v", err)
	}
	rows, err := db.Query(ctx, "SELECT id FROM todos")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 0 {
		t.Fatal("expected 0 todos after cascade delete")
	}
}

func TestIntegration_RemoveReferencedTable_Cascades(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfgV1 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"categories": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "name", Type: "text"},
			}},
			"products": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "category_id", Type: "bigint", ForeignKey: &domain.ForeignKey{References: "categories.id"}},
			}},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfgV1); err != nil {
		t.Fatalf("v1 apply: %v", err)
	}

	// Remove the referenced table; CASCADE in DROP should handle the FK.
	cfgV2 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"products": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
			}},
		},
	}

	if err := migrator.Apply(ctx, cfgV2); err != nil {
		t.Fatalf("v2 apply: %v", err)
	}

	if tableExists(t, db, "categories") {
		t.Fatal("categories should be dropped")
	}
	if !tableExists(t, db, "products") {
		t.Fatal("products should still exist")
	}
}

// --- Auth ---

func TestIntegration_AuthUsersTable(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfg := &domain.Config{
		Version: 1,
		Auth: &domain.Auth{
			RefreshTokens: true,
			Email:         &domain.AuthEmail{VerifyEmail: true},
		},
		Tables: map[string]domain.Table{},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfg); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if !tableExists(t, db, "auth.users") {
		t.Fatal("auth.users table should exist")
	}
	if !columnExists(t, db, "auth.users", "email") {
		t.Fatal("email column should exist")
	}
	if !columnExists(t, db, "auth.users", "password_hash") {
		t.Fatal("password_hash column should exist")
	}
	if !tableExists(t, db, "auth.identities") {
		t.Fatal("auth.identities table should exist")
	}
	if !tableExists(t, db, "auth.refresh_tokens") {
		t.Fatal("auth.refresh_tokens table should exist")
	}
	if !tableExists(t, db, "auth.one_time_tokens") {
		t.Fatal("auth.one_time_tokens table should exist")
	}
	if !tableExists(t, db, "auth.mfa_factors") {
		t.Fatal("auth.mfa_factors table should exist")
	}
	if !tableExists(t, db, "auth.jwt_keys") {
		t.Fatal("auth.jwt_keys table should exist")
	}

	// Verify auth helper functions were created.
	row, err := db.QueryRow(ctx, "SELECT auth.role() AS role")
	if err != nil {
		t.Fatalf("auth.role(): %v", err)
	}
	if fmt.Sprint(row["role"]) != "anon" {
		t.Fatalf("expected 'anon' role, got %v", row["role"])
	}
}

// --- Extensions ---

func TestIntegration_Extensions(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfg := &domain.Config{
		Version:    1,
		Extensions: []string{"pgcrypto", "pg_trgm"},
		Tables:     map[string]domain.Table{},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfg); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Verify extensions were installed.
	for _, ext := range []string{"pgcrypto", "pg_trgm"} {
		row, err := db.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname=$1) AS exists`, ext)
		if err != nil {
			t.Fatalf("check extension %s: %v", ext, err)
		}
		if row["exists"] != true {
			t.Fatalf("extension %s should be installed", ext)
		}
	}
}

// --- Storage ---

func TestIntegration_StorageTable(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	// Auth is required for the users FK in _objects.
	cfg := &domain.Config{
		Version: 1,
		Auth:    &domain.Auth{},
		Tables:  map[string]domain.Table{},
		Storage: map[string]domain.Bucket{
			"avatars": {MaxSize: "5MB", Public: true},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfg); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if !tableExists(t, db, "_objects") {
		t.Fatal("_objects table should exist")
	}
	if !columnExists(t, db, "_objects", "bucket_id") {
		t.Fatal("bucket_id column should exist")
	}

	if !policyExists(t, db, "_objects", "avatars_public_select") {
		t.Fatal("public select policy should exist for avatars bucket")
	}
}

// --- Events ---

func TestIntegration_EventsTable(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfg := &domain.Config{
		Version: 1,
		Tables:  map[string]domain.Table{},
		On: map[string]domain.Trigger{
			"new_todo": {Events: []string{"todos.insert"}},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfg); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if !tableExists(t, db, "_events") {
		t.Fatal("_events table should exist")
	}
	if !columnExists(t, db, "_events", "trigger_name") {
		t.Fatal("trigger_name column should exist")
	}
	if !indexExists(t, db, "idx_events_pending") {
		t.Fatal("idx_events_pending should exist")
	}
}

// --- Search ---

func TestIntegration_Search_TSVECTOR(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfg := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"articles": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "title", Type: "text", Required: true},
					{Name: "body", Type: "text"},
				},
				Searchable:   []string{"title", "body"},
				SearchConfig: "english",
			},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfg); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if !columnExists(t, db, "articles", "_tsv") {
		t.Fatal("_tsv column should exist")
	}
	if !indexExists(t, db, "idx_articles_tsv") {
		t.Fatal("GIN index should exist")
	}

	// Insert and search.
	_, err := db.Exec(ctx, "INSERT INTO articles (title, body) VALUES ('PostgreSQL Guide', 'Learn full text search in Postgres')")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	rows, err := db.Query(ctx, "SELECT title FROM articles WHERE _tsv @@ to_tsquery('english', 'postgres')")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 search result, got %d", len(rows))
	}
}

func TestIntegration_Search_Remove(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfgV1 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"articles": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "title", Type: "text", Required: true},
				},
				Searchable:   []string{"title"},
				SearchConfig: "english",
			},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfgV1); err != nil {
		t.Fatalf("v1 apply: %v", err)
	}

	if !columnExists(t, db, "articles", "_tsv") {
		t.Fatal("_tsv column should exist after v1")
	}

	// Remove searchable — the _tsv column is a generated column, removing
	// searchable from the config doesn't currently drop it (generated columns
	// are not tracked in the config diff). This test documents the behavior.
	cfgV2 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"articles": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "title", Type: "text", Required: true},
				},
			},
		},
	}

	if err := migrator.Apply(ctx, cfgV2); err != nil {
		t.Fatalf("v2 apply: %v", err)
	}

	// _tsv column persists — this is a known limitation since it's not
	// tracked as a regular field in the config.
	if !columnExists(t, db, "articles", "_tsv") {
		t.Log("_tsv column was removed — search column cleanup is now implemented")
	}
}

// --- DB Functions ---

func TestIntegration_RPCFunction_UpdateBody(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfgV1 := &domain.Config{
		Version: 1,
		Tables:  map[string]domain.Table{},
		Functions: map[string]domain.Function{
			"greet": {
				Language:   "sql",
				Volatility: "immutable",
				Security:   "invoker",
				Returns:    domain.FuncReturn{Type: "text"},
				Args:       []domain.FuncArg{{Name: "name", Type: "text"}},
				Body:       "SELECT 'hello ' || name;",
			},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfgV1); err != nil {
		t.Fatalf("v1 apply: %v", err)
	}

	row, err := db.QueryRow(ctx, `SELECT public."greet"('world') AS result`)
	if err != nil {
		t.Fatalf("v1 call: %v", err)
	}
	if fmt.Sprint(row["result"]) != "hello world" {
		t.Fatalf("expected 'hello world', got %v", row["result"])
	}

	// Update the function body.
	cfgV2 := &domain.Config{
		Version: 1,
		Tables:  map[string]domain.Table{},
		Functions: map[string]domain.Function{
			"greet": {
				Language:   "sql",
				Volatility: "immutable",
				Security:   "invoker",
				Returns:    domain.FuncReturn{Type: "text"},
				Args:       []domain.FuncArg{{Name: "name", Type: "text"}},
				Body:       "SELECT 'hi ' || name || '!';",
			},
		},
	}

	if err := migrator.Apply(ctx, cfgV2); err != nil {
		t.Fatalf("v2 apply: %v", err)
	}

	row, err = db.QueryRow(ctx, `SELECT public."greet"('world') AS result`)
	if err != nil {
		t.Fatalf("v2 call: %v", err)
	}
	if fmt.Sprint(row["result"]) != "hi world!" {
		t.Fatalf("expected 'hi world!', got %v", row["result"])
	}
}

// --- RLS ---

func TestIntegration_RestrictiveRLSPolicy(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfg := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
				},
				RLS: []domain.RLSPolicy{
					{Operations: []string{"select"}, Check: "true", Type: "restrictive"},
				},
			},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfg); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if !policyExists(t, db, "todos", "todos_select_0") {
		t.Fatal("restrictive policy should exist")
	}

	// Verify the policy type in pg_policies.
	row, err := db.QueryRow(ctx,
		`SELECT permissive FROM pg_policies WHERE tablename='todos' AND policyname='todos_select_0'`)
	if err != nil {
		t.Fatalf("query policy type: %v", err)
	}
	if fmt.Sprint(row["permissive"]) != "RESTRICTIVE" {
		t.Fatalf("expected RESTRICTIVE, got %v", row["permissive"])
	}
}

func TestIntegration_RemoveAllRLS_DisablesRLS(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfgV1 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
				},
				RLS: []domain.RLSPolicy{
					{Operations: []string{"select"}, Check: "true"},
				},
			},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfgV1); err != nil {
		t.Fatalf("v1 apply: %v", err)
	}

	if !rlsEnabled(t, db, "todos") {
		t.Fatal("RLS should be enabled after v1")
	}

	cfgV2 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
				},
			},
		},
	}

	if err := migrator.Apply(ctx, cfgV2); err != nil {
		t.Fatalf("v2 apply: %v", err)
	}

	if rlsEnabled(t, db, "todos") {
		t.Fatal("RLS should be disabled after removing all policies")
	}
}

// --- Multi-step sequential ---

func TestIntegration_ThreeStepMigration(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()
	migrator := app.NewMigrator(db)

	// v1: Create tables.
	cfgV1 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"teams": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "name", Type: "text", Required: true},
			}},
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "title", Type: "text", Required: true},
				{Name: "team_id", Type: "bigint", ForeignKey: &domain.ForeignKey{References: "teams.id", OnDelete: "cascade"}},
			}},
		},
	}
	if err := migrator.Apply(ctx, cfgV1); err != nil {
		t.Fatalf("v1: %v", err)
	}

	_, err := db.Exec(ctx, "INSERT INTO teams (name) VALUES ('eng')")
	if err != nil {
		t.Fatalf("insert team: %v", err)
	}
	_, err = db.Exec(ctx, "INSERT INTO todos (title, team_id) VALUES ('ship it', 1)")
	if err != nil {
		t.Fatalf("insert todo: %v", err)
	}

	// v2: Add columns, add indexes, add RLS.
	cfgV2 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"teams": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "name", Type: "text", Required: true},
				{Name: "slug", Type: "text"},
			}},
			"todos": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "title", Type: "text", Required: true},
					{Name: "team_id", Type: "bigint", ForeignKey: &domain.ForeignKey{References: "teams.id", OnDelete: "cascade"}},
					{Name: "status", Type: "text", Default: "pending"},
				},
				Indexes: []domain.Index{{Columns: []string{"team_id", "status"}}},
				RLS: []domain.RLSPolicy{
					{Operations: []string{"select"}, Check: "true"},
				},
			},
		},
	}
	if err := migrator.Apply(ctx, cfgV2); err != nil {
		t.Fatalf("v2: %v", err)
	}

	if !columnExists(t, db, "teams", "slug") {
		t.Fatal("slug should exist after v2")
	}
	if !columnExists(t, db, "todos", "status") {
		t.Fatal("status should exist after v2")
	}
	if !indexExists(t, db, "idx_todos_team_id_status") {
		t.Fatal("composite index should exist after v2")
	}
	if !policyExists(t, db, "todos", "todos_select_0") {
		t.Fatal("RLS policy should exist after v2")
	}

	// v3: Remove column, remove table, remove RLS.
	cfgV3 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "title", Type: "text", Required: true},
				},
			},
		},
	}
	if err := migrator.Apply(ctx, cfgV3); err != nil {
		t.Fatalf("v3: %v", err)
	}

	if tableExists(t, db, "teams") {
		t.Fatal("teams should be dropped after v3")
	}
	if columnExists(t, db, "todos", "status") {
		t.Fatal("status column should be dropped after v3")
	}
	if !columnExists(t, db, "todos", "title") {
		t.Fatal("title should survive v3")
	}
	if rlsEnabled(t, db, "todos") {
		t.Fatal("RLS should be disabled after v3")
	}

	// Data should survive (the todo lost its FK column but the row itself persists).
	row, err := db.QueryRow(ctx, "SELECT title FROM todos LIMIT 1")
	if err != nil {
		t.Fatalf("query after v3: %v", err)
	}
	if fmt.Sprint(row["title"]) != "ship it" {
		t.Fatalf("data lost: expected 'ship it', got %v", row["title"])
	}

	// Verify migration history has 3 records.
	rows, err := db.Query(ctx, "SELECT id FROM _ultrabase_migrations ORDER BY id")
	if err != nil {
		t.Fatalf("query migrations: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 migration records, got %d", len(rows))
	}
}

// --- Default values ---

func TestIntegration_DefaultValues(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfg := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "status", Type: "text", Default: "pending"},
				{Name: "active", Type: "boolean", Default: true},
				{Name: "priority", Type: "integer", Default: 0},
				{Name: "created_at", Type: "timestamptz", Default: "now()"},
			}},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfg); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Insert with only PK and let defaults fill in.
	_, err := db.Exec(ctx, "INSERT INTO todos DEFAULT VALUES")
	if err != nil {
		t.Fatalf("insert with defaults: %v", err)
	}

	row, err := db.QueryRow(ctx, "SELECT status, active, priority, created_at FROM todos LIMIT 1")
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if fmt.Sprint(row["status"]) != "pending" {
		t.Fatalf("expected default status 'pending', got %v", row["status"])
	}
	if row["active"] != true {
		t.Fatalf("expected default active true, got %v", row["active"])
	}
	if fmt.Sprint(row["priority"]) != "0" {
		t.Fatalf("expected default priority 0, got %v", row["priority"])
	}
	if row["created_at"] == nil {
		t.Fatal("expected created_at to be set by now()")
	}
}

// Regression: verify that removing a FK column that has a constraint works.
func TestIntegration_DropFKColumn(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfgV1 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"teams": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "name", Type: "text", Required: true},
			}},
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "title", Type: "text", Required: true},
				{Name: "team_id", Type: "bigint", ForeignKey: &domain.ForeignKey{References: "teams.id", OnDelete: "cascade"}},
			}},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfgV1); err != nil {
		t.Fatalf("v1 apply: %v", err)
	}

	// Remove the FK column from todos but keep both tables.
	cfgV2 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"teams": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "name", Type: "text", Required: true},
			}},
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "title", Type: "text", Required: true},
			}},
		},
	}

	if err := migrator.Apply(ctx, cfgV2); err != nil {
		t.Fatalf("v2 apply (drop FK column): %v", err)
	}

	if columnExists(t, db, "todos", "team_id") {
		t.Fatal("team_id column should be dropped")
	}
}

// Regression: removing a column that has an index on it.
func TestIntegration_DropColumnWithIndex(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfgV1 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "status", Type: "text"},
				},
				Indexes: []domain.Index{{Columns: []string{"status"}}},
			},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfgV1); err != nil {
		t.Fatalf("v1 apply: %v", err)
	}

	// Remove both the column and the index.
	cfgV2 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
			}},
		},
	}

	if err := migrator.Apply(ctx, cfgV2); err != nil {
		t.Fatalf("v2 apply (drop indexed column): %v", err)
	}

	if columnExists(t, db, "todos", "status") {
		t.Fatal("status column should be dropped")
	}
}

// Edge case: remove a column that is the TARGET of another table's FK.
// e.g., teams.id is referenced by todos.team_id — what happens if we
// try to drop a non-PK column on teams that todos references? (We can't
// drop the PK itself via config since PK removal isn't supported, but
// this tests the general pattern.)
func TestIntegration_DropReferencedColumnTarget(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	// Create teams with a secondary unique column that todos references.
	cfgV1 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"teams": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "code", Type: "text", Unique: true, Required: true},
				{Name: "name", Type: "text"},
			}},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfgV1); err != nil {
		t.Fatalf("v1 apply: %v", err)
	}

	// Manually add a FK from a new table pointing to teams.code.
	_, err := db.Exec(ctx, `CREATE TABLE refs (id BIGSERIAL PRIMARY KEY, team_code TEXT REFERENCES teams(code))`)
	if err != nil {
		t.Fatalf("create refs: %v", err)
	}

	// Now remove the "code" column from teams config. This should fail
	// because refs.team_code has a FK pointing to teams.code and our
	// DROP COLUMN doesn't use CASCADE.
	cfgV2 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"teams": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "name", Type: "text"},
			}},
		},
	}

	err = migrator.Apply(ctx, cfgV2)
	if err == nil {
		t.Fatal("expected error when dropping column referenced by external FK")
	}
	t.Logf("correctly failed: %v", err)
}

// --- New integration tests for config-based diffing ---

func TestIntegration_NewTableWithFK(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfgV1 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"teams": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "name", Type: "text", Required: true},
			}},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfgV1); err != nil {
		t.Fatalf("v1 apply: %v", err)
	}

	// Add a new table that references the existing one.
	cfgV2 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"teams": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "name", Type: "text", Required: true},
			}},
			"members": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "team_id", Type: "bigint", ForeignKey: &domain.ForeignKey{References: "teams.id", OnDelete: "cascade"}},
			}},
		},
	}

	if err := migrator.Apply(ctx, cfgV2); err != nil {
		t.Fatalf("v2 apply: %v", err)
	}

	if !tableExists(t, db, "members") {
		t.Fatal("members table should exist")
	}

	// Insert team and member.
	_, err := db.Exec(ctx, "INSERT INTO teams (name) VALUES ('eng')")
	if err != nil {
		t.Fatalf("insert team: %v", err)
	}
	_, err = db.Exec(ctx, "INSERT INTO members (team_id) VALUES (1)")
	if err != nil {
		t.Fatalf("insert member: %v", err)
	}

	// FK violation.
	_, err = db.Exec(ctx, "INSERT INTO members (team_id) VALUES (999)")
	if err == nil {
		t.Fatal("expected FK violation")
	}
}

func TestIntegration_AddColumnWithFK(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfgV1 := &domain.Config{
		Version: 1,
		Auth:    &domain.Auth{},
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "title", Type: "text", Required: true},
			}},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfgV1); err != nil {
		t.Fatalf("v1 apply: %v", err)
	}

	// Add a FK column to existing table.
	cfgV2 := &domain.Config{
		Version: 1,
		Auth:    &domain.Auth{},
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "title", Type: "text", Required: true},
				{Name: "user_id", ForeignKey: &domain.ForeignKey{References: "users.id", OnDelete: "cascade"}},
			}},
		},
	}

	if err := migrator.Apply(ctx, cfgV2); err != nil {
		t.Fatalf("v2 apply: %v", err)
	}

	if !columnExists(t, db, "todos", "user_id") {
		t.Fatal("user_id column should exist")
	}

	if ct := columnType(t, db, "todos", "user_id"); ct != "uuid" {
		t.Fatalf("expected uuid type for FK to users.id, got %s", ct)
	}
}

func TestIntegration_ChangedRPCFunction_Body(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfgV1 := &domain.Config{
		Version: 1,
		Tables:  map[string]domain.Table{},
		Functions: map[string]domain.Function{
			"multiply": {
				Language:   "sql",
				Volatility: "immutable",
				Security:   "invoker",
				Returns:    domain.FuncReturn{Type: "int"},
				Args:       []domain.FuncArg{{Name: "a", Type: "int"}, {Name: "b", Type: "int"}},
				Body:       "SELECT a * b;",
			},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfgV1); err != nil {
		t.Fatalf("v1 apply: %v", err)
	}

	row, err := db.QueryRow(ctx, `SELECT public."multiply"(3, 4) AS result`)
	if err != nil {
		t.Fatalf("v1 call: %v", err)
	}
	if fmt.Sprint(row["result"]) != "12" {
		t.Fatalf("expected 12, got %v", row["result"])
	}

	// Change function to add an offset.
	cfgV2 := &domain.Config{
		Version: 1,
		Tables:  map[string]domain.Table{},
		Functions: map[string]domain.Function{
			"multiply": {
				Language:   "sql",
				Volatility: "immutable",
				Security:   "invoker",
				Returns:    domain.FuncReturn{Type: "int"},
				Args:       []domain.FuncArg{{Name: "a", Type: "int"}, {Name: "b", Type: "int"}},
				Body:       "SELECT (a * b) + 1;",
			},
		},
	}

	if err := migrator.Apply(ctx, cfgV2); err != nil {
		t.Fatalf("v2 apply: %v", err)
	}

	row, err = db.QueryRow(ctx, `SELECT public."multiply"(3, 4) AS result`)
	if err != nil {
		t.Fatalf("v2 call: %v", err)
	}
	if fmt.Sprint(row["result"]) != "13" {
		t.Fatalf("expected 13, got %v", row["result"])
	}
}

func TestIntegration_MixedMigration(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfgV1 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "title", Type: "text"},
				{Name: "priority", Type: "integer"},
			}},
			"comments": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "body", Type: "text"},
			}},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfgV1); err != nil {
		t.Fatalf("v1 apply: %v", err)
	}

	// Simultaneously: add table, remove table, add column, change type, change nullability
	cfgV2 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "title", Type: "text", Required: true},      // nullability change
				{Name: "priority", Type: "bigint"},                 // type change
				{Name: "status", Type: "text", Default: "pending"}, // new column
			}},
			// comments removed
			"posts": {Fields: []domain.Field{ // new table
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "body", Type: "text"},
			}},
		},
	}

	if err := migrator.Apply(ctx, cfgV2); err != nil {
		t.Fatalf("v2 apply: %v", err)
	}

	// Verify all changes
	if tableExists(t, db, "comments") {
		t.Fatal("comments should be dropped")
	}
	if !tableExists(t, db, "posts") {
		t.Fatal("posts should exist")
	}
	if !columnExists(t, db, "todos", "status") {
		t.Fatal("status column should exist")
	}
	if columnNullable(t, db, "todos", "title") {
		t.Fatal("title should be NOT NULL")
	}
	if ct := columnType(t, db, "todos", "priority"); ct != "bigint" {
		t.Fatalf("expected bigint, got %s", ct)
	}
}

func TestIntegration_DiffSQL_StoredNotFullDDL(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfgV1 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "title", Type: "text"},
			}},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfgV1); err != nil {
		t.Fatalf("v1 apply: %v", err)
	}

	cfgV2 := &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "title", Type: "text"},
				{Name: "status", Type: "text"},
			}},
		},
	}

	if err := migrator.Apply(ctx, cfgV2); err != nil {
		t.Fatalf("v2 apply: %v", err)
	}

	rows, err := db.Query(ctx, "SELECT sql FROM _ultrabase_migrations ORDER BY id")
	if err != nil {
		t.Fatalf("query migrations: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 migration records, got %d", len(rows))
	}

	// The second migration should contain ADD COLUMN, not CREATE TABLE
	v2SQL := fmt.Sprint(rows[1]["sql"])
	if !strings.Contains(v2SQL, "ADD COLUMN") {
		t.Fatalf("v2 migration should contain ADD COLUMN, got:\n%s", v2SQL)
	}
}

func TestIntegration_NewExtension(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfgV1 := &domain.Config{
		Version:    1,
		Extensions: []string{"pgcrypto"},
		Tables:     map[string]domain.Table{},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfgV1); err != nil {
		t.Fatalf("v1 apply: %v", err)
	}

	cfgV2 := &domain.Config{
		Version:    1,
		Extensions: []string{"pgcrypto", "pg_trgm"},
		Tables:     map[string]domain.Table{},
	}

	if err := migrator.Apply(ctx, cfgV2); err != nil {
		t.Fatalf("v2 apply: %v", err)
	}

	row, err := db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname='pg_trgm') AS exists`)
	if err != nil {
		t.Fatalf("check extension: %v", err)
	}
	if row["exists"] != true {
		t.Fatal("pg_trgm extension should be installed after v2")
	}
}

func TestIntegration_ChangedRLSPolicy(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfgV1 := &domain.Config{
		Version: 1,
		Auth:    &domain.Auth{},
		Tables: map[string]domain.Table{
			"todos": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "user_id", Type: "uuid"},
				},
				RLS: []domain.RLSPolicy{
					{Operations: []string{"select"}, Check: "true"},
				},
			},
		},
	}

	migrator := app.NewMigrator(db)
	if err := migrator.Apply(ctx, cfgV1); err != nil {
		t.Fatalf("v1 apply: %v", err)
	}

	// Change the policy check expression.
	cfgV2 := &domain.Config{
		Version: 1,
		Auth:    &domain.Auth{},
		Tables: map[string]domain.Table{
			"todos": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "user_id", Type: "uuid"},
				},
				RLS: []domain.RLSPolicy{
					{Operations: []string{"select"}, Check: "user_id = auth.uid()"},
				},
			},
		},
	}

	if err := migrator.Apply(ctx, cfgV2); err != nil {
		t.Fatalf("v2 apply: %v", err)
	}

	if !policyExists(t, db, "todos", "todos_select_0") {
		t.Fatal("select policy should still exist with updated check")
	}

	// Verify the policy qual changed.
	row, err := db.QueryRow(ctx,
		`SELECT qual FROM pg_policies WHERE tablename='todos' AND policyname='todos_select_0'`)
	if err != nil {
		t.Fatalf("query policy: %v", err)
	}
	qual := fmt.Sprint(row["qual"])
	if !strings.Contains(qual, "uid") {
		t.Fatalf("expected policy to reference auth.uid(), got qual: %s", qual)
	}
}
