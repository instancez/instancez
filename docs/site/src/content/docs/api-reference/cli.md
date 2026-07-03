---
title: CLI Reference
description: All inz subcommands with flags and examples.
---

```
inz [command] [flags]
```

## inz init

Scaffold a new instancez project in the current directory.

Writes `instancez.yaml`, a `.development.env.example`, and optional boilerplate. Never touches a database.

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
| `--no-watch` | `false` | Disable hot-reload. |
| `--port` | (from config or `8080`) | Override server port. |
| `--use-cloud` | — | Run against the cloud project's draft database (requires `inz init --with-cloud`). |
| `--verbose` | `false` | Enable debug logging. |
| `--watch` | `true` | Watch the config source for changes. |
| `--watch-interval` | `1m` | S3-watch poll interval (minimum 10s). |

```bash
INSTANCEZ_DATABASE_URL=postgres://postgres:postgres@localhost:5432/postgres inz dev
```

## inz serve

Start the production server.

Unlike `dev`, does not hot-reload and defaults to dashboard disabled.

```
inz serve [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--allow-destructive` | `false` | **Not currently enforced.** Flag is accepted but DROP operations are always executed. |
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
| `--branch` | `draft` | With `--project`, branch to preview against: `draft` or `production` (default `draft`) |
| `--config` | `instancez.yaml` | Config source. Env: `INSTANCEZ_CONFIG`. |
| `--json` | `false` | Output errors as JSON (for CI). |
| `--project` | — | Preview migration against a cloud project. Bare `--project` uses `instancez.yaml`'s linked project; `--project <id>` or `--project=<id>` targets a different one. Never creates a project; link one first with `inz cloud deploy --new`. |
| `--use-dsn` | — | After syntax check, plan a migration against this owner-class DSN (plan only — never applied). |

```bash
inz validate
inz validate --use-dsn postgres://owner:pass@localhost/mydb
inz validate --project                # preview against instancez.yaml's linked project
inz validate --project abc123         # preview against a specific project id
```

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

Write the current `instancez.yaml` to the branch named by `--branch` on an instancez Cloud project.

For `--branch production`, shows a diff of what would change and prompts for confirmation before writing. If no project is linked, pass `--new` to create one (after local validation passes) or `--project <id>` to target an existing one without editing the yaml.

```
inz cloud deploy [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--branch` | `draft` | Branch to write: `draft` or `production` (default `draft`) |
| `--config` | `instancez.yaml` | Path to `instancez.yaml`. |
| `--new` | `false` | Create a new instancez Cloud project when none is linked yet (only after local validation passes). |
| `--project` | — | Target this cloud project id for this run, instead of `instancez.yaml`'s `project.cloud.project_id`. Does not modify the file. |
| `--yes`, `-y` | `false` | Skip the confirmation prompt. |

```bash
inz cloud deploy --new --yes       # first deploy: create + link + push
inz cloud deploy --project abc123  # target a specific project without editing the yaml
inz cloud deploy --yes
```

## inz doctor

Run preflight checks for `inz dev` and `inz serve`: config validity, database DSNs, and Postgres role layout.

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

Show the linked cloud project's current state: name, ID, URL, production deploy status, and whether the local draft has unpublished changes.

Requires `inz init --with-cloud`.

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
inz cloud whoami [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `instancez.yaml` | Used to honor `project.cloud.api_url`; ignored if missing. |

```bash
inz cloud whoami
```

## inz version

Print the binary version.

```
inz version
```
