# Dashboard consistency pass — design

Date: 2026-06-09

## Problem

The dashboard has drifted from the backend's config model and exposes things it
shouldn't. Concretely:

1. **`functions:` vs `rpc:` are conflated.** The backend split these (see
   `internal/domain/schema.go`): `rpc:` is `map[string]Function` (Postgres
   stored procedures, served at `/rest/v1/rpc/<name>`) and `functions:` is
   `map[string]CodeFunction` (Node.js HTTP handlers backed by a `.js` file,
   served at `/functions/v1/<name>`). The dashboard still has a single
   "Functions" surface whose TS type and editor describe the **SQL RPC** shape
   (`language`/`body`/`args`/`returns`, posts to `/rest/v1/rpc/`) but reads/writes
   it from `config.functions`. Since `/api/_admin/config` round-trips the real
   `domain.Config` on GET and PUT, a project that actually uses code functions
   renders garbage today, and **saving from the dashboard corrupts the
   `functions:` block** (binds SQL-shaped fields into `CodeFunction`, dropping
   `runtime`/`file`). The dashboard has no view of `config.rpc` at all.

2. **Settings is exposed** in the nav and routes, surfacing server/CORS/pool
   internals as dashboard-editable.

3. **Live-data clutter:** the Overview shows "N total rows" and per-table row
   counts, which aren't useful in a config editor.

4. **`ultra init` scaffold never exercises storage** — there's no `storage:`
   block, so the flagship bucket feature isn't demonstrated to new users.

## Goals

- Make the dashboard accurately model `rpc:` (SQL) and `functions:` (code) as
  two distinct, correctly-typed surfaces. Saving must never corrupt either block.
- Remove the Settings surface from the dashboard.
- Drop row-count metrics from the Overview.
- Add a storage bucket to the `init` scaffold so the feature ships in the example.

Non-goals: editing JS handler source in the browser; a data browser; reworking
auth/providers/tables/seeds pages.

## Constraints (verified against the code)

- `/api/_admin/config` marshals/unmarshals `domain.Config` verbatim. The TS
  `Config` must carry **both** `rpc` (Function shape) and `functions`
  (CodeFunction shape) accurately. Go marshals nil maps as `null`, so every
  access guards with `|| {}`.
- `config.Validate` (`validateCodeFunctions`) requires `runtime` (`"node"`) and
  `file` to be non-empty but does **not** require the file to exist on disk. The
  dashboard cannot author `.js` files, so the code-function surface is
  **list + metadata-edit only** (no "Add", since a new entry without a real file
  is dead at runtime).
- Storage RLS validation requires each policy to have ≥1 valid operation
  (`select/insert/update/delete`) and a non-empty `check`. Empty `type` is valid
  (defaults to permissive). The scaffold bucket must satisfy this.
- `internal/cli/init_test.go` parses + validates the scaffold YAML; adding a
  `storage:` block must keep it valid and we add an assertion for the bucket.

## Design

### 1. Types (`dashboard/src/lib/types.ts`)

- Rename `FunctionDef` → `RpcFunction` (same fields: description, auth_required,
  language, volatility, security, args, body, returns). Keep `FuncArg`/`FuncReturn`.
- Add `CodeFunction`:
  ```ts
  export interface CodeFunction {
    runtime: string;            // "node"
    file: string;               // path relative to config root
    auth_required: boolean;
    timeout?: string;           // e.g. "30s"
    env?: Record<string, string>;
  }
  ```
- `Config`: add `rpc: Record<string, RpcFunction>`; change
  `functions: Record<string, CodeFunction>`.

### 2. SQL RPC surface (rename existing pages)

The current `Functions.tsx` / `FunctionDetail.tsx` already *are* the SQL RPC
editor — they just point at the wrong key. Rename and re-point:

- `pages/Functions.tsx` → `pages/Rpc.tsx` (`export function Rpc`). Reads
  `config.rpc || {}`. Adds/edits entries under `rpc`. Label copy → "Database
  Functions" / "custom SQL function".
- `pages/FunctionDetail.tsx` → `pages/RpcDetail.tsx` (`export function RpcDetail`).
  Reads/writes `config.rpc`. Keeps the language/volatility/security/body/args
  editor and the `/rest/v1/rpc/<name>` test pane (correct for SQL RPC).
- Routes: `/rpc`, `/rpc/:name`.

### 3. Code (edge) functions surface (new pages)

- `pages/Functions.tsx` (new `export function Functions`): lists
  `config.functions || {}`. Each row shows name, `runtime`, `file`, an `auth`
  badge when `auth_required`, and `timeout` when set. **No "Add" button** (see
  constraints); empty state explains functions are declared in YAML + a `.js`
  file under `functions/`.
- `pages/FunctionDetail.tsx` (new `export function FunctionDetail`): metadata
  editor for one code function — `auth_required` (checkbox), `timeout` (text),
  `runtime` (text, default node), `file` (text), and `env` (key/value list).
  Shows a read-only note: "Edit the handler source in `<file>`." Includes a
  Delete button (removes the config entry; the `.js` file is left untouched).
  No JS editor, no RPC test pane.
- Routes: `/functions`, `/functions/:name`.

### 4. Sidebar / App routing

- Sidebar `NAV_ITEMS`: replace the single Functions entry with two, and drop
  Settings:
  - "Database Functions" → `/rpc` (icon: `Database`)
  - "Edge Functions" → `/functions` (icon: `Code2`)
- `App.tsx`: lazy-import `Rpc`/`RpcDetail` and the new `Functions`/`FunctionDetail`;
  wire `/rpc`, `/rpc/:name`, `/functions`, `/functions/:name`. Remove the
  Settings lazy import and `/settings` route.

### 5. Remove Settings

- Delete `pages/Settings.tsx` and `pages/Settings.test.tsx`.
- The one genuinely useful affordance there — **"Download current config as
  YAML"** — moves to the Overview page header (it already uses `getConfig` +
  `downloadYamlFromConfig`). Extensions/server/CORS/pool editing is dropped from
  the dashboard; those remain YAML-managed. (Trade-off accepted per the "do not
  expose Settings" instruction.)

### 6. Overview: drop row-count metrics

- Remove `totalRows` and the "N total rows" subtitle on the Tables card (remove
  the subtitle entirely).
- Remove the per-table "N rows" figure in the Tables detail list (keep the table
  name + "N fields").
- Keep storage object/byte stats (not named in the request; storage is being
  emphasized). `getStats` therefore stays.
- Drop the now-unused `formatNumber` import. Add the "Download config as YAML"
  button to the header actions.
- Update `Overview.test.tsx`: the "row counts" test asserts the table name +
  "2 fields" only (no row count).

### 7. Init scaffold storage block (`internal/cli/init.go`)

Add a `storage:` block to `scaffoldYAML` after `tables:`:

```yaml
# Storage buckets: file uploads served at /storage/v1/object/<bucket>/<path>.
# Access is governed by RLS, same as tables.
storage:
  avatars:
    public: true          # objects are world-readable by URL
    max_size: 5MB
    types: [image/*]
    rls:
      # Only signed-in users can upload/replace/remove avatars.
      - operations: [insert, update, delete]
        check: "auth.is_authenticated()"
```

Add an assertion in `init_test.go` that the parsed scaffold has the `avatars`
bucket (public, max_size 5MB).

## Testing

- `dashboard/`: `npm test` green. Update `Overview.test.tsx`; remove
  `Settings.test.tsx`. The new code-function pages get light render tests
  mirroring the existing page-test style; the renamed RPC pages keep behavior.
- Go: `go build ./...` and `go test -race ./internal/cli/...` for the scaffold
  change. The diff stays out of `internal/adapter/http`, so `TestSupabaseJSCompat`
  is not implicated.

## Risks

- Renaming page exports could miss a reference. Mitigation: grep for old
  symbols (`FunctionDef`, the old `/functions` SQL test usage) after the change.
- A pre-existing project whose `functions:` block still holds SQL-shaped data
  (from the old dashboard) would now show under Edge Functions as malformed.
  That's pre-existing corruption surfaced, not introduced; out of scope to
  migrate automatically.
