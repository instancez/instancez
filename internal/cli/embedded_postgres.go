package cli

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"strings"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
)

// startEmbeddedPostgres starts an embedded Postgres 16 process using
// opts.pgDataDir as the persistent data directory. If opts.resetPG is true,
// the data directory is wiped first. Returns a stop func (call on shutdown)
// and the superuser DSN to inject as INSTANCEZ_DATABASE_URL.
func startEmbeddedPostgres(opts devOptions) (stop func(), dsn string, err error) {
	port, err := freePort()
	if err != nil {
		return nil, "", fmt.Errorf("find free port for embedded Postgres: %w", err)
	}

	if opts.resetPG {
		if err := os.RemoveAll(opts.pgDataDir); err != nil {
			return nil, "", fmt.Errorf("reset pgdata: %w", err)
		}
	}

	// The library defaults its logger to os.Stdout, which floods the dev banner
	// with initdb/pg_ctl chatter and PG's own server log. Capture it instead and
	// surface it only when Start fails.
	// ponytail: buffer grows with PG's session logging; fine for a dev session.
	var pgLog bytes.Buffer
	pg := embeddedpostgres.NewDatabase(
		embeddedpostgres.DefaultConfig().
			Version(embeddedpostgres.V16).
			DataPath(opts.pgDataDir).
			Port(port).
			Logger(&pgLog),
	)

	if err := pg.Start(); err != nil {
		return nil, "", fmt.Errorf("start embedded Postgres: %w\n%s", err, pgLog.String())
	}

	superuserDSN := fmt.Sprintf("postgres://postgres:postgres@localhost:%d/postgres?sslmode=disable", port)
	return func() {
		if err := pg.Stop(); err != nil {
			fmt.Fprintf(os.Stderr, "embedded Postgres stop: %v\n", err)
		}
	}, superuserDSN, nil
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
		return "./pgdata may be corrupt, left by a different Postgres version, or still in use by a stale process — stop any running instance, or wipe and reinitialize with: inz dev --embedded-pg --reset-pg"

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
