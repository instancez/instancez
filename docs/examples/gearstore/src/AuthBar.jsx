import { useEffect, useState } from 'react'
import { supabase, DEV_SECRET_KEY, INSTANCEZ_URL } from './supabase.js'

// AuthBar exercises the full auth surface of supabase-js against
// Instancez:
//   - supabase.auth.signUp / signInWithPassword              (password)
//   - supabase.auth.signInWithOtp + verifyOtp({ token_hash }) (magic link)
//   - supabase.auth.signInWithOtp + verifyOtp({ token, type:'email' }) (6-digit OTP)
//   - supabase.auth.signInAnonymously                         (guest)
//   - supabase.auth.signInWithOAuth({ provider })              (google, github)
//   - supabase.auth.signOut / getSession / onAuthStateChange  (lifecycle)
//
// Instancez has no email provider configured in this demo, so the
// magic-link and OTP tabs fetch their token via the admin generate_link
// endpoint (dev-only) and then hand it back to supabase-js to complete
// the verification — this keeps the client-side call shape identical to
// what a real Supabase app would look like while still being runnable
// locally without SMTP.
const TABS = [
  { id: 'password', label: 'Password' },
  { id: 'magiclink', label: 'Magic link' },
  { id: 'emailotp', label: 'Email OTP' },
  { id: 'guest', label: 'Guest' },
  { id: 'google', label: 'Google', oauthProvider: 'google' },
  { id: 'github', label: 'GitHub', oauthProvider: 'github' },
]

export function AuthBar({ variant = 'bar' }) {
  const [session, setSession] = useState(null)
  const [tab, setTab] = useState('password')
  const [mode, setMode] = useState('signin') // signin | signup (password tab)
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [code, setCode] = useState('')
  const [notice, setNotice] = useState(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(null)

  useEffect(() => {
    supabase.auth.getSession().then(({ data }) => setSession(data.session))
    const { data: sub } = supabase.auth.onAuthStateChange((_event, s) => {
      setSession(s)
    })
    return () => sub.subscription.unsubscribe()
  }, [])

  function resetFormState() {
    setEmail('')
    setPassword('')
    setDisplayName('')
    setCode('')
    setNotice(null)
    setError(null)
  }

  async function handlePasswordSubmit(e) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      if (mode === 'signup') {
        const { error } = await supabase.auth.signUp({
          email,
          password,
          options: { data: { display_name: displayName || email.split('@')[0] } },
        })
        if (error) throw error
      } else {
        const { error } = await supabase.auth.signInWithPassword({ email, password })
        if (error) throw error
      }
      resetFormState()
    } catch (err) {
      setError(err.message || String(err))
    } finally {
      setBusy(false)
    }
  }

  // devGenerateLink fetches a fresh magic-link token/code from the
  // admin endpoint. In a real app an email provider would deliver this;
  // here we surface it client-side so the whole flow can be demonstrated.
  async function devGenerateLink(type) {
    const resp = await fetch(`${INSTANCEZ_URL}/auth/v1/admin/generate_link`, {
      method: 'POST',
      headers: {
        apikey: DEV_SECRET_KEY,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({ type, email }),
    })
    if (!resp.ok) {
      throw new Error(`admin.generate_link failed: ${await resp.text()}`)
    }
    return resp.json()
  }

  async function handleMagicLink(e) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    setNotice(null)
    try {
      // This is the real supabase-js entry point — the server accepts
      // it, writes a token row, and would normally email the link.
      const { error } = await supabase.auth.signInWithOtp({ email })
      if (error) throw error
      if (!DEV_SECRET_KEY) {
        setNotice('Magic link dispatched. Check your inbox to sign in.')
        return
      }
      // Dev-only: fetch the same token we just dispatched and hand it to
      // verifyOtp so the user doesn't need to click an email link.
      const link = await devGenerateLink('magiclink')
      const { error: vErr } = await supabase.auth.verifyOtp({
        token_hash: link.hashed_token,
        type: 'magiclink',
      })
      if (vErr) throw vErr
      resetFormState()
    } catch (err) {
      setError(err.message || String(err))
    } finally {
      setBusy(false)
    }
  }

  async function handleOtpRequest(e) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    setNotice(null)
    try {
      const { error } = await supabase.auth.signInWithOtp({ email })
      if (error) throw error
      if (!DEV_SECRET_KEY) {
        setNotice('6-digit code sent. Enter it below to complete sign-in.')
        return
      }
      // Dev-only: peek at the OTP and prefill it.
      const link = await devGenerateLink('magiclink')
      setCode(link.email_otp || '')
      setNotice(
        `Dev helper prefilled code ${link.email_otp}. In production this would arrive by email.`,
      )
    } catch (err) {
      setError(err.message || String(err))
    } finally {
      setBusy(false)
    }
  }

  async function handleOtpVerify(e) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const { error } = await supabase.auth.verifyOtp({
        email,
        token: code,
        type: 'email',
      })
      if (error) throw error
      resetFormState()
    } catch (err) {
      setError(err.message || String(err))
    } finally {
      setBusy(false)
    }
  }

  async function handleGuest() {
    setBusy(true)
    setError(null)
    try {
      const { error } = await supabase.auth.signInAnonymously()
      if (error) throw error
      resetFormState()
    } catch (err) {
      setError(err.message || String(err))
    } finally {
      setBusy(false)
    }
  }

  // Redirects the whole page to the provider; the session comes back in the
  // URL fragment on return, which supabase-js picks up automatically
  // (detectSessionInUrl is on by default), so there's nothing to await here.
  async function handleOAuth(provider) {
    setBusy(true)
    setError(null)
    try {
      const { error } = await supabase.auth.signInWithOAuth({
        provider,
        options: { redirectTo: window.location.origin },
      })
      if (error) throw error
    } catch (err) {
      setError(err.message || String(err))
      setBusy(false)
    }
  }

  async function handleSignOut() {
    setBusy(true)
    setError(null)
    try {
      const { error } = await supabase.auth.signOut()
      if (error) throw error
    } catch (err) {
      setError(err.message || String(err))
    } finally {
      setBusy(false)
    }
  }

  if (session?.user) {
    const u = session.user
    const isAnon = u.is_anonymous || u.app_metadata?.provider === 'anonymous'
    const aal = u.app_metadata?.aal || 'aal1'
    const name = isAnon
      ? 'Guest'
      : u.user_metadata?.display_name || u.email || u.id
    return (
      <aside className="authbar signed-in">
        <div>
          Signed in as <strong>{name}</strong>{' '}
          {isAnon ? (
            <span className="badge stock">anonymous</span>
          ) : (
            <span className="muted">({u.email})</span>
          )}{' '}
          <span className={`badge ${aal === 'aal2' ? 'featured' : ''}`}>
            {aal}
          </span>
        </div>
        <button disabled={busy} onClick={handleSignOut}>
          Sign out
        </button>
        {error && <div className="error inline">{error}</div>}
      </aside>
    )
  }

  const wrapperClass = variant === 'page' ? 'login-card' : 'authbar'

  return (
    <aside className={wrapperClass}>
      {variant === 'page' && (
        <div className="login-header">
          <h1>instancez × Gearstore</h1>
          <p className="hint">
            Four supabase-js auth flows, all wired to the same instancez
            backend.
          </p>
        </div>
      )}
      <div className="tabs auth-tabs">
        {TABS.map((t) => (
          <button
            key={t.id}
            type="button"
            className={tab === t.id ? 'active' : ''}
            onClick={() => {
              setTab(t.id)
              resetFormState()
            }}
          >
            {t.label}
          </button>
        ))}
      </div>

      {tab === 'password' && (
        <form className="login-form" onSubmit={handlePasswordSubmit}>
          <div className="subtabs">
            <button
              type="button"
              className={mode === 'signin' ? 'active' : ''}
              onClick={() => setMode('signin')}
            >
              Sign in
            </button>
            <button
              type="button"
              className={mode === 'signup' ? 'active' : ''}
              onClick={() => setMode('signup')}
            >
              Sign up
            </button>
          </div>
          <label>
            Email
            <input
              type="email"
              placeholder="you@example.com"
              autoComplete="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              required
            />
          </label>
          <label>
            Password
            <input
              type="password"
              placeholder="at least 8 characters"
              autoComplete={mode === 'signin' ? 'current-password' : 'new-password'}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              minLength={8}
              required
            />
          </label>
          {mode === 'signup' && (
            <label>
              Display name <span className="muted">(optional)</span>
              <input
                type="text"
                placeholder="how we'll greet you"
                value={displayName}
                onChange={(e) => setDisplayName(e.target.value)}
              />
            </label>
          )}
          <button type="submit" disabled={busy}>
            {busy ? 'Working…' : mode === 'signup' ? 'Create account' : 'Sign in'}
          </button>
        </form>
      )}

      {tab === 'magiclink' && (
        <form className="login-form" onSubmit={handleMagicLink}>
          <p className="hint small">
            Calls <code>supabase.auth.signInWithOtp</code>. In production the
            server would email a link; this demo fetches the token via the
            admin endpoint and verifies it immediately.
          </p>
          <label>
            Email
            <input
              type="email"
              placeholder="you@example.com"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              required
            />
          </label>
          <button type="submit" disabled={busy}>
            {busy ? 'Working…' : 'Send magic link & sign in'}
          </button>
        </form>
      )}

      {tab === 'emailotp' && (
        <form className="login-form" onSubmit={code ? handleOtpVerify : handleOtpRequest}>
          <p className="hint small">
            Two-step 6-digit code flow backed by{' '}
            <code>supabase.auth.verifyOtp(&#123;type:'email'&#125;)</code>.
          </p>
          <label>
            Email
            <input
              type="email"
              placeholder="you@example.com"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              required
            />
          </label>
          <label>
            6-digit code
            <input
              type="text"
              inputMode="numeric"
              pattern="[0-9]{6}"
              maxLength={6}
              placeholder="000000"
              value={code}
              onChange={(e) => setCode(e.target.value)}
            />
          </label>
          <button type="submit" disabled={busy || !email}>
            {busy ? 'Working…' : code ? 'Verify code' : 'Request code'}
          </button>
        </form>
      )}

      {tab === 'guest' && (
        <div className="login-form">
          <p className="hint small">
            Issues an anonymous session via{' '}
            <code>supabase.auth.signInAnonymously()</code>. The resulting
            JWT has <code>is_anonymous: true</code> and no email.
            Anonymous users can browse the catalog but can't post reviews
            (RLS requires <code>user_id = auth.uid()</code> and a real
            authenticated role).
          </p>
          <button type="button" disabled={busy} onClick={handleGuest}>
            {busy ? 'Working…' : 'Continue as guest'}
          </button>
        </div>
      )}

      {TABS.find((t) => t.id === tab)?.oauthProvider && (
        <div className="login-form">
          <p className="hint small">
            Calls{' '}
            <code>
              supabase.auth.signInWithOAuth({'{'}provider: '
              {TABS.find((t) => t.id === tab).oauthProvider}'{'}'})
            </code>
            , which redirects to the provider and back through instancez's
            OAuth callback. Returns as a normal session once you land back
            here.
          </p>
          <button
            type="button"
            disabled={busy}
            onClick={() => handleOAuth(TABS.find((t) => t.id === tab).oauthProvider)}
          >
            {busy ? 'Redirecting…' : `Continue with ${TABS.find((t) => t.id === tab).label}`}
          </button>
        </div>
      )}

      {notice && <div className="notice inline">{notice}</div>}
      {error && <div className="error inline">{error}</div>}
      {variant === 'page' && (
        <p className="login-foot hint small">
          Backed by instancez auth via <code>@supabase/supabase-js</code>.
        </p>
      )}
    </aside>
  )
}

// useSession is a tiny hook other components use to read the live session.
export function useSession() {
  const [session, setSession] = useState(null)
  useEffect(() => {
    supabase.auth.getSession().then(({ data }) => setSession(data.session))
    const { data: sub } = supabase.auth.onAuthStateChange((_e, s) => setSession(s))
    return () => sub.subscription.unsubscribe()
  }, [])
  return session
}
