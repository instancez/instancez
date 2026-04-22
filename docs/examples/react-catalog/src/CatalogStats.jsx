import { useEffect, useState } from 'react'
import { supabase } from './supabase.js'

// CatalogStats exercises PostgREST aggregate select — both the "single
// row of aggregates" shape and the "group by" shape that Ultrabase
// infers automatically from mixing an unaggregated column with aggregate
// columns in the same select.
//
//   supabase.from('products')
//     .select('total:id.count(),avg:price_cents.avg(),min:price_cents.min(),max:price_cents.max(),stock:stock.sum()')
//
//   supabase.from('products')
//     .select('category_id,count:id.count(),avg:price_cents.avg()')  // GROUP BY category_id
//
// There is no client-side math here: the database returns the numbers
// and we just format them.
export function CatalogStats({ categories = [] }) {
  const [overall, setOverall] = useState(null)
  const [byCategory, setByCategory] = useState([])
  const [error, setError] = useState(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    let cancelled = false
    async function run() {
      setLoading(true)
      setError(null)
      try {
        const overallQ = supabase
          .from('products')
          .select(
            'total:id.count(),avg_cents:price_cents.avg(),min_cents:price_cents.min(),max_cents:price_cents.max(),stock_total:stock.sum()',
          )
          .eq('status', 'active')

        const byCatQ = supabase
          .from('products')
          .select(
            'category_id,count:id.count(),avg_cents:price_cents.avg()',
          )
          .eq('status', 'active')
          .order('count', { ascending: false })

        const [oRes, cRes] = await Promise.all([overallQ, byCatQ])
        if (cancelled) return
        if (oRes.error) throw oRes.error
        if (cRes.error) throw cRes.error
        setOverall(oRes.data?.[0] ?? null)
        setByCategory(cRes.data ?? [])
      } catch (err) {
        if (!cancelled) setError(err.message || String(err))
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    run()
    return () => {
      cancelled = true
    }
  }, [])

  const catName = (id) =>
    categories.find((c) => c.id === id)?.name ?? `#${id ?? '—'}`

  const fmtPrice = (cents) =>
    cents == null ? '—' : `$${(Number(cents) / 100).toFixed(2)}`

  return (
    <section className="stats">
      <h2>Catalog stats</h2>
      <p className="hint small">
        Aggregates returned directly by Ultrabase via{' '}
        <code>price_cents.avg()</code> / <code>id.count()</code> — no
        client-side math.
      </p>
      {error && <div className="error inline">{error}</div>}
      {loading && !overall ? (
        <div className="muted">Loading…</div>
      ) : (
        <>
          <div className="stat-grid">
            <Stat label="Active listings" value={overall?.total ?? 0} />
            <Stat label="Avg price" value={fmtPrice(overall?.avg_cents)} />
            <Stat label="Min price" value={fmtPrice(overall?.min_cents)} />
            <Stat label="Max price" value={fmtPrice(overall?.max_cents)} />
            <Stat label="Total stock" value={overall?.stock_total ?? 0} />
          </div>
          <table className="stat-table">
            <thead>
              <tr>
                <th>Category</th>
                <th>Listings</th>
                <th>Avg price</th>
              </tr>
            </thead>
            <tbody>
              {byCategory.map((row) => (
                <tr key={String(row.category_id)}>
                  <td>{catName(row.category_id)}</td>
                  <td>{row.count}</td>
                  <td>{fmtPrice(row.avg_cents)}</td>
                </tr>
              ))}
              {byCategory.length === 0 && (
                <tr>
                  <td colSpan={3} className="muted">
                    No active products.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </>
      )}
    </section>
  )
}

function Stat({ label, value }) {
  return (
    <div className="stat">
      <div className="stat-label">{label}</div>
      <div className="stat-value">{value}</div>
    </div>
  )
}
