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

Any Postgres type name is accepted — this list covers the most common cases.

## Field options

| Option | Type | Description |
|--------|------|-------------|
| `name` | string | Column name. Required. |
| `type` | string | Postgres type. Required unless `foreign_key` is set (the FK source column inherits the type of the referenced column). |
| `primary_key` | bool | Marks this column as the primary key. |
| `required` | bool | Adds a `NOT NULL` constraint. |
| `unique` | bool | Adds a `UNIQUE` constraint. |
| `default` | string or literal | Column default. See [defaults](#defaults) below. |
| `enum` | list of strings | Restricts column values to the given set. Only valid on `text`/`varchar` types. |
| `foreign_key` | object | Declares a foreign key. See [Foreign keys](#foreign-keys). |
| `check` | string | Arbitrary SQL check expression added as a `CHECK` constraint. |
| `min` / `max` | number | Numeric range constraints. Applied as check constraints. |
| `pattern` | string | Regex pattern constraint (applied as a check). |

### Defaults

The `default` option accepts:

- A literal value: `true`, `0`, `"pending"`
- One of the allowed SQL functions: `now()`, `uuid_v7()`, `uuid_v4()`, `current_date`, `current_time`

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

When `foreign_key` is the only option on a field, the column type is inferred from the referenced column — you don't have to repeat it.

**`on_delete` options:**

| Value | Postgres behavior |
|-------|-------------------|
| `cascade` | Delete child rows when the parent is deleted. |
| `restrict` | Prevent deletion of the parent if children exist. |
| `set_null` | Set the FK column to `NULL` when the parent is deleted. |

Omit `on_delete` to get the Postgres default (`NO ACTION`).

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
- **Drops require a flag.** Removing a table or column from the YAML does not drop it unless you pass `--allow-destructive` to `inz dev` or `inz serve`. This prevents accidental data loss.
- **The migrator never injects columns.** No hidden `id`, `created_at`, or `updated_at` is added. Every column must be declared.
- **Dev mode watches for changes.** `inz dev` re-applies the schema diff on every save, so the feedback loop is instant. `inz validate` checks the YAML for errors without touching the database.

## What's next

- [RLS](/instancez/build/rls/) — lock down table access with row-level security policies
- [Auth](/instancez/build/auth/) — configure authentication, JWT expiry, and OAuth providers
