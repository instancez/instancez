# Project templates

Ready-made backends offered when a user creates a project (`inz init --template`, or the hosted console). Each lives in
`pkg/templates/catalog/<id>/` and is embedded into the engine module.

```
catalog/<id>/
  template.yaml      manifest (shown in the UI)
  instancez.yaml     the backend config copied into the new project
  functions/*.js     code functions the config references (optional)
```

A template is copied into the project at create time. Editing a template never
changes existing projects, so templates need no versioning.

## Using a template

```
inz init --template <id> [name] [--dir <dir>] [--force]
inz init --list-templates
```

- `--list-templates` prints every id with its category and tagline.
- The optional `[name]` replaces `project.name` in `instancez.yaml`.
- Existing files are never overwritten without `--force`. Symlinked targets are refused, even with `--force`.
- Secrets are listed as `REQUIRED` (the headline feature needs them) or `optional`. Each name is also appended, empty, to `.development.env.example` and `.production.env.example`; copy them to
  `.development.env` (read by `inz dev`) and `.production.env` (read by `inz serve`). The app boots without any of them;
  a function that needs a missing secret returns 503 naming it.
- Manual steps from `setup[]` are printed after the secrets.

Trust: templates are fetched from the Instancez Cloud API (`INSTANCEZ_CLOUD_API` overrides the URL).
That includes `functions/package.json`, so review it before running `npm install`.

## Adding or changing a template

1. Create the folder. `id` equals the folder name: 1-3 lowercase hyphenated words, max 30 chars (`blog`, `kanban-board`).
2. Run `go test ./pkg/templates`. It is the compatibility gate: it
   validates every template with the same validator the deploy path uses, so an
   engine or validator change that breaks a template fails CI.
3. Prove it on the real engine (needs Docker):
   `docker run -d --rm -p 55432:5432 -e POSTGRES_PASSWORD=pw postgres:16`, then
   `INSTANCEZ_TEST_PG_DSN=postgres://postgres:pw@localhost:55432/postgres go test -tags=integration ./pkg/templates -run EngineBoot`
   (`TEMPLATES_ONLY=id1,id2` narrows it). It boots each template on a fresh database, then reads every
   table as anon and as a signed-up user and calls every function: no 5xx, no engine errors.
4. To check a draft before it lands in `catalog/`: `TEMPLATES_DIR=<dir> go test ./pkg/templates -run Contract`.

## template.yaml

| field | notes |
|---|---|
| `id`, `name` | `id` == directory name |
| `tagline` | one line, max 90 chars, shown on the card |
| `description` | 1-3 sentences. Say what works with zero setup and what needs a secret |
| `category` | `commerce` `content` `booking` `productivity` `business` `community` `education` `tools` |
| `tags` | search keywords |
| `secrets[]` | `name` (`INSTANCEZ_ENV_*`), `label`, `description`, `needed_for`, `required`, `url`. `label`, `needed_for` and `url` must not contain control characters |
| `setup[]` | manual steps that are not secrets: webhook URLs, promoting an admin, seed data |

`Includes` (tables, functions, buckets) is derived from `instancez.yaml`; do not write it.
Unknown manifest keys are rejected.

## Rules the contract test enforces

- **Boots with zero secrets.** Every `${INSTANCEZ_ENV_X}` carries a default: `${INSTANCEZ_ENV_X:-}`.
  Functions that need the secret check for empty and return 503 with the secret name.
- Declared secrets == referenced secrets. No undeclared refs, no unused declarations.
- `required: true` means the headline feature does not work without it; the app still boots.
- No top-level `providers` key at all: `inz init` adds a local storage provider itself.
- No `auth.oauth` or `providers.email` (their validators reject empty secrets). Send email from a
  function with `fetch` to the provider's REST API.
- No npm dependencies: use `fetch` and `node:crypto`. Verify webhook signatures against `req.rawBody`.
- Every table has `rls` (no `rls` = RLS off = every role sees every row). `update` policies set `using`.
- `rls.operations` is exactly one command or all four. Names are lowercase snake_case (buckets too).
- Every function file exists, is referenced, and parses as ESM with a default export.
- **Policies never query their own table.** A policy on T (or on a table whose policy reads T) that
  selects from T dies at runtime with `infinite recursion detected in policy` while still passing
  validation. Put the lookup in an rpc and call it: `using: "public.is_admin()"`. Such helper rpcs
  must be `language: plpgsql` (a `sql` body is checked at creation, before the tables exist),
  `security: definer`, `volatility: stable`, `set: {search_path: public}`, and are called schema-qualified.
- `ctx.supabase` / `ctx.serviceClient` need `functions/package.json` with exactly
  `{"type":"module","dependencies":{"@supabase/supabase-js":"^2.107.0"}}`. Ship it only when a function uses them.
- Roles: the first user is not special. Use a `role` column on `profiles` and a documented `setup` step,
  never a policy that lets a user grant themselves a role.

## Engine behaviors to design around

Found by driving the real engine; each cost a template a bug.

- **Secret key = `anon` inside rpc.** A call with the secret key (`ctx.serviceClient.rpc`) runs as `anon`; `auth.role()`
  is never `service_role` there. A service-only rpc cannot be role-guarded; do the privileged step with table ops in the function.
- **`RAISE ... ERRCODE`**: `P0001` (default) returns 400 and `42501` returns 403. `22023`, `P0002`, `55000`, `54000` return 500.
- **Optional rpc args need a literal `default`.** Without one the rpc is not found when the caller omits the arg.
  Defaults are quoted literals, so `null` is not expressible: use `""` / `0` or `CAST(NULL AS type)` and treat as unset.
- **No triggers, no column-level RLS, no OLD row in policies.** `updated_at` is client-set; use an rpc when a column must be immutable.
- **A function that returns 3xx** is followed by the runtime, not passed through: return a 200 meta-refresh or JSON instead.
- **`storage.objects.uploaded_by`** blocks deleting an auth user who has uploaded a file; note it in `setup` when a bucket is user-writable.
- **Storage policies** key on `name` (path); require `name LIKE auth.uid() || '/%'` for per-user folders.
- **Trusting the caller:** never accept IP, base URL or role from the request body or Host headers. Rate limits live in rpcs, keyed by data the caller cannot choose.

## Compatibility going forward

- Do not rename or remove an existing template `id` or secret name: users and docs link to them.
  Deprecate by dropping the card from the catalog only if no docs reference it.
- Add new capabilities by adding folders; the loader and UI are data-driven.
- A new manifest field needs a Go struct field (unknown keys fail), a UI use, and a line in this doc.
