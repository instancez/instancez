import { useCallback, useEffect, useState } from 'react'
import { supabase } from './supabase.js'
import { useSession } from './AuthBar.jsx'

export function ProductDetail({ productId, onClose }) {
  const session = useSession()
  const [product, setProduct] = useState(null)
  const [minRating, setMinRating] = useState(1)
  const [error, setError] = useState(null)

  const load = useCallback(async () => {
    setError(null)
    // Demonstrates embed-scoped filters and order:
    // reviews.rating=gte.<n>, reviews.order=rating.desc, reviews.limit=5.
    // Also: !inner on category, alias (category:categories), and a cast
    // (price_numeric:price_cents::numeric) turned into a client-friendly shape.
    const { data, error } = await supabase
      .from('products')
      .select(
        `
        id,
        name,
        description,
        status,
        featured,
        on_sale,
        stock,
        tags,
        metadata,
        price_numeric:price_cents::numeric,
        category:categories!inner(id,slug,name),
        reviews(id,author,rating,body,created_at,user_id)
        `,
      )
      .eq('id', productId)
      .gte('reviews.rating', minRating)
      .order('rating', { ascending: false, foreignTable: 'reviews' })
      .limit(5, { foreignTable: 'reviews' })
      .single()

    if (error) setError(error.message)
    else setProduct(data)
  }, [productId, minRating])

  useEffect(() => {
    let cancelled = false
    ;(async () => {
      await load()
      if (cancelled) setProduct(null)
    })()
    return () => {
      cancelled = true
    }
  }, [load])

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div className="modal" onClick={(e) => e.stopPropagation()}>
        <button className="close" onClick={onClose}>
          ×
        </button>
        {error && <div className="error">{error}</div>}
        {!product ? (
          <p>Loading…</p>
        ) : (
          <>
            <h2>{product.name}</h2>
            <p className="meta">
              {product.category?.name} ·{' '}
              {Number(product.price_numeric / 100).toLocaleString(undefined, {
                style: 'currency',
                currency: 'USD',
              })}{' '}
              · {product.stock} in stock
            </p>
            <p>{product.description}</p>

            {product.metadata && (
              <div className="metadata">
                <strong>Metadata</strong>
                <pre>{JSON.stringify(product.metadata, null, 2)}</pre>
              </div>
            )}

            <div className="reviews-head">
              <strong>Reviews</strong>
              <label>
                min rating:{' '}
                <select
                  value={minRating}
                  onChange={(e) => setMinRating(Number(e.target.value))}
                >
                  {[1, 2, 3, 4, 5].map((n) => (
                    <option key={n} value={n}>
                      ≥ {n}
                    </option>
                  ))}
                </select>
              </label>
            </div>
            <ul className="reviews">
              {(product.reviews ?? []).map((r) => (
                <ReviewItem
                  key={r.id}
                  review={r}
                  session={session}
                  onChanged={load}
                />
              ))}
              {(product.reviews ?? []).length === 0 && (
                <li className="empty">No reviews at this threshold.</li>
              )}
            </ul>

            <ReviewComposer
              productId={product.id}
              session={session}
              onCreated={load}
            />
          </>
        )}
      </div>
    </div>
  )
}

function ReviewItem({ review, session, onChanged }) {
  const [editing, setEditing] = useState(false)
  const [body, setBody] = useState(review.body ?? '')
  const [rating, setRating] = useState(review.rating)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(null)

  const mine = !!session?.user && session.user.id === review.user_id

  async function save() {
    setBusy(true)
    setError(null)
    // UPDATE /rest/v1/reviews?id=eq.<id> — RLS restricts this to the owner
    const { error } = await supabase
      .from('reviews')
      .update({ body, rating })
      .eq('id', review.id)
    setBusy(false)
    if (error) setError(error.message)
    else {
      setEditing(false)
      onChanged()
    }
  }

  async function remove() {
    if (!confirm('Delete this review?')) return
    setBusy(true)
    setError(null)
    const { error } = await supabase.from('reviews').delete().eq('id', review.id)
    setBusy(false)
    if (error) setError(error.message)
    else onChanged()
  }

  if (editing) {
    return (
      <li>
        <div className="review-edit">
          <select
            value={rating}
            onChange={(e) => setRating(Number(e.target.value))}
          >
            {[1, 2, 3, 4, 5].map((n) => (
              <option key={n} value={n}>
                ★ {n}
              </option>
            ))}
          </select>
          <textarea value={body} onChange={(e) => setBody(e.target.value)} />
          <div className="row">
            <button disabled={busy} onClick={save}>
              Save
            </button>
            <button disabled={busy} onClick={() => setEditing(false)}>
              Cancel
            </button>
          </div>
          {error && <div className="error inline">{error}</div>}
        </div>
      </li>
    )
  }

  return (
    <li>
      <div>
        <strong>{review.author}</strong> · ★ {review.rating}
        {mine && <span className="muted"> · yours</span>}
      </div>
      {review.body && <p>{review.body}</p>}
      {mine && (
        <div className="row">
          <button onClick={() => setEditing(true)}>Edit</button>
          <button onClick={remove}>Delete</button>
        </div>
      )}
    </li>
  )
}

function ReviewComposer({ productId, session, onCreated }) {
  const [rating, setRating] = useState(5)
  const [body, setBody] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(null)

  if (!session?.user) {
    return (
      <p className="hint small">Sign in above to post a review.</p>
    )
  }

  async function submit(e) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    const author =
      session.user.user_metadata?.display_name ||
      session.user.email?.split('@')[0] ||
      'anon'
    // INSERT /rest/v1/reviews — RLS requires user_id = auth.uid().
    // supabase-js's .select() on an insert returns the created row(s).
    const { error } = await supabase
      .from('reviews')
      .insert({
        product_id: productId,
        author,
        rating,
        body,
        user_id: session.user.id,
      })
      .select()
      .single()
    setBusy(false)
    if (error) {
      setError(error.message)
      return
    }
    setBody('')
    setRating(5)
    onCreated()
  }

  return (
    <form className="composer" onSubmit={submit}>
      <div className="row">
        <label>
          <span>Rating</span>
          <select
            value={rating}
            onChange={(e) => setRating(Number(e.target.value))}
          >
            {[1, 2, 3, 4, 5].map((n) => (
              <option key={n} value={n}>
                ★ {n}
              </option>
            ))}
          </select>
        </label>
        <label className="grow">
          <span>Review</span>
          <textarea
            value={body}
            onChange={(e) => setBody(e.target.value)}
            placeholder="What did you think?"
            required
          />
        </label>
      </div>
      <button type="submit" disabled={busy}>
        {busy ? 'Posting…' : 'Post review'}
      </button>
      {error && <div className="error inline">{error}</div>}
    </form>
  )
}
