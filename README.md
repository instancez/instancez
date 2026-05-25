<p align="center">
  <img src="docs/logo.svg" alt="Ultrabase" width="80" height="80" />
</p>

<h1 align="center">ultrabase</h1>

<p align="center">
  Define your backend in YAML. Get a production-ready REST API.
</p>

<p align="center">
  <a href="#quickstart">Quickstart</a> &middot;
  <a href="#yaml-schema">YAML Schema</a> &middot;
  <a href="#features">Features</a> &middot;
  <a href="docs/">Documentation</a>
</p>

---

Ultrabase is a declarative backend framework. A single YAML file defines your tables, authentication, file storage, event webhooks, and custom SQL functions. A single Go binary reads that file and produces a fully functional REST API powered by PostgreSQL.

No backend code. No migrations to write. No ORM to learn.

```yaml
version: 1
project:
  name: My App

auth:
  jwt_expiry: 15m
  refresh_tokens: true

tables:
  todos:
    fields:
      id:    { type: bigserial, primary_key: true }
      title: { type: text, required: true }
      done:  { type: boolean, default: false }
      user_id: { foreign_key: { references: users.id, on_delete: cascade } }
    rls:
      - operations: [select, insert, update, delete]
        check: user_id = auth.uid()

storage:
  avatars:
    max_size: 2MB
    types: [image/*]

on:
  welcome_email:
    events: [users.insert]
    webhook:
      url: https://api.example.com/welcome
```

```sh
ultra dev --use-dsn
# REST API on :8080, dashboard on :5173
```

## How It Differs

| | Ultrabase | Supabase | Firebase | Hasura |
|---|---|---|---|---|
| Config format | YAML (declarative) | Dashboard / SQL | Dashboard / JSON | Console / metadata |
| Query interface | PostgREST-compatible | PostgREST | Proprietary SDK | GraphQL |
| Auth | Built-in, YAML-configured | Built-in (GoTrue) | Built-in | External |
| Custom logic | SQL functions in YAML | Edge functions | Cloud functions | Actions / Events |
| Self-hosted | Single binary | Docker Compose (many services) | No | Docker |
| Source of truth | YAML file | Database + dashboard | Dashboard | Metadata files |
| Vendor lock-in | None (YAML + Postgres) | Medium | High | Medium |

## Quickstart

### Using Docker Compose

```sh
git clone https://github.com/user/ultrabase.git
cd ultrabase
docker compose -f docker-compose.dev.yaml up
```

This starts PostgreSQL 17, the Ultrabase API server on port `8080`, and the dashboard on port `5173`.

### From Source

```sh
# Prerequisites: Go 1.25+, PostgreSQL 17+, Node.js 20+ (for dashboard)

go build -o ultra ./cmd/ultra

# Initialize the project and bootstrap a Postgres in one step. Supply any
# CREATEROLE-capable DSN (superuser works); ultra creates ultrabase_owner +
# authenticator + the API roles and writes the resulting DSNs to .development.env.
./ultra init --with-dsn postgres://superuser:pass@localhost:5432/mydb

export JWT_SECRET="your-secret"
export ULTRABASE_ADMIN_KEY="your-admin-key"

./ultra dev --use-dsn
```

### CLI Commands

```sh
ultra init [--with-dsn <url>]        # Scaffold project; optionally bootstrap a DB
ultra dev --use-dsn                   # Start dev server with hot-reload
ultra serve                           # Production mode (reads .production.env)
ultra validate [--use-dsn <url>]      # YAML syntax check; with DSN, also plan a migration
ultra slot reset                      # Drop & recreate the WAL replication slot
ultra version
```

## Features

### Tables & Auto-Migrations

Define tables in YAML. Ultrabase diffs your schema against the database and applies migrations automatically on startup.

```yaml
tables:
  todos:
    fields:
      id:         { type: bigserial, primary_key: true }
      title:      { type: text, required: true }
      status:     { type: text, enum: [pending, active, done], default: pending }
      priority:   { type: integer, min: 0, max: 5, default: 0 }
      due_date:   { type: date }
      created_at: { type: timestamptz, default: "now()" }
      team_id:    { foreign_key: { references: teams.id, on_delete: cascade } }
    indexes:
      - columns: [team_id, status]
      - columns: [due_date]
        where: "status != 'done'"
    searchable: [title]
```

Every table gets a PostgREST-compatible REST API:

```sh
# Create
curl -X POST /rest/v1/todos -d '{"title": "Ship it", "status": "active"}'

# Read with filters
curl /rest/v1/todos?status=eq.active&order=created_at.desc&limit=10

# Full-text search
curl /rest/v1/todos?search=ship
```

### Row-Level Security

All authorization runs through Postgres RLS policies, declared in YAML. No HTTP-level middleware, no role tables.

```yaml
tables:
  todos:
    rls:
      - operations: [select]
        check: team_id IN (SELECT team_id FROM team_members WHERE user_id = auth.uid())
      - operations: [insert]
        check: user_id = auth.uid()
      - operations: [update, delete]
        check: user_id = auth.uid()
```

`auth.uid()` and `auth.is_authenticated()` are available in every policy expression.

### Authentication

Email/password, Google OAuth, and GitHub OAuth out of the box. JWTs with configurable expiry and optional refresh tokens.

```yaml
auth:
  jwt_expiry: 15m
  refresh_tokens: true
  refresh_token_expiry: 7d
  fields:
    display_name: { type: text }
    avatar_url:   { type: text }
  google:
    client_id: ${GOOGLE_CLIENT_ID}
    client_secret: ${GOOGLE_CLIENT_SECRET}
    redirect_url: http://localhost:3000/auth/callback
```

```sh
# Sign up
curl -X POST /auth/v1/signup -d '{"email": "me@example.com", "password": "..."}'

# Sign in
curl -X POST /auth/v1/token?grant_type=password -d '{"email": "me@example.com", "password": "..."}'
```

### File Storage

Named buckets with MIME type restrictions, size limits, and RLS policies. Ultrabase returns signed URLs; clients upload and download directly to/from the storage provider.

```yaml
providers:
  storage:
    type: s3  # or gcs, minio, local

storage:
  avatars:
    max_size: 2MB
    types: [image/*]
    rls:
      - operations: [insert, delete]
        check: auth.uid() IS NOT NULL
  documents:
    max_size: 10MB
    types: [application/pdf, image/*]
    public: true
```

### Events & Webhooks

WAL-based change data capture. No polling. Every insert, update, and delete is captured through PostgreSQL logical replication and dispatched to webhooks or email actions.

```yaml
on:
  new_todo_notify:
    events: [todos.insert]
    webhook:
      url: https://hooks.slack.com/...
      headers:
        Authorization: Bearer ${SLACK_TOKEN}
      retry:
        max: 3
        backoff: exponential

  daily_digest:
    schedule: "0 9 * * *"
    webhook:
      url: https://api.example.com/digest
```

### Custom SQL Functions

Expose arbitrary SQL queries as typed REST endpoints with parameter validation.

```yaml
functions:
  team_stats:
    description: Get team statistics
    method: GET
    query: |
      SELECT
        (SELECT COUNT(*) FROM todos WHERE team_id = $1) AS todo_count,
        (SELECT COUNT(*) FROM todos WHERE team_id = $1 AND status = 'done') AS completed_count,
        (SELECT COUNT(*) FROM team_members WHERE team_id = $1) AS member_count
    params:
      team_id: { type: bigint, required: true }
    returns:
      type: row
      schema:
        todo_count: bigint
        completed_count: bigint
        member_count: bigint
    auth_required: true
```

```sh
curl /rest/v1/rpc/team_stats?team_id=1
```

### Dashboard

A built-in admin dashboard for managing your config, browsing data, monitoring events, and previewing migrations.

- Visual table/field editor
- RLS policy builder
- SQL function editor with CodeMirror
- Event log with dead-letter queue
- Migration diff preview

### Observability

- OpenAPI 3.0 spec auto-generated at `/api/openapi.json`
- Prometheus-compatible metrics endpoint
- OpenTelemetry distributed tracing
- Structured JSON logging in production

## YAML Schema

Top-level sections in `ultrabase.yaml`:

```yaml
version: 1                     # Schema version
project:   { name, description }
extensions: [pgcrypto, ...]    # Postgres extensions
server:    { port, cors, timeouts, db, docs_ui }
providers: { email, storage }  # External service config
auth:      { jwt_expiry, refresh_tokens, fields, google, github }
tables:    { ... }             # Table definitions
storage:   { ... }             # File storage buckets
on:        { ... }             # Event triggers & cron
functions: { ... }             # Custom SQL endpoints
seeds:     { ... }             # Initial data
```

See [docs/example-ultrabase.yaml](docs/example-ultrabase.yaml) for a fully annotated example covering every section.

## Architecture

```
CLI (cobra)
     |
     v
+------------------------+
|   app/ (services)      |    Business logic & orchestration
+----------+-------------+
           | depends on
+----------v-------------+
|   domain/ (pure types  |    Interfaces & value objects
|   + port interfaces)   |    Zero external imports
+----------+-------------+
           | implemented by
+----------v-------------+
|   adapter/             |    Postgres, HTTP/Gin, S3,
|                        |    GCS, Resend, SendGrid
+------------------------+
           |
      PostgreSQL
     (WAL + RLS)
```

- **PostgreSQL only** -- no database abstraction layer, direct pgx usage
- **WAL-based CDC** -- logical replication feeds the event system
- **RLS as access control** -- all authorization via Postgres policies
- **Single binary** -- ships as a standalone Go binary or Docker image
- **Signed URL storage** -- never proxies file bytes

## Configuration

Environment variables:

| Variable | Required | Description |
|---|---|---|
| `ULTRABASE_OWNER_DATABASE_URL` | Yes | Privileged login (migrations, seeding, replication). Role needs `CREATEROLE`, `CREATEDB`, `BYPASSRLS`, `REPLICATION`. |
| `ULTRABASE_AUTH_DATABASE_URL` | Yes | Authenticator login for HTTP requests. `NOINHERIT`; ultrabase issues `SET LOCAL ROLE` per transaction (CRUD: from JWT; system endpoints: `service_role`). |
| `ULTRABASE_DB_AUTHENTICATOR_ROLE` | No | Override the authenticator role name (default: `authenticator`). |
| `ULTRABASE_DB_ANON_ROLE` | No | Override the anon role name (default: `anon`). |
| `ULTRABASE_DB_AUTHENTICATED_ROLE` | No | Override the authenticated role name (default: `authenticated`). |
| `ULTRABASE_DB_SERVICE_ROLE` | No | Override the service role name (default: `service_role`). |
| `JWT_SECRET` | Yes | Secret for signing JWTs |
| `ULTRABASE_ADMIN_KEY` | Yes | Admin API authentication key |
| `PORT` | No | Override server port (default: from YAML or 8080) |

## Contributing

Contributions are welcome. Please open an issue first to discuss what you'd like to change.

```sh
# Run tests
go test ./...

# Run with Docker
docker compose -f docker-compose.dev.yaml up
```

## License

[MIT](LICENSE)
