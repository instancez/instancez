---
title: RLS Policies
description: Row-level security is the only authorization layer in instancez. All access decisions are Postgres policies declared in instancez.yaml.
---

instancez has no HTTP-level RBAC. Every access decision is a Postgres row-level security (RLS) policy declared in `instancez.yaml` under the table's `rls:` block. The HTTP middleware validates the JWT and issues `SET LOCAL ROLE` to the correct Postgres role; from there, Postgres enforces the policies. There is no application-side role table and no separate permission system to synchronize.

## Policy syntax

Each entry in `rls:` is a policy object with these fields:

- `operations` — either exactly one of `select`, `insert`, `update`, `delete`, or all four together. Partial combinations (e.g. `[insert, update]`) are rejected.
- `using` — a SQL boolean expression that decides which existing rows the operation can see or target. Read by `select`, `update`, `delete`.
- `with_check` — a SQL boolean expression that decides what a written row is allowed to look like. Read by `insert`, `update`.
- at least one of `using`/`with_check` is required, whichever your operations read. `select`/`delete` have no `with_check` fallback and `insert` has no `using` fallback, so those always need the matching field directly. For `update` alone, the two fields aren't symmetric. Setting only `using` gets `with_check` auto-filled with the same expression, for both permissive and restrictive policies. Setting only `with_check` auto-fills `using` only when `type: restrictive` is set. On a permissive (default) `update` policy, `with_check` alone leaves `using` unset, and that blocks every update through the policy, so set `using` explicitly there too.

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
        using: "true"
      # Only the owner can write
      - operations: [insert]
        with_check: "auth.uid() = user_id"
      - operations: [update]
        using: "auth.uid() = user_id"
        with_check: "auth.uid() = user_id"
      - operations: [delete]
        using: "auth.uid() = user_id"
```

When a table has at least one `rls:` entry, instancez emits `ALTER TABLE ... ENABLE ROW LEVEL SECURITY` and `FORCE ROW LEVEL SECURITY`. Tables with no `rls:` block have RLS disabled — all rows are visible to all roles.

instancez passes `using` straight through to Postgres's `USING` clause and `with_check` to `WITH CHECK`. This matches standard Postgres semantics, including the asymmetric `update`-only auto-fill behavior described above.

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
    using: "true"
  - operations: [insert]
    with_check: "auth.uid() = user_id"
  - operations: [update]
    using: "auth.uid() = user_id"
    with_check: "auth.uid() = user_id"
  - operations: [delete]
    using: "auth.uid() = user_id"
```

### Signed-in only

Any authenticated user can access the table; anonymous requests cannot.

```yaml
rls:
  - operations: [select, insert, update, delete]
    using: "auth.is_authenticated()"
    with_check: "auth.is_authenticated()"
```

### Private (owner only)

Only the row's owner can see or modify it.

```yaml
rls:
  - operations: [select, insert, update, delete]
    using: "auth.uid() = user_id"
    with_check: "auth.uid() = user_id"
```

### Divergent read/write rules on update

`using` and `with_check` don't have to match. A common case: a user can see
any row they own, but can only save changes to it while it isn't locked.

```yaml
rls:
  - operations: [update]
    using: "owner_id = auth.uid()"
    with_check: "owner_id = auth.uid() AND status != 'locked'"
```

If you only set `using` on an `update` policy, Postgres reuses it as
`with_check` too, checked against the new row. This works whether the
policy is permissive or restrictive.

The other direction doesn't hold in general. Setting only `with_check` on a
**permissive** `update` policy (permissive is the default `type`) does not
gate writes while leaving reads open: it leaves `using` unset, so the
policy matches zero rows and no update ever goes through it. Set `using`
explicitly on a permissive `update` policy even when it repeats
`with_check`.

`with_check` alone is valid on a **restrictive** `update` policy: a missing
`using` there is a no-op (restrictive policies narrow visibility granted by
a permissive policy elsewhere, they don't grant any of their own), so
`with_check` still enforces the write gate. Use this to layer an extra write
condition on top of a separate permissive policy:

```yaml
rls:
  - operations: [update]
    using: "owner_id = auth.uid()"
    with_check: "owner_id = auth.uid()"
  - operations: [update]
    type: restrictive
    with_check: "status != 'locked'"
```

### Admin bypass via service role

The `service_role` has `BYPASSRLS` in Postgres — it skips all policies. Requests made with the secret key are automatically assigned `service_role`, so they see every row regardless of any `using`/`with_check` expression. This applies both to the REST API (when the caller passes the secret key in the `apikey` header) and to code functions that use the backend client.

In code functions, use `ctx.serviceClient` to get a client that runs as `service_role`:

```js
export default async function handler(ctx) {
  // Bypasses RLS — use only for trusted server-side logic.
  const { data } = await ctx.serviceClient.from('posts').select('*');
  return Response.json(data);
}
```

Use `ctx.supabase` for operations that should respect RLS and run as the calling user.

## How roles are assigned

The middleware maps each request to one of three Postgres roles. The publishable key selects `anon` by default; a valid user token upgrades the request to `authenticated`; the secret key selects `service_role` outright.

| Request credential | Postgres role (default name) | BYPASSRLS |
|---|---|---|
| Publishable key, no user token | `anon` | No |
| Publishable key + valid user token | `authenticated` | No |
| Secret key | `service_role` | Yes |

The Postgres role names default to the values in the table above, matching Supabase. They are configurable via `INSTANCEZ_DB_ANON_ROLE`, `INSTANCEZ_DB_AUTHENTICATED_ROLE`, and `INSTANCEZ_DB_SERVICE_ROLE` environment variables — but the JWT claim values (`anon`, `authenticated`, `service_role`) are fixed and cannot be changed. They are part of the Supabase wire format.

The request pool logs in as the `authenticator` role, which is `NOINHERIT`. Without an explicit `SET LOCAL ROLE`, it carries no table privileges. Every request transaction starts by issuing `SET LOCAL ROLE` to the appropriate role, then runs the query, so the role is always correct for the lifetime of that transaction.

## What's next

- [Auth](/instancez/build/auth/) — how users sign up and get JWTs
- [Tables / Schema](/instancez/build/schema/) — table and column definitions
- [Storage](/instancez/build/storage/) — per-bucket RLS policies
- [Querying](/instancez/build/querying/) — filtering and embedding from the client
