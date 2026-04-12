// supabase-js compatibility harness. Exits 0 on success, non-zero on any
// assertion failure. Output is streamed to the Go test log.
import { createClient } from '@supabase/supabase-js'

const URL = process.env.ULTRABASE_URL
const ANON_KEY = process.env.ULTRABASE_ANON_KEY
if (!URL || !ANON_KEY) {
  console.error('ULTRABASE_URL and ULTRABASE_ANON_KEY must be set')
  process.exit(2)
}

let failures = 0
const step = async (name, fn) => {
  try {
    await fn()
    console.log(`  ok  ${name}`)
  } catch (err) {
    failures++
    console.error(`  FAIL ${name}`)
    console.error(err?.stack || err)
  }
}

const assert = (cond, msg) => {
  if (!cond) throw new Error(msg || 'assertion failed')
}
const assertEq = (got, want, msg) => {
  if (got !== want) throw new Error(`${msg || 'assertEq'}: got ${JSON.stringify(got)}, want ${JSON.stringify(want)}`)
}

const anon = createClient(URL, ANON_KEY, {
  auth: { persistSession: false, autoRefreshToken: false },
})

const email = `user_${Date.now()}_${Math.floor(Math.random() * 1e6)}@example.com`
const password = 'hunter2hunter2'

let accessToken = ''
let refreshToken = ''
let userId = ''

await step('auth.signUp creates a user and returns a session', async () => {
  const { data, error } = await anon.auth.signUp({
    email,
    password,
    options: { data: { display_name: 'Alice' } },
  })
  if (error) throw error
  assert(data.user, 'expected user')
  assert(data.session, 'expected session (verify_email is off)')
  assert(data.user.id, 'user.id must be set')
  assertEq(data.user.email, email, 'user.email')
  assertEq(data.user.aud, 'authenticated', 'user.aud')
  assertEq(data.user.role, 'authenticated', 'user.role')
  assert(data.user.user_metadata, 'user_metadata present')
  assertEq(data.user.user_metadata.display_name, 'Alice', 'display_name roundtrip')
  assert(Array.isArray(data.user.identities), 'identities must be an array')
  accessToken = data.session.access_token
  refreshToken = data.session.refresh_token
  userId = data.user.id
})

await step('auth.signInWithPassword with wrong password fails', async () => {
  const { data, error } = await anon.auth.signInWithPassword({
    email,
    password: 'wrong-password',
  })
  assert(error, 'expected error')
  assert(!data?.session, 'no session on bad password')
})

await step('auth.signInWithPassword issues a new session', async () => {
  const { data, error } = await anon.auth.signInWithPassword({ email, password })
  if (error) throw error
  assert(data.session, 'session returned')
  assertEq(data.user.email, email)
  accessToken = data.session.access_token
  refreshToken = data.session.refresh_token
})

// Authenticated client for subsequent requests.
const authed = createClient(URL, ANON_KEY, {
  auth: { persistSession: false, autoRefreshToken: false },
  global: { headers: { Authorization: `Bearer ${accessToken}` } },
})

await step('auth.getUser returns the current user', async () => {
  const { data, error } = await authed.auth.getUser(accessToken)
  if (error) throw error
  assertEq(data.user.email, email)
  assertEq(data.user.id, userId)
})

await step('auth.refreshSession rotates tokens', async () => {
  const { data, error } = await anon.auth.refreshSession({ refresh_token: refreshToken })
  if (error) throw error
  assert(data.session, 'session returned')
  assert(data.session.access_token !== accessToken, 'access_token rotated')
  accessToken = data.session.access_token
  refreshToken = data.session.refresh_token
})

await step('rest: insert row via PostgREST', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data, error } = await client
    .from('todos')
    .insert({ title: 'buy milk', user_id: userId })
    .select()
    .single()
  if (error) throw error
  assertEq(data.title, 'buy milk')
  assertEq(data.done, false)
})

await step('rest: select rows back', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data, error } = await client.from('todos').select('*').eq('user_id', userId)
  if (error) throw error
  assert(Array.isArray(data), 'array result')
  assert(data.length >= 1, 'at least one row')
})

await step('rest: patch row', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { error } = await client.from('todos').update({ done: true }).eq('user_id', userId)
  if (error) throw error
})

// --- Nested embed tests ---
// These tests rely on the todos row inserted earlier still being present.

let todoId = ''
await step('rest: get todoId for embed tests', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data, error } = await client.from('todos').select('id').eq('user_id', userId).limit(1).single()
  if (error) throw error
  todoId = data.id
})

await step('rest: insert comment for nested embed test', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { error } = await client
    .from('comments')
    .insert({ body: 'test comment', todo_id: todoId, user_id: userId })
  if (error) throw error
})

await step('rest: nested embed — has-many with nested belongs-to', async () => {
  // todos → comments(body, users(id))
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data, error } = await client
    .from('todos')
    .select('title, comments(body, users(id))')
    .eq('user_id', userId)
  if (error) throw error
  assert(Array.isArray(data), 'result is array')
  assert(data.length >= 1, 'at least one todo')
  const todo = data[0]
  assert(Array.isArray(todo.comments), 'comments is array')
  assert(todo.comments.length >= 1, 'at least one comment')
  const comment = todo.comments[0]
  assertEq(comment.body, 'test comment')
  assert(comment.users, 'nested users should be present')
  assertEq(comment.users.id, userId, 'nested user id should match')
})

await step('rest: spread embed — ...users(id) on comments', async () => {
  // Spread flattens the joined columns into the parent row.
  // Use raw fetch since supabase-js spread syntax may vary by version.
  const resp = await fetch(
    `${URL}/rest/v1/comments?select=body,...users(id)&user_id=eq.${userId}`,
    { headers: { Authorization: `Bearer ${accessToken}`, apikey: ANON_KEY } }
  )
  assert(resp.ok, `spread request failed: ${resp.status}`)
  const rows = await resp.json()
  assert(Array.isArray(rows), 'result is array')
  assert(rows.length >= 1, 'at least one row')
  const row = rows[0]
  // The user's id should be inlined into the parent row, not nested under "users".
  assert(row.id !== undefined, 'spread should inline id')
  assert(row.users === undefined, 'spread should not have nested users key')
})

await step('rest: cleanup comments', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  await client.from('comments').delete().eq('user_id', userId)
})

await step('rest: delete row', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { error } = await client.from('todos').delete().eq('user_id', userId)
  if (error) throw error
})

await step('auth.signOut revokes the session', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { error } = await client.auth.signOut()
  if (error) throw error
})

if (failures > 0) {
  console.error(`\n${failures} step(s) failed`)
  process.exit(1)
}
console.log('\nall steps passed')
