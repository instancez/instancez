package domain

import "context"

// Database is the port for all database operations.
type Database interface {
	// Lifecycle
	Close() error
	Ping(ctx context.Context) error

	// Migrations
	EnsureMigrationsTable(ctx context.Context) error
	GetLastMigration(ctx context.Context) (*Migration, error)
	RecordMigration(ctx context.Context, checksum, sql string) error
	ExecDDL(ctx context.Context, sql string) error

	// CRUD (called by PostgREST-compatible query engine)
	Query(ctx context.Context, query string, args ...any) ([]map[string]any, error)
	QueryRow(ctx context.Context, query string, args ...any) (map[string]any, error)
	Exec(ctx context.Context, query string, args ...any) (int64, error) // returns affected rows

	// RLS context — sets session variables before query execution
	WithRLS(ctx context.Context, session Session) (context.Context, error)

	// Transactions
	Begin(ctx context.Context) (Tx, error)
}

// Tx represents a database transaction.
type Tx interface {
	Query(ctx context.Context, query string, args ...any) ([]map[string]any, error)
	QueryRow(ctx context.Context, query string, args ...any) (map[string]any, error)
	Exec(ctx context.Context, query string, args ...any) (int64, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}
