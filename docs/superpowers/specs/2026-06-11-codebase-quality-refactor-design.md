# Codebase Quality Refactor — Design Spec

**Date:** 2026-06-11  
**Status:** Approved  
**Branch strategy:** Sequential commits directly on `main`; each phase must pass `go test -race ./...` and `npm test` (dashboard/) before the next phase starts.

---

## Motivation

Five improvements identified in a codebase audit:
1. CRUD mutation handler deduplication
2. PostgREST query engine package split
3. `golangci-lint` CI step
4. New tests (storage handler + dashboard detail pages)
5. Auth service extraction into `domain.AuthService`

No backward-compatibility constraints — full refactoring freedom.

---

## Phase ordering (lowest risk → highest risk)

| Phase | Work | Primary files touched |
|---|---|---|
| 1 | CRUD mutation dedup | `internal/adapter/http/crud_handler.go` |
| 2 | PostgREST package split | `internal/adapter/http/{crud_handler,where,select,csv}.go` → new `internal/adapter/http/postgrest/` |
| 3 | golangci-lint CI | `.golangci.yml`, `.github/workflows/ci.yml` |
| 4 | New tests | `internal/adapter/http/storage_v1_handler_test.go`, `dashboard/src/pages/*.test.tsx` |
| 5 | Auth service extraction | `internal/adapter/http/auth_handler.go` → `internal/adapter/auth/`, `internal/domain/auth.go` |

---

## Phase 1 — CRUD mutation handler deduplication

### Problem
`handleCreate`, `handleUpsert`, and `handleUpdate` in `crud_handler.go` share ~150 lines of identical scaffolding:
- Body parsing (CSV vs JSON array vs single object + unknown-field validation)
- RLS context + transaction setup
- `return=representation` / `headers-only` / default response switch

### Solution
Extract three package-level helpers:

**`parseRequestBody(c *gin.Context, table domain.Table) ([]map[string]any, error)`**  
Handles CSV (`Content-Type: text/csv`), JSON array (`[...]`), and single-object JSON. Validates all records against `table.FieldMap()` and returns `problemJSON` errors via `c` on failure (returning `nil, err`). Callers check `err != nil` and return immediately.

**`writeMutationResponse(c *gin.Context, status int, returnMode string, results []map[string]any)`**  
Implements the three-way `return=` response: `representation` (object or array based on `Accept` header), `headers-only` (set header + status only), default (status only). Accepts status as parameter so `handleCreate` passes 201 and `handleUpsert`/`handleUpdate` pass 200.

**`setupMutationTx(c *gin.Context, db domain.Database, session domain.Session) (context.Context, domain.Tx, func(), error)`**  
Calls `db.WithRLS`, then `db.Begin`. Returns the context, tx, a `rollback` cleanup func (caller defers it), and any error. On error, writes `problemJSON` and returns `nil, nil, nil, err`.

### Correctness guard
No test changes. Existing `upsert_test.go`, `bulk_test.go`, `on_conflict_test.go`, `columns_hint_test.go`, `validation_test.go`, and `query_test.go` are the regression net.

---

## Phase 2 — PostgREST query engine subpackage

### Problem
`crud_handler.go` (2,870 lines) mixes HTTP orchestration with a self-contained PostgREST-compatible query engine. `where.go`, `select.go`, and `csv.go` already hint at a desired split but remain in the `http` package.

### Solution
New package: `internal/adapter/http/postgrest`

#### Types to move
`WhereNode`, `Filter`, `OrderClause`, `Embed`, `QueryParams`, `jsonPathStep`, `colValidator`

#### Functions to move (from `crud_handler.go`)
All builder/resolver functions that have no `*gin.Context` parameter:
- `buildSelectQueryFull`, `buildSelectQuery`
- `buildFilterCondition`
- `buildBulkInsertQuery`, `buildBulkUpsertQuery`, `buildInsertQuery`
- `renderOrderBy`, `renderJSONBSuffix`, `lastJSONBOp`
- `resolveEmbeds`, `buildEmbedRowExpr`, `buildChildEmbedSubselect`
- `parseSelectParam`, `parseOrderValue`, `parseOrderValueWithSelect`, `parseOrderValueWith`, `collectSelectAliases`
- `parseEmbedParam`, `parseEmbedScopedParams`
- `parseFilterValue`, `parsePatternList`
- `splitJSONBPath`, `isAllDigits`
- `qualifyOrderColumns`, `aliasWhereColumns`
- `hasBelongsToJoin`
- `recordsAllEmpty`, `unionColumns`, `renderRowTuples`
- `filterRecordsByColumns`, `parseColumnsParam`
- `primaryKeyColumns`
- `validateColumn`, `findUnknownFields`

Move `where.go` and `select.go` entirely into the new package (rename `package http` → `package postgrest`).

**Gin decoupling for parse functions:** Any function in the move list that currently takes `*gin.Context` gets its signature changed to take `url.Values` instead. Callers in `internal/adapter/http` pass `c.Request.URL.Query()`. Specifically:
- `parseWhere(c *gin.Context, ...)` → `parseWhere(vals url.Values, ...)`
- `parseEmbedScopedParams(c *gin.Context, ...)` → `parseEmbedScopedParams(vals url.Values, ...)`

This makes all moved functions testable without constructing a gin context.

**`parseQueryParams` stays in `internal/adapter/http`** — it is the gin-coupled orchestrator that calls into `postgrest.*`. Its signature stays `parseQueryParams(c *gin.Context, ...) (*postgrest.QueryParams, error)`.

#### csv.go
Move into `postgrest` package — no gin dependency.

#### Visibility
All exported from `postgrest`. The `internal/adapter/http` package imports `postgrest` and the existing test files (`query_test.go`, `select_test.go`, `where_test.go`, etc.) remain in package `http` and call the public API unchanged.

---

## Phase 3 — golangci-lint

### `.golangci.yml`
```yaml
linters:
  enable:
    - errcheck
    - staticcheck
    - gosimple
    - unused
    - govet
    - ineffassign
    - typecheck
  disable:
    - wrapcheck
    - exhaustive
    - exhaustruct

linters-settings:
  errcheck:
    check-blank: true

issues:
  exclude-rules:
    # Fire-and-forget DB updates (e.g. last_sign_in_at) — intentional
    - linters: [errcheck]
      text: "h\\.db\\.Exec"
      path: "auth_handler\\.go"
```

The ~20 intentional fire-and-forget `h.db.Exec(...)` calls in `auth_handler.go` are covered by the exclude rule above. Any other `errcheck` failures are fixed (not suppressed).

### CI job
Add to `.github/workflows/ci.yml`:
```yaml
lint:
  name: Lint
  runs-on: ubuntu-latest
  steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with:
        go-version: '1.25'
        cache: true
    - uses: golangci/golangci-lint-action@v6
      with:
        version: latest
```

The `lint` job does not gate the Docker build job (non-blocking on first introduction — can be tightened once all findings are clean).

---

## Phase 4 — New tests

### Go: `internal/adapter/http/storage_v1_handler_test.go`

Use the `stubDB` pattern from `auth_handler_test.go`. Stub `domain.ObjectStore` with a minimal `stubObjectStore` that records calls and returns canned values.

**Coverage targets:**
- `listBuckets` — empty list, non-empty list, DB error → 500
- `getBucket` — found, not found → 404
- `createBucket` — success → 201, duplicate → 400
- `updateBucket` — success → 200
- `deleteBucket` — success → 200, not empty → 400
- `emptyBucket` — success → 200
- `uploadObject` — success → 200, oversized → 413
- `getObject` (authenticated path) — found, not found → 404
- `createSignedUrl` — returns URL with token
- `deleteObject` — success → 200

All tests are unit (no Docker), using `httptest.NewRecorder()` and Gin's test mode.

### Dashboard: five detail pages

Follow the `ConfigContext.Provider` + `MemoryRouter` + `vi.fn()` pattern from `Functions.test.tsx` and `Auth.test.tsx`.

**`TableDetail.test.tsx`**
- Renders column list for a table with fields
- Shows RLS policies section
- Shows empty state when no fields defined
- "Edit" button transitions to edit mode (checks EditModeBanner appears)
- Save/discard triggers `save`/`updateConfig` mocks

**`RpcDetail.test.tsx`**
- Renders function name, argument names and types
- Renders return type
- Handles RPC with no arguments

**`StorageDetail.test.tsx`**
- Renders bucket name and public/private setting
- Renders file size limit field
- Shows allowed MIME types

**`Providers.test.tsx`**
- Email provider section renders with fields when auth.email config present
- Storage provider section renders
- Toggling provider calls `updateConfig`

**`FunctionDetail.test.tsx`**
- Renders function name, runtime, file path
- Shows `auth_required` toggle state
- Shows timeout field

---

## Phase 5 — Auth service extraction

### New domain port: `internal/domain/auth.go`

```go
package domain

import "context"

type CreateUserParams struct {
    Email          string
    Phone          string
    Password       string // pre-hashed bcrypt; empty = no password
    AppMetadata    map[string]any
    UserMetadata   map[string]any
    EmailConfirmed bool
    PhoneConfirmed bool
}

type UpdateUserParams struct {
    Email         *string
    Phone         *string
    Password      *string // pre-hashed; nil = no change
    AppMetadata   map[string]any
    UserMetadata  map[string]any
    EmailConfirmed *bool
    Banned        *bool
}

type AuthToken struct {
    Token     string
    SessionID string
    UserID    string
    ExpiresAt int64
}

type AuthService interface {
    // User lifecycle
    CreateUser(ctx context.Context, p CreateUserParams) (*User, error)
    GetUserByID(ctx context.Context, id string) (*User, error)
    GetUserByEmail(ctx context.Context, email string) (*User, error)
    GetUserByPhone(ctx context.Context, phone string) (*User, error)
    UpdateUser(ctx context.Context, id string, p UpdateUserParams) (*User, error)
    DeleteUser(ctx context.Context, id string) error
    ListUsers(ctx context.Context, page, perPage int) ([]*User, int, error)

    // Password
    VerifyPassword(ctx context.Context, userID, password string) error
    SetPassword(ctx context.Context, userID, bcryptHash string) error

    // Sessions & tokens
    CreateSession(ctx context.Context, userID string) (*Session, string, error) // session, refreshToken
    GetSession(ctx context.Context, sessionID string) (*Session, error)
    VerifyRefreshToken(ctx context.Context, token string) (*User, *Session, error)
    RevokeSession(ctx context.Context, sessionID string) error
    RevokeAllUserSessions(ctx context.Context, userID string) error

    // OTP / magic link / verification codes
    CreateOTPCode(ctx context.Context, userID, kind string) (token, code string, err error)
    VerifyOTPToken(ctx context.Context, token, kind string) (*User, error)
    VerifyOTPCode(ctx context.Context, userID, kind, code string) error

    // PKCE flow
    CreateFlowState(ctx context.Context, provider, codeChallenge, codeChallengeMethod string) (authCode string, err error)
    GetFlowState(ctx context.Context, authCode string) (codeChallenge, method string, userID string, err error)
    DeleteFlowState(ctx context.Context, authCode string) error

    // Identity linking
    GetOrCreateIdentity(ctx context.Context, provider, providerID string, userMeta map[string]any) (*User, bool, error)
    ListIdentities(ctx context.Context, userID string) ([]map[string]any, error)
    DeleteIdentity(ctx context.Context, userID, provider string) error

    // MFA
    CreateFactor(ctx context.Context, userID, factorType, friendlyName string) (map[string]any, error)
    VerifyFactor(ctx context.Context, factorID, code string) error
    DeleteFactor(ctx context.Context, factorID string) error
    ListFactors(ctx context.Context, userID string) ([]map[string]any, error)

    // Audit
    RecordSignIn(ctx context.Context, userID string)
}
```

### New adapter: `internal/adapter/auth/`

```
internal/adapter/auth/
  service.go      — AuthService implementation (Postgres queries)
  oauth.go        — fetchGoogleUser, fetchGitHubUser, fetchGitHubPrimaryEmail, exchangeCode
  tokens.go       — generateRandomToken, generateNumericCode, base64RawURL helpers
  password.go     — hashPassword, bcrypt helpers
```

`service.go` holds all SQL that today lives in `auth_handler.go`. It receives a `domain.Database` (the `RequestDB`'s inner `Database`) and `*domain.Config`.

**OAuth stays in the adapter** (`oauth.go`) — it makes HTTP calls to Google/GitHub and is not behind a second domain interface since there is one implementation per provider and no test-double need at that level. `AuthHandler` calls `h.authSvc` for data operations and calls the OAuth functions (package-level) for token exchange.

### Updated `AuthHandler`

- Drops `db domain.Database` field
- Gains `authSvc domain.AuthService` field
- All `h.db.QueryRow(...)` / `h.db.Exec(...)` calls replaced with `h.authSvc.*()` calls
- Utility functions that are purely transformational stay in `auth_handler.go`: `buildUser`, `buildSession` (now call `authSvc`), `asString`, `asTimeString`, `decodeJSONB`, `renderAuthTemplate`, `issuer`, `baseURL`
- Email sending stays via `h.email domain.EmailSender` (unchanged)

### `ServerDeps` update

```go
type ServerDeps struct {
    // ... existing fields ...
    AuthService domain.AuthService  // NEW: replaces direct DB auth queries
}
```

### Engine wiring (`internal/app/engine.go`)

Construct `authSvc := auth.NewService(requestDB.Database, cfg)` and pass it into `ServerDeps`.

### Test impact

`auth_handler_test.go`'s `stubDB` is replaced with a `stubAuthService` implementing `domain.AuthService`. Tests become simpler — no SQL string matching, just method call assertions.

---

## Invariants throughout all phases

- `go test -race ./...` green before moving to next phase
- `npm test` (dashboard/) green before moving to next phase
- `go test -run TestSupabaseJSCompat -tags=integration -race ./internal/adapter/http/...` green after Phase 5 (the wire-compat contract does not change)
- No `OwnerDB` → HTTP handler leakage (the distinct-type compile guard stays)
- JWT role wire tokens (`anon`, `authenticated`, `service_role`) unchanged
