import { useCallback, useEffect, useState } from 'react'
import { supabase } from './supabase.js'

// SecurityPanel drives the TOTP MFA endpoints via supabase-js:
//
//   supabase.auth.mfa.enroll({ factorType:'totp', friendlyName })
//   supabase.auth.mfa.listFactors()
//   supabase.auth.mfa.challenge({ factorId })
//   supabase.auth.mfa.verify({ factorId, challengeId, code })
//   supabase.auth.mfa.unenroll({ factorId })
//
// A successful verify against an unverified factor flips the row to
// verified and re-issues a session JWT with aal=aal2 in app_metadata —
// the AuthBar reads that claim back to render the "aal2" badge.
export function SecurityPanel({ session }) {
  const [factors, setFactors] = useState([])
  const [enrolling, setEnrolling] = useState(null)
  const [friendlyName, setFriendlyName] = useState('')
  const [code, setCode] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(null)
  const [notice, setNotice] = useState(null)

  const refresh = useCallback(async () => {
    setError(null)
    const { data, error } = await supabase.auth.mfa.listFactors()
    if (error) {
      setError(error.message)
      return
    }
    // supabase-js normalises to { totp, phone, all }; Instancez returns
    // { totp, phone } directly. Prefer `all` if present, else merge.
    const list = data?.all ?? [...(data?.totp ?? []), ...(data?.phone ?? [])]
    setFactors(list)
  }, [])

  useEffect(() => {
    refresh()
  }, [refresh])

  async function handleEnroll(e) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    setNotice(null)
    try {
      const { data, error } = await supabase.auth.mfa.enroll({
        factorType: 'totp',
        friendlyName: friendlyName || 'Authenticator',
      })
      if (error) throw error
      setEnrolling({
        factorId: data.id,
        secret: data.totp?.secret,
        uri: data.totp?.uri,
        qrCode: data.totp?.qr_code,
      })
      setFriendlyName('')
      await refresh()
    } catch (err) {
      setError(err.message || String(err))
    } finally {
      setBusy(false)
    }
  }

  async function handleVerify(e) {
    e.preventDefault()
    if (!enrolling) return
    setBusy(true)
    setError(null)
    try {
      const { data: ch, error: chErr } = await supabase.auth.mfa.challenge({
        factorId: enrolling.factorId,
      })
      if (chErr) throw chErr
      const { error: vErr } = await supabase.auth.mfa.verify({
        factorId: enrolling.factorId,
        challengeId: ch.id,
        code,
      })
      if (vErr) throw vErr
      setNotice('Factor verified — session upgraded to aal2.')
      setEnrolling(null)
      setCode('')
      await refresh()
    } catch (err) {
      setError(err.message || String(err))
    } finally {
      setBusy(false)
    }
  }

  async function handleUnenroll(factorId) {
    setBusy(true)
    setError(null)
    setNotice(null)
    try {
      const { error } = await supabase.auth.mfa.unenroll({ factorId })
      if (error) throw error
      if (enrolling?.factorId === factorId) setEnrolling(null)
      await refresh()
    } catch (err) {
      setError(err.message || String(err))
    } finally {
      setBusy(false)
    }
  }

  const aal = session?.user?.app_metadata?.aal || 'aal1'

  return (
    <section className="security">
      <h2>Security · MFA</h2>
      <p className="hint small">
        Current assurance level: <code>{aal}</code>. Enroll a TOTP factor
        and verify a code to upgrade the session to <code>aal2</code>.
      </p>

      {factors.length > 0 && (
        <ul className="factors">
          {factors.map((f) => (
            <li key={f.id}>
              <div>
                <strong>{f.friendly_name || '(unnamed)'}</strong>{' '}
                <span className="muted">· {f.factor_type || 'totp'}</span>{' '}
                <span
                  className={`badge ${
                    f.status === 'verified' ? 'stock' : 'oos'
                  }`}
                >
                  {f.status}
                </span>
              </div>
              <button
                type="button"
                disabled={busy}
                onClick={() => handleUnenroll(f.id)}
              >
                Remove
              </button>
            </li>
          ))}
        </ul>
      )}

      {!enrolling && (
        <form className="login-form" onSubmit={handleEnroll}>
          <label>
            Friendly name
            <input
              type="text"
              placeholder="Phone, Yubikey, 1Password…"
              value={friendlyName}
              onChange={(e) => setFriendlyName(e.target.value)}
            />
          </label>
          <button type="submit" disabled={busy}>
            {busy ? 'Working…' : 'Enroll TOTP factor'}
          </button>
        </form>
      )}

      {enrolling && (
        <form className="login-form" onSubmit={handleVerify}>
          <p className="hint small">
            Scan the URI with an authenticator app (Google Authenticator,
            1Password, Authy…), or paste the secret manually. Then enter
            the 6-digit code to complete enrollment.
          </p>
          <label>
            otpauth URI
            <input type="text" readOnly value={enrolling.uri || ''} />
          </label>
          <label>
            Secret
            <input type="text" readOnly value={enrolling.secret || ''} />
          </label>
          <label>
            6-digit code
            <input
              type="text"
              inputMode="numeric"
              pattern="[0-9]{6}"
              maxLength={6}
              value={code}
              onChange={(e) => setCode(e.target.value)}
              required
            />
          </label>
          <div className="row">
            <button type="submit" disabled={busy || code.length !== 6}>
              {busy ? 'Verifying…' : 'Verify & activate'}
            </button>
            <button
              type="button"
              disabled={busy}
              onClick={() => handleUnenroll(enrolling.factorId)}
            >
              Cancel
            </button>
          </div>
        </form>
      )}

      {notice && <div className="notice inline">{notice}</div>}
      {error && <div className="error inline">{error}</div>}
    </section>
  )
}
