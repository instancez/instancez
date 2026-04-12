# Functions — Feature PRD

## Overview

Functions expose custom SQL queries as typed REST endpoints. Unlike auto-generated CRUD, functions give full control over the SQL while providing parameter validation, typed return schemas, RLS enforcement, and SDK type generation.

**Status:** Confirmed as a feature. Detailed design for some aspects is deferred (marked TBD below).

---

## YAML Syntax

```yaml
functions:
  user_stats:
    description: "Get aggregated stats for a user"
    method: GET
    query: |
      SELECT
        COUNT(*) FILTER (WHERE status = 'pending')::integer as pending,
        COUNT(*) FILTER (WHERE status = 'done')::integer as completed,
        COUNT(*)::integer as total
      FROM todos
      WHERE user_id = $1
    params:
      user_id: { type: integer, required: true }
    returns:
      type: row
      schema:
        pending: integer
        completed: integer
        total: integer
    auth_required: true

  bulk_complete:
    description: "Mark multiple todos as done"
    method: PUT
    query: |
      UPDATE todos
      SET status = 'done', done_at = NOW()
      WHERE id = ANY($1) AND user_id = $2
      RETURNING id, title, status
    params:
      todo_ids: { type: "integer[]", required: true }
      user_id: { type: integer, required: true }
    returns:
      type: rows
      schema:
        id: integer
        title: text
        status: text
    auth_required: true

  cleanup_old:
    description: "Delete completed todos older than 90 days"
    method: DELETE
    query: "DELETE FROM todos WHERE status = 'done' AND created_at < NOW() - INTERVAL '90 days'"
    returns:
      type: void
    auth_required: true
```

---

## Endpoint

All functions are mounted at:

```
GET|POST|PUT|DELETE /api/functions/{function_name}
```

- `GET` — params via query string
- `POST` / `PUT` / `DELETE` — params via JSON body

Default method: `POST`.

---

## Return Types

| Type | Description | Response Shape |
|---|---|---|
| `rows` | Zero or more records | `[{...}, {...}]` |
| `row` | Single record or null | `{...}` or `null` |
| `scalar` | Single value | `{ "count": 42 }` |
| `void` | No return value | `{ "affected_rows": 5 }` |

### Schema Types

The `schema` maps column names to **Postgres types**:

```yaml
returns:
  type: rows
  schema:
    id: integer
    title: text
    done: boolean
    created_at: timestamptz
    author: text
```

---

## Parameter System

### Parameter Types

Parameters use Postgres type names: `integer`, `bigint`, `text`, `boolean`, `date`, `timestamptz`, `uuid`, `integer[]`, `text[]`, etc.

### Parameter Options

```yaml
params:
  user_id:
    type: integer
    required: true
  status:
    type: text
    enum: [pending, active, done]
    default: pending
  limit:
    type: integer
    default: 20
    min: 1
    max: 100
```

| Option | Description |
|---|---|
| `type` | Postgres type name |
| `required` | Must be provided (no default) |
| `default` | Used if not provided |
| `enum` | Restricted values |
| `min` / `max` | Numeric range |

### Placeholder Mapping

Parameters map to `$1`, `$2`, ... in declaration order. Param count must match the number of `$N` placeholders in the query.

---

## Auth & RLS

```yaml
functions:
  my_function:
    auth_required: true        # default: false
```

- `auth_required: false` — anyone can call this function
- `auth_required: true` — requires a valid JWT

No `allowed_roles` — roles are dropped in v1.

### RLS Enforcement

Functions execute within an RLS context. Session variables are set before execution:

```sql
BEGIN;
SET LOCAL app.user_id = '42';
SET LOCAL app.is_authenticated = 'true';
-- execute function query --
COMMIT;
```

PostgreSQL RLS policies on the tables used in the query are enforced. Functions cannot bypass table-level security unless called with the admin key.

---

## SQL Safety

1. **Prepared statements only** — parameters are always passed as `$N` placeholders, never interpolated.
2. **SQL allowlist** — only `SELECT`, `INSERT`, `UPDATE`, `DELETE`, `WITH` (CTE) are allowed. DDL statements (`DROP`, `CREATE`, `ALTER`, `TRUNCATE`) are rejected at schema validation time.
3. **No dynamic SQL** — the query is fixed at schema load time.
4. **Single statement** — each function is exactly one SQL statement (with CTEs). Semicolons between statements are rejected.

---

## Schema Validation (Boot-Time)

1. SQL parsed for syntax errors (without executing)
2. Number of `$N` placeholders matches number of params
3. `$1`, `$2`, ... must correspond to param definition order
4. `returns` block is required on every function
5. Function names must be valid identifiers (no reserved words)
6. SQL allowlist enforced (no DDL)

---

## Errors

```json
{
  "type": "https://ultrabase.dev/errors/validation",
  "title": "Parameter validation failed",
  "status": 422,
  "invalid_params": [
    { "name": "limit", "reason": "must be between 1 and 100" },
    { "name": "status", "reason": "must be one of: pending, active, done" }
  ]
}
```

---

## TBD — Deferred Design Decisions

The following are confirmed as future capabilities but detailed design is not yet finalized:

- **Caching** — response caching per function (cache key = function name + param values). TTL, invalidation strategy, storage backend TBD.
- **`auto_inject` replacement** — how functions access the current user's ID without client sending it. May use a special `$current_user` placeholder or require client to send it (RLS enforces correctness). TBD.
- **`$ref:table_name`** — shorthand for return schemas that match a table's columns. Whether to keep this TBD.
- **OpenAPI generation** — each function will generate an OpenAPI operation. Exact mapping of param types to OpenAPI schema types TBD.
- **SDK generation** — typed function clients (e.g., `client.functions.userStats({ user_id: 42 })`). Exact generation strategy TBD.
- **Timeout** — per-function timeout configuration. Default TBD.
- **Max rows** — per-function result size cap. Default TBD.
- **Programmatic functions** — WASM or container-based functions (future, not v1).

---

## Edge Cases

1. **Empty results:** `rows` returns `[]`, `row` returns `null`, `scalar` returns `null`. These are not errors.
2. **Multiple statements:** Not allowed. Rejected at validation time.
3. **Transaction isolation:** Each function call runs in its own transaction.
4. **Naming conflicts:** Function names cannot conflict with table names or reserved words.
