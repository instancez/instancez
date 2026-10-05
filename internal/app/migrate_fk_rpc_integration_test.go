//go:build integration

package app_test

import (
	"context"
	"testing"

	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
)

func uploadedByDelType(t *testing.T, db interface {
	QueryRow(context.Context, string, ...any) (map[string]any, error)
}) string {
	t.Helper()
	row, err := db.QueryRow(context.Background(), `SELECT string_agg(confdeltype::text, ',') AS t FROM pg_constraint
		WHERE conrelid = 'storage.objects'::regclass AND confrelid = 'auth.users'::regclass AND contype = 'f'`)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := row["t"].(string)
	return s
}

func TestIntegration_UploadedByFKUpgrade(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()
	cfg := &domain.Config{Version: 1, Auth: &domain.Auth{}, Storage: map[string]domain.Bucket{"b": {}}}
	m := app.NewMigrator(db)
	if err := m.Apply(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if got := uploadedByDelType(t, db); got != "n" {
		t.Fatalf("fresh FK confdeltype = %q, want n", got)
	}
	// Recreate the legacy constraint: no ON DELETE action, custom name.
	if _, err := db.Exec(ctx, `ALTER TABLE storage.objects DROP CONSTRAINT objects_uploaded_by_fkey;
		ALTER TABLE storage.objects ADD CONSTRAINT legacy_fk FOREIGN KEY (uploaded_by) REFERENCES auth.users(id)`); err != nil {
		t.Fatal(err)
	}
	if got := uploadedByDelType(t, db); got != "a" {
		t.Fatalf("legacy FK confdeltype = %q, want a", got)
	}
	for range 2 {
		if err := m.Harden(ctx, cfg); err != nil {
			t.Fatalf("harden: %v", err)
		}
		if got := uploadedByDelType(t, db); got != "n" {
			t.Fatalf("after harden confdeltype = %q, want exactly one FK with n", got)
		}
	}
}

func TestIntegration_UploadedByFKHealsTwoDuplicateFKs(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()
	cfg := &domain.Config{Version: 1, Auth: &domain.Auth{}, Storage: map[string]domain.Bucket{"b": {}}}
	m := app.NewMigrator(db)
	if err := m.Apply(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `ALTER TABLE storage.objects DROP CONSTRAINT objects_uploaded_by_fkey;
		ALTER TABLE storage.objects ADD CONSTRAINT legacy_a FOREIGN KEY (uploaded_by) REFERENCES auth.users(id);
		ALTER TABLE storage.objects ADD CONSTRAINT legacy_b FOREIGN KEY (uploaded_by) REFERENCES auth.users(id)`); err != nil {
		t.Fatal(err)
	}
	if err := m.Harden(ctx, cfg); err != nil {
		t.Fatalf("harden: %v", err)
	}
	if got := uploadedByDelType(t, db); got != "n" {
		t.Fatalf("after harden confdeltype = %q, want a single FK with n", got)
	}
}

func TestIntegration_HardenWithoutStorageIsNoop(t *testing.T) {
	db := startPostgres(t)
	if err := app.NewMigrator(db).Harden(context.Background(), &domain.Config{Version: 1}); err != nil {
		t.Fatalf("harden on empty db: %v", err)
	}
}

func TestIntegration_SQLRPCReferencingLaterTable(t *testing.T) {
	db := startPostgres(t)
	cfg := &domain.Config{Version: 1,
		Tables: map[string]domain.Table{"zebra": {Fields: []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}}}},
		RPC: map[string]domain.Function{"count_zebra": {
			Language: "sql", Volatility: "stable", Security: "invoker",
			Returns: domain.FuncReturn{Type: "bigint"},
			Body:    "SELECT count(*) FROM public.zebra",
		}}}
	m := app.NewMigrator(db)
	if err := m.Apply(context.Background(), cfg); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !functionExists(t, db, "count_zebra") {
		t.Fatal("rpc missing")
	}
}
