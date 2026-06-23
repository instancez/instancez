<div align="center">

<img src="docs/site/src/assets/logo.svg" alt="instancez" width="120" />

# instancez

**A single-binary, Supabase-compatible backend you define in one YAML file.**

Drop-in for `@supabase/supabase-js`, self-hosted in seconds, and built to be generated and edited by LLMs.

[![CI](https://github.com/instancez/instancez/actions/workflows/ci.yml/badge.svg)](https://github.com/instancez/instancez/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/instancez/instancez)](https://github.com/instancez/instancez/releases)
[![License](https://img.shields.io/badge/license-PENDING-lightgrey)](LICENSE)
<!-- TODO(license): replace the License badge above once the LICENSE is finalized (Apache-2.0 leaning). -->

[Quick start](#quick-start) · [Docs](https://instancez.github.io/instancez) · [Why instancez](#why-instancez) · [Compared to Supabase](#compared-to-supabase)

</div>

<!--
TODO(demo): record a terminal demo and drop it here, above the fold.
Suggested: asciinema of `inz init my-app && cd my-app && inz dev` through a working query, under 60 seconds.
Replace this comment with:  [![asciicast](https://asciinema.org/a/XXXX.svg)](https://asciinema.org/a/XXXX)
or an animated GIF: <p align="center"><img src="docs/site/src/assets/demo.gif" alt="instancez in 60 seconds" /></p>
-->

---

## What it is

instancez gives you a Postgres-backed API with auth, row-level security, storage, SQL functions, and JavaScript functions. You describe the whole project, tables, policies, buckets, and functions, in a single `instancez.yaml` file. On boot it diffs that file against your database and applies the changes. One Go binary runs the lot; the only hard dependency is Postgres 14 or newer.

It speaks the same HTTP API as Supabase, so existing `@supabase/supabase-js` code works without changes. You point the client at your instancez URL and keep your queries, auth calls, and storage uploads as they are.

## Why instancez

The whole backend is one file. Your schema, RLS policies, storage rules, and the function manifest all live in `instancez.yaml`, so a person or a model can read and edit the entire thing in one place. `inz init --generate-like "a recipe sharing app"` will write that file for you from a prompt.

Existing Supabase code keeps working. The REST, auth, RPC, and storage endpoints match the Supabase wire format, and a compatibility test drives `@supabase/supabase-js` against a live instancez on every commit, so the contract cannot quietly drift.

Setup is one binary. Run `inz dev --embedded-pg` and it starts a local Postgres for you, then provisions the roles it needs on first boot. Point it at your own Postgres with a connection string when you want one. Either way, there is no multi-container stack to stand up before you can make a request.

The same binary runs locally, in Docker, on a VM, or on AWS Lambda. You can bundle the config and functions into a single archive and boot from S3.

## Quick start

Install the CLI:

```bash
# macOS / Linux
curl -fsSL https://get.instancez.ai | sh

# Windows (PowerShell)
irm https://get.instancez.ai/windows | iex
```

Create a project and start the dev server. The fastest path uses the Postgres that ships inside the binary, so there is no database to install:

```bash
inz init my-app
cd my-app
inz dev --embedded-pg
```

The first run downloads a Postgres 16 binary (about 30 MB) and keeps its data in `./pgdata/`. Later runs reuse it.

**Or point at your own Postgres.** If you already run a Postgres 14+ instance, drop the flag and set a superuser connection string instead:

```bash
export INSTANCEZ_DATABASE_URL=postgres://postgres:postgres@localhost:5432/postgres
inz dev
```

`inz init` scaffolds an `instancez.yaml` with a `todos` table, an `avatars` storage bucket, and a `todos` function. The first `inz dev` provisions the Postgres roles, applies the schema, and serves the API at `http://localhost:8080`. Editing `instancez.yaml` re-applies the schema automatically.

Open `http://localhost:8080/dashboard` to manage tables and users and to copy your anon key.

## Query it with supabase-js

```js
import { createClient } from '@supabase/supabase-js'

const supabase = createClient('http://localhost:8080', '<your-anon-key>')

const { data, error } = await supabase
  .from('todos')
  .select('*')
```

The scaffolded `todos` table has a `user_id = auth.uid()` RLS policy, so rows are scoped to the signed-in user. To read without signing in while you experiment, set `check: "true"` on the policy in `instancez.yaml`.

## Compared to Supabase

instancez is wire-compatible with Supabase clients, but it is a smaller, self-hosted system with a different setup model. Here is an honest map of where they line up and where they do not.

| | instancez | Supabase |
| --- | --- | --- |
| Auth (password, magic link, OTP, anonymous, OAuth, TOTP MFA) | Yes | Yes |
| PostgREST-style REST API | Yes | Yes |
| SQL functions (RPC) | Yes | Yes |
| JavaScript functions | Yes (Node.js) | Yes (Deno) |
| Storage (local or S3, RLS, signed URLs, image resize) | Yes | Yes |
| Row-level security as the authorization layer | Yes | Yes |
| Realtime / websockets | No (see below) | Yes |
| Schema definition | One declarative YAML file | SQL migrations + dashboard |
| LLM generation of the backend | Yes (`--generate-like`) | No |
| Self-host footprint | One binary + Postgres | Multi-container stack |
| OAuth providers built in | Google, GitHub | Many |

**No realtime.** instancez does not ship websockets or realtime subscriptions, and that is a design choice rather than a missing feature on a roadmap. If your app depends on `supabase.channel(...)` subscriptions, instancez is not a drop-in for that part.

## Documentation

Full docs live at **[instancez.github.io/instancez](https://instancez.github.io/instancez)**:

- [Quick start](https://instancez.github.io/instancez/quick-start/)
- [Tables and schema](https://instancez.github.io/instancez/build/schema/)
- [Auth](https://instancez.github.io/instancez/build/auth/)
- [RLS policies](https://instancez.github.io/instancez/build/rls/)
- [Querying](https://instancez.github.io/instancez/build/querying/)
- [Supabase SDK compatibility](https://instancez.github.io/instancez/supabase-compatibility/)
- [Deploy](https://instancez.github.io/instancez/deploy/docker/)

## Contributing

Contributions are welcome. Start with [CONTRIBUTING.md](CONTRIBUTING.md) for the dev setup, the test loop, and how the codebase is laid out. Bug reports and feature requests go through the [issue templates](.github/ISSUE_TEMPLATE).

## Security

Found a vulnerability? Please follow the private disclosure process in [SECURITY.md](SECURITY.md) rather than opening a public issue.

## License

<!-- TODO(license): finalize. Apache-2.0 is the leaning default; see LICENSE. -->
See [LICENSE](LICENSE).
