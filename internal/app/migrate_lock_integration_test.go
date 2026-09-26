//go:build integration

package app_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
)

const concurrentMigrators = 8

func notesConfig(extraCols ...string) *domain.Config {
	fields := []domain.Field{
		{Name: "id", Type: "bigserial", PrimaryKey: true},
		{Name: "body", Type: "text"},
	}
	for _, c := range extraCols {
		fields = append(fields, domain.Field{Name: c, Type: "text"})
	}
	return &domain.Config{Tables: map[string]domain.Table{"notes": {Fields: fields}}}
}

// runConcurrently calls fn from n goroutines released at the same instant and
// returns every non-nil error.
func runConcurrently(n int, fn func() error) []error {
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		errs  []error
		start = make(chan struct{})
	)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := fn(); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	return errs
}

// Simulates N replicas (or Lambda cold starts) booting against a fresh DB.
func TestIntegration_ConcurrentApplyOnFreshDBRecordsOneMigration(t *testing.T) {
	ctx := context.Background()
	db := startPostgres(t)
	cfg := notesConfig()

	errs := runConcurrently(concurrentMigrators, func() error {
		return app.NewMigrator(db).Apply(ctx, cfg)
	})
	if len(errs) > 0 {
		t.Fatalf("%d of %d concurrent applies failed, first: %v", len(errs), concurrentMigrators, errs[0])
	}
	if n := countRows(t, db, "SELECT COUNT(*) AS n FROM _instancez_migrations"); n != 1 {
		t.Fatalf("history rows = %d, want 1", n)
	}
}

// ADD COLUMN has no IF NOT EXISTS, so without serialization every loser fails.
func TestIntegration_ConcurrentApplyOfSameChangeAppliesOnce(t *testing.T) {
	ctx := context.Background()
	db := startPostgres(t)
	if err := app.NewMigrator(db).Apply(ctx, notesConfig()); err != nil {
		t.Fatalf("apply v1: %v", err)
	}
	v2 := notesConfig("title")

	errs := runConcurrently(concurrentMigrators, func() error {
		return app.NewMigrator(db).Apply(ctx, v2)
	})
	if len(errs) > 0 {
		t.Fatalf("%d of %d concurrent applies failed, first: %v", len(errs), concurrentMigrators, errs[0])
	}
	if !columnExists(t, db, "notes", "title") {
		t.Fatal("column title missing after concurrent apply")
	}
	if n := countRows(t, db, "SELECT COUNT(*) AS n FROM _instancez_migrations"); n != 2 {
		t.Fatalf("history rows = %d, want 2", n)
	}
}

func TestIntegration_ConcurrentProvisionIdempotentSucceeds(t *testing.T) {
	ctx := context.Background()
	db := startPostgres(t)
	cfg := &domain.Config{
		Auth:    &domain.Auth{},
		Storage: map[string]domain.Bucket{"avatars": {}},
	}

	errs := runConcurrently(concurrentMigrators, func() error {
		return app.NewMigrator(db).ProvisionIdempotent(ctx, cfg)
	})
	if len(errs) > 0 {
		t.Fatalf("%d of %d concurrent provisions failed, first: %v", len(errs), concurrentMigrators, errs[0])
	}
}

// A long-running query on a table must not let the migration queue an ACCESS
// EXCLUSIVE lock that stalls every request behind it.
func TestIntegration_ApplyFailsFastWhenTableIsBusy(t *testing.T) {
	ctx := context.Background()
	db := startPostgres(t)
	if err := app.NewMigrator(db).Apply(ctx, notesConfig()); err != nil {
		t.Fatalf("apply v1: %v", err)
	}

	holder, err := db.Begin(ctx)
	if err != nil {
		t.Fatalf("begin holder: %v", err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	if _, err := holder.Exec(ctx, "LOCK TABLE notes IN ACCESS SHARE MODE"); err != nil {
		t.Fatalf("lock notes: %v", err)
	}

	v2 := notesConfig("title")
	applyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	start := time.Now()
	err = app.NewMigrator(db).LockTimeout(200*time.Millisecond).Apply(applyCtx, v2)

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		t.Fatalf("apply err = %v, want lock_not_available (55P03)", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("apply took %s, want fail-fast", elapsed)
	}
	if n := countRows(t, db, "SELECT COUNT(*) AS n FROM _instancez_migrations"); n != 1 {
		t.Fatalf("history rows = %d after failed apply, want 1", n)
	}

	if err := holder.Rollback(ctx); err != nil {
		t.Fatalf("release lock: %v", err)
	}
	if err := app.NewMigrator(db).LockTimeout(200*time.Millisecond).Apply(ctx, v2); err != nil {
		t.Fatalf("apply after release: %v", err)
	}
	if !columnExists(t, db, "notes", "title") {
		t.Fatal("column title missing after retry")
	}
}
