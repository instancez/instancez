# Ultrabase — Product Requirements Document

## Overview

Ultrabase is a declarative backend framework. Define your entire backend in a single YAML file, get a production-ready REST API with authentication, file storage, event-driven webhooks, and custom SQL functions. A single Go binary reads the YAML and produces a fully functional backend powered by PostgreSQL.

## Who It's For

- Solo developers and small teams who want a backend without writing backend code
- Agencies shipping multiple client projects with similar patterns
- Prototyping teams that need a real backend (not a mock) in minutes
- AI/LLM-powered tools generating full-stack applications from specs

## How It Differs

| | Ultrabase | Supabase | Firebase | Hasura |
|---|---|---|---|---|
| Config format | YAML (declarative) | Dashboard/SQL | Dashboard/JSON | Console/metadata |
| Query interface | PostgREST-compatible | PostgREST | Proprietary SDK | GraphQL |
| Auth | Built-in, YAML-configured | Built-in (GoTrue) | Built-in | External |
| Custom logic | SQL functions in YAML | Edge functions | Cloud functions | Actions/Events |
| Self-hosted | Single binary | Docker Compose (many services) | No | Docker |
| Events | WAL-based CDC | Realtime (WAL) | Cloud triggers | Event triggers |
| Vendor lock-in | None (YAML + Postgres) | Medium | High | Medium |
| Source of truth | YAML file | Database + dashboard | Dashboard | Metadata files |

**Core principle:** The YAML file is the source of truth. No hidden state, no dashboard-only config. Everything is version-controllable and LLM-readable.

---

## YAML Top-Level Sections

```yaml
version: 1                    # Schema version (framework compatibility)
project: { name, description } # Display metadata (logs, OpenAPI title)
extensions: [pgcrypto, ...]   # Postgres extensions to CREATE IF NOT EXISTS
server: { ... }               # Port, CORS, timeouts, DB pool, docs toggle
providers: { ... }            # Email + storage provider config
auth: { ... }                 # Auth methods (email, google, github), JWT, fields
tables: { ... }               # Table definitions (fields, RLS, indexes, search)
storage: { ... }              # File storage buckets with RLS
on: { ... }                   # Event triggers (WAL events + cron → webhooks, emails)
functions: { ... }            # Custom SQL queries as REST endpoints
seeds: { ... }                # Seed data keyed by table name
```

Each section has its own PRD (`01-tables.md` through `07-cli.md`).

---

## Architecture

### Hexagonal (Ports & Adapters)

```
                        CLI (cobra)
                           │
                           ▼
              ┌────────────────────────┐
              │      app/ (services)   │
              │  engine, crud, auth,   │
              │  migrate, storage,     │
              │  events, functions     │
              └──────────┬─────────────┘
                         │ depends on
              ┌──────────▼─────────────┐
              │   domain/ (pure types  │
              │   + port interfaces)   │
              └──────────┬─────────────┘
                         │ implemented by
              ┌──────────▼─────────────┐
              │   adapter/             │
              │  postgres/ (DB, RLS,   │
              │    WAL, migrations)    │
              │  http/ (handlers,      │
              │    middleware, OpenAPI) │
              │  s3/, resend/, ...     │
              │    (providers)         │
              └────────────────────────┘
                         │
                    PostgreSQL
                   (WAL + RLS)
```

- **domain/** — pure Go types and interfaces. Zero external imports.
- **app/** — use cases and orchestration. Depends only on domain/.
- **adapter/** — implementations for Postgres, HTTP, S3, email providers.
- **config/** — YAML loading and validation into domain types.
- **cli/** — cobra commands wiring everything together.

### Key Architectural Decisions

- **PostgreSQL only** — no database abstraction layer. Direct pgx usage.
- **WAL-based CDC** — logical replication slot feeds the event system. No polling, no outbox table.
- **RLS as the single access control system** — no HTTP-level RBAC. Postgres policies enforce all access.
- **PostgREST-compatible query interface** — filter, select, order, limit/offset, Prefer headers.
- **Single binary** — ships as a standalone Go binary and Docker image.
- **Signed URL storage** — Ultrabase never proxies file bytes. Clients upload/download directly to/from the storage provider.

### Request Flow

```
Client → HTTP adapter (gin)
  → JWT middleware (sets auth.uid(), auth.is_authenticated())
  → RLS context (SET LOCAL session vars)
  → PostgREST query builder (parse ?select, ?order, filters)
  → PostgreSQL (query with RLS enforced)
  → WAL captures change
  → Event dispatcher (webhooks, emails)
```

---

## Testing Strategy

- **Unit tests** — pure logic (YAML validation, query building, migration diffing). Table-driven, fast.
- **Integration tests** — real PostgreSQL via dockertest. RLS, migrations, CRUD, WAL. No DB mocks.
- **HTTP tests** — real handlers + test DB, actual HTTP requests.
- **Goal:** `go test ./...` in ~30s.

---

## V1 Scope

### Included

- YAML config loading and validation (single file)
- Table definitions with auto-generated CRUD (PostgREST-compatible)
- Row-Level Security (Postgres RLS policies from YAML)
- Auth: email/password, Google OAuth, GitHub OAuth
- JWT (HS256) with optional refresh tokens
- File storage via signed URLs (S3, GCS, MinIO)
- WAL-based event system with webhook + email actions
- Custom SQL functions as REST endpoints
- Auto-diff migrations
- Seed data
- OpenAPI 3.0 spec generation
- TypeScript, Python, Go SDK generation
- CLI: init, dev, serve, validate, generate, db tools
- Admin API (events, migrations, users, status)
- Prometheus metrics, OpenTelemetry tracing

### Deferred

- Roles / RBAC (RLS + auth.uid() covers v1 needs)
- Rate limiting (delegate to reverse proxy)
- Soft deletes, computed columns
- Cursor-based pagination
- Cookie-based sessions / CSRF
- Split YAML (directory mode)
- Plugin system
- MFA / passkeys
- Image transformations
- GraphQL
- Admin dashboard UI (beyond read-only schema view)
- Multi-database support

---

## Non-Goals

- **Not a frontend framework** — backend only. Use any frontend.
- **Not a hosting platform** — it's a runtime. Deploy it yourself.
- **Not a database abstraction** — PostgreSQL only, uses Postgres features directly.
- **Not extensible via plugins** — all features are compiled into the single binary.
- **Not a replacement for complex business logic** — for complex orchestration, use an application server that calls Ultrabase APIs.
- **Not a migration tool for existing databases** — it manages its own schema from YAML.
