//go:build integration

package cli

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/instancez/instancez/internal/cli/preflight"
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

func TestStartEmbeddedPostgres_PortStableAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	stop, dsn1 := startPGOrSkip(t, devOptions{pgDataDir: dir})
	stop()

	stop, dsn2 := startPGOrSkip(t, devOptions{pgDataDir: dir})
	if dsn2 != dsn1 {
		t.Fatalf("port changed across restart: %s -> %s", dsn1, dsn2)
	}
	stop()

	port := strings.SplitN(strings.SplitN(dsn1, "localhost:", 2)[1], "/", 2)[0]
	l, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Skipf("could not occupy port %s: %v", port, err)
	}
	defer func() { _ = l.Close() }()
	_, dsn3 := startPGOrSkip(t, devOptions{pgDataDir: dir})
	if dsn3 == dsn1 {
		t.Fatalf("started on a busy port: %s", dsn3)
	}
	saved, err := os.ReadFile(filepath.Join(dir, pgPortFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dsn3, "localhost:"+string(saved)+"/") {
		t.Fatalf("port file %q does not match dsn %s", saved, dsn3)
	}
}

func TestDoctorChecks_ReachableDBReportsConnectAndLayout(t *testing.T) {
	_, dsn := startPGOrSkip(t, devOptions{pgDataDir: t.TempDir()})
	lookup := func(k string) string {
		if k == "INSTANCEZ_DATABASE_URL" {
			return dsn
		}
		return ""
	}
	checks := doctorChecks("instancez.yaml", lookup)
	if len(checks) != 4 {
		t.Fatalf("got %d checks, want 4 (config, DSN, connect, role layout)", len(checks))
	}
	results := preflight.RunAll(checks)
	if r := results[2]; r.Name != "database connect" || !r.OK {
		t.Fatalf("connect result = %+v, want a passing database connect", r)
	}
	if r := results[3]; r.Name != "role layout" {
		t.Fatalf("last result = %+v, want role layout", r)
	}
}
