package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/instancez/instancez/internal/domain"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	dryRunLockTimeout      = "2s"
	dryRunStatementTimeout = "30s"
)

// DryRun runs the migration, then re-creates every rpc with body checking on, inside a transaction it always rolls back.
func (m *Migrator) DryRun(ctx context.Context, cfg *domain.Config) error {
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("dry run: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

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

	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("dry run: marshal config: %w", err)
	}
	checksum := fmt.Sprintf("%x", sha256.Sum256(configJSON))

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
		return fmt.Errorf("dry run: %w", err)
	}
	if last != nil && last.Checksum == checksum {
		return nil
	}

	var oldCfg *domain.Config
	if last != nil && last.ConfigJSON != "" && last.ConfigJSON != "{}" {
		oldCfg = &domain.Config{}
		if json.Unmarshal([]byte(last.ConfigJSON), oldCfg) != nil {
			oldCfg = nil
		}
	}

	gate := *m
	gate.allowDestructive = true
	stmts, err := gate.planStatements(ctx, tx, oldCfg, cfg)
	if err != nil {
		return fmt.Errorf("dry run: plan: %w", err)
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
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "42704" {
		msg += "\n  hint: provision roles first (inz dev, or INSTANCEZ_DATABASE_URL superuser)"
	}
	return errors.New(msg)
}
