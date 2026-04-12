import { useEffect, useState } from 'react'
import { supabase } from './supabase.js'

// AuthBar exercises the auth surface of supabase-js against Ultrabase:
//   - supabase.auth.onAuthStateChange  (live session subscription)
//   - supabase.auth.getSession         (rehydrate on mount)
//   - supabase.auth.signUp             (POST /auth/v1/signup)
//   - supabase.auth.signInWithPassword (POST /auth/v1/token?grant_type=password)
//   - supabase.auth.signOut            (POST /auth/v1/logout)
export function AuthBar({ variant = 'bar' }) {
  const [session, setSession] = useState(null)
  const [mode, setMode] = useState('signin') // signin | signup
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(null)

  useEffect(() => {
    supabase.auth.getSession().then(({ data }) => setSession(data.session))
    const { data: sub } = supabase.auth.onAuthStateChange((_event, s) => {
      setSession(s)
    })
    return () => sub.subscription.unsubscribe()
  }, [])

  async function handleSubmit(e) {
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
      setEmail('')
      setPassword('')
      setDisplayName('')
    } catch (err) {
      setError(err.message || String(err))
    } finally {
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
    const name = session.user.user_metadata?.display_name || session.user.email
    return (
      <aside className="authbar signed-in">
        <div>
          Signed in as <strong>{name}</strong>{' '}
          <span className="muted">({session.user.email})</span>
        </div>
        <button disabled={busy} onClick={handleSignOut}>
          Sign out
        </button>
        {error && <div className="error inline">{error}</div>}
      </aside>
    )
  }

  if (variant === 'page') {
    return (
      <div className="login-card">
        <div className="login-header">
          <h1>Ultrabase × React Catalog</h1>
          <p className="hint">
            Sign in or create an account to browse the catalog.
          </p>
        </div>
        <div className="tabs">
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
        <form className="login-form" onSubmit={handleSubmit}>
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
          {error && <div className="error inline">{error}</div>}
        </form>
        <p className="login-foot hint small">
          Backed by Ultrabase auth via <code>@supabase/supabase-js</code>.
        </p>
      </div>
    )
  }

  return (
    <aside className="authbar">
      <form onSubmit={handleSubmit}>
        <div className="tabs">
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
        <input
          type="email"
          placeholder="you@example.com"
          autoComplete="email"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          required
        />
        <input
          type="password"
          placeholder="password (min 8 chars)"
          autoComplete={mode === 'signin' ? 'current-password' : 'new-password'}
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          minLength={8}
          required
        />
        {mode === 'signup' && (
          <input
            type="text"
            placeholder="display name (optional)"
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
          />
        )}
        <button type="submit" disabled={busy}>
          {busy ? 'Working…' : mode === 'signup' ? 'Create account' : 'Sign in'}
        </button>
      </form>
      {error && <div className="error inline">{error}</div>}
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
