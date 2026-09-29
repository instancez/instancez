//go:build integration

package app_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
)

func TestIntegration_RPCSetAppliedChangedAndCleared(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()
	m := app.NewMigrator(db).AllowDestructive(true)
	fn := domain.Function{Language: "sql", Volatility: "stable", Security: "definer", Returns: domain.FuncReturn{Type: "text"},
		Body: "SELECT current_setting('search_path')"}
	proconfig := func() string {
		row, err := db.QueryRow(ctx, `SELECT coalesce(array_to_string(proconfig, ';'), '') AS c FROM pg_proc WHERE proname = 'sp'`)
		if err != nil {
			t.Fatal(err)
		}
		return fmt.Sprint(row["c"])
	}
	for _, c := range []struct {
		set  map[string]string
		want string
	}{
		{map[string]string{"search_path": "", "statement_timeout": "5s"}, `search_path="";statement_timeout=5s`},
		{map[string]string{"search_path": "public, pg_temp"}, "search_path=public, pg_temp"},
		{nil, ""},
	} {
		fn.Set = c.set
		if err := m.Apply(ctx, &domain.Config{Version: 1, RPC: map[string]domain.Function{"sp": fn}}); err != nil {
			t.Fatalf("%v: %v", c.set, err)
		}
		if got := proconfig(); got != c.want {
			t.Fatalf("%v: proconfig %q, want %q", c.set, got, c.want)
		}
	}
	fn.Set = map[string]string{"search_path": "pg_temp"}
	if err := m.Apply(ctx, &domain.Config{Version: 1, RPC: map[string]domain.Function{"sp": fn}}); err != nil {
		t.Fatal(err)
	}
	row, err := db.QueryRow(ctx, `SELECT public.sp() AS v`)
	if err != nil || fmt.Sprint(row["v"]) != "pg_temp" {
		t.Fatalf("function must run with its pinned search_path: %v %v", row, err)
	}
}
