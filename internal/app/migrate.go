// Package app contains application services (use cases) that orchestrate domain logic.
package app

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"

	"github.com/saedx1/ultrabase/internal/domain"
)

// Migrator generates and applies DDL migrations from config.
type Migrator struct {
	db domain.Database
}

func NewMigrator(db domain.Database) *Migrator {
	return &Migrator{db: db}
}

// Plan generates DDL statements to bring the DB in sync with the config.
// It introspects the current DB state and diffs against the config.
func (m *Migrator) Plan(ctx context.Context, cfg *domain.Config) (string, error) {
	var ddl []string

	// Extensions
	for _, ext := range cfg.Extensions {
		ddl = append(ddl, fmt.Sprintf("CREATE EXTENSION IF NOT EXISTS %s;", ext))
	}

	// Users table (from auth config)
	if cfg.Auth != nil {
		ddl = append(ddl, generateUsersTable(cfg.Auth)...)
	}

	// Tables in dependency order (FKs reference other tables)
	ordered := orderTables(cfg.Tables)
	for _, name := range ordered {
		table := cfg.Tables[name]
		// Generate CREATE TABLE IF NOT EXISTS (idempotent)
		ddl = append(ddl, generateTable(name, table, cfg.Tables)...)

		// Auto-diff: check for column additions/changes on existing tables
		if m.db != nil {
			exists, err := m.tableExists(ctx, name)
			if err == nil && exists {
				existing, err := m.introspectTable(ctx, name)
				if err == nil && len(existing) > 0 {
					alterDDL := diffTable(name, existing, table)
					ddl = append(ddl, alterDDL...)
				}
			}
		}
	}

	// Storage metadata table
	if len(cfg.Storage) > 0 {
		ddl = append(ddl, generateObjectsTable()...)
	}

	// Events table
	if len(cfg.On) > 0 {
		ddl = append(ddl, generateEventsTable()...)
	}

	// RLS policies
	if cfg.Auth != nil {
		ddl = append(ddl, generateRLSFunctions()...)
	}
	for _, name := range ordered {
		table := cfg.Tables[name]
		ddl = append(ddl, generateRLSPolicies(name, table)...)
	}
	for bucketName, bucket := range cfg.Storage {
		ddl = append(ddl, generateStorageRLS(bucketName, bucket)...)
	}

	// Search indexes
	for _, name := range ordered {
		table := cfg.Tables[name]
		ddl = append(ddl, generateSearch(name, table)...)
	}

	if len(ddl) == 0 {
		return "", nil
	}
	return strings.Join(ddl, "\n\n"), nil
}

// Apply generates and executes the migration, recording it in the history table.
func (m *Migrator) Apply(ctx context.Context, cfg *domain.Config) error {
	if err := m.db.EnsureMigrationsTable(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	sql, err := m.Plan(ctx, cfg)
	if err != nil {
		return fmt.Errorf("migrate plan: %w", err)
	}
	if sql == "" {
		return nil
	}

	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(sql)))

	// Check if this exact migration was already applied.
	last, err := m.db.GetLastMigration(ctx)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if last != nil && last.Checksum == checksum {
		return nil // already applied
	}

	if err := m.db.ExecDDL(ctx, sql); err != nil {
		return fmt.Errorf("migrate exec: %w", err)
	}

	if err := m.db.RecordMigration(ctx, checksum, sql); err != nil {
		return fmt.Errorf("migrate record: %w", err)
	}

	return nil
}

func generateUsersTable(auth *domain.Auth) []string {
	var ddl []string

	// pgcrypto provides gen_random_uuid(); safe to request idempotently.
	ddl = append(ddl, `CREATE EXTENSION IF NOT EXISTS pgcrypto;`)

	var cols []string
	// UUID primary key mirrors GoTrue (auth.users.id uuid).
	cols = append(cols, "id UUID PRIMARY KEY DEFAULT gen_random_uuid()")
	cols = append(cols, "email TEXT NOT NULL UNIQUE")
	cols = append(cols, "password_hash TEXT")
	cols = append(cols, "email_verified BOOLEAN NOT NULL DEFAULT FALSE")
	// GoTrue surfaces these timestamps on the user object.
	cols = append(cols, "email_confirmed_at TIMESTAMPTZ")
	cols = append(cols, "last_sign_in_at TIMESTAMPTZ")
	// Arbitrary app/user metadata blobs, read into JWT claims and the
	// /auth/v1/user response. raw_app_meta_data is server-managed; clients
	// can only write raw_user_meta_data via signup data / updateUser data.
	cols = append(cols, "raw_app_meta_data JSONB NOT NULL DEFAULT '{}'::jsonb")
	cols = append(cols, "raw_user_meta_data JSONB NOT NULL DEFAULT '{}'::jsonb")
	cols = append(cols, "created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()")
	cols = append(cols, "updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()")

	// Custom auth fields are promoted to top-level columns so existing
	// YAML-driven projects keep working. They're also surfaced under
	// user_metadata in the GoTrue response.
	names := sortedKeys(auth.Fields)
	for _, name := range names {
		field := auth.Fields[name]
		cols = append(cols, formatColumn(name, field))
	}

	ddl = append(ddl, fmt.Sprintf("CREATE TABLE IF NOT EXISTS users (\n  %s\n);", strings.Join(cols, ",\n  ")))

	// JWT signing keys. Managed by app.JWTKeyManager; ultrabase generates
	// a random HS256 key on first startup and never exposes it in YAML.
	ddl = append(ddl, `CREATE TABLE IF NOT EXISTS _auth_jwt_keys (
  kid TEXT PRIMARY KEY,
  secret BYTEA NOT NULL,
  algorithm TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  retired_at TIMESTAMPTZ
);`)

	// User identities table for OAuth
	ddl = append(ddl, `CREATE TABLE IF NOT EXISTS _user_identities (
  id BIGSERIAL PRIMARY KEY,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  provider TEXT NOT NULL,
  provider_user_id TEXT NOT NULL,
  identity_data JSONB NOT NULL DEFAULT '{}'::jsonb,
  email TEXT,
  last_sign_in_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(provider, provider_user_id)
);`)

	// Refresh tokens table
	if auth.RefreshTokens {
		ddl = append(ddl, `CREATE TABLE IF NOT EXISTS _refresh_tokens (
  id BIGSERIAL PRIMARY KEY,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token TEXT NOT NULL UNIQUE,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`)
	}

	// Email verifications table (for verify email + password reset tokens)
	if auth.Email != nil {
		ddl = append(ddl, `CREATE TABLE IF NOT EXISTS _auth_email_verifications (
  id BIGSERIAL PRIMARY KEY,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token TEXT NOT NULL UNIQUE,
  purpose TEXT NOT NULL DEFAULT 'signup',
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`)
	}

	return ddl
}

func generateTable(name string, table domain.Table, allTables map[string]domain.Table) []string {
	var ddl []string
	var cols []string
	var constraints []string

	// Sort field names for deterministic output, but put PK first.
	fieldNames := sortedKeys(table.Fields)
	sort.SliceStable(fieldNames, func(i, j int) bool {
		fi := table.Fields[fieldNames[i]]
		fj := table.Fields[fieldNames[j]]
		if fi.PrimaryKey != fj.PrimaryKey {
			return fi.PrimaryKey
		}
		return false
	})

	for _, fname := range fieldNames {
		field := table.Fields[fname]
		cols = append(cols, formatColumn(fname, field))

		// FK constraint
		if field.ForeignKey != nil {
			parts := strings.SplitN(field.ForeignKey.References, ".", 2)
			if len(parts) == 2 {
				onDelete := "RESTRICT"
				if field.ForeignKey.OnDelete != "" {
					onDelete = strings.ToUpper(strings.ReplaceAll(field.ForeignKey.OnDelete, "_", " "))
				}
				constraints = append(constraints,
					fmt.Sprintf("FOREIGN KEY (%s) REFERENCES %s(%s) ON DELETE %s",
						fname, parts[0], parts[1], onDelete))
			}
		}

		// CHECK constraints from enum
		if len(field.Enum) > 0 {
			quoted := make([]string, len(field.Enum))
			for i, v := range field.Enum {
				quoted[i] = "'" + v + "'"
			}
			constraints = append(constraints,
				fmt.Sprintf("CHECK (%s IN (%s))", fname, strings.Join(quoted, ", ")))
		}

		// CHECK from pattern
		if field.Pattern != "" {
			constraints = append(constraints,
				fmt.Sprintf("CHECK (%s ~ '%s')", fname, field.Pattern))
		}

		// CHECK from min/max
		if field.Min != nil {
			constraints = append(constraints,
				fmt.Sprintf("CHECK (%s >= %g)", fname, *field.Min))
		}
		if field.Max != nil {
			constraints = append(constraints,
				fmt.Sprintf("CHECK (%s <= %g)", fname, *field.Max))
		}

		// Raw check
		if field.Check != "" {
			constraints = append(constraints,
				fmt.Sprintf("CHECK (%s)", field.Check))
		}

		// Unique
		if field.Unique {
			constraints = append(constraints, fmt.Sprintf("UNIQUE (%s)", fname))
		}
	}

	allParts := append(cols, constraints...)
	ddl = append(ddl, fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (\n  %s\n);",
		name, strings.Join(allParts, ",\n  ")))

	// REPLICA IDENTITY FULL for WAL CDC
	ddl = append(ddl, fmt.Sprintf("ALTER TABLE %s REPLICA IDENTITY FULL;", name))

	// Indexes
	for _, idx := range table.Indexes {
		indexName := fmt.Sprintf("idx_%s_%s", name, strings.Join(idx.Columns, "_"))
		unique := ""
		if idx.Unique {
			unique = "UNIQUE "
		}
		where := ""
		if idx.Where != "" {
			where = " WHERE " + idx.Where
		}
		ddl = append(ddl, fmt.Sprintf("CREATE %sINDEX IF NOT EXISTS %s ON %s (%s)%s;",
			unique, indexName, name, strings.Join(idx.Columns, ", "), where))
	}

	return ddl
}

func formatColumn(name string, field domain.Field) string {
	typ := field.Type
	if typ == "" && field.ForeignKey != nil {
		// Infer FK column type from the referenced table's PK. users.id is
		// UUID (GoTrue contract); everything else defaults to BIGINT to
		// match the historical bigserial PK convention.
		typ = "BIGINT"
		if strings.EqualFold(field.ForeignKey.References, "users.id") {
			typ = "UUID"
		}
	}

	parts := []string{name, typ}

	if field.PrimaryKey {
		parts = append(parts, "PRIMARY KEY")
	}
	if field.Required {
		parts = append(parts, "NOT NULL")
	}
	if field.Default != nil {
		parts = append(parts, "DEFAULT "+formatDefault(field.Default, typ))
	}

	return strings.Join(parts, " ")
}

func formatDefault(val any, colType string) string {
	switch v := val.(type) {
	case string:
		lower := strings.ToLower(v)
		// SQL functions/keywords — pass through as-is
		if strings.Contains(v, "(") || lower == "current_date" || lower == "current_time" {
			// Map our shorthand to real Postgres functions
			switch lower {
			case "uuid_v7()":
				return "gen_random_uuid()" // pgcrypto; uuid v7 needs extension
			case "uuid_v4()":
				return "gen_random_uuid()"
			default:
				return v
			}
		}
		// String literal
		return "'" + strings.ReplaceAll(v, "'", "''") + "'"
	case bool:
		if v {
			return "TRUE"
		}
		return "FALSE"
	case int:
		return fmt.Sprintf("%d", v)
	case int64:
		return fmt.Sprintf("%d", v)
	case float64:
		return fmt.Sprintf("%g", v)
	default:
		return fmt.Sprintf("'%v'", v)
	}
}

func generateObjectsTable() []string {
	return []string{`CREATE TABLE IF NOT EXISTS _objects (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  bucket_id TEXT NOT NULL,
  size BIGINT NOT NULL,
  mime TEXT NOT NULL,
  uploaded_by UUID REFERENCES users(id),
  uploaded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  metadata JSONB
);`}
}

func generateEventsTable() []string {
	return []string{`CREATE TABLE IF NOT EXISTS _events (
  id BIGSERIAL PRIMARY KEY,
  source_id TEXT NOT NULL,
  trigger_name TEXT NOT NULL,
  event_name TEXT NOT NULL,
  table_name TEXT NOT NULL,
  operation TEXT NOT NULL,
  payload JSONB NOT NULL,
  delivery JSONB NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending',
  attempts INTEGER NOT NULL DEFAULT 0,
  next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  last_error TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  delivered_at TIMESTAMPTZ,
  UNIQUE (source_id, trigger_name)
);

CREATE INDEX IF NOT EXISTS idx_events_pending ON _events (next_attempt_at) WHERE status = 'pending';`}
}

func generateRLSFunctions() []string {
	return []string{
		// Ensure auth schema exists before creating functions in it.
		`CREATE SCHEMA IF NOT EXISTS auth;`,

		// auth.uid() returns the authenticated user's UUID, or NULL for
		// anonymous requests. Matches Supabase's GoTrue signature.
		`CREATE OR REPLACE FUNCTION auth.uid() RETURNS UUID AS $$
  SELECT NULLIF(current_setting('app.user_id', true), '')::UUID;
$$ LANGUAGE SQL STABLE;`,

		// auth.role() returns 'anon' | 'authenticated' | 'service_role'.
		// Populated by the HTTP middleware from the JWT `role` claim.
		`CREATE OR REPLACE FUNCTION auth.role() RETURNS TEXT AS $$
  SELECT COALESCE(NULLIF(current_setting('app.role', true), ''), 'anon');
$$ LANGUAGE SQL STABLE;`,

		// auth.email() returns the authenticated user's email from the JWT.
		`CREATE OR REPLACE FUNCTION auth.email() RETURNS TEXT AS $$
  SELECT NULLIF(current_setting('app.email', true), '');
$$ LANGUAGE SQL STABLE;`,

		// auth.jwt() returns the raw JWT claims as jsonb, matching the
		// Supabase helper. Decodes the payload from the encoded token that
		// the middleware stashed in app.jwt.
		`CREATE OR REPLACE FUNCTION auth.jwt() RETURNS JSONB AS $$
  SELECT CASE
    WHEN NULLIF(current_setting('app.jwt', true), '') IS NULL THEN NULL
    ELSE convert_from(
      decode(
        translate(
          split_part(current_setting('app.jwt', true), '.', 2),
          '-_', '+/'
        ) ||
        repeat('=', (4 - length(split_part(current_setting('app.jwt', true), '.', 2)) % 4) % 4),
        'base64'
      ),
      'utf8'
    )::jsonb
  END;
$$ LANGUAGE SQL STABLE;`,

		// Legacy helper kept for backwards compatibility with any existing
		// RLS policies written against older ultrabase versions.
		`CREATE OR REPLACE FUNCTION auth.is_authenticated() RETURNS BOOLEAN AS $$
  SELECT auth.role() = 'authenticated' OR auth.role() = 'service_role';
$$ LANGUAGE SQL STABLE;`,
	}
}

func rlsPolicyTypeClause(policy domain.RLSPolicy) string {
	if policy.Type == "restrictive" {
		return " AS RESTRICTIVE"
	}
	return " AS PERMISSIVE"
}

func generateRLSPolicies(tableName string, table domain.Table) []string {
	if len(table.RLS) == 0 {
		return nil
	}

	var ddl []string
	ddl = append(ddl, fmt.Sprintf("ALTER TABLE %s ENABLE ROW LEVEL SECURITY;", tableName))
	ddl = append(ddl, fmt.Sprintf("ALTER TABLE %s FORCE ROW LEVEL SECURITY;", tableName))

	for i, policy := range table.RLS {
		typeClause := rlsPolicyTypeClause(policy)
		for _, op := range policy.Operations {
			policyName := fmt.Sprintf("%s_%s_%d", tableName, op, i)
			pgOp := strings.ToUpper(op)

			if op == "insert" {
				ddl = append(ddl, fmt.Sprintf(
					"CREATE POLICY %s ON %s%s FOR %s WITH CHECK (%s);",
					policyName, tableName, typeClause, pgOp, policy.Check))
			} else {
				ddl = append(ddl, fmt.Sprintf(
					"CREATE POLICY %s ON %s%s FOR %s USING (%s);",
					policyName, tableName, typeClause, pgOp, policy.Check))
			}
		}
	}

	return ddl
}

func generateStorageRLS(bucketName string, bucket domain.Bucket) []string {
	if len(bucket.RLS) == 0 && !bucket.Public {
		return nil
	}

	var ddl []string

	// Public bucket: allow SELECT without auth
	if bucket.Public {
		ddl = append(ddl, fmt.Sprintf(
			"CREATE POLICY %s_public_select ON _objects FOR SELECT USING (bucket_id = '%s');",
			bucketName, bucketName))
	}

	for i, policy := range bucket.RLS {
		typeClause := rlsPolicyTypeClause(policy)
		for _, op := range policy.Operations {
			policyName := fmt.Sprintf("storage_%s_%s_%d", bucketName, op, i)
			pgOp := strings.ToUpper(op)
			scopedCheck := fmt.Sprintf("bucket_id = '%s' AND (%s)", bucketName, policy.Check)

			if op == "insert" {
				ddl = append(ddl, fmt.Sprintf(
					"CREATE POLICY %s ON _objects%s FOR %s WITH CHECK (%s);",
					policyName, typeClause, pgOp, scopedCheck))
			} else {
				ddl = append(ddl, fmt.Sprintf(
					"CREATE POLICY %s ON _objects%s FOR %s USING (%s);",
					policyName, typeClause, pgOp, scopedCheck))
			}
		}
	}

	return ddl
}

func generateSearch(tableName string, table domain.Table) []string {
	if len(table.Searchable) == 0 {
		return nil
	}

	searchConfig := table.SearchConfig
	if searchConfig == "" {
		searchConfig = "english"
	}

	// Build tsvector expression with COALESCE for nullable columns
	parts := make([]string, len(table.Searchable))
	for i, col := range table.Searchable {
		parts[i] = fmt.Sprintf("to_tsvector('%s', COALESCE(%s, ''))", searchConfig, col)
	}
	tsvExpr := strings.Join(parts, " || ")

	colName := "_tsv"

	var ddl []string
	ddl = append(ddl,
		fmt.Sprintf("ALTER TABLE %s ADD COLUMN IF NOT EXISTS %s TSVECTOR GENERATED ALWAYS AS (%s) STORED;",
			tableName, colName, tsvExpr))
	ddl = append(ddl,
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_tsv ON %s USING GIN (%s);",
			tableName, tableName, colName))

	return ddl
}

// orderTables does a topological sort based on FK dependencies.
// Tables with no FK deps come first.
func orderTables(tables map[string]domain.Table) []string {
	deps := make(map[string][]string)
	for name, table := range tables {
		for _, field := range table.Fields {
			if field.ForeignKey != nil {
				parts := strings.SplitN(field.ForeignKey.References, ".", 2)
				if len(parts) == 2 && parts[0] != name && parts[0] != "users" {
					if _, exists := tables[parts[0]]; exists {
						deps[name] = append(deps[name], parts[0])
					}
				}
			}
		}
	}

	var result []string
	visited := make(map[string]bool)
	visiting := make(map[string]bool)

	var visit func(name string)
	visit = func(name string) {
		if visited[name] {
			return
		}
		if visiting[name] {
			// Circular dependency — just add it (Postgres will handle with deferred constraints)
			return
		}
		visiting[name] = true
		for _, dep := range deps[name] {
			visit(dep)
		}
		visiting[name] = false
		visited[name] = true
		result = append(result, name)
	}

	// Sort names for deterministic output
	names := sortedKeys(tables)
	for _, name := range names {
		visit(name)
	}

	return result
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
