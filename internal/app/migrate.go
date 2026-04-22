// Package app contains application services (use cases) that orchestrate domain logic.
package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
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
// When oldCfg is nil (first migration), it generates the full schema.
// When oldCfg is provided, it generates only the diff between configs.
func (m *Migrator) Plan(ctx context.Context, oldCfg, newCfg *domain.Config) (string, error) {
	if oldCfg == nil {
		return planFromScratch(newCfg), nil
	}
	return planUpdate(oldCfg, newCfg), nil
}

// planFromScratch generates full DDL for a fresh database using IF NOT EXISTS /
// CREATE OR REPLACE for safety.
func planFromScratch(cfg *domain.Config) string {
	var ddl []string

	// Extensions
	for _, ext := range cfg.Extensions {
		ddl = append(ddl, fmt.Sprintf("CREATE EXTENSION IF NOT EXISTS %s;", ext))
	}

	// Users table (core auth columns + custom fields from tables.users)
	if cfg.Auth != nil {
		ddl = append(ddl, generateUsersTable(cfg.Auth, cfg.UserExtraFields())...)
	}

	// Tables in dependency order (FKs reference other tables).
	// Pass 1: CREATE TABLE (must complete before indexes
	// so that ADD COLUMN IF NOT EXISTS runs before CREATE INDEX references the column).
	ordered := orderTables(cfg.Tables)
	for _, name := range ordered {
		table := cfg.Tables[name]
		ddl = append(ddl, generateTable(name, table, cfg.Tables)...)
	}

	// Pass 2: Indexes (after all tables/columns exist).
	for _, name := range ordered {
		table := cfg.Tables[name]
		ddl = append(ddl, generateIndexes(name, table)...)
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

	// RPC functions (Postgres stored functions)
	if len(cfg.Functions) > 0 {
		fnNames := sortedKeys(cfg.Functions)
		for _, n := range fnNames {
			ddl = append(ddl, generateRPCFunction(n, cfg.Functions[n]))
		}
	}

	if len(ddl) == 0 {
		return ""
	}
	return strings.Join(ddl, "\n\n")
}

// planUpdate generates DDL for transitioning from oldCfg to newCfg.
// It produces removal DDL from the config diff, followed by additions and
// alterations, then re-emits idempotent items (indexes, RLS, search, functions).
func planUpdate(oldCfg, newCfg *domain.Config) string {
	diff := diffConfigs(oldCfg, newCfg)
	var ddl []string

	// 1. Removals (DROP TABLE, DROP COLUMN, DROP INDEX, DROP POLICY, etc.)
	ddl = append(ddl, diff.Removals...)

	// 2. Additions (new extensions, auth, tables, columns, storage, events)
	ddl = append(ddl, diff.Additions...)

	// 3. Alterations (column type changes, nullability changes)
	ddl = append(ddl, diff.Alterations...)

	// 4. Re-emit idempotent items for all current tables.
	// These use IF NOT EXISTS / CREATE OR REPLACE / DROP+CREATE patterns,
	// so they're safe to run even when nothing changed.
	ordered := orderTables(newCfg.Tables)

	// Indexes (CREATE INDEX IF NOT EXISTS)
	for _, name := range ordered {
		ddl = append(ddl, generateIndexes(name, newCfg.Tables[name])...)
	}

	// RLS functions (CREATE OR REPLACE)
	if newCfg.Auth != nil {
		ddl = append(ddl, generateRLSFunctions()...)
	}

	// RLS policies (DROP IF EXISTS + CREATE POLICY — idempotent)
	for _, name := range ordered {
		ddl = append(ddl, generateRLSPolicies(name, newCfg.Tables[name])...)
	}
	for bucketName, bucket := range newCfg.Storage {
		ddl = append(ddl, generateStorageRLS(bucketName, bucket)...)
	}

	// Search (ADD COLUMN IF NOT EXISTS + CREATE INDEX IF NOT EXISTS)
	for _, name := range ordered {
		ddl = append(ddl, generateSearch(name, newCfg.Tables[name])...)
	}

	// RPC functions (CREATE OR REPLACE FUNCTION)
	if len(newCfg.Functions) > 0 {
		fnNames := sortedKeys(newCfg.Functions)
		for _, n := range fnNames {
			ddl = append(ddl, generateRPCFunction(n, newCfg.Functions[n]))
		}
	}

	if len(ddl) == 0 {
		return ""
	}
	return strings.Join(ddl, "\n\n")
}

// Apply generates and executes the migration, recording it in the history table.
// It stores the applied config as JSON so the next run can diff against it to
// detect removed objects and avoid re-running unchanged migrations.
func (m *Migrator) Apply(ctx context.Context, cfg *domain.Config) error {
	if err := m.db.EnsureMigrationsTable(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("migrate: marshal config: %w", err)
	}
	configChecksum := fmt.Sprintf("%x", sha256.Sum256(configJSON))

	last, err := m.db.GetLastMigration(ctx)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if last != nil && last.Checksum == configChecksum {
		return nil // config unchanged
	}

	// Recover the previous config to diff against.
	var oldCfg *domain.Config
	if last != nil && last.ConfigJSON != "" && last.ConfigJSON != "{}" {
		oldCfg = &domain.Config{}
		if err := json.Unmarshal([]byte(last.ConfigJSON), oldCfg); err != nil {
			oldCfg = nil // treat as first migration if unparseable
		}
	}

	planSQL, err := m.Plan(ctx, oldCfg, cfg)
	if err != nil {
		return fmt.Errorf("migrate plan: %w", err)
	}

	if planSQL == "" {
		return nil
	}

	if err := m.db.ExecDDL(ctx, planSQL); err != nil {
		return fmt.Errorf("migrate exec: %w", err)
	}

	if err := m.db.RecordMigration(ctx, configChecksum, planSQL, string(configJSON)); err != nil {
		return fmt.Errorf("migrate record: %w", err)
	}

	return nil
}

func generateUsersTable(auth *domain.Auth, extraFields []domain.Field) []string {
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
	for _, field := range extraFields {
		cols = append(cols, formatColumn(field.Name, field))
	}

	ddl = append(ddl, fmt.Sprintf("CREATE TABLE IF NOT EXISTS users (\n  %s\n);", strings.Join(cols, ",\n  ")))

	// Additive column for anonymous sign-in support. Declared as an ALTER
	// outside the CREATE TABLE block so existing deployments pick it up on
	// the next migration without needing a destructive schema reset.
	ddl = append(ddl, `ALTER TABLE users ADD COLUMN IF NOT EXISTS is_anonymous BOOLEAN NOT NULL DEFAULT FALSE;`)
	// Email is nullable for anonymous users (they have no mailbox). The
	// original CREATE declared email NOT NULL UNIQUE, so drop the NOT NULL
	// on existing deployments; the UNIQUE constraint stays and Postgres
	// permits multiple NULLs under a regular UNIQUE by default.
	ddl = append(ddl, `ALTER TABLE users ALTER COLUMN email DROP NOT NULL;`)

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
		// Additive columns for 6-digit OTP support. `code` is the numeric
		// token a user types into a form; `email` scopes lookups so two
		// users can't collide on the same random 6 digits. Both are
		// nullable so legacy signup/recovery rows keep working.
		ddl = append(ddl, `ALTER TABLE _auth_email_verifications ADD COLUMN IF NOT EXISTS code TEXT;`)
		ddl = append(ddl, `ALTER TABLE _auth_email_verifications ADD COLUMN IF NOT EXISTS email TEXT;`)
		ddl = append(ddl, `CREATE INDEX IF NOT EXISTS _auth_email_verif_email_code_idx ON _auth_email_verifications (email, code);`)
	}

	// MFA: TOTP factors + challenges. We always create these (the
	// migration cost is trivial and gating on a config flag would force
	// callers to restart ultrabase just to enable 2FA).
	ddl = append(ddl, `CREATE TABLE IF NOT EXISTS _mfa_factors (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  friendly_name TEXT,
  factor_type TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'unverified',
  secret TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`)
	ddl = append(ddl, `CREATE INDEX IF NOT EXISTS _mfa_factors_user_idx ON _mfa_factors (user_id);`)
	ddl = append(ddl, `CREATE TABLE IF NOT EXISTS _mfa_challenges (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  factor_id UUID NOT NULL REFERENCES _mfa_factors(id) ON DELETE CASCADE,
  verified_at TIMESTAMPTZ,
  ip_address TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`)

	return ddl
}

func generateTable(name string, table domain.Table, allTables map[string]domain.Table) []string {
	var ddl []string
	var cols []string
	var constraints []string

	// Copy fields and stable-sort so PK comes first; slice order is
	// otherwise preserved from the YAML declaration.
	fields := make([]domain.Field, len(table.Fields))
	copy(fields, table.Fields)
	sort.SliceStable(fields, func(i, j int) bool {
		if fields[i].PrimaryKey != fields[j].PrimaryKey {
			return fields[i].PrimaryKey
		}
		return false
	})

	for _, field := range fields {
		fname := field.Name
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

	return ddl
}

// generateIndexes creates index DDL for a table. Split from generateTable so
// indexes run after column diffs (ADD COLUMN must precede CREATE INDEX).
func generateIndexes(name string, table domain.Table) []string {
	var ddl []string
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

// effectiveType returns the resolved SQL type for a field, inferring from FK
// references when the type is empty (users.id → UUID, others → BIGINT).
func effectiveType(f domain.Field) string {
	if f.Type != "" {
		return f.Type
	}
	if f.ForeignKey != nil {
		if strings.EqualFold(f.ForeignKey.References, "users.id") {
			return "UUID"
		}
		return "BIGINT"
	}
	return f.Type
}

func formatColumn(name string, field domain.Field) string {
	typ := effectiveType(field)

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
  name TEXT NOT NULL,
  size BIGINT NOT NULL DEFAULT 0,
  mime TEXT NOT NULL DEFAULT '',
  uploaded_by UUID REFERENCES users(id),
  uploaded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  metadata JSONB,
  UNIQUE (bucket_id, name)
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

			ddl = append(ddl, fmt.Sprintf("DROP POLICY IF EXISTS %s ON %s;", policyName, tableName))
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
		policyName := fmt.Sprintf("%s_public_select", bucketName)
		ddl = append(ddl, fmt.Sprintf("DROP POLICY IF EXISTS %s ON _objects;", policyName))
		ddl = append(ddl, fmt.Sprintf(
			"CREATE POLICY %s ON _objects FOR SELECT USING (bucket_id = '%s');",
			policyName, bucketName))
	}

	for i, policy := range bucket.RLS {
		typeClause := rlsPolicyTypeClause(policy)
		for _, op := range policy.Operations {
			policyName := fmt.Sprintf("storage_%s_%s_%d", bucketName, op, i)
			pgOp := strings.ToUpper(op)
			scopedCheck := fmt.Sprintf("bucket_id = '%s' AND (%s)", bucketName, policy.Check)

			ddl = append(ddl, fmt.Sprintf("DROP POLICY IF EXISTS %s ON _objects;", policyName))
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

// generateRPCFunction emits a CREATE OR REPLACE FUNCTION statement for a
// YAML-declared Postgres function. The function name, arg names, arg types,
// return type, language, volatility and security clause have all been
// validated by config.validateRPCFunction before reaching this point, so
// the values are safe to interpolate as SQL identifiers. Body bytes are
// wrapped in $ub$...$ub$ dollar quoting; bodies that contain that tag are
// rejected at config load, so the body cannot break out of the literal.
func generateRPCFunction(name string, fn domain.Function) string {
	var sig []string
	for _, a := range fn.Args {
		piece := fmt.Sprintf(`"%s" %s`, a.Name, a.Type)
		if a.Default != nil {
			piece += fmt.Sprintf(" DEFAULT %s", formatDefault(a.Default, a.Type))
		}
		sig = append(sig, piece)
	}

	language := strings.ToLower(fn.Language)
	volatility := strings.ToUpper(fn.Volatility)
	security := "SECURITY INVOKER"
	if strings.EqualFold(fn.Security, "definer") {
		security = "SECURITY DEFINER"
	}

	return fmt.Sprintf(
		"CREATE OR REPLACE FUNCTION public.\"%s\"(%s)\nRETURNS %s\nLANGUAGE %s\n%s\n%s\nAS $ub$%s$ub$;",
		name,
		strings.Join(sig, ", "),
		fn.Returns.Type,
		language,
		volatility,
		security,
		fn.Body,
	)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
