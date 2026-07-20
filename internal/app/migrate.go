// Package app contains application services (use cases) that orchestrate domain logic.
package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/instancez/instancez/internal/domain"
)

// ErrDestructive is returned when a migration plan would drop a table or a
// column and the caller has not opted in via AllowDestructive.
var ErrDestructive = errors.New("destructive migration")

// Migrator generates and applies DDL migrations from config.
type Migrator struct {
	db               domain.Database
	roles            domain.Roles
	allowDestructive bool
	logger           *slog.Logger
}

// AllowDestructive permits DROP TABLE / DROP COLUMN in generated plans. It
// returns the receiver so it can be chained onto NewMigrator.
func (m *Migrator) AllowDestructive(v bool) *Migrator {
	m.allowDestructive = v
	return m
}

// NewMigrator builds a Migrator. Pass an explicit Roles value, or
// domain.DefaultRoles() to keep Supabase-compatible defaults.
func NewMigrator(db domain.Database, roles ...domain.Roles) *Migrator {
	r := domain.DefaultRoles()
	if len(roles) > 0 {
		r = roles[0]
	}
	return &Migrator{db: db, roles: r, logger: slog.Default()}
}

// Plan generates DDL statements to bring the DB in sync with the config.
// When oldCfg is nil (first migration), it generates the full schema.
// When oldCfg is provided, it generates only the diff between configs.
//
// Plan is the preview path and is deliberately not gated by AllowDestructive:
// seeing the DROP statements is how an operator discovers a rename was about
// to discard data. PlanStatements, which Apply executes, is the gated one.
func (m *Migrator) Plan(ctx context.Context, oldCfg, newCfg *domain.Config) (string, error) {
	if oldCfg == nil {
		return planFromScratch(newCfg, m.roles), nil
	}
	return planUpdate(oldCfg, newCfg, m.roles), nil
}

// PlanStatements is like Plan but returns statements as a slice, so callers
// can run them inside a single transaction. Returns nil when there are no
// changes to apply.
//
// This is the execution path, so it returns ErrDestructive when the plan would
// drop a table or column and AllowDestructive is unset. Use Plan to preview
// such a change without tripping the gate.
func (m *Migrator) PlanStatements(ctx context.Context, oldCfg, newCfg *domain.Config) ([]string, error) {
	if oldCfg == nil {
		return planFromScratchStatements(newCfg, m.roles), nil
	}
	if destroys := diffConfigs(oldCfg, newCfg).Destroys; len(destroys) > 0 {
		if !m.allowDestructive {
			return nil, destructiveError(destroys)
		}
		// Gate is open (dev, or an explicit opt-in), so this is the only
		// warning before the data goes away.
		m.logger.Warn("applying destructive migration",
			"drops", strings.Join(destroys, ", "))
	}
	return planUpdateStatements(oldCfg, newCfg, m.roles), nil
}

// destructiveError explains what the plan would destroy and how to proceed.
// A rename reaches this path as a drop plus an add, so the message calls that
// case out: there is no way to tell the two apart from config alone.
func destructiveError(destroys []string) error {
	return fmt.Errorf(
		"%w: this change drops %s.\n"+
			"If you meant to rename it, declare the old name with renamed_from: "+
			"so the data moves instead of being dropped.\n"+
			"To drop it for real, pass --allow-destructive (or set INSTANCEZ_ALLOW_DESTRUCTIVE=true)",
		ErrDestructive, strings.Join(destroys, ", "))
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

	// auth.jwt_keys is the signing-key store for the whole token system (anon
	// key, service tokens, JWKS). It is needed even when user-facing auth is
	// off, so it is always emitted. The other auth tables stay gated on cfg.Auth.
	ddl = append(ddl, generateJWTKeysTable()...)

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
	ddl = append(ddl, generateStorageRLSAll(cfg.Storage)...)

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

	// Always re-assert auth.jwt_keys (idempotent) so an app deployed without it
	// heals on its next migration. This covers apps whose config has no auth block.
	ddl = append(ddl, generateJWTKeysTable()...)

	// 0. Renames, before anything else looks at these tables by name.
	ddl = append(ddl, diff.Renames...)

	// 1. Removals (DROP TABLE, DROP COLUMN, DROP INDEX, DROP POLICY, etc.)
	ddl = append(ddl, diff.Removals...)

	// 2. Additions (new auth, tables, columns, storage)
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
	ddl = append(ddl, generateStorageRLSAll(newCfg.Storage)...)

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
// All DDL statements and the history row run inside a single transaction, so
// a mid-migration failure rolls back cleanly and the row commits atomically
// with the schema change: either both land or neither does.
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

	// Record the migration in the same transaction as the DDL. The history row
	// and the schema change then commit together: the row is present iff the
	// change was applied, so a later Apply never re-emits a committed statement
	// against already-changed schema. This is why plan statements don't all
	// need to be idempotent (e.g. RENAME COLUMN, which has no IF EXISTS form).
	planSQL := strings.Join(stmts, "\n\n")
	if _, err := tx.Exec(ctx,
		`INSERT INTO _instancez_migrations (checksum, sql, config_json) VALUES ($1, $2, $3)`,
		configChecksum, planSQL, string(configJSON)); err != nil {
		return fmt.Errorf("migrate record: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("migrate commit: %w", err)
	}
	return nil
}

// isReservedSchema reports whether a schema is engine-owned (auth, storage).
// The seed role used by run_sql is never granted on these.
func isReservedSchema(s string) bool {
	return s == "auth" || s == "storage"
}

// generateSchemaGrants emits per-schema USAGE + default privileges for the
// three API roles. Mirrors what Supabase configures for `public` at project
// init, applied uniformly to every schema we manage. CREATE SCHEMA is
// idempotent so this is safe to re-emit every migration.
func generateSchemaGrants(schemas []string, roles domain.Roles) []string {
	rlist := apiRoleList(roles)
	ddl := make([]string, 0, len(schemas)*4+1)
	// Postgres 14 and older ship the public schema with CREATE granted to
	// PUBLIC, so anon/authenticated/seed could create objects there. 15 dropped
	// that default; revoke it every run so the owner-only DDL model holds on
	// every major we support. public always exists, so this can't miss.
	ddl = append(ddl, "REVOKE CREATE ON SCHEMA public FROM PUBLIC;")
	for _, s := range schemas {
		ddl = append(ddl,
			fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s;", s),
			fmt.Sprintf("GRANT USAGE ON SCHEMA %s TO %s;", s, rlist),
			fmt.Sprintf("ALTER DEFAULT PRIVILEGES IN SCHEMA %s GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %s;", s, rlist),
			fmt.Sprintf("ALTER DEFAULT PRIVILEGES IN SCHEMA %s GRANT USAGE, SELECT ON SEQUENCES TO %s;", s, rlist),
		)
		if roles.Seed != "" && !isReservedSchema(s) {
			ddl = append(ddl,
				fmt.Sprintf("GRANT USAGE ON SCHEMA %s TO %s;", s, roles.Seed),
				fmt.Sprintf("ALTER DEFAULT PRIVILEGES IN SCHEMA %s GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %s;", s, roles.Seed),
				fmt.Sprintf("ALTER DEFAULT PRIVILEGES IN SCHEMA %s GRANT USAGE, SELECT ON SEQUENCES TO %s;", s, roles.Seed),
			)
		}
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
		if roles.Seed != "" && !isReservedSchema(s) {
			ddl = append(ddl,
				fmt.Sprintf("GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA %s TO %s;", s, roles.Seed),
				fmt.Sprintf("GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA %s TO %s;", s, roles.Seed),
			)
		}
	}
	// _instancez_migrations lives in public and is caught by the public GRANT
	// above; revoke it so the seed role cannot touch migration history. This
	// runs near the end of the plan, after EnsureMigrationsTable has created
	// the table, so the REVOKE target always exists.
	if roles.Seed != "" {
		ddl = append(ddl, fmt.Sprintf("REVOKE ALL ON _instancez_migrations FROM %s;", roles.Seed))
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

// generateJWTKeysTable emits the auth schema and auth.jwt_keys table. This is
// separate from generateAuthTables because the signing keys back user session
// tokens and JWKS regardless of whether user-facing auth is enabled, so the
// migrator emits it for every app. Managed by app.JWTKeyManager.
func generateJWTKeysTable() []string {
	return []string{
		`CREATE SCHEMA IF NOT EXISTS auth;`,
		`CREATE TABLE IF NOT EXISTS auth.jwt_keys (
  kid TEXT PRIMARY KEY,
  secret BYTEA NOT NULL,
  algorithm TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  retired_at TIMESTAMPTZ
);`,
	}
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
		cols = append(cols, "banned_until TIMESTAMPTZ")
		cols = append(cols, "raw_app_meta_data JSONB NOT NULL DEFAULT '{}'::jsonb")
		cols = append(cols, "raw_user_meta_data JSONB NOT NULL DEFAULT '{}'::jsonb")
		cols = append(cols, "is_anonymous BOOLEAN NOT NULL DEFAULT FALSE")
		cols = append(cols, "created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()")
		cols = append(cols, "updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()")

		ddl = append(ddl, fmt.Sprintf("CREATE TABLE IF NOT EXISTS auth.users (\n  %s\n);", strings.Join(cols, ",\n  ")))
	}

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
  attempts INT NOT NULL DEFAULT 0,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`)
		// Additive column for deployments whose one_time_tokens table predates
		// OTP attempt-limiting.
		ddl = append(ddl, `ALTER TABLE auth.one_time_tokens ADD COLUMN IF NOT EXISTS attempts INT NOT NULL DEFAULT 0;`)
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
  attempts INT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`)
	// Additive column for deployments whose mfa_challenges table predates
	// TOTP attempt-limiting.
	ddl = append(ddl, `ALTER TABLE auth.mfa_challenges ADD COLUMN IF NOT EXISTS attempts INT NOT NULL DEFAULT 0;`)

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
			ddl = append(ddl, fmt.Sprintf("CREATE POLICY %s ON %s%s FOR %s%s;",
				policyName, qualName, typeClause, pgOp, rlsClauses(op, policy.Using, policy.WithCheck)))
		}
	}

	return ddl
}

// rlsClauses renders the " USING (...)"/" WITH CHECK (...)" suffix for a
// single CREATE POLICY statement targeting one operation. select/delete only
// ever accept USING (Postgres rejects WITH CHECK there); insert only ever
// accepts WITH CHECK; update may carry either or both. using/withCheck may be
// the same RLSPolicy fields reused across every operation in policy.Operations
// — this function decides, per call, which of the two apply to op.
func rlsClauses(op, using, withCheck string) string {
	var b strings.Builder
	if using != "" && op != "insert" {
		fmt.Fprintf(&b, " USING (%s)", using)
	}
	if withCheck != "" && op != "select" && op != "delete" {
		fmt.Fprintf(&b, " WITH CHECK (%s)", withCheck)
	}
	return b.String()
}

// generateStorageRLSAll decides whether to enforce RLS on the shared
// storage.objects table and emits the per-bucket policies.
//
// The model mirrors regular tables: RLS is only turned on when at least one
// bucket declares `rls:` policies. A project that declares no storage policies
// keeps the previous open behaviour (object access is gated by the route-level
// auth + the bucket's public flag, exactly as before) — this is what keeps the
// supabase-js storage compat checks green.
//
// Once any bucket opts in, RLS is enabled table-wide (it's one table for all
// buckets), so buckets that did NOT declare policies receive a permissive
// default policy to preserve their open behaviour. Buckets WITH declared
// policies are enforced — closing the gap where those policies were previously
// inert because the table never had RLS enabled and the handlers ran as
// service_role. service_role (admin key) has BYPASSRLS throughout.
func generateStorageRLSAll(storage map[string]domain.Bucket) []string {
	if len(storage) == 0 {
		return nil
	}
	anyRLS := false
	for _, b := range storage {
		if len(b.RLS) > 0 {
			anyRLS = true
			break
		}
	}

	var ddl []string
	if anyRLS {
		ddl = append(ddl,
			`ALTER TABLE storage.objects ENABLE ROW LEVEL SECURITY;`,
			`ALTER TABLE storage.objects FORCE ROW LEVEL SECURITY;`,
		)
	}

	for _, name := range sortedKeys(storage) {
		bucket := storage[name]
		switch {
		case len(bucket.RLS) > 0:
			ddl = append(ddl, generateStorageRLS(name, bucket)...)
		case anyRLS:
			// RLS is on table-wide but this bucket opted out: keep it open so
			// behaviour matches a table without RLS. Route-level auth still
			// gates anonymous writes (upload routes require a JWT).
			policyName := fmt.Sprintf("%s_default_all", name)
			ddl = append(ddl, fmt.Sprintf("DROP POLICY IF EXISTS %s ON storage.objects;", policyName))
			ddl = append(ddl, fmt.Sprintf(
				"CREATE POLICY %s ON storage.objects FOR ALL USING (bucket_id = '%s') WITH CHECK (bucket_id = '%s');",
				policyName, name, name))
		case bucket.Public:
			// No RLS anywhere — the public-select policy is inert without RLS
			// but kept for parity and in case RLS is enabled later.
			ddl = append(ddl, generateStorageRLS(name, bucket)...)
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
		scopedUsing := scopeToBucket(bucketName, policy.Using)
		scopedWithCheck := scopeToBucket(bucketName, policy.WithCheck)
		for _, op := range policy.Operations {
			policyName := fmt.Sprintf("storage_%s_%s_%d", bucketName, op, i)
			pgOp := strings.ToUpper(op)

			ddl = append(ddl, fmt.Sprintf("DROP POLICY IF EXISTS %s ON storage.objects;", policyName))
			ddl = append(ddl, fmt.Sprintf("CREATE POLICY %s ON storage.objects%s FOR %s%s;",
				policyName, typeClause, pgOp, rlsClauses(op, scopedUsing, scopedWithCheck)))
		}
	}

	return ddl
}

// scopeToBucket ANDs the bucket_id predicate onto a possibly-empty RLS
// expression. Empty stays empty so rlsClauses can still tell "not set" from
// "set to a scoped expression".
func scopeToBucket(bucketName, expr string) string {
	if expr == "" {
		return ""
	}
	return fmt.Sprintf("bucket_id = '%s' AND (%s)", bucketName, expr)
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
