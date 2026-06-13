---
title: RPC
description: Call Postgres stored procedures at /rest/v1/rpc/<name>.
---

`rpc:` declares Postgres stored procedures. This is distinct from `functions:`, which declares JavaScript code functions served at `/functions/v1/<name>`.

## Declaring

```yaml
rpc:
  team_stats:
    description: Get team statistics
    auth_required: true
    language: sql
    volatility: stable      # stable | volatile | immutable
    security: invoker       # invoker | definer
    args:
      - name: team_id
        type: bigint
        required: true
      - name: limit_rows
        type: integer
        default: 10
    returns:
      type: record           # scalar type, record, setof <table>, void, etc.
    body: |
      SELECT count(*) AS total FROM todos WHERE team_id = team_stats.team_id
```

| Field | Required | Description |
|-------|----------|-------------|
| `language` | no | `sql` or `plpgsql` (default: `sql`) |
| `volatility` | no | `volatile` (default), `stable`, or `immutable` |
| `security` | no | `invoker` (default) or `definer` |
| `auth_required` | no | Reject unauthenticated callers when `true` |
| `args` | no | Ordered list of named arguments |
| `args[].required` | no | Return 400 if the argument is absent |
| `args[].default` | no | Postgres DEFAULT value for optional args |
| `returns.type` | yes | Return type; `void` emits 204 No Content |

## Calling via HTTP

```http
POST /rest/v1/rpc/team_stats
Content-Type: application/json
Authorization: Bearer <jwt>

{"team_id": 42}
```

Stable and immutable functions may also be called with `GET`:

```
GET /rest/v1/rpc/team_stats?team_id=42
```

Volatile functions reject `GET` with HTTP 405.

Pass the entire body as a single `jsonb` argument using `Prefer: params=single-object`:

```http
POST /rest/v1/rpc/process_payload
Prefer: params=single-object
Content-Type: application/json

{"key": "value", "nested": {"x": 1}}
```

## Calling via supabase-js

```ts
const { data, error } = await supabase.rpc('team_stats', { team_id: 42 })
```

For setof functions, filter/order/limit can be chained:

```ts
const { data, error } = await supabase
  .rpc('search_todos', { query: 'milk' })
  .eq('done', false)
  .order('created_at', { ascending: false })
  .limit(10)
```

## Arguments

Arguments are passed as a JSON object in the request body (POST) or as query parameters (GET). Each key must match a declared arg name exactly; unknown keys are rejected with HTTP 400.

Values are passed to Postgres as typed bind parameters — they are never concatenated into SQL.

Required arguments (`required: true`) that are missing cause a 400 error. Optional arguments with a `default` receive the Postgres DEFAULT when omitted.

## Roles

The function runs under the same Postgres role as any other request: `anon` for anonymous, `authenticated` for signed-in users, `service_role` for admin-key requests. RLS applies inside the function body unless `security: definer` is set, in which case the function runs as its owner and must manage access explicitly.
