---
title: Tables / Schema
description: Define tables, fields, types, and foreign keys in instancez.yaml. Changes apply automatically in dev mode.
---

Your database schema lives in `instancez.yaml`. When you run `inz dev`, the migrator watches the file and applies any changes automatically — no migration files to write or track by hand.

## Declaring a table

Tables go under the top-level `tables:` key. Each table gets a name and a list of fields:

```yaml
tables:
  posts:
    fields:
      - name: id
        type: bigserial
        primary_key: true
      - name: user_id
        foreign_key:
          references: auth.users.id
          on_delete: cascade
      - name: title
        type: text
        required: true
      - name: published_at
        type: timestamptz
        default: now()
```

Every table must have at least one field marked `primary_key: true`. The migrator will not inject one for you.

## Field types

The `type` field accepts standard Postgres type names. The most commonly used ones:

| Type | Notes |
|------|-------|
| `bigserial` | Auto-incrementing 64-bit integer. Use for surrogate PKs. |
| `uuid` | UUID. Pair with `default: uuid_v7()` or `default: uuid_v4()`. |
| `text` | Unbounded string. Also accepts `varchar(n)` and `char(n)`. |
| `int` / `bigint` / `smallint` | Integer variants. |
| `bool` / `boolean` | True/false. |
| `numeric` / `decimal` | Exact decimal arithmetic. |
| `float` / `real` / `double` | Floating-point numbers. |
| `date` | Calendar date (no time). |
| `time` / `timetz` | Time of day, with or without timezone. |
| `timestamp` / `timestamptz` | Date + time. `timestamptz` is timezone-aware and usually what you want. |
| `jsonb` / `json` | JSON data. Prefer `jsonb` for indexing and operators. |
| `bytea` | Raw binary data. |
| `inet` / `cidr` | IP address or network. |
| `serial` / `smallserial` | Smaller auto-increment variants. |

`type` is validated against a fixed allowlist (roughly: the integer/serial variants, `text`/`varchar`/`char`, `boolean`, `numeric`/`decimal`/`real`/`double`/`float`, the date/time types, `uuid`, `json`/`jsonb`, `bytea`, `inet`/`cidr`/`macaddr`, `money`, the geometric types, `tsquery`/`tsvector`, `xml`, and `bit`) — not an arbitrary Postgres type name. `varchar(n)`/`char(n)`/`bit(n)` parameterization and `[]` array suffixes are supported; unlisted types (e.g. `citext`, `hstore`, `ltree`, custom enums) fail validation with `unknown type`.

## Field options

| Option | Type | Description |
|--------|------|-------------|
| `name` | string | Column name. Required. |
| `type` | string | Postgres type. Required unless `foreign_key` is set (see [Foreign keys](#foreign-keys) for how the type is picked in that case). |
| `primary_key` | bool | Marks this column as the primary key. |
| `required` | bool | Adds a `NOT NULL` constraint. |
| `unique` | bool | Adds a `UNIQUE` constraint. |
| `default` | string or literal | Column default. See [defaults](#defaults) below. |
| `enum` | list of strings | Restricts column values to the given set. Only valid on `text`/`varchar`/`char` types. |
| `foreign_key` | object | Declares a foreign key. See [Foreign keys](#foreign-keys). |
| `check` | string | Arbitrary SQL check expression added as a `CHECK` constraint. |
| `min` / `max` | number | Numeric range constraints. Applied as check constraints. |
| `pattern` | string | Regex pattern constraint (applied as a check). |

### Defaults

The `default` option accepts:

- A literal value: `true`, `0`, `"pending"`
- One of the allowed SQL functions: `now()`, `uuid_v7()`, `uuid_v4()`, `current_date`, `current_time`

Note: `uuid_v7()` and `uuid_v4()` currently both generate a v4 UUID (`gen_random_uuid()`) — true v7 generation isn't wired up yet. Use whichever name reads better; there's no behavioral difference today.

```yaml
- name: status
  type: text
  required: true
  enum: [draft, published, archived]
  default: draft

- name: created_at
  type: timestamptz
  required: true
  default: now()

- name: id
  type: uuid
  primary_key: true
  default: uuid_v7()
```

## Foreign keys

Declare a foreign key with `foreign_key.references` pointing to `table.column` or `schema.table.column`:

```yaml
- name: user_id
  foreign_key:
    references: auth.users.id
    on_delete: cascade
```

When `foreign_key` is the only option on a field, the column type uses a heuristic: `UUID` for `auth.users.id`, `BIGINT` for all other references. The type is not inferred from the actual referenced column.

**`on_delete` options:**

| Value | Postgres behavior |
|-------|-------------------|
| `cascade` | Delete child rows when the parent is deleted. |
| `restrict` | Prevent deletion of the parent if children exist. |
| `set_null` | Set the FK column to `NULL` when the parent is deleted. |

Omit `on_delete` to default to `restrict`.

## Table schema

By default a table lives in the `public` Postgres schema. Set `schema:` on the table to place it elsewhere:

```yaml
tables:
  events:
    schema: analytics
    fields:
      - name: id
        type: bigserial
        primary_key: true
```

`auth` and `storage` are reserved — they're owned by the framework, and declaring a table with `schema: auth` or `schema: storage` fails validation. Table and column names must start with a lowercase letter and contain only lowercase letters, digits, and underscores, and can't be a reserved SQL keyword.

## Indexes

Add indexes under the table's `indexes:` key:

```yaml
tables:
  posts:
    fields:
      - name: id
        type: bigserial
        primary_key: true
      - name: author_id
        type: bigint
    indexes:
      - columns: [author_id]
      - columns: [author_id, id]
        unique: true
```

Set `where:` for a partial index: `where: "status = 'published'"`.

## How migrations work

- **Additive changes apply immediately.** New tables, new columns, new indexes, new policies — the migrator adds them on the next run.
- **Drops need an opt-in under `inz serve`.** Removing a table or column from the YAML drops it, along with its data. `inz serve` refuses such a plan unless you pass `--allow-destructive` (or set `INSTANCEZ_ALLOW_DESTRUCTIVE=true`), and the error names what would have been dropped. `inz dev` still applies drops without asking, since rebuilding the schema is the normal dev loop, but it logs a warning listing each one. Be careful with watch mode, which re-applies the diff on every save.
- **Renames are declared, not detected.** The migrator compares two versions of your YAML, and a rename looks exactly like a drop plus an add. Use `renamed_from:` so it renames in place instead. See below.
- **The migrator never injects columns.** No hidden `id`, `created_at`, or `updated_at` is added. Every column must be declared.
- **Dev mode watches for changes.** `inz dev` re-applies the schema diff on every save, so the feedback loop is instant. `inz validate` checks the YAML for errors without touching the database.

## Renaming a table or column

The migrator only sees two versions of your config. If you change a column's `name`, the old name is gone and a new one has appeared, which is indistinguishable from deleting one column and adding another. Applied literally, that discards the column's data.

Declare the previous name with `renamed_from:` and the migrator issues a rename instead:

```yaml
tables:
  notes:
    fields:
      - name: id
        type: bigserial
        primary_key: true
      - name: content
        type: text
        renamed_from: body
```

That produces `ALTER TABLE notes RENAME COLUMN body TO content`, so the values stay put. Tables take the same key:

```yaml
tables:
  articles:
    renamed_from: posts
    fields:
      - name: id
        type: bigserial
        primary_key: true
```

Renames run before everything else in the migration, so you can rename a table and its columns in one edit, and combine a rename with a type change on the same column.

A few things worth knowing:

- Once the rename has been applied, `renamed_from:` does nothing on later runs, because the stored config already carries the new name. You can leave it in place or delete it at your next cleanup.
- If the old name never existed, the field is treated as a plain new column.
- If both names exist in the live schema, no rename happens. The migrator will not overwrite a column that is already there.
- Foreign keys follow a rename automatically, since Postgres renames the constraint's target with the object. Remember to update any `references:` in your YAML that pointed at the old name.

Renaming is the one schema change you have to declare. Everything else is inferred from the config.

## What's next

- [RLS](/instancez/build/rls/) — lock down table access with row-level security policies
- [Auth](/instancez/build/auth/) — configure authentication, JWT expiry, and OAuth providers
