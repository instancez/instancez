package cli

import (
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
			name:    "stale pid file: start failure points at postmaster.pid",
			errMsg:  "start embedded Postgres: could not start postgres using /bin/pg_ctl:\nFATAL: lock file \"postmaster.pid\" already exists",
			wantSub: "delete ./pgdata/postmaster.pid",
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

func TestEmbeddedPGHintRunning(t *testing.T) {
	got := embeddedPGHint(fmt.Errorf("start embedded Postgres: %w", &embeddedPGRunningError{pid: 4242, port: 5432}))
	for _, want := range []string{"stop the other inz dev", "kill 4242"} {
		if !strings.Contains(got, want) {
			t.Errorf("hint %q missing %q", got, want)
		}
	}
	if strings.Contains(got, "--reset-pg") {
		t.Errorf("live-instance hint must not suggest --reset-pg: %q", got)
	}
}

// deadPID returns the PID of a process that has already exited.
func deadPID(t *testing.T) int {
	t.Helper()
	// nosemgrep -- test re-execs its own binary to get an exited PID
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

func writePidFile(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "postmaster.pid"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLiveEmbeddedPG(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	livePort := l.Addr().(*net.TCPAddr).Port
	deadPort, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	self := os.Getpid()

	cases := []struct {
		name     string
		content  *string
		wantLive bool
		wantPID  int
		wantPort int
	}{
		{name: "no file"},
		{name: "empty file", content: ptr("")},
		{name: "garbage", content: ptr("abc\n")},
		{name: "two lines only", content: ptr("123\n/data\n")},
		{name: "garbage port", content: ptr("123\n/data\n1\nxx\n")},
		{name: "port zero", content: ptr("123\n/data\n1\n0\n")},
		{name: "live pid and listener", content: ptr(fmt.Sprintf("%d\n/data\n1\n%d\n", self, livePort)), wantLive: true, wantPID: self, wantPort: livePort},
		{name: "live pid, nothing listening", content: ptr(fmt.Sprintf("%d\n/data\n1\n%d\n", self, deadPort))},
		{name: "pid zero", content: ptr(fmt.Sprintf("0\n/data\n1\n%d\n", livePort))},
		{name: "negative pid", content: ptr(fmt.Sprintf("-1\n/data\n1\n%d\n", livePort))},
	}
	if runtime.GOOS != "windows" {
		// An unrelated service now owns the port the crashed Postgres used.
		cases = append(cases, struct {
			name     string
			content  *string
			wantLive bool
			wantPID  int
			wantPort int
		}{name: "stale pid file, port held by unrelated listener", content: ptr(fmt.Sprintf("%d\n/data\n1\n%d\n", deadPID(t), livePort))})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.content != nil {
				writePidFile(t, dir, *tc.content)
			}
			pid, port, live := liveEmbeddedPG(dir)
			if live != tc.wantLive || pid != tc.wantPID || port != tc.wantPort {
				t.Fatalf("got (%d, %d, %v), want (%d, %d, %v)", pid, port, live, tc.wantPID, tc.wantPort, tc.wantLive)
			}
		})
	}
}

func ptr(s string) *string { return &s }

func TestStartEmbeddedPostgres_ResetRefusedWhileLiveUnit(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	writePidFile(t, dir, fmt.Sprintf("%d\n%s\n1\n%d\n", os.Getpid(), dir, l.Addr().(*net.TCPAddr).Port))
	marker := filepath.Join(dir, "marker")
	if err := os.WriteFile(marker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err = startEmbeddedPostgres(devOptions{pgDataDir: dir, resetPG: true})
	if !errors.Is(err, errEmbeddedPGRunning) {
		t.Fatalf("err = %v, want errEmbeddedPGRunning", err)
	}
	if _, serr := os.Stat(marker); serr != nil {
		t.Fatalf("--reset-pg wiped a live data dir: %v", serr)
	}
}

func TestPreferredPGPort(t *testing.T) {
	free, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	heldPort := uint32(held.Addr().(*net.TCPAddr).Port)

	cases := []struct {
		name    string
		content *string
		want    uint32 // 0 = any valid free port other than heldPort
	}{
		{name: "no file"},
		{name: "empty", content: ptr("")},
		{name: "garbage", content: ptr("abc")},
		{name: "zero", content: ptr("0")},
		{name: "too large", content: ptr("70000")},
		{name: "negative", content: ptr("-5")},
		{name: "free port with whitespace", content: ptr(fmt.Sprintf(" %d\n", free)), want: free},
		{name: "busy port", content: ptr(fmt.Sprint(heldPort))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.content != nil {
				if err := os.WriteFile(filepath.Join(dir, pgPortFile), []byte(*tc.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := preferredPGPort(dir)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want != 0 {
				if got != tc.want {
					t.Fatalf("got %d, want %d", got, tc.want)
				}
				return
			}
			if got == 0 || got > 65535 || got == heldPort {
				t.Fatalf("got %d, want a free valid port", got)
			}
		})
	}
}

// The liveness check must run under the start lock, or two concurrent starts both pass it.
func TestStartEmbeddedPostgres_LivenessCheckedUnderLock(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()

	errCh := make(chan error, 1)
	lockErr := withFileLock(startLockPath(), 5*time.Second, func() error {
		go func() {
			_, _, err := startEmbeddedPostgres(devOptions{pgDataDir: dir})
			errCh <- err
		}()
		time.Sleep(300 * time.Millisecond) // the second start is now waiting on the lock
		writePidFile(t, dir, fmt.Sprintf("%d\n%s\n1\n%d\n", os.Getpid(), dir, l.Addr().(*net.TCPAddr).Port))
		return nil
	})
	if lockErr != nil {
		t.Fatal(lockErr)
	}
	select {
	case err := <-errCh:
		if !errors.Is(err, errEmbeddedPGRunning) {
			t.Fatalf("err = %v, want errEmbeddedPGRunning", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("second start did not return")
	}
}

func TestPidAliveRejectsOutOfRangePIDs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows falls back to the port check")
	}
	// 1<<32 truncates to 0 in a 32-bit pid_t, which would signal our own process group.
	for _, pid := range []int{0, -1, 1 << 32, math.MaxInt32 + 1} {
		if pidAlive(pid) {
			t.Errorf("pidAlive(%d) = true, want false", pid)
		}
	}
	if !pidAlive(os.Getpid()) {
		t.Error("pidAlive(own pid) = false, want true")
	}
}

func TestPersistPGPortWarnsOnWriteFailure(t *testing.T) {
	var warn strings.Builder
	persistPGPort(filepath.Join(t.TempDir(), "missing"), 5432, &warn)
	if !strings.Contains(warn.String(), "could not save embedded Postgres port") {
		t.Fatalf("no warning on a failed save: %q", warn.String())
	}

	dir := t.TempDir()
	warn.Reset()
	persistPGPort(dir, 5432, &warn)
	if warn.Len() != 0 {
		t.Fatalf("unexpected warning: %q", warn.String())
	}
	if data, _ := os.ReadFile(filepath.Join(dir, pgPortFile)); string(data) != "5432" {
		t.Fatalf("port file = %q, want 5432", data)
	}
}
