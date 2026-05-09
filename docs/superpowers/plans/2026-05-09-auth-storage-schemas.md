# Auth + storage schemas — implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move auth tables into an `auth` schema and the storage objects table into a `storage` schema, matching Supabase's SQL surface. Drop the legacy "extra fields on `tables.users`" capability so profiles become a regular user-defined table FK'd to `auth.users.id`.

**Architecture:** All work is in the Go backend (`internal/app/migrate.go` for DDL emission, `internal/app/migrate_config_diff.go` for diff plans, `internal/adapter/http/*_handler.go` for runtime SQL strings, `internal/domain/schema.go` for validation). The change is wide but shallow: no new ports, no new lifecycle hooks. The FK parser learns about 3-part schema-qualified references (`auth.users.id`); the DDL emitter learns about per-table schemas it didn't author; HTTP handlers just take new SQL strings.

**Tech Stack:** Go 1.22+, pgx/v5, gin, testcontainers-go (integration), Node + `@supabase/supabase-js` (compat suite).

---

## Spec reference

Authoritative spec: `docs/superpowers/specs/2026-05-09-auth-storage-schemas-design.md`. Re-read before starting work; any deviation requires updating the spec first.

## Pre-flight

This plan was written without first creating an isolated worktree. Before starting Phase 1, decide whether to work on `main` or branch off:

```bash
# recommended: dedicated worktree
git worktree add ../ultrabase-auth-schemas -b feat/auth-storage-schemas
cd ../ultrabase-auth-schemas
```

The CLAUDE.md non-negotiable applies: `go build ./... && go test -race ./... && go test -tags=integration -race ./<changed-pkg>` must pass before any push, plus `cd dashboard && npm test` if you touch the dashboard.

## File map

**Modified (Go):**
- `internal/domain/schema.go` — drop `coreUserColumns` + `UserExtraFields()`, add validation for reserved schemas
- `internal/domain/schema_test.go` — update / add tests
- `internal/app/migrate.go` — FK parser, `effectiveType`, `generateUsersTable` → `generateAuthTables`, `generateStorageTables`, `orderedSchemas`, `generateRLSPolicies`, `generateIndexes`, `orderTables`, schema-qualified-name helper
- `internal/app/migrate_test.go` — assertions across all renames
- `internal/app/migrate_integration_test.go` — `tableExists`/`columnExists`/`policyExists` schema-aware; assertions across renames
- `internal/app/migrate_config_diff.go` — `_objects` → `storage.objects` everywhere; bucket-policy DDL schema-qualified
- `internal/app/migrate_config_diff_test.go` — assertion updates
- `internal/app/engine.go` — `validateDataColumns`, `applyDataRow`, `orderDataTables`, `orderTables` (call site) — replace `"users"` literal with `"auth.users"`
- `internal/app/engine_test.go` — seed-order / data validation tests
- `internal/adapter/http/auth_handler.go` — every direct SQL string targeting users / refresh_tokens / email_verifications / auth_codes / oauth_states
- `internal/adapter/http/auth_handler_test.go` — adjust mocks/expectations
- `internal/adapter/http/mfa_handler.go` — every SQL string targeting mfa_factors / mfa_challenges / users
- `internal/adapter/http/mfa_handler_test.go`
- `internal/adapter/http/storage_v1_handler.go` and `storage_handler.go` — every `_objects`
- `internal/adapter/http/supabase_integration_test.go` and `test/integration/supabase-js/run.mjs` — table-name updates in setup, plus a new cross-schema `profiles` assertion
- `ultrabase.yaml` — drop `tables.users.fields`, add commented `profiles` example
- `docs/examples/react-catalog/ultrabase.yaml` — drop `tables.users.fields`, add `profiles`, update `references: users.id` → `auth.users.id`
- `docs/examples/react-catalog/README.md` — one-paragraph note
- `CLAUDE.md` — gotchas section
- `dashboard/src/pages/Auth.tsx:86` — page-description copy update

**Untouched but referenced:**
- `internal/cli/dbsetup.go` — only opens pools and reads role-name env vars; no schema-aware change needed (grants flow through `orderedSchemas`)
- `internal/app/drift.go` — checksum-based, schema-agnostic
- WAL replication — uses wal2json with no publication filter; schema move is invisible at the slot level. Event filtering already happens by `table.operation` pattern in `cfg.On`, which user-defined patterns won't match against `auth.*` / `storage.*` unless the user writes those literally
- `internal/domain/database.go` — `Roles`, `Session`, `Database` interface unchanged

---

## Test conventions in this repo

- Unit tests: `go test -race ./<package>`, files named `*_test.go`, package matches the dir (not `_test`).
- Integration tests: `//go:build integration` build tag at the top of the file; testcontainers spins up Postgres. Run with `go test -tags=integration -race ./...`.
- Single test: `go test -run TestNameRegex -race ./internal/...`.
- The supabase-js compat test: `go test -run TestSupabaseJSCompat -tags=integration -race ./internal/adapter/http/...`.
- Dashboard: `cd dashboard && npm test`.
- Local feedback loop (must pass before commit per CLAUDE.md):
  ```sh
  go build ./... \
    && go test -race ./... \
    && go test -tags=integration -race ./internal/app/... ./internal/adapter/http/... \
    && (cd dashboard && npm test)
  ```

---

# Phase 1 — Foundation (parser, validation, helper)

## Task 1: Schema-aware FK reference parser

**Files:**
- Modify: `internal/app/migrate.go` — extract a helper, use it from FK constraint emission and `orderTables`
- Test: `internal/app/migrate_test.go` (new test function appended)

- [ ] **Step 1: Write the failing parser test**

Append to `internal/app/migrate_test.go`:

```go
func TestParseFKReference(t *testing.T) {
	tests := []struct {
		in        string
		schema    string
		table     string
		column    string
		expectErr bool
	}{
		{"posts.id", "public", "posts", "id", false},
		{"auth.users.id", "auth", "users", "id", false},
		{"id", "", "", "", true},                          // no column
		{"a.b.c.d", "", "", "", true},                     // too many parts
		{"", "", "", "", true},                            // empty
		{"public.posts.id", "public", "posts", "id", false}, // explicit public allowed
	}
	for _, tt := range tests {
		s, table, col, err := parseFKReference(tt.in)
		if (err != nil) != tt.expectErr {
			t.Errorf("parseFKReference(%q) err=%v want err=%v", tt.in, err, tt.expectErr)
			continue
		}
		if tt.expectErr {
			continue
		}
		if s != tt.schema || table != tt.table || col != tt.column {
			t.Errorf("parseFKReference(%q) = (%q, %q, %q); want (%q, %q, %q)",
				tt.in, s, table, col, tt.schema, tt.table, tt.column)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```sh
go test -run TestParseFKReference -race ./internal/app/...
```
Expected: FAIL with "undefined: parseFKReference".

- [ ] **Step 3: Implement the parser**

In `internal/app/migrate.go`, add (near the existing `effectiveType` / FK helper code, around line 600):

```go
// parseFKReference splits a foreign-key target string into (schema, table, column).
// 2-part inputs default to the public schema; 3-part inputs are schema-qualified.
// Anything else is an error.
func parseFKReference(ref string) (schema, table, column string, err error) {
	parts := strings.Split(ref, ".")
	switch len(parts) {
	case 2:
		return "public", parts[0], parts[1], nil
	case 3:
		return parts[0], parts[1], parts[2], nil
	default:
		return "", "", "", fmt.Errorf("invalid foreign_key.references %q: expected table.column or schema.table.column", ref)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

```sh
go test -run TestParseFKReference -race ./internal/app/...
```
Expected: PASS.

- [ ] **Step 5: Use the helper in FK constraint emission**

In `internal/app/migrate.go`, replace the FK block in `generateTable` (currently at `migrate.go:516-527`):

```go
		// FK constraint
		if field.ForeignKey != nil {
			schema, refTable, refCol, err := parseFKReference(field.ForeignKey.References)
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
```

- [ ] **Step 6: Use the helper in `orderTables`**

Replace `migrate.go:867-873`:

```go
			if field.ForeignKey != nil {
				_, refTable, _, err := parseFKReference(field.ForeignKey.References)
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
```

- [ ] **Step 7: Run all unit tests**

```sh
go test -race ./internal/app/...
```
Expected: PASS. Existing FK tests should still pass (they use 2-part refs, which the helper preserves).

- [ ] **Step 8: Commit**

```sh
git add internal/app/migrate.go internal/app/migrate_test.go
git commit -m "Schema-aware FK reference parser"
```

---

## Task 2: FK auto-type rule keys off `auth.users.id`

**Files:**
- Modify: `internal/app/migrate.go` (`effectiveType` around `migrate.go:603`)
- Test: `internal/app/migrate_test.go` (new test)

- [ ] **Step 1: Write the failing test**

```go
func TestEffectiveTypeAutoUUIDForAuthUsers(t *testing.T) {
	cases := []struct {
		name string
		f    domain.Field
		want string
	}{
		{"auth.users.id → UUID", domain.Field{ForeignKey: &domain.ForeignKey{References: "auth.users.id"}}, "UUID"},
		{"posts.id → BIGINT", domain.Field{ForeignKey: &domain.ForeignKey{References: "posts.id"}}, "BIGINT"},
		{"explicit type wins", domain.Field{Type: "TEXT", ForeignKey: &domain.ForeignKey{References: "auth.users.id"}}, "TEXT"},
		{"legacy users.id no longer auto-uuids", domain.Field{ForeignKey: &domain.ForeignKey{References: "users.id"}}, "BIGINT"},
	}
	for _, tt := range cases {
		got := effectiveType(tt.f)
		if got != tt.want {
			t.Errorf("%s: got %q want %q", tt.name, got, tt.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```sh
go test -run TestEffectiveTypeAutoUUIDForAuthUsers -race ./internal/app/...
```
Expected: the legacy-users case fails because today's code still maps `users.id` → UUID.

- [ ] **Step 3: Update `effectiveType`**

Replace `migrate.go:603-614`:

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

```sh
go test -run TestEffectiveTypeAutoUUIDForAuthUsers -race ./internal/app/...
```
Expected: PASS.

- [ ] **Step 5: Run all unit tests**

```sh
go test -race ./internal/app/...
```
Expected: PASS. (Tests using `users.id` 2-part form for the auth user table are about to be deleted in subsequent tasks.)

- [ ] **Step 6: Commit**

```sh
git add internal/app/migrate.go internal/app/migrate_test.go
git commit -m "Auto-infer UUID for auth.users.id FK references"
```

---

## Task 3: Reject `schema: auth` and `schema: storage` in YAML validation

**Files:**
- Modify: `internal/domain/schema.go` — find `(c *Config) Validate()` (or equivalent) and add a reserved-schema check
- Test: `internal/domain/schema_test.go` (new test)

- [ ] **Step 1: Find the validation entry point**

```sh
grep -n "func.*Config.*Validate\|func.*Validate.*Config\|func validateConfig" internal/domain/schema.go internal/domain/schema_test.go
```

The plan assumes the validator is `(*Config).Validate()` returning `error`. If the actual entry point differs, add the check there instead and adjust the test.

- [ ] **Step 2: Write the failing test**

Append to `internal/domain/schema_test.go`:

```go
func TestValidateRejectsReservedSchemas(t *testing.T) {
	for _, reserved := range []string{"auth", "storage"} {
		cfg := &domain.Config{
			Tables: map[string]domain.Table{
				"sneaky": {Schema: reserved, Fields: []domain.Field{
					{Name: "id", Type: "BIGINT", PrimaryKey: true},
				}},
			},
		}
		err := cfg.Validate()
		if err == nil {
			t.Fatalf("schema=%q: expected validation error, got nil", reserved)
		}
		if !strings.Contains(err.Error(), reserved) {
			t.Errorf("schema=%q: error %q should mention the reserved schema name", reserved, err.Error())
		}
	}
}
```

(If `domain` test files don't import `strings`, add it to the `_test.go` imports.)

- [ ] **Step 3: Run test to verify it fails**

```sh
go test -run TestValidateRejectsReservedSchemas -race ./internal/domain/...
```
Expected: FAIL.

- [ ] **Step 4: Implement the check**

Add to `(*Config).Validate()` (location depends on existing structure — append to the end of the table-iteration loop):

```go
	for name, table := range c.Tables {
		switch table.Schema {
		case "auth", "storage":
			return fmt.Errorf("table %q declares schema %q, which is reserved by the framework", name, table.Schema)
		}
		// (existing per-table validation continues here)
	}
```

- [ ] **Step 5: Run test to verify it passes**

```sh
go test -run TestValidateRejectsReservedSchemas -race ./internal/domain/...
```
Expected: PASS.

- [ ] **Step 6: Commit**

```sh
git add internal/domain/schema.go internal/domain/schema_test.go
git commit -m "Reject reserved schemas auth/storage in YAML validation"
```

---

## Task 4: Drop `coreUserColumns` and `Config.UserExtraFields()`

**Files:**
- Modify: `internal/domain/schema.go:46-68` — delete the map and method
- Modify: `internal/app/engine.go:497-501` — remove the `e.cfg.UserExtraFields()` call (Task 21 covers the rest of this function's rewrite, but the compile error from this delete needs an immediate stub)

- [ ] **Step 1: Delete the map and method**

In `internal/domain/schema.go`, remove lines 46–68 entirely:

```go
// coreUserColumns are auto-emitted by the migrator and should not be
// treated as user-defined fields when iterating tables.users.
var coreUserColumns = map[string]bool{ ... }

// UserExtraFields returns the custom (non-core) fields from tables.users.
func (c *Config) UserExtraFields() []Field { ... }
```

- [ ] **Step 2: Remove the only caller in engine.go**

In `internal/app/engine.go:497-501`, the loop:

```go
		for _, f := range e.cfg.UserExtraFields() {
			known[f.Name] = true
		}
```

Delete those four lines. (Task 21 fully rewrites this validator.)

- [ ] **Step 3: Build to confirm compile**

```sh
go build ./...
```
Expected: builds cleanly. If anything else referenced `UserExtraFields` or `coreUserColumns`, the compile error tells you exactly where.

- [ ] **Step 4: Run unit tests**

```sh
go test -race ./internal/domain/... ./internal/app/...
```
Some tests will fail because the merge behavior they tested no longer exists; those tests get rewritten in later tasks. For now, confirm only the *expected* failures (anything matching "extra_fields", "user_extra", "display_name in users") are red.

- [ ] **Step 5: Commit**

```sh
git add internal/domain/schema.go internal/app/engine.go
git commit -m "Remove tables.users extra-fields capability"
```

---

## Task 5: Schema-qualified table-name helper, used by indexes and RLS

**Files:**
- Modify: `internal/app/migrate.go` — extract the existing `qualName` block at `:568-571` into a helper, use it from `generateRLSPolicies` (`:761`) and `generateIndexes` (`:583`)
- Test: `internal/app/migrate_test.go` (new test for `qualifiedTableName` plus updated assertions on RLS/index DDL)

- [ ] **Step 1: Write the failing test**

```go
func TestQualifiedTableName(t *testing.T) {
	cases := []struct {
		name  string
		table domain.Table
		want  string
	}{
		{"public default", domain.Table{}, "posts"},
		{"explicit public", domain.Table{Schema: "public"}, "posts"},
		{"non-default", domain.Table{Schema: "analytics"}, "analytics.posts"},
	}
	for _, tt := range cases {
		got := qualifiedTableName("posts", tt.table)
		if got != tt.want {
			t.Errorf("%s: got %q want %q", tt.name, got, tt.want)
		}
	}
}

func TestRLSAndIndexesUseQualifiedNames(t *testing.T) {
	tables := map[string]domain.Table{
		"posts": {
			Schema: "analytics",
			Fields: []domain.Field{{Name: "id", Type: "BIGINT", PrimaryKey: true}},
			Indexes: []domain.Index{{Columns: []string{"id"}}},
			RLS: []domain.RLSPolicy{
				{Operations: []string{"select"}, Check: "true"},
			},
		},
	}
	idx := strings.Join(generateIndexes("posts", tables["posts"]), "\n")
	if !strings.Contains(idx, "ON analytics.posts (") {
		t.Errorf("expected schema-qualified index DDL, got: %s", idx)
	}
	rls := strings.Join(generateRLSPolicies("posts", tables["posts"]), "\n")
	if !strings.Contains(rls, "ALTER TABLE analytics.posts") {
		t.Errorf("expected schema-qualified RLS DDL, got: %s", rls)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```sh
go test -run "TestQualifiedTableName|TestRLSAndIndexesUseQualifiedNames" -race ./internal/app/...
```
Expected: both FAIL.

- [ ] **Step 3: Add the helper and use it**

In `internal/app/migrate.go`, near the other small helpers around line 600:

```go
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
```

Replace the inline `qualName` block in `generateTable` (`:568-571`) with:

```go
	qualName := qualifiedTableName(name, table)
```

Update `generateIndexes` (`:583-599`):

```go
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
```

Update `generateRLSPolicies` (`:761-789`):

```go
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
```

Note: the policy *name* (`policyName`) intentionally stays unqualified — it's a Postgres identifier scoped to the (table, schema) pair, not a schema name itself.

- [ ] **Step 4: Run tests to verify they pass**

```sh
go test -run "TestQualifiedTableName|TestRLSAndIndexesUseQualifiedNames" -race ./internal/app/...
```
Expected: PASS.

- [ ] **Step 5: Run all unit tests**

```sh
go test -race ./internal/app/...
```
Expected: PASS. Existing tests for public-schema tables still emit unqualified names because `qualifiedTableName` returns the bare name for `public`.

- [ ] **Step 6: Commit**

```sh
git add internal/app/migrate.go internal/app/migrate_test.go
git commit -m "Schema-qualified table-name helper for indexes and RLS"
```

---

# Phase 2 — Migrator: auth schema

## Task 6: `generateUsersTable` → `generateAuthTables` with renamed/qualified tables

**Files:**
- Modify: `internal/app/migrate.go:351-493`
- Modify: any caller of `generateUsersTable` (search for it; should be in `migrate.go` itself or `migrate_config_diff.go`)
- Modify: `internal/app/migrate_test.go` — update existing assertions for new names

- [ ] **Step 1: Find the caller(s)**

```sh
grep -n "generateUsersTable" internal/app/
```
Note where it's called from. The signature change `(auth, extraFields []domain.Field)` → `(auth)` requires updating the call site.

- [ ] **Step 2: Update existing test expectations to fail loudly first**

In `internal/app/migrate_test.go`, find the test asserting `CREATE TABLE IF NOT EXISTS _user_identities` (around line 169) and change the expectation to `CREATE TABLE IF NOT EXISTS auth.identities`. Same for any other assertions about `users`, `_refresh_tokens`, `_auth_email_verifications`, `_auth_jwt_keys`, `_mfa_factors`, `_mfa_challenges`. Run the tests — they should fail with the *new* expected strings vs. the *old* generated DDL:

```sh
go test -run TestMigrateGenerateAuthTables -race ./internal/app/... -v
```
(The exact test name in your repo may differ; pick the one that asserts the auth-table DDL.) Expected: FAIL on the new strings.

- [ ] **Step 3: Rewrite the function**

Replace `internal/app/migrate.go:351-493` with the new implementation. The full body:

```go
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

	// JWT signing keys.
	ddl = append(ddl, `CREATE TABLE IF NOT EXISTS auth.jwt_keys (
  kid TEXT PRIMARY KEY,
  secret BYTEA NOT NULL,
  algorithm TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  retired_at TIMESTAMPTZ
);`)

	// OAuth identities.
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

	// MFA: TOTP factors + challenges.
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

	return ddl
}
```

Note the changes vs. the old function:
- No `extraFields` parameter and no per-extra-field columns.
- `email TEXT UNIQUE` (was previously `NOT NULL UNIQUE` + later `ALTER COLUMN email DROP NOT NULL`). Clean break — no historical ALTER needed.
- `is_anonymous` is now declared inline (was a follow-up `ALTER TABLE … ADD COLUMN IF NOT EXISTS`). Same reason.
- `_user_identities` → `auth.identities`.
- `_refresh_tokens` → `auth.refresh_tokens`. The session_id/ip/user_agent ALTERs are folded into the CREATE — clean break.
- `_auth_email_verifications` → `auth.one_time_tokens`. Fold the `code` and `email` ALTERs into CREATE.
- `_mfa_factors` / `_mfa_challenges` → `auth.mfa_factors` / `auth.mfa_challenges`.
- `_oauth_states` and `_auth_codes` are NOT here — they get replaced by `auth.flow_state` in Task 7.

- [ ] **Step 4: Update the call site**

Find where `generateUsersTable(cfg.Auth, cfg.UserExtraFields())` was called and change it to `generateAuthTables(cfg.Auth)`. Remove any `if len(extraFields) > 0` branches.

- [ ] **Step 5: Run the tests you primed in Step 2**

```sh
go test -race ./internal/app/...
```
Expected: tests using the new auth-schema names pass. Tests that hard-code old names (e.g. assertions for `CREATE TABLE IF NOT EXISTS users`) fail and need updating in Step 6.

- [ ] **Step 6: Sweep `migrate_test.go` and `migrate_config_diff_test.go` for the old names**

```sh
grep -nE 'CREATE TABLE IF NOT EXISTS (users|_user_identities|_refresh_tokens|_auth_email_verifications|_auth_jwt_keys|_mfa_factors|_mfa_challenges|_oauth_states|_auth_codes)\b' internal/app/migrate_test.go internal/app/migrate_config_diff_test.go
```

Update each match:
- `users` → `auth.users`
- `_user_identities` → `auth.identities`
- `_refresh_tokens` → `auth.refresh_tokens`
- `_auth_email_verifications` → `auth.one_time_tokens`
- `_auth_jwt_keys` → `auth.jwt_keys`
- `_mfa_factors` → `auth.mfa_factors`
- `_mfa_challenges` → `auth.mfa_challenges`
- `_oauth_states`, `_auth_codes` → these are *gone*; their replacement (`auth.flow_state`) appears in Task 7. Mark these failing assertions with a `// TODO Task 7` comment for now and rerun after Task 7.

- [ ] **Step 7: Run unit tests**

```sh
go test -race ./internal/app/...
```
Expected: all the renamed-table assertions pass. The `_oauth_states` / `_auth_codes` assertions you commented out for Task 7 are still red but you'll fix them shortly.

- [ ] **Step 8: Commit**

```sh
git add internal/app/migrate.go internal/app/migrate_test.go internal/app/migrate_config_diff_test.go
git commit -m "Move auth tables into auth schema with renamed identifiers"
```

---

## Task 7: `auth.flow_state` consolidates PKCE + OAuth state

**Files:**
- Modify: `internal/app/migrate.go` — add a `flow_state` block to `generateAuthTables` (returning to the function from Task 6)
- Modify: `internal/app/migrate_test.go` — finish updating the assertions left as `// TODO Task 7`

- [ ] **Step 1: Write the failing test**

Append to `internal/app/migrate_test.go`:

```go
func TestGenerateAuthFlowState(t *testing.T) {
	ddl := strings.Join(generateAuthTables(&domain.Auth{}), "\n")
	mustContain(t, ddl, "CREATE TABLE IF NOT EXISTS auth.flow_state")
	mustContain(t, ddl, "auth_code TEXT")
	mustContain(t, ddl, "code_challenge TEXT")
	mustContain(t, ddl, "code_challenge_method TEXT")
	mustContain(t, ddl, "provider_type TEXT NOT NULL")
	mustContain(t, ddl, "provider_access_token TEXT")
	mustContain(t, ddl, "provider_refresh_token TEXT")
	mustContain(t, ddl, "authentication_method TEXT")
	mustContain(t, ddl, "redirect_to TEXT")
	mustContain(t, ddl, "linking_user_id TEXT")
	mustContain(t, ddl, "auth_code_issued_at TIMESTAMPTZ")
	mustContain(t, ddl, "CREATE INDEX IF NOT EXISTS idx_flow_state_auth_code ON auth.flow_state (auth_code)")
	mustContain(t, ddl, "CREATE INDEX IF NOT EXISTS idx_flow_state_user_id_auth_method ON auth.flow_state (user_id, authentication_method)")

	// And the old tables must NOT be emitted.
	mustNotContain(t, ddl, "_oauth_states")
	mustNotContain(t, ddl, "_auth_codes")
}
```

If `mustNotContain` doesn't exist alongside `mustContain` in this test file, add it:

```go
func mustNotContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Fatalf("expected DDL to NOT contain %q, but it did:\n%s", needle, haystack)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```sh
go test -run TestGenerateAuthFlowState -race ./internal/app/...
```
Expected: FAIL.

- [ ] **Step 3: Implement `auth.flow_state`**

In `generateAuthTables` (the function you wrote in Task 6), append before the final `return ddl`:

```go
	// flow_state consolidates PKCE auth codes and OAuth state into one
	// Supabase-shaped table. provider_type distinguishes the two flows
	// ('pkce' vs. 'oauth'); auth_code holds the PKCE code or the OAuth
	// state token.
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
	ddl = append(ddl, `CREATE INDEX IF NOT EXISTS idx_flow_state_auth_code ON auth.flow_state (auth_code);`)
	ddl = append(ddl, `CREATE INDEX IF NOT EXISTS idx_flow_state_user_id_auth_method ON auth.flow_state (user_id, authentication_method);`)
```

- [ ] **Step 4: Run the new test**

```sh
go test -run TestGenerateAuthFlowState -race ./internal/app/...
```
Expected: PASS.

- [ ] **Step 5: Re-enable the assertions you parked in Task 6 Step 6**

Search for `// TODO Task 7` in `migrate_test.go` and `migrate_config_diff_test.go` and replace the old `_oauth_states` / `_auth_codes` expectations with `auth.flow_state` checks (or, where the test was solely about the existence of those tables, delete the now-meaningless assertion).

- [ ] **Step 6: Run all unit tests**

```sh
go test -race ./internal/app/...
```
Expected: PASS.

- [ ] **Step 7: Commit**

```sh
git add internal/app/migrate.go internal/app/migrate_test.go internal/app/migrate_config_diff_test.go
git commit -m "Consolidate PKCE and OAuth state into auth.flow_state"
```

---

## Task 8: `orderedSchemas` includes `auth` and `storage` automatically

**Files:**
- Modify: `internal/app/migrate.go:339-349` (`orderedSchemas`)
- Test: `internal/app/migrate_test.go` (new test)

- [ ] **Step 1: Write the failing test**

```go
func TestOrderedSchemasIncludesAuthAndStorage(t *testing.T) {
	cfg := &domain.Config{
		Auth:    &domain.Auth{},
		Storage: map[string]domain.Bucket{"avatars": {}},
		Tables: map[string]domain.Table{
			"posts": {Schema: "analytics"},
		},
	}
	got := orderedSchemas(cfg)
	want := []string{"public", "auth", "storage", "analytics"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("orderedSchemas: got %v, want %v", got, want)
	}
}

func TestOrderedSchemasOmitsAuthWhenUnconfigured(t *testing.T) {
	cfg := &domain.Config{}
	got := orderedSchemas(cfg)
	if len(got) != 1 || got[0] != "public" {
		t.Errorf("orderedSchemas with no auth/storage: got %v, want [public]", got)
	}
}
```

(Add `"reflect"` to the test imports if needed.)

- [ ] **Step 2: Run test to verify it fails**

```sh
go test -run TestOrderedSchemas -race ./internal/app/...
```
Expected: FAIL.

- [ ] **Step 3: Update `orderedSchemas`**

Replace `internal/app/migrate.go:339-349`:

```go
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
```

- [ ] **Step 4: Run tests**

```sh
go test -run TestOrderedSchemas -race ./internal/app/...
```
Expected: PASS.

```sh
go test -race ./internal/app/...
```
Expected: PASS.

- [ ] **Step 5: Commit**

```sh
git add internal/app/migrate.go internal/app/migrate_test.go
git commit -m "orderedSchemas covers auth and storage when configured"
```

---

## Task 9: Integration test — auth tables exist in the auth schema

**Files:**
- Modify: `internal/app/migrate_integration_test.go` — make the test helpers (`tableExists`, `columnExists`, `policyExists`, `indexExists`) schema-aware; update assertions.

- [ ] **Step 1: Find the helpers**

```sh
grep -n "func tableExists\|func columnExists\|func policyExists\|func indexExists" internal/app/migrate_integration_test.go
```

- [ ] **Step 2: Make the helpers accept "schema.table" or bare table**

Replace `tableExists` (and analogously `columnExists`, `policyExists`, `indexExists`) with a version that splits the input:

```go
// tableExists accepts either a bare table name (defaults to public) or a
// "schema.table" form.
func tableExists(t *testing.T, db domain.Database, name string) bool {
	t.Helper()
	schema := "public"
	tbl := name
	if i := strings.Index(name, "."); i >= 0 {
		schema = name[:i]
		tbl = name[i+1:]
	}
	rows, err := db.Query(context.Background(),
		`SELECT 1 FROM pg_tables WHERE schemaname = $1 AND tablename = $2`,
		schema, tbl)
	if err != nil {
		t.Fatalf("tableExists query: %v", err)
	}
	return len(rows) > 0
}
```

Apply the same pattern to:
- `columnExists`: `SELECT 1 FROM information_schema.columns WHERE table_schema = $1 AND table_name = $2 AND column_name = $3`
- `indexExists`: `SELECT 1 FROM pg_indexes WHERE schemaname = $1 AND tablename = $2 AND indexname = $3`
- `policyExists`: `SELECT 1 FROM pg_policies WHERE schemaname = $1 AND tablename = $2 AND policyname = $3`

(If the existing helper signatures take the table name as a single string and a separate column/index/policy name, keep the second arg as-is; only the table-name argument changes.)

- [ ] **Step 3: Update existing assertions to use schema-qualified names**

Search for the affected calls:

```sh
grep -nE 'tableExists\(.*"users"|tableExists\(.*"_user_identities"|tableExists\(.*"_objects"|columnExists\(.*"users"' internal/app/migrate_integration_test.go
```

Update each:
- `tableExists(t, db, "users")` → `tableExists(t, db, "auth.users")`
- `tableExists(t, db, "_user_identities")` → `tableExists(t, db, "auth.identities")`
- `tableExists(t, db, "_objects")` → `tableExists(t, db, "storage.objects")` (Task 11 covers the storage move; you can stub these for now and finish in Task 11)
- All `columnExists(t, db, "users", …)` → `columnExists(t, db, "auth.users", …)` etc.

- [ ] **Step 4: Run the integration tests**

```sh
go test -tags=integration -race -run TestMigrate ./internal/app/...
```
Expected: PASS for the auth-schema assertions. Storage assertions still red until Task 11.

- [ ] **Step 5: Commit**

```sh
git add internal/app/migrate_integration_test.go
git commit -m "Schema-aware integration helpers; assert auth.* tables"
```

---

# Phase 3 — Migrator: storage schema

## Task 10: `generateStorageTables` emits `storage.objects`

**Files:**
- Modify: `internal/app/migrate.go:669-702` (`generateStorageTable` / equivalent)
- Modify: callers
- Test: `internal/app/migrate_test.go`

- [ ] **Step 1: Find the existing storage emitter**

```sh
grep -n "_objects\b" internal/app/migrate.go
```
Locate the function that emits `_objects`. If it's an inline block rather than a function, refactor it into `generateStorageTables(cfg *domain.Config) []string`.

- [ ] **Step 2: Write the failing test**

```go
func TestGenerateStorageTablesUsesStorageSchema(t *testing.T) {
	cfg := &domain.Config{
		Storage: map[string]domain.Bucket{"avatars": {}},
	}
	ddl := strings.Join(generateStorageTables(cfg), "\n")
	mustContain(t, ddl, "CREATE SCHEMA IF NOT EXISTS storage;")
	mustContain(t, ddl, "CREATE TABLE IF NOT EXISTS storage.objects")
	mustNotContain(t, ddl, "CREATE TABLE IF NOT EXISTS _objects")
}
```

- [ ] **Step 3: Run test to verify it fails**

```sh
go test -run TestGenerateStorageTables -race ./internal/app/...
```
Expected: FAIL.

- [ ] **Step 4: Rewrite the emitter**

Replace the existing storage emission (currently emitting `_objects`) with:

```go
func generateStorageTables(cfg *domain.Config) []string {
	if len(cfg.Storage) == 0 {
		return nil
	}
	var ddl []string
	ddl = append(ddl, `CREATE SCHEMA IF NOT EXISTS storage;`)
	ddl = append(ddl, `CREATE TABLE IF NOT EXISTS storage.objects (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  bucket_id TEXT NOT NULL,
  name TEXT NOT NULL,
  size BIGINT NOT NULL,
  mime TEXT,
  uploaded_by UUID,
  uploaded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb
);`)
	// Existing _objects had per-bucket-policies elsewhere — those move in Task 12.
	return ddl
}
```

(If the existing `_objects` schema has columns this snippet doesn't list, copy them over — the goal is the schema move + rename, not a column redesign.)

Update the caller in `migrate.go`'s `Plan` (or `planFromScratch` / `planUpdate` in `migrate_config_diff.go`) to call `generateStorageTables(cfg)` and pass the result through.

- [ ] **Step 5: Run tests**

```sh
go test -race ./internal/app/...
```
Expected: storage-emit tests pass. Per-bucket policy tests likely fail because they still expect `_objects` — Task 12 fixes those.

- [ ] **Step 6: Commit**

```sh
git add internal/app/migrate.go internal/app/migrate_test.go
git commit -m "Emit storage.objects in storage schema"
```

---

## Task 11: Per-bucket policies target `storage.objects`

**Files:**
- Modify: `internal/app/migrate_config_diff.go:155-170, 380-392` and any other site that constructs DROP / CREATE POLICY ... ON _objects
- Modify: `internal/app/migrate_config_diff_test.go` — assertions

- [ ] **Step 1: Find every literal `_objects`**

```sh
grep -nE '_objects\b' internal/app/migrate_config_diff.go internal/app/migrate.go
```

- [ ] **Step 2: Update each literal**

Wherever you see `_objects` as a Go string literal, replace it with `storage.objects`. There are roughly six sites in `migrate_config_diff.go` (DROP POLICY emission, CREATE POLICY emission, change-detection comparisons).

- [ ] **Step 3: Update test expectations**

```sh
grep -nE 'storage\.objects|_objects' internal/app/migrate_config_diff_test.go
```

For each `_objects` expectation, change it to `storage.objects`.

- [ ] **Step 4: Run tests**

```sh
go test -race ./internal/app/...
```
Expected: PASS.

- [ ] **Step 5: Commit**

```sh
git add internal/app/migrate_config_diff.go internal/app/migrate_config_diff_test.go internal/app/migrate.go
git commit -m "Per-bucket storage policies target storage.objects"
```

---

## Task 12: Integration test for storage schema

**Files:**
- Modify: `internal/app/migrate_integration_test.go` — finish the storage assertions stubbed in Task 9

- [ ] **Step 1: Convert the stubbed storage assertions**

Where Task 9 left `tableExists(t, db, "storage.objects")` failing, run the integration test:

```sh
go test -tags=integration -race -run TestMigrateStorage ./internal/app/...
```
Expected: PASS now that Task 10 is in.

If any policy assertions reference `_objects` (e.g. `policyExists(t, db, "_objects", "avatars_public_select")`), update them to use the schema-qualified form.

- [ ] **Step 2: Commit**

```sh
git add internal/app/migrate_integration_test.go
git commit -m "Integration assertions for storage schema move"
```

---

# Phase 4 — Engine + data seeding adjustments

## Task 13: `validateDataColumns` recognises `auth.users` instead of `users`

**Files:**
- Modify: `internal/app/engine.go:492-522`
- Modify: `internal/app/engine_test.go` — adjust seed-validation tests

- [ ] **Step 1: Write the failing test**

In `internal/app/engine_test.go`:

```go
func TestValidateDataColumnsAuthUsersAllowsKnownCols(t *testing.T) {
	e := &Engine{cfg: &domain.Config{}}
	err := e.validateDataColumns("auth.users", []map[string]any{
		{"email": "a@b.com", "password": "x", "raw_user_meta_data": map[string]any{}},
	})
	if err != nil {
		t.Fatalf("expected validation to pass for known auth.users cols, got: %v", err)
	}
}

func TestValidateDataColumnsAuthUsersRejectsUnknown(t *testing.T) {
	e := &Engine{cfg: &domain.Config{}}
	err := e.validateDataColumns("auth.users", []map[string]any{
		{"display_name": "alice"},
	})
	if err == nil {
		t.Fatal("expected validation to reject unknown column on auth.users")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```sh
go test -run TestValidateDataColumnsAuthUsers -race ./internal/app/...
```
Expected: FAIL (the special-case in the validator still keys off `"users"`, not `"auth.users"`).

- [ ] **Step 3: Rewrite the function**

Replace `internal/app/engine.go:492-522`:

```go
func (e *Engine) validateDataColumns(tableName string, records []map[string]any) error {
	if tableName == "auth.users" {
		// Known auth.users columns. "password" is allowed (gets bcrypted to
		// password_hash by applyDataRow); custom profile fields belong in a
		// separate user-defined table FK'd to auth.users.id, not here.
		known := map[string]bool{
			"id": true, "email": true, "password": true, "password_hash": true,
			"email_verified": true, "email_confirmed_at": true,
			"last_sign_in_at": true, "raw_app_meta_data": true,
			"raw_user_meta_data": true, "is_anonymous": true,
			"created_at": true, "updated_at": true,
		}
		for _, rec := range records {
			for col := range rec {
				if !known[col] {
					return fmt.Errorf("unknown column %q in auth.users data", col)
				}
			}
		}
		return nil
	}

	table, ok := e.cfg.Tables[tableName]
	if !ok {
		return fmt.Errorf("unknown table %q", tableName)
	}
	fieldMap := table.FieldMap()
	for _, rec := range records {
		for col := range rec {
			if _, exists := fieldMap[col]; !exists {
				return fmt.Errorf("unknown column %q in %q", col, tableName)
			}
		}
	}
	return nil
}
```

- [ ] **Step 4: Run tests**

```sh
go test -race ./internal/app/...
```
Expected: PASS for the new tests. Old tests that used data key `"users"` will fail — Task 14 covers the seed-order rename.

- [ ] **Step 5: Commit**

```sh
git add internal/app/engine.go internal/app/engine_test.go
git commit -m "Validate auth.users data seed columns by hardcoded list"
```

---

## Task 14: `applyDataRow` and `orderDataTables` use `auth.users`

**Files:**
- Modify: `internal/app/engine.go:473-490` (`applyDataRow`)
- Modify: `internal/app/engine.go:620-634` (`orderDataTables`)
- Modify: `internal/app/engine_test.go` — seed-order test

- [ ] **Step 1: Update `applyDataRow`**

Replace the `if tableName == "users"` branch at `internal/app/engine.go:474`:

```go
	if tableName == "auth.users" {
		if pwd, ok := row["password"]; ok {
			if pwdStr, ok := pwd.(string); ok {
				hash, err := bcrypt.GenerateFromPassword([]byte(pwdStr), bcrypt.DefaultCost)
				if err != nil {
					return fmt.Errorf("data %s: hash password: %w", compositeKey, err)
				}
				row["password_hash"] = string(hash)
				delete(row, "password")
			}
		}
	}
```

- [ ] **Step 2: Update `orderDataTables`**

Replace `internal/app/engine.go:620-634`:

```go
// orderDataTables returns data table names in a safe insertion order.
// "auth.users" always comes first, then user tables ordered by FK deps.
func orderDataTables(cfg *domain.Config) []string {
	var result []string
	if _, ok := cfg.Data["auth.users"]; ok {
		result = append(result, "auth.users")
	}
	ordered := orderTables(cfg.Tables)
	for _, name := range ordered {
		if _, ok := cfg.Data[name]; ok {
			result = append(result, name)
		}
	}
	return result
}
```

- [ ] **Step 3: Update the seed-order test**

In `internal/app/engine_test.go` find the test asserting `"users"` first (around `engine_test.go:34-44`) and change the data key to `"auth.users"`:

```go
"auth.users": {CSVFiles: map[string]string{"demo": "./seeds/users.csv"}},
```

And the assertion:

```go
if order[0] != "auth.users" {
	t.Fatalf("expected auth.users first, got %v", order)
}
```

- [ ] **Step 4: Run tests**

```sh
go test -race ./internal/app/...
```
Expected: PASS.

- [ ] **Step 5: Commit**

```sh
git add internal/app/engine.go internal/app/engine_test.go
git commit -m "Seed auth.users via data key auth.users"
```

---

# Phase 5 — Handler SQL rewrites (auth)

## Task 15: `auth_handler.go` — users / refresh_tokens / one_time_tokens rewrite

**Files:**
- Modify: `internal/adapter/http/auth_handler.go` — every direct SQL string
- Modify: `internal/adapter/http/auth_handler_test.go` — adjust mocks where they assert the SQL string

- [ ] **Step 1: Build the rewrite list**

```sh
grep -nE 'FROM users\b|UPDATE users\b|INSERT INTO users\b|DELETE FROM users\b|FROM _refresh_tokens|UPDATE _refresh_tokens|INSERT INTO _refresh_tokens|DELETE FROM _refresh_tokens|FROM _auth_email_verifications|UPDATE _auth_email_verifications|INSERT INTO _auth_email_verifications|DELETE FROM _auth_email_verifications' internal/adapter/http/auth_handler.go
```

You should see roughly 25 sites. Capture the line numbers; you'll be doing a careful find-and-replace across them.

- [ ] **Step 2: Apply the renames**

In a single editor pass, apply (in this order, to avoid double-rewrites):

1. `_auth_email_verifications` → `auth.one_time_tokens`
2. `_refresh_tokens` → `auth.refresh_tokens`
3. Bare `users` (only inside SQL strings) → `auth.users`. Be careful: do NOT rewrite Go identifiers, comments, log messages, JSON keys, or `userSelectCols`. Use grep to limit your replacements to lines containing SQL keywords (`FROM`, `INSERT INTO`, `UPDATE`, `DELETE FROM`, `JOIN`, `REFERENCES`).

- [ ] **Step 3: Build to confirm compile**

```sh
go build ./...
```

- [ ] **Step 4: Run the unit tests**

```sh
go test -race ./internal/adapter/http/...
```

Some tests likely assert specific SQL strings (e.g. `mock.ExpectQuery("FROM users")`); update each to match the new strings. Re-run.

- [ ] **Step 5: Commit**

```sh
git add internal/adapter/http/auth_handler.go internal/adapter/http/auth_handler_test.go
git commit -m "auth_handler: SQL targets auth.users, auth.refresh_tokens, auth.one_time_tokens"
```

---

## Task 16: `auth_handler.go` — PKCE flow uses `auth.flow_state`

**Files:**
- Modify: `internal/adapter/http/auth_handler.go` — the PKCE grant handler around `auth_handler.go:407` and any helper that inserts into `_auth_codes`
- Modify: `internal/adapter/http/auth_handler_test.go`

- [ ] **Step 1: Find every `_auth_codes` reference**

```sh
grep -n "_auth_codes" internal/adapter/http/auth_handler.go internal/adapter/http/auth_handler_test.go
```

- [ ] **Step 2: Rewrite each**

The schema map:
- `_auth_codes (code TEXT PRIMARY KEY, user_id, code_challenge, code_challenge_method, created_at, expires_at)` →
- `auth.flow_state (id UUID, user_id UUID, auth_code TEXT, code_challenge, code_challenge_method, provider_type='pkce', authentication_method, auth_code_issued_at, …)`

Statements to update:

INSERT:
```go
"INSERT INTO _auth_codes (code, user_id, code_challenge, code_challenge_method, expires_at) VALUES ($1, $2, $3, $4, $5)"
```
becomes:
```go
"INSERT INTO auth.flow_state (auth_code, user_id, code_challenge, code_challenge_method, provider_type, authentication_method, auth_code_issued_at) VALUES ($1, $2::uuid, $3, $4, 'pkce', 'pkce', NOW())"
```
(adjust the parameter list to match — drop `expires_at`; expiry is derived from `auth_code_issued_at` plus a constant TTL constant defined below.)

LOOKUP:
```go
"SELECT user_id, code_challenge, code_challenge_method FROM _auth_codes WHERE code = $1 AND expires_at > NOW()"
```
becomes:
```go
"SELECT user_id::text, code_challenge, code_challenge_method FROM auth.flow_state WHERE auth_code = $1 AND provider_type = 'pkce' AND auth_code_issued_at > NOW() - INTERVAL '10 minutes'"
```

DELETE:
```go
"DELETE FROM _auth_codes WHERE code = $1"
```
becomes:
```go
"DELETE FROM auth.flow_state WHERE auth_code = $1 AND provider_type = 'pkce'"
```

- [ ] **Step 3: Run tests**

```sh
go test -race ./internal/adapter/http/...
```

- [ ] **Step 4: Commit**

```sh
git add internal/adapter/http/auth_handler.go internal/adapter/http/auth_handler_test.go
git commit -m "PKCE flow uses auth.flow_state with provider_type='pkce'"
```

---

## Task 17: `auth_handler.go` — OAuth flow uses `auth.flow_state`

**Files:**
- Modify: `internal/adapter/http/auth_handler.go` — OAuth state init/callback (around `:1331` and `:1383`) and any helper that touches `_oauth_states`
- Modify: `internal/adapter/http/auth_handler_test.go`

- [ ] **Step 1: Find every `_oauth_states` reference**

```sh
grep -n "_oauth_states" internal/adapter/http/auth_handler.go internal/adapter/http/auth_handler_test.go
```

- [ ] **Step 2: Rewrite each**

Schema map:
- `_oauth_states (state TEXT PRIMARY KEY, code_challenge, code_challenge_method, redirect_to, provider, linking_user_id, created_at, expires_at)` →
- `auth.flow_state (id UUID, auth_code TEXT, code_challenge, code_challenge_method, provider_type='oauth', redirect_to, linking_user_id, ...)`.

The old `provider` column **is dropped**. `handleOAuthCallback` already takes the provider as a curried argument (`auth_handler.go:1383: func (h *AuthHandler) handleOAuthCallback(provider string) gin.HandlerFunc`) — the route mount already knows which provider is handling the callback. Storing it in `_oauth_states` was redundant.

INSERT (initiate OAuth):
```go
"INSERT INTO _oauth_states (state, code_challenge, code_challenge_method, redirect_to, provider, linking_user_id, expires_at) VALUES ($1, $2, $3, $4, $5, $6, $7)"
```
becomes:
```go
"INSERT INTO auth.flow_state (auth_code, code_challenge, code_challenge_method, redirect_to, provider_type, authentication_method, linking_user_id, auth_code_issued_at) VALUES ($1, $2, $3, $4, 'oauth', 'oauth', $5, NOW())"
```

Drop the `provider` parameter from the Go-side INSERT call. `linking_user_id` shifts from `$6` to `$5`.

LOOKUP (callback):
```go
"SELECT code_challenge, code_challenge_method, redirect_to, provider, linking_user_id FROM _oauth_states WHERE state = $1 AND expires_at > NOW()"
```
becomes:
```go
"SELECT code_challenge, code_challenge_method, redirect_to, linking_user_id FROM auth.flow_state WHERE auth_code = $1 AND provider_type = 'oauth' AND auth_code_issued_at > NOW() - INTERVAL '10 minutes'"
```

The Go scan no longer reads `provider`. Wherever the callback used the queried `provider` value, replace it with the curried `provider` argument that `handleOAuthCallback` already has in scope.

DELETE:
```go
"DELETE FROM _oauth_states WHERE state = $1"
```
becomes:
```go
"DELETE FROM auth.flow_state WHERE auth_code = $1 AND provider_type = 'oauth'"
```

- [ ] **Step 3: Run the OAuth integration test paths**

```sh
go test -tags=integration -race -run TestOAuth ./internal/adapter/http/...
```

If your repo doesn't have a dedicated OAuth integration test, the supabase-js compat suite covers it indirectly; run that in Task 23.

- [ ] **Step 4: Commit**

```sh
git add internal/adapter/http/auth_handler.go internal/adapter/http/auth_handler_test.go
git commit -m "OAuth flow uses auth.flow_state with provider_type='oauth'"
```

---

## Task 18: `mfa_handler.go` SQL rewrites

**Files:**
- Modify: `internal/adapter/http/mfa_handler.go` — every SQL string referencing `_mfa_factors`, `_mfa_challenges`, or `users`
- Modify: `internal/adapter/http/mfa_handler_test.go`

- [ ] **Step 1: Find references**

```sh
grep -nE '_mfa_factors|_mfa_challenges|FROM users\b|UPDATE users\b' internal/adapter/http/mfa_handler.go
```

You should see roughly seven sites.

- [ ] **Step 2: Apply the renames**

- `_mfa_factors` → `auth.mfa_factors`
- `_mfa_challenges` → `auth.mfa_challenges`
- bare `users` inside SQL strings → `auth.users`

- [ ] **Step 3: Run tests**

```sh
go test -race ./internal/adapter/http/...
```

- [ ] **Step 4: Commit**

```sh
git add internal/adapter/http/mfa_handler.go internal/adapter/http/mfa_handler_test.go
git commit -m "mfa_handler: SQL targets auth.mfa_factors, auth.mfa_challenges, auth.users"
```

---

# Phase 6 — Handler SQL rewrites (storage)

## Task 19: storage handlers target `storage.objects`

**Files:**
- Modify: `internal/adapter/http/storage_v1_handler.go` and `internal/adapter/http/storage_handler.go`
- Modify: any storage tests (`storage_v1_handler_test.go` if it exists)

- [ ] **Step 1: Find references**

```sh
grep -nE '_objects\b' internal/adapter/http/storage_v1_handler.go internal/adapter/http/storage_handler.go
```

- [ ] **Step 2: Replace each `_objects` with `storage.objects`**

In SQL strings only — leave Go identifiers and comments alone.

- [ ] **Step 3: Run tests**

```sh
go test -race ./internal/adapter/http/...
go test -tags=integration -race -run TestStorage ./internal/adapter/http/...
```

- [ ] **Step 4: Commit**

```sh
git add internal/adapter/http/storage_v1_handler.go internal/adapter/http/storage_handler.go
git commit -m "Storage handlers target storage.objects"
```

---

# Phase 7 — Examples and docs

## Task 20: Root `ultrabase.yaml`

**Files:**
- Modify: `ultrabase.yaml`

- [ ] **Step 1: Apply the YAML diff**

In `ultrabase.yaml`, find the `tables.users` block (around line 50):

```yaml
tables:
    users:
        fields:
            - name: avatar_url
              type: text
            - name: display_name
              type: text
```

Delete it entirely. The auth user record is no longer configurable from `tables.users`.

In its place, add a commented `profiles` example so the new pattern is discoverable:

```yaml
tables:
    # Profile data lives in a regular user-defined table FK'd to auth.users.id.
    # Uncomment and adjust to suit your app.
    #
    # profiles:
    #     fields:
    #         - name: id
    #           foreign_key:
    #               references: auth.users.id
    #               on_delete: cascade
    #           primary_key: true
    #         - name: avatar_url
    #           type: text
    #         - name: display_name
    #           type: text
    #     rls:
    #         - operations: [select]
    #           check: "true"
    #         - operations: [update]
    #           check: "auth.uid() = id"
```

- [ ] **Step 2: Validate the file parses**

```sh
go build -o ultrabase ./cmd/ultrabase
./ultrabase validate
```
Expected: no errors.

- [ ] **Step 3: Commit**

```sh
git add ultrabase.yaml
git commit -m "Drop tables.users from root config; document profiles pattern"
```

---

## Task 21: react-catalog example

**Files:**
- Modify: `docs/examples/react-catalog/ultrabase.yaml`
- Modify: `docs/examples/react-catalog/README.md`

- [ ] **Step 1: Replace `tables.users` with a `profiles` table**

In `docs/examples/react-catalog/ultrabase.yaml` find:

```yaml
tables:
    users:
        fields:
            - name: display_name
              type: text
              required: false
```

Replace with:

```yaml
tables:
    profiles:
        fields:
            - name: id
              foreign_key:
                  references: auth.users.id
                  on_delete: cascade
              primary_key: true
            - name: display_name
              type: text
              required: false
        rls:
            - operations: [select]
              check: "true"
            - operations: [insert, update]
              check: "auth.uid() = id"
```

- [ ] **Step 2: Update FK references**

```sh
grep -n "references: users.id" docs/examples/react-catalog/ultrabase.yaml
```

For each match (the README mentions `reviews` at `:159`), change `users.id` → `auth.users.id`.

- [ ] **Step 3: Update README**

In `docs/examples/react-catalog/README.md`, the line:

> **reviews** — has-many on `products`, FK to `users.id` via `user_id`.

becomes:

> **reviews** — has-many on `products`, FK to `auth.users.id` via `user_id`.

Plus a new short paragraph near the top (after the table list) explaining that the project's user identity lives in `auth.users` (managed by ultrabase) and `profiles` is a separate user-defined table FK'd to `auth.users.id`.

- [ ] **Step 4: Validate**

```sh
./ultrabase validate --config docs/examples/react-catalog/ultrabase.yaml
```

- [ ] **Step 5: Commit**

```sh
git add docs/examples/react-catalog/ultrabase.yaml docs/examples/react-catalog/README.md
git commit -m "react-catalog example uses profiles + auth.users.id"
```

---

## Task 22: CLAUDE.md updates

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Update the "Reserved names" gotcha**

Find the bullet under `<gotchas>`:

> - **Reserved names:** `users` (auth users) and any identifier starting with `_` (framework tables: `_objects`, `_events`, `_ultrabase_migrations`, `_user_identities`, …).

Replace with:

> - **Reserved schemas:** `auth` and `storage`. The migrator owns these schemas; user tables declaring `schema: auth` or `schema: storage` are rejected at validation. The auth user record lives at `auth.users`; profile data is a regular user-defined table FK'd to `auth.users.id`.
> - **Underscore-prefixed names** are still reserved for framework-internal tables (`_events`, `_ultrabase_migrations`).

- [ ] **Step 2: Update the auth.uid() bullet**

The line:

> - **`auth.uid()` and `auth.is_authenticated()`** are Postgres functions installed by migrations, used inside RLS policy expressions. They read session GUCs set by the request middleware, not application memory.

Append:

> They live in the `auth` schema and are typically referenced from RLS policies on tables that FK to `auth.users.id`.

- [ ] **Step 3: Commit**

```sh
git add CLAUDE.md
git commit -m "CLAUDE.md: reserved schemas, auth.users gotcha"
```

---

## Task 23: Dashboard auth-page copy

**Files:**
- Modify: `dashboard/src/pages/Auth.tsx:86`

- [ ] **Step 1: Update the description**

Find:

```tsx
description="Configure auth providers, JWT settings, and custom user fields"
```

Replace with:

```tsx
description="Configure auth providers and JWT settings"
```

If the page renders any UI controlling extra fields on `tables.users`, remove that section. Otherwise this is a one-line change.

- [ ] **Step 2: Run dashboard tests**

```sh
cd dashboard && npm test
```

- [ ] **Step 3: Commit**

```sh
git add dashboard/src/pages/Auth.tsx
git commit -m "Dashboard: drop 'custom user fields' from auth page copy"
```

---

# Phase 8 — Integration verification

## Task 24: supabase-js compat suite — table-name updates and cross-schema profiles test

**Files:**
- Modify: `test/integration/supabase-js/run.mjs` — any direct SQL or REST call referencing `users` or `_objects`
- Modify: `internal/adapter/http/supabase_integration_test.go` — setup SQL for the harness

- [ ] **Step 1: Find references in the harness**

```sh
grep -nE 'from\(.users.|from\([\'"]_objects|FROM users|FROM _objects' test/integration/supabase-js/run.mjs internal/adapter/http/supabase_integration_test.go
```

PostgREST's URL-derived table name does NOT have to change for `supabase.from('users')`-style calls if the user has a `public.users` table; but if the harness intends to query the auth user record directly it needs the schema-qualified PostgREST form (which most clients expose as `from('users', { schema: 'auth' })` — confirm against the version of `@supabase/supabase-js` pinned in `test/integration/supabase-js/package.json`).

For setup-side raw SQL (in `supabase_integration_test.go`), apply the renames:
- `INSERT INTO users` → `INSERT INTO auth.users`
- `DELETE FROM _objects` → `DELETE FROM storage.objects`
- etc.

- [ ] **Step 2: Add a cross-schema profiles assertion**

In `run.mjs`, add a new check after the existing happy-path tests:

```js
// Cross-schema FK: a profiles table FKs to auth.users.id and uses RLS
// referencing auth.uid().
{
  // Sign in as a user (the harness already creates one via signUp).
  const { data: signin } = await supa.auth.signInWithPassword({
    email: 'alice@example.com',
    password: 'password123',
  });
  const userId = signin.user.id;

  const { data: ins, error: insErr } = await supa
    .from('profiles')
    .insert({ id: userId, display_name: 'Alice' })
    .select()
    .single();
  assert(!insErr, `profiles insert failed: ${insErr?.message}`);
  assert(ins.id === userId, 'profiles.id should equal auth.users.id');

  // Read back through RLS.
  const { data: read } = await supa.from('profiles').select('*').eq('id', userId);
  assert(read.length === 1, 'profiles select should return one row');
}
```

The matching `profiles` table must be in the test config (`internal/adapter/http/supabase_integration_test.go` constructs the YAML inline; add the table there).

- [ ] **Step 3: Run the compat suite**

```sh
go test -run TestSupabaseJSCompat -tags=integration -race ./internal/adapter/http/...
```
Expected: PASS.

- [ ] **Step 4: Commit**

```sh
git add test/integration/supabase-js/run.mjs internal/adapter/http/supabase_integration_test.go
git commit -m "supabase-js compat: schema-qualified setup + cross-schema profiles test"
```

---

## Task 25: Schema grants integration test covers auth and storage

**Files:**
- Modify: `internal/app/schema_grants_integration_test.go`

- [ ] **Step 1: Extend the assertions**

The existing test asserts that `anon`, `authenticated`, and `service_role` get `SELECT/INSERT/UPDATE/DELETE` on tables in the configured schemas. Extend it to assert these privileges on:
- `auth.users` (when `cfg.Auth != nil`)
- `storage.objects` (when `cfg.Storage` is non-empty)

Use `pg_class` + `has_table_privilege` to check:

```go
func TestSchemaGrantsCoverAuthAndStorage(t *testing.T) {
	ctx, db := newOwnerDB(t)
	cfg := &domain.Config{
		Auth:    &domain.Auth{},
		Storage: map[string]domain.Bucket{"avatars": {}},
	}
	require.NoError(t, NewMigrator(db).Apply(ctx, cfg))

	for _, role := range []string{"anon", "authenticated", "service_role"} {
		for _, fqn := range []string{"auth.users", "storage.objects"} {
			var canSelect bool
			err := db.QueryRow(ctx,
				`SELECT has_table_privilege($1, $2, 'SELECT')`,
				role, fqn).Scan(&canSelect)
			require.NoError(t, err)
			require.True(t, canSelect, "%s should have SELECT on %s", role, fqn)
		}
	}
}
```

(Adapt to whatever helper utilities the existing tests use — the structure above assumes testify; the repo may use plain `t.Errorf`.)

- [ ] **Step 2: Run**

```sh
go test -tags=integration -race -run TestSchemaGrants ./internal/app/...
```

- [ ] **Step 3: Commit**

```sh
git add internal/app/schema_grants_integration_test.go
git commit -m "Schema-grants test covers auth and storage schemas"
```

---

## Task 26: Role-separation integration test covers `auth.users` and `storage.objects`

**Files:**
- Modify: `internal/adapter/postgres/role_separation_integration_test.go`

- [ ] **Step 1: Extend the test**

The existing test asserts that the `authenticator` login (NOINHERIT) cannot read tables without `SET LOCAL ROLE`. Add cases for `auth.users` and `storage.objects`:

```go
func TestAuthenticatorWithoutRoleSwitchCannotReadAuthOrStorage(t *testing.T) {
	ctx, db := newAuthenticatorDB(t)
	for _, fqn := range []string{"auth.users", "storage.objects"} {
		_, err := db.Query(ctx, fmt.Sprintf("SELECT 1 FROM %s LIMIT 1", fqn))
		require.Error(t, err, "authenticator without SET LOCAL ROLE should be denied on %s", fqn)
	}
}
```

(Use the existing helper that builds an `authenticator`-only connection without the role-switch pre-amble — this is the regression check for the NOINHERIT load-bearing rule from CLAUDE.md.)

- [ ] **Step 2: Run**

```sh
go test -tags=integration -race -run TestAuthenticator ./internal/adapter/postgres/...
```

- [ ] **Step 3: Commit**

```sh
git add internal/adapter/postgres/role_separation_integration_test.go
git commit -m "Role-separation test covers auth and storage schemas"
```

---

## Task 27: Final feedback loop

**Files:** none (verification only)

- [ ] **Step 1: Build**

```sh
go build ./...
```

- [ ] **Step 2: Unit tests**

```sh
go test -race ./...
```

- [ ] **Step 3: Integration tests**

```sh
go test -tags=integration -race ./...
```

- [ ] **Step 4: Dashboard tests**

```sh
cd dashboard && npm test && cd ..
```

- [ ] **Step 5: supabase-js compat**

```sh
go test -run TestSupabaseJSCompat -tags=integration -race ./internal/adapter/http/...
```

All four must pass. If any fail, fix the root cause before pushing.

- [ ] **Step 6: Push when green**

```sh
git push -u origin feat/auth-storage-schemas
```

(Skip if you're not using a worktree branch.)

---

## Open follow-ups (for the events-removal spec, not this one)

- Drop the events subsystem entirely: `internal/app/events.go`, `internal/adapter/postgres/wal.go`, `_events` table emission, `cfg.On` triggers, `EventDispatcher`, `EventWorker`, slot management. Tracked separately.
- The `auth.flow_state` row TTL is currently inlined as `INTERVAL '10 minutes'` in two SQL strings. If a future spec wants this configurable, expose it as a constant or config field.
