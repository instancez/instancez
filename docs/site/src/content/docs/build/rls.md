---
title: RLS Policies
description: Row-level security is the only authorization layer in instancez. All access decisions are Postgres policies declared in instancez.yaml.
---

instancez has no HTTP-level RBAC. Every access decision is a Postgres row-level security (RLS) policy declared in `instancez.yaml` under the table's `rls:` block. The HTTP middleware validates the JWT and issues `SET LOCAL ROLE` to the correct Postgres role; from there, Postgres enforces the policies. There is no application-side role table and no separate permission system to synchronize.

## Policy syntax

Each entry in `rls:` is a policy object with two required fields:

- `operations` — a list of one or more of `select`, `insert`, `update`, `delete`
- `check` — a SQL expression evaluated per row

An optional `type` field accepts `permissive` (default) or `restrictive`. Multiple permissive policies on the same table and operation combine with OR — a row passes if any policy allows it. A restrictive policy additionally narrows the result with AND — the row must also satisfy every restrictive policy.

```yaml
tables:
  posts:
    fields:
      - name: id
        type: bigserial
        primary_key: true
      - name: user_id
        type: uuid
        required: true
      - name: body
        type: text
        required: true
    rls:
      # Anyone can read
      - operations: [select]
        check: "true"
      # Only the owner can write
      - operations: [insert, update, delete]
        check: "auth.uid() = user_id"
```

When a table has at least one `rls:` entry, instancez emits `ALTER TABLE ... ENABLE ROW LEVEL SECURITY` and `FORCE ROW LEVEL SECURITY`. Tables with no `rls:` block have RLS disabled — all rows are visible to all roles.

For `insert` operations, instancez uses `WITH CHECK`. For all other operations it uses `USING`. This matches standard Postgres semantics.

## auth.uid() and auth.is\_authenticated()

instancez installs these helper functions in the `auth` schema at startup. They read session variables set by the request middleware, not application memory.

| Function | Return type | Returns non-null when |
|---|---|---|
| `auth.uid()` | `uuid` | Request carries a valid JWT with a `sub` claim (i.e. a signed-in user). Returns `NULL` for `anon` requests and for `service_role` tokens. |
| `auth.role()` | `text` | Always returns a value: `'anon'`, `'authenticated'`, or `'service_role'`. |
| `auth.email()` | `text` | Request carries a JWT with an `email` claim. |
| `auth.jwt()` | `jsonb` | Request carries any JWT. Returns the full decoded payload. |
| `auth.is_authenticated()` | `boolean` | Role is `authenticated` or `service_role`. Returns `false` for `anon`. |

`auth.uid()` is the right function for owner-scoped policies. `auth.is_authenticated()` is useful as a simpler signed-in-only gate. The underlying implementation reads session GUCs (`app.user_id`, `app.role`, etc.) set at the start of every request transaction.

## Common patterns

### Public read, owner write

Anyone can read; only the row's owner can modify it.

```yaml
rls:
  - operations: [select]
    check: "true"
  - operations: [insert, update, delete]
    check: "auth.uid() = user_id"
```

### Signed-in only

Any authenticated user can access the table; anonymous requests cannot.

```yaml
rls:
  - operations: [select, insert, update, delete]
    check: "auth.is_authenticated()"
```

### Private (owner only)

Only the row's owner can see or modify it.

```yaml
rls:
  - operations: [select, insert, update, delete]
    check: "auth.uid() = user_id"
```

### Admin bypass via service role

The `service_role` has `BYPASSRLS` in Postgres — it skips all policies. Requests made with the admin key are automatically assigned `service_role`, so they see every row regardless of any `check` expression. This applies both to the REST API (when the caller passes the service-role JWT or the `apikey` header that maps to the admin key) and to code functions that use the backend client.

In code functions, use `ctx.serviceClient` to get a client that runs as `service_role`:

```js
export default async function handler(ctx) {
  // Bypasses RLS — use only for trusted server-side logic.
  const { data } = await ctx.serviceClient.from('posts').select('*');
  return Response.json(data);
}
```

Use `ctx.client` (or `ctx.userClient`) for operations that should respect RLS and run as the calling user.

## How roles are assigned

The middleware maps each request to one of three Postgres roles based on the JWT.

| JWT `role` claim | Postgres role (default name) | BYPASSRLS |
|---|---|---|
| `anon` (no token, or token without `sub`) | `anon` | No |
| `authenticated` (valid token with `sub`) | `authenticated` | No |
| `service_role` (admin key or service JWT) | `service_role` | Yes |

The Postgres role names default to the values in the table above, matching Supabase. They are configurable via `INSTANCEZ_DB_ANON_ROLE`, `INSTANCEZ_DB_AUTHENTICATED_ROLE`, and `INSTANCEZ_DB_SERVICE_ROLE` environment variables — but the JWT claim values (`anon`, `authenticated`, `service_role`) are fixed and cannot be changed. They are part of the Supabase wire format.

The request pool logs in as the `authenticator` role, which is `NOINHERIT`. Without an explicit `SET LOCAL ROLE`, it carries no table privileges. Every request transaction starts by issuing `SET LOCAL ROLE` to the appropriate role, then runs the query, so the role is always correct for the lifetime of that transaction.

## What's next

- [Auth](/build/auth) — how users sign up and get JWTs
- [Schema](/build/schema) — table and column definitions
- [Storage](/build/storage) — per-bucket RLS policies
- [Querying](/build/querying) — filtering and embedding from the client
