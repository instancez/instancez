---
title: CLI Reference
description: All inz subcommands with flags and examples.
---

```
inz [command] [flags]
```

## inz init

Scaffold a new instancez project in the current directory.

Writes `instancez.yaml`, a `.development.env.example`, and optional boilerplate. Never touches a database. The example code function is scaffolded only when Node.js 22+ is on your PATH; otherwise init warns and omits the `functions:` block.

```
inz init [name] [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--dir` | `.` | Output directory. |
| `--force` | `false` | Overwrite existing scaffolding files. |

```bash
inz init my-app --dir ./my-app
```

## inz dev

Start a local development server with hot-reload.

Reads config, connects to Postgres, runs migrations, and watches for file changes. Requires `INSTANCEZ_DATABASE_URL` (a superuser DSN) to provision roles on every startup, or set `INSTANCEZ_OWNER_DATABASE_URL` and `INSTANCEZ_AUTH_DATABASE_URL` directly.

```
inz dev [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `instancez.yaml` | Config source: file path or `s3://bucket/key`. Env: `INSTANCEZ_CONFIG`. |
| `--dashboard` | `readwrite` | Dashboard mode: `disabled`, `readonly`, or `readwrite`. |
| `--dashboard-write-dotenv` | `true` | Allow the dashboard to write secrets to `.development.env`. |
| `--dotenv-path` | `.development.env` | Path to the .env file for dashboard secret writing. |
| `--embedded-pg` | `false` | Start an embedded Postgres 16 (data at `./pgdata/`); no external DB needed. |
| `--no-watch` | `false` | Disable hot-reload. |
| `--port` | (from config or `8080`) | Override server port. |
| `--reset-pg` | `false` | Wipe `./pgdata/` before starting (requires `--embedded-pg`). |
| `--verbose` | `false` | Enable debug logging. |
| `--watch` | `true` | Watch the config source for changes. |
| `--watch-interval` | `1m` | S3-watch poll interval (minimum 10s). |

```bash
INSTANCEZ_DATABASE_URL=postgres://postgres:postgres@localhost:5432/postgres inz dev
```

## inz serve

Start the production server.

Unlike `dev`, does not hot-reload and defaults to dashboard disabled.

Even without `--migrate`, every boot creates the migration-history table if missing, as its own locked step, then — in a second locked transaction — creates `auth.jwt_keys` if missing, re-applies the privilege revokes on `auth.*` and `_instancez_migrations`, and adds any auth columns or indexes that newer releases introduced. Boot fails if either step fails. With `--watch`, this also re-runs after every hot-reloaded migration; a failure there is logged, not fatal.

```
inz serve [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--allow-destructive` | `false` | Permit `DROP TABLE` and `DROP COLUMN` during migration. Without it, `inz serve` refuses to apply a plan that drops a table or column and reports what would have been lost. `inz dev` always permits drops and logs each one. Env: `INSTANCEZ_ALLOW_DESTRUCTIVE`. |
| `--migrate-lock-timeout` | `5s` | Longest an `inz serve` migration statement waits for a table lock. If a long query holds the table, the migration fails and the server keeps running on the last applied config instead of blocking traffic behind the pending lock. `0` disables the limit. Env: `INSTANCEZ_MIGRATE_LOCK_TIMEOUT`. |
| `--bundle` | — | Bundle pointer: file path or `s3://bucket/key[#version]`. When set, reads config and functions from the bundle archive instead of `--config`. Env: `INSTANCEZ_BUNDLE`. |
| `--config` | `instancez.yaml` | Config source: file path or `s3://bucket/key`. Ignored when `--bundle` is set. Env: `INSTANCEZ_CONFIG`. |
| `--dashboard` | `disabled` | Dashboard mode. Env: `INSTANCEZ_DASHBOARD`. |
| `--dashboard-write-dotenv` | `false` | Allow dashboard to write secrets to a .env file. Env: `INSTANCEZ_DASHBOARD_WRITE_DOTENV`. |
| `--dotenv-path` | — | Path to .env file when `--dashboard-write-dotenv` is set. Env: `INSTANCEZ_DOTENV_PATH`. |
| `--migrate` | `false` | Run pending migrations on startup. |
| `--port` | (from config or `8080`) | Override server port. |
| `--watch` | `false` | Watch the config source for changes. In bundle mode, polls the bundle ETag (S3) or mtime (local) instead of the config file. Env: `INSTANCEZ_WATCH`. |
| `--watch-interval` | `1m` | S3-watch poll interval. Env: `INSTANCEZ_WATCH_INTERVAL`. |

```bash
inz serve --migrate --config instancez.yaml

# Bundle mode: config + functions come from a single archive (no race condition)
inz serve --bundle s3://my-bucket/bundles/app.tar.gz --migrate --watch
inz serve --bundle /path/to/bundle.tar.gz --migrate
```

## inz validate

Validate `instancez.yaml` structure and references without starting the server.

Checks YAML structure, identifiers, cross-references, and verifies that each declared code function's `file:` exists on disk.

With `--use-dsn`, also connects to the database and prints the migration plan (DDL diff) without applying it.

```
inz validate [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `instancez.yaml` | Config source. Env: `INSTANCEZ_CONFIG`. |
| `--json` | `false` | Output errors as JSON (for CI). |
| `--project` | — | Preview against a cloud project. Bare `--project` uses `instancez.yaml`'s linked project; `--project <id>` or `--project=<id>` targets a different one. Never creates a project; link one first with `inz cloud deploy --new`. |
| `--use-dsn` | — | After syntax check, plan a migration against this owner-class DSN (plan only — never applied). |

```bash
inz validate
inz validate --use-dsn postgres://owner:pass@localhost/mydb
inz validate --project                # preview against instancez.yaml's linked project
inz validate --project abc123         # preview against a specific project id
```

## inz vet

Lint `instancez.yaml` for security problems: open RLS policies, disabled RLS, hardcoded secrets, unsafe auth and CORS settings. It reads the file only, with no database or network, and does not interpolate `${...}` values.

```
inz vet [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `instancez.yaml` | Local config file. Env: `INSTANCEZ_CONFIG`. |
| `--json` | `false` | Print the report as JSON. Env: `INSTANCEZ_JSON`. |
| `--fail-on` | `high` | Exit 1 when a finding is at this severity or above: `info`, `low`, `medium`, `high`, `critical`, or `none`. Env: `INSTANCEZ_FAIL_ON`. |

Exit codes: `0` when nothing reaches `--fail-on`, `1` when something does, or when the file cannot be read or parsed. A structurally invalid file (wrong types) is an error; run `inz validate` for details.

`--json` prints every finding, even those below `--fail-on`:

```json
{
  "findings": [
    {
      "rule": "rls-disabled",
      "severity": "critical",
      "path": "tables.posts.rls_enabled",
      "line": 4,
      "title": "Row-level security is off",
      "message": "Anyone with the public key can read and write every row in posts.",
      "fix": "Set rls_enabled: true and add policies, unless the table is meant to be public.",
      "edit": { "path": ["tables", "posts", "rls_enabled"], "value": true }
    }
  ],
  "counts": { "info": 0, "low": 0, "medium": 0, "high": 0, "critical": 1 },
  "checks": { "total": 21, "passed": 20 }
}
```

`checks.total` is the number of rules below, and `checks.passed` is how many of them have no finding. The human output ends with the same tally, for example `20 of 21 checks passed`. `unknown-key` is reported but is not a check.

`edit` is present when one config change resolves the finding: `rls-disabled`, `bucket-public`, `anonymous-signins`, `jwt-expiry-long`, `signup-unverified-email` (only when an email provider is configured), `cors-null-origin` and `max-limit-disabled`. `path` lists object keys and array indexes, and `remove: true` deletes the value instead of setting `value`. Findings whose right fix depends on your app, such as policies, have no `edit`.

The running engine serves the same report at `GET /_admin/vet` (secret key required). It returns `400 invalid_config` when the config does not parse.

Run it in CI to block a merge:

```yaml
- run: inz vet --fail-on medium
```

### Dashboard

The dashboard's Security page runs the same scan. A banner shows how many checks passed, with severity chips to filter the list. Pick a finding to see its detail. When the finding has an `edit`, a Fix it button shows the change in the usual save confirmation and rescans after you confirm; it needs write access to the config. Use Re-scan after editing the config by hand.

### Rules

| Rule | Severity | Flags |
|------|----------|-------|
| `rls-disabled` | critical | A table with `rls_enabled: false`. |
| `policy-open-write` | critical | An insert, update or delete policy whose `using` or `with_check` is always true. |
| `policy-open-read` | low, high | A select policy whose `using` is always true. High when the table has sensitive columns. |
| `policy-authed-read-sensitive` | medium | A select policy that only checks the caller is signed in, on a table with sensitive columns. |
| `policy-no-identity-write` | medium, high | A write policy that never checks the caller's identity, directly or through a declared rpc that does. High when the table has an owner column. |
| `bucket-open-write` | critical | A storage policy that lets any caller with a JWT (including anonymous sign-in users) write or delete. |
| `bucket-open-read` | medium | A select policy on a non-public bucket whose `using` is always true, so any caller with a JWT can list and read every object. |
| `bucket-no-rls` | medium, high | A bucket with no policies. High when other buckets have them. |
| `bucket-public` | low | A bucket with `public: true`. |
| `rpc-definer-no-auth` | low, medium, high | A `security: definer` function callable without sign-in, so its reads bypass RLS. High when the body writes (or runs `EXECUTE`) with no `auth.*` check, medium when it only reads or writes with a check, low when it only reads and checks `auth.uid()`, `auth.jwt()`, `auth.email()` or `auth.role()`. |
| `rpc-definer-search-path` | high | A `security: definer` function with no pinned `search_path`. |
| `rpc-dynamic-sql` | medium, high | A plpgsql `EXECUTE` on a string built with `\|\|` or `format(%s)`. High on definer functions. |
| `function-public-secrets` | low | A function with `auth_required: false` that has secret-looking env values, so anyone can trigger its use (not read it). |
| `hardcoded-secret` | high | A literal email `api_key`, storage keys, OAuth `client_secret`, or secret-looking function env value. |
| `jwt-expiry-long` | medium, high | `auth.jwt_expiry` over 1 hour. High over 24 hours. |
| `signup-unverified-email` | low, medium | Sign-up is open and `auth.email.verify_email` is off. Low when no `providers.email` is configured to send mail. |
| `anonymous-signins` | low | `auth.allow_anonymous: true`. |
| `redirect-insecure` | low, medium | An auth redirect URL that uses a wildcard or local http (low), or plain http or a `javascript:`, `data:` or `vbscript:` scheme (medium). |
| `cors-wildcard` | low | `*` in `server.cors.origins`. |
| `cors-null-origin` | medium | `null` in `server.cors.origins`. |
| `unknown-key` | medium | A key that is not a known setting (usually a typo), so it has no effect. |
| `max-limit-disabled` | medium | `server.max_limit: -1` (no row limit). |

## inz bundle

Build a self-contained tar.gz bundle from `instancez.yaml` and `functions/`.

The bundle is the deployment artifact for projects that use code functions. It contains `instancez.yaml`, `functions/` (with vendored `node_modules/`), and a `manifest.json`. Upload it to S3 then set `functions_bundle:` in `instancez.yaml` to the returned pointer.

Runs stateless validation (same as `inz validate`) including checking that each declared function's `file:` exists on disk.

```
inz bundle [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `instancez.yaml` | Path to `instancez.yaml`. |
| `--output` | — | Destination: local file path or `s3://bucket/key`. If omitted, writes to a temp file and prints the path. |

```bash
inz bundle                                       # write temp file, print path
inz bundle --output bundle.tar.gz                # write to local file
inz bundle --output s3://my-bucket/bundle.tar.gz # upload to S3, print pointer
```

## inz cloud deploy

Write the current `instancez.yaml` to an instancez Cloud project.

Shows a diff of what would change and prompts for confirmation before writing. If no project is linked, pass `--new` to create one (after local validation passes) or `--project <id>` to target an existing one without editing the yaml.

```
inz cloud deploy [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `instancez.yaml` | Path to `instancez.yaml`. |
| `--new` | `false` | Create a new instancez Cloud project when none is linked yet (only after local validation passes). |
| `--project` | — | Target this cloud project id for this run, instead of `instancez.yaml`'s `project.cloud.project_id`. Does not modify the file. |
| `--yes`, `-y` | `false` | Skip the deploy confirmation prompt. |

```bash
inz cloud deploy --new             # first deploy: create + link + push
inz cloud deploy --project abc123  # target a specific project without editing the yaml
inz cloud deploy --yes             # skip the confirmation prompt (e.g. in CI)
```

## inz doctor

Run preflight checks for `inz dev`: config validity, the superuser database DSN, and Postgres role layout.

Exits non-zero if any check fails.

```
inz doctor [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `instancez.yaml` | Path to `instancez.yaml`. |

```bash
inz doctor
```

## inz cloud status

Show the linked cloud project's current state: name, ID, URL, and deploy status.

Requires a linked project (`inz cloud deploy --new` links one).

```
inz cloud status [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `instancez.yaml` | Path to `instancez.yaml`. |

```bash
inz cloud status
```

## inz cloud login

Authenticate against instancez Cloud via device-code flow.

Opens a browser to confirm a one-time code, then stores a Personal Access Token at `~/.instancez/credentials`.

```
inz cloud login [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--force` | `false` | Re-authenticate even if already logged in. |

```bash
inz cloud login
```

## inz cloud logout

Remove the PAT stored at `~/.instancez/credentials`. The token remains valid server-side until revoked from the dashboard.

```
inz cloud logout
```

## inz cloud whoami

Print the currently logged-in instancez Cloud user.

```
inz cloud whoami
```

## inz version

Print the binary version.

```
inz version
```
