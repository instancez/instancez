package cli

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
)

// startEmbeddedPostgres starts an embedded Postgres 16 process using
// opts.pgDataDir as the persistent data directory. If opts.resetPG is true,
// the data directory is wiped first. Returns a stop func (call on shutdown)
// and the superuser DSN to inject as INSTANCEZ_DATABASE_URL.
func startEmbeddedPostgres(opts devOptions) (stop func(), dsn string, err error) {
	// The library defaults its logger to os.Stdout, which floods the dev banner
	// with initdb/pg_ctl chatter and PG's own server log. Capture it instead and
	// surface it only when Start fails.
	// ponytail: buffer grows with PG's session logging; fine for a dev session.
	var pgLog bytes.Buffer
	var pg *embeddedpostgres.EmbeddedPostgres
	var port uint32
	err = withFileLock(startLockPath(), pgStartLockTimeout, func() error {
		if pid, livePort, live := liveEmbeddedPG(opts.pgDataDir); live {
			return &embeddedPGRunningError{pid: pid, port: livePort}
		}
		if opts.resetPG {
			if err := os.RemoveAll(opts.pgDataDir); err != nil {
				return fmt.Errorf("reset pgdata: %w", err)
			}
		}
		p, err := preferredPGPort(opts.pgDataDir)
		if err != nil {
			return fmt.Errorf("find free port for embedded Postgres: %w", err)
		}
		port = p
		pg = embeddedpostgres.NewDatabase(
			embeddedpostgres.DefaultConfig().
				Version(embeddedpostgres.V16).
				DataPath(opts.pgDataDir).
				RuntimePath(opts.pgDataDir + ".runtime").
				Port(port).
				Logger(&pgLog),
		)
		if err := pg.Start(); err != nil {
			return err
		}
		_ = os.WriteFile(filepath.Join(opts.pgDataDir, pgPortFile), []byte(strconv.Itoa(int(port))), 0o600)
		return nil
	})
	if errors.Is(err, errEmbeddedPGRunning) {
		return nil, "", err
	}
	if err != nil {
		return nil, "", fmt.Errorf("start embedded Postgres: %w\n%s", err, pgLog.String())
	}

	superuserDSN := fmt.Sprintf("postgres://postgres:postgres@localhost:%d/postgres?sslmode=disable", port)
	return func() {
		if err := pg.Stop(); err != nil {
			fmt.Fprintf(os.Stderr, "embedded Postgres stop: %v\n", err)
		}
	}, superuserDSN, nil
}

const pgPortFile = ".inz-port"

// preferredPGPort reuses the port saved in dataDir when it is still free, else picks a new one.
func preferredPGPort(dataDir string) (uint32, error) {
	if data, err := os.ReadFile(filepath.Join(dataDir, pgPortFile)); err == nil {
		if p, perr := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 16); perr == nil && p > 0 {
			if l, lerr := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.FormatUint(p, 10))); lerr == nil {
				_ = l.Close()
				return uint32(p), nil
			}
		}
	}
	return freePort()
}

var errEmbeddedPGRunning = errors.New("another inz dev is already using this pgdata directory")

type embeddedPGRunningError struct{ pid, port int }

func (e *embeddedPGRunningError) Error() string {
	return fmt.Sprintf("%s (Postgres PID %d, port %d)", errEmbeddedPGRunning, e.pid, e.port)
}

func (e *embeddedPGRunningError) Unwrap() error { return errEmbeddedPGRunning }

// liveEmbeddedPG reports whether postmaster.pid in dataDir names a running process that accepts connections on its port.
func liveEmbeddedPG(dataDir string) (pid int, port int, live bool) {
	data, err := os.ReadFile(filepath.Join(dataDir, "postmaster.pid"))
	if err != nil {
		return 0, 0, false
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) < 4 {
		return 0, 0, false
	}
	pid, perr := strconv.Atoi(strings.TrimSpace(lines[0]))
	port, qerr := strconv.Atoi(strings.TrimSpace(lines[3]))
	if perr != nil || qerr != nil || !pidAlive(pid) {
		return 0, 0, false
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 500*time.Millisecond)
	if err != nil {
		return 0, 0, false
	}
	_ = conn.Close()
	return pid, port, true
}

// embeddedPGHint maps a failure from startEmbeddedPostgres to a single
// actionable remediation hint. The matches key off the error strings emitted
// by the fergusstrange/embedded-postgres library and the OS, so they are
// advisory: an unrecognized error falls back to the generic external-DB hint,
// which is always safe. If those upstream strings change, hints degrade to the
// fallback rather than misleading the user.
func embeddedPGHint(err error) string {
	msg := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, errEmbeddedPGRunning):
		hint := "stop the other inz dev (Ctrl+C in its terminal), or run this one from a different project directory"
		var running *embeddedPGRunningError
		if errors.As(err, &running) {
			hint += fmt.Sprintf("; if no inz dev is running, a leftover Postgres is still up: kill %d", running.pid)
		}
		return hint

	// Unsupported OS/arch: the library has no Postgres binary to download.
	case strings.Contains(msg, "no version found matching"),
		strings.Contains(msg, "cannot find binary in archive"):
		return "embedded Postgres has no prebuilt binary for this OS/architecture — set INSTANCEZ_DATABASE_URL to point at a full Postgres installation instead"

	// First-run download problems: binaries are fetched once (~30MB). Match the
	// remote-fetch "unable to connect to <host>" but not the post-start
	// "unable to connect to create database" case (handled as corrupt below).
	case strings.Contains(msg, "download sha256"),
		strings.Contains(msg, "checksums do not match"),
		strings.Contains(msg, "error fetching postgres"),
		strings.Contains(msg, "unable to connect to") && !strings.Contains(msg, "create database"):
		return "the first run downloads Postgres binaries (~30MB) — check your network connection and retry, or set INSTANCEZ_DATABASE_URL to use an existing Postgres"

	// Filesystem: can't write the data/runtime directory.
	case strings.Contains(msg, "permission denied"),
		strings.Contains(msg, "no space left"),
		strings.Contains(msg, "unable to write password file"),
		strings.Contains(msg, "unable to create runtime directory"):
		return "check write permissions and free disk space for ./pgdata"

	// Stale or corrupt ./pgdata: Postgres won't start, init, or accept a
	// connection — usually a leftover process or a data dir from a different
	// Postgres version.
	case strings.Contains(msg, "could not start postgres"),
		strings.Contains(msg, "unable to init database"),
		strings.Contains(msg, "unable to connect to create database"),
		strings.Contains(msg, "process already listening on port"),
		strings.Contains(msg, "timed out waiting"):
		return "./pgdata may be corrupt, left by a different Postgres version, or left by a crashed run — stop any running instance, delete ./pgdata/postmaster.pid if it is stale, or wipe and reinitialize with: inz dev --embedded-pg --reset-pg"

	default:
		return "set INSTANCEZ_DATABASE_URL to use a full Postgres installation instead"
	}
}

// freePort returns an available TCP port on localhost.
// Note: there is a small TOCTOU window between closing l and the embedded-postgres
// library binding the port; this is acceptable for dev tooling where the race is
// negligible in practice.
func freePort() (uint32, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return uint32(l.Addr().(*net.TCPAddr).Port), nil
}

const pgStartLockTimeout = 5 * time.Minute

var (
	staleLockAge      = 2 * time.Minute
	lockHeartbeatTick = 10 * time.Second
)

// startLockPath is per-user so another account's lock cannot block or be tampered with.
func startLockPath() string {
	dir, err := os.UserCacheDir()
	if err != nil || os.MkdirAll(dir, 0o700) != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "inz-embedded-pg.lock")
}

// withFileLock serializes fn across processes via an atomic lock directory; the holder touches it so only a dead holder's lock goes stale.
func withFileLock(path string, timeout time.Duration, fn func() error) error {
	deadline := time.Now().Add(timeout)
	for {
		err := os.Mkdir(path, 0o700)
		if err == nil {
			break
		}
		if !os.IsExist(err) {
			return err
		}
		if fi, serr := os.Stat(path); serr == nil && time.Since(fi.ModTime()) > staleLockAge {
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for lock %s", path)
		}
		time.Sleep(50 * time.Millisecond)
	}
	stop, tick := make(chan struct{}), lockHeartbeatTick
	go func() {
		t := time.NewTicker(tick)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case now := <-t.C:
				_ = os.Chtimes(path, now, now)
			}
		}
	}()
	defer func() {
		close(stop)
		_ = os.Remove(path)
	}()
	return fn()
}
