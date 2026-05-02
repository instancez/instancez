//go:build integration

package app_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tc "github.com/testcontainers/testcontainers-go"
	pgcontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	pgadapter "github.com/saedx1/ultrabase/internal/adapter/postgres"
	"github.com/saedx1/ultrabase/internal/app"
	"github.com/saedx1/ultrabase/internal/config"
	"github.com/saedx1/ultrabase/internal/domain"
)

// startExamplesPostgres spins up a postgres:15-alpine container (image cached locally).
func startExamplesPostgres(t *testing.T) *pgadapter.DB {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	container, err := pgcontainer.Run(ctx,
		"postgres:15-alpine",
		pgcontainer.WithDatabase("ultrabase_test"),
		pgcontainer.WithUsername("ultrabase"),
		pgcontainer.WithPassword("ultrabase"),
		tc.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	connStr, err := container.ConnectionString(context.Background(), "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	db, err := pgadapter.New(context.Background(), connStr, domain.PoolConfig{Max: 4, Min: 1})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	return db
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

			t.Logf("ok — %d tables, %d functions, %d buckets",
				len(cfg.Tables), len(cfg.Functions), len(cfg.Storage))
		})
	}
}
