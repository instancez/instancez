// supabase-js compatibility harness. Exits 0 on success, non-zero on any
// assertion failure. Output is streamed to the Go test log.
import { createClient } from '@supabase/supabase-js'
import crypto from 'node:crypto'

// RFC 6238 TOTP with the same defaults pquerna/otp uses on the server
// (SHA1, 6 digits, 30-second step). Secret is base32 — decode to bytes,
// HMAC the big-endian counter, extract a 4-byte dynamic truncation, mod
// 10^6, zero-pad. Small enough to drop in here without a dependency.
function base32Decode(str) {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'
  const clean = str.replace(/=+$/, '').toUpperCase()
  let bits = ''
  for (const c of clean) {
    const v = alphabet.indexOf(c)
    if (v < 0) continue
    bits += v.toString(2).padStart(5, '0')
  }
  const bytes = []
  for (let i = 0; i + 8 <= bits.length; i += 8) {
    bytes.push(parseInt(bits.slice(i, i + 8), 2))
  }
  return Buffer.from(bytes)
}

async function generateTOTP(secret, when = Date.now()) {
  const counter = Math.floor(when / 1000 / 30)
  const buf = Buffer.alloc(8)
  buf.writeBigUInt64BE(BigInt(counter))
  const key = base32Decode(secret)
  const hmac = crypto.createHmac('sha1', key).update(buf).digest()
  const offset = hmac[hmac.length - 1] & 0x0f
  const code =
    ((hmac[offset] & 0x7f) << 24) |
    ((hmac[offset + 1] & 0xff) << 16) |
    ((hmac[offset + 2] & 0xff) << 8) |
    (hmac[offset + 3] & 0xff)
  return (code % 1_000_000).toString().padStart(6, '0')
}

const URL = process.env.INSTANCEZ_URL
const ANON_KEY = process.env.INSTANCEZ_ANON_KEY
const ADMIN_KEY = process.env.INSTANCEZ_ADMIN_KEY
if (!URL || !ANON_KEY) {
  console.error('INSTANCEZ_URL and INSTANCEZ_ANON_KEY must be set')
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

// --- Resend OTP ---
await step('auth: resend OTP returns 200', async () => {
  const resp = await fetch(`${URL}/auth/v1/resend`, {
    method: 'POST',
    headers: { apikey: ANON_KEY, 'Content-Type': 'application/json' },
    body: JSON.stringify({ type: 'magiclink', email }),
  })
  assertEq(resp.status, 200)
})

// --- Token verify ---
await step('auth: token verify returns claims', async () => {
  const resp = await fetch(`${URL}/auth/v1/token/verify`, {
    method: 'POST',
    headers: { apikey: ANON_KEY, 'Content-Type': 'application/json' },
    body: JSON.stringify({ token: accessToken }),
  })
  assertEq(resp.status, 200)
  const claims = await resp.json()
  assert(claims.sub, 'sub present in claims')
  assert(claims.email === email, 'email matches')
  assert(claims.role === 'authenticated', 'role is authenticated')
})

// --- Reauthenticate ---
await step('auth: reauthenticate returns 200', async () => {
  const resp = await fetch(`${URL}/auth/v1/reauthenticate`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
      'Content-Type': 'application/json',
    },
  })
  assertEq(resp.status, 200)
})

// --- Identity listing ---
await step('auth: list identities returns array', async () => {
  const resp = await fetch(`${URL}/auth/v1/user/identities`, {
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
    },
  })
  assertEq(resp.status, 200)
  const data = await resp.json()
  assert(Array.isArray(data.identities), 'identities is array')
})

// --- OAuth / PKCE (against the "fake" provider registered by the Go test
// harness — see fakeOAuthProvider in supabase_integration_test.go). Its
// AuthorizeURL skips the real IdP hop and redirects straight back to our own
// callback, so these run against the real HTTP surface with no external
// dependency on Google/GitHub being reachable from CI. ---

await step('auth: signInWithOAuth (PKCE) completes via /authorize + /callback + exchangeCodeForSession', async () => {
  // A separate client because flowType defaults to 'implicit'; PKCE must be
  // opted into, same as a real app would.
  const pkceClient = createClient(URL, ANON_KEY, {
    auth: { flowType: 'pkce', persistSession: false, autoRefreshToken: false },
  })

  const { data, error } = await pkceClient.auth.signInWithOAuth({ provider: 'fake' })
  if (error) throw error
  assert(data.url, 'signInWithOAuth should return an authorize url')
  assert(data.url.includes('code_challenge='), 'authorize url should carry a PKCE code_challenge')

  // No redirect_to was supplied, so the server responds with the auth code
  // as JSON instead of a browser redirect — this single fetch drives the
  // whole /authorize -> (fake provider) -> /callback chain.
  const callbackResp = await fetch(data.url)
  assert(callbackResp.ok, `authorize+callback chain failed: ${callbackResp.status}`)
  const { code } = await callbackResp.json()
  assert(code, 'callback should return a PKCE auth code')

  // exchangeCodeForSession reads the code_verifier signInWithOAuth stashed in
  // this client's storage — real supabase-js code, not a raw fetch.
  const { data: sessionData, error: exchangeError } = await pkceClient.auth.exchangeCodeForSession(code)
  if (exchangeError) throw exchangeError
  assert(sessionData.session, 'exchangeCodeForSession should return a session')
  assert(sessionData.session.access_token, 'session should have an access token')
  assertEq(sessionData.user.app_metadata.provider, 'fake', 'oauth user provider metadata')
})

await step('auth: identity linking adds a new identity for the signed-in user', async () => {
  const authorizeResp = await fetch(`${URL}/auth/v1/user/identities/authorize?provider=fake`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${accessToken}`, apikey: ANON_KEY },
  })
  assertEq(authorizeResp.status, 200, 'link-identity authorize status')
  const { url: linkURL } = await authorizeResp.json()
  assert(linkURL, 'expected an authorize url for linking')

  const followResp = await fetch(linkURL)
  assert(followResp.ok, `link-identity callback failed: ${followResp.status}`)
  const followBody = await followResp.json()
  assertEq(followBody.message, 'Identity linked', 'link confirmation message')

  const idResp = await fetch(`${URL}/auth/v1/user/identities`, {
    headers: { Authorization: `Bearer ${accessToken}`, apikey: ANON_KEY },
  })
  const { identities } = await idResp.json()
  assert(identities.some((i) => i.provider === 'fake'), 'linked fake identity should appear in the list')
})

// --- Admin signOut ---
await step('admin: signOut revokes user sessions', async () => {
  if (!ADMIN_KEY) return
  // Sign in to get a session, then admin-revoke it
  const { data: s } = await anon.auth.signInWithPassword({ email, password })
  const rt = s.session.refresh_token

  const resp = await fetch(`${URL}/auth/v1/admin/users/${userId}/signout`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${ADMIN_KEY}`, apikey: ANON_KEY },
  })
  assertEq(resp.status, 204)

  // Refresh should now fail
  const { error } = await anon.auth.refreshSession({ refresh_token: rt })
  assert(error, 'refresh should fail after admin signOut')

  // Re-login so subsequent tests work
  const { data: re } = await anon.auth.signInWithPassword({ email, password })
  accessToken = re.session.access_token
  refreshToken = re.session.refresh_token
})

// --- Admin deleteFactor ---
await step('admin: deleteFactor removes a factor', async () => {
  if (!ADMIN_KEY) return
  // Enroll a factor
  const enrollResp = await fetch(`${URL}/auth/v1/factors`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ factor_type: 'totp', friendly_name: 'admin-test' }),
  })
  assert(enrollResp.ok, `enroll failed: ${enrollResp.status}`)
  const factor = await enrollResp.json()

  // Admin delete it
  const delResp = await fetch(`${URL}/auth/v1/admin/users/${userId}/factors/${factor.id}`, {
    method: 'DELETE',
    headers: { Authorization: `Bearer ${ADMIN_KEY}`, apikey: ANON_KEY },
  })
  assertEq(delResp.status, 200)

  // Verify it's gone
  const listResp = await fetch(`${URL}/auth/v1/factors`, {
    headers: { Authorization: `Bearer ${accessToken}`, apikey: ANON_KEY },
  })
  const factors = await listResp.json()
  assert(!factors.all?.some(f => f.id === factor.id), 'factor should be gone')
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
  // uuid columns must come back as canonical strings, not a byte array.
  assertEq(data.user_id, userId)
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

await step('rest: .maybeSingle() returns null on 0 rows (no error)', async () => {
  // supabase-js maps PGRST116 with "0 rows" in details to { data: null }.
  // Any other shape (generic 406, non-PGRST116 code) surfaces as an error.
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data, error } = await client
    .from('todos')
    .select('id')
    .eq('user_id', '00000000-0000-0000-0000-000000000000')
    .maybeSingle()
  if (error) throw error
  assertEq(data, null, 'maybeSingle 0 rows → null')
})

await step('rest: .single() surfaces PGRST116 on 0 rows', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data, error } = await client
    .from('todos')
    .select('id')
    .eq('user_id', '00000000-0000-0000-0000-000000000000')
    .single()
  assert(error, 'expected error from .single() on 0 rows')
  assertEq(error.code, 'PGRST116', 'single 0 rows error code')
  assert(data === null || data === undefined, 'no data on single() 0-row error')
})

await step('rest: patch row', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { error } = await client.from('todos').update({ done: true }).eq('user_id', userId)
  if (error) throw error
})

await step('rest: .match() regex operator', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  // `~` case-sensitive regex — match titles starting with "buy".
  const { data, error } = await client
    .from('todos')
    .select('id,title')
    .eq('user_id', userId)
    .filter('title', 'match', '^buy')
  if (error) throw error
  assert(Array.isArray(data) && data.length >= 1, 'match found rows')
  const caseMismatch = await client
    .from('todos')
    .select('id')
    .eq('user_id', userId)
    .filter('title', 'match', '^BUY')
  if (caseMismatch.error) throw caseMismatch.error
  assertEq(caseMismatch.data.length, 0, 'case-sensitive match rejects uppercase')
})

await step('rest: .imatch() case-insensitive regex', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data, error } = await client
    .from('todos')
    .select('id,title')
    .eq('user_id', userId)
    .filter('title', 'imatch', '^BUY')
  if (error) throw error
  assert(data.length >= 1, 'imatch case-insensitive matches')
})

await step('rest: isdistinct operator treats NULL as a value', async () => {
  // IS DISTINCT FROM NULL is true for any non-null, false for null.
  const resp = await fetch(
    `${URL}/rest/v1/todos?select=id,title&title=isdistinct.null&user_id=eq.${userId}`,
    { headers: { Authorization: `Bearer ${accessToken}`, apikey: ANON_KEY } }
  )
  assert(resp.ok, `isdistinct request failed: ${resp.status}`)
  const rows = await resp.json()
  assert(rows.length >= 1, 'isdistinct.null matches non-null titles')
})

await step('rest: Prefer return=headers-only suppresses body', async () => {
  // Create a scratch row with headers-only and verify no body is returned
  // but Preference-Applied echoes back.
  const resp = await fetch(`${URL}/rest/v1/todos`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
      'Content-Type': 'application/json',
      Prefer: 'return=headers-only',
    },
    body: JSON.stringify({ title: 'headers-only probe', user_id: userId }),
  })
  assertEq(resp.status, 201, 'headers-only insert status')
  const applied = resp.headers.get('Preference-Applied') || ''
  assert(
    applied.includes('return=headers-only'),
    `Preference-Applied should echo headers-only, got ${applied}`
  )
  const body = await resp.text()
  assertEq(body, '', 'headers-only response body is empty')
})

await step('rest: Prefer handling=strict rejects unknown directive', async () => {
  const resp = await fetch(`${URL}/rest/v1/todos?user_id=eq.${userId}`, {
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
      Prefer: 'handling=strict,bogus=value',
    },
  })
  assertEq(resp.status, 400, 'strict rejects unknown Prefer')
  const body = await resp.json()
  assertEq(body.code, 'PGRST122', 'strict returns PGRST122')
})

await step('rest: Prefer handling=strict accepts all known directives', async () => {
  const resp = await fetch(`${URL}/rest/v1/todos?user_id=eq.${userId}&limit=1`, {
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
      Prefer: 'handling=strict,count=exact',
    },
  })
  assert(resp.ok, `strict with known directives must succeed: ${resp.status}`)
})

await step('rest: Prefer missing=default substitutes column defaults and echoes back', async () => {
  const resp = await fetch(`${URL}/rest/v1/todos`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
      'Content-Type': 'application/json',
      Prefer: 'return=representation,missing=default',
    },
    // done/priority omitted on purpose — the server-side column defaults
    // (false / 0) must be substituted.
    body: JSON.stringify({ title: 'missing-default probe', user_id: userId }),
  })
  assertEq(resp.status, 201, 'missing=default insert status')
  const applied = resp.headers.get('Preference-Applied') || ''
  assert(applied.includes('missing=default'), `Preference-Applied should echo missing=default, got ${applied}`)
  const [row] = await resp.json()
  assertEq(row.done, false, 'done should fall back to its column default')
  assertEq(row.priority, 0, 'priority should fall back to its column default')
})

await step('rest: Prefer max-affected rejects an update touching too many rows', async () => {
  const marker = `max-affected-${Date.now()}`
  const insertResp = await fetch(`${URL}/rest/v1/todos`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
      'Content-Type': 'application/json',
      Prefer: 'return=representation',
    },
    body: JSON.stringify([
      { title: marker, user_id: userId },
      { title: marker, user_id: userId },
    ]),
  })
  assertEq(insertResp.status, 201, 'max-affected fixture insert status')

  const patchResp = await fetch(`${URL}/rest/v1/todos?title=eq.${marker}`, {
    method: 'PATCH',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
      'Content-Type': 'application/json',
      Prefer: 'return=representation,max-affected=1',
    },
    body: JSON.stringify({ priority: 9 }),
  })
  assertEq(patchResp.status, 400, 'max-affected=1 must reject a 2-row update')
  const body = await patchResp.json()
  assertEq(body.code, 'PGRST124', 'max-affected error code')

  // The rejected update must have rolled back — priority stays at its default.
  const checkResp = await fetch(`${URL}/rest/v1/todos?title=eq.${marker}&order=priority.desc&limit=1`, {
    headers: { Authorization: `Bearer ${accessToken}`, apikey: ANON_KEY },
  })
  const [row] = await checkResp.json()
  assertEq(row.priority, 0, 'update must not have applied after max-affected rejection')

  const patchOk = await fetch(`${URL}/rest/v1/todos?title=eq.${marker}`, {
    method: 'PATCH',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
      'Content-Type': 'application/json',
      Prefer: 'return=representation,max-affected=2',
    },
    body: JSON.stringify({ priority: 9 }),
  })
  assertEq(patchOk.status, 200, 'max-affected=2 must allow a 2-row update')
})

await step('rest: Prefer tx=rollback discards the mutation', async () => {
  const marker = `tx-rollback-${Date.now()}`
  const resp = await fetch(`${URL}/rest/v1/todos`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
      'Content-Type': 'application/json',
      Prefer: 'return=representation,tx=rollback',
    },
    body: JSON.stringify({ title: marker, user_id: userId }),
  })
  assertEq(resp.status, 201, 'tx=rollback insert reports success')
  const [row] = await resp.json()
  assertEq(row.title, marker, 'tx=rollback still returns the representation')

  const checkResp = await fetch(`${URL}/rest/v1/todos?title=eq.${marker}`, {
    headers: { Authorization: `Bearer ${accessToken}`, apikey: ANON_KEY },
  })
  const rows = await checkResp.json()
  assertEq(rows.length, 0, 'tx=rollback must not persist the row')
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
  // todos → comments(body, todos(title))
  // The nested belongs-to back to todos exercises the parent-of-child embed
  // codepath. The schema has no public.users any more (auth lives in the
  // auth schema and the embed parser's 2-part FK split can't reach it),
  // so we use the comments→todos FK as the inner belongs-to leg.
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data, error } = await client
    .from('todos')
    .select('title, comments(body, todos(title))')
    .eq('user_id', userId)
  if (error) throw error
  assert(Array.isArray(data), 'result is array')
  assert(data.length >= 1, 'at least one todo')
  const todo = data[0]
  assert(Array.isArray(todo.comments), 'comments is array')
  assert(todo.comments.length >= 1, 'at least one comment')
  const comment = todo.comments[0]
  assertEq(comment.body, 'test comment')
  assert(comment.todos, 'nested todos (parent) should be present')
  assertEq(comment.todos.title, todo.title, 'nested todo title should match parent title')
})

await step('rest: aliased belongs-to embed — parent:todos(title) on comments', async () => {
  // Regression for the docs/examples/gearstore bug where
  // `category:categories!left(...)` was rejected with "could not find a
  // relationship between 'products' and 'category:categories'". The alias
  // prefix must be stripped from the relation lookup and surfaced as the
  // JSON output key.
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data, error } = await client
    .from('comments')
    .select('body, parent:todos!left(id,title)')
    .eq('user_id', userId)
  if (error) throw error
  assert(Array.isArray(data) && data.length >= 1, 'expected at least one comment')
  const row = data[0]
  assert(row.parent, 'aliased embed must surface under the alias key')
  assert(row.todos === undefined, 'must not also surface under the relation name')
  assert(row.parent.id !== undefined, 'aliased embed should expose joined columns')
  assert(typeof row.parent.title === 'string', 'aliased embed should expose joined columns')
})

await step('rest: aliased has-many embed — feedback:comments(body) on todos', async () => {
  // Aliased reverse (has-many) embed: todos → feedback (=comments).
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data, error } = await client
    .from('todos')
    .select('title, feedback:comments(body)')
    .eq('user_id', userId)
  if (error) throw error
  assert(Array.isArray(data) && data.length >= 1, 'expected at least one todo')
  const todo = data[0]
  assert(Array.isArray(todo.feedback), 'aliased has-many must surface under the alias key as an array')
  assert(todo.comments === undefined, 'must not also surface under the relation name')
})

await step('rest: spread embed — ...todos(title) on comments', async () => {
  // Spread flattens the joined columns into the parent row.
  // Use raw fetch since supabase-js spread syntax may vary by version.
  const resp = await fetch(
    `${URL}/rest/v1/comments?select=body,...todos(title)&user_id=eq.${userId}`,
    { headers: { Authorization: `Bearer ${accessToken}`, apikey: ANON_KEY } }
  )
  assert(resp.ok, `spread request failed: ${resp.status}`)
  const rows = await resp.json()
  assert(Array.isArray(rows), 'result is array')
  assert(rows.length >= 1, 'at least one row')
  const row = rows[0]
  // The todo's title should be inlined into the parent row, not nested under "todos".
  assert(typeof row.title === 'string', 'spread should inline title')
  assert(row.todos === undefined, 'spread should not have nested todos key')
})

// --- Bulk insert ---
await step('rest: bulk insert an array of rows', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data, error } = await client
    .from('todos')
    .insert([
      { title: 'bulk-a', priority: 10, user_id: userId },
      { title: 'bulk-b', priority: 11, user_id: userId },
      { title: 'bulk-c', priority: 12, user_id: userId },
    ])
    .select('id,title')
  if (error) throw error
  assert(Array.isArray(data), 'bulk insert returns an array')
  assertEq(data.length, 3, 'all three rows inserted')
  const titles = data.map(r => r.title).sort()
  assertEq(JSON.stringify(titles), JSON.stringify(['bulk-a', 'bulk-b', 'bulk-c']), 'titles round-trip')
  for (const r of data) {
    await client.from('todos').delete().eq('id', r.id)
  }
})

// --- Upsert tests ---
await step('rest: upsert via Prefer resolution=merge-duplicates', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  // Insert a row, then upsert with same PK to update title.
  const { data: inserted, error: insErr } = await client
    .from('todos')
    .insert({ title: 'upsert-me', priority: 1, user_id: userId })
    .select()
    .single()
  if (insErr) throw insErr
  const id = inserted.id

  const { data, error } = await client
    .from('todos')
    .upsert({ id, title: 'upserted!', priority: 2, user_id: userId })
    .select()
    .single()
  if (error) throw error
  assertEq(data.title, 'upserted!', 'upsert updated title')
  assertEq(data.priority, 2, 'upsert updated priority')
  // Clean up
  await client.from('todos').delete().eq('id', id)
})

await step('rest: upsert with ignoreDuplicates (resolution=ignore)', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data: inserted, error: insErr } = await client
    .from('todos')
    .insert({ title: 'ignore-dup', priority: 5, user_id: userId })
    .select()
    .single()
  if (insErr) throw insErr
  const id = inserted.id

  const { error } = await client
    .from('todos')
    .upsert({ id, title: 'should-be-ignored', priority: 99, user_id: userId }, { ignoreDuplicates: true })
  if (error) throw error

  const { data: check } = await client.from('todos').select('title,priority').eq('id', id).single()
  assertEq(check.title, 'ignore-dup', 'ignoreDuplicates kept original title')
  assertEq(check.priority, 5, 'ignoreDuplicates kept original priority')
  await client.from('todos').delete().eq('id', id)
})

// --- Filter operator tests ---
// Insert some rows with varying priority for numeric operator testing.
const filterIds = []
await step('rest: insert rows for filter tests', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  for (const [title, priority] of [['alpha', 1], ['beta', 2], ['gamma', 3], ['delta', 4], ['epsilon', 5]]) {
    const { data, error } = await client
      .from('todos')
      .insert({ title, priority, user_id: userId })
      .select('id')
      .single()
    if (error) throw error
    filterIds.push(data.id)
  }
})

await step('rest: .gt() greater-than filter', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data, error } = await client
    .from('todos').select('title').in('id', filterIds).gt('priority', 3).order('priority')
  if (error) throw error
  assertEq(data.length, 2, 'gt(3) returns 2 rows')
  assertEq(data[0].title, 'delta')
  assertEq(data[1].title, 'epsilon')
})

await step('rest: .gte() greater-than-or-equal filter', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data, error } = await client
    .from('todos').select('title').in('id', filterIds).gte('priority', 4).order('priority')
  if (error) throw error
  assertEq(data.length, 2, 'gte(4) returns 2 rows')
  assertEq(data[0].title, 'delta')
})

await step('rest: .lt() less-than filter', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data, error } = await client
    .from('todos').select('title').in('id', filterIds).lt('priority', 3).order('priority')
  if (error) throw error
  assertEq(data.length, 2, 'lt(3) returns 2 rows')
  assertEq(data[0].title, 'alpha')
})

await step('rest: .lte() less-than-or-equal filter', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data, error } = await client
    .from('todos').select('title').in('id', filterIds).lte('priority', 2).order('priority')
  if (error) throw error
  assertEq(data.length, 2, 'lte(2) returns 2 rows')
  assertEq(data[1].title, 'beta')
})

await step('rest: .neq() not-equal filter', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data, error } = await client
    .from('todos').select('title').in('id', filterIds).neq('priority', 3).order('priority')
  if (error) throw error
  assertEq(data.length, 4, 'neq(3) returns 4 rows')
  assert(!data.some(r => r.title === 'gamma'), 'neq excludes gamma')
})

await step('rest: .like() pattern filter', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data, error } = await client
    .from('todos').select('title').eq('user_id', userId).like('title', '%eta')
  if (error) throw error
  assertEq(data.length, 1, 'like %eta')
  assertEq(data[0].title, 'beta')
})

await step('rest: .ilike() case-insensitive pattern filter', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data, error } = await client
    .from('todos').select('title').eq('user_id', userId).ilike('title', '%ETA')
  if (error) throw error
  assertEq(data.length, 1, 'ilike %ETA')
  assertEq(data[0].title, 'beta')
})

await step('rest: .in() set membership filter', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data, error } = await client
    .from('todos').select('title').eq('user_id', userId).in('title', ['alpha', 'gamma']).order('priority')
  if (error) throw error
  assertEq(data.length, 2, 'in([alpha,gamma])')
  assertEq(data[0].title, 'alpha')
  assertEq(data[1].title, 'gamma')
})

await step('rest: .order() with ascending and descending', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data, error } = await client
    .from('todos').select('title').in('id', filterIds).order('priority', { ascending: false })
  if (error) throw error
  assertEq(data[0].title, 'epsilon', 'desc order first')
  assertEq(data[data.length - 1].title, 'alpha', 'desc order last')
})

await step('rest: .limit() and .range() pagination', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data: page1, error: e1 } = await client
    .from('todos').select('title').in('id', filterIds).order('priority').limit(2)
  if (e1) throw e1
  assertEq(page1.length, 2, 'limit(2)')
  assertEq(page1[0].title, 'alpha')

  const { data: page2, error: e2 } = await client
    .from('todos').select('title').in('id', filterIds).order('priority').range(2, 3)
  if (e2) throw e2
  assertEq(page2.length, 2, 'range(2,3)')
  assertEq(page2[0].title, 'gamma')
  assertEq(page2[1].title, 'delta')
})

await step('rest: .or() logical disjunction', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  // priority=1 OR priority=5 → alpha + epsilon.
  const { data, error } = await client
    .from('todos').select('title').in('id', filterIds)
    .or('priority.eq.1,priority.eq.5').order('priority')
  if (error) throw error
  assertEq(data.length, 2, 'or() returns 2 rows')
  assertEq(data[0].title, 'alpha')
  assertEq(data[1].title, 'epsilon')
})

await step('rest: .not() negates a filter', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data, error } = await client
    .from('todos').select('title').in('id', filterIds)
    .not('priority', 'eq', 3).order('priority')
  if (error) throw error
  assertEq(data.length, 4, 'not(eq 3) returns 4 rows')
  assert(!data.some(r => r.title === 'gamma'), 'not excludes gamma')
})

await step('rest: .is() boolean check', async () => {
  // The filter rows are inserted with the default done=false (the earlier
  // patch that set done=true ran before they existed). Scope strictly by
  // id so the count is deterministic.
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data, error } = await client
    .from('todos').select('title').in('id', filterIds).is('done', false)
  if (error) throw error
  assertEq(data.length, 5, 'is(done,false) returns all 5 filter rows')
})

await step('rest: .textSearch() full-text search', async () => {
  // fts → to_tsquery; the single-lexeme query 'beta' matches the 'beta'
  // title via the text @@ tsquery operator.
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data, error } = await client
    .from('todos').select('title').in('id', filterIds).textSearch('title', 'beta')
  if (error) throw error
  assertEq(data.length, 1, 'textSearch matches one row')
  assertEq(data[0].title, 'beta')
})

await step('rest: select with exact count + head', async () => {
  // head:true suppresses the body but the Content-Range-derived count must
  // still come back. supabase-js exposes it as the `count` field.
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data, count, error } = await client
    .from('todos').select('*', { count: 'exact', head: true }).in('id', filterIds)
  if (error) throw error
  assertEq(count, 5, 'exact head count')
  assert(data === null || (Array.isArray(data) && data.length === 0), 'head suppresses rows')
})

await step('rest: select with exact count + rows', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data, count, error } = await client
    .from('todos').select('title', { count: 'exact' }).in('id', filterIds)
  if (error) throw error
  assertEq(count, 5, 'exact count alongside rows')
  assertEq(data.length, 5, 'rows still returned with count')
})

await step('rest: .csv() returns text/csv body', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data, error } = await client
    .from('todos').select('title').in('id', filterIds).order('priority').csv()
  if (error) throw error
  assertEq(typeof data, 'string', 'csv body is a string')
  const lines = data.trim().split('\n')
  assertEq(lines[0].trim(), 'title', 'csv header row')
  assert(lines.includes('alpha'), 'csv contains alpha')
  assert(lines.includes('epsilon'), 'csv contains epsilon')
})

await step('rest: array operators (.contains/.containedBy/.overlaps)', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data: ins, error: insErr } = await client
    .from('todos')
    .insert({ title: 'tagged', user_id: userId, tags: ['urgent', 'home'] })
    .select('id')
    .single()
  if (insErr) throw insErr
  const id = ins.id

  // cs: tags @> {urgent}
  const cs = await client.from('todos').select('title,tags').eq('id', id).contains('tags', ['urgent'])
  if (cs.error) throw cs.error
  assertEq(cs.data.length, 1, 'contains matches the tagged row')
  assert(cs.data[0].tags.includes('home'), 'tags array round-trips')

  // cs miss: a tag the row doesn't have
  const csMiss = await client.from('todos').select('id').eq('id', id).contains('tags', ['missing'])
  if (csMiss.error) throw csMiss.error
  assertEq(csMiss.data.length, 0, 'contains excludes non-matching')

  // ov: overlaps shares at least one element
  const ov = await client.from('todos').select('id').eq('id', id).overlaps('tags', ['home', 'work'])
  if (ov.error) throw ov.error
  assertEq(ov.data.length, 1, 'overlaps matches on shared element')

  // cd: tags <@ {urgent,home,extra} (the row's tags are a subset)
  const cd = await client.from('todos').select('id').eq('id', id).containedBy('tags', ['urgent', 'home', 'extra'])
  if (cd.error) throw cd.error
  assertEq(cd.data.length, 1, 'containedBy matches subset')

  await client.from('todos').delete().eq('id', id)
})

await step('rest: cleanup filter test rows', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  for (const id of filterIds) {
    await client.from('todos').delete().eq('id', id)
  }
})

// --- auth.updateUser / signInWithOtp / resetPasswordForEmail ---
// These hit PUT /user, POST /otp, and POST /recover respectively.
await step('auth.updateUser updates user_metadata', async () => {
  // Metadata only — deliberately NOT touching email/password, since later
  // steps still sign this shared user in by the original password.
  // updateUser() reads from the client's in-memory session, so sign in on a
  // dedicated client to populate it (a Bearer header alone isn't enough).
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
  })
  const { error: signInErr } = await client.auth.signInWithPassword({ email, password })
  if (signInErr) throw signInErr
  const { data, error } = await client.auth.updateUser({ data: { nickname: 'ace' } })
  if (error) throw error
  assertEq(data.user.user_metadata.nickname, 'ace', 'updated metadata round-trips')
  // The original display_name must survive a partial metadata merge.
  assertEq(data.user.user_metadata.display_name, 'Alice', 'existing metadata preserved')
})

await step('auth.signInWithOtp issues an OTP without erroring', async () => {
  // No SMTP provider is configured in the harness, so this exercises the
  // request/token path; GoTrue-style enumeration protection means it returns
  // success regardless. Use a fresh address so create-user runs.
  const otpEmail = `otp_${Date.now()}_${Math.floor(Math.random() * 1e6)}@example.com`
  const { error } = await anon.auth.signInWithOtp({
    email: otpEmail,
    options: { shouldCreateUser: true },
  })
  if (error) throw error
})

await step('auth.resetPasswordForEmail returns success', async () => {
  // Always-200 (enumeration protection). supabase-js surfaces no error.
  const { error } = await anon.auth.resetPasswordForEmail(email)
  if (error) throw error
})

await step('auth.resend returns success', async () => {
  // Same empty-body-200 contract: supabase-js parses the response as JSON,
  // so the handler must emit a body. (The pre-existing raw-fetch resend test
  // only checked status, masking this.)
  const { error } = await anon.auth.resend({ type: 'signup', email })
  if (error) throw error
})

await step('auth.reauthenticate returns success', async () => {
  // reauthenticate() reads the client's in-memory session, so sign in on a
  // dedicated client first (a Bearer header alone isn't enough).
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
  })
  const { error: signInErr } = await client.auth.signInWithPassword({ email, password })
  if (signInErr) throw signInErr
  const { error } = await client.auth.reauthenticate()
  if (error) throw error
})

// --- .rpc() tests ---
// Exercise the supabase-js .rpc() API against YAML-declared Postgres
// stored functions. These assertions are the ultimate client-level
// contract check: if any of them fail, .rpc() is broken for real
// supabase-js users regardless of what the server thinks it returned.

await step('rpc: scalar function returns bare number', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data, error } = await client.rpc('add_two', { a: 4, b: 5 })
  if (error) throw error
  assertEq(data, 9, 'add_two result')
})

await step('rpc: text function roundtrips a string', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data, error } = await client.rpc('echo_text', { msg: 'hi' })
  if (error) throw error
  assertEq(data, 'hi', 'echo_text result')
})

await step('rpc: jsonb arg roundtrips structured payload', async () => {
  // Normal named-arg path: supabase-js sends {payload: {...}} as the JSON
  // body, the RPC dispatcher matches "payload" against fn.Args, and pgx
  // encodes the value as jsonb. The function returns it verbatim, so
  // whatever we send should come back unchanged.
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const payload = { nested: { k: 'v' }, arr: [1, 2, 3], flag: true }
  const { data, error } = await client.rpc('echo_json', { payload })
  if (error) throw error
  // jsonb doesn't preserve key order through Postgres, so compare by
  // sorted serialization rather than the original insertion order.
  const sort = (o) => JSON.stringify(o, Object.keys(o).sort())
  assertEq(sort(data), sort(payload), 'echo_json roundtrip')
})

await step('rpc: Prefer params=single-object treats body as single jsonb arg', async () => {
  // supabase-js doesn't emit the Prefer: params=single-object header
  // itself, so we drive it via raw fetch. PostgREST uses this mode for
  // functions declared with one json/jsonb parameter where you want the
  // entire request body to become that parameter's value.
  const payload = { foo: 'bar', n: 42 }
  const resp = await fetch(`${URL}/rest/v1/rpc/echo_json`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'Prefer': 'params=single-object',
      'Authorization': `Bearer ${accessToken}`,
      'apikey': ANON_KEY,
    },
    body: JSON.stringify(payload),
  })
  assert(resp.ok, `single-object request failed: ${resp.status}`)
  const got = await resp.json()
  assertEq(JSON.stringify(got), JSON.stringify(payload), 'single-object roundtrip')
})

await step('rpc: unknown function surfaces PGRST202', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data, error } = await client.rpc('does_not_exist', {})
  assert(error, 'expected error for missing function')
  // supabase-js passes the server's error envelope through verbatim, so
  // the code must be the exact PGRST202 slug PostgREST uses.
  assertEq(error.code, 'PGRST202', 'missing-function error code')
  assert(data === null || data === undefined, 'no data for error path')
})

// --- SQL-kind RPC via /rest/v1/rpc/ ---
// SQL-kind functions are defined with kind:"sql" in the YAML but should be
// reachable via supabase-js .rpc() which hits /rest/v1/rpc/<name>.

await step('rpc: SQL-kind scalar function via .rpc()', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data, error } = await client.rpc('double_it', { n: 7 })
  if (error) throw error
  assertEq(data, 14, 'double_it(7) = 14')
})

await step('rpc: SQL-kind void function returns 204', async () => {
  const resp = await fetch(`${URL}/rest/v1/rpc/sql_void`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'Authorization': `Bearer ${accessToken}`,
      'apikey': ANON_KEY,
    },
    body: '{}',
  })
  assertEq(resp.status, 204, 'sql void returns 204')
  const body = await resp.text()
  assertEq(body, '', 'void body is empty')
})

// --- Void RPC (rpc-kind) ---
await step('rpc: void function returns null via supabase-js', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data, error } = await client.rpc('noop_void')
  if (error) throw error
  assertEq(data, null, 'void RPC returns null')
})

// --- GET on non-volatile (stable/immutable) RPC ---
await step('rpc: GET request works for stable functions', async () => {
  const resp = await fetch(`${URL}/rest/v1/rpc/add_two?a=10&b=20`, {
    method: 'GET',
    headers: {
      'Authorization': `Bearer ${accessToken}`,
      'apikey': ANON_KEY,
    },
  })
  assert(resp.ok, `GET rpc should succeed: ${resp.status}`)
  const data = await resp.json()
  assertEq(data, 30, 'GET add_two(10,20) = 30')
})

await step('rpc: GET request rejected for volatile functions', async () => {
  const resp = await fetch(`${URL}/rest/v1/rpc/noop_void`, {
    method: 'GET',
    headers: {
      'Authorization': `Bearer ${accessToken}`,
      'apikey': ANON_KEY,
    },
  })
  assertEq(resp.status, 405, 'GET on volatile → 405')
})

// --- Setof RPC chaining ---
// list_todos returns SETOF todos, so supabase-js should be able to chain
// .eq()/.order()/.limit() on the result just like a table query.
await step('rpc: setof function with .eq() chaining', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  // Insert a row to ensure there's data.
  const { data: ins } = await client
    .from('todos')
    .insert({ title: 'chain-test', priority: 42, user_id: userId })
    .select('id')
    .single()
  assert(ins, 'insert for chain test')

  const { data, error } = await client
    .rpc('list_todos')
    .eq('title', 'chain-test')
  if (error) throw error
  assert(Array.isArray(data), 'setof returns array')
  assert(data.length >= 1, 'at least one row')
  assertEq(data[0].title, 'chain-test')

  await client.from('todos').delete().eq('id', ins.id)
})

await step('rpc: setof function with .order().limit()', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  // Insert multiple rows with distinctive titles.
  const setofIds = []
  for (const title of ['z-last', 'a-first', 'm-middle']) {
    const { data: ins } = await client
      .from('todos').insert({ title, user_id: userId }).select('id').single()
    setofIds.push(ins.id)
  }

  const { data, error } = await client
    .rpc('list_todos')
    .in('title', ['z-last', 'a-first', 'm-middle'])
    .order('title')
    .limit(2)
  if (error) throw error
  assertEq(data.length, 2, 'limit(2) on setof')
  assertEq(data[0].title, 'a-first', 'ordered first')
  assertEq(data[1].title, 'm-middle', 'ordered second')

  for (const id of setofIds) {
    await client.from('todos').delete().eq('id', id)
  }
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

// --- MFA / TOTP ---
// supabase-js exposes auth.mfa.{enroll, challenge, verify, unenroll, listFactors}.
// We drive the lot against a real, password-verified session so the JWT
// middleware actually authorizes the enrollment.
await step('mfa: enroll TOTP factor returns secret + otpauth URI', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { data, error } = await client.auth.mfa.enroll({
    factorType: 'totp',
    friendlyName: 'harness-phone',
  })
  if (error) throw error
  assert(data.id, 'factor id returned')
  assertEq(data.type, 'totp', 'factor type')
  assert(data.totp?.secret, 'totp.secret present')
  assert(data.totp?.uri?.startsWith('otpauth://totp/'), 'totp.uri is otpauth URL')
  globalThis.__mfaFactorId = data.id
  globalThis.__mfaSecret = data.totp.secret
})

await step('mfa: listFactors returns the unverified factor', async () => {
  // Hit /factors directly; supabase-js's listFactors reads from session
  // user, not the server, so it can't observe freshly enrolled unverified
  // factors without a re-login.
  const resp = await fetch(`${URL}/auth/v1/factors`, {
    headers: { Authorization: `Bearer ${accessToken}`, apikey: ANON_KEY },
  })
  if (!resp.ok) throw new Error(`list factors failed: ${resp.status}`)
  const body = await resp.json()
  assert(Array.isArray(body.totp), 'totp is an array')
  assert(Array.isArray(body.all), 'all is an array')
  assert(body.all.length >= 1, 'all contains at least one factor')
  assert(body.totp.length >= 1, 'at least one totp factor')
  assertEq(body.totp[0].status, 'unverified', 'status before verify')
  // `all` should union totp+phone so supabase-js clients that read `all`
  // see every factor regardless of type.
  assertEq(body.all[0].id, body.totp[0].id, 'all[0] mirrors totp[0]')
})

await step('mfa: verify with valid TOTP code flips factor to verified and upgrades AAL', async () => {
  // Compute a live code against the shared secret via the standard
  // RFC 6238 algorithm. We avoid pulling otplib as a dep — the harness
  // already needs supabase-js, crypto is built in.
  const secret = globalThis.__mfaSecret
  const code = await generateTOTP(secret)
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  // Challenge then verify.
  const ch = await client.auth.mfa.challenge({ factorId: globalThis.__mfaFactorId })
  if (ch.error) throw ch.error
  assert(ch.data?.id, 'challenge id returned')

  const ver = await client.auth.mfa.verify({
    factorId: globalThis.__mfaFactorId,
    challengeId: ch.data.id,
    code,
  })
  if (ver.error) throw ver.error
  assert(ver.data?.access_token, 'new access_token from verify')
  // Decode the new JWT and assert aal=aal2 in app_metadata.
  const [, payload] = ver.data.access_token.split('.')
  const claims = JSON.parse(Buffer.from(payload, 'base64url').toString('utf8'))
  assertEq(claims.app_metadata?.aal, 'aal2', 'aal bumped to aal2')
})

await step('mfa: unenroll deletes the factor', async () => {
  const client = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const { error } = await client.auth.mfa.unenroll({ factorId: globalThis.__mfaFactorId })
  if (error) throw error

  const resp = await fetch(`${URL}/auth/v1/factors`, {
    headers: { Authorization: `Bearer ${accessToken}`, apikey: ANON_KEY },
  })
  const body = await resp.json()
  const stillThere = (body.totp || []).some((f) => f.id === globalThis.__mfaFactorId)
  assert(!stillThere, 'factor should be gone after unenroll')
})

// --- Anonymous sign-in ---
// signInAnonymously fires POST /auth/v1/signup with an empty body. The
// dispatcher must route that to the anonymous handler; the returned JWT
// should carry is_anonymous=true so RLS policies can distinguish guest
// sessions from real ones.
await step('auth.signInAnonymously issues an anonymous session', async () => {
  const { data, error } = await anon.auth.signInAnonymously()
  if (error) throw error
  assert(data.session, 'session returned')
  assert(data.user, 'user returned')
  const tok = data.session.access_token
  const [, payload] = tok.split('.')
  const claims = JSON.parse(Buffer.from(payload, 'base64url').toString('utf8'))
  assertEq(claims.is_anonymous, true, 'is_anonymous claim')
  assertEq(claims.role, 'authenticated', 'role claim')
})

// --- Admin generate_link + /verify roundtrip ---
// We create a fresh user via admin.generateLink(type=signup), then hit
// /auth/v1/verify with the returned token to confirm it is minted,
// stored, and consumable by the public verify endpoint.
if (ADMIN_KEY) {
  const linkEmail = `link_${Date.now()}_${Math.floor(Math.random() * 1e6)}@example.com`
  let actionLink = ''
  let actionToken = ''

  await step('admin.generateLink mints a signup token without sending email', async () => {
    const resp = await fetch(`${URL}/auth/v1/admin/generate_link`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${ADMIN_KEY}`,
      },
      body: JSON.stringify({
        type: 'signup',
        email: linkEmail,
        password: 'hunter2hunter2',
      }),
    })
    if (!resp.ok) throw new Error(`generate_link failed: ${resp.status} ${await resp.text()}`)
    const body = await resp.json()
    assert(body.action_link, 'action_link present')
    assertEq(body.verification_type, 'signup')
    assert(body.user, 'user present')
    assertEq(body.user.email, linkEmail)
    actionLink = body.action_link
    actionToken = body.email_otp
  })

  await step('admin.generateLink rejects unknown admin key with 401', async () => {
    const resp = await fetch(`${URL}/auth/v1/admin/generate_link`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': 'Bearer wrong-key',
      },
      body: JSON.stringify({ type: 'signup', email: 'x@x.com' }),
    })
    assertEq(resp.status, 401, 'unauthorized')
  })

  await step('verify consumes the generate_link token', async () => {
    assert(actionToken, 'prior step must have set actionToken')
    const resp = await fetch(`${URL}/auth/v1/verify`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ type: 'signup', token: actionToken }),
    })
    if (!resp.ok) throw new Error(`verify failed: ${resp.status} ${await resp.text()}`)
    const session = await resp.json()
    assert(session.access_token, 'access_token returned')
    assert(session.user, 'user returned')
    assertEq(session.user.email, linkEmail)
  })

  await step('verify rejects a reused generate_link token', async () => {
    const resp = await fetch(`${URL}/auth/v1/verify`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ type: 'signup', token: actionToken }),
    })
    assertEq(resp.status, 401, 'reused token rejected')
  })
}

// --- Admin user CRUD via supabase-js auth.admin.* ---
// Drives createUser → getUserById → listUsers → updateUserById →
// deleteUser, plus inviteUserByEmail, through the real supabase-js admin
// client (service key). This is the GoTrue admin API surface; the response
// envelopes (bare user, { users, aud }, …) are part of the wire contract.
if (ADMIN_KEY) {
  const adminClient = createClient(URL, ADMIN_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
  })
  const adminEmail = `admin_${Date.now()}_${Math.floor(Math.random() * 1e6)}@example.com`
  let adminUserId = ''

  await step('admin.createUser provisions a user', async () => {
    const { data, error } = await adminClient.auth.admin.createUser({
      email: adminEmail,
      password: 'hunter2hunter2',
      email_confirm: true,
      user_metadata: { plan: 'pro' },
    })
    if (error) throw error
    assert(data.user, 'created user returned')
    assertEq(data.user.email, adminEmail)
    assertEq(data.user.user_metadata.plan, 'pro', 'user_metadata round-trips')
    adminUserId = data.user.id
  })

  await step('admin.getUserById fetches the created user', async () => {
    const { data, error } = await adminClient.auth.admin.getUserById(adminUserId)
    if (error) throw error
    assertEq(data.user.id, adminUserId)
    assertEq(data.user.email, adminEmail)
  })

  await step('admin.listUsers includes the created user', async () => {
    const { data, error } = await adminClient.auth.admin.listUsers({ page: 1, perPage: 1000 })
    if (error) throw error
    assert(Array.isArray(data.users), 'users is an array')
    assert(data.users.some(u => u.id === adminUserId), 'created user appears in list')
  })

  await step('admin.updateUserById updates metadata', async () => {
    const { data, error } = await adminClient.auth.admin.updateUserById(adminUserId, {
      user_metadata: { plan: 'enterprise' },
    })
    if (error) throw error
    assertEq(data.user.user_metadata.plan, 'enterprise', 'metadata updated')
  })

  await step('admin.deleteUser removes the user', async () => {
    const { error } = await adminClient.auth.admin.deleteUser(adminUserId)
    if (error) throw error
    const { data: gone } = await adminClient.auth.admin.getUserById(adminUserId)
    assert(!gone?.user, 'user should be gone after delete')
  })

  await step('admin.inviteUserByEmail creates an invited user', async () => {
    const inviteEmail = `invite_${Date.now()}_${Math.floor(Math.random() * 1e6)}@example.com`
    const { data, error } = await adminClient.auth.admin.inviteUserByEmail(inviteEmail)
    if (error) throw error
    assert(data.user, 'invited user returned')
    assertEq(data.user.email, inviteEmail)
    // Clean up so repeat runs don't collide.
    await adminClient.auth.admin.deleteUser(data.user.id)
  })
}

// ==========================================================================
// Storage tests — supabase-js compatible /storage/v1/ endpoints
// ==========================================================================

// Helper: create an authenticated client with fresh access token.
const storageClient = () => createClient(URL, ANON_KEY, {
  auth: { persistSession: false, autoRefreshToken: false },
  global: { headers: { Authorization: `Bearer ${accessToken}` } },
})

// --- Bucket admin ---

await step('storage: listBuckets returns configured buckets', async () => {
  const resp = await fetch(`${URL}/storage/v1/bucket`, {
    headers: { Authorization: `Bearer ${accessToken}`, apikey: ANON_KEY },
  })
  assert(resp.ok, `listBuckets failed: ${resp.status}`)
  const buckets = await resp.json()
  assert(Array.isArray(buckets), 'buckets is array')
  assert(buckets.length >= 2, 'at least 2 buckets (avatars, documents)')
  const names = buckets.map(b => b.name)
  assert(names.includes('avatars'), 'has avatars bucket')
  assert(names.includes('documents'), 'has documents bucket')
})

await step('storage: getBucket returns bucket details', async () => {
  const resp = await fetch(`${URL}/storage/v1/bucket/avatars`, {
    headers: { Authorization: `Bearer ${accessToken}`, apikey: ANON_KEY },
  })
  assert(resp.ok, `getBucket failed: ${resp.status}`)
  const b = await resp.json()
  assertEq(b.name, 'avatars')
  assertEq(b.public, true)
})

await step('storage: getBucket 404 for unknown bucket', async () => {
  const resp = await fetch(`${URL}/storage/v1/bucket/nonexistent`, {
    headers: { Authorization: `Bearer ${accessToken}`, apikey: ANON_KEY },
  })
  assertEq(resp.status, 404)
})

await step('storage: createBucket returns 400 (YAML-only)', async () => {
  const resp = await fetch(`${URL}/storage/v1/bucket`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${accessToken}`, apikey: ANON_KEY, 'Content-Type': 'application/json' },
    body: JSON.stringify({ name: 'test', id: 'test' }),
  })
  assertEq(resp.status, 400)
})

// --- Upload (proxy) ---

await step('storage: upload file via POST', async () => {
  const content = 'hello world'
  const resp = await fetch(`${URL}/storage/v1/object/avatars/test-file.txt`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
      'Content-Type': 'text/plain',
    },
    body: content,
  })
  const data = await resp.json()
  assert(resp.ok, `upload failed: ${resp.status} ${JSON.stringify(data)}`)
  assert(data.Key, 'Key present')
})

await step('storage: upload duplicate without upsert returns 409', async () => {
  const resp = await fetch(`${URL}/storage/v1/object/avatars/test-file.txt`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
      'Content-Type': 'text/plain',
    },
    body: 'duplicate',
  })
  assertEq(resp.status, 409, 'duplicate without upsert → 409')
})

await step('storage: upload with x-upsert header succeeds', async () => {
  const resp = await fetch(`${URL}/storage/v1/object/avatars/test-file.txt`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
      'Content-Type': 'text/plain',
      'x-upsert': 'true',
    },
    body: 'updated content',
  })
  assert(resp.ok, `upsert upload failed: ${resp.status}`)
})

// --- Exists (HEAD) ---

await step('storage: HEAD existing object returns 200', async () => {
  const resp = await fetch(`${URL}/storage/v1/object/avatars/test-file.txt`, {
    method: 'HEAD',
    headers: { Authorization: `Bearer ${accessToken}`, apikey: ANON_KEY },
  })
  assertEq(resp.status, 200)
})

await step('storage: HEAD missing object returns 404', async () => {
  const resp = await fetch(`${URL}/storage/v1/object/avatars/no-such-file.txt`, {
    method: 'HEAD',
    headers: { Authorization: `Bearer ${accessToken}`, apikey: ANON_KEY },
  })
  assertEq(resp.status, 404)
})

// --- Info ---

await step('storage: object info returns metadata', async () => {
  const resp = await fetch(`${URL}/storage/v1/object/info/authenticated/avatars/test-file.txt`, {
    headers: { Authorization: `Bearer ${accessToken}`, apikey: ANON_KEY },
  })
  assert(resp.ok, `info failed: ${resp.status}`)
  const info = await resp.json()
  assertEq(info.name, 'test-file.txt')
  assert(info.id, 'id present')
})

// --- Download (authenticated) ---

await step('storage: download authenticated object', async () => {
  const resp = await fetch(`${URL}/storage/v1/object/authenticated/avatars/test-file.txt`, {
    headers: { Authorization: `Bearer ${accessToken}`, apikey: ANON_KEY },
  })
  assert(resp.ok, `download failed: ${resp.status}`)
  const body = await resp.text()
  assertEq(body, 'updated content', 'downloaded body matches last upsert')
})

// --- Download (public) ---

await step('storage: download public object without auth', async () => {
  const resp = await fetch(`${URL}/storage/v1/object/public/avatars/test-file.txt`)
  assert(resp.ok, `public download failed: ${resp.status}`)
  const body = await resp.text()
  assertEq(body, 'updated content')
})

await step('storage: download from non-public bucket fails', async () => {
  // First upload to documents (private bucket)
  const up = await fetch(`${URL}/storage/v1/object/documents/secret.txt`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
      'Content-Type': 'text/plain',
    },
    body: 'classified',
  })
  assert(up.ok, `documents upload failed: ${up.status}`)

  const resp = await fetch(`${URL}/storage/v1/object/public/documents/secret.txt`)
  assertEq(resp.status, 400, 'private bucket returns 400 on public download')
})

// --- List ---

await step('storage: list objects in bucket', async () => {
  const resp = await fetch(`${URL}/storage/v1/object/list/avatars`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ prefix: '', limit: 100, offset: 0 }),
  })
  assert(resp.ok, `list failed: ${resp.status}`)
  const items = await resp.json()
  assert(Array.isArray(items), 'list returns array')
  assert(items.length >= 1, 'at least one object')
  assert(items.some(i => i.name === 'test-file.txt' || i.id === 'test-file.txt'), 'test-file.txt in list')
})

await step('storage: listV2 returns cursor-based results', async () => {
  const resp = await fetch(`${URL}/storage/v1/object/list-v2/avatars`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ prefix: '', limit: 100 }),
  })
  assert(resp.ok, `listV2 failed: ${resp.status}`)
  const result = await resp.json()
  assert(typeof result.has_next === 'boolean', 'has_next is boolean')
  assert(Array.isArray(result.folders), 'folders is array')
  assert(Array.isArray(result.objects), 'objects is array')
  assert(result.objects.length >= 1, 'at least one object')
  assert(result.objects.some(o => o.name === 'test-file.txt' || o.id === 'test-file.txt'), 'test-file.txt in objects')
})

await step('storage: listV2 with_delimiter separates folders', async () => {
  // Upload a nested file to test delimiter behavior
  const nestedResp = await fetch(`${URL}/storage/v1/object/avatars/subfolder/nested.txt`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
      'Content-Type': 'text/plain',
      'x-upsert': 'true',
    },
    body: 'nested content',
  })
  assert(nestedResp.ok, `nested upload failed: ${nestedResp.status}`)

  const resp = await fetch(`${URL}/storage/v1/object/list-v2/avatars`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ prefix: '', limit: 100, with_delimiter: true }),
  })
  assert(resp.ok, `listV2 delimiter failed: ${resp.status}`)
  const result = await resp.json()
  assert(result.folders.length >= 1, 'at least one folder')
  assert(result.folders.some(f => f.name === 'subfolder/'), 'subfolder/ in folders')
})

await step('storage: listV2 cursor pagination', async () => {
  const resp1 = await fetch(`${URL}/storage/v1/object/list-v2/avatars`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ prefix: '', limit: 1 }),
  })
  assert(resp1.ok, `listV2 page1 failed: ${resp1.status}`)
  const page1 = await resp1.json()
  assert(page1.has_next === true, 'has_next should be true with limit=1')
  assert(typeof page1.next_cursor === 'string', 'next_cursor present')

  const resp2 = await fetch(`${URL}/storage/v1/object/list-v2/avatars`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ prefix: '', limit: 100, cursor: page1.next_cursor }),
  })
  assert(resp2.ok, `listV2 page2 failed: ${resp2.status}`)
  const page2 = await resp2.json()
  assert(page2.objects.length >= 1, 'page2 has objects')
})

// --- Update (PUT) ---

await step('storage: update existing object via PUT', async () => {
  const resp = await fetch(`${URL}/storage/v1/object/avatars/test-file.txt`, {
    method: 'PUT',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
      'Content-Type': 'text/plain',
    },
    body: 'final content',
  })
  assert(resp.ok, `update failed: ${resp.status}`)

  // Verify
  const dl = await fetch(`${URL}/storage/v1/object/authenticated/avatars/test-file.txt`, {
    headers: { Authorization: `Bearer ${accessToken}`, apikey: ANON_KEY },
  })
  const body = await dl.text()
  assertEq(body, 'final content')
})

// --- Copy ---

await step('storage: copy object', async () => {
  const resp = await fetch(`${URL}/storage/v1/object/copy`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({
      bucketId: 'avatars',
      sourceKey: 'test-file.txt',
      destinationKey: 'test-file-copy.txt',
    }),
  })
  assert(resp.ok, `copy failed: ${resp.status}`)

  // Verify copy exists
  const head = await fetch(`${URL}/storage/v1/object/avatars/test-file-copy.txt`, {
    method: 'HEAD',
    headers: { Authorization: `Bearer ${accessToken}`, apikey: ANON_KEY },
  })
  assertEq(head.status, 200, 'copied object exists')
})

// --- Move ---

await step('storage: move object', async () => {
  const resp = await fetch(`${URL}/storage/v1/object/move`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({
      bucketId: 'avatars',
      sourceKey: 'test-file-copy.txt',
      destinationKey: 'test-file-moved.txt',
    }),
  })
  assert(resp.ok, `move failed: ${resp.status}`)

  // Verify source is gone, destination exists
  const headSrc = await fetch(`${URL}/storage/v1/object/avatars/test-file-copy.txt`, {
    method: 'HEAD',
    headers: { Authorization: `Bearer ${accessToken}`, apikey: ANON_KEY },
  })
  assertEq(headSrc.status, 404, 'source gone after move')

  const headDst = await fetch(`${URL}/storage/v1/object/avatars/test-file-moved.txt`, {
    method: 'HEAD',
    headers: { Authorization: `Bearer ${accessToken}`, apikey: ANON_KEY },
  })
  assertEq(headDst.status, 200, 'destination exists after move')
})

// --- Signed download URL ---

await step('storage: createSignedUrl returns a URL', async () => {
  const resp = await fetch(`${URL}/storage/v1/object/sign/avatars/test-file.txt`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ expiresIn: 3600 }),
  })
  assert(resp.ok, `createSignedUrl failed: ${resp.status}`)
  const data = await resp.json()
  assert(data.signedURL, 'signedURL present')
})

// --- Batch signed URLs ---

await step('storage: createSignedUrls returns batch URLs', async () => {
  const resp = await fetch(`${URL}/storage/v1/object/sign/avatars`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ expiresIn: 3600, paths: ['test-file.txt'] }),
  })
  assert(resp.ok, `createSignedUrls failed: ${resp.status}`)
  const data = await resp.json()
  assert(Array.isArray(data), 'batch returns array')
  assert(data.length >= 1, 'at least one URL')
  assert(data[0].signedURL, 'signedURL in batch item')
})

// --- Signed upload URL + uploadToSignedUrl ---

await step('storage: createSignedUploadUrl + uploadToSignedUrl flow', async () => {
  // 1. Get signed upload token
  const signResp = await fetch(`${URL}/storage/v1/object/upload/sign/avatars/signed-upload.txt`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
      'Content-Type': 'application/json',
    },
    body: '{}',
  })
  assert(signResp.ok, `createSignedUploadUrl failed: ${signResp.status}`)
  const signData = await signResp.json()
  assert(signData.token, 'token present')

  // 2. Upload to signed URL
  const upResp = await fetch(`${URL}/storage/v1/object/upload/sign/avatars/signed-upload.txt?token=${signData.token}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'text/plain' },
    body: 'signed upload content',
  })
  assert(upResp.ok, `uploadToSignedUrl failed: ${upResp.status}`)

  // 3. Verify the upload
  const dl = await fetch(`${URL}/storage/v1/object/authenticated/avatars/signed-upload.txt`, {
    headers: { Authorization: `Bearer ${accessToken}`, apikey: ANON_KEY },
  })
  assert(dl.ok, 'download signed upload')
  const body = await dl.text()
  assertEq(body, 'signed upload content')
})

await step('storage: uploadToSignedUrl rejects bad token', async () => {
  const resp = await fetch(`${URL}/storage/v1/object/upload/sign/avatars/bad-token.txt?token=invalid`, {
    method: 'PUT',
    headers: { 'Content-Type': 'text/plain' },
    body: 'should fail',
  })
  assertEq(resp.status, 400, 'bad token rejected')
})

// --- Signed upload authorization (owner-scoped RLS on the `owned` bucket) ---

await step('storage: createSignedUploadUrl enforces the bucket insert policy at mint time', async () => {
  // The `owned` policy confines writes to the "mine/" prefix. An authenticated
  // user requesting a token for a path outside it fails the probe INSERT, so no
  // token is minted. This is the gap the mint-time check closes: without it,
  // any authenticated caller could obtain a token for any path and redeem it as
  // service_role.
  const resp = await fetch(`${URL}/storage/v1/object/upload/sign/owned/blocked.txt`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
      'Content-Type': 'application/json',
    },
    body: '{}',
  })
  assertEq(resp.status, 403, 'mint denied for a path the insert policy forbids')
  const data = await resp.json()
  assert(!data.token, 'no token handed to an unauthorized caller')
})

await step('storage: signed upload threads owner so the uploader can read it back', async () => {
  // For an allowed path the insert policy passes, and the owner bound into the
  // token is persisted as uploaded_by. The owner-scoped select policy then
  // matches on read-back, proof the redemption recorded the owner rather than
  // a NULL.
  const signResp = await fetch(`${URL}/storage/v1/object/upload/sign/owned/mine/file.txt`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
      'Content-Type': 'application/json',
    },
    body: '{}',
  })
  assert(signResp.ok, `authenticated mint failed: ${signResp.status}`)
  const signData = await signResp.json()
  assert(signData.token, 'token present')

  const upResp = await fetch(`${URL}/storage/v1/object/upload/sign/owned/mine/file.txt?token=${signData.token}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'text/plain' },
    body: 'owned content',
  })
  assert(upResp.ok, `redeem failed: ${upResp.status}`)

  const dl = await fetch(`${URL}/storage/v1/object/authenticated/owned/mine/file.txt`, {
    headers: { Authorization: `Bearer ${accessToken}`, apikey: ANON_KEY },
  })
  assert(dl.ok, `owner read-back failed (uploaded_by not threaded?): ${dl.status}`)
  assertEq(await dl.text(), 'owned content', 'owner reads back their object')
})

// --- Remove ---

await step('storage: remove objects', async () => {
  const resp = await fetch(`${URL}/storage/v1/object/avatars`, {
    method: 'DELETE',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ prefixes: ['test-file.txt', 'test-file-moved.txt', 'signed-upload.txt'] }),
  })
  assert(resp.ok, `remove failed: ${resp.status}`)
  const deleted = await resp.json()
  assert(Array.isArray(deleted), 'deleted is array')
  assert(deleted.length >= 2, 'at least 2 objects deleted')

  // Verify they're gone
  const head = await fetch(`${URL}/storage/v1/object/avatars/test-file.txt`, {
    method: 'HEAD',
    headers: { Authorization: `Bearer ${accessToken}`, apikey: ANON_KEY },
  })
  assertEq(head.status, 404, 'removed object is gone')
})

// --- Empty bucket ---

await step('storage: emptyBucket removes all objects', async () => {
  // Upload a file first
  await fetch(`${URL}/storage/v1/object/avatars/temp.txt`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
      'Content-Type': 'text/plain',
    },
    body: 'temp',
  })

  const resp = await fetch(`${URL}/storage/v1/bucket/avatars/empty`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${accessToken}`, apikey: ANON_KEY },
  })
  assert(resp.ok, `emptyBucket failed: ${resp.status}`)

  // Verify empty
  const listResp = await fetch(`${URL}/storage/v1/object/list/avatars`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ prefix: '', limit: 100, offset: 0 }),
  })
  const items = await listResp.json()
  assertEq(items.length, 0, 'bucket is empty after emptyBucket')
})

// --- Cleanup documents bucket ---
await step('storage: cleanup documents bucket', async () => {
  await fetch(`${URL}/storage/v1/bucket/documents/empty`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${accessToken}`, apikey: ANON_KEY },
  })
})

// --- explain() response ---
await step('rest: explain returns query plan', async () => {
  const resp = await fetch(`${URL}/rest/v1/todos?select=*`, {
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
      Accept: 'application/vnd.pgrst.plan+json',
    },
  })
  assertEq(resp.status, 200)
  const plan = await resp.json()
  assert(Array.isArray(plan) || (typeof plan === 'object'), 'plan returned')
})

// --- signOut scope=local ---
await step('auth: signOut scope=local revokes only current session', async () => {
  // Sign in twice to get two sessions
  const { data: s1 } = await anon.auth.signInWithPassword({ email, password })
  const { data: s2 } = await anon.auth.signInWithPassword({ email, password })

  // signOut scope=local on s1
  const resp = await fetch(`${URL}/auth/v1/logout?scope=local`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${s1.session.access_token}`, apikey: ANON_KEY },
  })
  assertEq(resp.status, 204)

  // s2 refresh should still work
  const { data: refreshed, error } = await anon.auth.refreshSession({ refresh_token: s2.session.refresh_token })
  assert(!error, 'second session should still be valid after local signout')
  accessToken = refreshed.session.access_token
  refreshToken = refreshed.session.refresh_token
})

// --- JWKS endpoint ---
await step('auth: JWKS endpoint returns RS256 public key', async () => {
  const resp = await fetch(`${URL}/auth/v1/.well-known/jwks.json`, {
    headers: { apikey: ANON_KEY },
  })
  assertEq(resp.status, 200)
  const body = await resp.json()
  assert(Array.isArray(body.keys), 'JWKS should have keys array')
  assert(body.keys.length > 0, 'JWKS should have at least one key')
  const key = body.keys[0]
  assertEq(key.kty, 'RSA')
  assertEq(key.alg, 'RS256')
  assertEq(key.use, 'sig')
  assert(key.kid, 'key should have kid')
  assert(key.n, 'key should have modulus n')
  assert(key.e, 'key should have exponent e')
})

// --- RS256 token verification ---
await step('auth: tokens are signed with RS256', async () => {
  const { data } = await anon.auth.signInWithPassword({ email, password })
  assert(data.session, 'should have session')
  const parts = data.session.access_token.split('.')
  const header = JSON.parse(Buffer.from(parts[0], 'base64url').toString())
  assertEq(header.alg, 'RS256')
  assert(header.kid, 'token header should have kid')
})

// --- Image transforms ---
await step('storage: download with image transform', async () => {
  // Upload a small valid PNG (1x1 red pixel)
  const pngBuf = Buffer.from(
    'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8/5+hHgAHggJ/PchI7wAAAABJRU5ErkJggg==',
    'base64'
  )
  const uploadResp = await fetch(`${URL}/storage/v1/object/avatars/transform-test.png`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${accessToken}`,
      apikey: ANON_KEY,
      'Content-Type': 'image/png',
      'x-upsert': 'true',
    },
    body: pngBuf,
  })
  assert(uploadResp.ok, `upload failed: ${uploadResp.status}`)

  // Download with transform
  const dlResp = await fetch(`${URL}/storage/v1/object/public/avatars/transform-test.png?width=1&height=1&format=png`, {
    headers: { apikey: ANON_KEY },
  })
  assert(dlResp.ok, `transformed download failed: ${dlResp.status}`)
  const ct = dlResp.headers.get('content-type')
  assert(ct && ct.includes('image/png'), `expected image/png, got ${ct}`)
})

// IHDR immediately follows the 8-byte PNG signature: 4-byte length, 4-byte
// "IHDR", then big-endian width/height at bytes 16-23.
function pngDimensions(buf) {
  return { width: buf.readUInt32BE(16), height: buf.readUInt32BE(20) }
}

await step('storage: image transform resize=fill stretches to exact dimensions', async () => {
  const dlResp = await fetch(
    `${URL}/storage/v1/object/public/avatars/transform-test.png?width=3&height=5&resize=fill&format=png`,
    { headers: { apikey: ANON_KEY } }
  )
  assert(dlResp.ok, `fill transform failed: ${dlResp.status}`)
  const buf = Buffer.from(await dlResp.arrayBuffer())
  const { width, height } = pngDimensions(buf)
  assertEq(width, 3, 'resize=fill width')
  assertEq(height, 5, 'resize=fill height')
})

await step('storage: image transform resize=contain preserves aspect within box', async () => {
  const dlResp = await fetch(
    `${URL}/storage/v1/object/public/avatars/transform-test.png?width=4&height=2&resize=contain&format=png`,
    { headers: { apikey: ANON_KEY } }
  )
  assert(dlResp.ok, `contain transform failed: ${dlResp.status}`)
  const buf = Buffer.from(await dlResp.arrayBuffer())
  const { width, height } = pngDimensions(buf)
  assert(width <= 4 && height <= 2, `contain must fit within box, got ${width}x${height}`)
  assert(width > 0 && height > 0, 'contain output must be non-empty')
})

await step('storage: image transform quality affects jpeg output size', async () => {
  const fetchJpeg = async (quality) => {
    const resp = await fetch(
      `${URL}/storage/v1/object/public/avatars/transform-test.png?width=64&height=64&resize=fill&format=jpeg&quality=${quality}`,
      { headers: { apikey: ANON_KEY } }
    )
    assert(resp.ok, `jpeg transform (quality=${quality}) failed: ${resp.status}`)
    assert((resp.headers.get('content-type') || '').includes('image/jpeg'), 'expected image/jpeg')
    return Buffer.from(await resp.arrayBuffer())
  }
  const low = await fetchJpeg(5)
  const high = await fetchJpeg(95)
  assert(low.length < high.length, `low quality (${low.length}b) should be smaller than high quality (${high.length}b)`)
})

// --- Serverless-friendly endpoints (raw fetch, not supabase-js) ---

await step('storage: serverless-friendly presigned URL — sign via /api/storage', async () => {
  // Re-login to get a fresh token (signOut may have been called in earlier tests,
  // or session may need refreshing). Use the original anon client to sign in.
  const { data: signIn } = await anon.auth.signInWithPassword({ email, password })
  if (!signIn?.session) throw new Error('re-login failed')
  const freshToken = signIn.session.access_token

  const signResp = await fetch(`${URL}/api/storage/avatars/sign`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${freshToken}`,
      apikey: ANON_KEY,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ content_type: 'text/plain', size: 100 }),
  })
  assert(signResp.ok, `presign failed: ${signResp.status} ${await signResp.clone().text()}`)
  const signData = await signResp.json()
  assert(signData.id, 'id present')
  assert(signData.upload_url, 'upload_url present')
})

// --- RLS / two-login enforcement ---
// rls_secrets has a policy: owner_id = auth.uid(). This exercises the
// per-request SET LOCAL ROLE: anon must be denied, service_role must
// bypass, and authenticated users can only see their own rows.
await step('rls: anon cannot insert into rls_secrets', async () => {
  const anonClient = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
  })
  const { error } = await anonClient.from('rls_secrets').insert({
    owner_id: '00000000-0000-0000-0000-000000000001',
    secret: 'should-fail',
  })
  assert(error, 'anon insert should be rejected by RLS')
})

await step('rls: anon cannot read other users\' secrets', async () => {
  const anonClient = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
  })
  const { data, error } = await anonClient.from('rls_secrets').select('*')
  if (error) throw error
  assertEq(data.length, 0, 'anon must not see any rows')
})

await step('rls: service_role (admin key) bypasses RLS on insert', async () => {
  const adminClient = createClient(URL, ADMIN_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${ADMIN_KEY}` } },
  })
  const { error } = await adminClient.from('rls_secrets').insert({
    owner_id: '00000000-0000-0000-0000-000000000099',
    secret: 'admin-seeded',
  })
  if (error) throw error
})

await step('rls: authenticated user can write + read own row', async () => {
  const userClient = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })
  const insRes = await userClient.from('rls_secrets').insert({
    owner_id: userId,
    secret: 'mine',
  })
  if (insRes.error) throw insRes.error
  const selRes = await userClient.from('rls_secrets').select('*')
  if (selRes.error) throw selRes.error
  assert(selRes.data.length >= 1, 'user should see at least their own row')
  // owner_id may come back as a string or as a byte buffer depending on the
  // pgx codec path. Normalize both to a hyphenated UUID string before comparing.
  const toUuid = (v) => {
    if (typeof v === 'string') return v
    const bytes = Array.isArray(v) ? v : Object.values(v)
    const hex = bytes.map((b) => b.toString(16).padStart(2, '0')).join('')
    return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
  }
  for (const row of selRes.data) {
    assertEq(toUuid(row.owner_id), userId, 'user must only see own rows')
  }
})

// --- Cross-schema FK + RLS via auth.uid() ---
// `profiles` lives in public, with id FK'd to auth.users.id, RLS gated
// by auth.uid() = id. This is the supabase-canonical pattern for
// per-user metadata that doesn't belong on auth.users itself.
await step('rest: profiles cross-schema FK + auth.uid()-gated RLS', async () => {
  const userClient = createClient(URL, ANON_KEY, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { headers: { Authorization: `Bearer ${accessToken}` } },
  })

  // pgx may codec UUIDs as a 16-byte buffer rather than a string in JSON
  // outputs; normalize before comparing. (Same helper as the rls_secrets
  // step.)
  const toUuid = (v) => {
    if (typeof v === 'string') return v
    const bytes = Array.isArray(v) ? v : Object.values(v)
    const hex = bytes.map((b) => b.toString(16).padStart(2, '0')).join('')
    return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
  }

  const { data: ins, error: insErr } = await userClient
    .from('profiles')
    .insert({ id: userId, display_name: 'Alice' })
    .select()
    .single()
  if (insErr) throw new Error(`profiles insert failed: ${insErr.message}`)
  assertEq(toUuid(ins.id), userId, 'profiles.id must equal auth.users.id')
  assertEq(ins.display_name, 'Alice', 'profiles.display_name roundtrip')

  // Read back through RLS.
  const { data: read, error: readErr } = await userClient
    .from('profiles')
    .select('*')
    .eq('id', userId)
  if (readErr) throw new Error(`profiles select failed: ${readErr.message}`)
  assertEq(read.length, 1, 'profiles select should return one row')
  assertEq(read[0].display_name, 'Alice', 'read-back display_name')

  // Inserting a profile for a different auth user must be blocked: the
  // RLS WITH CHECK clause (auth.uid() = id) and the FK to auth.users.id
  // both reject this. Either failure mode is acceptable — the contract
  // is that the operation is denied.
  const otherId = '00000000-0000-0000-0000-000000000042'
  const { error: foreignErr } = await userClient
    .from('profiles')
    .insert({ id: otherId, display_name: 'Mallory' })
  assert(foreignErr, "profiles insert with other user's id should be denied")
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
