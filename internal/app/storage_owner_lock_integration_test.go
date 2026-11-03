//go:build integration

package app

import (
	"context"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
	"github.com/instancez/instancez/internal/testutil/dbboot"
)

func TestIntegration_StorageOwnerHealTakesNoLockOnceHealed(t *testing.T) {
	owner, _ := dbboot.StartContainer(t)
	ctx := context.Background()
	cfg := &domain.Config{Version: 1, Auth: &domain.Auth{}, Storage: map[string]domain.Bucket{
		"b": {RLS: []domain.RLSPolicy{{Operations: []string{"select"}, Using: "owner = auth.uid()"}}},
	}}
	if err := NewMigrator(owner).Apply(ctx, cfg); err != nil {
		t.Fatalf("apply: %v", err)
	}
	filenode := func() int64 {
		rows, err := owner.Query(ctx, "SELECT relfilenode::bigint AS f FROM pg_class WHERE oid = 'storage.objects'::regclass")
		if err != nil {
			t.Fatal(err)
		}
		return rows[0]["f"].(int64)
	}
	before := filenode()

	// A reader holding ACCESS SHARE blocks anything that wants ACCESS EXCLUSIVE.
	reader, err := owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Rollback(ctx) }()
	if _, err := reader.Exec(ctx, "LOCK TABLE storage.objects IN ACCESS SHARE MODE"); err != nil {
		t.Fatal(err)
	}

	run := func(stmt string) error {
		tx, err := owner.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout = '300ms'"); err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx, stmt)
		return err
	}

	// Control: the unguarded form queues for ACCESS EXCLUSIVE even though the column exists.
	if err := run("ALTER TABLE storage.objects ADD COLUMN IF NOT EXISTS owner UUID GENERATED ALWAYS AS (uploaded_by) STORED"); err == nil || !strings.Contains(err.Error(), "lock timeout") {
		t.Fatalf("control should hit the lock timeout, got %v", err)
	}
	for _, stmt := range storageOwnerColumnDDL(cfg.Storage) {
		if err := run(stmt); err != nil {
			t.Fatalf("healed table must need no lock: %v", err)
		}
	}
	if after := filenode(); after != before {
		t.Fatalf("storage.objects was rewritten: relfilenode %d -> %d", before, after)
	}
}
