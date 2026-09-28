//go:build integration

package app_test

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
	"github.com/instancez/instancez/internal/testutil/dbboot"
)

// legacyGrants reproduces what releases before this fix left on a live DB.
var legacyGrants = []string{
	"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA auth TO anon, authenticated, service_role",
	"GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA auth TO anon, authenticated",
	"ALTER DEFAULT PRIVILEGES IN SCHEMA auth GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO anon, authenticated",
	"GRANT SELECT ON _instancez_migrations TO anon, authenticated, service_role",
}

func applyLegacyGrants(t *testing.T, db domain.Database) {
	t.Helper()
	for _, s := range legacyGrants {
		if _, err := db.Exec(context.Background(), s); err != nil {
			t.Fatalf("legacy grant %q: %v", s, err)
		}
	}
	if !hasPriv(t, db, "anon", "auth.jwt_keys", "SELECT") {
		t.Fatal("setup: legacy grant did not land")
	}
}

func assertHardened(t *testing.T, db domain.Database) {
	t.Helper()
	for _, role := range []string{"anon", "authenticated"} {
		for _, fqn := range []string{"auth.jwt_keys", "auth.users", "auth.refresh_tokens", "_instancez_migrations"} {
			if hasPriv(t, db, role, fqn, "SELECT") {
				t.Errorf("%s still has SELECT on %s", role, fqn)
			}
		}
	}
	if hasPriv(t, db, "service_role", "auth.jwt_keys", "SELECT") || hasPriv(t, db, "service_role", "_instancez_migrations", "SELECT") {
		t.Error("service_role still reads jwt_keys or migration history")
	}
	if !hasPriv(t, db, "service_role", "auth.users", "UPDATE") {
		t.Error("service_role lost DML on auth.users")
	}
	if n := authDefaultACLGrants(t, db); n != 0 {
		t.Errorf("auth default ACL still grants anon/authenticated (%d)", n)
	}
}

// Upgrade path: unchanged YAML means Apply is a no-op, so only Harden can fix the DB.
func TestHarden_FixesLegacyGrantsAndIsConcurrencySafe(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()
	cfg := &domain.Config{Version: 1, Auth: &domain.Auth{}}
	m := app.NewMigrator(db)
	if err := m.Apply(ctx, cfg); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	applyLegacyGrants(t, db)
	if err := m.Apply(ctx, cfg); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	if !hasPriv(t, db, "anon", "auth.jwt_keys", "SELECT") {
		t.Fatal("precondition: unchanged-config Apply should not touch grants")
	}

	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- m.Harden(ctx, cfg)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Harden: %v", err)
		}
	}
	assertHardened(t, db)
}

// Harden must not fail boot on a DB that has never been migrated.
func TestHarden_FreshDatabase(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()
	m := app.NewMigrator(db)
	for i := 0; i < 2; i++ {
		if err := m.Harden(ctx, nil); err != nil {
			t.Fatalf("Harden #%d on fresh DB: %v", i+1, err)
		}
	}
	for _, fqn := range []string{"auth.jwt_keys", "_instancez_migrations"} {
		if hasPriv(t, db, "anon", fqn, "SELECT") || hasPriv(t, db, "service_role", fqn, "SELECT") {
			t.Errorf("API role can read %s after Harden", fqn)
		}
	}
	// The first real migration still works on top of a hardened DB.
	if err := m.Apply(ctx, &domain.Config{Version: 1, Auth: &domain.Auth{}}); err != nil {
		t.Fatalf("migrate after harden: %v", err)
	}
	assertHardened(t, db)
}

// Engine wiring: serve without --migrate still hardens on boot.
func TestEngineStart_HardensWithoutMigrate(t *testing.T) {
	owner, req := dbboot.StartContainer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cfg := &domain.Config{Version: 1, Auth: &domain.Auth{}}
	if err := app.NewMigrator(owner).Apply(ctx, cfg); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	applyLegacyGrants(t, owner)

	engineCtx, stop := context.WithCancel(ctx)
	defer stop()
	eng := app.NewEngine(cfg, owner, req, domain.DefaultRoles(),
		app.WithMode(app.ModeProd), app.WithMigrate(false),
		app.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	done := make(chan error, 1)
	go func() { done <- eng.Start(engineCtx) }()

	deadline := time.Now().Add(30 * time.Second)
	for hasPriv(t, owner, "anon", "auth.jwt_keys", "SELECT") {
		if time.Now().After(deadline) {
			t.Fatal("engine.Start did not harden the live DB")
		}
		select {
		case err := <-done:
			t.Fatalf("engine exited early: %v", err)
		case <-time.After(200 * time.Millisecond):
		}
	}
	assertHardened(t, owner)
	stop()
	<-done
}

func storagePolicyNames(t *testing.T, db domain.Database) []string {
	t.Helper()
	rows, err := db.Query(context.Background(), `SELECT policyname::text AS p FROM pg_policies WHERE schemaname = 'storage' AND tablename = 'objects' ORDER BY 1`)
	if err != nil {
		t.Fatalf("list policies: %v", err)
	}
	var names []string
	for _, r := range rows {
		names = append(names, r["p"].(string))
	}
	return names
}

// A migrate=false boot must heal from the applied config, never the pending one.
func TestEngineStart_DriftBootHealsFromAppliedConfig(t *testing.T) {
	owner, req := dbboot.StartContainer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	applied := &domain.Config{Version: 1, Auth: &domain.Auth{}, Storage: map[string]domain.Bucket{
		"sec": {RLS: []domain.RLSPolicy{{Operations: []string{"select"}, Using: "true"}}},
	}}
	if err := app.NewMigrator(owner).Apply(ctx, applied); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	before := storagePolicyNames(t, owner)

	pending := &domain.Config{Version: 1, Auth: &domain.Auth{}, Storage: map[string]domain.Bucket{
		"sec": {RLS: []domain.RLSPolicy{
			{Operations: []string{"select"}, Using: "true"},
			{Operations: []string{"select"}, Using: "no_such_fn(name)", Type: "restrictive"},
			{Operations: []string{"insert"}, WithCheck: "true"},
		}},
	}}
	engineCtx, stop := context.WithCancel(ctx)
	defer stop()
	eng := app.NewEngine(pending, owner, req, domain.DefaultRoles(),
		app.WithMode(app.ModeProd), app.WithMigrate(false),
		app.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	done := make(chan error, 1)
	go func() { done <- eng.Start(engineCtx) }()

	// The JWT key is seeded right after Harden, so a row means boot got past it.
	deadline := time.Now().Add(30 * time.Second)
	for {
		row, err := owner.QueryRow(ctx, "SELECT count(*) AS n FROM auth.jwt_keys")
		if err == nil && row["n"].(int64) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("engine never got past Harden")
		}
		select {
		case err := <-done:
			t.Fatalf("drift boot failed: %v", err)
		case <-time.After(200 * time.Millisecond):
		}
	}
	if got := storagePolicyNames(t, owner); !reflect.DeepEqual(got, before) {
		t.Fatalf("drift boot changed storage policies: got %v, want %v", got, before)
	}
	stop()
	<-done
}
