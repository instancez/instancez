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

func nameIndexExists(t *testing.T, db domain.Database) bool {
	t.Helper()
	row, err := db.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = 'storage' AND tablename = 'objects' AND indexname = 'objects_bucket_name_c_idx') AS ok`)
	if err != nil {
		t.Fatal(err)
	}
	return row["ok"].(bool)
}

// dropListHeal leaves a pre-heal database: no column, no list indexes.
func dropListHeal(t *testing.T, db domain.Database) {
	t.Helper()
	if _, err := db.Exec(context.Background(), `ALTER TABLE storage.objects DROP COLUMN name_lower; DROP INDEX storage.objects_bucket_name_c_idx`); err != nil {
		t.Fatal(err)
	}
}

func holdAccessShare(t *testing.T, db domain.Database) domain.Tx {
	t.Helper()
	tx, err := db.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), `LOCK TABLE storage.objects IN ACCESS SHARE MODE`); err != nil {
		t.Fatal(err)
	}
	return tx
}

func TestIntegration_HardenStorageListHealIsBestEffort(t *testing.T) {
	owner, _ := dbboot.StartContainer(t)
	ctx := context.Background()
	cfg := &domain.Config{Version: 1, Auth: &domain.Auth{}, Storage: map[string]domain.Bucket{"b": {}}}
	m := app.NewMigrator(owner)
	if err := m.Apply(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	dropListHeal(t, owner)
	tx := holdAccessShare(t, owner)

	if err := m.LockTimeout(100*time.Millisecond).Harden(ctx, cfg); err != nil {
		t.Fatalf("a busy table must not fail boot: %v", err)
	}
	if col, _ := storageListShape(t, owner); col {
		t.Fatal("column added despite the lock")
	}
	if !nameIndexExists(t, owner) {
		t.Fatal("name index must survive a failed column step")
	}

	_ = tx.Rollback(ctx)
	if err := m.Harden(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if col, idx := storageListShape(t, owner); !col || idx != 2 {
		t.Fatalf("next boot: column=%v indexes=%d", col, idx)
	}
}

func TestIntegration_HardenStorageListHealTimesOutWithoutFailingBoot(t *testing.T) {
	owner, _ := dbboot.StartContainer(t)
	ctx := context.Background()
	cfg := &domain.Config{Version: 1, Auth: &domain.Auth{}, Storage: map[string]domain.Bucket{"b": {}}}
	m := app.NewMigrator(owner)
	if err := m.Apply(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	dropListHeal(t, owner)
	tx := holdAccessShare(t, owner)
	defer func() { _ = tx.Rollback(ctx) }()

	start := time.Now()
	if err := m.LockTimeout(0).StorageHealLimits(300*time.Millisecond, 500_000).Harden(ctx, cfg); err != nil {
		t.Fatalf("a timed-out heal must not fail boot: %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("heal was not bounded by its statement timeout: %v", d)
	}
	if col, _ := storageListShape(t, owner); col {
		t.Fatal("column added despite the lock")
	}
	if !nameIndexExists(t, owner) {
		t.Fatal("name index must survive a timed-out column step")
	}
}

func TestIntegration_HardenStorageListHealSkipsColumnOnLargeTables(t *testing.T) {
	owner, _ := dbboot.StartContainer(t)
	ctx := context.Background()
	cfg := &domain.Config{Version: 1, Auth: &domain.Auth{}, Storage: map[string]domain.Bucket{"b": {}}}
	m := app.NewMigrator(owner)
	if err := m.Apply(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a", "b", "c"} {
		if _, err := owner.Exec(ctx, `INSERT INTO storage.objects (bucket_id, name) VALUES ('b', $1)`, n); err != nil {
			t.Fatal(err)
		}
	}
	dropListHeal(t, owner)
	if _, err := owner.Exec(ctx, `ANALYZE storage.objects`); err != nil {
		t.Fatal(err)
	}

	if err := m.StorageHealLimits(20*time.Second, 2).Harden(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if col, _ := storageListShape(t, owner); col {
		t.Fatal("column must be skipped above the row gate")
	}
	if !nameIndexExists(t, owner) {
		t.Fatal("name index is cheap and must still be built")
	}

	if err := m.StorageHealLimits(20*time.Second, 3).Harden(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if col, idx := storageListShape(t, owner); !col || idx != 2 {
		t.Fatalf("at the gate the heal runs: column=%v indexes=%d", col, idx)
	}
}
