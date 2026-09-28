//go:build integration

package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
	"github.com/instancez/instancez/internal/testutil/dbboot"
)

func storageListShape(t *testing.T, db domain.Database) (col bool, idx int) {
	t.Helper()
	row, err := db.QueryRow(context.Background(), `SELECT
  EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid = 'storage.objects'::regclass AND attname = 'name_lower' AND NOT attisdropped) AS col,
  (SELECT count(*) FROM pg_indexes WHERE schemaname = 'storage' AND tablename = 'objects' AND indexname IN ('objects_bucket_name_c_idx', 'objects_bucket_name_lower_c_idx')) AS idx`)
	if err != nil {
		t.Fatal(err)
	}
	return row["col"].(bool), int(row["idx"].(int64))
}

// A DB migrated before the list indexes existed never re-runs the plan, so only Harden can add them.
func TestIntegration_HardenAddsStorageListIndexes(t *testing.T) {
	cfg := &domain.Config{Version: 1, Auth: &domain.Auth{}, Storage: map[string]domain.Bucket{"b": {}}}
	for name, drop := range map[string]string{
		"indexes only":       `DROP INDEX storage.objects_bucket_name_c_idx; DROP INDEX storage.objects_bucket_name_lower_c_idx`,
		"column and indexes": `ALTER TABLE storage.objects DROP COLUMN name_lower; DROP INDEX storage.objects_bucket_name_c_idx`,
	} {
		t.Run(name, func(t *testing.T) {
			owner, _ := dbboot.StartContainer(t)
			ctx := context.Background()
			m := app.NewMigrator(owner)
			if err := m.Apply(ctx, cfg); err != nil {
				t.Fatal(err)
			}
			if col, idx := storageListShape(t, owner); !col || idx != 2 {
				t.Fatalf("fresh apply: column=%v indexes=%d", col, idx)
			}
			if _, err := owner.Exec(ctx, drop); err != nil {
				t.Fatal(err)
			}

			if err := m.Apply(ctx, cfg); err != nil {
				t.Fatal(err)
			}
			if err := m.Harden(ctx, cfg); err != nil {
				t.Fatalf("harden: %v", err)
			}
			if col, idx := storageListShape(t, owner); !col || idx != 2 {
				t.Fatalf("after harden: column=%v indexes=%d", col, idx)
			}
		})
	}
}

func TestIntegration_HardenStorageListHealRunsNoDDLWhenHealed(t *testing.T) {
	owner, _ := dbboot.StartContainer(t)
	ctx := context.Background()
	cfg := &domain.Config{Version: 1, Auth: &domain.Auth{}, Storage: map[string]domain.Bucket{"b": {}}}
	m := app.NewMigrator(owner)
	if err := m.Apply(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if err := m.Harden(ctx, cfg); err != nil {
		t.Fatal(err)
	}

	before := storageCatalogStamp(t, owner)
	if err := m.Harden(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if after := storageCatalogStamp(t, owner); after != before {
		t.Fatalf("healed db changed storage.objects catalog rows:\nbefore %s\nafter  %s", before, after)
	}
}

// DDL rewrites or inserts pg_class rows, which moves their xmin and relfilenode.
func storageCatalogStamp(t *testing.T, db domain.Database) string {
	t.Helper()
	row, err := db.QueryRow(context.Background(), `SELECT string_agg(oid || ':' || relfilenode || ':' || xmin::text, ',' ORDER BY relname) AS s
FROM pg_class WHERE oid = 'storage.objects'::regclass OR oid IN (SELECT indexrelid FROM pg_index WHERE indrelid = 'storage.objects'::regclass)`)
	if err != nil {
		t.Fatal(err)
	}
	return row["s"].(string)
}

func TestIntegration_HardenStorageListHealIsBestEffort(t *testing.T) {
	owner, _ := dbboot.StartContainer(t)
	ctx := context.Background()
	cfg := &domain.Config{Version: 1, Auth: &domain.Auth{}, Storage: map[string]domain.Bucket{"b": {}}}
	m := app.NewMigrator(owner)
	if err := m.Apply(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `ALTER TABLE storage.objects DROP COLUMN name_lower`); err != nil {
		t.Fatal(err)
	}
	tx, err := owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `LOCK TABLE storage.objects IN ACCESS SHARE MODE`); err != nil {
		t.Fatal(err)
	}

	if err := m.LockTimeout(100*time.Millisecond).Harden(ctx, cfg); err != nil {
		t.Fatalf("a busy table must not fail boot: %v", err)
	}
	if col, _ := storageListShape(t, owner); col {
		t.Fatal("column added despite the lock")
	}

	_ = tx.Rollback(ctx)
	if err := m.Harden(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if col, idx := storageListShape(t, owner); !col || idx != 2 {
		t.Fatalf("next boot: column=%v indexes=%d", col, idx)
	}
}
