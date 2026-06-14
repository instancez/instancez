---
title: Todo App
description: Auth, per-user data, and RLS in a minimal task manager.
---

A complete example covering user sign-up, sign-in, per-user CRUD, and row-level security. Everything a real app needs before it gets interesting.

## Schema

```yaml
# instancez.yaml
tables:
  todos:
    columns:
      id:     { type: uuid, default: gen_random_uuid(), primary_key: true }
      user_id: { type: uuid, nullable: false }
      title:  { type: text, nullable: false }
      done:   { type: boolean, default: false }
      created_at: { type: timestamptz, default: now() }
    rls:
      - operations: [select, insert, update, delete]
        check: "auth.uid() = user_id"
```

`auth.uid()` reads the user ID from the JWT on every query. No application code enforces ownership — Postgres does.

## Client setup

```js
import { createClient } from '@supabase/supabase-js'

const supabase = createClient(
  'http://localhost:8080',
  'your-anon-key'
)
```

## Sign up and sign in

```js
// Sign up
const { data, error } = await supabase.auth.signUp({
  email: 'user@example.com',
  password: 'hunter2',
})

// Sign in
const { data, error } = await supabase.auth.signInWithPassword({
  email: 'user@example.com',
  password: 'hunter2',
})
```

After sign-in, `supabase-js` stores the JWT and attaches it to every subsequent request automatically.

## CRUD

```js
// Create
await supabase
  .from('todos')
  .insert({ title: 'Buy milk', user_id: supabase.auth.getUser().data.user.id })

// List (only the signed-in user's todos — RLS filters the rest)
const { data: todos } = await supabase
  .from('todos')
  .select('*')
  .order('created_at', { ascending: false })

// Toggle done
await supabase
  .from('todos')
  .update({ done: true })
  .eq('id', todoId)

// Delete
await supabase
  .from('todos')
  .delete()
  .eq('id', todoId)
```

The RLS policy on the table means a user can never read, update, or delete another user's todos — even if they craft a raw request with their own JWT.

## Sign out

```js
await supabase.auth.signOut()
```

## What to explore next

- Add a `priority` column and filter by it with `.eq('priority', 'high')`
- Add a `tags` text array column and filter with `.contains('tags', ['work'])`
- Restrict inserts so `user_id` can only be the caller's own ID by tightening the `check` expression
- See [RLS Policies](/instancez/build/rls/) for more policy patterns
