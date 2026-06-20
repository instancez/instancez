# Backend audit: redundancy, reuse, extensibility

Scope: the Go backend under `internal/`, `pkg/`, `cmd/` (~25k source LOC, ~24k test LOC). Frontend not covered. Measurements taken on the `main` worktree at the current HEAD.

## How much redundant code is there, really

I ran a token-based clone detector (`dupl`) over the whole tree, tests included:

- **84 clone groups** at a 50-token threshold (catches small repeats).
- **9 clone groups** at a 100-token threshold (substantial, copy-paste-sized blocks).

The split matters: **about three quarters of the duplication is in test files**, not source. Counting clone occurrences, 143 land in `_test.go` files versus 47 in source. So "redundant code" here is mostly a test-fixture problem, not a production-logic problem.

Worst offenders by clone count:

| File | Clones | Kind |
|------|--------|------|
| `internal/app/migrate_integration_test.go` | 35 | test |
| `internal/adapter/http/pgrupstream/conformance_test.go` | 19 | test |
| `internal/adapter/http/auth_handler_test.go` | 19 | test |
| `internal/config/validate_test.go` | 15 | test |
| `internal/adapter/http/admin_handler.go` | 8 | source |
| `internal/adapter/auth/service.go` | 8 | source |
| `internal/cloud/client.go` | 4 | source |

The source-side clones are short and low-severity. For example `admin_handler.go:142-162` repeats a list-query handler shape, and `auth/service.go:40-73` repeats a `QueryRow → nil → ErrNotFound` lookup across `GetUserByID` / `GetUserByEmail` / `GetUserIDByEmail`. These are readable as-is; extracting them buys little. I would not prioritize them.

## Where the real wins are

### 1. Auth error responses are hand-built 69 times (highest priority)

The PostgREST error path is already well-centralized: `problemJSON` (`middleware.go:401`) is called **221 times** and emits the canonical `{code, message, details, hint}` PostgREST envelope. That part is healthy.

The auth/GoTrue path is not. There are **69 inline `gin.H{...}` error envelopes**, almost all in `auth_handler.go`, each hand-assembling the GoTrue error shape (`error`, `error_description`, `msg`, `code`). Two problems:

- It is duplication: the same shape rebuilt by hand dozens of times.
- It is a contract risk. `CLAUDE.md` names the error envelope a load-bearing supabase-js contract surface, enforced by `TestSupabaseJSCompat`. One typo'd key in one of 69 hand-written envelopes is a silent client-parse break that the compat test only catches if that exact path is exercised.

Recommendation: add an `authError()` helper that mirrors `problemJSON`, and route the auth handlers through it. Do not merge the two into one envelope. PostgREST and GoTrue deliberately use different wire shapes, and supabase-js depends on that. The goal is one helper per shape, not one shape.

### 2. Provider selection is hardcoded switches, not a registry (extensibility)

The domain layer is in good shape for an open-source project. It defines clean port interfaces: `EmailSender`, `ObjectStore`, `FunctionRuntime`, `AuthService`, `Database`, `Tx`. Those are the right bones for contributors to plug in new backends.

The wiring undercuts it. Concrete providers are chosen by hardcoded `switch` statements:

- `internal/cli/providers.go:19,38`: `switch cfg.Providers.Email.Type` (`resend`) and `switch cfg.Providers.Storage.Type` (`s3`, `local`).
- OAuth is worse: provider logic is scattered across **seven** `switch provider` sites in `auth_handler.go` (lines 426, 1176, 1204, 1255, 1272, 1942, 1961) plus `auth/oauth.go:24`.

For a contributor, "add a storage backend" or "add an OAuth provider" means hunting down and editing every switch. A small registry (`Register(name, constructor)`) behind each port interface turns that into one self-contained file per provider, which is the difference between a contribution someone can make in an afternoon and one they give up on. OAuth especially should consolidate to a single provider abstraction rather than seven scattered switches.

### 3. A few files are too big to navigate

`auth_handler.go` is 2037 lines, `crud_handler.go` 1279, `storage_v1_handler.go` 1078, `admin_handler.go` 934. These aren't redundant, just large. Splitting `auth_handler.go` along its natural seams (password, OAuth, MFA, magic-link/OTP, admin) lowers the barrier to a first-time reader, and it pairs naturally with the OAuth-consolidation work above.

### 4. Test duplication: shared fixtures over copy-paste

This is where most of the measured redundancy lives. The integration and conformance suites repeat setup blocks heavily (`migrate_integration_test.go`, `conformance_test.go`, `auth_handler_test.go`). Table-driven cases plus a few shared `testutil` builders would cut the bulk. Lower urgency than the source-side items, but it is the bulk of the raw number, and clean tests are part of what makes an OSS project approachable.

## Pre-open-source hygiene (one-time, do before first public push)

Largely clean already:

- `.development.env` is **not** tracked; `.gitignore` covers `.env`, `.env.*`, `*.env`. Good.
- `uploads/` is empty.

One thing to verify: history has commits touching files named `credentials` and `secret.yaml` (e.g. `017644d`, `4c7195d`). These look like templates and code, not real secrets, but a public push exposes all of history irreversibly. Run a history scanner (`gitleaks detect`) once before going public, and confirm those commits carry no real values.

## Suggested order

1. `authError()` helper for the 69 inline auth envelopes. Fixes a contract risk and removes the duplication at the same time, so it goes first.
2. Provider registries behind the existing port interfaces; consolidate the seven OAuth switches.
3. `gitleaks` history scan before the first public push.
4. Split the oversized handlers.
5. Shared test fixtures to drain the test-side duplication.

The architecture itself is sound. The hexagonal layering, the real port interfaces, RLS as the single authorization layer, and the supabase-js compat harness are all working in its favor. The work here is finishing the seams the design already implies, not reworking it.
