---
title: SQL Functions
description: Call Postgres stored procedures at /rest/v1/rpc/<name>.
---

`rpc:` declares Postgres stored procedures called via HTTP. This is distinct from `functions:`, which declares JavaScript [code functions](/instancez/build/functions/) served at `/functions/v1/<name>`.

## Declaring

```yaml
rpc:
  team_stats:
    description: Get team statistics
    auth_required: true
    language: sql
    volatility: stable      # stable | volatile | immutable
    security: invoker       # invoker | definer
    set: { search_path: "" }
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
      SELECT count(*) AS total FROM public.todos WHERE team_id = team_stats.team_id
```

| Field | Required | Description |
|-------|----------|-------------|
| `language` | no | `sql` or `plpgsql` (default: `plpgsql`) |
| `volatility` | no | `volatile` (default), `stable`, or `immutable` |
| `security` | no | `invoker` (default) or `definer` |
| `auth_required` | no | Reject unauthenticated callers when `true` |
| `set` | no | Map of `search_path`, `statement_timeout`, `lock_timeout`, `work_mem`, emitted as `SET` on the function. |
| `args` | no | Ordered list of named arguments |
| `args[].required` | no | Return 400 if the argument is absent |
| `args[].default` | no | Postgres DEFAULT value for optional args |
| `returns.type` | yes | Return type; `void` emits 204 No Content. One of: `void`, `record`, a scalar or composite type (`int`, `text`, `uuid`, a table's row type, …), `setof <type>`, or `table(col type, …)`. In the dashboard this is a dropdown of the common scalar types plus `void`/`record`, or `Custom…` to type `setof`/`table(...)`/a composite type directly into the generated `RETURNS` line. |

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

setof results accept the same `select` as tables, including aggregates next to embeds: `select=status,count(),messages(id)` groups by the embed. `count=exact` counts the ungrouped rows.

Pass the entire body as a single `jsonb` argument using `Prefer: params=single-object`:

```http
POST /rest/v1/rpc/process_payload
Prefer: params=single-object
Content-Type: application/json

{"key": "value", "nested": {"x": 1}}
```

## Calling via a Supabase client

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

With `security: definer`, set `search_path` (`""` plus schema-qualified names is safest); `inz validate` warns otherwise.

## Settings

`set:` pins settings on `CREATE FUNCTION`. A change or removal applies on the next migration.

| Key | Accepts |
|-----|---------|
| `search_path` | Comma-separated lowercase schema names or `"$user"`. `""`, or a bare `search_path:` with no value, sets an empty path. |
| `statement_timeout`, `lock_timeout` | Milliseconds, or a number with `ms`, `s`, `min`, `h` or `d`, up to 2147483647 ms (24 days). `0` disables. |
| `work_mem` | kB, or a number with `kB`, `MB`, `GB` or `TB`, from 64kB to 2147483647 kB (under 2TB). |

Values outside those ranges fail `inz validate`.

## Policies that call an RPC

An RLS policy can call a function declared here (`using: "public.can_see(id)"`). instancez creates RPCs before policies. Dropping an RPC or changing its arguments first drops the instancez-managed policies that call it and recreates them afterwards, without `CASCADE`. A policy created outside `instancez.yaml` that still calls it stops the migration with `rpc <name> is still used by policy <policy> on <schema>.<table>`.
