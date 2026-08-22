//go:build integration

package postgres_test

import (
	"context"
	"errors"
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
}
