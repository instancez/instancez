// supabase-js view of allow_signup=false.
import { createClient } from '@supabase/supabase-js'

const { INSTANCEZ_URL: URL, INSTANCEZ_PUBLISHABLE_KEY: KEY, EXISTING_EMAIL, EXISTING_PASSWORD } = process.env
const client = () => createClient(URL, KEY, { auth: { persistSession: false, autoRefreshToken: false } })

let failures = 0
const expect422 = async (name, fn, code) => {
  const { error } = await fn(client())
  const ok = error?.name === 'AuthApiError' && error.status === 422 && error.code === code
  console.log(`  ${ok ? 'ok ' : 'FAIL'} ${name}`)
  if (!ok) {
    failures++
    console.error(`    got ${error ? `${error.name} ${error.status} ${error.code}` : 'no error'}, want 422 ${code}`)
  }
}

await expect422('signUp', (c) => c.auth.signUp({ email: 'new@example.com', password: 'hunter2hunter2' }), 'signup_disabled')
await expect422('signInAnonymously', (c) => c.auth.signInAnonymously(), 'signup_disabled')
const { data, error } = await client().auth.signInWithPassword({ email: EXISTING_EMAIL, password: EXISTING_PASSWORD })
if (error || !data.session) {
  failures++
  console.error('  FAIL existing user signInWithPassword', error)
} else console.log('  ok  existing user signInWithPassword')
process.exit(failures ? 1 : 0)
