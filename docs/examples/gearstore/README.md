# Gearstore Example

A desk-gear catalog React app that talks to an Instancez backend with
[`@supabase/supabase-js`](https://github.com/supabase/supabase-js). It's
deliberately dense — it exercises most of Instancez's query surface plus the
full auth flow so you can see how the pieces fit together.

## Run it

From this directory:

```sh
docker compose up --build
```

Then open:

- Web app: http://localhost:5174
- Instancez API: http://localhost:8080
- Docs UI: http://localhost:8080/api/docs

## What it demonstrates

Schema (see `instancez.yaml`):

The project's user identity lives in `auth.users` (managed by instancez).
Profile data lives in `profiles`, a user-defined table FK'd to `auth.users.id`.

- **categories** — simple lookup table, public read/write
- **products** — `text[]` tags, `jsonb` metadata, enum `status`, booleans
- **reviews** — has-many on `products`, FK to `auth.users.id` via `user_id`.
  Public **select**, but **insert/update/delete** are gated by RLS:
  `user_id = auth.uid()` — only the row owner can touch their own review
- **profiles** — user-defined table FK'd to `auth.users.id`; `display_name`
  is promoted into `raw_user_meta_data` on signup
Auth flows (see `src/AuthBar.jsx`) — four tabs, all driven by supabase-js:

- **Password** — `supabase.auth.signUp(...)` / `signInWithPassword(...)`
  with `display_name` promoted into `user_metadata` on signup.
- **Magic link** — `supabase.auth.signInWithOtp({ email })` dispatches a
  token, then (dev-only) `supabase.auth.verifyOtp({ token_hash, type: 'magiclink' })`
  completes the flow without needing an inbox.
- **Email OTP** — `signInWithOtp` + `verifyOtp({ email, token, type: 'email' })`
  using a 6-digit code.
- **Guest** — `supabase.auth.signInAnonymously()` issues a session JWT
  with `is_anonymous: true`. Anonymous users can read the catalog but
  RLS blocks them from posting reviews.
- **Session lifecycle** — `getSession()` + `onAuthStateChange(...)` to
  hydrate and react across components; `signOut()` to clear.

Because this demo runs without an SMTP provider, the magic-link and
email-OTP tabs call `/auth/v1/admin/generate_link` under the hood to
retrieve the token that would normally arrive by email. That admin key
is wired through `VITE_INSTANCEZ_ADMIN_KEY` in docker-compose and is
**never** safe to expose in a real browser.

Aggregates (see `src/CatalogStats.jsx`):

- Single-row totals via
  `products.select('total:id.count(),avg_cents:price_cents.avg(),min_cents:price_cents.min(),max_cents:price_cents.max(),stock_total:stock.sum()')`
- Group-by-category via
  `products.select('category_id,count:id.count(),avg_cents:price_cents.avg()')`
  — Instancez infers the `GROUP BY category_id` automatically from the
  unaggregated column.

TOTP MFA (see `src/SecurityPanel.jsx`):

- `supabase.auth.mfa.enroll({ factorType:'totp', friendlyName })` returns
  a shared secret + otpauth URI.
- `supabase.auth.mfa.challenge(...)` + `verify(...)` flips the factor to
  `verified` and upgrades the session to `aal2` (visible on the AuthBar
  badge).
- `supabase.auth.mfa.listFactors()` and `unenroll(...)` round out the
  management flow.

REST / PostgREST query features (see `src/App.jsx` and `src/ProductDetail.jsx`):

- **Websearch FTS** — `textSearch('name', q, { type: 'websearch' })`
- **Comparisons** — `eq`, `gt`, `gte`, `lte` on `status`, `stock`, `price_cents`
- **Array contains** — `contains('tags', [tag])`
- **JSONB path filter** — `eq('metadata->>brand', brand)`
- **Logical OR** — `or('on_sale.eq.true,featured.eq.true')`
- **Multi-column sort** — `order('featured').order('created_at')`
- **Count + range pagination** — `{ count: 'exact' }` + `range(from, to)`
- **Embeds** — belongs-to `category:categories!left(...)` and has-many
  `reviews(...)`; product detail uses `!inner`, embed-scoped filters
  (`gte('reviews.rating', n)`), embed ordering and limit via `foreignTable`
- **Alias + cast** — `price_numeric:price_cents::numeric`
- **Authenticated writes under RLS** — when signed in, the review composer
  inserts with `user_id: session.user.id`. The server enforces
  `user_id = auth.uid()` on INSERT/UPDATE/DELETE, so clients can't forge
  ownership. Attempts from anonymous sessions are rejected by RLS.
- **Owner-scoped edit / delete** — only reviews whose `user_id` matches the
  current session's UUID show the Edit / Delete buttons, and the RLS policy
  enforces the same invariant server-side.

The grid shows the **last PostgREST URL** at the bottom — expand it to see
exactly what supabase-js sent the backend.

## Code functions

`instancez.yaml` also declares a few JavaScript HTTP handlers (served at
`/functions/v1/<name>`); the source is in [`functions/`](functions/), with
shared deps in [`functions/package.json`](functions/package.json). `docker
compose up` runs them; locally, `inz dev` runs `npm ci` there on boot.

- **`hello`** — the minimum handler.
- **`echo`** — reflects the request/ctx surface (method, path, query, headers, body, claims).
- **`my-reviews`** — `auth_required`; uses `ctx.supabase` (RLS as the caller) + `zod` to
  list/create the signed-in user's reviews.
- **`webhook`** — HMAC-verified import endpoint that writes via `ctx.serviceClient`
  (BYPASSRLS, since there's no caller to attribute the row to).

```sh
# no auth needed
curl -s localhost:8080/functions/v1/hello -d '{"name":"you"}' | jq
curl -s 'localhost:8080/functions/v1/echo?q=hi' -d '{"x":1}' | jq

# auth_required: sign in (see below), then pass the access token
curl -s localhost:8080/functions/v1/my-reviews -H "Authorization: Bearer $TOKEN" | jq
```

See [`../../functions.md`](../../functions.md) for the full code-functions reference.

## The "anon key"

supabase-js always requires an anon key at `createClient()` time. In a real
Supabase project that key is a signed JWT with `role: "anon"`. For this demo
we send a placeholder string — Instancez's JWT middleware fails to parse it
and falls through to `role=anon`; table-level grants + RLS policies decide
access from there. When a user signs in, supabase-js replaces the
`Authorization` header with the real access token automatically, and RLS
then evaluates against `auth.uid()`.
