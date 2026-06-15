package cli

import (
	"fmt"
	"net"
	"os"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
)

// startEmbeddedPostgres starts an embedded Postgres 16 process using
// opts.pgDataDir as the persistent data directory. If opts.resetPG is true,
// the data directory is wiped first. Returns a stop func (call on shutdown)
// and the superuser DSN to inject as INSTANCEZ_DATABASE_URL.
func startEmbeddedPostgres(opts devOptions) (stop func(), dsn string, err error) {
	if opts.resetPG {
		if err := os.RemoveAll(opts.pgDataDir); err != nil {
			return nil, "", fmt.Errorf("reset pgdata: %w", err)
		}
	}

	port, err := freePort()
	if err != nil {
		return nil, "", fmt.Errorf("find free port for embedded Postgres: %w", err)
	}

	pg := embeddedpostgres.NewDatabase(
		embeddedpostgres.DefaultConfig().
			Version(embeddedpostgres.V16).
			DataPath(opts.pgDataDir).
			Port(port),
	)

	if err := pg.Start(); err != nil {
		return nil, "", fmt.Errorf("start embedded Postgres: %w\nhint: if this is a platform support error, use INSTANCEZ_DATABASE_URL with a full Postgres installation instead", err)
	}

	superuserDSN := fmt.Sprintf("postgres://postgres:postgres@localhost:%d/postgres?sslmode=disable", port)
	return func() { _ = pg.Stop() }, superuserDSN, nil
}

// freePort returns an available TCP port on localhost.
func freePort() (uint32, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return uint32(l.Addr().(*net.TCPAddr).Port), nil
}
