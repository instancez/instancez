package app

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/instancez/instancez/internal/domain"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	dryRunLockTimeout      = "2s"
	dryRunStatementTimeout = "30s"
)

// DryRunTimeout bounds the whole dry run, since statement_timeout only bounds each statement.
const DryRunTimeout = 2 * time.Minute

// DryRun runs the migration, then re-creates every rpc with body checking on, inside a transaction it always rolls back.
// An in-sync config skips the plan but still checks the rpc bodies.
func (m *Migrator) DryRun(ctx context.Context, cfg *domain.Config) error {
	ctx, cancel := context.WithTimeout(ctx, DryRunTimeout)
	defer cancel()
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("dry run: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	for _, s := range []string{
		"SET LOCAL lock_timeout = '" + dryRunLockTimeout + "'",
		"SET LOCAL statement_timeout = '" + dryRunStatementTimeout + "'",
		"SET LOCAL check_function_bodies = off",
	} {
		if _, err := tx.Exec(ctx, s); err != nil {
			return fmt.Errorf("dry run: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", domain.MigrationLockKey); err != nil {
		return fmt.Errorf("dry run: migration lock: %w", err)
	}

	_, checksum, err := marshalConfig(cfg)
	if err != nil {
		return fmt.Errorf("dry run: %w", err)
	}

	var last *domain.Migration
	row, err := tx.QueryRow(ctx, "SELECT to_regclass('_instancez_migrations') IS NOT NULL AS ok")
	if err != nil {
		return fmt.Errorf("dry run: %w", err)
	}
	if ok, _ := row["ok"].(bool); ok {
		if last, err = lastMigration(ctx, tx); err != nil {
			return fmt.Errorf("dry run: %w", err)
		}
	} else if _, err := tx.Exec(ctx, "CREATE TABLE _instancez_migrations (id bigserial PRIMARY KEY)"); err != nil {
		return dryRunError(0, "create migrations table", err)
	}

	var stmts []string
	if last == nil || last.Checksum != checksum {
		gate := *m
		gate.allowDestructive = true
		if stmts, err = gate.planStatements(ctx, tx, previousConfig(last), cfg); err != nil {
			return fmt.Errorf("dry run: plan: %w", err)
		}
	}
	for i, s := range stmts {
		if _, err := tx.Exec(ctx, s); err != nil {
			return dryRunError(i+1, s, err)
		}
	}

	if _, err := tx.Exec(ctx, "SELECT set_config('check_function_bodies', 'on', true)"); err != nil {
		return fmt.Errorf("dry run: %w", err)
	}
	for i, n := range slices.Sorted(maps.Keys(cfg.RPC)) {
		s := generateRPCFunction(n, cfg.RPC[n])
		if _, err := tx.Exec(ctx, s); err != nil {
			return dryRunError(len(stmts)+i+1, s, err)
		}
	}
	return nil
}

func dryRunError(n int, stmt string, err error) error {
	first, _, _ := strings.Cut(strings.TrimSpace(stmt), "\n")
	msg := fmt.Sprintf("dry run: statement %d: %v\n  %s", n, err, first)
	if n == 0 {
		msg = fmt.Sprintf("dry run: %s: %v", stmt, err)
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "42704":
			msg += "\n  hint: provision roles first (inz dev, or INSTANCEZ_DATABASE_URL superuser)"
		case "42501":
			msg += "\n  hint: the dry run needs an owner-class DSN that can create objects in the database"
		}
	}
	return errors.New(msg)
}
