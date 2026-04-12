import { createClient } from '@supabase/supabase-js'

// Ultrabase speaks the same HTTP shape as Supabase, so we can create a
// standard supabase-js client against it. `VITE_ULTRABASE_URL` must be the
// server root (no /api or /rest/v1 suffix) — supabase-js adds the /auth/v1
// and /rest/v1 prefixes itself.
const URL = import.meta.env.VITE_ULTRABASE_URL || window.location.origin

// supabase-js requires an "anon key" even for unauthenticated requests. For
// this demo we send a placeholder: the server's JWT middleware fails to parse
// it, then falls through to role=anon for tables that have `allow_anon: true`.
// In a real deployment you'd issue a proper anon JWT and rotate it like a
// public credential.
const ANON_KEY = import.meta.env.VITE_ULTRABASE_ANON_KEY ?? 'public-anon-placeholder'

export const supabase = createClient(URL, ANON_KEY, {
  auth: {
    persistSession: true,
    autoRefreshToken: true,
    storage: window.localStorage,
    storageKey: 'ultrabase-catalog-auth',
  },
})
