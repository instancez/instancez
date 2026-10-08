//go:build integration

package app_test

import (
	"context"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/config"
	"github.com/instancez/instancez/internal/domain"
)

// The early language-sql body check mirrors Postgres: transaction control creates fine and fails every call,
// and the commands the check lets through are accepted at CREATE time.
func TestIntegration_SQLBodyCheckMatchesPostgres(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()
	create := func(body string) error {
		_, err := db.Exec(ctx, `CREATE OR REPLACE FUNCTION public.conf_fn() RETURNS void LANGUAGE sql AS $ub$`+body+`$ub$`)
		return err
	}
	validates := func(body string) bool {
		cfg := &domain.Config{Version: 1, Project: domain.Project{Name: "t"}, RPC: map[string]domain.Function{"conf_fn": {
			Language: "sql", Volatility: "volatile", Security: "invoker", Returns: domain.FuncReturn{Type: "void"}, Body: body,
		}}}
		for _, e := range config.Validate(cfg) {
			if strings.HasSuffix(e.Path, ".body") {
				return false
			}
		}
		return true
	}

	for _, body := range []string{"BEGIN", "BEGIN; SELECT 1", "-- c\rCOMMIT", "PREPARE /* x */ TRANSACTION 'x'", "COMMIT", "END", "ROLLBACK", "ABORT", "START TRANSACTION", "SAVEPOINT s", "RELEASE SAVEPOINT s", "PREPARE TRANSACTION 'x'"} {
		if validates(body) {
			t.Errorf("%q passes validation but Postgres refuses it inside a SQL function", body)
		}
		if err := create(body); err != nil {
			t.Errorf("%q: Postgres should accept it at CREATE time: %v", body, err)
			continue
		}
		if _, err := db.Exec(ctx, `SELECT public.conf_fn()`); err == nil || !strings.Contains(err.Error(), "is not allowed in an SQL function") {
			t.Errorf("%q: call should fail with the transaction-control error, got %v", body, err)
		}
	}

	for _, body := range []string{
		"SELECT 1", "(SELECT 1)", "WITH x AS (SELECT 1) SELECT 1", "VALUES (1)", "SET LOCAL work_mem = '4MB'", "RESET work_mem", "SHOW work_mem",
		"EXPLAIN SELECT 1", "DO $$ BEGIN NULL; END $$", "NOTIFY ch", "LISTEN ch", "CREATE TEMP TABLE conf_t(a int)", "-- c\nSELECT 1",
		"/* a /* nested */ b */ SELECT 1", "; SELECT 1", "PREPARE p AS SELECT 1", "DECLARE c CURSOR FOR SELECT 1", "CHECKPOINT",
		"ANALYSE", "-- c\rSELECT 1", "-- c\r\nSELECT 1", "SELECT 1; COMMIT",
	} {
		if !validates(body) {
			t.Errorf("%q: validation rejects a body Postgres accepts", body)
		}
		if err := create(body); err != nil {
			t.Errorf("%q: Postgres rejected it at CREATE time: %v", body, err)
		}
	}
}
