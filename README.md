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
  <a href="docs/functions.md">Code Functions</a> &middot;
  <a href="docs/">Documentation</a>
</p>

---

Ultrabase is a declarative backend framework. A single YAML file defines your tables, authentication, file storage, and custom SQL functions. A single Go binary reads that file and produces a fully functional REST API powered by PostgreSQL.

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
```

```sh
ultra dev
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

# Scaffold project files (no DB calls).
./ultra init

export JWT_SECRET="your-secret"
export ULTRABASE_ADMIN_KEY="your-admin-key"

# 1. point ultra at a superuser DSN
export ULTRABASE_DATABASE_URL=postgres://postgres:postgres@localhost:5432/postgres
# 2. dev provisions roles on first run
./ultra dev
```

### CLI Commands

```sh
ultra init                           # Scaffold project (no DB calls)
ultra dev                            # Start dev server with hot-reload
ultra serve                          # Production mode (reads .production.env)
ultra validate                       # YAML syntax check, no DB
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

### Custom SQL Functions (rpc:)

Expose PostgreSQL stored procedures as typed REST endpoints, compatible with `supabase-js .rpc()`.

```yaml
rpc:
  team_stats:
    description: Get team statistics
    auth_required: true
    language: plpgsql
    volatility: stable
    args:
      - name: team_id
        type: bigint
        required: true
    returns:
      type: "table(todo_count bigint, completed_count bigint, member_count bigint)"
    body: |
      BEGIN
        RETURN QUERY
        SELECT
          (SELECT COUNT(*) FROM todos WHERE team_id = team_stats.team_id),
          (SELECT COUNT(*) FROM todos WHERE team_id = team_stats.team_id AND status = 'done'),
          (SELECT COUNT(*) FROM team_members WHERE team_id = team_stats.team_id);
      END;
```

```sh
curl /rest/v1/rpc/team_stats?team_id=1
```

### Code Functions (functions:)

Write arbitrary JavaScript handlers served at `/functions/v1/<name>`, invocable from `supabase-js` via `functions.invoke()`.

```yaml
functions:
  hello:
    runtime: node
    file: functions/hello.js
    timeout: 30s
    env:
      API_KEY: "${ULTRA_ENV_MY_API_KEY}"
```

```js
// functions/hello.js
export default async function handler(req, ctx) {
  const name = req.body?.name ?? "world";
  return { status: 200, body: { hello: name } };
}
```

```sh
curl -X POST /functions/v1/hello -d '{"name": "ultrabase"}'
# → {"hello":"ultrabase"}
```

The `ctx` argument provides: `supabase` (caller-RLS client), `serviceClient` (service_role), `claims` (JWT claims or null for anonymous callers), `env` (resolved secrets), `log`, and `signal` (AbortSignal). Secrets are resolved from the `ULTRA_ENV_*` namespace at startup and never written to the worker's process environment. Set `auth_required: true` on a function to have Ultrabase return 401 for unauthenticated callers before the handler is invoked; handlers can still inspect `ctx.claims` for finer-grained authorization.

### Dashboard

A built-in admin dashboard for managing your config, browsing data, and previewing migrations.

- Visual table/field editor
- RLS policy builder
- SQL function editor with CodeMirror
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
rpc:       { ... }             # Custom Postgres stored procedures (/rest/v1/rpc/<name>)
functions: { ... }             # Code functions — JS handlers (/functions/v1/<name>)
seeds:     { ... }             # Initial data
```

See [docs/examples/react-catalog](docs/examples/react-catalog) for a complete, runnable example (`docker compose up`) covering tables, RLS, storage, auth, and code functions.

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
| `ULTRABASE_DATABASE_URL` | No (dev only) | Superuser/privileged DSN. `ultra dev` provisions the role layout from it and writes the derived owner/authenticator DSNs to `.development.env`. Not used by `serve`. |
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
