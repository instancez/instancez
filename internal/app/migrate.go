// Package app contains application services (use cases) that orchestrate domain logic.
package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/instancez/instancez/internal/domain"
)

// ErrDestructive is returned when a migration plan would drop a table or a
// column and the caller has not opted in via AllowDestructive.
var ErrDestructive = errors.New("destructive migration")

// ErrPrimaryKeyChange is returned when a plan would change which columns form a live table's primary key.
var ErrPrimaryKeyChange = errors.New("primary key change not supported")

// Migrator generates and applies DDL migrations from config.
type Migrator struct {
	db               domain.Database
	roles            domain.Roles
	allowDestructive bool
	lockTimeout      time.Duration
	healTimeout      time.Duration
	healMaxBytes     int64
	logger           *slog.Logger
}

// DefaultMigrateLockTimeout bounds one DDL statement's table lock wait.
const DefaultMigrateLockTimeout = 5 * time.Second

// DefaultStorageHealTimeout is per heal step; two steps plus one shared lock wait stay under the platform's 30s init budget.
const DefaultStorageHealTimeout = 8 * time.Second

// DefaultStorageHealMaxBytes is the storage.objects heap size above which boot skips the list heal.
const DefaultStorageHealMaxBytes = 128 << 20

// AllowDestructive permits DROP TABLE / DROP COLUMN in generated plans. It
// returns the receiver so it can be chained onto NewMigrator.
func (m *Migrator) AllowDestructive(v bool) *Migrator {
	m.allowDestructive = v
	return m
}

// LockTimeout sets the Postgres lock_timeout for migration DDL; 0 disables it.
func (m *Migrator) LockTimeout(d time.Duration) *Migrator {
	m.lockTimeout = d
	return m
}

// StorageHealLimits bounds each storage list heal step by statement timeout and the table's heap size in bytes.
func (m *Migrator) StorageHealLimits(timeout time.Duration, maxBytes int64) *Migrator {
	m.healTimeout, m.healMaxBytes = timeout, maxBytes
	return m
}

// NewMigrator builds a Migrator. Pass an explicit Roles value, or
// domain.DefaultRoles() to keep Supabase-compatible defaults.
func NewMigrator(db domain.Database, roles ...domain.Roles) *Migrator {
	r := domain.DefaultRoles()
	if len(roles) > 0 {
		r = roles[0]
	}
	return &Migrator{db: db, roles: r, lockTimeout: DefaultMigrateLockTimeout, healTimeout: DefaultStorageHealTimeout, healMaxBytes: DefaultStorageHealMaxBytes, logger: slog.Default()}
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
	return m.planStatements(ctx, nil, oldCfg, newCfg)
}

// planStatements is PlanStatements, but with a tx it accepts a primary key change the live table already has.
func (m *Migrator) planStatements(ctx context.Context, tx domain.Tx, oldCfg, newCfg *domain.Config) ([]string, error) {
	if oldCfg == nil {
		return planFromScratchStatements(newCfg, m.roles), nil
	}
	diff := diffConfigs(oldCfg, newCfg)
	var rejected []string
	for _, c := range diff.PKChanges {
		if tx != nil {
			live, err := LivePrimaryKey(ctx, tx, c.Qual)
			if err != nil {
				return nil, err
			}
			if sameColumns(live, c.New) {
				continue
			}
		}
		rejected = append(rejected, c.String())
	}
	if len(rejected) > 0 {
		return nil, fmt.Errorf("%w: %s. Changing which columns form a live table's primary key is not supported; "+
			"revert the primary_key flags, or create a new table and copy the data",
			ErrPrimaryKeyChange, strings.Join(rejected, "; "))
	}
	if destroys := diff.Destroys; len(destroys) > 0 {
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

// LivePrimaryKey returns the primary key columns Postgres has for rel, sorted.
func LivePrimaryKey(ctx context.Context, tx domain.Tx, rel string) ([]string, error) {
	row, err := tx.QueryRow(ctx, `SELECT coalesce(string_agg(a.attname, ',' ORDER BY a.attname), '') AS cols
		FROM pg_index i JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
		WHERE i.indisprimary AND i.indrelid = to_regclass($1)`, rel)
	if err != nil {
		return nil, fmt.Errorf("read primary key of %s: %w", rel, err)
	}
	cols, _ := row["cols"].(string)
	if cols == "" {
		return nil, nil
	}
	return strings.Split(cols, ","), nil
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

	// Each table is followed by its indexes so later FKs find their unique index.
	ordered := orderTables(cfg.Tables)
	for _, name := range ordered {
		table := cfg.Tables[name]
		ddl = append(ddl, generateTable(name, table, cfg.Tables)...)
		ddl = append(ddl, generateIndexes(name, table)...)
		ddl = append(ddl, generateDeferredFKs(name, table)...)
		ddl = append(ddl, generateGuards(name, table)...)
	}

	// Storage metadata table
	ddl = append(ddl, generateStorageTables(cfg)...)

	if cfg.Auth != nil {
		ddl = append(ddl, generateRLSFunctions()...)
	}
	// Policies may call user RPCs, and SQL RPC bodies need the tables above.
	for _, n := range sortedKeys(cfg.RPC) {
		ddl = append(ddl, generateRPCFunction(n, cfg.RPC[n]))
	}
	for _, name := range ordered {
		ddl = append(ddl, generateRLSPolicies(name, cfg.Tables[name])...)
	}
	ddl = append(ddl, generateStorageRLSAll(cfg.Storage)...)

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
		ddl = append(ddl, generateGuards(name, newCfg.Tables[name])...)
	}

	// RLS functions (CREATE OR REPLACE)
	if newCfg.Auth != nil {
		ddl = append(ddl, generateRLSFunctions()...)
	}

	// Heal storage.objects on existing DBs. diffNewStorage only emits the
	// table (and its columns) when storage is *newly* added, so a DB that
	// already had storage.objects before user_metadata existed would never
	// gain the column. This idempotent ALTER runs on every migration.
	if len(newCfg.Storage) > 0 {
		ddl = append(ddl, `ALTER TABLE storage.objects ADD COLUMN IF NOT EXISTS user_metadata JSONB;`)
	}

	// RPCs come before the policies that may call them.
	for _, n := range sortedKeys(newCfg.RPC) {
		ddl = append(ddl, generateRPCFunction(n, newCfg.RPC[n]))
	}

	// RLS policies (DROP IF EXISTS + CREATE POLICY — idempotent)
	for _, name := range ordered {
		ddl = append(ddl, generateRLSPolicies(name, newCfg.Tables[name])...)
	}
	ddl = append(ddl, generateStorageRLSAll(newCfg.Storage)...)

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

	tx, err := m.beginLocked(ctx)
	if err != nil {
		return fmt.Errorf("migrate begin: %w", err)
	}
	// Rollback after Commit is a no-op.
	defer func() { _ = tx.Rollback(ctx) }()

	// Read under the lock so an instance that waited sees the winner's row.
	last, err := lastMigration(ctx, tx)
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

	stmts, err := m.planStatements(ctx, tx, oldCfg, cfg)
	if err != nil {
		// Apply stays pure: a rejected plan (e.g. ErrDestructive) runs no DDL, so
		// the interactive config editor can reject a bad edit without side
		// effects. The engine's boot/reconcile fallback calls ProvisionIdempotent
		// when a destructive change blocks the plan — that path has the
		// authoritative config, so a configured bucket still gets its DB backing.
		return fmt.Errorf("migrate plan: %w", err)
	}
	if len(stmts) == 0 {
		return nil
	}

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
	if err := recordMigration(ctx, tx, configChecksum, planSQL, string(configJSON)); err != nil {
		return fmt.Errorf("migrate record: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("migrate commit: %w", err)
	}
	return nil
}

// ProvisionIdempotent runs the additive, IF-NOT-EXISTS provisioning subset for
// cfg (schema grants, JWT keys, auth tables + RLS functions, storage.objects,
// storage RLS) without recording a migration row. The engine's boot/reconcile
// fallback calls it when a destructive change blocks the real plan, so a
// configured bucket still gets its DB backing while the drop stays gated and the
// engine reports drift. Safe to re-run: every statement is CREATE ... IF NOT
// EXISTS / CREATE OR REPLACE / DROP POLICY IF EXISTS + CREATE POLICY.
func (m *Migrator) ProvisionIdempotent(ctx context.Context, cfg *domain.Config) error {
	missing, err := m.missingStorageRPCs(ctx, cfg)
	if err != nil {
		return fmt.Errorf("provision: %w", err)
	}
	if len(missing) > 0 {
		m.logger.Warn("provision: storage policies calling rpcs not yet created deny all access until the blocked migration applies", "rpcs", missing)
		c := *cfg
		c.Storage = denyPoliciesCalling(cfg.Storage, missing)
		cfg = &c
	}
	prov := idempotentProvisioning(cfg, m.roles)
	if len(prov) == 0 {
		return nil
	}
	return m.applyStatements(ctx, prov)
}

// missingStorageRPCs returns config rpcs that storage policies call but the database lacks.
func (m *Migrator) missingStorageRPCs(ctx context.Context, cfg *domain.Config) ([]string, error) {
	called := rpcsCalledByStorage(cfg.Storage, sortedKeys(cfg.RPC))
	if len(called) == 0 {
		return nil, nil
	}
	rows, err := m.db.Query(ctx, `SELECT proname::text AS name FROM pg_proc WHERE pronamespace = 'public'::regnamespace AND proname = ANY($1)`, called)
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, fn := range called {
		if !slices.ContainsFunc(rows, func(r map[string]any) bool { return r["name"] == fn }) {
			missing = append(missing, fn)
		}
	}
	return missing, nil
}

// Harden re-applies security fixes on every boot, since an unchanged config never re-runs a migration.
func (m *Migrator) Harden(ctx context.Context, cfg *domain.Config) error {
	if err := m.db.EnsureMigrationsTable(ctx); err != nil {
		return fmt.Errorf("harden: %w", err)
	}
	// Best effort, so a busy table cannot crash-loop boot.
	m.healStorageList(ctx)
	stmts := append(generateJWTKeysTable(), generatePrivilegeRevokes(m.roles)...)
	if cfg != nil && cfg.Auth != nil {
		stmts = append(stmts, authHealDDL...)
	}
	applied, err := m.appliedStorage(ctx)
	if err != nil {
		return fmt.Errorf("harden: %w", err)
	}
	stmts = append(stmts, healStorageRLS(applied)...)
	if row, err := m.db.QueryRow(ctx, storageUploadedByFKNeedsHeal); err == nil && row["need"] == true {
		stmts = append(stmts, storageUploadedByFKHeal)
	}
	return m.applyStatements(ctx, stmts)
}

// healStorageList never fails boot.
func (m *Migrator) healStorageList(ctx context.Context) {
	row, err := m.db.QueryRow(ctx, storageHealMissing)
	if err != nil {
		m.logger.Warn("harden: storage list heal skipped, retrying next boot", "error", err)
		return
	}
	needName, needColumn := row["need_name_index"] == true, row["need_column"] == true
	if !needName && !needColumn {
		return
	}
	lockLeft := m.lockTimeout
	start := time.Now()
	size, err := m.storageSize(ctx, lockLeft)
	if m.lockTimeout > 0 {
		lockLeft = max(lockLeft-time.Since(start), time.Millisecond)
	}
	if err != nil {
		m.logger.Warn("harden: storage list heal skipped, storage.objects is locked; run the SQL by hand when the warning persists",
			"error", err, "sql", storageListManualSQL)
		return
	}
	if size > m.healMaxBytes {
		m.logger.Warn("harden: storage.objects is too large to change at boot, list stays on the slower query until you run the SQL by hand",
			"bytes", size, "max_bytes", m.healMaxBytes, "sql", storageListManualSQL)
		return
	}
	for _, step := range []struct {
		name string
		need bool
		sql  string
	}{{"name index", needName, storageNameIndexHeal}, {"column", needColumn, storageListColumnHeal}} {
		if !step.need {
			continue
		}
		waited, err := m.healTx(ctx, lockLeft, m.healTimeout, step.sql)
		if err != nil {
			m.logger.Warn("harden: storage list "+step.name+" heal skipped, retrying next boot", "error", err)
		}
		if m.lockTimeout > 0 {
			lockLeft = max(lockLeft-waited, time.Millisecond)
		}
	}
}

// storageSize reads the heap size in its own tx, since pg_relation_size waits behind an ACCESS EXCLUSIVE lock.
func (m *Migrator) storageSize(ctx context.Context, lockWait time.Duration) (int64, error) {
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('lock_timeout', $1, true)", strconv.FormatInt(lockWait.Milliseconds(), 10)+"ms"); err != nil {
		return 0, err
	}
	row, err := tx.QueryRow(ctx, storageHeapSize)
	if err != nil {
		return 0, err
	}
	size, _ := row["bytes"].(int64)
	return size, nil
}

// healTx runs one statement under the migration lock and returns how long the lock wait took.
func (m *Migrator) healTx(ctx context.Context, lockWait, timeout time.Duration, stmt string) (time.Duration, error) {
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('statement_timeout', $1, true)", strconv.FormatInt(timeout.Milliseconds(), 10)+"ms"); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('lock_timeout', $1, true)", strconv.FormatInt(lockWait.Milliseconds(), 10)+"ms"); err != nil {
		return 0, err
	}
	start := time.Now()
	_, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", domain.MigrationLockKey)
	waited := time.Since(start)
	if err != nil {
		return waited, err
	}
	if _, err := tx.Exec(ctx, stmt); err != nil {
		return waited, err
	}
	return waited, tx.Commit(ctx)
}

// appliedStorage returns the last migrated buckets, since a pending config may reference objects that don't exist yet.
func (m *Migrator) appliedStorage(ctx context.Context) (map[string]domain.Bucket, error) {
	last, err := m.db.GetLastMigration(ctx)
	if err != nil || last == nil || last.ConfigJSON == "" || last.ConfigJSON == "{}" {
		return nil, err
	}
	var cfg domain.Config
	if err := json.Unmarshal([]byte(last.ConfigJSON), &cfg); err != nil {
		m.logger.Warn("harden: skipping storage heal, applied config is unreadable", "error", err)
		return nil, nil
	}
	return cfg.Storage, nil
}

// healStorageRLS re-emits legacy storage policies, checking pg_policies first so a healed DB takes no lock.
func healStorageRLS(storage map[string]domain.Bucket) []string {
	stmts := dropPublicSelect(storage, true)
	var body []string
	for _, name := range sortedKeys(storage) {
		bucket := storage[name]
		var restrictive []string
		for i, p := range bucket.RLS {
			if p.Type != "restrictive" {
				continue
			}
			for _, op := range p.Operations {
				restrictive = append(restrictive, fmt.Sprintf("'storage_%s_%s_%d'", name, op, i))
			}
		}
		if len(restrictive) == 0 {
			continue
		}
		scoped := fmt.Sprintf("position('bucket_id <> ''%s''' IN %%[1]s) = 0", name)
		body = append(body, fmt.Sprintf(
			"IF EXISTS (SELECT 1 FROM pg_policies WHERE schemaname = 'storage' AND tablename = 'objects' AND policyname IN (%s) AND ((qual IS NOT NULL AND %s) OR (with_check IS NOT NULL AND %s))) THEN\n%s\nEND IF;",
			strings.Join(restrictive, ", "), fmt.Sprintf(scoped, "qual"), fmt.Sprintf(scoped, "with_check"),
			strings.Join(generateStorageRLS(name, bucket), "\n")))
	}
	if len(body) == 0 {
		return stmts
	}
	return append(stmts, fmt.Sprintf("DO $$ BEGIN\nIF to_regclass('storage.objects') IS NOT NULL THEN\n%s\nEND IF;\nEND $$;", strings.Join(body, "\n")))
}

// applyStatements runs stmts inside a single transaction without recording a
// migration row. Used for the additive provisioning that runs when a
// destructive change blocks the normal plan: it must not stamp _instancez_migrations
// (the destructive part is still pending, so the config is NOT fully applied and
// the engine should stay in drift), but the idempotent DDL must still land.
func (m *Migrator) applyStatements(ctx context.Context, stmts []string) error {
	if len(stmts) == 0 {
		return nil
	}
	tx, err := m.beginLocked(ctx)
	if err != nil {
		return fmt.Errorf("provision begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, stmt := range stmts {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("provision exec: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// beginLocked opens a tx holding the migration lock; lock_timeout covers DDL only.
// The lock wait is unbounded so waiters queue behind a slow migration.
func (m *Migrator) beginLocked(ctx context.Context) (domain.Tx, error) {
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", domain.MigrationLockKey); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	// SQL rpc bodies may reference tables created later in the same migration.
	// set_config with is_local=true is SET LOCAL with a bound value.
	timeout := strconv.FormatInt(m.lockTimeout.Milliseconds(), 10) + "ms"
	if _, err := tx.Exec(ctx, "SELECT set_config('lock_timeout', $1, true), set_config('check_function_bodies', 'off', true)", timeout); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}

// lastMigration reads on the locked tx; a pool read can deadlock a small pool.
func lastMigration(ctx context.Context, tx domain.Tx) (*domain.Migration, error) {
	row, err := tx.QueryRow(ctx,
		`SELECT checksum, config_json FROM _instancez_migrations ORDER BY id DESC LIMIT 1`)
	if err != nil || row == nil {
		return nil, err
	}
	checksum, _ := row["checksum"].(string)
	configJSON, _ := row["config_json"].(string)
	return &domain.Migration{Checksum: checksum, ConfigJSON: configJSON}, nil
}

// idempotentProvisioning returns the subset of the from-scratch plan that is
// purely additive and safe to re-run any number of times: schema grants, the
// JWT-keys store, auth tables (storage.objects FKs auth.users), the auth RLS
// functions (storage policies call auth.uid()), the storage metadata table, and
// the storage RLS policies. Every statement here uses CREATE ... IF NOT EXISTS,
// CREATE OR REPLACE, or DROP POLICY IF EXISTS + CREATE POLICY, so running it
// outside the diff — e.g. when a destructive change has blocked the real
// migration — can only create missing objects, never drop or alter data. Table
// and column diffs are deliberately excluded: those can be destructive or
// order-sensitive and belong to the gated plan.
func idempotentProvisioning(cfg *domain.Config, roles domain.Roles) []string {
	var ddl []string
	ddl = append(ddl, generateSchemaGrants(orderedSchemas(cfg), roles)...)
	ddl = append(ddl, generateJWTKeysTable()...)
	if cfg.Auth != nil {
		ddl = append(ddl, generateAuthTables(cfg.Auth)...)
		ddl = append(ddl, generateRLSFunctions()...)
	}
	ddl = append(ddl, generateStorageTables(cfg)...)
	ddl = append(ddl, generateStorageRLSAll(cfg.Storage)...)
	return ddl
}

// recordMigration inserts the history row on the given transaction, so it
// commits atomically with the DDL. The values arrive as parameters (both here
// and in the bound $1/$2/$3 placeholders): the query text is a constant and
// nothing is concatenated into it.
func recordMigration(ctx context.Context, tx domain.Tx, checksum, planSQL, configJSON string) error {
	// The query text is a constant and every value is bound as a $1/$2/$3
	// parameter, so nothing is interpolated into the SQL. The scanners flag any
	// Exec on a changed line regardless, so suppress the false positive here.
	//nosec G201,G202 -- constant query text; values are bound parameters
	// nosemgrep -- constant query text; values are bound parameters
	_, err := tx.Exec(ctx,
		`INSERT INTO _instancez_migrations (checksum, sql, config_json) VALUES ($1, $2, $3)`,
		checksum, planSQL, configJSON)
	return err
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
		grantees := tableGrantees(s, roles)
		ddl = append(ddl,
			fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s;", s),
			fmt.Sprintf("GRANT USAGE ON SCHEMA %s TO %s;", s, rlist),
			fmt.Sprintf("ALTER DEFAULT PRIVILEGES IN SCHEMA %s GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %s;", s, grantees),
			fmt.Sprintf("ALTER DEFAULT PRIVILEGES IN SCHEMA %s GRANT USAGE, SELECT ON SEQUENCES TO %s;", s, grantees),
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
	ddl := make([]string, 0, len(schemas)*2+6)
	for _, s := range schemas {
		grantees := tableGrantees(s, roles)
		ddl = append(ddl,
			fmt.Sprintf("GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA %s TO %s;", s, grantees),
			fmt.Sprintf("GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA %s TO %s;", s, grantees),
		)
		if roles.Seed != "" && !isReservedSchema(s) {
			ddl = append(ddl,
				fmt.Sprintf("GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA %s TO %s;", s, roles.Seed),
				fmt.Sprintf("GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA %s TO %s;", s, roles.Seed),
			)
		}
	}
	// Runs after the public backfill, which re-grants _instancez_migrations.
	return append(ddl, generatePrivilegeRevokes(roles)...)
}

func apiRoleList(roles domain.Roles) string {
	return fmt.Sprintf("%s, %s, %s", roles.Anon, roles.Authenticated, roles.Service)
}

// tableGrantees limits auth tables to service_role; other schemas get every API role.
func tableGrantees(schema string, roles domain.Roles) string {
	if schema == "auth" {
		return roles.Service
	}
	return apiRoleList(roles)
}

// generatePrivilegeRevokes strips API-role access to auth.* and migration history; idempotent.
func generatePrivilegeRevokes(roles domain.Roles) []string {
	users := fmt.Sprintf("%s, %s", roles.Anon, roles.Authenticated)
	history := apiRoleList(roles)
	if roles.Seed != "" {
		history += ", " + roles.Seed
	}
	return []string{
		fmt.Sprintf("REVOKE ALL ON ALL TABLES IN SCHEMA auth FROM %s;", users),
		fmt.Sprintf("REVOKE ALL ON ALL SEQUENCES IN SCHEMA auth FROM %s;", users),
		fmt.Sprintf("ALTER DEFAULT PRIVILEGES IN SCHEMA auth REVOKE ALL ON TABLES FROM %s;", users),
		fmt.Sprintf("ALTER DEFAULT PRIVILEGES IN SCHEMA auth REVOKE ALL ON SEQUENCES FROM %s;", users),
		fmt.Sprintf("REVOKE ALL ON auth.jwt_keys FROM %s;", apiRoleList(roles)),
		fmt.Sprintf("REVOKE ALL ON _instancez_migrations FROM %s;", history),
	}
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

// refreshTokensDDL is shared by generateAuthTables and diffNewAuth's
// heal-existing-deployments path so the two stay in sync.
const refreshTokensDDL = `CREATE TABLE IF NOT EXISTS auth.refresh_tokens (
  id BIGSERIAL PRIMARY KEY,
  user_id UUID NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
  token TEXT NOT NULL UNIQUE,
  expires_at TIMESTAMPTZ NOT NULL,
  session_id TEXT,
  ip TEXT,
  user_agent TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`

// authHealDDL adds auth columns and indexes introduced after the tables first shipped.
var authHealDDL = []string{
	healAuthIndex("idx_users_email_lower", "users", "lower(email)"),
	healAuthIndex("idx_mfa_challenges_factor_created", "mfa_challenges", "factor_id, created_at"),
	healAuthColumn("refresh_tokens", "revoked_at", "TIMESTAMPTZ"),
	healAuthColumn("refresh_tokens", "aal", "TEXT NOT NULL DEFAULT 'aal1'"),
	healAuthColumn("refresh_tokens", "amr", "JSONB NOT NULL DEFAULT '[]'::jsonb"),
	healAuthIndex("idx_refresh_tokens_session", "refresh_tokens", "session_id"),
	healAuthColumn("one_time_tokens", "attempts", "INT NOT NULL DEFAULT 0"),
	healAuthColumn("mfa_challenges", "attempts", "INT NOT NULL DEFAULT 0"),
	healAuthColumn("mfa_factors", "last_totp_step", "BIGINT"),
}

// healAuthColumn checks the catalog first, since ADD COLUMN IF NOT EXISTS locks the table even when it skips.
func healAuthColumn(table, column, def string) string {
	return fmt.Sprintf(`DO $$ BEGIN
IF to_regclass('auth.%[1]s') IS NOT NULL AND NOT EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid = to_regclass('auth.%[1]s') AND attname = '%[2]s' AND NOT attisdropped) THEN
  ALTER TABLE auth.%[1]s ADD COLUMN %[2]s %[3]s;
END IF;
END $$;`, table, column, def)
}

func healAuthIndex(name, table, expr string) string {
	return fmt.Sprintf(`DO $$ BEGIN
IF to_regclass('auth.%[2]s') IS NOT NULL AND to_regclass('auth.%[1]s') IS NULL THEN
  CREATE INDEX %[1]s ON auth.%[2]s (%[3]s);
END IF;
END $$;`, name, table, expr)
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

	ddl = append(ddl, refreshTokensDDL)

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

	return append(ddl, authHealDDL...)
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

	pkCols := table.PrimaryKeyColumns()
	composite := len(pkCols) > 1

	for _, field := range fields {
		fname := field.Name
		colField := field
		if composite {
			colField.PrimaryKey = false
		}
		cols = append(cols, formatColumn(fname, colField, allTables))

		// FK constraint
		if field.ForeignKey != nil {
			schema, refTable, refCol, err := domain.ParseFKReference(field.ForeignKey.References)
			if err != nil {
				// Surface the validation error as a SQL syntax error in the
				// emitted DDL, which the migrator will fail on with a clear
				// message. (Validation runs before this in normal flow.)
				constraints = append(constraints, fmt.Sprintf("/* invalid FK: %s */", err.Error()))
			} else if !deferSelfFK(name, table, field) {
				constraints = append(constraints,
					fmt.Sprintf("FOREIGN KEY (%s) REFERENCES %s.%s(%s) ON DELETE %s",
						fname, schema, refTable, refCol, fkOnDelete(field.ForeignKey)))
			}
		}

		// CHECK constraints from enum
		if len(field.Enum) > 0 {
			quoted := make([]string, len(field.Enum))
			for i, v := range field.Enum {
				quoted[i] = sqlLiteral(v)
			}
			constraints = append(constraints,
				fmt.Sprintf("CHECK (%s IN (%s))", fname, strings.Join(quoted, ", ")))
		}

		// CHECK from pattern
		if field.Pattern != "" {
			constraints = append(constraints,
				fmt.Sprintf("CHECK (%s ~ %s)", fname, sqlLiteral(field.Pattern)))
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

	if composite {
		constraints = append(constraints, fmt.Sprintf("PRIMARY KEY (%s)", strings.Join(pkCols, ", ")))
	}
	allParts := append(cols, constraints...)
	qualName := qualifiedTableName(name, table)
	ddl = append(ddl, fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (\n  %s\n);",
		qualName, strings.Join(allParts, ",\n  ")))

	return ddl
}

func fkOnDelete(fk *domain.ForeignKey) string {
	if fk.OnDelete == "" {
		return "RESTRICT"
	}
	return strings.ToUpper(strings.ReplaceAll(fk.OnDelete, "_", " "))
}

// deferSelfFK reports an FK to a column of its own table that only a later unique index can back.
func deferSelfFK(name string, table domain.Table, f domain.Field) bool {
	if f.ForeignKey == nil {
		return false
	}
	schema, refTable, refCol, err := domain.ParseFKReference(f.ForeignKey.References)
	if err != nil || refTable != name || schema != table.EffectiveSchema() {
		return false
	}
	ref, ok := table.GetField(refCol)
	if !ok {
		return false
	}
	return !ref.Unique && (!ref.PrimaryKey || len(table.PrimaryKeyColumns()) != 1)
}

// generateDeferredFKs adds the self-referencing FKs generateTable left out; call it after the table's indexes.
func generateDeferredFKs(name string, table domain.Table) []string {
	var ddl []string
	qual := qualifiedTableName(name, table)
	for _, f := range table.Fields {
		if !deferSelfFK(name, table, f) {
			continue
		}
		schema, refTable, refCol, _ := domain.ParseFKReference(f.ForeignKey.References)
		con := name + "_" + f.Name + "_fkey"
		if len(con) > 63 {
			con = con[:63]
		}
		ddl = append(ddl, fmt.Sprintf(`DO $$ BEGIN
IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = '%s'::regclass AND conname = '%s') THEN
  ALTER TABLE %s ADD CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s.%s(%s) ON DELETE %s;
END IF;
END $$;`, qual, con, qual, con, f.Name, schema, refTable, refCol, fkOnDelete(f.ForeignKey)))
	}
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
		using := ""
		if idx.EffectiveMethod() != "btree" {
			using = " USING " + idx.Method
		}
		ddl = append(ddl, fmt.Sprintf("CREATE %sINDEX IF NOT EXISTS %s ON %s%s (%s)%s;",
			unique, indexName, qualName, using, strings.Join(idx.Columns, ", "), where))
	}
	return ddl
}

// guardFuncName is the per-table trigger function; long names get a hash suffix to stay under 63 bytes.
func guardFuncName(name string, t domain.Table) string {
	fn := "inz_guard_" + name
	if len(fn) > 63 {
		sum := sha256.Sum256([]byte(fn))
		fn = fn[:54] + "_" + hex.EncodeToString(sum[:4])[:8]
	}
	return qualifiedTableName(fn, t)
}

const guardTrigger = "inz_guard"

// generateGuards emits the engine-managed BEFORE UPDATE trigger for auto_updated_at and immutable fields.
func generateGuards(name string, table domain.Table) []string {
	if !table.HasGuards() {
		return nil
	}
	autoUpdated, immutable := table.GuardedFields()
	var body strings.Builder
	for _, c := range immutable {
		fmt.Fprintf(&body, "  IF NEW.%[1]s IS DISTINCT FROM OLD.%[1]s THEN\n    RAISE EXCEPTION 'column \"%[1]s\" is immutable' USING ERRCODE = '23514';\n  END IF;\n", c)
	}
	for _, c := range autoUpdated {
		fmt.Fprintf(&body, "  NEW.%s := now();\n", c)
	}
	fn := guardFuncName(name, table)
	return []string{
		fmt.Sprintf("CREATE OR REPLACE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $guard$\nBEGIN\n%s  RETURN NEW;\nEND;\n$guard$;", fn, body.String()),
		fmt.Sprintf("CREATE OR REPLACE TRIGGER %s BEFORE UPDATE ON %s FOR EACH ROW EXECUTE FUNCTION %s();", guardTrigger, qualifiedTableName(name, table), fn),
	}
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

// resolverOver builds a domain.FieldResolver backed by tables, precomputing
// each table's FieldMap once rather than per lookup.
func resolverOver(tables map[string]domain.Table) domain.FieldResolver {
	if tables == nil {
		return nil
	}
	fm := make(map[string]map[string]domain.Field, len(tables))
	for name, t := range tables {
		fm[name] = t.FieldMap()
	}
	return func(table, col string) (domain.Field, bool) {
		f, ok := fm[table][col]
		return f, ok
	}
}

// effectiveType returns the resolved SQL type for a field, inferring an
// untyped FK column's type from its referenced column via tables.
func effectiveType(f domain.Field, tables map[string]domain.Table) string {
	return domain.EffectiveType(f, resolverOver(tables))
}

func formatColumn(name string, field domain.Field, tables map[string]domain.Table) string {
	typ := effectiveType(field, tables)

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

func sqlLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
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
		return sqlLiteral(v)
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
  uploaded_by UUID REFERENCES auth.users(id) ON DELETE SET NULL,
  uploaded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  metadata JSONB,
  user_metadata JSONB,
  name_lower TEXT COLLATE "C" GENERATED ALWAYS AS (lower(name)) STORED,
  UNIQUE (bucket_id, name)
);`,
		// Additive column for deployments whose storage.objects table predates
		// user_metadata. supabase-js's .list() carries user_metadata separately
		// from the storage-managed metadata blob.
		`ALTER TABLE storage.objects ADD COLUMN IF NOT EXISTS user_metadata JSONB;`,
		storageIndexesWhenEmpty,
	}
}

// storageUploadedByFKNeedsHeal is a catalog-only check, so a healed DB skips the heal on boot.
const storageUploadedByFKNeedsHeal = `SELECT to_regclass('storage.objects') IS NOT NULL AND to_regclass('auth.users') IS NOT NULL AND EXISTS (
  SELECT 1 FROM pg_constraint
  WHERE conrelid = 'storage.objects'::regclass AND confrelid = 'auth.users'::regclass
    AND contype = 'f' AND confdeltype <> 'n'
    AND conkey = ARRAY[(SELECT attnum FROM pg_attribute WHERE attrelid = 'storage.objects'::regclass AND attname = 'uploaded_by')]
) AS need`

// storageUploadedByFKHeal drops every uploaded_by FK lacking ON DELETE SET NULL, then re-adds one.
const storageUploadedByFKHeal = `DO $$
DECLARE c RECORD; dropped boolean := false;
BEGIN
  IF to_regclass('storage.objects') IS NULL OR to_regclass('auth.users') IS NULL THEN RETURN; END IF;
  FOR c IN SELECT conname FROM pg_constraint
    WHERE conrelid = 'storage.objects'::regclass AND confrelid = 'auth.users'::regclass
      AND contype = 'f' AND confdeltype <> 'n'
      AND conkey = ARRAY[(SELECT attnum FROM pg_attribute WHERE attrelid = 'storage.objects'::regclass AND attname = 'uploaded_by')]
  LOOP
    EXECUTE format('ALTER TABLE storage.objects DROP CONSTRAINT %I', c.conname);
    dropped := true;
  END LOOP;
  IF dropped THEN
    ALTER TABLE storage.objects ADD CONSTRAINT objects_uploaded_by_fkey FOREIGN KEY (uploaded_by) REFERENCES auth.users(id) ON DELETE SET NULL;
  END IF;
END $$;`

// storageListManualSQL is the by-hand equivalent of the boot heal for tables too large to change at boot; drop an INVALID index first.
const storageListManualSQL = `ALTER TABLE storage.objects ADD COLUMN name_lower TEXT COLLATE "C" GENERATED ALWAYS AS (lower(name)) STORED; ` +
	`CREATE INDEX CONCURRENTLY IF NOT EXISTS objects_bucket_name_c_idx ON storage.objects ` + nameIndexDef + `; ` +
	`CREATE INDEX CONCURRENTLY IF NOT EXISTS objects_bucket_name_lower_c_idx ON storage.objects ` + nameLowerIndexDef + `;`

// storageHealMissing reads the catalog only, so it takes no table lock; an INVALID index counts as missing.
const storageHealMissing = `SELECT
  t.oid IS NOT NULL AND NOT EXISTS (SELECT 1 FROM pg_index i WHERE i.indexrelid = to_regclass('storage.objects_bucket_name_c_idx') AND i.indisvalid) AS need_name_index,
  t.oid IS NOT NULL AND (NOT EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid = t.oid AND attname = 'name_lower' AND NOT attisdropped)
    OR NOT EXISTS (SELECT 1 FROM pg_index i WHERE i.indexrelid = to_regclass('storage.objects_bucket_name_lower_c_idx') AND i.indisvalid)) AS need_column
FROM (SELECT to_regclass('storage.objects') AS oid) t`

// storageHeapSize takes ACCESS SHARE on the table, so it must run under a lock_timeout.
const storageHeapSize = `SELECT COALESCE(pg_relation_size(to_regclass('storage.objects')), 0)::bigint AS bytes`

// ensureStorageIndex builds a list index unless a valid one exists, dropping an INVALID leftover first.
func ensureStorageIndex(name, def string) string {
	return fmt.Sprintf(`IF NOT EXISTS (SELECT 1 FROM pg_index i WHERE i.indexrelid = to_regclass('storage.%[1]s') AND i.indisvalid) THEN
    DROP INDEX IF EXISTS storage.%[1]s;
    CREATE INDEX %[1]s ON storage.objects %[2]s;
  END IF;`, name, def)
}

const (
	nameIndexDef      = `(bucket_id, name COLLATE "C")`
	nameLowerIndexDef = `(bucket_id, name_lower, (name COLLATE "C"))`
)

var (
	// storageNameIndexHeal builds the v2 list index.
	storageNameIndexHeal = `DO $$ BEGIN
IF to_regclass('storage.objects') IS NOT NULL THEN
  ` + ensureStorageIndex("objects_bucket_name_c_idx", nameIndexDef) + `
END IF;
END $$;`

	// storageListColumnHeal adds name_lower and its index.
	storageListColumnHeal = `DO $$ BEGIN
IF to_regclass('storage.objects') IS NOT NULL THEN
  IF NOT EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid = 'storage.objects'::regclass AND attname = 'name_lower' AND NOT attisdropped) THEN
    ALTER TABLE storage.objects ADD COLUMN name_lower TEXT COLLATE "C" GENERATED ALWAYS AS (lower(name)) STORED;
  END IF;
  ` + ensureStorageIndex("objects_bucket_name_lower_c_idx", nameLowerIndexDef) + `
END IF;
END $$;`

	// storageIndexesWhenEmpty indexes a fresh table for free; a populated one is left to the bounded Harden heal.
	storageIndexesWhenEmpty = `DO $$ BEGIN
IF NOT EXISTS (SELECT 1 FROM storage.objects LIMIT 1) THEN
  ` + ensureStorageIndex("objects_bucket_name_c_idx", nameIndexDef) + `
  IF EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid = 'storage.objects'::regclass AND attname = 'name_lower' AND NOT attisdropped) THEN
    ` + ensureStorageIndex("objects_bucket_name_lower_c_idx", nameLowerIndexDef) + `
  END IF;
END IF;
END $$;`
)

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
	if !table.EffectiveRLSEnabled() {
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

	ddl := dropPublicSelect(storage, false)
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
		}
	}
	return ddl
}

// dropPublicSelect drops every bucket's pre-parity <bucket>_public_select; guarded checks pg_policies first, whose name literal truncates to 63 bytes like the policy.
func dropPublicSelect(storage map[string]domain.Bucket, guarded bool) []string {
	var ddl []string
	for _, name := range sortedKeys(storage) {
		if guarded {
			ddl = append(ddl, fmt.Sprintf(
				"IF EXISTS (SELECT 1 FROM pg_policies WHERE schemaname = 'storage' AND tablename = 'objects' AND policyname = '%s_public_select') THEN\nDROP POLICY %s_public_select ON storage.objects;\nEND IF;",
				name, name))
		} else {
			ddl = append(ddl, fmt.Sprintf("DROP POLICY IF EXISTS %s_public_select ON storage.objects;", name))
		}
	}
	if !guarded || len(ddl) == 0 {
		return ddl
	}
	return []string{fmt.Sprintf("DO $$ BEGIN\n%s\nEND $$;", strings.Join(ddl, "\n"))}
}

func generateStorageRLS(bucketName string, bucket domain.Bucket) []string {
	var ddl []string
	for i, policy := range bucket.RLS {
		typeClause := rlsPolicyTypeClause(policy)
		restrictive := policy.Type == "restrictive"
		scopedUsing := scopeToBucket(bucketName, policy.Using, restrictive)
		scopedWithCheck := scopeToBucket(bucketName, policy.WithCheck, restrictive)
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

// scopeToBucket confines a possibly-empty RLS expression to bucketName, using OR-negation for restrictive policies so they don't AND-deny other buckets.
func scopeToBucket(bucketName, expr string, restrictive bool) string {
	if expr == "" {
		return ""
	}
	if restrictive {
		return fmt.Sprintf("bucket_id <> '%s' OR (%s)", bucketName, expr)
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
		if str, ok := a.Default.(string); ok && strings.EqualFold(strings.TrimSpace(str), domain.NullDefault) {
			piece += " DEFAULT NULL"
		} else if a.Default != nil {
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
		"CREATE OR REPLACE FUNCTION public.\"%s\"(%s)\nRETURNS %s\nLANGUAGE %s\n%s\n%s%s\nAS $ub$%s$ub$;",
		name,
		strings.Join(sig, ", "),
		fn.Returns.Type,
		language,
		volatility,
		security,
		rpcSetClauses(fn.Set),
		fn.Body,
	)
}

// rpcSetClauses renders validated set: entries as SET lines in key order.
func rpcSetClauses(set map[string]string) string {
	var b strings.Builder
	for _, k := range sortedKeys(set) {
		v := strings.TrimSpace(set[k])
		switch {
		case k == "search_path" && v == "":
			v = "''"
		case k == "search_path":
			parts := strings.Split(v, ",")
			for i := range parts {
				parts[i] = strings.TrimSpace(parts[i])
			}
			v = strings.Join(parts, ", ")
		default:
			v = "'" + v + "'"
		}
		fmt.Fprintf(&b, "\nSET %s = %s", k, v)
	}
	return b.String()
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
