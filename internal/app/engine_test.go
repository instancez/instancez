package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/saedx1/ultrabase/internal/config"
	"github.com/saedx1/ultrabase/internal/domain"
	"golang.org/x/crypto/bcrypt"
)

func TestOrderDataTables_UsersFirst(t *testing.T) {
	cfg := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "user_id", ForeignKey: &domain.ForeignKey{References: "users.id"}},
				},
			},
			"teams": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
				},
			},
		},
		Data: map[string]domain.TableData{
			"users": {CSVFiles: map[string]string{"demo": "./seeds/users.csv"}},
			"todos": {CSVFiles: map[string]string{"init": "./seeds/todos.csv"}},
			"teams": {CSVFiles: map[string]string{"init": "./seeds/teams.csv"}},
		},
	}

	order := orderDataTables(cfg)
	if len(order) == 0 {
		t.Fatal("expected data tables")
	}
	if order[0] != "users" {
		t.Errorf("first data table should be 'users', got %q", order[0])
	}
}

func TestOrderDataTables_NoUsers(t *testing.T) {
	cfg := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}}},
		},
		Data: map[string]domain.TableData{
			"todos": {CSVFiles: map[string]string{"init": "./seeds/todos.csv"}},
		},
	}

	order := orderDataTables(cfg)
	if len(order) != 1 || order[0] != "todos" {
		t.Errorf("expected [todos], got %v", order)
	}
}

func TestDataPasswordHashing(t *testing.T) {
	row := map[string]any{
		"email":    "admin@test.com",
		"password": "secret123",
	}

	if pwd, ok := row["password"]; ok {
		if pwdStr, ok := pwd.(string); ok {
			hash, err := bcrypt.GenerateFromPassword([]byte(pwdStr), bcrypt.DefaultCost)
			if err != nil {
				t.Fatalf("hash error: %v", err)
			}
			row["password_hash"] = string(hash)
			delete(row, "password")
		}
	}

	if _, ok := row["password"]; ok {
		t.Error("password should have been removed")
	}

	hashStr, ok := row["password_hash"].(string)
	if !ok || hashStr == "" {
		t.Fatal("password_hash should be set")
	}

	err := bcrypt.CompareHashAndPassword([]byte(hashStr), []byte("secret123"))
	if err != nil {
		t.Errorf("bcrypt verification failed: %v", err)
	}

	err = bcrypt.CompareHashAndPassword([]byte(hashStr), []byte("wrong"))
	if err == nil {
		t.Error("wrong password should fail verification")
	}
}

func TestValidateDataColumns_UnknownColumn(t *testing.T) {
	e := &Engine{
		cfg: &domain.Config{
			Tables: map[string]domain.Table{
				"products": {
					Fields: []domain.Field{
						{Name: "id", Type: "bigserial", PrimaryKey: true},
						{Name: "name", Type: "text"},
					},
				},
			},
		},
	}

	records := []map[string]any{
		{"id": "1", "name": "Widget", "nonexistent": "bad"},
	}

	err := e.validateDataColumns("products", records)
	if err == nil {
		t.Error("expected error for unknown column")
	}
}

func TestValidateDataColumns_ValidColumns(t *testing.T) {
	e := &Engine{
		cfg: &domain.Config{
			Tables: map[string]domain.Table{
				"products": {
					Fields: []domain.Field{
						{Name: "id", Type: "bigserial", PrimaryKey: true},
						{Name: "name", Type: "text"},
					},
				},
			},
		},
	}

	records := []map[string]any{
		{"id": "1", "name": "Widget"},
	}

	err := e.validateDataColumns("products", records)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// newFakeRequestDB returns a domain.RequestDB wrapping a fakeDB. The engine
// constructor stores it but the migration-fallback path under test never
// dispatches queries through it, so the underlying fake's stub methods are
// sufficient.
func newFakeRequestDB(t *testing.T) domain.RequestDB {
	t.Helper()
	return domain.RequestDB{Database: newFakeDB(t)}
}

// TestEngineFallsBackOnMigrationFailure: if a previous successful migration
// exists, a failed Apply must not crash the boot — the engine should swap
// e.cfg back to the recorded last-known-good and report drift.
func TestEngineFallsBackOnMigrationFailure(t *testing.T) {
	db := newFakeDB(t)
	roles := domain.DefaultRoles()
	authDB := newFakeRequestDB(t)

	good := &domain.Config{
		Tables: map[string]domain.Table{
			"a": {Fields: []domain.Field{{Name: "id", Type: "BIGINT", PrimaryKey: true}}},
		},
		Server: domain.Server{Port: 8080},
	}

	migrator := NewMigrator(db, roles)
	if err := migrator.Apply(context.Background(), good); err != nil {
		t.Fatalf("seed: %v", err)
	}

	bad := &domain.Config{
		Tables: map[string]domain.Table{
			"a": good.Tables["a"],
			"b": {Fields: []domain.Field{{Name: "id", Type: "BIGINT", PrimaryKey: true}}},
		},
		Server: domain.Server{Port: 8080},
	}
	db.failOnStatementContaining = "CREATE TABLE IF NOT EXISTS b"

	engine := NewEngine(bad, domain.OwnerDB{Database: db}, authDB, roles, WithMode(ModeProd), WithMigrate(true))
	tracker, err := engine.applyMigrationsWithFallback(context.Background())
	if err != nil {
		t.Fatalf("engine should not error on a recoverable failure: %v", err)
	}
	state := tracker.Snapshot()
	if state.Status != DriftStatusDrift {
		t.Fatalf("expected drift status, got %q", state.Status)
	}
	if state.LastError == "" {
		t.Fatalf("expected last error to be populated")
	}

	if _, hasB := engine.cfg.Tables["b"]; hasB {
		t.Fatalf("engine.cfg should have fallen back to good config without table b")
	}
}

// TestEngineFailsHardOnFirstBootMigrationFailure: with no recorded
// last-known-good, there is nothing to fall back to and the boot must error.
func TestEngineFailsHardOnFirstBootMigrationFailure(t *testing.T) {
	db := newFakeDB(t)
	roles := domain.DefaultRoles()
	authDB := newFakeRequestDB(t)

	bad := &domain.Config{
		Tables: map[string]domain.Table{
			"a": {Fields: []domain.Field{{Name: "id", Type: "BIGINT", PrimaryKey: true}}},
		},
	}
	db.failOnStatementContaining = "CREATE TABLE IF NOT EXISTS a"

	engine := NewEngine(bad, domain.OwnerDB{Database: db}, authDB, roles, WithMode(ModeProd), WithMigrate(true))
	if _, err := engine.applyMigrationsWithFallback(context.Background()); err == nil {
		t.Fatalf("expected hard error on first-boot failure")
	}
}

// TestEngineFailsHardOnUnparseableLastConfig: if the recorded
// last-known-good config_json is corrupt and can't be deserialized,
// the engine must hard-fail rather than continue with an unknown state.
func TestEngineFailsHardOnUnparseableLastConfig(t *testing.T) {
	db := newFakeDB(t)
	roles := domain.DefaultRoles()
	authDB := newFakeRequestDB(t)

	good := &domain.Config{
		Tables: map[string]domain.Table{
			"a": {Fields: []domain.Field{{Name: "id", Type: "BIGINT", PrimaryKey: true}}},
		},
		Server: domain.Server{Port: 8080},
	}
	migrator := NewMigrator(db, roles)
	if err := migrator.Apply(context.Background(), good); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Corrupt the recorded config_json so unmarshal fails.
	db.lastMigration.ConfigJSON = "{not valid json"

	bad := &domain.Config{
		Tables: map[string]domain.Table{
			"a": good.Tables["a"],
			"b": {Fields: []domain.Field{{Name: "id", Type: "BIGINT", PrimaryKey: true}}},
		},
	}
	db.failOnStatementContaining = "CREATE TABLE IF NOT EXISTS b"

	engine := NewEngine(bad, domain.OwnerDB{Database: db}, authDB, roles, WithMode(ModeProd), WithMigrate(true))
	_, err := engine.applyMigrationsWithFallback(context.Background())
	if err == nil {
		t.Fatalf("expected hard error when last-known-good is unparseable")
	}
	if !strings.Contains(err.Error(), "unparseable") {
		t.Fatalf("expected error to mention 'unparseable', got: %v", err)
	}
}

func TestDriftHeartbeatLogsPeriodically(t *testing.T) {
	tracker := NewDriftTracker("test://x")
	tracker.MarkOK("good", time.Now())
	tracker.MarkDrift("bad", "boom", time.Now())

	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tickCh := make(chan struct{}, 4)
	var logged int32
	go runDriftHeartbeat(ctx, tracker, logger, 20*time.Millisecond, func() {
		atomic.AddInt32(&logged, 1)
		select {
		case tickCh <- struct{}{}:
		default:
		}
	})

	for i := 0; i < 3; i++ {
		select {
		case <-tickCh:
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("heartbeat did not tick %d times (got %d)", 3, atomic.LoadInt32(&logged))
		}
	}
}

// fakeSource is a config.Source whose Watch channel is driven directly by
// tests. Read/Write/Load are not exercised by runWatcher and return stubs.
type fakeSource struct {
	ch chan config.WatchEvent
}

func (s *fakeSource) Load(ctx context.Context) (*domain.Config, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *fakeSource) Read(ctx context.Context) ([]byte, string, error) {
	return nil, "", nil
}
func (s *fakeSource) Write(ctx context.Context, data []byte, expected string) (string, error) {
	return "", nil
}
func (s *fakeSource) Describe() string { return "fake://" }
func (s *fakeSource) Watch(ctx context.Context, interval time.Duration) (<-chan config.WatchEvent, error) {
	if s.ch == nil {
		s.ch = make(chan config.WatchEvent, 4)
	}
	return s.ch, nil
}

// goodYAML / badYAML are used to drive runWatcher with a parseable config
// that the migrator either accepts or rejects.
const watcherGoodYAML = `version: 1
project:
  name: t
server:
  port: 8080
tables:
  a:
    fields:
      - name: id
        type: bigint
        primary_key: true
`

const watcherBadMigrationYAML = `version: 1
project:
  name: t
server:
  port: 8080
tables:
  a:
    fields:
      - name: id
        type: bigint
        primary_key: true
  b:
    fields:
      - name: id
        type: bigint
        primary_key: true
`

const watcherInvalidYAML = `version: 1
project:
  name: t
server:
  port: 8080
tables:
  "Bad-Name":
    fields:
      - name: id
        type: bigint
        primary_key: true
`

// TestEngineRunWatcher_GoodEventReloads: a successful parse + validate + apply
// updates engine.cfg and produces a tracker in OK state.
func TestEngineRunWatcher_GoodEventReloads(t *testing.T) {
	db := newFakeDB(t)
	roles := domain.DefaultRoles()
	authDB := newFakeRequestDB(t)

	// Seed an initial migration so the fallback path has a last-known-good
	// to compare against (not strictly required for the OK path, but makes
	// the engine behave like it would after a normal boot).
	initial := &domain.Config{
		Tables: map[string]domain.Table{
			"a": {Fields: []domain.Field{{Name: "id", Type: "BIGINT", PrimaryKey: true}}},
		},
		Server: domain.Server{Port: 8080},
	}
	if err := NewMigrator(db, roles).Apply(context.Background(), initial); err != nil {
		t.Fatalf("seed: %v", err)
	}

	src := &fakeSource{ch: make(chan config.WatchEvent, 4)}
	engine := NewEngine(initial, domain.OwnerDB{Database: db}, authDB, roles,
		WithMode(ModeProd), WithMigrate(true), WithConfigSource(src))
	engine.drift = NewDriftTracker(src.Describe())
	engine.drift.MarkOK("seed", time.Now())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		engine.runWatcher(ctx, 0)
		close(done)
	}()

	src.ch <- config.WatchEvent{Data: []byte(watcherGoodYAML), Version: "v1"}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state := engine.Drift().Snapshot()
		if state.Status == DriftStatusOK && state.RunningChecksum != "seed" && len(engine.Config().Tables) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	state := engine.Drift().Snapshot()
	if state.Status != DriftStatusOK {
		t.Fatalf("expected drift status OK, got %q (last_error=%q)", state.Status, state.LastError)
	}
	if state.RunningChecksum == "seed" {
		t.Fatalf("expected running checksum to update after reload")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("runWatcher did not return after context cancel")
	}
}

// TestEngineRunWatcher_BadMigrationFallsBack: a parseable+valid config whose
// migration fails should swap engine.cfg back to last-known-good and report
// drift instead of crashing the watcher.
func TestEngineRunWatcher_BadMigrationFallsBack(t *testing.T) {
	db := newFakeDB(t)
	roles := domain.DefaultRoles()
	authDB := newFakeRequestDB(t)

	initial := &domain.Config{
		Tables: map[string]domain.Table{
			"a": {Fields: []domain.Field{{Name: "id", Type: "BIGINT", PrimaryKey: true}}},
		},
		Server: domain.Server{Port: 8080},
	}
	if err := NewMigrator(db, roles).Apply(context.Background(), initial); err != nil {
		t.Fatalf("seed: %v", err)
	}

	src := &fakeSource{ch: make(chan config.WatchEvent, 4)}
	engine := NewEngine(initial, domain.OwnerDB{Database: db}, authDB, roles,
		WithMode(ModeProd), WithMigrate(true), WithConfigSource(src))
	engine.drift = NewDriftTracker(src.Describe())
	engine.drift.MarkOK("seed", time.Now())

	// Trip the migrator on the new table coming through the watcher.
	db.failOnStatementContaining = "CREATE TABLE IF NOT EXISTS b"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		engine.runWatcher(ctx, 0)
		close(done)
	}()

	src.ch <- config.WatchEvent{Data: []byte(watcherBadMigrationYAML), Version: "v2"}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if engine.Drift().Snapshot().Status == DriftStatusDrift {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	state := engine.Drift().Snapshot()
	if state.Status != DriftStatusDrift {
		t.Fatalf("expected drift status drift, got %q", state.Status)
	}
	if state.LastError == "" {
		t.Fatalf("expected last error to be populated on drift")
	}
	if _, hasB := engine.Config().Tables["b"]; hasB {
		t.Fatalf("engine.cfg should have fallen back to good config without table b")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("runWatcher did not return after context cancel")
	}
}

// TestEngineRunWatcher_InvalidYAMLMarksDrift: a config that parses but fails
// validation must mark drift via the raw-bytes checksum without touching
// engine.cfg or invoking the migrator.
func TestEngineRunWatcher_InvalidYAMLMarksDrift(t *testing.T) {
	db := newFakeDB(t)
	roles := domain.DefaultRoles()
	authDB := newFakeRequestDB(t)

	initial := &domain.Config{
		Tables: map[string]domain.Table{
			"a": {Fields: []domain.Field{{Name: "id", Type: "BIGINT", PrimaryKey: true}}},
		},
		Server: domain.Server{Port: 8080},
	}
	src := &fakeSource{ch: make(chan config.WatchEvent, 4)}
	engine := NewEngine(initial, domain.OwnerDB{Database: db}, authDB, roles,
		WithMode(ModeProd), WithMigrate(true), WithConfigSource(src))
	engine.drift = NewDriftTracker(src.Describe())
	engine.drift.MarkOK("seed", time.Now())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		engine.runWatcher(ctx, 0)
		close(done)
	}()

	src.ch <- config.WatchEvent{Data: []byte(watcherInvalidYAML), Version: "v3"}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if engine.Drift().Snapshot().Status == DriftStatusDrift {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	state := engine.Drift().Snapshot()
	if state.Status != DriftStatusDrift {
		t.Fatalf("expected drift status drift on validation failure, got %q", state.Status)
	}
	// engine.cfg must remain unchanged: we never assigned the bad cfg.
	if _, hasA := engine.Config().Tables["a"]; !hasA {
		t.Fatalf("engine.cfg should have been left untouched on validation failure")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("runWatcher did not return after context cancel")
	}
}

func TestValidateDataColumnsAuthUsersAllowsKnownCols(t *testing.T) {
	e := &Engine{cfg: &domain.Config{}}
	err := e.validateDataColumns("auth.users", []map[string]any{
		{"email": "a@b.com", "password": "x", "raw_user_meta_data": map[string]any{}},
	})
	if err != nil {
		t.Fatalf("expected validation to pass for known auth.users cols, got: %v", err)
	}
}

func TestValidateDataColumnsAuthUsersRejectsUnknown(t *testing.T) {
	e := &Engine{cfg: &domain.Config{}}
	err := e.validateDataColumns("auth.users", []map[string]any{
		{"display_name": "alice"},
	})
	if err == nil {
		t.Fatal("expected validation to reject unknown column on auth.users")
	}
}

// TestEngineRunWatcher_TransientErrorIgnored: a WatchEvent carrying only
// Err must not flip the tracker into drift; it's a watcher hiccup, not a
// config change.
func TestEngineRunWatcher_TransientErrorIgnored(t *testing.T) {
	db := newFakeDB(t)
	roles := domain.DefaultRoles()
	authDB := newFakeRequestDB(t)

	initial := &domain.Config{
		Tables: map[string]domain.Table{
			"a": {Fields: []domain.Field{{Name: "id", Type: "BIGINT", PrimaryKey: true}}},
		},
		Server: domain.Server{Port: 8080},
	}
	src := &fakeSource{ch: make(chan config.WatchEvent, 4)}
	engine := NewEngine(initial, domain.OwnerDB{Database: db}, authDB, roles,
		WithMode(ModeProd), WithMigrate(true), WithConfigSource(src))
	engine.drift = NewDriftTracker(src.Describe())
	engine.drift.MarkOK("seed", time.Now())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		engine.runWatcher(ctx, 0)
		close(done)
	}()

	src.ch <- config.WatchEvent{Err: fmt.Errorf("transient watcher hiccup")}
	// Follow up with a known-good event so we have something concrete to
	// wait for; the tracker should be OK at the end.
	src.ch <- config.WatchEvent{Data: []byte(watcherGoodYAML), Version: "v4"}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state := engine.Drift().Snapshot()
		if state.Status == DriftStatusOK && state.RunningChecksum != "seed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if state := engine.Drift().Snapshot(); state.Status != DriftStatusOK {
		t.Fatalf("transient error should not have flipped to drift; status=%q error=%q", state.Status, state.LastError)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("runWatcher did not return after context cancel")
	}
}
