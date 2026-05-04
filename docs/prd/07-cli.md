# CLI — Feature PRD

## Overview

The CLI is the primary developer interface for Ultrabase. It handles project scaffolding, local development, schema validation, SDK generation, and production serving. Built with Go's `cobra` library.

---

## Commands

### `ultrabase init <name>`

Scaffold a new project.

```bash
$ ultrabase init my-app
Creating project in ./my-app ...
  + ultrabase.yaml
  + .env.example
  + .gitignore
Done! Next steps:
  cd my-app
  cp .env.example .env    # configure database
  ultrabase dev            # start dev server
```

The generated `ultrabase.yaml` is a **minimal working example** — one table + email auth configured, so `ultrabase dev` boots a working app immediately after setting DB env vars.

**Flags:**
- `--dir <path>` — output directory (default: `./<name>`)

### `ultrabase dev`

Start local development server with hot-reload.

```bash
$ ultrabase dev
  Ultrabase v0.1.0

  ✓ Schema valid (3 tables, 2 functions, 1 event)       12ms
  ✓ Connected to PostgreSQL                               8ms
  ✓ Migrations applied (2 new)                           45ms
  ✓ Seeds applied (1 table)                               6ms
  ✓ Auth enabled (email, google)                          2ms
  ✓ WAL replication slot active                          15ms
  ✓ Search indexes: todos [title, body]                   3ms

  API:     http://localhost:8080
  Docs:    http://localhost:8080/api/docs
  OpenAPI: http://localhost:8080/api/openapi.json

  Watching for changes... (Ctrl+C to stop)
```

**Behavior:**
- Auto-loads `.env` from project root
- Auto-applies migrations (destructive ops prompt y/N interactively)
- Auto-applies seeds
- CORS defaults to `origins: ["*"]` (zero-config local dev)
- Pretty/colored logs
- Hot-reload on YAML changes

**Flags:**
- `--port <port>` — server port (default: 8080)
- `--config <path>` — config file (default: `./ultrabase.yaml`)
- `--no-watch` — disable hot-reload
- `--verbose` — debug logging

### `ultrabase serve`

Production server. No watching, strict defaults.

```bash
$ ultrabase serve
```

**Behavior:**
- Does NOT load `.env` (12-factor: use real env vars)
- Does NOT auto-apply migrations (must opt in)
- Does NOT apply seeds (must opt in)
- CORS requires explicit origins (no default)
- Structured JSON logs (`slog`)
- Graceful shutdown on SIGTERM/SIGINT
- Docs UI disabled by default

**Flags:**
- `--port <port>` — server port (default: 8080)
- `--config <path>` — config file
- `--migrate` — apply pending migrations on startup (or `ULTRABASE_MIGRATE=true`)
- `--seed` — apply seeds on startup
- `--allow-destructive` — permit DROP TABLE/COLUMN in migrations

### `ultrabase validate`

Validate config without starting the server.

```bash
$ ultrabase validate
  ✓ Schema valid

$ ultrabase validate
  ✗ Error in tables.todos.fields.category_id:
    Foreign key references 'categories.id' but table 'categories' is not defined
    at ultrabase.yaml:42
    Suggestion: Define a 'categories' table or remove the foreign_key

  ✗ Error in on.welcome_email:
    Email action configured but no email provider in providers:
    at ultrabase.yaml:78

  Found 2 errors
```

**Two modes:**
- **Syntax-only** (no DB): validates YAML structure, types, references, RLS expressions
- **With DB** (if `ULTRABASE_OWNER_DATABASE_URL` available): additionally shows a dry-run of pending migration diff

**Flags:**
- `--config <path>` — config file
- `--json` — output errors as JSON (for CI)

**Exit codes:** 0 = valid, 1 = errors.

### `ultrabase rollback`

Revert the last migration(s).

```bash
$ ultrabase rollback              # rollback last migration
$ ultrabase rollback --steps 3    # rollback last 3
```

### `ultrabase db`

Database utilities.

```bash
ultrabase db console              # open psql connected to the owner login
ultrabase db dump > backup.sql    # pg_dump
ultrabase db restore backup.sql   # pg_restore
```

### `ultrabase generate sdk`

Generate typed client SDKs from config.

```bash
$ ultrabase generate sdk --lang typescript --output ./sdk
  ✓ Generated types (15 interfaces)
  ✓ Generated client
  ✓ Written to ./sdk/
```

**Flags:**
- `--lang typescript|python|go` — target language
- `--output <dir>` — output directory (default: `./sdk`)

### `ultrabase generate openapi`

Generate OpenAPI spec to file.

```bash
$ ultrabase generate openapi --output ./openapi.json
  ✓ Generated OpenAPI 3.0 spec (45 endpoints)
  ✓ Written to ./openapi.json
```

### `ultrabase slot reset`

Emergency WAL replication slot reset (drop + recreate).

```bash
$ ultrabase slot reset
  ⚠ This will drop the replication slot and recreate it.
  ⚠ Events between the last checkpoint and now will be lost.
  Continue? (y/N) y
  ✓ Slot dropped
  ✓ Slot recreated
```

---

## Config File

- Default: `ultrabase.yaml` in current working directory
- Override: `--config path/to/file.yaml`
- Single-file only in v1 (no directory/split mode, no imports)
- Terminology: the YAML file is the "config" (not "schema"). "Schema" refers to the DB schema.

---

## CLI ↔ Server Auth

| Command Category | Needs | Examples |
|---|---|---|
| Pure-local | Nothing | `init`, `validate` (syntax), `generate sdk`, `generate openapi` |
| Database access | `ULTRABASE_OWNER_DATABASE_URL` | `dev`, `serve`, `validate` (dry-run), `db *`, `rollback` |
| Admin API (remote) | `ULTRABASE_ADMIN_KEY` + `ULTRABASE_URL` | `events retry`, `events purge`, `users disable` |

`ULTRABASE_URL` defaults to `http://localhost:8080`.

---

## Hot-Reload (dev mode)

1. Watch `ultrabase.yaml` for changes
2. On change: validate → migrate diff → remount HTTP routes
3. In-flight requests drain on last-good config before switchover
4. Validation failure: keep running on previous config, log errors prominently; retry on next save
5. Migrations auto-apply; destructive ops prompt y/N interactively

---

## Environment Variables

- `ultrabase dev` auto-loads `.env` from project root
- `ultrabase serve` does NOT load `.env` (12-factor compliance)
- Real env vars always override `.env` values
- Interpolation: `${VAR}` (required) and `${VAR:-default}` (with fallback)
- Startup scans all `${VAR}` refs and reports full list of missing vars at once

---

## Error Reporting

### Schema Validation Errors

```bash
  Error: tables.todos.fields.category_id
  ├─ Foreign key references 'categories.id' but table 'categories' is not defined
  ├─ File: ultrabase.yaml:42
  └─ Suggestion: Define a 'categories' table or remove the foreign_key
```

- File path and line number for every error
- Suggestions for common mistakes
- JSON output mode for CI (`--json`)
- All errors collected, reported as a single list (no "first error wins")

### Runtime Errors

```bash
  ✗ Failed to connect to database
    Host: localhost:5432
    Error: connection refused

    Suggestions:
    - Is PostgreSQL running? Try: pg_isready -h localhost -p 5432
    - Check ULTRABASE_OWNER_DATABASE_URL + ULTRABASE_AUTH_DATABASE_URL in .env
```

---

## Exit Codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | Validation error / general error |
| 2 | Database connection error |

---

## Signals

- `SIGINT` (Ctrl+C) / `SIGTERM` — Graceful shutdown:
  1. Stop accepting new connections
  2. Drain in-flight requests (up to `shutdown` timeout)
  3. Force-exit after timeout
  4. Close DB pool, flush logs, release WAL slot lease

---

## Project Structure

Generated by `ultrabase init`:

```
my-app/
├── ultrabase.yaml              # Main config (single-file mode)
├── .env.example                # Template for env vars
├── .gitignore                  # Ignores .env, uploads/, sdk/
├── templates/                  # Email templates (if auth configured)
│   └── verify.html
└── sdk/                        # Generated SDK (optional)
    ├── types.ts
    ├── client.ts
    └── index.ts
```
