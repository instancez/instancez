import { createClient } from '@supabase/supabase-js'

const supabase = createClient(
  'http://localhost:8080',
  process.env.INSTANCEZ_PUBLISHABLE_KEY,
)

await supabase.auth.signInWithPassword({
  email: 'maya@acme.dev',
  password: 'demo-pass-123',
})

const { data, error } = await supabase
  .from('todos')
  .select('id, title, status')
  .order('id')

console.log(error ?? data)
