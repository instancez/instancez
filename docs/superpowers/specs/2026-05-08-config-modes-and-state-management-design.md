# Config modes and state management — design

<context>
Ultrabase has two CLI commands today (`dev`, `serve`), a YAML-on-disk source of truth, and a dashboard that already round-trips the config via `GET/PUT /api/_admin/config`. The product needs to be usable across three deployment shapes — solo / single-container, k8s / platform team, and OSS dashboard-driven — without becoming three different products. This spec defines the small set of operator-controlled levers that make all three work coherently in v1, and the boot-time and runtime behavior that follows from those levers.
</context>

<scope>
**In scope (v1):**
- Two storage backends for the YAML blob: local file and S3-compatible object store.
- Single-replica deployments only.
- Operator-controlled toggles via CLI flags and env vars (never inside the YAML the toggles govern).
- UI edit behavior, including messaging that surfaces source-of-truth drift to operators.
- Boot-time migration behavior, including failure handling (log + run last good) backed by atomic transactional apply.
- A new admin endpoint exposing drift state.

**Explicitly punted (post-v1):**
- Multi-replica coordination (advisory locks, LISTEN/NOTIFY, distributed migration runner).
- An `ultrabase apply` CLI verb (deferred until long migrations or zero-downtime become real user pain).
- An `ultrabase config export` CLI verb (the dashboard offers a download instead).
- Drift detection or three-way merge between repo YAML and live blob (operators reconcile by hand with `diff`).
- Additional backends (k8s ConfigMap, GCS native, secrets managers, generic HTTP+ETag).
- S3 watch via event notifications.
</scope>

## Operator-controlled levers

Three levers, set by CLI flag or env var. **Never** in the YAML they control — putting them inside the file/blob they govern is circular (a UI edit could lock itself out; switching backends raises "where does the change get written"). They are deployment concerns, not application concerns.

| Flag | Env var | Default in `serve` | Default in `dev` |
|---|---|---|---|
| `--config <path-or-uri>` | `ULTRABASE_CONFIG_SOURCE` | `./ultrabase.yaml` | hardcoded `./ultrabase.yaml` |
| `--watch` / `--no-watch` | `ULTRABASE_CONFIG_WATCH` | off | hardcoded on |
| `--ui-edit` / `--no-ui-edit` | `ULTRABASE_CONFIG_UI_EDIT` | off | hardcoded on |

**Backend is derived from the `--config` URI scheme**, not a separate flag:
- A bare path (`./ultrabase.yaml`, `/etc/ultrabase/config.yaml`) → `file` backend.
- `s3://bucket/key`, `s3+https://endpoint/bucket/key` → `s3` backend (any S3-compatible service: AWS S3, R2, GCS via S3 compat, MinIO).

This avoids a redundant `--backend` flag and lets the URI scheme be the single source of intent.

**`ultrabase dev` is unchanged** and ignores these flags. It hardcodes file backend, watch on, ui-edit on, auto-loads `.env`, and applies lenient CORS defaults. The flags exist for `serve` only.

**Validation at startup:**
- `--watch` with an `s3://` URI → reject with a clear error (`config watch is only supported with file backends in v1`).
- `--config` URI with an unsupported scheme → reject (`unsupported config backend: <scheme>`).
- S3 URI without resolvable credentials in env → reject before opening the listener.

## Mode matrix

The three named user segments map onto combinations of these levers:

| Segment | Topology | `--config` | `--watch` | `--ui-edit` |
|---|---|---|---|---|
| Solo / small team | local laptop | (uses `dev`) | (on) | (on) |
| Solo / small team | single container, GitOps | `./ultrabase.yaml` (volume mount) | off | off |
| Engineering team | k8s, GitOps | `s3://…` | off | off |
| OSS / dashboard-driven | single container | `./ultrabase.yaml` | off | on |
| OSS / dashboard-driven | serverless | `s3://…` | off | on |

There is no need for named presets beyond `dev` and `serve` — the lever combinations cover all five cells with one CLI surface.

## UI edit behavior

When `--ui-edit` is on, `PUT /api/_admin/config` is enabled. The handler:

1. Receives the proposed JSON config.
2. Validates it (same rules as `ultrabase validate`).
3. Diffs it against the running config; builds a migration plan.
4. Opens a Postgres transaction; runs the migration statements; on success, records a new row in `_ultrabase_migrations` (with the full `config_json`), then writes the new YAML to the configured backend.
5. On any failure: rolls back the tx, leaves the backend untouched, returns a 4xx with the failing statement and reason.

Order matters: **DB migration first, backend write second.** If the backend write succeeds but the migration didn't, we'd have a stale "successful" config in the source that doesn't match the DB. This order keeps DB and backend consistent even on partial failure.

When `--ui-edit` is off, `PUT /api/_admin/config` returns `403 Forbidden` with a body pointing the operator at their deployment pipeline:

```json
{
  "error": "ui_edit_disabled",
  "message": "This deployment is GitOps-managed. To change the configuration, update the source YAML and redeploy.",
  "config_source": "s3://my-bucket/ultrabase.yaml"
}
```

Schema-editing screens in the dashboard render read-only with a banner explaining the same. Ops actions that don't touch config (event retry, user disable, password reset) continue to work regardless.

### UI edit messaging

When `--ui-edit` is on, two surfaces make the operator aware of the source-of-truth question:

**Persistent banner** at the top of every dashboard page:

> **Live edit mode.** Changes you make here are written directly to `<config_source>` and applied to the database. If your team manages `ultrabase.yaml` in git, mirror these changes there — anything written here will be overwritten the next time the source is updated outside the dashboard. [Download current config →]

**Save-confirmation toast** after every successful PUT:

> Saved to `<config_source>`. Migrations applied: `<count>` statement(s). **Reminder:** update your git source to match, or your next external update will revert this. [Download YAML]

Both surfaces show the actual `config_source` URI so the operator knows whether they wrote to a file or an S3 object. The Download links call `GET /api/_admin/config` and serialize the response to YAML on the client.

## Boot-time migration behavior

The defining behavior change from today: **the server never crashes on migration failure.**

```
read incoming config from --config backend     → incoming
load latest row from _ultrabase_migrations     → lastGood (deserialize config_json)
diff(lastGood, incoming)                       → migration plan

if plan is empty:
    run with incoming
else:
    BEGIN
    for each stmt in plan: tx.Exec(stmt)
    if any error:
        ROLLBACK
        log error + reason + failing statement
        run with lastGood
        set drift status
    else:
        INSERT _ultrabase_migrations(config_json = incoming)
        COMMIT
        run with incoming
```

**Edge case — first boot, no `lastGood`:** there is no fallback to run on. Exit with a clear error pointing at the failing statement and the YAML location. Single-replica or not, we cannot synthesize a config out of thin air. This is the only crash-on-config-failure path that remains.

**Atomicity fix:** today's `ExecDDL` (`internal/adapter/postgres/pool.go:198`) calls `pool.Exec(ctx, sql)` with the whole migration as one multi-statement string. Under pgx's simple protocol that runs autocommit per statement, so a failure mid-migration leaves earlier statements committed. To make the "fall back to last good" promise hold, the migration apply must run in an explicit transaction (`pool.BeginTx` → loop → `Commit`/`Rollback`). The DDL we generate is uniformly tx-safe (the YAML schema can't express non-transactional statements like `CREATE INDEX CONCURRENTLY`), so wrapping in a tx requires no other changes.

**Retry policy:**
- `--watch` on (file backend only): fsnotify fires when the operator edits the file again. Natural retry on save.
- `--watch` off (file or S3): no automatic retry. Drift persists until next process restart, or until a UI edit (when `--ui-edit` is on) provides a new candidate that succeeds.

**Heartbeat logging while drifted:** the server logs an error-level line on every boot in drift state, then again every N minutes (default 10), so the issue doesn't get buried in log volume:

```
ERROR config drift: source <config_source> has unapplied changes
      (failed: <reason>)
      running with last successful config from <timestamp>
```

## Drift visibility — `GET /api/_admin/config/status`

A new admin endpoint exposes drift state for monitoring and dashboard use:

```
GET /api/_admin/config/status
Authorization: <admin key>
```

Response:

```json
{
  "status": "ok" | "drift",
  "config_source": "s3://my-bucket/ultrabase.yaml",
  "running": {
    "applied_at": "2026-05-08T14:22:11Z",
    "checksum": "sha256:..."
  },
  "source": {
    "checksum": "sha256:...",
    "last_seen_at": "2026-05-08T14:25:03Z"
  },
  "last_error": null | "ERROR: column \"foo\" cannot be cast to type ..."
}
```

External monitoring (Prometheus, Datadog, plain HTTP probes) can alert on `status == "drift"`. The dashboard polls this endpoint to decide whether to render the drift banner described below.

### Drift banner (dashboard)

Shown at the top of every page when `status == "drift"`, regardless of `--ui-edit`:

> ⚠️ **Configuration drift.** The source `<config_source>` has changes that failed to apply: `<last_error>`. The server is running on the last successful config from `<applied_at>`. Fix the source and restart, or revert the failing change. [View error details]

The "View error details" link expands to show the failing DDL statement(s) and the Postgres error message verbatim.

### UI edits while drifted

When `--ui-edit` is on and the server is in drift state, the dashboard's editing surfaces show the **running** config (`lastGood`), not the failing source. This way, a user fixing things via UI is editing what is actually live.

A successful UI save in this state follows the same DB-first / backend-second order as a normal UI edit:
1. Migrations run in a tx against `lastGood` → new candidate. On commit, the new candidate becomes the recorded `lastGood` in `_ultrabase_migrations`.
2. The new candidate is written to the backend, overwriting the failing source content. Drift is cleared.
3. If the migration fails, the tx is rolled back, the backend is **not** written, and we stay in drift state with the new error reflected in `last_error`.

This makes "fix it from the dashboard" a viable recovery path when GitOps round-trips are slow or the operator just needs to unstick prod.

## What stays the same

- `_ultrabase_migrations.config_json` already exists (`internal/adapter/postgres/pool.go:97-115`); we just use it on the failure path. No schema change to the framework's internal tables.
- `GET /api/_admin/config` and `PUT /api/_admin/config` keep their current shape; PUT gains the gate described above.
- `ultrabase dev` and `ultrabase serve` keep their command names and arguments.
- WAL-driven event delivery, two-DB-login architecture, and the supabase-js wire compat surface are untouched.

## Implementation surface

Components that change or are added:

- **`internal/cli/serve.go`** — register `--config`, `--watch`, `--ui-edit` flags; resolve env-var fallbacks; reject invalid combinations (watch + s3, unknown URI scheme, missing S3 creds).
- **`internal/config/loader.go`** (or new `internal/config/source.go`) — backend abstraction with `file` and `s3` implementations. Read returns bytes + checksum; Write takes bytes + expected checksum (ETag for S3, mtime for file) and returns the new checksum.
- **`internal/adapter/postgres/pool.go`** — replace `ExecDDL`'s single-string apply with a tx-wrapped variant that takes `[]string` and runs each statement inside one `pool.BeginTx` → loop → `Commit`/`Rollback`. The diff layer already produces `[]string` (`migrationPlan.Removals`, `Additions`, `Alterations` in `internal/app/migrate_config_diff.go`), so no string-splitting is required at the call site.
- **`internal/app/migrate.go`** — `Apply` returns failure without wiping the running config; engine boot loop catches the error, sets a drift state, falls back to `lastGood`'s `config_json`.
- **`internal/app/engine.go`** — track drift state in memory (`{status, lastError, sourceChecksum, runningChecksum, runningAppliedAt}`) and expose a getter.
- **`internal/adapter/http/admin_handler.go`** — add `GET /_admin/config/status`; gate `PUT /_admin/config` on `--ui-edit`.
- **`dashboard/src`** — drift banner component (polls `/_admin/config/status`); UI edit banner + save toast; download-as-YAML helper.

## Open questions deferred to implementation planning

- Exact heartbeat-log interval (default 10 min, configurable?).
- Whether the file backend uses `mtime + size` or computed checksum for change detection.
- S3 credential resolution (SDK default chain vs explicit env).
- Whether the dashboard polls `/_admin/config/status` on a timer or relies on user navigation.
- Behavior if `--config` points to a file the process cannot write to but `--ui-edit` is on (reject at startup vs allow PUTs that 500 on write).
