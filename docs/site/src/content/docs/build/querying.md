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

List queries default to `LIMIT 20` when no `.limit()`/`.range()` is given — use pagination (below) to get more than 20 rows back.

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

The response includes a `Content-Range` header: `2-3/*` (or `2-3/N` when a count is requested).

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
| `exact` | `COUNT(*)` — precise but adds a query |
| `planned` | Uses the Postgres query planner estimate |
| `estimated` | `pg_class.reltuples` statistic when no filters exist, planner estimate (via `EXPLAIN`) when filters exist. Never executes `COUNT(*)`. |

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

### Alias

Rename the embed key in the response:

```js
// parent:todos is returned under "parent", not "todos"
const { data } = await supabase.from('comments').select('body, parent:todos(id, title)')
```

The `!left` modifier is accepted (explicit left join, the default) and can be combined with an alias: `parent:todos!left(id,title)`.

### FK disambiguation

When two FKs exist between the same tables, use `!fk_column` to pick the right one:

```
select=title,assignee:users!assignee_id(name)
```

### Spread embed

Inline the joined columns directly into the parent row (belongs-to only):

```
GET /rest/v1/comments?select=body,...todos(title)
// response: [{ body: "...", title: "..." }, ...]
```

### Embed-scoped filters, order, and limit

Filter, order, or paginate within a has-many embed using `<embed>.` prefixes:

```
GET /rest/v1/todos?select=title,comments(body)&comments.body=like.%important%&comments.order=created_at.desc&comments.limit=5
```

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
