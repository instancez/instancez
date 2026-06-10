//go:build integration

package app_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pgadapter "github.com/saedx1/instancez/internal/adapter/postgres"
	"github.com/saedx1/instancez/internal/app"
	"github.com/saedx1/instancez/internal/config"
	"github.com/saedx1/instancez/internal/testutil/dbboot"
)

// startExamplesPostgres spins up a postgres:15-alpine container (image cached locally).
func startExamplesPostgres(t *testing.T) *pgadapter.DB {
	t.Helper()
	owner, _ := dbboot.StartContainer(t, "postgres:15-alpine")
	return owner.Database.(*pgadapter.DB)
}

// resetSchema drops all user schemas and recreates public, giving each subtest
// a clean slate without restarting the container.
const resetSchemaSQL = `
DO $$
DECLARE r RECORD;
BEGIN
  FOR r IN
    SELECT nspname FROM pg_namespace
    WHERE nspname NOT IN ('pg_catalog','pg_toast','information_schema')
      AND nspname NOT LIKE 'pg_%'
  LOOP
    EXECUTE 'DROP SCHEMA IF EXISTS ' || quote_ident(r.nspname) || ' CASCADE';
  END LOOP;
END $$;
CREATE SCHEMA public;
`

// TestExampleConfigMigrations loads every YAML in prompts/examples/, runs the
// migrator against a shared Postgres container (schema reset between each), and
// reports any DDL failures — errors the YAML validator cannot catch.
func TestExampleConfigMigrations(t *testing.T) {
	examplesDir := filepath.Join("..", "..", "prompts", "examples")

	entries, err := os.ReadDir(examplesDir)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skip("prompts/examples directory not found")
		}
		t.Fatalf("read examples dir: %v", err)
	}

	var yamlFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yaml") {
			yamlFiles = append(yamlFiles, filepath.Join(examplesDir, e.Name()))
		}
	}
	if len(yamlFiles) == 0 {
		t.Skip("no example configs found")
	}

	db := startExamplesPostgres(t)

	for _, cfgPath := range yamlFiles {
		cfgPath := cfgPath
		name := strings.TrimSuffix(filepath.Base(cfgPath), ".yaml")

		t.Run(name, func(t *testing.T) {
			ctx := context.Background()

			if err := db.ExecDDL(ctx, resetSchemaSQL); err != nil {
				t.Fatalf("reset schema: %v", err)
			}

			cfg, err := config.Load(cfgPath)
			if err != nil {
				t.Skipf("skipped — config load error (likely unset env vars): %v", err)
				return
			}

			migrator := app.NewMigrator(db)
			if err := migrator.Apply(ctx, cfg); err != nil {
				// Dump the generated DDL so failures are actionable
				if planSQL, planErr := migrator.Plan(ctx, nil, cfg); planErr == nil {
					t.Logf("generated DDL:\n%s", planSQL)
				}
				t.Fatalf("migration failed: %v", err)
			}

			t.Logf("ok — %d tables, %d rpc, %d buckets",
				len(cfg.Tables), len(cfg.RPC), len(cfg.Storage))
		})
	}
}
