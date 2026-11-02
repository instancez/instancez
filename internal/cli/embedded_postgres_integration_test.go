//go:build integration

package cli

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestStartEmbeddedPostgres_BootsAndAcceptsConnections covers the `inz dev
// --embedded-pg` boot path end to end: start the process, connect on the
// returned superuser DSN, run a trivial query, then stop. Unit coverage only
// exercised embeddedPGHint's string mapping; nothing actually booted the
// database. On a host where the library can't fetch or run a binary (no
// prebuilt for the arch, offline first run) the start fails for environmental
// reasons, so we skip rather than fail. This mirrors the posture the
// supabase-js harness takes when node is absent.
func TestStartEmbeddedPostgres_BootsAndAcceptsConnections(t *testing.T) {
	opts := devOptions{pgDataDir: t.TempDir(), resetPG: true}

	stop, dsn, err := startEmbeddedPostgres(opts)
	if err != nil {
		if isEmbeddedPGEnvLimitation(err) {
			t.Skipf("embedded Postgres unavailable in this environment: %v", err)
		}
		t.Fatalf("startEmbeddedPostgres: %v", err)
	}
	t.Cleanup(stop)

	if !strings.HasPrefix(dsn, "postgres://postgres:postgres@localhost:") {
		t.Fatalf("dsn = %q, want a localhost superuser DSN", dsn)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to embedded Postgres: %v", err)
	}
	defer conn.Close(ctx)

	var got int
	if err := conn.QueryRow(ctx, "SELECT 1").Scan(&got); err != nil {
		t.Fatalf("query embedded Postgres: %v", err)
	}
	if got != 1 {
		t.Fatalf("SELECT 1 = %d, want 1", got)
	}
}

// isEmbeddedPGEnvLimitation reports whether a start failure is an environmental
// constraint (no binary for the OS/arch, or a first-run download that couldn't
// complete) rather than a real regression. It keys off the same upstream error
// strings embeddedPGHint matches, so it degrades safely: an unrecognized error
// is treated as a genuine failure and surfaces.
func isEmbeddedPGEnvLimitation(err error) bool {
	msg := strings.ToLower(err.Error())
	for _, frag := range []string{
		"no version found matching",
		"cannot find binary in archive",
		"download sha256",
		"checksums do not match",
		"error fetching postgres",
	} {
		if strings.Contains(msg, frag) {
			return true
		}
	}
	return false
}

func startPGOrSkip(t *testing.T, opts devOptions) (func(), string) {
	t.Helper()
	stop, dsn, err := startEmbeddedPostgres(opts)
	if err != nil {
		if isEmbeddedPGEnvLimitation(err) {
			t.Skipf("embedded Postgres unavailable in this environment: %v", err)
		}
		t.Fatalf("startEmbeddedPostgres: %v", err)
	}
	t.Cleanup(stop)
	return stop, dsn
}

func execPG(t *testing.T, dsn, sql string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, sql); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func TestStartEmbeddedPostgres_SecondStartRefusedWhileLive(t *testing.T) {
	dir := t.TempDir()
	_, dsn := startPGOrSkip(t, devOptions{pgDataDir: dir})

	_, _, err := startEmbeddedPostgres(devOptions{pgDataDir: dir})
	if !errors.Is(err, errEmbeddedPGRunning) {
		t.Fatalf("err = %v, want errEmbeddedPGRunning", err)
	}
	if _, serr := os.Stat(dir + ".runtime"); serr != nil {
		t.Fatalf("runtime dir of the live instance was removed: %v", serr)
	}
	execPG(t, dsn, "SELECT 1")
}

func TestStartEmbeddedPostgres_ResetRefusedWhileLive(t *testing.T) {
	dir := t.TempDir()
	_, dsn := startPGOrSkip(t, devOptions{pgDataDir: dir})
	execPG(t, dsn, "CREATE TABLE marker (id int)")

	_, _, err := startEmbeddedPostgres(devOptions{pgDataDir: dir, resetPG: true})
	if !errors.Is(err, errEmbeddedPGRunning) {
		t.Fatalf("err = %v, want errEmbeddedPGRunning", err)
	}
	execPG(t, dsn, "SELECT id FROM marker")
}

func TestStartEmbeddedPostgres_ConcurrentStartsOneWins(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	errs := make([]error, 2)
	stops := make([]func(), 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stops[i], _, errs[i] = startEmbeddedPostgres(devOptions{pgDataDir: dir})
		}()
	}
	wg.Wait()
	for _, st := range stops {
		if st != nil {
			t.Cleanup(st)
		}
	}
	var ok, refused int
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, errEmbeddedPGRunning):
			refused++
		case isEmbeddedPGEnvLimitation(err):
			t.Skipf("embedded Postgres unavailable in this environment: %v", err)
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 || refused != 1 {
		t.Fatalf("ok=%d refused=%d, want exactly one of each", ok, refused)
	}
}
