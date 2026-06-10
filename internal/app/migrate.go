// Package app contains application services (use cases) that orchestrate domain logic.
package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/saedx1/instancez/internal/domain"
)

// Migrator generates and applies DDL migrations from config.
type Migrator struct {
	db    domain.Database
	roles domain.Roles
}

// NewMigrator builds a Migrator. Pass an explicit Roles value, or
// domain.DefaultRoles() to keep Supabase-compatible defaults.
func NewMigrator(db domain.Database, roles ...domain.Roles) *Migrator {
	r := domain.DefaultRoles()
	if len(roles) > 0 {
		r = roles[0]
	}
	return &Migrator{db: db, roles: r}
}

// Plan generates DDL statements to bring the DB in sync with the config.
// When oldCfg is nil (first migration), it generates the full schema.
// When oldCfg is provided, it generates only the diff between configs.
func (m *Migrator) Plan(ctx context.Context, oldCfg, newCfg *domain.Config) (string, error) {
	if oldCfg == nil {
		return planFromScratch(newCfg, m.roles), nil
	}
	return planUpdate(oldCfg, newCfg, m.roles), nil
}

// PlanStatements is like Plan but returns statements as a slice, so callers
// can run them inside a single transaction. Returns nil when there are no
// changes to apply.
func (m *Migrator) PlanStatements(ctx context.Context, oldCfg, newCfg *domain.Config) ([]string, error) {
	if oldCfg == nil {
		return planFromScratchStatements(newCfg, m.roles), nil
	}
	return planUpdateStatements(oldCfg, newCfg, m.roles), nil
}

// planFromScratch generates full DDL for a fresh database using IF NOT EXISTS /
// CREATE OR REPLACE for safety. Delegates to planFromScratchStatements and
// joins the result with blank-line separators for callers that want the
// joined-string form (e.g. the rollback dry-run output).
func planFromScratch(cfg *domain.Config, roles domain.Roles) string {
	stmts := planFromScratchStatements(cfg, roles)
	if len(stmts) == 0 {
		return ""
	}
	return strings.Join(stmts, "\n\n")
}

// planFromScratchStatements is the slice-returning core of planFromScratch.
// Apply uses this directly so each statement can run inside a single
// transaction.
func planFromScratchStatements(cfg *domain.Config, roles domain.Roles) []string {
	var ddl []string
	schemas := orderedSchemas(cfg)

	// Schemas (including public) get USAGE + default privileges so any
	// table created later in this migration picks up the grants. The
	// Postgres roles themselves are infrastructure — provisioned by the
	// control plane in managed deployments and by
	// scripts/postgres-init/01-roles.sql in dev — so the migration assumes
	// they already exist.
	ddl = append(ddl, generateSchemaGrants(schemas, roles)...)

	// Extensions
	for _, ext := range cfg.Extensions {
		ddl = append(ddl, fmt.Sprintf("CREATE EXTENSION IF NOT EXISTS %s;", ext))
	}

	// Users table (core auth columns).
	if cfg.Auth != nil {
		ddl = append(ddl, generateAuthTables(cfg.Auth)...)
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
	ddl = append(ddl, generateStorageTables(cfg)...)

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
	if len(cfg.RPC) > 0 {
		fnNames := sortedKeys(cfg.RPC)
		for _, n := range fnNames {
			ddl = append(ddl, generateRPCFunction(n, cfg.RPC[n]))
		}
	}

	// Backfill grants on tables created earlier in this same migration —
	// ALTER DEFAULT PRIVILEGES applies to objects created after the ALTER,
	// but the underlying behavior in some Postgres versions can race with
	// CREATE TABLE in the same session, so we issue an explicit catch-up.
	ddl = append(ddl, generateExistingObjectGrants(schemas, roles)...)

	return ddl
}

// planUpdate generates DDL for transitioning from oldCfg to newCfg.
// It produces removal DDL from the config diff, followed by additions and
// alterations, then re-emits idempotent items (indexes, RLS, search, functions).
// Delegates to planUpdateStatements; see planFromScratch for the rationale.
func planUpdate(oldCfg, newCfg *domain.Config, roles domain.Roles) string {
	stmts := planUpdateStatements(oldCfg, newCfg, roles)
	if len(stmts) == 0 {
		return ""
	}
	return strings.Join(stmts, "\n\n")
}

// planUpdateStatements is the slice-returning core of planUpdate. Apply uses
// this directly so each statement can run inside a single transaction.
func planUpdateStatements(oldCfg, newCfg *domain.Config, roles domain.Roles) []string {
	diff := diffConfigs(oldCfg, newCfg)
	var ddl []string
	schemas := orderedSchemas(newCfg)

	// Re-assert schema bootstrapping (idempotent) so renames or new schemas
	// in the config are picked up on subsequent migrations. The Postgres
	// roles (login + API) are infrastructure — provisioned by the control
	// plane in managed deployments and by scripts/postgres-init/01-roles.sql
	// in dev — so the migration assumes they already exist.
	ddl = append(ddl, generateSchemaGrants(schemas, roles)...)

	// 1. Removals (DROP TABLE, DROP COLUMN, DROP INDEX, DROP POLICY, etc.)
	ddl = append(ddl, diff.Removals...)

	// 2. Additions (new extensions, auth, tables, columns, storage)
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
	if len(newCfg.RPC) > 0 {
		fnNames := sortedKeys(newCfg.RPC)
		for _, n := range fnNames {
			ddl = append(ddl, generateRPCFunction(n, newCfg.RPC[n]))
		}
	}

	// Catch-up grants on any newly-added tables.
	ddl = append(ddl, generateExistingObjectGrants(schemas, roles)...)

	return ddl
}

// Apply generates and executes the migration, recording it in the history table.
// It stores the applied config as JSON so the next run can diff against it to
// detect removed objects and avoid re-running unchanged migrations.
//
// All DDL statements run inside a single transaction so a mid-migration
// failure rolls back cleanly: either every statement applies or none do.
// The history row is recorded post-commit; see the comment below for why.
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

	stmts, err := m.PlanStatements(ctx, oldCfg, cfg)
	if err != nil {
		return fmt.Errorf("migrate plan: %w", err)
	}
	if len(stmts) == 0 {
		return nil
	}

	tx, err := m.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("migrate begin: %w", err)
	}
	// Safe to defer: tx.Rollback after a successful Commit is a no-op error
	// we ignore. Calling Rollback before return guarantees no leak on panic.
	defer func() { _ = tx.Rollback(ctx) }()

	for _, stmt := range stmts {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("migrate exec: %w", err)
		}
	}

	planSQL := strings.Join(stmts, "\n\n")
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("migrate commit: %w", err)
	}

	// Record AFTER commit. If RecordMigration fails post-commit the DB schema
	// is correct but the history is missing the row — next Apply will diff
	// from the older lastGood and try to re-emit idempotent statements,
	// which is safe (CREATE OR REPLACE / IF NOT EXISTS / DROP+CREATE
	// patterns), so we accept that risk.
	if err := m.db.RecordMigration(ctx, configChecksum, planSQL, string(configJSON)); err != nil {
		return fmt.Errorf("migrate record: %w", err)
	}
	return nil
}

// generateSchemaGrants emits per-schema USAGE + default privileges for the
// three API roles. Mirrors what Supabase configures for `public` at project
// init, applied uniformly to every schema we manage. CREATE SCHEMA is
// idempotent so this is safe to re-emit every migration.
func generateSchemaGrants(schemas []string, roles domain.Roles) []string {
	rlist := apiRoleList(roles)
	ddl := make([]string, 0, len(schemas)*4)
	for _, s := range schemas {
		ddl = append(ddl,
			fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s;", s),
			fmt.Sprintf("GRANT USAGE ON SCHEMA %s TO %s;", s, rlist),
			fmt.Sprintf("ALTER DEFAULT PRIVILEGES IN SCHEMA %s GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %s;", s, rlist),
			fmt.Sprintf("ALTER DEFAULT PRIVILEGES IN SCHEMA %s GRANT USAGE, SELECT ON SEQUENCES TO %s;", s, rlist),
		)
	}
	return ddl
}

// generateExistingObjectGrants backfills GRANTs on tables/sequences that
// already exist (created by an earlier migration, before this run's
// ALTER DEFAULT PRIVILEGES took effect). Emitted near the end of the plan
// so it picks up tables created in the same migration.
func generateExistingObjectGrants(schemas []string, roles domain.Roles) []string {
	rlist := apiRoleList(roles)
	ddl := make([]string, 0, len(schemas)*2)
	for _, s := range schemas {
		ddl = append(ddl,
			fmt.Sprintf("GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA %s TO %s;", s, rlist),
			fmt.Sprintf("GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA %s TO %s;", s, rlist),
		)
	}
	return ddl
}

func apiRoleList(roles domain.Roles) string {
	return fmt.Sprintf("%s, %s, %s", roles.Anon, roles.Authenticated, roles.Service)
}

// orderedSchemas returns the deduped list of schemas the migrator manages,
// with "public" first, "auth" if auth is configured, "storage" if buckets
// exist, then any user-declared schemas in YAML order.
func orderedSchemas(cfg *domain.Config) []string {
	seen := map[string]bool{"public": true}
	out := []string{"public"}
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if cfg.Auth != nil {
		add("auth")
	}
	if len(cfg.Storage) > 0 {
		add("storage")
	}
	for _, table := range cfg.Tables {
		add(table.EffectiveSchema())
	}
	return out
}

// generateAuthTables emits the auth.* tables. All tables live in the auth
// schema; the underscore prefixes used pre-schema-move are dropped because
// the schema already provides the namespace. Names align with Supabase's
// auth schema where they map cleanly.
func generateAuthTables(auth *domain.Auth) []string {
	var ddl []string

	// pgcrypto provides gen_random_uuid().
	ddl = append(ddl, `CREATE EXTENSION IF NOT EXISTS pgcrypto;`)
	// Ensure the auth schema exists before we put tables in it. (Schema GRANTs
	// are emitted separately by generateSchemaGrants once orderedSchemas
	// includes "auth"; see Task 8.)
	ddl = append(ddl, `CREATE SCHEMA IF NOT EXISTS auth;`)

	// auth.users
	{
		var cols []string
		cols = append(cols, "id UUID PRIMARY KEY DEFAULT gen_random_uuid()")
		cols = append(cols, "email TEXT UNIQUE")
		cols = append(cols, "password_hash TEXT")
		cols = append(cols, "email_verified BOOLEAN NOT NULL DEFAULT FALSE")
		cols = append(cols, "email_confirmed_at TIMESTAMPTZ")
		cols = append(cols, "last_sign_in_at TIMESTAMPTZ")
		cols = append(cols, "raw_app_meta_data JSONB NOT NULL DEFAULT '{}'::jsonb")
		cols = append(cols, "raw_user_meta_data JSONB NOT NULL DEFAULT '{}'::jsonb")
		cols = append(cols, "is_anonymous BOOLEAN NOT NULL DEFAULT FALSE")
		cols = append(cols, "created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()")
		cols = append(cols, "updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()")

		ddl = append(ddl, fmt.Sprintf("CREATE TABLE IF NOT EXISTS auth.users (\n  %s\n);", strings.Join(cols, ",\n  ")))
	}

	// auth.jwt_keys — managed by app.JWTKeyManager.
	ddl = append(ddl, `CREATE TABLE IF NOT EXISTS auth.jwt_keys (
  kid TEXT PRIMARY KEY,
  secret BYTEA NOT NULL,
  algorithm TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  retired_at TIMESTAMPTZ
);`)

	// auth.identities — OAuth identity links.
	ddl = append(ddl, `CREATE TABLE IF NOT EXISTS auth.identities (
  id BIGSERIAL PRIMARY KEY,
  user_id UUID NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
  provider TEXT NOT NULL,
  provider_user_id TEXT NOT NULL,
  identity_data JSONB NOT NULL DEFAULT '{}'::jsonb,
  email TEXT,
  last_sign_in_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(provider, provider_user_id)
);`)

	if auth.RefreshTokens {
		ddl = append(ddl, `CREATE TABLE IF NOT EXISTS auth.refresh_tokens (
  id BIGSERIAL PRIMARY KEY,
  user_id UUID NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
  token TEXT NOT NULL UNIQUE,
  expires_at TIMESTAMPTZ NOT NULL,
  session_id TEXT,
  ip TEXT,
  user_agent TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`)
	}

	if auth.Email != nil {
		ddl = append(ddl, `CREATE TABLE IF NOT EXISTS auth.one_time_tokens (
  id BIGSERIAL PRIMARY KEY,
  user_id UUID NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
  token TEXT NOT NULL UNIQUE,
  purpose TEXT NOT NULL DEFAULT 'signup',
  email TEXT,
  code TEXT,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`)
		ddl = append(ddl, `CREATE INDEX IF NOT EXISTS idx_one_time_tokens_email_code ON auth.one_time_tokens (email, code);`)
	}

	// auth.mfa_factors / auth.mfa_challenges — TOTP MFA. Always emitted
	// (the migration cost is trivial and gating on a config flag would
	// force callers to restart instancez just to enable 2FA).
	ddl = append(ddl, `CREATE TABLE IF NOT EXISTS auth.mfa_factors (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
  friendly_name TEXT,
  factor_type TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'unverified',
  secret TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`)
	ddl = append(ddl, `CREATE INDEX IF NOT EXISTS idx_mfa_factors_user ON auth.mfa_factors (user_id);`)
	ddl = append(ddl, `CREATE TABLE IF NOT EXISTS auth.mfa_challenges (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  factor_id UUID NOT NULL REFERENCES auth.mfa_factors(id) ON DELETE CASCADE,
  verified_at TIMESTAMPTZ,
  ip_address TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`)

	// auth.flow_state — consolidates PKCE auth codes and OAuth state into one
	// Supabase-shaped table. provider_type distinguishes the two flows
	// ('pkce' vs. 'oauth'); auth_code holds the PKCE code or the OAuth
	// state token. redirect_to and linking_user_id are carry-overs instancez
	// needs that Supabase doesn't represent natively.
	ddl = append(ddl, `CREATE TABLE IF NOT EXISTS auth.flow_state (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID,
  auth_code TEXT,
  code_challenge TEXT,
  code_challenge_method TEXT,
  provider_type TEXT NOT NULL,
  provider_access_token TEXT,
  provider_refresh_token TEXT,
  authentication_method TEXT,
  redirect_to TEXT,
  linking_user_id TEXT,
  auth_code_issued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`)
	// auth_code uniqueness is enforced via a partial unique index because the
	// column is nullable (linking flows can leave it empty). Pre-merge, both
	// _auth_codes.code and _oauth_states.state were PRIMARY KEY; this preserves
	// that uniqueness invariant for the merged table.
	ddl = append(ddl, `CREATE UNIQUE INDEX IF NOT EXISTS idx_flow_state_auth_code ON auth.flow_state (auth_code) WHERE auth_code IS NOT NULL;`)
	ddl = append(ddl, `CREATE INDEX IF NOT EXISTS idx_flow_state_user_id_auth_method ON auth.flow_state (user_id, authentication_method);`)

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
			schema, refTable, refCol, err := domain.ParseFKReference(field.ForeignKey.References)
			if err != nil {
				// Surface the validation error as a SQL syntax error in the
				// emitted DDL, which the migrator will fail on with a clear
				// message. (Validation runs before this in normal flow.)
				constraints = append(constraints, fmt.Sprintf("/* invalid FK: %s */", err.Error()))
			} else {
				onDelete := "RESTRICT"
				if field.ForeignKey.OnDelete != "" {
					onDelete = strings.ToUpper(strings.ReplaceAll(field.ForeignKey.OnDelete, "_", " "))
				}
				constraints = append(constraints,
					fmt.Sprintf("FOREIGN KEY (%s) REFERENCES %s.%s(%s) ON DELETE %s",
						fname, schema, refTable, refCol, onDelete))
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
	qualName := qualifiedTableName(name, table)
	ddl = append(ddl, fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (\n  %s\n);",
		qualName, strings.Join(allParts, ",\n  ")))

	return ddl
}

// generateIndexes creates index DDL for a table. Split from generateTable so
// indexes run after column diffs (ADD COLUMN must precede CREATE INDEX).
func generateIndexes(name string, table domain.Table) []string {
	var ddl []string
	qualName := qualifiedTableName(name, table)
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
			unique, indexName, qualName, strings.Join(idx.Columns, ", "), where))
	}
	return ddl
}

// qualifiedTableName returns "schema.table" for non-default schemas, and the
// bare table name for the default ("public") schema. Mirrors the inline logic
// previously embedded in generateTable.
func qualifiedTableName(name string, t domain.Table) string {
	s := t.EffectiveSchema()
	if s == "public" {
		return name
	}
	return s + "." + name
}

// effectiveType returns the resolved SQL type for a field, inferring from FK
// references when the type is empty (auth.users.id → UUID, others → BIGINT).
func effectiveType(f domain.Field) string {
	if f.Type != "" {
		return f.Type
	}
	if f.ForeignKey != nil {
		if strings.EqualFold(f.ForeignKey.References, "auth.users.id") {
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

// generateStorageTables emits the storage.* tables. Today that's just
// storage.objects (file metadata). Buckets remain configured in YAML; no
// storage.buckets table is emitted.
func generateStorageTables(cfg *domain.Config) []string {
	if len(cfg.Storage) == 0 {
		return nil
	}
	return []string{
		`CREATE SCHEMA IF NOT EXISTS storage;`,
		`CREATE TABLE IF NOT EXISTS storage.objects (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  bucket_id TEXT NOT NULL,
  name TEXT NOT NULL,
  size BIGINT NOT NULL DEFAULT 0,
  mime TEXT NOT NULL DEFAULT '',
  uploaded_by UUID REFERENCES auth.users(id),
  uploaded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  metadata JSONB,
  UNIQUE (bucket_id, name)
);`,
	}
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
		// RLS policies written against older instancez versions.
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
	qualName := qualifiedTableName(tableName, table)

	var ddl []string
	ddl = append(ddl, fmt.Sprintf("ALTER TABLE %s ENABLE ROW LEVEL SECURITY;", qualName))
	ddl = append(ddl, fmt.Sprintf("ALTER TABLE %s FORCE ROW LEVEL SECURITY;", qualName))

	for i, policy := range table.RLS {
		typeClause := rlsPolicyTypeClause(policy)
		for _, op := range policy.Operations {
			policyName := fmt.Sprintf("%s_%s_%d", tableName, op, i)
			pgOp := strings.ToUpper(op)

			ddl = append(ddl, fmt.Sprintf("DROP POLICY IF EXISTS %s ON %s;", policyName, qualName))
			if op == "insert" {
				ddl = append(ddl, fmt.Sprintf(
					"CREATE POLICY %s ON %s%s FOR %s WITH CHECK (%s);",
					policyName, qualName, typeClause, pgOp, policy.Check))
			} else {
				ddl = append(ddl, fmt.Sprintf(
					"CREATE POLICY %s ON %s%s FOR %s USING (%s);",
					policyName, qualName, typeClause, pgOp, policy.Check))
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
		ddl = append(ddl, fmt.Sprintf("DROP POLICY IF EXISTS %s ON storage.objects;", policyName))
		ddl = append(ddl, fmt.Sprintf(
			"CREATE POLICY %s ON storage.objects FOR SELECT USING (bucket_id = '%s');",
			policyName, bucketName))
	}

	for i, policy := range bucket.RLS {
		typeClause := rlsPolicyTypeClause(policy)
		for _, op := range policy.Operations {
			policyName := fmt.Sprintf("storage_%s_%s_%d", bucketName, op, i)
			pgOp := strings.ToUpper(op)
			scopedCheck := fmt.Sprintf("bucket_id = '%s' AND (%s)", bucketName, policy.Check)

			ddl = append(ddl, fmt.Sprintf("DROP POLICY IF EXISTS %s ON storage.objects;", policyName))
			if op == "insert" {
				ddl = append(ddl, fmt.Sprintf(
					"CREATE POLICY %s ON storage.objects%s FOR %s WITH CHECK (%s);",
					policyName, typeClause, pgOp, scopedCheck))
			} else {
				ddl = append(ddl, fmt.Sprintf(
					"CREATE POLICY %s ON storage.objects%s FOR %s USING (%s);",
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
				_, refTable, _, err := domain.ParseFKReference(field.ForeignKey.References)
				if err != nil {
					continue
				}
				// Self-references and cross-schema references (e.g. auth.users.id)
				// don't create dependency edges within cfg.Tables.
				if refTable == name {
					continue
				}
				if _, exists := tables[refTable]; exists {
					deps[name] = append(deps[name], refTable)
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
