//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/instancez/instancez/internal/adapter/postgres"
	"github.com/instancez/instancez/internal/testutil/dbboot"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestRunSQL(t *testing.T) {
	owner, _ := dbboot.StartContainer(t)
	ctx := context.Background()
	db := owner.Database.(*postgres.DB)

	t.Run("select", func(t *testing.T) {
		cols, rows, err := db.RunSQL(ctx, "SELECT 1 AS a, 'x' AS b", false, 1000)
		if err != nil {
			t.Fatalf("RunSQL: %v", err)
		}
		if len(cols) != 2 || cols[0] != "a" || cols[1] != "b" {
			t.Fatalf("cols = %v, want [a b]", cols)
		}
		if len(rows) != 1 || rows[0][0] != "1" || rows[0][1] != "x" {
			t.Fatalf("rows = %v, want [[1 x]]", rows)
		}
	})

	t.Run("null", func(t *testing.T) {
		_, rows, err := db.RunSQL(ctx, "SELECT NULL::text AS a", false, 1000)
		if err != nil {
			t.Fatalf("RunSQL: %v", err)
		}
		if len(rows) != 1 || rows[0][0] != nil {
			t.Fatalf("rows = %v, want [[nil]]", rows)
		}
	})

	t.Run("multi_statement_last_result_wins", func(t *testing.T) {
		_, rows, err := db.RunSQL(ctx,
			"CREATE TEMP TABLE t(x int); INSERT INTO t VALUES (1),(2); SELECT x FROM t ORDER BY x",
			false, 1000)
		if err != nil {
			t.Fatalf("RunSQL: %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("rows = %v, want 2 rows", rows)
		}
	})

	t.Run("read_only_rejects_writes", func(t *testing.T) {
		_, _, err := db.RunSQL(ctx, "CREATE TABLE ro_test(x int)", true, 1000)
		if err == nil {
			t.Fatal("expected error for write in read-only tx")
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			if pgErr.Code != "25006" {
				t.Fatalf("SQLSTATE = %s, want 25006", pgErr.Code)
			}
		}
	})

	t.Run("read_only_bypass_blocked", func(t *testing.T) {
		_, _, err := db.RunSQL(ctx, "COMMIT; CREATE TABLE ro_bypass(x int)", true, 1000)
		if err == nil {
			t.Fatal("expected error for multi-statement buffer in read-only mode")
		}
		_, rows, err := db.RunSQL(ctx, "SELECT to_regclass('ro_bypass')", false, 1000)
		if err != nil {
			t.Fatalf("RunSQL: %v", err)
		}
		if len(rows) != 1 || rows[0][0] != nil {
			t.Fatalf("rows = %v, want [[nil]] (ro_bypass must not exist)", rows)
		}
	})

	t.Run("read_only_single_select", func(t *testing.T) {
		cols, rows, err := db.RunSQL(ctx, "SELECT 1 AS a", true, 1000)
		if err != nil {
			t.Fatalf("RunSQL: %v", err)
		}
		if len(cols) != 1 || cols[0] != "a" {
			t.Fatalf("cols = %v, want [a]", cols)
		}
		if len(rows) != 1 {
			t.Fatalf("rows = %v, want 1 row", rows)
		}
	})

	t.Run("read_only_rejects_single_write_statement", func(t *testing.T) {
		_, _, err := db.RunSQL(ctx, "CREATE TABLE ro_single(x int)", true, 1000)
		if err == nil {
			t.Fatal("expected error for write in read-only tx")
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			if pgErr.Code != "25006" {
				t.Fatalf("SQLSTATE = %s, want 25006", pgErr.Code)
			}
		}
	})

	t.Run("rollback_on_mid_buffer_error", func(t *testing.T) {
		_, _, err := db.RunSQL(ctx, "CREATE TABLE rb(x int); SELECT bad_col", false, 1000)
		if err == nil {
			t.Fatal("expected error from bad_col")
		}
		_, _, err = db.RunSQL(ctx, "SELECT * FROM rb", false, 1000)
		if err == nil {
			t.Fatal("expected rb to not exist after rollback")
		}
	})

	t.Run("cap", func(t *testing.T) {
		_, rows, err := db.RunSQL(ctx, "SELECT generate_series(1,5000) AS n", false, 1000)
		if err != nil {
			t.Fatalf("RunSQL: %v", err)
		}
		if len(rows) != 1000 {
			t.Fatalf("rows = %d, want 1000", len(rows))
		}
	})

	t.Run("statement_timeout", func(t *testing.T) {
		restore := postgres.SetSQLEditorStatementTimeoutForTest("150ms")
		defer restore()
		_, _, err := db.RunSQL(ctx, "SELECT pg_sleep(2)", false, 1000)
		if err == nil {
			t.Fatal("expected statement_timeout cancellation, got nil")
		}
	})

	t.Run("readwrite_values_are_text", func(t *testing.T) {
		_, rows, err := db.RunSQL(ctx, "SELECT true AS b, 7 AS n", false, 1000)
		if err != nil {
			t.Fatalf("RunSQL: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("rows = %v, want 1 row", rows)
		}
		if b, ok := rows[0][0].(string); !ok || b != "t" {
			t.Fatalf("rows[0][0] = %#v, want string \"t\"", rows[0][0])
		}
		if n, ok := rows[0][1].(string); !ok || n != "7" {
			t.Fatalf("rows[0][1] = %#v, want string \"7\"", rows[0][1])
		}
	})

	t.Run("readonly_values_are_typed", func(t *testing.T) {
		_, rows, err := db.RunSQL(ctx, "SELECT true AS b", true, 1000)
		if err != nil {
			t.Fatalf("RunSQL: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("rows = %v, want 1 row", rows)
		}
		if reflect.TypeOf(rows[0][0]).Kind() != reflect.Bool {
			t.Fatalf("rows[0][0] = %#v (%T), want Go bool", rows[0][0], rows[0][0])
		}
		if b, ok := rows[0][0].(bool); !ok || !b {
			t.Fatalf("rows[0][0] = %#v, want true", rows[0][0])
		}
	})

	t.Run("read_only_rejects_data_modifying_cte", func(t *testing.T) {
		_, _, err := db.RunSQL(ctx, "CREATE TABLE cte_t(x int)", false, 1000)
		if err != nil {
			t.Fatalf("RunSQL (setup): %v", err)
		}
		_, _, err = db.RunSQL(ctx,
			"WITH ins AS (INSERT INTO cte_t VALUES (1) RETURNING x) SELECT * FROM ins", true, 1000)
		if err == nil {
			t.Fatal("expected read-only rejection of data-modifying CTE")
		}
	})
}
