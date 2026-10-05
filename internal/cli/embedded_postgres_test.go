package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEmbeddedPGHint(t *testing.T) {
	cases := []struct {
		name    string
		errMsg  string
		wantSub string // substring the hint must contain
	}{
		{
			name:    "unsupported platform: no version",
			errMsg:  "start embedded Postgres: no version found matching 16.4.0",
			wantSub: "no prebuilt binary",
		},
		{
			name:    "unsupported platform: no binary in archive",
			errMsg:  "error fetching postgres: cannot find binary in archive retrieved from https://repo",
			wantSub: "no prebuilt binary",
		},
		{
			name:    "first-run download: connect to host",
			errMsg:  "start embedded Postgres: unable to connect to repo1.maven.org",
			wantSub: "downloads Postgres binaries",
		},
		{
			name:    "first-run download: checksum mismatch",
			errMsg:  "start embedded Postgres: downloaded checksums do not match",
			wantSub: "downloads Postgres binaries",
		},
		{
			name:    "filesystem: write password file",
			errMsg:  "start embedded Postgres: unable to write password file to pgdata/runtime/pwfile",
			wantSub: "write permissions",
		},
		{
			name:    "filesystem: permission denied",
			errMsg:  "start embedded Postgres: mkdir /pgdata: permission denied",
			wantSub: "write permissions",
		},
		{
			name:    "corrupt data dir: could not start postgres",
			errMsg:  "start embedded Postgres: could not start postgres using /bin/pg_ctl:\nFATAL: database files are incompatible",
			wantSub: "--reset-pg",
		},
		{
			name:    "corrupt data dir: init database",
			errMsg:  "start embedded Postgres: unable to init database using 'initdb'",
			wantSub: "--reset-pg",
		},
		{
			name:    "stale process: port already listening",
			errMsg:  "start embedded Postgres: process already listening on port 54213",
			wantSub: "--reset-pg",
		},
		{
			name:    "post-start connect failure is treated as corrupt, not network",
			errMsg:  "start embedded Postgres: unable to connect to create database with custom name postgres",
			wantSub: "--reset-pg",
		},
		{
			name:    "unknown error falls back to generic",
			errMsg:  "start embedded Postgres: something nobody anticipated",
			wantSub: "INSTANCEZ_DATABASE_URL",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := embeddedPGHint(errors.New(tc.errMsg))
			if !strings.Contains(got, tc.wantSub) {
				t.Errorf("embeddedPGHint(%q)\n  = %q\n  want substring %q", tc.errMsg, got, tc.wantSub)
			}
		})
	}
}

func TestWithFileLock_SerializesConcurrentCallers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	var inside, maxInside, done atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := withFileLock(path, 10*time.Second, func() error {
				n := inside.Add(1)
				if n > maxInside.Load() {
					maxInside.Store(n)
				}
				time.Sleep(5 * time.Millisecond)
				inside.Add(-1)
				done.Add(1)
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if maxInside.Load() != 1 || done.Load() != 8 {
		t.Fatalf("max concurrent=%d done=%d", maxInside.Load(), done.Load())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("lock file left behind")
	}
}

func TestWithFileLock_ReleasesOnErrorAndTimesOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	want := errors.New("boom")
	if err := withFileLock(path, time.Second, func() error { return want }); !errors.Is(err, want) {
		t.Fatalf("got %v", err)
	}
	if err := withFileLock(path, time.Second, func() error { return nil }); err != nil {
		t.Fatalf("lock not released after error: %v", err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := withFileLock(path, 100*time.Millisecond, func() error { return nil }); err == nil {
		t.Fatal("want timeout while lock held")
	}
}

func TestWithFileLock_BreaksStaleLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-staleLockAge - time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := withFileLock(path, time.Second, func() error { return nil }); err != nil {
		t.Fatalf("stale lock not broken: %v", err)
	}
}

func TestWithFileLock_HeartbeatKeepsLiveLockFromBeingBroken(t *testing.T) {
	oldAge, oldTick := staleLockAge, lockHeartbeatTick
	staleLockAge, lockHeartbeatTick = 300*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { staleLockAge, lockHeartbeatTick = oldAge, oldTick })

	path := filepath.Join(t.TempDir(), "x.lock")
	held, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		_ = withFileLock(path, time.Second, func() error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	time.Sleep(3 * staleLockAge)
	err := withFileLock(path, 100*time.Millisecond, func() error { return nil })
	close(release)
	<-done
	if err == nil {
		t.Fatal("live lock was broken despite heartbeat")
	}
}

func TestStartLockPath_PerUser(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	p := startLockPath()
	if filepath.Dir(p) == os.TempDir() {
		t.Fatalf("lock in shared temp dir: %s", p)
	}
	if _, err := os.Stat(filepath.Dir(p)); err != nil {
		t.Fatal(err)
	}
}
