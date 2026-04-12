# React Catalog Example

A product catalog React app that talks to an Ultrabase backend with
[`@supabase/supabase-js`](https://github.com/supabase/supabase-js). It's
deliberately dense — it exercises most of Ultrabase's query surface plus the
full auth flow so you can see how the pieces fit together.

## Run it

From this directory:

```sh
docker compose up --build
```

Then open:

- Web app: http://localhost:5174
- Ultrabase API: http://localhost:8080
- Docs UI: http://localhost:8080/api/docs

## What it demonstrates

Schema (see `ultrabase.yaml`):

- **categories** — simple lookup table, public read/write
- **products** — `text[]` tags, `jsonb` metadata, enum `status`, booleans,
  `searchable: [name, description]` with `search_config: english` for FTS
- **reviews** — has-many on `products`, FK to `users.id` via `user_id`.
  Public **select**, but **insert/update/delete** are gated by RLS:
  `user_id = auth.uid()` — only the row owner can touch their own review
- **users** (implicit, from `auth:`) — `display_name` field promoted into
  `raw_user_meta_data` on signup

Auth flow (see `src/AuthBar.jsx`):

- `supabase.auth.signUp({ email, password, options: { data: { display_name } } })`
- `supabase.auth.signInWithPassword(...)`
- `supabase.auth.signOut()`
- `supabase.auth.getSession()` + `onAuthStateChange(...)` to hydrate and react
- `display_name` is stored in `user_metadata` and rendered back on the bar

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

## The "anon key"

supabase-js always requires an anon key at `createClient()` time. In a real
Supabase project that key is a signed JWT with `role: "anon"`. For this demo
we send a placeholder string — Ultrabase's JWT middleware fails to parse it
and falls through to `role=anon` on tables with `allow_anon: true`. When a
user signs in, supabase-js replaces the `Authorization` header with the real
access token automatically, and RLS then evaluates against `auth.uid()`.
