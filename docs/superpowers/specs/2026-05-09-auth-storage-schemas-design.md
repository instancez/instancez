# Auth + storage schemas: matching Supabase layout

Status: design approved (verbal); awaiting written-spec review.
Slice: this spec covers auth + storage schema moves. The events subsystem removal is a separate spec.

## Why

Today every framework-managed table (`users`, `_user_identities`, `_refresh_tokens`, `_auth_email_verifications`, `_auth_jwt_keys`, `_auth_codes`, `_oauth_states`, `_mfa_factors`, `_mfa_challenges`, `_objects`) lives in the default schema, jumbled in with whatever the user puts in `tables:`. The `tables.users` entry doubles as "configure the auth user record" by allowing extra fields that the migrator merges into the auth table. Two consequences:

1. The user has no clean place to attach profile data — the existing escape hatch (extra fields on `tables.users`) couples profile shape to the auth table's lifecycle and means RLS on profile fields piggybacks on the auth row.
2. The wire surface is supabase-js-compatible but the SQL surface diverges from Supabase's. Anyone porting policies, helper functions, or DB-side tooling from Supabase has to rewrite every schema reference.

We're realigning the SQL surface with Supabase's. Profiles become a user-defined table that FKs to `auth.users.id`, the same pattern Supabase users already know.

## Non-goals

- **Events subsystem removal.** Out of scope. Tracked in a separate spec. `_events` and the `on:` triggers stay where they are for this change.
- **`storage.buckets` table.** Bucket configuration stays in YAML. Adding a runtime buckets table is a separate question.
- **Backwards compatibility.** Clean break. Existing deployments must reset or hand-migrate; the migrator does not auto-rename old layouts. Ultrabase is treated as pre-1.0 for this change.
- **Helper functions.** `auth.uid()` / `auth.role()` / `auth.email()` / `auth.jwt()` / `auth.is_authenticated()` are already in the `auth` schema; they don't move.

## Target schema layout

### `auth` schema

| Today (default schema)     | New                       |
| -------------------------- | ------------------------- |
| `users`                    | `auth.users`              |
| `_user_identities`         | `auth.identities`         |
| `_refresh_tokens`          | `auth.refresh_tokens`     |
| `_auth_email_verifications`| `auth.one_time_tokens`    |
| `_auth_jwt_keys`           | `auth.jwt_keys`           |
| `_auth_codes`              | `auth.flow_state` (merged)|
| `_oauth_states`            | `auth.flow_state` (merged)|
| `_mfa_factors`             | `auth.mfa_factors`        |
| `_mfa_challenges`          | `auth.mfa_challenges`     |

### `auth.flow_state` — merged PKCE + OAuth state

Supabase-shaped columns:

```
id                       UUID PRIMARY KEY DEFAULT gen_random_uuid()
user_id                  UUID                       -- linking_user_id for OAuth, sub for PKCE
auth_code                TEXT                       -- PKCE code AND OAuth state token (single column, like Supabase)
code_challenge           TEXT
code_challenge_method    TEXT                       -- 'S256' | 'plain'
provider_type            TEXT NOT NULL              -- 'pkce' | 'oauth'
provider_access_token    TEXT
provider_refresh_token   TEXT
authentication_method    TEXT                       -- 'password' | 'oauth' | 'otp' | …
created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW()
updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW()
auth_code_issued_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
```

Two carry-over fields the existing `_oauth_states` has that Supabase's `flow_state` doesn't represent natively — `redirect_to` and `linking_user_id` — keep as nullable columns:

```
redirect_to              TEXT
linking_user_id          TEXT
```

Indexes:
- `idx_flow_state_auth_code` on `(auth_code)` — primary lookup for both flows.
- `idx_flow_state_user_id_auth_method` on `(user_id, authentication_method)` — for sweep / rotation queries.

**TTL:** today's `_auth_codes` and `_oauth_states` carry an explicit `expires_at`. Supabase's `flow_state` derives expiry from `auth_code_issued_at` plus a server constant. Choose one in the implementation plan; the spec doesn't gate on it. Tests that previously asserted `expires_at` must be updated either way.

### `storage` schema

| Today      | New                |
| ---------- | ------------------ |
| `_objects` | `storage.objects`  |

Buckets remain configured in YAML — no `storage.buckets` table.

### Stays in default schema

- `_events` (events spec will move/remove this)
- `_ultrabase_migrations`

## YAML / config impact

### `tables.users` is no longer reserved

Users may declare a table named `users` in YAML. It creates `public.users` and is treated as any other user-defined table. It is **not** the auth user record — `auth.users` is auto-emitted by the migrator when `auth:` is configured. The two are distinct because they live in different schemas.

### Reserved schemas: `auth` and `storage`

The migrator owns `auth` and `storage` exclusively. Any user table declaring `schema: auth` or `schema: storage` is rejected at validation:

```
table "<name>" declares schema "<auth|storage>", which is reserved by the framework
```

### `Config.UserExtraFields`, `coreUserColumns`, `extraFields` parameter — removed

`auth.users` has a single, fixed shape. There is no merge path from YAML into the auth table.

### FK reference grammar

`references:` accepts:
- `table.column` — defaults to `public.table.column`. Existing 2-part form continues to work.
- `schema.table.column` — new 3-part form. Required to FK across schemas (e.g., `auth.users.id`).

```yaml
profiles:
  fields:
    - name: id
      foreign_key:
        references: auth.users.id
        on_delete: cascade
```

The parser splits on `.`. 1 part is invalid (no column). 2 parts → public. 3 parts → schema-qualified. >3 parts is a hard error.

### FK auto-type rule

A field with `foreign_key.references: auth.users.id` and no `type:` infers `UUID`. The legacy 2-part `users.id` rule is removed.

### `auth:` config block

Unchanged. Same keys (`jwt_expiry`, `refresh_tokens`, `email`, `google`, `github`, `allow_signup`, `allow_anonymous`). It still gates whether `auth.*` tables are emitted at all.

## Code changes

### `internal/app/migrate.go`

- `generateUsersTable(auth, extraFields []domain.Field)` → `generateAuthTables(auth *domain.Auth)`. Drops the `extraFields` param. Emits all auth tables with `auth.` qualification, drops the `_` prefix on each, applies the renames listed above.
- New `generateStorageTables(cfg *domain.Config)` extracted from the current `_objects` emission. Emits `storage.objects` with the same columns and bucket-id semantics.
- `auth.flow_state` consolidation: replaces both `_auth_codes` and `_oauth_states`. Schema as described above.
- `orderedSchemas(cfg)`: include `auth` whenever `cfg.Auth != nil`; include `storage` whenever `cfg.Storage` is non-empty. Existing GRANTs / default privileges then cover those schemas automatically (they iterate `orderedSchemas`).
- FK constraint emitter (`migrate.go:517` area): rewrite to handle 2-part and 3-part dotted refs; reject 1-part and >3-part with a clear error. Schema-qualify the `REFERENCES` clause.
- `effectiveType` (`migrate.go:603` area): UUID inference triggers when target is `auth.users.id`. The 2-part `users.id` rule is removed.

### `internal/app/migrate_config_diff.go`

- Every literal `_objects` becomes `storage.objects`. Three diff sites currently DROP/CREATE per-bucket policies on `_objects`; all are schema-qualified.
- `diffNewStorage` / `diffNewEvents` call sites updated where they enter the new emission paths.

### `generateRLSPolicies` (in `internal/app/migrate.go`)

Today this emits `ALTER TABLE <name> ENABLE ROW LEVEL SECURITY` and `CREATE POLICY … ON <name>` with an unqualified table name. It must use the schema-qualified form so policies on cross-schema tables (`auth.users`, `storage.objects`, plus user tables in non-default schemas) work.

Implementation note: there is already a `qualName` computation inside `generateTable` (`migrate.go:568`). Extract it into a `schemaQualifiedName(name string, table domain.Table) string` helper and reuse from `generateRLSPolicies` and `generateIndexes`.

### `generateIndexes`

Currently emits `CREATE INDEX … ON <name>` (unqualified). Same fix as RLS: use the schema-qualified table name.

### HTTP handlers — direct SQL string rewrites

| File                                              | Tables to rewrite                                                                                              |
| ------------------------------------------------- | -------------------------------------------------------------------------------------------------------------- |
| `internal/adapter/http/auth_handler.go`           | `users` → `auth.users`; `_refresh_tokens` → `auth.refresh_tokens`; `_auth_email_verifications` → `auth.one_time_tokens`; `_auth_codes` + `_oauth_states` → `auth.flow_state` (+ new column names) |
| `internal/adapter/http/mfa_handler.go`            | `_mfa_factors` → `auth.mfa_factors`; `_mfa_challenges` → `auth.mfa_challenges`; `users` → `auth.users`        |
| `internal/adapter/http/storage_v1_handler.go`     | `_objects` → `storage.objects`                                                                                 |
| `internal/adapter/http/storage_handler.go`        | `_objects` → `storage.objects`                                                                                 |

The `auth.flow_state` merge is the most invasive piece. Two flows in `auth_handler.go` use these tables today:
- **PKCE grant** (`handlePKCEGrant`, currently around `auth_handler.go:407`): inserts on signup/exchange, looks up by `code`, deletes on consume.
- **OAuth callback / authorize** (`handleAuthorize`, `handleOAuthCallback`, around `auth_handler.go:1331` and `:1383`): inserts on initiate, looks up by `state`, deletes on callback.

Both flows now write into the same table with `provider_type` distinguishing them.

### Validation (`internal/domain/schema.go` or wherever `Config.Validate` lives)

- Add a check that rejects `Table.Schema == "auth"` or `"storage"`.
- Delete `coreUserColumns` and `Config.UserExtraFields()`.

### WAL publication

The replication slot publishes a specific table list. Confirm during implementation:
- Auth-internal tables (`auth.*`) and `storage.objects` should **not** be in the publication. They aren't user data and shouldn't trigger event-system matches.
- The publication construction needs to see schema-qualified table names.

If today's publication code references unqualified names, this is the spot where it grows a schema awareness.

### Domain types — `Roles`, `Session`, JWT layer, middleware

Untouched. JWT wire role tokens (`anon`, `authenticated`, `service_role`) stay the same. The middleware sets `app.user_id` / `app.role` / `app.email` / `app.jwt` GUCs as before. `SET LOCAL ROLE` still maps via `Roles.AssumableFromSession`. None of this layer cares which schema the auth tables live in.

## supabase-js compatibility

The compat contract is HTTP-layer (`/auth/v1`, `/rest/v1`, `/storage/v1`, error envelope, headers, JWT shape). None of that changes. The supabase-js compat suite (`internal/adapter/http/supabase_integration_test.go`) and `test/integration/supabase-js/run.mjs` should pass without changes.

The one place the integration suite touches the SQL surface is the test's own setup/teardown queries. Audit `run.mjs` and any test helpers that issue raw SQL against `users` / `_objects` and update them to `auth.users` / `storage.objects`.

## Tests

### Unit tests

- `internal/app/migrate_test.go`: assertions against generated DDL — currently expects `CREATE TABLE IF NOT EXISTS users`, `_user_identities`, `_objects`, etc. Update to expect the schema-qualified names. Add a test asserting the dropped underscore prefix and renamed tables.
- `internal/app/migrate_config_diff_test.go`: every `_objects` reference in expectations needs to become `storage.objects`. Currently 6+ sites.
- New: FK parser tests for 2-part / 3-part / invalid forms; auto-type inference for `auth.users.id`.
- New: validation tests for `schema: auth` / `schema: storage` rejection.
- Update `engine_test.go` ordering: today the seed-order test asserts `users` first; with `auth.users` opaque, the test loses meaning — rewrite or remove the special-case.

### Integration tests

- `internal/app/migrate_integration_test.go`: every `tableExists(t, db, "users")` / `_user_identities` / `_objects` etc. → schema-qualified equivalents. The integration helpers (`tableExists`, `columnExists`, `policyExists`) need to accept schema-qualified names or be split.
- `internal/app/schema_grants_integration_test.go`: extend to assert grants on `auth` and `storage` schemas.
- `internal/adapter/postgres/role_separation_integration_test.go`: the role-separation regression test should explicitly cover `auth.users` and `storage.objects` as RLS-managed tables.

### supabase-js compat

- Run the existing suite — should be green.
- Add a new check in `run.mjs`: create a `profiles` table FK'd to `auth.users.id`, insert a profile via the REST surface as the authenticated user, assert RLS works against the cross-schema FK.

## Examples & docs

### `ultrabase.yaml` (root)

- Remove the `tables.users` block (extra fields no longer supported there).
- Add a commented-out example of a `profiles` table with `references: auth.users.id` to demonstrate the new pattern.

### `docs/examples/react-catalog/ultrabase.yaml`

- Same: remove `tables.users.fields` (the existing `display_name` extra field).
- Add `profiles` table with `id` FK'd to `auth.users.id` and the existing `display_name` field.
- Update any `references: users.id` to `references: auth.users.id`. The `reviews` table at line 159 is one such site.
- README needs a one-paragraph note that the project's user model is `auth.users` + a `profiles` table.

### `CLAUDE.md`

- Update the "Reserved names" gotcha: `users` is no longer reserved; the framework owns `schema: auth` and `schema: storage` instead.
- Update the "auth.uid() and auth.is_authenticated()" gotcha to mention that user-data tables (e.g., `profiles`) can FK to `auth.users.id`.

### Dashboard

`dashboard/src/pages/Auth.tsx:86` describes the auth page as "Configure auth providers, JWT settings, and custom user fields". The "custom user fields" capability is going away — update the copy. If the dashboard exposes any UI for editing `tables.users` fields, remove it.

## Rollout

Single PR. Clean break — no version flag, no dual-path code. Anyone running an existing ultrabase database against this build either rebuilds from a fresh DB or hand-migrates. The PR description should call this out and link to the rationale.

## Risks

- **The flow_state merge is the highest-risk piece.** Two distinct flows now share a table; column overlap is partial; getting the indexes right matters for the OAuth-state lookup hot path. The implementation plan should land the merge as its own commit so it's reviewable in isolation.
- **Cross-schema RLS policies that reference functions in another schema** are a known footgun (`search_path` shenanigans). All `auth.*` helper functions are already schema-qualified at the call site (`auth.uid()`, not `uid()`), so this should be a non-issue, but worth a regression test.
- **Grants on the new schemas:** `generateSchemaGrants` and `generateExistingObjectGrants` already iterate `orderedSchemas`, so adding `auth` and `storage` to that list propagates correctly. Verify in an integration test that `anon`/`authenticated`/`service_role` get the expected privileges on `auth.users` and `storage.objects`.
- **WAL publication:** if the publication is built from an unqualified table list, schema-moved tables will silently drop out of replication. This is desirable for auth-internal tables but must be verified, not assumed. Add an integration assertion.

## Subsystems unaffected

For reviewer reassurance — these were considered and don't need changes:

- **`internal/cli/dbsetup.go`** — only opens pools and reads role-name env vars. GRANTs come from the migrator's `generateSchemaGrants`, which iterates `orderedSchemas`; adding `auth` and `storage` to that list flows through automatically.
- **`internal/app/drift.go`** — checksum-based, not schema-introspecting. No schema awareness needed.
- **`Roles`, `Session`, JWT keys, middleware, role-switching** — JWT wire tokens and `SET LOCAL ROLE` flow are independent of which schema tables live in.
- **PostgREST query parsing (`where.go`, `select.go`, `csv.go`)** — operates on table identifiers from the URL, not on the underlying schema. No change.

## Open questions resolved

- Migration policy: clean break, no backwards compat.
- Scope: auth + storage in this spec; events removal separate.
- FK syntax: 3-part schema-qualified.
- Underscore prefixes: dropped in the new schemas.
- `tables.users`: no longer reserved; `auth` and `storage` schemas are reserved instead.
- PKCE + OAuth state: merged into `auth.flow_state`, matching Supabase.
