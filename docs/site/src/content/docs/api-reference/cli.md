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
| `--generate-like` | — | Generate `instancez.yaml` from a free-form prompt (requires `inz login`). |
| `--with-cloud` | — | Create a project in instancez Cloud (requires `inz login`). |

```bash
inz init my-app --dir ./my-app
```

## inz dev

Start a local development server with hot-reload.

Reads config, connects to Postgres, runs migrations, and watches for file changes. Set `INSTANCEZ_DATABASE_URL` (a superuser DSN) to let dev provision roles on first run, or set `INSTANCEZ_OWNER_DATABASE_URL` and `INSTANCEZ_AUTH_DATABASE_URL` directly.

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
| `--allow-destructive` | `false` | Permit `DROP TABLE`/`COLUMN` in migrations. |
| `--config` | `instancez.yaml` | Config source: file path or `s3://bucket/key`. Env: `INSTANCEZ_CONFIG`. |
| `--dashboard` | `disabled` | Dashboard mode. Env: `INSTANCEZ_DASHBOARD`. |
| `--dashboard-write-dotenv` | `false` | Allow dashboard to write secrets to a .env file. Env: `INSTANCEZ_DASHBOARD_WRITE_DOTENV`. |
| `--dotenv-path` | — | Path to .env file when `--dashboard-write-dotenv` is set. Env: `INSTANCEZ_DOTENV_PATH`. |
| `--migrate` | `false` | Run pending migrations on startup. |
| `--port` | (from config or `8080`) | Override server port. |
| `--watch` | `false` | Watch the config source for changes. Env: `INSTANCEZ_WATCH`. |
| `--watch-interval` | `1m` | S3-watch poll interval. Env: `INSTANCEZ_WATCH_INTERVAL`. |

```bash
inz serve --migrate --config instancez.yaml
```

## inz validate

Validate `instancez.yaml` structure and references without starting the server.

With `--use-dsn`, also plans (but does not apply) the migration needed to bring the database in sync.

```
inz validate [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `instancez.yaml` | Config source. Env: `INSTANCEZ_CONFIG`. |
| `--json` | `false` | Output errors as JSON (for CI). |
| `--project` | `false` | Preview migration against the linked cloud project. |
| `--use-dsn` | — | After syntax check, plan a migration against this owner-class DSN. |

```bash
inz validate --use-dsn postgres://owner:pass@localhost/mydb
```

## inz deploy

Deploy the current `instancez.yaml` to an instancez Cloud project.

Shows a migration preview and prompts for confirmation before promoting the draft to production.

```
inz deploy [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `instancez.yaml` | Path to `instancez.yaml`. |
| `--functions-bundle-dest` | — | `s3://bucket/key` destination for the built functions bundle. |
| `--yes`, `-y` | `false` | Skip the confirmation prompt. |

```bash
inz deploy --yes --functions-bundle-dest s3://my-bucket/bundles/
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

## inz status

Show the linked cloud project's current state: name, ID, URL, production deploy status, and whether the local draft has unpublished changes.

Requires `inz init --with-cloud`.

```
inz status [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `instancez.yaml` | Path to `instancez.yaml`. |

```bash
inz status
```

## inz login

Authenticate against instancez Cloud via device-code flow.

Opens a browser to confirm a one-time code, then stores a Personal Access Token at `~/.instancez/credentials`.

```
inz login [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--force` | `false` | Re-authenticate even if already logged in. |

```bash
inz login
```

## inz logout

Remove the PAT stored at `~/.instancez/credentials`. The token remains valid server-side until revoked from the dashboard.

```
inz logout
```

## inz whoami

Print the currently logged-in instancez Cloud user.

```
inz whoami [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `instancez.yaml` | Used to honor `project.cloud.api_url`; ignored if missing. |

```bash
inz whoami
```

## inz version

Print the binary version.

```
inz version
```
