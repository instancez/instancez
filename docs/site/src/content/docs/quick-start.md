---
title: Quick Start
description: Install instancez and have a Supabase-compatible API running in under 5 minutes.
---

## Install

```bash
curl -fsSL https://get.instancez.io | sh
```

Installs `inz` to `~/.local/bin`. Check it works: `inz --version`.

Windows or need a manual download? See the [Installation guide](/install/).

## Create a project

```bash
inz init my-app
cd my-app
```

This creates `instancez.yaml` with a `todos` table, an `avatars` storage bucket, and a `todos` code function. Nothing touches the database yet.

## Start the dev server

Set your Postgres connection string (any Postgres 14+ superuser DSN works):

```bash
export INSTANCEZ_DATABASE_URL=postgres://postgres:postgres@localhost:5432/postgres
inz dev
```

On first run, instancez provisions the required Postgres roles and writes the derived DSNs to `.development.env`. Subsequent runs read from `.development.env` directly.

Your API is live at `http://localhost:8080`. Save `instancez.yaml` and the schema updates automatically.

## Get your anon key

Open the dashboard at `http://localhost:8080/dashboard`. The API Keys section shows your anon key — copy it from there.

## Query your data

instancez speaks the same HTTP API as Supabase, so any Supabase client library works. Examples here use `@supabase/supabase-js`:

Your project starts with a `todos` table. Paste the anon key you copied above:

```js
import { createClient } from '@supabase/supabase-js'

const supabase = createClient('http://localhost:8080', '<your-anon-key>')

const { data, error } = await supabase
  .from('todos')
  .select('*')
```

> The scaffolded `todos` table has a `user_id = auth.uid()` RLS policy, so rows are filtered by the
> authenticated user. To read without signing in, temporarily set `check: "true"` in `instancez.yaml`.

## What's next

- [Schema](/build/schema/) — add tables, columns, and enums
- [Auth](/build/auth/) — sign up, sign in, OAuth, MFA
- [Querying](/build/querying/) — filters, embeds, pagination, aggregates
- [Deploy](/deploy/docker/) — run in production
