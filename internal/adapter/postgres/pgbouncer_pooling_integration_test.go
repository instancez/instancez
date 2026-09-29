//go:build integration

package postgres_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"

	"github.com/instancez/instancez/internal/adapter/postgres"
	"github.com/instancez/instancez/internal/domain"
)

// startPgBouncerPool boots a postgres container and a pgbouncer container in
// front of it (pool_mode=transaction, default_pool_size=1: exactly one real
// backend connection, so any two concurrent clients must share it), wired on
// a private docker network, and creates the instancez_owner role. It returns
// a DSN pointed at pgbouncer's mapped host port.
func startPgBouncerPool(t *testing.T) (bouncerDSN string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	nw, err := network.New(ctx)
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = nw.Remove(context.Background()) })

	pg, err := tc.Run(ctx, "postgres:16-alpine",
		tc.WithEnv(map[string]string{
			"POSTGRES_USER":     "instancez_owner",
			"POSTGRES_PASSWORD": "instancez_test",
			"POSTGRES_DB":       "instancez_test",
		}),
		tc.WithExposedPorts("5432/tcp"),
		network.WithNetwork([]string{"pg"}, nw),
		tc.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })

	bouncer, err := tc.Run(ctx, "edoburu/pgbouncer:latest",
		tc.WithEnv(map[string]string{
			"DATABASE_URL":      "postgres://instancez_owner:instancez_test@pg:5432/instancez_test",
			"AUTH_TYPE":         "plain",
			"POOL_MODE":         "transaction",
			"DEFAULT_POOL_SIZE": "1",
			"MAX_CLIENT_CONN":   "100",
		}),
		tc.WithExposedPorts("5432/tcp"),
		network.WithNetwork([]string{"pgbouncer"}, nw),
		tc.WithWaitStrategy(wait.ForLog("process up")),
	)
	if err != nil {
		t.Fatalf("start pgbouncer: %v", err)
	}
	t.Cleanup(func() { _ = bouncer.Terminate(context.Background()) })

	host, err := bouncer.Host(ctx)
	if err != nil {
		t.Fatalf("bouncer host: %v", err)
	}
	port, err := bouncer.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatalf("bouncer port: %v", err)
	}
	return fmt.Sprintf("postgres://instancez_owner:instancez_test@%s:%s/instancez_test?sslmode=disable", host, port.Port())
}

// hijackRaw opens a connection through the pooler and takes over the raw
// wire protocol, so the test can control the exact Parse/Describe/Bind
// sequencing that pgx's QueryExecModeDescribeExec produces. Plain
// error-returning (no *testing.T): it's called from the competitor goroutine
// below, and t.Fatalf/FailNow must only ever be called from the goroutine
// running the test.
func hijackRaw(ctx context.Context, dsn string) (*pgconn.HijackedConn, error) {
	pc, err := pgconn.Connect(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("raw connect: %w", err)
	}
	if err := pc.SyncConn(ctx); err != nil {
		return nil, fmt.Errorf("raw sync: %w", err)
	}
	hc, err := pc.Hijack()
	if err != nil {
		return nil, fmt.Errorf("hijack: %w", err)
	}
	return hc, nil
}

// drainToReady reads backend messages until ReadyForQuery. Plain
// error-returning for the same reason as hijackRaw.
func drainToReady(fe *pgproto3.Frontend) ([]pgproto3.BackendMessage, error) {
	var msgs []pgproto3.BackendMessage
	for {
		msg, err := fe.Receive()
		if err != nil {
			return msgs, fmt.Errorf("receive: %w", err)
		}
		msgs = append(msgs, msg)
		if _, ok := msg.(*pgproto3.ReadyForQuery); ok {
			return msgs, nil
		}
	}
}

func mustExec(t *testing.T, pc *pgconn.PgConn, sql string) {
	t.Helper()
	if _, err := pc.Exec(context.Background(), sql).ReadAll(); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// startBackendStealer runs, in the background, a tight loop of full
// extended-protocol queries (Parse+Bind+Execute+Sync, unnamed) against a
// 4-column table — a different shape than the 5-column
// _instancez_migrations queries the foreground uses. Against a
// default_pool_size=1 pooler this repeatedly steals the one real backend out
// from under the foreground's own two-round-trip DescribeExec queries.
// Returns a stop func and a counter of completed steal attempts, so the
// caller can assert real contention actually happened.
func startBackendStealer(t *testing.T, ctx context.Context, dsn string) (stop func(), attempts *atomic.Int64) {
	t.Helper()
	done := make(chan struct{})
	var wg sync.WaitGroup
	var once sync.Once
	attempts = &atomic.Int64{}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			hc, err := hijackRaw(ctx, dsn)
			if err != nil {
				t.Errorf("competitor hijack: %v", err)
				continue
			}
			fe := hc.Frontend
			fe.SendParse(&pgproto3.Parse{Query: "SELECT a,b,c,d FROM competitor4 LIMIT 1"})
			fe.SendBind(&pgproto3.Bind{})
			fe.SendExecute(&pgproto3.Execute{})
			fe.SendSync(&pgproto3.Sync{})
			if err := fe.Flush(); err != nil {
				t.Errorf("competitor flush: %v", err)
				_ = hc.Conn.Close()
				continue
			}
			if _, err := drainToReady(fe); err != nil {
				t.Errorf("competitor drain: %v", err)
				_ = hc.Conn.Close()
				continue
			}
			_ = hc.Conn.Close()
			attempts.Add(1)
		}
	}()

	return func() {
		once.Do(func() {
			close(done)
			wg.Wait()
		})
	}, attempts
}

// TestDBMethods_SurviveTransactionPoolingUnderContention is the regression
// guard for the reported production bug ("harden ... get_last_migration:
// ERROR: bind message has 5 result formats but query has 4 columns
// (SQLSTATE 08P01)"): GetLastMigration, QueryRow, Query, and Exec must not
// surface SQLSTATE 08P01 even when a competing connection is constantly
// stealing the pooler's single backend with a differently-shaped query,
// because each of them now runs its query inside an explicit transaction
// (see withTx and GetLastMigration in pool.go) instead of a bare pool call —
// pinning the backend for the whole Parse/Describe/Bind/Execute exchange.
//
// Reverting that fix reproduces the exact reported error on this test within
// the first few iterations (verified manually while developing this test).
func TestDBMethods_SurviveTransactionPoolingUnderContention(t *testing.T) {
	dsn := startPgBouncerPool(t)
	ctx := context.Background()

	setup, err := pgconn.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("setup connect: %v", err)
	}
	mustExec(t, setup, `CREATE TABLE _instancez_migrations (
		id BIGSERIAL PRIMARY KEY,
		checksum TEXT NOT NULL,
		sql TEXT NOT NULL,
		config_json TEXT NOT NULL DEFAULT '{}',
		applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`)
	mustExec(t, setup, `INSERT INTO _instancez_migrations (checksum, sql, config_json) VALUES ('c1', 'select 1', '{}')`)
	mustExec(t, setup, "CREATE TABLE competitor4(a int, b int, c int, d int)")
	mustExec(t, setup, "INSERT INTO competitor4 VALUES (1,2,3,4)")
	_ = setup.Close(ctx)

	db, err := postgres.NewOwner(ctx, dsn, domain.PoolConfig{Max: 4, Min: 1})
	if err != nil {
		t.Fatalf("owner pool: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	stop, attempts := startBackendStealer(t, ctx, dsn)
	defer stop()

	const selectSQL = `SELECT id, checksum, sql, config_json, applied_at FROM _instancez_migrations ORDER BY id DESC LIMIT 1`
	const iterations = 100
	for i := 0; i < iterations; i++ {
		last, err := db.GetLastMigration(ctx)
		if err != nil {
			t.Fatalf("GetLastMigration iteration %d: %v", i, err)
		}
		if last == nil || last.Checksum != "c1" {
			t.Fatalf("GetLastMigration iteration %d: unexpected result %+v", i, last)
		}

		row, err := db.QueryRow(ctx, selectSQL)
		if err != nil {
			t.Fatalf("QueryRow iteration %d: %v", i, err)
		}
		if row == nil || row["checksum"] != "c1" {
			t.Fatalf("QueryRow iteration %d: unexpected result %+v", i, row)
		}

		rows, err := db.Query(ctx, selectSQL)
		if err != nil {
			t.Fatalf("Query iteration %d: %v", i, err)
		}
		if len(rows) != 1 || rows[0]["checksum"] != "c1" {
			t.Fatalf("Query iteration %d: unexpected result %+v", i, rows)
		}

		// Exec with a bound parameter exercises the same DescribeExec path as
		// Query/QueryRow (a zero-arg Exec would be immune; see ExecDDL).
		n, err := db.Exec(ctx, `UPDATE _instancez_migrations SET config_json = $1 WHERE id = $2`, "{}", last.ID)
		if err != nil {
			t.Fatalf("Exec iteration %d: %v", i, err)
		}
		if n != 1 {
			t.Fatalf("Exec iteration %d: expected 1 row affected, got %d", i, n)
		}
	}

	stop()
	if attempts.Load() == 0 {
		t.Fatal("competitor never completed a single steal attempt — this test didn't actually exercise contention")
	}
}
