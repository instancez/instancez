//go:build integration

package app_test

import (
	"context"
	"sync"
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

func holdTableLock(t *testing.T, db domain.Database, mode string) domain.Tx {
	t.Helper()
	tx, err := db.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), `LOCK TABLE storage.objects IN `+mode+` MODE`); err != nil {
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
	if err := m.LockTimeout(0).StorageHealLimits(300*time.Millisecond, 1<<30).Harden(ctx, cfg); err != nil {
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

func seedThreeObjects(t *testing.T, db domain.Database) {
	t.Helper()
	for _, n := range []string{"a", "b", "c"} {
		if _, err := db.Exec(context.Background(), `INSERT INTO storage.objects (bucket_id, name) VALUES ('b', $1)`, n); err != nil {
			t.Fatal(err)
		}
	}
}

// One page of heap is 8192 bytes; reltuples stays -1 because the table is never analyzed.
func TestIntegration_HardenStorageListHealGatesOnRelationSize(t *testing.T) {
	owner, _ := dbboot.StartContainer(t)
	ctx := context.Background()
	cfg := &domain.Config{Version: 1, Auth: &domain.Auth{}, Storage: map[string]domain.Bucket{"b": {}}}
	m := app.NewMigrator(owner)
	if err := m.Apply(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	seedThreeObjects(t, owner)
	dropListHeal(t, owner)

	row, err := owner.QueryRow(ctx, `SELECT reltuples::bigint AS n, pg_relation_size('storage.objects') AS bytes FROM pg_class WHERE oid = 'storage.objects'::regclass`)
	if err != nil || row["n"].(int64) > 0 || row["bytes"].(int64) != 8192 {
		t.Fatalf("setup: want an unanalyzed one-page table, got %v (%v)", row, err)
	}

	if err := m.StorageHealLimits(8*time.Second, 8191).Harden(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if col, _ := storageListShape(t, owner); col {
		t.Fatal("column must be skipped above the size gate even when reltuples is -1")
	}
	if nameIndexExists(t, owner) {
		t.Fatal("name index must be skipped above the size gate too")
	}

	if err := m.StorageHealLimits(8*time.Second, 8192).Harden(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if col, idx := storageListShape(t, owner); !col || idx != 2 {
		t.Fatalf("at the gate the heal runs: column=%v indexes=%d", col, idx)
	}
}

// diffNewStorage and the fresh plan share the storage DDL, so it must never rewrite an existing table.
func TestIntegration_ProvisionIdempotentNeverAddsTheColumn(t *testing.T) {
	owner, _ := dbboot.StartContainer(t)
	ctx := context.Background()
	cfg := &domain.Config{Version: 1, Auth: &domain.Auth{}, Storage: map[string]domain.Bucket{"b": {}}}
	m := app.NewMigrator(owner).StorageHealLimits(50*time.Millisecond, 2)
	if err := m.Apply(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	seedThreeObjects(t, owner)
	dropListHeal(t, owner)

	if err := m.ProvisionIdempotent(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if col, _ := storageListShape(t, owner); col {
		t.Fatal("ProvisionIdempotent added name_lower to a populated table")
	}
	if nameIndexExists(t, owner) {
		t.Fatal("ProvisionIdempotent built an index on a populated table")
	}
}

func TestIntegration_ApplyIndexesAnEmptyLegacyTable(t *testing.T) {
	owner, _ := dbboot.StartContainer(t)
	ctx := context.Background()
	cfg := &domain.Config{Version: 1, Auth: &domain.Auth{}, Storage: map[string]domain.Bucket{"b": {}}}
	m := app.NewMigrator(owner)
	if err := m.Apply(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	dropListHeal(t, owner)
	if err := m.ProvisionIdempotent(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if !nameIndexExists(t, owner) {
		t.Fatal("an empty table is indexed for free")
	}
}

func TestIntegration_HardenRebuildsAnInvalidListIndex(t *testing.T) {
	for _, idx := range []string{"objects_bucket_name_c_idx", "objects_bucket_name_lower_c_idx"} {
		t.Run(idx, func(t *testing.T) {
			owner, _ := dbboot.StartContainer(t)
			ctx := context.Background()
			cfg := &domain.Config{Version: 1, Auth: &domain.Auth{}, Storage: map[string]domain.Bucket{"b": {}}}
			m := app.NewMigrator(owner)
			if err := m.Apply(ctx, cfg); err != nil {
				t.Fatal(err)
			}
			seedObjects := `INSERT INTO storage.objects (bucket_id, name) VALUES ('b', 'x')`
			if _, err := owner.Exec(ctx, seedObjects); err != nil {
				t.Fatal(err)
			}
			if _, err := owner.Exec(ctx, `DROP INDEX storage.`+idx); err != nil {
				t.Fatal(err)
			}
			cols := map[string]string{"objects_bucket_name_c_idx": `name COLLATE "C"`, "objects_bucket_name_lower_c_idx": `name_lower`}[idx]
			_, err := owner.Exec(ctx, `CREATE UNIQUE INDEX CONCURRENTLY `+idx+` ON storage.objects (bucket_id, `+cols+`, (1 / (length(name) - 1)))`)
			if err == nil {
				t.Fatal("setup: the build should fail and leave an INVALID index")
			}
			if err := m.Harden(ctx, cfg); err != nil {
				t.Fatal(err)
			}
			row, err := owner.QueryRow(ctx, `SELECT count(*) AS n, bool_and(indisvalid) AS ok FROM pg_index WHERE indexrelid = 'storage.`+idx+`'::regclass`)
			if err != nil || row["n"].(int64) != 1 || row["ok"] != true {
				t.Fatalf("invalid index not rebuilt: %v (%v)", row, err)
			}
		})
	}
}

func TestIntegration_HardenStorageNameIndexHasAStatementTimeout(t *testing.T) {
	owner, _ := dbboot.StartContainer(t)
	ctx := context.Background()
	cfg := &domain.Config{Version: 1, Auth: &domain.Auth{}, Storage: map[string]domain.Bucket{"b": {}}}
	m := app.NewMigrator(owner)
	if err := m.Apply(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	dropListHeal(t, owner)
	tx := holdTableLock(t, owner, "ROW EXCLUSIVE")
	defer func() { _ = tx.Rollback(ctx) }()

	start := time.Now()
	if err := m.LockTimeout(0).StorageHealLimits(300*time.Millisecond, 1<<30).Harden(ctx, cfg); err != nil {
		t.Fatalf("a timed-out index build must not fail boot: %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("index build was not bounded by its statement timeout: %v", d)
	}
	if nameIndexExists(t, owner) {
		t.Fatal("index built despite the conflicting lock")
	}
}

func TestIntegration_HardenStorageListHealConcurrentBootsStayBounded(t *testing.T) {
	url := dbboot.StartRawContainer(t)
	ctx := context.Background()
	owner, _, err := dbboot.Bootstrap(ctx, url, domain.PoolConfig{Max: 12, Min: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	cfg := &domain.Config{Version: 1, Auth: &domain.Auth{}, Storage: map[string]domain.Bucket{"b": {}}}
	if err := app.NewMigrator(owner).Apply(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	dropListHeal(t, owner)
	tx := holdTableLock(t, owner, "ROW EXCLUSIVE")
	defer func() { _ = tx.Rollback(ctx) }()

	const stmt, lockWait, boots = 400 * time.Millisecond, 300 * time.Millisecond, 4
	var wg sync.WaitGroup
	elapsed := make([]time.Duration, boots)
	errs := make([]error, boots)
	for i := range boots {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			errs[i] = app.NewMigrator(owner).LockTimeout(lockWait).StorageHealLimits(stmt, 1<<30).Harden(ctx, cfg)
			elapsed[i] = time.Since(start)
		}()
	}
	wg.Wait()

	var slowest time.Duration
	for i := range boots {
		if errs[i] != nil {
			t.Fatalf("boot %d: %v", i, errs[i])
		}
		slowest = max(slowest, elapsed[i])
	}
	t.Logf("slowest boot %v (2T + lock wait = %v)", slowest, 2*stmt+lockWait)
	if limit := 2*stmt + lockWait + time.Second; slowest > limit {
		t.Fatalf("slowest boot %v exceeds 2T + lock wait + margin = %v", slowest, limit)
	}
}
