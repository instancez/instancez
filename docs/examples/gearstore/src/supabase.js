import { createClient } from '@supabase/supabase-js'

// Instancez speaks the same HTTP shape as Supabase, so we can create a
// standard supabase-js client against it. `VITE_INSTANCEZ_URL` must be the
// server root (no /api or /rest/v1 suffix) — supabase-js adds the /auth/v1
// and /rest/v1 prefixes itself.
const URL = import.meta.env.VITE_INSTANCEZ_URL || window.location.origin

// supabase-js requires an API key even for unauthenticated requests. This is
// the publishable key: client-safe, maps to role=anon, and table grants plus
// RLS policies decide access from there. It ships in client apps and is rotated
// like a public credential.
const PUBLISHABLE_KEY = import.meta.env.VITE_INSTANCEZ_PUBLISHABLE_KEY ?? 'inz_publishable_demo'

export const supabase = createClient(URL, PUBLISHABLE_KEY, {
  auth: {
    persistSession: true,
    autoRefreshToken: true,
    storage: window.localStorage,
    storageKey: 'instancez-catalog-auth',
  },
})

// DEV-ONLY: the magic-link and email-OTP tabs need to read back the
// token that would normally be delivered by email. They call
// /auth/v1/admin/generate_link with the secret key to fetch a fresh one.
// This value is only wired through VITE_INSTANCEZ_SECRET_KEY for the compose
// demo — a production deployment must never expose the secret key to the
// browser.
export const DEV_SECRET_KEY = import.meta.env.VITE_INSTANCEZ_SECRET_KEY || ''
export const INSTANCEZ_URL = URL
