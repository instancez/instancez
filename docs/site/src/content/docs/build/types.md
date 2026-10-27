---
title: TypeScript Types
description: Generate a database.types.ts from instancez.yaml and pass it to createClient<Database> for typed supabase-js queries.
---

`inz gen types` turns `instancez.yaml` into a `database.types.ts` file. Pass its `Database` type to `createClient` and `supabase-js` types every `from()`, `insert()`, `update()` and `rpc()` call.

It reads only the yaml. There is no database or network call, so the output is correct even before you migrate.

```sh
inz gen types --out database.types.ts
```

```ts
import { createClient } from '@supabase/supabase-js'
import type { Database } from './database.types'

const supabase = createClient<Database>('http://localhost:8080', process.env.INSTANCEZ_PUBLISHABLE_KEY!)

const { data } = await supabase.from('todos').select('id, title') // typed rows
await supabase.from('todos').insert({ title: 'ship it' })         // checked against Insert
```

The output has the same shape as `supabase gen types typescript`: `Tables` with `Row`, `Insert`, `Update` and `Relationships`, plus the `Tables<>`, `TablesInsert<>` and `TablesUpdate<>` helpers and a `Constants` export. The file is a runtime module because of `Constants`, so name it `.ts`, not `.d.ts`.

## Flags

See the [CLI reference](/api-reference/cli/#inz-gen-types). `public` is always emitted, even when empty, plus every other schema that has a table. Unset `${INSTANCEZ_ENV_*}` references are fine in string fields, so you need no secrets to generate types.

## Column types

Column types come from the list `inz validate` accepts. In `rpc` args and returns, the longer spellings (`int2`, `int4`, `int8`, `float4`, `float8`, `character varying`, `character`, `timestamp with time zone`, `timestamp without time zone`, `time with time zone`) map to the same types.

| Postgres | TypeScript |
|---|---|
| smallint, integer, int, serial, smallserial, bigint, bigserial, real, double precision, float, numeric, decimal | `number` |
| boolean, bool | `boolean` |
| json, jsonb | `Json` |
| text, varchar, char, uuid, date, timestamp, timestamptz, timetz, money, inet, cidr, macaddr, bytea, xml, point, tsquery | `string` |
| interval, time, bit, line, box, polygon, circle, tsvector, anything else | `unknown` |

`macaddr8`, `lseg`, `path`, `bit varying` and range types such as `int4range` work only in `rpc` args and returns, where `macaddr8` is `string` and the rest are `unknown`. `inz validate` rejects them as column types.

Types are matched case-insensitively. Parameters are ignored (`varchar(255)`, `numeric(10,2)`), and `text[]` becomes `string[]`. An `enum:` column is a union of string literals.

`Row` is nullable (`T | null`) unless the column is `required`, a primary key, or a serial type. In `Insert`, a column is optional when it has a `default`, is serial, or is nullable. `Update` makes every column optional, and an `immutable` column becomes `never`.

## RPC return types

Functions from `rpc:` are typed under `public`.

- `void` returns `undefined`.
- A scalar returns its mapped type `T`. A SQL NULL still arrives as `null`. `record`, composite and schema-qualified names are `unknown`.
- `setof <table>` returns that table's `Row[]` plus `SetofOptions`; a leading `public.` is ignored. An unknown table name, or a schema-qualified name other than `public.`, gives `Record<string, unknown>[]`.
- `setof <scalar>` returns `{ fn_name: T | null }[]`.
- `table(...)` returns inline objects, with every field `| null`.

## Differences from Supabase

- `Enums` and `Constants.*.Enums` stay empty because instancez enums are CHECK constraints; columns use inline unions.
- `setof <scalar>` returns `{ fn_name: T | null }[]`, which is the shape the REST layer sends.
- `interval`, `time`, `bit`, geometric types and `tsvector` are `unknown`, because the REST layer returns objects for them, not strings.
- Every relationship has `isOneToOne: false`, so a reverse embed is typed as an array. The server always returns an array for reverse embeds, even for a unique foreign key.
- `rpc` functions appear only under `public`, where instancez creates them.
- No `__InternalSupabase` block.
- Embeds across schemas work at runtime, since the server serves them, but are not typed because relationships are same-schema only. Use a cast or `.schema()` to type them.
- `auth` and `storage` are not typed, so FKs to `auth.users.id` produce no relationship (Supabase does the same).
- `functions:` (code functions) are not typed.

## Keep it in sync in CI

Commit `database.types.ts` and fail the build when it drifts from the yaml:

```yaml
- run: inz gen types | diff - database.types.ts
```
