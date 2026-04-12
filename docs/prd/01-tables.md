# Tables — Feature PRD

## Overview

Tables are the core data modeling primitive in Ultrabase. Each table definition in YAML produces:
- A PostgreSQL table with auto-diff migrations
- PostgREST-compatible CRUD REST endpoints
- Row-Level Security policies
- Postgres CHECK constraint validation
- Full-text search indexes
- OpenAPI documentation
- Typed SDK definitions

---

## YAML Syntax

```yaml
tables:
  todos:
    fields:
      id: { type: bigserial, primary_key: true }
      team_id:
        foreign_key:
          references: teams.id
          on_delete: cascade
      user_id:
        foreign_key:
          references: users.id
          on_delete: cascade
      title: { type: text, required: true }
      body: { type: text }
      status: { type: text, required: true, enum: [pending, active, done], default: pending }
      priority: { type: integer, required: true, default: 0, min: 0, max: 5 }
      due_date: { type: date }
      created_at: { type: timestamptz, required: true, default: now() }

    indexes:
      - { columns: [team_id, status] }
      - { columns: [user_id, created_at] }
      - { columns: [due_date], where: "status != 'done'" }

    searchable: [title, body]
    search_config: english

    allow_anon: false   # default; set true for public tables

    rls:
      - operations: [select]
        check: "team_id IN (SELECT team_id FROM team_members WHERE user_id = auth.uid())"
      - operations: [insert]
        check: "user_id = auth.uid()"
      - operations: [update, delete]
        check: "user_id = auth.uid()"
```

---

## Types

Types are **raw PostgreSQL type names**. No abstract aliases.

| Postgres Type | Example | Notes |
|---|---|---|
| `bigserial` | `id: { type: bigserial, primary_key: true }` | Auto-incrementing PK |
| `uuid` | `id: { type: uuid, primary_key: true, default: uuid_v7() }` | UUID PK |
| `text` | `title: { type: text, required: true }` | Unbounded string |
| `varchar(N)` | `slug: { type: varchar(63) }` | Length in the type |
| `integer` | `priority: { type: integer, min: 0, max: 10 }` | 32-bit signed |
| `bigint` | `count: { type: bigint }` | 64-bit signed |
| `boolean` | `done: { type: boolean, default: false }` | true/false |
| `numeric(P,S)` | `price: { type: numeric(10,2) }` | Precision in the type |
| `timestamptz` | `created_at: { type: timestamptz, default: now() }` | Timestamp with timezone |
| `date` | `due_date: { type: date }` | ISO 8601 date |
| `time` | `start_time: { type: time }` | HH:MM:SS |
| `jsonb` | `metadata: { type: jsonb }` | JSON binary |
| `text[]` | `tags: { type: "text[]" }` | Array types |
| `integer[]` | `scores: { type: "integer[]" }` | Array types |
| `inet` | `ip: { type: inet }` | Network address |
| `interval` | `duration: { type: interval }` | Time interval |

Length, precision, and scale are encoded in the type itself (`varchar(255)`, `numeric(10,2)`) — no separate `max_length` or `precision` fields.

---

## Field Options

| Option | Type | Default | Description |
|---|---|---|---|
| `type` | string | — | Postgres type name. Omit for FK columns (inferred). |
| `primary_key` | bool | false | Exactly one per table. |
| `required` | bool | false | NOT NULL + must be present on create. |
| `unique` | bool | false | UNIQUE constraint (does NOT auto-create index). |
| `default` | any | — | Literal or allowlisted SQL function. |
| `foreign_key` | object | — | FK reference (see Foreign Keys). |
| `enum` | string[] | — | CHECK constraint: `CHECK (col IN (...))`. |
| `pattern` | string | — | CHECK constraint: `CHECK (col ~ '...')`. |
| `min` / `max` | number | — | CHECK constraint: `CHECK (col >= N)`. |
| `check` | string | — | Raw CHECK expression escape hatch. |
| `ref` | string | — | Storage reference hint: `ref: storage.avatars`. |
| `on_delete` | string | keep | For `ref` fields: `cascade` or `keep`. |

### Nullability

Fields are **nullable by default**. Use `required: true` for NOT NULL. There is no separate `nullable` option.

### Defaults

`default:` accepts:
- Literals: strings, integers, booleans, floats (`default: 0`, `default: false`, `default: pending`)
- Allowlisted SQL functions: `now()`, `uuid_v7()`, `uuid_v4()`, `current_date`, `current_time`

String and enum defaults need no SQL quoting — `default: pending` just works. The framework handles quoting in DDL generation.

### No Auto Columns

The framework does **not** auto-add any columns — not even `id`. Users explicitly declare every column including the primary key.

---

## Foreign Keys

Long form only. The FK column's type is **inferred from the referenced column** (no `type:` needed):

```yaml
fields:
  category_id:
    foreign_key:
      references: categories.id
      on_delete: cascade       # cascade | restrict | set_null
```

- `on_delete` defaults to `restrict` if omitted
- Boot-time validation rejects FKs to non-existent tables/columns
- Two FKs to the same table from one table = validation error (ambiguous relation names); user must add explicit `relations:` block to disambiguate

---

## Indexes

Declared at table level via `indexes:` array. Single form only — objects with `columns` plus optional flags:

```yaml
indexes:
  - { columns: [user_id, created_at] }              # composite btree
  - { columns: [email], unique: true }               # unique index
  - { columns: [due_date], where: "status != 'done'" }  # partial index
```

No field-level `index: true` shorthand. Column-level `unique: true` creates a UNIQUE constraint but does NOT auto-create an index entry — add one to `indexes:` if needed.

---

## Full-Text Search

Declarative per-table:

```yaml
tables:
  todos:
    searchable: [title, body]
    search_config: english        # defaults to english
```

- Framework creates a generated tsvector column and GIN index on migration
- Query via PostgREST full-text operators: `?tsv=plfts.search+text`
- `plfts` = plainto_tsquery (raw text, AND-joined, zero injection surface)
- Other operators available: `fts` (to_tsquery), `phfts` (phraseto_tsquery), `wfts` (websearch_to_tsquery)
- NULLable searchable columns: COALESCE to empty string in the generated tsvector expression

---

## Row-Level Security (RLS)

### How It Works

RLS policies in YAML compile to PostgreSQL RLS policies. Each request sets session variables via `SET LOCAL`:

```sql
BEGIN;
SET LOCAL app.user_id = '42';
SET LOCAL app.is_authenticated = 'true';
-- execute query --
COMMIT;
```

### Helper Functions

| Function | Returns | Maps to |
|---|---|---|
| `auth.uid()` | `BIGINT` | `current_setting('app.user_id', true)::bigint` |
| `auth.is_authenticated()` | `BOOLEAN` | `current_setting('app.is_authenticated', true)::boolean` |

No `auth.role()` — roles are dropped in v1.

### Policy Definition

```yaml
rls:
  - operations: [select, update, delete]
    check: "user_id = auth.uid()"
  - operations: [insert]
    check: "auth.is_authenticated()"
```

- Multiple policies on the same operation are **OR'd** (any match grants access)
- `check` supports any SQL expression including subqueries and joins
- No RLS section on a table = no policies enabled, open access (Postgres default)
- `allow_anon: true` on a table allows unauthenticated requests (RLS alone decides access)
- Admin key (`ULTRABASE_ADMIN_KEY`) always bypasses RLS

---

## Auto-Generated REST API (PostgREST-Compatible)

For a table `todos`, the following endpoints are created:

### List — `GET /api/todos`

**Filters** (column names as query params):
```
?status=eq.active                     # equality
?priority=gte.5                       # greater than or equal
?status=in.(pending,active)           # IN list
?title=ilike.*task*                   # case-insensitive LIKE
?metadata->>theme=eq.dark            # JSONB access
?tags=cs.{urgent}                     # array contains
```

Operators: `eq`, `neq`, `gt`, `gte`, `lt`, `lte`, `like`, `ilike`, `in`, `is`, `cs` (contains), `cd` (contained by), `fts`, `plfts`, `phfts`, `wfts`.

**Field selection and embedding:**
```
?select=id,title,status                       # sparse fieldsets
?select=*,author(id,name)                     # embed relation with column picking
?select=*,author(*,company(*))                # nested embeds
```

Default: `?select=*` (all columns, no embeds).

**Sorting:**
```
?order=created_at.desc,title.asc
```

**Pagination:**
```
?limit=20&offset=0
```

Default limit: 20. Max limit: 100 (configurable in `server:` block).

**Count** (opt-in via request header):
```
Prefer: count=exact       # runs COUNT(*)
Prefer: count=planned     # pg planner estimate
Prefer: count=estimated   # pg_class.reltuples
```

Response includes `Content-Range` header: `Content-Range: 0-19/42` (or `0-19/*` without count).

**Response:** Raw JSON array (no envelope):
```json
[
  { "id": 1, "title": "Task", "status": "active", "author": { "id": 7, "name": "Jane" } },
  { "id": 2, "title": "Other", "status": "pending" }
]
```

### Get Single — `GET /api/todos?id=eq.42`

Use filter + `Prefer: return=representation` with `Accept: application/vnd.pgrst.object+json` for a single object response. Returns 404 if not found or RLS denies access.

No `/:id` path parameter — PostgREST style uses filters.

### Create — `POST /api/todos`

```json
{ "title": "New task", "status": "active", "user_id": 42 }
```

Default response: `201 Created` with no body (Prefer: return=minimal).
With `Prefer: return=representation`: returns the created record.

**Bulk insert:** POST with array body `[{...}, {...}]` — single transaction.

**Unknown fields in body → 400 Bad Request** listing the unknown field names.

### Update — `PATCH /api/todos?id=eq.42`

PATCH only (no PUT). Partial update — only provided keys change.
- `null` in body = set column to NULL (400 if non-nullable)
- Omitted key = leave unchanged

**Bulk update:** PATCH with filters — updates all matching rows in one transaction.

### Delete — `DELETE /api/todos?id=eq.42`

Response: `204 No Content`.

**Bulk delete:** DELETE with filters — deletes all matching rows.

### Write Response Control

Controlled by `Prefer` header:
- `return=minimal` (default) — no body
- `return=representation` — full record(s) in response
- `return=headers-only` — Location header only

---

## Relations & Embedding

### Inference

Relations are inferred from foreign keys by stripping the `_id` suffix:
- `user_id FK → users.id` → embed name: `user`
- `category_id FK → categories.id` → embed name: `category`

Both directions work:
- **Belongs-to:** `?select=*,user(*)` on todos (forward FK)
- **Has-many:** `?select=*,todos(*)` on users (reverse FK, nested as array)

### Behavior

- Execution: LEFT JOIN in main query
- Depth: unlimited (`?select=*,author(*,company(*,industry(*)))`)
- Column-picking: `?select=*,author(id,name)`
- Has-many: no default limit on nested collection size
- RLS enforced on included relations (JOIN runs in same RLS context)
- Unknown embed name → 400 with suggestion (e.g., `"Did you mean 'author'?"`)
- Two FKs to same table → boot-time validation error; disambiguate with `relations:` block

---

## Migrations

### Auto-Diff

On startup (or via `--migrate` flag):
1. Compute current YAML schema
2. Compare against `_ultrabase_migrations` history table (`{ id, checksum, sql, applied_at }`)
3. If changed, generate diff and apply migration

### Supported Changes

| Change | Automatic | Notes |
|---|---|---|
| Add table | Yes | CREATE TABLE |
| Drop table | No | Requires `--allow-destructive` |
| Add column | Yes | ALTER TABLE ADD COLUMN |
| Drop column | No | Requires `--allow-destructive` |
| Change column type | Partial | Safe casts only (int→bigint, varchar→text) |
| Add/remove index | Yes | CREATE/DROP INDEX CONCURRENTLY |
| Change default | Yes | ALTER COLUMN SET DEFAULT |
| Add/remove NOT NULL | Yes | With data validation |
| Add/modify RLS policy | Yes | CREATE/ALTER POLICY |
| Change enum values | Add only | Removing requires `--allow-destructive` |

### No Rollback Command

YAML is the source of truth. To revert, edit the YAML back to the desired state — the framework diffs and generates the reverse migration automatically.

### Destructive Operations

`DROP TABLE`, `DROP COLUMN`, and similar operations are blocked by default. Pass `--allow-destructive` to permit them. In `dev` mode, destructive ops prompt y/N interactively.

---

## Seeds

Seeds are declared at the **top level** (not per-table):

```yaml
seeds:
  users:
    - email: admin@example.com
      password: secret123
      display_name: Admin
      email_verified: true

  teams:
    - id: 1
      name: Acme Corp
      slug: acme
```

- Idempotent by primary key (upsert semantics)
- Applied via `ultrabase dev` (auto) or `ultrabase serve --seed` (opt-in)
- Run in table dependency order (respects foreign keys)
- `seeds.users` is special: framework hashes the `password` field automatically

---

## Table Naming

- Any valid PostgreSQL identifier (user's choice of casing/style)
- **Reserved names:** `users` (auth table) and any name starting with `_` (framework tables)
- Framework tables: `_objects`, `_events`, `_ultrabase_migrations`, `_user_identities`, etc.

---

## Errors

All errors follow RFC 7807 Problem+JSON format:

```json
{
  "type": "https://ultrabase.dev/errors/validation",
  "title": "Validation failed",
  "status": 422,
  "invalid_params": [
    { "name": "priority", "reason": "must be between 0 and 5" },
    { "name": "status", "reason": "must be one of: pending, active, done" }
  ]
}
```

### HTTP Status Mapping

| Status | Meaning |
|---|---|
| 400 | Malformed request, unknown fields, invalid query params |
| 401 | Missing/invalid JWT on auth-required endpoint |
| 403 | RLS denied (authenticated but not allowed) |
| 404 | Resource not found |
| 409 | Unique constraint violation |
| 422 | FK violation, CHECK constraint, enum violation |

---

## Schema Validation (Boot-Time)

- Every table has exactly one primary key
- No duplicate field names
- Valid Postgres types
- Foreign key targets exist
- Enum values are non-empty
- RLS expressions parse correctly (SQL syntax check)
- Index columns exist in the table
- `searchable` columns exist and are text-compatible
- `ref: storage.<bucket>` points to a declared bucket
- Circular RLS references detected and rejected
- All errors collected and reported as a single list (no "first error wins")
