// Package postgres implements domain.Database using pgx.
package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/saedx1/ultrabase/internal/domain"
)

// DB implements domain.Database backed by a pgx connection pool. The roles
// field is non-nil only when wrapped as a RequestDB.
type DB struct {
	pool  *pgxpool.Pool
	roles *domain.Roles
}

// NewOwner constructs the privileged pool used for migrations, seeding,
// replication slot creation, and extension installs.
func NewOwner(ctx context.Context, databaseURL string, poolCfg domain.PoolConfig) (domain.OwnerDB, error) {
	db, err := New(ctx, databaseURL, poolCfg)
	if err != nil {
		return domain.OwnerDB{}, err
	}
	return domain.OwnerDB{Database: db}, nil
}

// NewRequest constructs the per-request pool. Each transaction issues
// SET LOCAL ROLE based on the inbound session before running queries.
func NewRequest(ctx context.Context, databaseURL string, poolCfg domain.PoolConfig, roles domain.Roles) (domain.RequestDB, error) {
	if err := roles.Validate(); err != nil {
		return domain.RequestDB{}, fmt.Errorf("invalid roles config: %w", err)
	}
	db, err := New(ctx, databaseURL, poolCfg)
	if err != nil {
		return domain.RequestDB{}, err
	}
	db.roles = &roles
	return domain.RequestDB{Database: db}, nil
}

// New creates a new DB from a Postgres connection URL and pool config.
// Most callers should use NewOwner or NewRequest instead.
func New(ctx context.Context, databaseURL string, poolCfg domain.PoolConfig) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, &domain.DatabaseError{Op: "parse_url", Err: err}
	}

	if poolCfg.Max > 0 {
		cfg.MaxConns = int32(poolCfg.Max)
	}
	if poolCfg.Min > 0 {
		cfg.MinConns = int32(poolCfg.Min)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, &domain.DatabaseError{Op: "connect", Err: err}
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, &domain.DatabaseError{Op: "ping", Err: err}
	}

	return &DB{pool: pool}, nil
}

func (db *DB) Close() error {
	db.pool.Close()
	return nil
}

func (db *DB) Ping(ctx context.Context) error {
	return db.pool.Ping(ctx)
}

func (db *DB) Pool() *pgxpool.Pool {
	return db.pool
}

// EnsureMigrationsTable creates the _ultrabase_migrations table if it doesn't exist.
func (db *DB) EnsureMigrationsTable(ctx context.Context) error {
	_, err := db.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS _ultrabase_migrations (
			id BIGSERIAL PRIMARY KEY,
			checksum TEXT NOT NULL,
			sql TEXT NOT NULL,
			config_json TEXT NOT NULL DEFAULT '{}',
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	if err != nil {
		return &domain.DatabaseError{Op: "ensure_migrations_table", Err: err}
	}
	// Additive column for existing deployments that predate config storage.
	_, err = db.pool.Exec(ctx,
		`ALTER TABLE _ultrabase_migrations ADD COLUMN IF NOT EXISTS config_json TEXT NOT NULL DEFAULT '{}'`)
	if err != nil {
		return &domain.DatabaseError{Op: "ensure_migrations_table", Err: err}
	}
	return nil
}

// GetLastMigration returns the most recently applied migration, or nil if none.
func (db *DB) GetLastMigration(ctx context.Context) (*domain.Migration, error) {
	row := db.pool.QueryRow(ctx,
		`SELECT id, checksum, sql, config_json, applied_at FROM _ultrabase_migrations ORDER BY id DESC LIMIT 1`)

	var m domain.Migration
	err := row.Scan(&m.ID, &m.Checksum, &m.SQL, &m.ConfigJSON, &m.AppliedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, &domain.DatabaseError{Op: "get_last_migration", Err: err}
	}
	return &m, nil
}

// RecordMigration stores a new migration record.
func (db *DB) RecordMigration(ctx context.Context, checksum, sql, configJSON string) error {
	_, err := db.pool.Exec(ctx,
		`INSERT INTO _ultrabase_migrations (checksum, sql, config_json) VALUES ($1, $2, $3)`,
		checksum, sql, configJSON)
	if err != nil {
		return &domain.DatabaseError{Op: "record_migration", Err: err}
	}
	return nil
}

// EnsureDataTable creates the _ultrabase_data tracking table if it doesn't exist.
func (db *DB) EnsureDataTable(ctx context.Context) error {
	_, err := db.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS _ultrabase_data (
			key        TEXT PRIMARY KEY,
			table_name TEXT NOT NULL,
			source     TEXT NOT NULL,
			checksum   TEXT NOT NULL,
			row_count  INTEGER NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	if err != nil {
		return &domain.DatabaseError{Op: "ensure_data_table", Err: err}
	}
	return nil
}

// GetAppliedData returns all previously applied data import records.
func (db *DB) GetAppliedData(ctx context.Context) ([]domain.DataRecord, error) {
	rows, err := db.pool.Query(ctx,
		`SELECT key, table_name, source, checksum, row_count, applied_at FROM _ultrabase_data`)
	if err != nil {
		return nil, &domain.DatabaseError{Op: "get_applied_data", Err: err}
	}
	defer rows.Close()

	var records []domain.DataRecord
	for rows.Next() {
		var r domain.DataRecord
		if err := rows.Scan(&r.Key, &r.TableName, &r.Source, &r.Checksum, &r.RowCount, &r.AppliedAt); err != nil {
			return nil, &domain.DatabaseError{Op: "scan_data_record", Err: err}
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, &domain.DatabaseError{Op: "data_rows_iteration", Err: err}
	}
	return records, nil
}

// RecordData inserts a data import tracking record within the given transaction.
func (db *DB) RecordData(ctx context.Context, tx domain.Tx, key, tableName, source, checksum string, rowCount int) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO _ultrabase_data (key, table_name, source, checksum, row_count) VALUES ($1, $2, $3, $4, $5)`,
		key, tableName, source, checksum, rowCount)
	if err != nil {
		return &domain.DatabaseError{Op: "record_data", Err: err}
	}
	return nil
}

// ExecDDL executes raw DDL (migration SQL).
func (db *DB) ExecDDL(ctx context.Context, sql string) error {
	_, err := db.pool.Exec(ctx, sql)
	if err != nil {
		return &domain.DatabaseError{Op: "exec_ddl", Err: err}
	}
	return nil
}

// Query executes a query and returns all rows as maps.
func (db *DB) Query(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	rows, err := db.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, &domain.DatabaseError{Op: "query", Err: err}
	}
	defer rows.Close()
	return collectRows(rows)
}

// QueryRow executes a query and returns a single row as a map, or nil.
func (db *DB) QueryRow(ctx context.Context, query string, args ...any) (map[string]any, error) {
	rows, err := db.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, &domain.DatabaseError{Op: "query_row", Err: err}
	}
	defer rows.Close()

	results, err := collectRows(rows)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, nil
	}
	return results[0], nil
}

// Exec executes a statement and returns affected row count.
func (db *DB) Exec(ctx context.Context, query string, args ...any) (int64, error) {
	tag, err := db.pool.Exec(ctx, query, args...)
	if err != nil {
		return 0, &domain.DatabaseError{Op: "exec", Err: err}
	}
	return tag.RowsAffected(), nil
}

// WithRLS sets session variables for RLS enforcement within a transaction.
func (db *DB) WithRLS(ctx context.Context, session domain.Session) (context.Context, error) {
	// RLS context is applied per-transaction via SET LOCAL in the tx wrapper.
	// This stores the session in context for the Tx to pick up.
	return contextWithSession(ctx, session), nil
}

// Begin starts a new transaction. Session GUCs, request-id, and (on the
// request pool) SET LOCAL ROLE are all batched into a single Exec to keep
// the per-request hot path to one round-trip.
func (db *DB) Begin(ctx context.Context) (domain.Tx, error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return nil, &domain.DatabaseError{Op: "begin", Err: err}
	}

	if stmt := buildSessionSetup(ctx, db.roles); stmt != "" {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			tx.Rollback(ctx)
			return nil, &domain.DatabaseError{Op: "set_session_vars", Err: err}
		}
	}
	return &Tx{tx: tx}, nil
}

// buildSessionSetup composes the SET LOCAL statements for a transaction.
// Role names come from Roles which is validated at construction; literal
// values are escaped via quote(). Returns "" when nothing needs setting.
func buildSessionSetup(ctx context.Context, roles *domain.Roles) string {
	var b strings.Builder
	if session, ok := sessionFromContext(ctx); ok {
		role := session.Role
		if role == "" {
			if session.IsAuthenticated {
				role = "authenticated"
			} else {
				role = "anon"
			}
		}
		isAuthed := "false"
		if session.IsAuthenticated {
			isAuthed = "true"
		}
		fmt.Fprintf(&b, "SET LOCAL app.user_id = %s;", quote(session.UserID))
		fmt.Fprintf(&b, "SET LOCAL app.role = %s;", quote(role))
		fmt.Fprintf(&b, "SET LOCAL app.email = %s;", quote(session.Email))
		fmt.Fprintf(&b, "SET LOCAL app.jwt = %s;", quote(session.JWT))
		fmt.Fprintf(&b, "SET LOCAL app.is_authenticated = %s;", quote(isAuthed))

		if roles != nil {
			fmt.Fprintf(&b, "SET LOCAL ROLE %s;", roles.AssumableFromSession(session))
		}
	}
	if reqID := domain.RequestIDFromContext(ctx); reqID != "" {
		fmt.Fprintf(&b, "SET LOCAL request.request_id = %s;", quote(reqID))
	}
	return b.String()
}

func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// collectRows converts pgx rows into a slice of maps.
func collectRows(rows pgx.Rows) ([]map[string]any, error) {
	descs := rows.FieldDescriptions()
	var results []map[string]any

	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, &domain.DatabaseError{Op: "scan_row", Err: err}
		}
		row := make(map[string]any, len(descs))
		for i, desc := range descs {
			row[desc.Name] = values[i]
		}
		results = append(results, row)
	}

	if err := rows.Err(); err != nil {
		return nil, &domain.DatabaseError{Op: "rows_iteration", Err: err}
	}
	return results, nil
}

// Tx wraps a pgx transaction implementing domain.Tx.
type Tx struct {
	tx pgx.Tx
}

func (t *Tx) Query(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	rows, err := t.tx.Query(ctx, query, args...)
	if err != nil {
		return nil, &domain.DatabaseError{Op: "tx_query", Err: err}
	}
	defer rows.Close()
	return collectRows(rows)
}

func (t *Tx) QueryRow(ctx context.Context, query string, args ...any) (map[string]any, error) {
	rows, err := t.tx.Query(ctx, query, args...)
	if err != nil {
		return nil, &domain.DatabaseError{Op: "tx_query_row", Err: err}
	}
	defer rows.Close()

	results, err := collectRows(rows)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, nil
	}
	return results[0], nil
}

func (t *Tx) Exec(ctx context.Context, query string, args ...any) (int64, error) {
	tag, err := t.tx.Exec(ctx, query, args...)
	if err != nil {
		return 0, &domain.DatabaseError{Op: "tx_exec", Err: err}
	}
	return tag.RowsAffected(), nil
}

func (t *Tx) Commit(ctx context.Context) error {
	if err := t.tx.Commit(ctx); err != nil {
		return &domain.DatabaseError{Op: "commit", Err: err}
	}
	return nil
}

func (t *Tx) Rollback(ctx context.Context) error {
	if err := t.tx.Rollback(ctx); err != nil {
		return fmt.Errorf("rollback: %w", err)
	}
	return nil
}
