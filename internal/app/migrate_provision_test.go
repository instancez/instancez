package app

import (
	"context"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

// Apply stays pure: a destructive change is rejected with ErrDestructive and
// runs NO DDL — provisioning is the engine's boot/reconcile fallback's job, not
// Apply's, so the interactive config-editor path (handlePutConfig) can reject a
// destructive edit without side effects.
func TestApplyRejectsDestructiveWithoutProvisioning(t *testing.T) {
	db := newFakeDB(t)
	m := NewMigrator(db, domain.DefaultRoles())

	cfg1 := &domain.Config{
		Tables: map[string]domain.Table{
			"posts": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "featured_image_url", Type: "text"},
			}},
		},
	}
	if err := m.Apply(context.Background(), cfg1); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	execsBefore := len(db.execs)

	// Drop featured_image_url (destructive) AND add a bucket.
	cfg2 := &domain.Config{
		Tables: map[string]domain.Table{
			"posts": {Fields: []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}}},
		},
		Storage: map[string]domain.Bucket{"blog_images": {Public: true}},
	}
	err := m.Apply(context.Background(), cfg2)
	if err == nil || !strings.Contains(err.Error(), "destructive") {
		t.Fatalf("expected destructive rejection, got: %v", err)
	}
	// Apply must not have executed any DDL for the rejected plan.
	if delta := strings.Join(db.execs[execsBefore:], "\n"); strings.TrimSpace(delta) != "" {
		t.Fatalf("Apply must run no DDL on a destructive plan, ran:\n%s", delta)
	}
}

// ProvisionIdempotent emits the additive, IF-NOT-EXISTS provisioning subset
// (storage schema/table + RLS) and records no migration row. It is what the
// engine runs when a destructive change blocks the real plan, so a configured
// bucket still gets its DB backing.
func TestProvisionIdempotentCreatesStorage(t *testing.T) {
	db := newFakeDB(t)
	m := NewMigrator(db, domain.DefaultRoles())

	cfg := &domain.Config{
		Storage: map[string]domain.Bucket{
			"blog_images": {Public: true, RLS: []domain.RLSPolicy{
				{Operations: []string{"select"}, Using: "true"},
			}},
		},
	}
	before := len(db.execs)
	if err := m.ProvisionIdempotent(context.Background(), cfg); err != nil {
		t.Fatalf("provision: %v", err)
	}
	joined := strings.Join(db.execs[before:], "\n")
	if !strings.Contains(joined, "CREATE SCHEMA IF NOT EXISTS storage") {
		t.Fatalf("storage schema not provisioned:\n%s", joined)
	}
	if !strings.Contains(joined, "storage.objects") {
		t.Fatalf("storage.objects not provisioned:\n%s", joined)
	}
	// Provisioning must not stamp a migration row (the config is not fully applied).
	if last, _ := db.GetLastMigration(context.Background()); last != nil {
		t.Fatalf("provisioning must not record a migration row, got %+v", last)
	}
}

// A purely additive edit (add a bucket, drop nothing) migrates normally through
// Apply's regular recorded path — the fallback provisioning is not involved.
func TestAdditiveStorageMigratesNormally(t *testing.T) {
	db := newFakeDB(t)
	m := NewMigrator(db, domain.DefaultRoles())

	cfg1 := &domain.Config{
		Tables: map[string]domain.Table{
			"posts": {Fields: []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}}},
		},
	}
	if err := m.Apply(context.Background(), cfg1); err != nil {
		t.Fatalf("first apply: %v", err)
	}

	cfg2 := &domain.Config{
		Tables:  cfg1.Tables,
		Storage: map[string]domain.Bucket{"blog_images": {Public: true}},
	}
	if err := m.Apply(context.Background(), cfg2); err != nil {
		t.Fatalf("additive storage apply: %v", err)
	}
	last, _ := db.GetLastMigration(context.Background())
	if last == nil {
		t.Fatalf("expected a recorded migration")
	}
}
