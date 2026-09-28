---
title: Querying
description: Filter, embed, aggregate, and paginate with the PostgREST-compatible query API.
---

instancez exposes the same HTTP API as Supabase, so any Supabase client library works — JavaScript, Python, Swift, Flutter, and others. Examples here use `@supabase/supabase-js` (which is also what the integration tests run against), but the raw HTTP parameters are shown so you can use any client or make requests directly.

## Basic select

Fetch all columns:

```js
const { data, error } = await supabase.from('todos').select('*')
// GET /rest/v1/todos?select=*
```

There's no default limit, same as PostgREST. A list query returns every matching row, up to `server.max_limit` (default 1000). Page with `.limit()`/`.range()`.

Fetch specific columns:

```js
const { data, error } = await supabase.from('todos').select('id, title, done')
// GET /rest/v1/todos?select=id,title,done
```

Alias a column in the response:

```js
const { data, error } = await supabase.from('todos').select('label:title, done')
// GET /rest/v1/todos?select=label:title,done
// response key is "label", not "title"
```

Cast a column type:

```js
// GET /rest/v1/todos?select=id::text,title
```

## Filtering

Filters are query parameters of the form `column=operator.value`.

### Comparison operators

| Operator | Meaning | Example |
|----------|---------|---------|
| `eq` | equal | `priority=eq.3` |
| `neq` | not equal | `priority=neq.3` |
| `gt` | greater than | `priority=gt.3` |
| `gte` | greater than or equal | `priority=gte.3` |
| `lt` | less than | `priority=lt.3` |
| `lte` | less than or equal | `priority=lte.3` |
| `like` | SQL LIKE (case-sensitive, `%` wildcard) | `title=like.%milk%` |
| `ilike` | SQL ILIKE (case-insensitive) | `title=ilike.%MILK%` |
| `match` | regex match (`~`) | `title=match.^buy` |
| `imatch` | case-insensitive regex (`~*`) | `title=imatch.^BUY` |
| `is` | IS NULL / IS TRUE / IS FALSE / IS UNKNOWN | `done=is.false` |
| `isdistinct` | IS DISTINCT FROM (NULL-safe not-equal) | `title=isdistinct.null` |
| `in` | set membership | `status=in.(active,pending)` |

```js
// eq
const { data } = await supabase.from('todos').select('*').eq('done', false)

// neq
const { data } = await supabase.from('todos').select('*').neq('priority', 3)

// gt / gte / lt / lte
const { data } = await supabase.from('todos').select('*').gt('priority', 3)
const { data } = await supabase.from('todos').select('*').lte('priority', 2)

// like / ilike
const { data } = await supabase.from('todos').select('*').like('title', '%milk%')
const { data } = await supabase.from('todos').select('*').ilike('title', '%MILK%')

// is
const { data } = await supabase.from('todos').select('*').is('done', false)

// in
const { data } = await supabase.from('todos').select('*').in('status', ['active', 'pending'])
```

### Pattern matching against multiple values

```
# match ALL patterns (LIKE ALL)
title=like(all).{%milk%,%eggs%}

# match ANY pattern (LIKE ANY)
title=like(any).{%milk%,%eggs%}

# case-insensitive variants
title=ilike(all).{%MILK%,%EGGS%}
title=ilike(any).{%MILK%,%EGGS%}
```

### Negation

Prefix any operator value with `not.` to negate it:

```js
// not equal to 3
const { data } = await supabase.from('todos').select('*').not('priority', 'eq', 3)

// URL: priority=not.eq.3
```

### Logical OR and AND

```js
// OR: rows where priority is 1 or 5
const { data } = await supabase
  .from('todos')
  .select('*')
  .or('priority.eq.1,priority.eq.5')

// URL: or=(priority.eq.1,priority.eq.5)
```

Nested logic:

```
or=(title.like.%milk%,and(priority.gt.3,done.is.false))
```

### Array and range operators

These apply to Postgres array and range types:

| Operator | SQL | Meaning |
|----------|-----|---------|
| `cs` | `@>` | array/range contains value |
| `cd` | `<@` | array/range is contained by |
| `ov` | `&&` | arrays/ranges overlap |
| `sl` | `<<` | range strictly left of |
| `sr` | `>>` | range strictly right of |
| `nxl` | `&>` | range does not extend left of |
| `nxr` | `&<` | range does not extend right of |
| `adj` | `-|-` | ranges are adjacent |

```
tags=cs.{urgent,blocked}
price_range=ov.[10,50]
```

### Full-text search

```js
// fts → to_tsquery
const { data } = await supabase.from('todos').select('*').textSearch('title', 'milk')

// URL: title=fts.milk
```

Other FTS operators (use via raw URL parameters):

| Operator | Postgres function |
|----------|-------------------|
| `fts` | `to_tsquery` |
| `plfts` | `plainto_tsquery` |
| `phfts` | `phraseto_tsquery` |
| `wfts` | `websearch_to_tsquery` |

Pass a language config in parentheses: `title=fts(english).milk`.

### JSONB path filtering

Access nested JSONB values using `->` (returns jsonb) and `->>` (returns text):

```
metadata->>theme=eq.dark
settings->notifications->>enabled=eq.true
```

## Ordering and pagination

### Order

```js
// ascending (default)
const { data } = await supabase.from('todos').select('*').order('priority')

// descending
const { data } = await supabase.from('todos').select('*').order('priority', { ascending: false })

// nulls first / nulls last (use URL param directly)
// order=priority.desc.nullslast
```

Multiple columns: `order=priority.asc,created_at.desc`.

### Limit and offset

```js
const { data } = await supabase.from('todos').select('*').order('priority').limit(10)

// URL: order=priority&limit=10&offset=0
```

### Range (HTTP Range header)

The client can request a slice with an HTTP `Range` header (`Range-Unit: items`). `supabase-js` exposes this via `.range()`:

```js
// rows 2–3 (zero-based, inclusive)
const { data } = await supabase.from('todos').select('*').order('priority').range(2, 3)
```

The response includes `Content-Range` (`2-3/*`, or `2-3/N` with a count). With a count, a partial page returns 206 and an offset past the total returns 416 `PGRST103` with `Content-Range: */N`, as in PostgREST; without a count it's always 200. The `Range` header is read on GET only. An empty page sends `*/*`, or `*/N` with a count.

supabase-js reports that 416 as an error with `data: null` and `count: null` (on a `head: true` request, `error.message` is `''`). Stop paging when `offset >= count`.

Range handling follows PostgREST:

- A `Range` header is intersected with `limit`/`offset`. A malformed header is ignored, `3-` is open-ended, and `9-0` returns 416.
- A negative `offset` is ignored. A negative `limit` returns 416, and so does an empty range such as `.range(5, 4)` (`offset=5&limit=0`), with or without a count. `.range(0, -1)` returns `[]`.
- Writes and RPCs check `limit`/`offset` the same way and return 416 before touching any row. `PUT` with `limit` or `offset` returns 400 `PGRST114`.

### Count

Pass `Prefer: count=exact` to get the total row count alongside (or instead of) the rows:

```js
const { data, count } = await supabase
  .from('todos')
  .select('*', { count: 'exact' })
```

Use `head: true` to skip the body and return only the count:

```js
const { count } = await supabase
  .from('todos')
  .select('*', { count: 'exact', head: true })
```

Count modes:

| Mode | Behavior |
|------|----------|
| `exact` | `COUNT(*)` over the same joins and filters as the rows (`!inner` embeds, `!inner` embed filters, `Accept-Profile` schema), in the same transaction. |
| `planned` | Uses the Postgres query planner estimate |
| `estimated` | `pg_class.reltuples` for an unfiltered read of a plain table; the planner estimate otherwise. |

`count=exact` on an aggregate counts ungrouped rows, so a grouped result is usually 206.

On a setof RPC, `exact` counts the function's result after filters in the same statement as the rows, so the function runs once. `planned` and `estimated` use the planner estimate and don't run the function again.

## Embeds (joins)

Embeds use foreign key relationships declared in `instancez.yaml` to join related tables in a single request.

### Belongs-to (many-to-one)

When the current table has a foreign key pointing to another table, the joined row is returned as an object:

```js
// comments has a FK: todo_id → todos.id
const { data } = await supabase.from('comments').select('body, todos(title)')
// response: [{ body: "...", todos: { title: "..." } }, ...]
```

### Has-many (one-to-many)

When another table has a FK pointing back to the current table, the joined rows are returned as an array:

```js
// todos has many comments
const { data } = await supabase.from('todos').select('title, comments(body)')
// response: [{ title: "...", comments: [{ body: "..." }, ...] }, ...]
```

### Inner join

By default, embeds use a LEFT join — rows with no matching related record are still returned (the embed key is `null`). Use `!inner` to drop those rows:

```js
// only todos that have at least one comment
// GET /rest/v1/todos?select=title,comments!inner(body)
```

A filter on a non-`!inner` embed (`?select=*,author(name)&author.name=eq.bob`) only filters the embedded rows. A to-one embed that doesn't match becomes `null` and a to-many embed becomes `[]`, and every parent row is still returned. Add `!inner` to filter the parents.

### Alias

Rename the embed key in the response:

```js
// parent:todos is returned under "parent", not "todos"
const { data } = await supabase.from('comments').select('body, parent:todos(id, title)')
```

The `!left` modifier is accepted (explicit left join, the default) and can be combined with an alias: `parent:todos!left(id,title)`.

### Many-to-many

A junction table whose primary key includes a foreign key to each side links the two tables, as in PostgREST. With `post_tags` keyed on `(post_id, tag_id)`, `posts` embeds `tags` directly:

```js
const { data } = await supabase.from('posts').select('title, tags(name)')
// response: [{ title: "...", tags: [{ name: "go" }] }, { title: "...", tags: [] }]
```

The junction's own columns aren't returned. Embed filters, `order`, `limit`, `offset` and `!inner` work as on a has-many embed, and RLS on both the junction and the target applies. A self many-to-many (both FKs to the same table) doesn't resolve, same as PostgREST.

### FK disambiguation

When more than one relationship links the same tables (two FKs, two junctions, or a FK and a junction), an embed without a hint returns HTTP 300 `PGRST201`, as in PostgREST. The `details` list each candidate and the `hint` names the embeds that resolve it. Pick one with a hint: the FK column, the referenced column, the constraint name (`<table>_<column>_fkey`), or the junction table for a many-to-many:

```
select=title,assignee:users!assignee_id(name)
select=title,creator:users!tasks_created_by_fkey(name)
select=title,tags!post_tags(name)
```

A FK written with a schema (`auth.users.id`, or any table outside `instancez.yaml`) can't be embedded. A two-part reference (`notes.id`) always means `public.notes`.

### Spread embed

Inline the joined columns directly into the parent row (belongs-to only):

```
GET /rest/v1/comments?select=body,...todos(title)
// response: [{ body: "...", title: "..." }, ...]
```

### Embed-scoped filters, order, and limit

Filter, order, or paginate within an embed using `<embed>.` prefixes:

```
GET /rest/v1/todos?select=title,comments(body)&comments.body=like.%important%&comments.order=created_at.desc&comments.limit=5
```

Filters (including `<embed>.or=(...)` and `<embed>.and=(...)`) apply to any embed, spreads included. `order`, `limit`, and `offset` apply to has-many embeds only; on a belongs-to embed they return 400.

A filter on a spread embed doesn't drop the parent either: when the embed doesn't match, its spread columns come back `null`.

### Nested embeds

```js
const { data } = await supabase
  .from('todos')
  .select('title, comments(body, todos(title))')
```

## Aggregates

Use PostgREST-style aggregate suffixes in the `select` parameter. When any aggregate is present, the query groups by all non-aggregate columns automatically.

### Syntax

```
col.agg()          — aggregate over a column
alias:col.agg()    — explicit alias
col.agg()::type    — cast the result
count()            — COUNT(*) with no column
```

Supported aggregates: `count`, `sum`, `avg`, `min`, `max`.

```js
// count rows per status
// GET /rest/v1/todos?select=status,count()
// response: [{ status: "active", count: 3 }, ...]

// sum of a column
// GET /rest/v1/todos?select=status,total:priority.sum()

// average with cast
// GET /rest/v1/todos?select=avg_priority:priority.avg()::numeric
```

### Grouping by an embed

An embed next to an aggregate is a group key, as in PostgREST. This works for belongs-to, has-many, and spread embeds:

```
GET /rest/v1/orders?select=amount.sum(),customers(name)
// response: [{ sum: 100, customers: { name: "Customer A" } }, ...]
```

Embeds in an aggregate query are returned as `jsonb`, so their object keys may come back in a different order.

Aggregates also work on setof RPC results (`rpc/fn?select=status,count()`), grouped by the plain columns, and next to embeds (`rpc/fn?select=status,count(),messages(id)`), grouped by the embed as on tables.

`*` can't be combined with an aggregate (`select=*,count()`), since it gives no columns to group by. That returns 400; list the columns instead. As in PostgREST, `count=exact`, `planned` and `estimated` on an aggregate query count the rows matching the filters, not the groups: `GROUP BY` and `having` don't change the total.

### HAVING

Filter on aggregate results with the `having` parameter:

```
GET /rest/v1/todos?select=status,total:id.count()&having=total.gt.2
```

### Order by aggregate

Reference the aggregate alias in the `order` parameter:

```
GET /rest/v1/todos?select=status,id.count()&order=count.desc
```

## Inserts, updates, and deletes

### Insert

```js
const { data, error } = await supabase
  .from('todos')
  .insert({ title: 'buy milk', user_id: userId })
  .select()
```

Bulk insert:

```js
const { data, error } = await supabase
  .from('todos')
  .insert([
    { title: 'buy milk', user_id: userId },
    { title: 'buy eggs', user_id: userId },
  ])
  .select('id, title')
```

Control what the server returns with `Prefer: return=`:

| Value | Behavior |
|-------|----------|
| `minimal` | No body returned (default) |
| `headers-only` | Status + headers only (201, no body) |
| `representation` | Full row(s) returned |

### Upsert

```js
// merge on PK conflict
const { data } = await supabase
  .from('todos')
  .upsert({ id: existingId, title: 'updated title' })
  .select()

// ignore on PK conflict
const { data } = await supabase
  .from('todos')
  .upsert({ id: existingId, title: 'ignored' }, { ignoreDuplicates: true })
```

To upsert on a non-PK column, pass `on_conflict=column_name` in the query string and `Prefer: resolution=merge-duplicates` or `resolution=ignore-duplicates`.

### Update

```js
const { error } = await supabase
  .from('todos')
  .update({ done: true })
  .eq('user_id', userId)
```

### Delete

```js
const { error } = await supabase
  .from('todos')
  .delete()
  .eq('id', todoId)
```

### max-affected guard

Prevent accidentally broad mutations with `Prefer: max-affected=N`. The request is rejected (rolled back) if more than N rows would be affected:

```
Prefer: max-affected=1
```

### Dry-run

Roll back the transaction after the query executes without committing changes:

```
Prefer: tx=rollback
```

## CSV output

Add `.csv()` in `supabase-js` or set `Accept: text/csv` to receive results as CSV:

```js
const { data } = await supabase.from('todos').select('title').order('priority').csv()
```

## What's next

- [RLS](/instancez/build/rls/) — control access to rows with row-level security policies
- [Auth](/instancez/build/auth/) — configure authentication and JWT handling
- [RPC reference](/instancez/api-reference/rpc/) — call Postgres functions over HTTP
