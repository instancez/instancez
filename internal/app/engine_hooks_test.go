package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/instancez/instancez/internal/config"
	"github.com/instancez/instancez/internal/domain"
)

type errRecorder struct {
	mu   sync.Mutex
	errs []error
}

func (r *errRecorder) record(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errs = append(r.errs, err)
}

func (r *errRecorder) snapshot() []error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]error(nil), r.errs...)
}

var quietLogger = WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))

func hookTableCfg(names ...string) *domain.Config {
	tables := map[string]domain.Table{}
	for _, n := range names {
		tables[n] = domain.Table{Fields: []domain.Field{{Name: "id", Type: "BIGINT", PrimaryKey: true}}}
	}
	return &domain.Config{Tables: tables, Server: domain.Server{Port: 8080}}
}

// runWatcherWith feeds one event through runWatcher and waits until done() or timeout.
func runWatcherWith(t *testing.T, e *Engine, src *fakeSource, data string, done func() bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { e.runWatcher(ctx, 0); close(finished) }()
	src.ch <- config.WatchEvent{Data: []byte(data), Version: "v2"}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !done() {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-finished
}

func TestOnReadyCalledOnce(t *testing.T) {
	db := newFakeDB(t)
	calls := 0
	e := NewEngine(hookTableCfg("a"), domain.OwnerDB{Database: db}, newFakeRequestDB(t), domain.DefaultRoles(),
		WithMode(ModeProd), WithMigrate(true), quietLogger, WithOnReady(func() { calls++ }))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if calls != 1 {
		t.Fatalf("OnReady calls = %d, want 1", calls)
	}
}

func TestOnReadyNotCalledWhenBootFails(t *testing.T) {
	db := newFakeDB(t)
	db.failOnStatementContaining = "CREATE TABLE IF NOT EXISTS a"
	calls := 0
	e := NewEngine(hookTableCfg("a"), domain.OwnerDB{Database: db}, newFakeRequestDB(t), domain.DefaultRoles(),
		WithMode(ModeDev), quietLogger, WithOnReady(func() { calls++ }))
	if err := e.Start(context.Background()); err == nil {
		t.Fatal("expected boot error")
	}
	if calls != 0 {
		t.Fatalf("OnReady fired on failed boot: %d", calls)
	}
}

func TestNilHooksAreNoOps(t *testing.T) {
	db := newFakeDB(t)
	src := &fakeSource{ch: make(chan config.WatchEvent, 4)}
	e := NewEngine(hookTableCfg("a"), domain.OwnerDB{Database: db}, newFakeRequestDB(t), domain.DefaultRoles(),
		WithMode(ModeProd), WithMigrate(true), WithConfigSource(src), quietLogger,
		WithOnReady(nil), WithOnRuntimeError(nil))
	e.drift = NewDriftTracker(src.Describe())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	runWatcherWith(t, e, src, "not: [valid", func() bool { return false })
}

func TestOnRuntimeErrorOnMigrationFailure(t *testing.T) {
	db := newFakeDB(t)
	roles := domain.DefaultRoles()
	if err := NewMigrator(db, roles).Apply(context.Background(), hookTableCfg("a")); err != nil {
		t.Fatal(err)
	}
	db.failOnStatementContaining = "CREATE TABLE IF NOT EXISTS b"
	rec := &errRecorder{}
	e := NewEngine(hookTableCfg("a", "b"), domain.OwnerDB{Database: db}, newFakeRequestDB(t), roles,
		WithMode(ModeProd), WithMigrate(true), quietLogger, WithOnRuntimeError(rec.record))
	if _, err := e.applyMigrationsWithFallback(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := rec.snapshot(); len(got) != 1 {
		t.Fatalf("hook calls = %d, want 1", len(got))
	}
}

func TestOnRuntimeErrorDestructiveNotCalled(t *testing.T) {
	db := newFakeDB(t)
	roles := domain.DefaultRoles()
	if err := NewMigrator(db, roles).Apply(context.Background(), hookTableCfg("a")); err != nil {
		t.Fatal(err)
	}
	rec := &errRecorder{}
	e := NewEngine(hookTableCfg(), domain.OwnerDB{Database: db}, newFakeRequestDB(t), roles,
		WithMode(ModeProd), WithMigrate(true), quietLogger, WithOnRuntimeError(rec.record))
	tracker, err := e.applyMigrationsWithFallback(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tracker.Snapshot().Status != DriftStatusDrift {
		t.Fatal("expected drift from destructive gate")
	}
	if got := rec.snapshot(); len(got) != 0 {
		t.Fatalf("destructive gate must not fire hook, got %v", got)
	}
}

func TestOnRuntimeErrorNotCalledOnFirstBootFailure(t *testing.T) {
	db := newFakeDB(t)
	db.failOnStatementContaining = "CREATE TABLE IF NOT EXISTS a"
	rec := &errRecorder{}
	e := NewEngine(hookTableCfg("a"), domain.OwnerDB{Database: db}, newFakeRequestDB(t), domain.DefaultRoles(),
		WithMode(ModeDev), quietLogger, WithOnRuntimeError(rec.record))
	if _, err := e.applyMigrationsWithFallback(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if got := rec.snapshot(); len(got) != 0 {
		t.Fatalf("returned errors must not also fire hook, got %v", got)
	}
}

func TestOnRuntimeErrorOnParseFailure(t *testing.T) {
	db := newFakeDB(t)
	src := &fakeSource{ch: make(chan config.WatchEvent, 4)}
	rec := &errRecorder{}
	e := NewEngine(hookTableCfg("a"), domain.OwnerDB{Database: db}, newFakeRequestDB(t), domain.DefaultRoles(),
		WithMode(ModeProd), WithConfigSource(src), quietLogger, WithOnRuntimeError(rec.record))
	runWatcherWith(t, e, src, "tables: [unterminated", func() bool { return len(rec.snapshot()) > 0 })
	got := rec.snapshot()
	if len(got) != 1 {
		t.Fatalf("hook calls = %d, want 1", len(got))
	}
	var cfgErr *domain.ConfigError
	if !errors.As(got[0], &cfgErr) {
		t.Fatalf("want *domain.ConfigError, got %T: %v", got[0], got[0])
	}
}

func TestOnRuntimeErrorOnValidationFailure(t *testing.T) {
	db := newFakeDB(t)
	src := &fakeSource{ch: make(chan config.WatchEvent, 4)}
	rec := &errRecorder{}
	e := NewEngine(hookTableCfg("a"), domain.OwnerDB{Database: db}, newFakeRequestDB(t), domain.DefaultRoles(),
		WithMode(ModeProd), WithConfigSource(src), quietLogger, WithOnRuntimeError(rec.record))
	runWatcherWith(t, e, src, watcherInvalidYAML, func() bool { return len(rec.snapshot()) > 0 })
	got := rec.snapshot()
	if len(got) != 1 {
		t.Fatalf("hook calls = %d, want 1", len(got))
	}
	var ve domain.ValidationErrors
	if !errors.As(got[0], &ve) || len(ve) == 0 {
		t.Fatalf("want non-empty domain.ValidationErrors, got %T: %v", got[0], got[0])
	}
}

func TestOnRuntimeErrorOnWatcherHardenFailure(t *testing.T) {
	db := newFakeDB(t)
	roles := domain.DefaultRoles()
	cfg := hookTableCfg("a")
	if err := NewMigrator(db, roles).Apply(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	db.failOnStatementContaining = "REVOKE ALL ON auth.jwt_keys"
	src := &fakeSource{ch: make(chan config.WatchEvent, 4)}
	rec := &errRecorder{}
	e := NewEngine(cfg, domain.OwnerDB{Database: db}, newFakeRequestDB(t), roles,
		WithMode(ModeProd), WithMigrate(true), WithConfigSource(src), quietLogger, WithOnRuntimeError(rec.record))
	e.drift = NewDriftTracker(src.Describe())
	runWatcherWith(t, e, src, watcherGoodYAML, func() bool { return len(rec.snapshot()) > 1 })
	if got := rec.snapshot(); len(got) != 2 {
		t.Fatalf("hook calls = %d, want 2 (drift fallback, harden): %v", len(got), got)
	}
}
