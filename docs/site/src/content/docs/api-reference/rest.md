---
title: REST API
description: CRUD endpoints and full PostgREST query operator reference.
---

## Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/rest/v1/<table>` | Select rows |
| `POST` | `/rest/v1/<table>` | Insert rows |
| `PATCH` | `/rest/v1/<table>` | Update rows |
| `DELETE` | `/rest/v1/<table>` | Delete rows |
| `POST` | `/rest/v1/rpc/<name>` | Call a Postgres function |

## Authentication

Every request must carry one of:

- `Authorization: Bearer <jwt>` — user or service-role JWT
- `apikey: <anon-key>` — anonymous requests (sets role to `anon`)

Both headers may be present simultaneously; `Authorization` takes precedence for role resolution.

## Filtering operators

Apply filters as query parameters: `?<column>=<operator>.<value>`.

| Operator | SQL equivalent | Example |
|----------|---------------|---------|
| `eq` | `=` | `?status=eq.active` |
| `neq` | `!=` | `?status=neq.done` |
| `gt` | `>` | `?priority=gt.3` |
| `gte` | `>=` | `?priority=gte.3` |
| `lt` | `<` | `?price=lt.100` |
| `lte` | `<=` | `?price=lte.100` |
| `like` | `LIKE` | `?title=like.*task*` (use `*` for `%`) |
| `ilike` | `ILIKE` | `?title=ilike.*Task*` |
| `match` | `~` | `?name=match.^[A-Z]` |
| `imatch` | `~*` | `?name=imatch.^[a-z]` |
| `is` | `IS` | `?deleted_at=is.null`, `?active=is.true` |
| `isdistinct` | `IS DISTINCT FROM` | `?col=isdistinct.null` |
| `in` | `IN` | `?status=in.(pending,active,done)` |
| `cs` | `@>` (contains) | `?tags=cs.{urgent}` |
| `cd` | `<@` (contained by) | `?tags=cd.{a,b,c}` |
| `ov` | `&&` (overlaps) | `?tags=ov.{a,b}` |
| `fts` | `@@ to_tsquery` | `?title=fts(english).cats & dogs` |
| `plfts` | `@@ plainto_tsquery` | `?body=plfts.quick brown fox` |
| `phfts` | `@@ phraseto_tsquery` | `?body=phfts.exact phrase` |
| `wfts` | `@@ websearch_to_tsquery` | `?body=wfts.cats OR dogs` |
| `sl` | `<<` (range left of) | `?range=sl.[5,10)` |
| `sr` | `>>` (range right of) | `?range=sr.[5,10)` |
| `nxl` | `&>` (not extend left) | `?range=nxl.[5,10)` |
| `nxr` | `&<` (not extend right) | `?range=nxr.[5,10)` |
| `adj` | `-\|-` (range adjacent) | `?range=adj.[5,10)` |
| `like(all)` | `LIKE ALL(ARRAY[...])` | `?title=like(all).{%foo%,%bar%}` |
| `like(any)` | `LIKE ANY(ARRAY[...])` | `?title=like(any).{%foo%,%bar%}` |
| `ilike(all)` | `ILIKE ALL(ARRAY[...])` | `?title=ilike(all).{%Foo%,%Bar%}` |
| `ilike(any)` | `ILIKE ANY(ARRAY[...])` | `?title=ilike(any).{%Foo%,%Bar%}` |

Prefix any filter value with `not.` to negate: `?status=not.eq.archived`.

### Logic operators

Combine filters with `and` / `or` at the top level:

```
?and=(status.eq.active,priority.gte.3)
?or=(status.eq.active,status.eq.pending)
?or=(status.eq.active,and(priority.gt.3,category.eq.bug))
```

### JSONB paths

Filter or select into JSONB columns using `->` / `->>`:

```
?metadata->>theme=eq.dark
?data->nested=cs.{"key":"val"}
```

## Ordering, limit, and range

```
?order=created_at.desc            # sort descending
?order=priority.asc,created_at.desc  # multi-column sort
?order=name.asc.nullsfirst        # null placement
?limit=20&offset=40               # page 3 of 20-per-page
```

**Range header** — alternative pagination compatible with PostgREST clients:

```
Range: 0-19
Range-Unit: items
```

Returns HTTP 206 when the result may be partial. When both `Range` and `limit`/`offset` are present, the query params take precedence.

## Count

Request a row count alongside results by adding a `count` directive to the `Prefer` header.

| Value | Behaviour |
|-------|-----------|
| `count=exact` | `COUNT(*)` — accurate, costs a full scan |
| `count=planned` | Planner estimate — cheap, may be inaccurate |
| `count=estimated` | Same as `planned` |

```http
Prefer: count=exact
```

The server responds with:

```
Content-Range: 0-19/342
```

When no count was requested:

```
Content-Range: 0-19/*
```

## Column selection and embeds

```
?select=id,name,created_at              # specific columns
?select=*                               # all columns (default)
?select=id,team(id,name)               # embed related table
?select=id,team!inner(id,name)         # inner join embed
?select=id,comments!left(body)         # left join embed
```

Scoped filters inside embeds:

```
?select=id,comments(body)&comments.status=eq.published
```

Nested embeds:

```
?select=id,team(id,members(id,name))
```

## Aggregates

```
?select=status,count()                 # count all rows
?select=price.sum()                    # sum of price
?select=price.avg()                    # average
?select=price.min(),price.max()        # min and max
?select=total:price.sum()             # with alias
?select=total:price::numeric.sum()    # with cast
```

Filter on aggregates using `having=`:

```
?select=status,count()&having=count.gt.5
```

## Insert

```http
POST /rest/v1/todos
Content-Type: application/json
Prefer: return=representation

{"title": "Buy milk", "done": false}
```

**Bulk insert** — send an array.

**Upsert** — combine with a conflict resolution directive:

```http
Prefer: resolution=merge-duplicates
Prefer: resolution=ignore-duplicates
```

Specify the conflict target:

```
?on_conflict=id
```

**Return modes:**

| `Prefer: return=` | Response |
|-------------------|----------|
| `minimal` (default) | Empty body, 201 |
| `representation` | Inserted row(s) as JSON, 201 |

**Missing columns** — `Prefer: missing=default` causes the server to echo `Preference-Applied: missing=default`; the behaviour (inserting `DEFAULT` for omitted columns) is always active.

## Update

```http
PATCH /rest/v1/todos?id=eq.42
Content-Type: application/json
Prefer: return=representation

{"done": true}
```

`Prefer: return=representation` returns the updated rows. Default is `minimal` (empty body).

Guard against accidentally broad updates:

```http
Prefer: max-affected=1
```

Returns PGRST124 if more than 1 row would be affected.

## Delete

```http
DELETE /rest/v1/todos?done=eq.true
Prefer: return=representation
```

Returns the deleted rows when `return=representation` is set.

## Error envelope

All errors return a JSON object:

```json
{
  "message": "Human-readable description",
  "details": "Additional context, may be null",
  "hint": "Suggested fix, may be null",
  "code": "PGRST-or-SQLSTATE-code"
}
```

HTTP status codes follow PostgREST conventions (400 for bad queries, 401/403 for auth, 404 for unknown tables/functions, 409 for conflicts, 500 for server errors).

## Dry run

Execute a mutation without committing:

```http
Prefer: tx=rollback
```

The query runs and the response is returned normally, but the transaction is rolled back. Use this to preview what would change.
