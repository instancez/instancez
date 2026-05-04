# Dashboard — Feature PRD

## Overview

The Dashboard is a browser-based GUI for managing Ultrabase projects. It reads and writes the `ultrabase.yaml` file through a local API, giving developers a visual alternative to hand-editing YAML. The dashboard does **not** replace the CLI — it is a companion that makes config exploration, editing, and debugging faster.

**Primary user:** A developer who has already run `ultrabase init` and has a working project. They want to add a table, tweak an RLS policy, or check event delivery status without context-switching to a YAML reference doc.

**Non-goals:**
- Not a hosted SaaS product — runs locally as part of `ultrabase dev`
- Not a database admin tool — no raw SQL console, no query profiler
- Not a user management UI — no end-user CRUD (that's the API's job)
- No drag-and-drop visual builder — the YAML remains the source of truth; the dashboard is a structured editor for it

---

## Architecture

```
Browser (React SPA)
    │
    ▼
Ultrabase dev server (Go, Gin)
    ├── GET  /api/_admin/config          → returns parsed YAML as JSON
    ├── PUT  /api/_admin/config          → validates + writes YAML to disk
    ├── GET  /api/_admin/config/diff     → dry-run migration diff for current config
    ├── GET  /api/_admin/events          → paginated event log
    ├── GET  /api/_admin/events/dead     → dead-letter queue (already exists)
    ├── POST /api/_admin/events/:id/retry → retry a failed event (already exists)
    ├── POST /api/_admin/events/purge    → purge dead events (already exists)
    ├── GET  /api/_admin/stats           → table row counts, event throughput, storage usage
    └── Static files at /dashboard/*     → embedded SPA assets (go:embed)
```

**Auth:** All `/api/_admin/*` endpoints require the `ULTRABASE_ADMIN_KEY` header. The dashboard prompts for this key on first load and stores it in `sessionStorage`.

**Embedding:** The compiled SPA is embedded into the Go binary via `go:embed`. No separate build step in development — the SPA is built once and vendored, or served from a local dev server in dev mode with a proxy.

---

## Screens

### 1. Overview

The landing page after login. Shows at-a-glance project health.

**Content:**
- Project name and description (from `project:`)
- Server status: port, mode (dev/prod), uptime
- Table list with row counts (from `pg_class.reltuples`)
- Auth status: enabled/disabled, providers configured (email, Google, GitHub)
- Storage buckets with object counts
- Event throughput: delivered / failed / dead in last hour
- Active cron schedules with next fire time

**Layout:** Card grid. Each card links to its detail screen.

---

### 2. Tables

Visual editor for the `tables:` section.

#### Table List (left sidebar)
- List of all tables, sorted alphabetically
- Badge showing field count
- "Add Table" button at top
- Click to select → detail view on right

#### Table Detail (main panel)

**Fields tab:**
- Editable table (spreadsheet-like) with columns: Name, Type, PK, Required, Unique, Default, FK, Enum, Min, Max, Pattern, Check
- Type column: dropdown with all supported Postgres types (`bigserial`, `text`, `integer`, `boolean`, `timestamptz`, `uuid`, `jsonb`, `varchar(N)`, `numeric(P,S)`, `date`, `time`, `text[]`, `integer[]`, `inet`, `interval`)
- FK column: dropdown listing `<table>.<column>` for all tables + `users.id`
- Enum column: tag input (comma-separated values)
- Default column: text input with autocomplete for SQL functions (`now()`, `uuid_v7()`, `uuid_v4()`, `current_date`, `current_time`, `true`, `false`)
- Min/Max: numeric inputs, only enabled when type is numeric
- Add row button at bottom
- Delete row button (with confirmation for existing fields — warns about data loss)
- Drag-to-reorder rows (cosmetic — field order in YAML, not DB order)

**Indexes tab:**
- List of indexes with columns, unique flag, and partial index `where` clause
- Each index: multi-select for columns, checkbox for unique, text input for where
- Add/remove index buttons

**RLS tab:**
- List of RLS policies
- Each policy: multi-select for operations (`select`, `insert`, `update`, `delete`), code editor (monospace, syntax-highlighted) for `check` expression
- Helper buttons that insert common patterns:
  - "Owner only" → `user_id = auth.uid()`
  - "Authenticated" → `auth.is_authenticated()`
  - "Team member" → `<fk> IN (SELECT team_id FROM team_members WHERE user_id = auth.uid())`
- Add/remove policy buttons

**Search tab:**
- Checkbox list of text columns to include in `searchable`
- Dropdown for `search_config` (english, simple, etc.)

**Preview pane (collapsible bottom):**
- Shows the YAML output for this table in real time as the user edits
- Shows the migration diff (DDL statements) that would be generated

---

### 3. Auth

Visual editor for the `auth:` section.

**General settings:**
- Toggle: auth enabled/disabled (adds or removes entire `auth:` block)
- JWT expiry: text input with duration validation (e.g., `15m`, `1h`)
- Refresh tokens: toggle
- Refresh token expiry: text input (e.g., `7d`)

**Custom Fields:**
- Same spreadsheet-style editor as table fields, but scoped to `auth.fields`
- Only non-system fields (system fields `id`, `email`, `password_hash`, `email_verified`, `created_at` shown as read-only)

**Email verification:**
- Toggle: `verify_email`
- Template editor for `verify` and `reset` templates:
  - Subject: text input
  - Body: code editor with template variable autocomplete (`{{data.display_name}}`, `{{link}}`, `{{project.name}}`)
  - Body file: file path input (alternative to inline body)

**OAuth providers:**
- Google: toggle + client_id, client_secret, redirect_url fields
- GitHub: toggle + client_id, client_secret, redirect_url fields
- Values can use `${ENV_VAR}` interpolation — show a hint that env vars are resolved at runtime

---

### 4. Storage

Visual editor for the `storage:` section.

**Bucket list (left sidebar):**
- List of buckets with name, max_size, public badge
- "Add Bucket" button

**Bucket detail (main panel):**
- Name: text input (immutable after creation — warn about rename implications)
- Max size: text input with unit (e.g., `2MB`, `10MB`)
- Allowed types: tag input for MIME patterns (e.g., `image/*`, `application/pdf`)
- Public: toggle (explains: "Public buckets allow unauthenticated downloads")
- RLS policies: same editor as table RLS, but scoped to `_objects` table with `bucket_id` filter auto-applied

**Bucket stats (read-only):**
- Object count
- Total size
- Recent uploads (from `_objects` table)

---

### 5. Functions

Visual editor for the `functions:` section.

**Function list (left sidebar):**
- List of functions with name, method badge (GET/POST/PUT/DELETE), auth badge
- "Add Function" button

**Function detail (main panel):**
- Name: text input
- Description: text input
- Method: dropdown (GET, POST, PUT, DELETE)
- Auth required: toggle
- Query: code editor (monospace, SQL syntax highlighting, multi-line)
  - Placeholder references (`$1`, `$2`, etc.) highlighted inline
  - Warning if placeholder count doesn't match param count

**Params:**
- Spreadsheet editor: Name, Type, Required, Default, Enum, Min, Max
- Sorted alphabetically (determines `$N` mapping) — show the `$N` assignment next to each param name

**Returns:**
- Type: dropdown (`void`, `scalar`, `row`, `rows`)
- Schema: key-value editor (column name → Postgres type) — only shown for `scalar`, `row`, `rows`

**Test pane (collapsible bottom):**
- Input fields for each param (auto-generated from param definitions)
- "Run" button → calls `POST /api/fn/<name>` with the inputs
- Response viewer (formatted JSON)
- Only available when server is running

---

### 6. Events

Visual editor for the `on:` section + event monitoring.

**Trigger list (left sidebar):**
- List of triggers with name, type icon (webhook/email/cron)
- "Add Trigger" button

**Trigger detail (main panel):**
- Name: text input
- Events: tag input for WAL event patterns (e.g., `todos.insert`, `*.delete`)
  - Autocomplete from known tables × operations
- Schedule: cron expression input with human-readable preview ("Every day at 9:00 AM UTC")

**Action editor (one of):**

*Webhook:*
- URL: text input (supports `${ENV_VAR}` interpolation)
- Headers: key-value editor
- Retry max: number input
- Retry backoff: dropdown (exponential, linear)

*Email:*
- To: text input (supports `{{data.field}}` templates)
- To query: code editor (SQL)
- Data query: code editor (SQL)
- Subject: text input with template autocomplete
- Body: code editor with template autocomplete
- Body file: file path input
- Condition: code editor (Go template expression)

**Event log (tab):**
- Table of recent events: ID, name, table, operation, status, attempts, timestamp
- Filter by status: `delivered`, `pending`, `failed`, `dead`
- Filter by trigger name
- Click row → event detail (full payload, delivery attempts with timestamps and errors)
- "Retry" button on failed/dead events
- "Purge Dead" bulk action

---

### 7. Seeds

Visual editor for the `seeds:` section.

**Table selector:**
- Dropdown of tables that have seeds defined (or "Add seeds for table...")

**Seed editor:**
- Spreadsheet-style grid for each table's seed rows
- Columns auto-populated from table field definitions
- Cell types match field types (text input for text, number input for integer, checkbox for boolean, dropdown for enum)
- Password fields show as password inputs — stored as plaintext in YAML (hashed at apply time)
- FK fields: dropdown of seed values from referenced table
- "Add Row" button
- "Remove Row" button with confirmation
- Values can use `${ENV_VAR:-default}` syntax — show raw value in editor

---

### 8. Server Settings

Visual editor for `server:`, `providers:`, `extensions:`, and `project:` sections.

**Project:**
- Name: text input
- Description: text input

**Extensions:**
- Tag input for Postgres extensions (e.g., `pgcrypto`, `pg_trgm`)

**Server:**
- Port: number input
- Max body size: text input with unit
- Docs UI: toggle
- Max limit: number input

**CORS:**
- Origins: tag input
- Methods: checkbox group (GET, POST, PATCH, DELETE, OPTIONS)
- Headers: tag input
- Credentials: toggle
- Max age: number input (seconds)

**Timeouts:**
- Request: duration input
- DB query: duration input
- Upload: duration input
- Shutdown: duration input

**Database pool:**
- Max connections: number input
- Min connections: number input
- Idle timeout: duration input

**Providers:**
- Email: dropdown (none, resend, sendgrid) + env var hint
- Storage: dropdown (none, s3, gcs, minio, local) + env var hint per type

---

## Config Read/Write Flow

### Reading
1. `GET /api/_admin/config` reads `ultrabase.yaml` from disk
2. Parses YAML → `domain.Config` struct → JSON response
3. Dashboard renders JSON into form fields

### Writing
1. Dashboard serializes form state to JSON
2. `PUT /api/_admin/config` receives JSON
3. Server converts JSON → `domain.Config` → validates (same rules as `ultrabase validate`)
4. If valid: marshals to YAML, writes to disk, triggers hot-reload
5. If invalid: returns validation errors as JSON array with field paths

**Conflict handling:** The PUT request includes an `If-Match` header with the SHA-256 of the last-read YAML content. If the file changed on disk since the last read (e.g., hand-edited), the server returns `409 Conflict` with the current file content, and the dashboard shows a diff.

**YAML formatting:** The server preserves comments and formatting where possible. For sections that were edited, it uses a canonical YAML style (flow style for simple field defs like `{ type: text, required: true }`, block style for complex nested structures). The goal is that round-tripping through the dashboard produces clean, readable YAML.

---

## Migration Preview

Every edit in the dashboard shows the migration diff in real time:

1. Dashboard sends the proposed config to `GET /api/_admin/config/diff` (or includes it in a POST body)
2. Server runs `Migrator.Plan()` against the proposed config
3. Returns DDL statements that would be executed
4. Dashboard renders DDL in a diff viewer (green for additions, red for removals)
5. User can review before saving

This prevents "save and pray" — the developer sees exactly what SQL will run.

---

## UI Framework

**Stack:**
- React 19 + TypeScript
- Tailwind CSS 4
- Radix UI primitives (for accessible dialogs, dropdowns, toggles)
- CodeMirror 6 (for SQL and template editors)
- React Router (client-side routing)

**Build:**
- Vite for development and production build
- Output: single `dist/` directory with `index.html` + hashed assets
- Embedded into Go binary via `go:embed` for production
- In development: Vite dev server proxied through Go server

**Routing:**
```
/dashboard                → Overview
/dashboard/tables         → Table list
/dashboard/tables/:name   → Table detail
/dashboard/auth           → Auth config
/dashboard/storage        → Storage config
/dashboard/storage/:name  → Bucket detail
/dashboard/functions      → Function list
/dashboard/functions/:name → Function detail
/dashboard/events         → Event triggers + log
/dashboard/events/:name   → Trigger detail
/dashboard/seeds          → Seed editor
/dashboard/settings       → Server/provider settings
```

---

## API Endpoints (New)

All endpoints require `X-Admin-Key` header matching `ULTRABASE_ADMIN_KEY`.

### `GET /api/_admin/config`

Returns the full parsed config as JSON.

**Response:** `200 OK`
```json
{
  "version": 1,
  "project": { "name": "Acme Todo App", "description": "..." },
  "tables": { ... },
  "auth": { ... },
  "storage": { ... },
  "on": { ... },
  "functions": { ... },
  "seeds": { ... },
  "server": { ... },
  "providers": { ... },
  "extensions": ["pgcrypto", "pg_trgm"],
  "_checksum": "sha256:abc123..."
}
```

The `_checksum` field is the SHA-256 of the raw YAML file on disk, used for conflict detection.

### `PUT /api/_admin/config`

Validates and writes the config to disk.

**Request headers:**
- `If-Match: sha256:abc123...` — checksum from last read

**Request body:** JSON config (same shape as GET response, minus `_checksum`)

**Response:**
- `200 OK` — config saved, hot-reload triggered
- `400 Bad Request` — validation errors:
  ```json
  {
    "errors": [
      {
        "path": "tables.todos.fields.category_id",
        "message": "Foreign key references 'categories.id' but table 'categories' is not defined",
        "suggestion": "Define a 'categories' table or remove the foreign_key"
      }
    ]
  }
  ```
- `409 Conflict` — file changed on disk since last read:
  ```json
  {
    "error": "conflict",
    "current_checksum": "sha256:def456...",
    "current_config": { ... }
  }
  ```

### `GET /api/_admin/config/diff`

Returns the DDL migration diff for the current config (or a proposed config in the request body).

**Response:** `200 OK`
```json
{
  "statements": [
    "ALTER TABLE todos ADD COLUMN priority integer;",
    "ALTER TABLE todos ALTER COLUMN title SET NOT NULL;"
  ],
  "is_destructive": false
}
```

### `GET /api/_admin/stats`

Returns aggregate stats for the overview page.

**Response:** `200 OK`
```json
{
  "tables": {
    "todos": { "row_count": 1542 },
    "users": { "row_count": 87 }
  },
  "events": {
    "last_hour": { "delivered": 42, "failed": 1, "dead": 0 }
  },
  "storage": {
    "avatars": { "object_count": 34, "total_bytes": 8421376 },
    "attachments": { "object_count": 12, "total_bytes": 52428800 }
  }
}
```

---

## Availability

- **`ultrabase dev`**: Dashboard enabled by default at `/dashboard`
- **`ultrabase serve`**: Dashboard disabled by default. Enabled via `--dashboard` flag or `ULTRABASE_DASHBOARD=true`
- Dashboard is read-only in production mode (no PUT to config, no migration apply). The overview, event log, and stats screens remain functional.

---

## Non-Goals (v1)

- **Visual schema designer / ERD diagram** — nice-to-have for v2, not v1
- **Multi-file config editing** — v1 is single-file only
- **Collaborative editing** — single-user, no WebSocket sync
- **Theming / white-labeling** — single design, no customization
- **Data browser** — the dashboard manages config, not data. Use the API or a DB tool for data.
- **Deployment management** — no CI/CD integration, no remote server management
