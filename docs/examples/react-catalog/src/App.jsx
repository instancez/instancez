import { useEffect, useMemo, useState } from 'react'
import { supabase } from './supabase.js'
import { AuthBar, useSession } from './AuthBar.jsx'
import { ProductCard } from './ProductCard.jsx'
import { ProductDetail } from './ProductDetail.jsx'
import { FiltersBar } from './FiltersBar.jsx'
import { CatalogStats } from './CatalogStats.jsx'
import { SecurityPanel } from './SecurityPanel.jsx'
import { StoragePanel } from './StoragePanel.jsx'

const PAGE_SIZE = 6

const SORTS = {
  newest: [{ col: 'created_at', asc: false }],
  price_asc: [{ col: 'price_cents', asc: true }],
  price_desc: [{ col: 'price_cents', asc: false }],
  name_asc: [{ col: 'name', asc: true }],
  featured_first: [
    { col: 'featured', asc: false },
    { col: 'created_at', asc: false },
  ],
}

const DEFAULT_FILTERS = {
  search: '',
  categoryId: '',
  minPrice: '',
  maxPrice: '',
  inStockOnly: false,
  onSaleOrFeatured: false,
  tag: '',
  brand: '',
  sort: 'featured_first',
}

export default function App() {
  const session = useSession()
  const signedIn = !!session?.user

  if (!signedIn) {
    return (
      <div className="login-page">
        <AuthBar variant="page" />
      </div>
    )
  }

  return <Catalog session={session} />
}

function Catalog({ session }) {
  const isAnon =
    session?.user?.is_anonymous ||
    session?.user?.app_metadata?.provider === 'anonymous'
  const [categories, setCategories] = useState([])
  const [products, setProducts] = useState([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(0)
  const [filters, setFilters] = useState(DEFAULT_FILTERS)
  const [selected, setSelected] = useState(null)
  const [error, setError] = useState(null)
  const [loading, setLoading] = useState(true)
  const [lastURL, setLastURL] = useState('')

  useEffect(() => {
    supabase
      .from('categories')
      .select('id,slug,name')
      .order('name', { ascending: true })
      .then(({ data, error }) => {
        if (error) setError(error.message)
        else setCategories(data ?? [])
      })
  }, [])

  useEffect(() => {
    setPage(0)
  }, [filters])

  useEffect(() => {
    let cancelled = false
    async function run() {
      setLoading(true)
      setError(null)

      let q = supabase
        .from('products')
        .select(
          `
          id,
          name,
          description,
          price_cents,
          stock,
          status,
          featured,
          on_sale,
          tags,
          metadata,
          created_at,
          category:categories!left(id,slug,name),
          reviews(id,author,rating,body,created_at)
          `,
          { count: 'exact' },
        )
        .eq('status', 'active')

      if (filters.search.trim()) {
        q = q.textSearch('name', filters.search.trim(), { type: 'websearch' })
      }
      if (filters.categoryId) {
        q = q.eq('category_id', Number(filters.categoryId))
      }
      if (filters.minPrice !== '') {
        q = q.gte('price_cents', Math.round(Number(filters.minPrice) * 100))
      }
      if (filters.maxPrice !== '') {
        q = q.lte('price_cents', Math.round(Number(filters.maxPrice) * 100))
      }
      if (filters.inStockOnly) {
        q = q.gt('stock', 0)
      }
      if (filters.onSaleOrFeatured) {
        q = q.or('on_sale.eq.true,featured.eq.true')
      }
      if (filters.tag.trim()) {
        q = q.contains('tags', [filters.tag.trim()])
      }
      if (filters.brand.trim()) {
        q = q.eq('metadata->>brand', filters.brand.trim())
      }

      for (const o of SORTS[filters.sort]) {
        q = q.order(o.col, { ascending: o.asc })
      }

      const from = page * PAGE_SIZE
      const to = from + PAGE_SIZE - 1
      q = q.range(from, to)

      const url = decodeURIComponent(q.url?.toString?.() ?? '')
      if (!cancelled) setLastURL(url)

      const { data, error, count } = await q
      if (cancelled) return
      if (error) {
        setError(error.message)
        setProducts([])
        setTotal(0)
      } else {
        setProducts(data ?? [])
        setTotal(count ?? 0)
      }
      setLoading(false)
    }
    run()
    return () => {
      cancelled = true
    }
  }, [filters, page])

  const pageCount = Math.max(1, Math.ceil(total / PAGE_SIZE))

  const suggestedTags = useMemo(() => {
    const s = new Set()
    for (const p of products) (p.tags ?? []).forEach((t) => s.add(t))
    return [...s].sort()
  }, [products])

  const [imageVersion, setImageVersion] = useState(0)

  return (
    <main>
      <header>
        <h1>Ultrabase × React Catalog</h1>
        <p className="hint">
          A React app driving Ultrabase with{' '}
          <code>@supabase/supabase-js</code>: full-text search, JSONB filters,
          array contains, <code>or=()</code> logic, embeds with scoped filters,
          range pagination, and an authenticated reviews flow (sign up / sign
          in, insert/update/delete under RLS).
        </p>
      </header>

      <AuthBar />

      <CatalogStats categories={categories} />

      {!isAnon && <SecurityPanel session={session} />}

      <StoragePanel />

      <FiltersBar
        filters={filters}
        setFilters={setFilters}
        categories={categories}
        suggestedTags={suggestedTags}
        onReset={() => setFilters(DEFAULT_FILTERS)}
      />

      {error && <div className="error">Error: {error}</div>}

      <div className="results-header">
        <span>
          {loading ? 'Loading…' : `${total} result${total === 1 ? '' : 's'}`}
        </span>
        <div className="pager">
          <button disabled={page === 0} onClick={() => setPage((p) => p - 1)}>
            ‹ Prev
          </button>
          <span>
            Page {page + 1} / {pageCount}
          </span>
          <button
            disabled={page + 1 >= pageCount}
            onClick={() => setPage((p) => p + 1)}
          >
            Next ›
          </button>
        </div>
      </div>

      <section className="grid">
        {products.map((p) => (
          <ProductCard key={p.id} product={p} onOpen={() => setSelected(p)} imageVersion={imageVersion} />
        ))}
        {!loading && products.length === 0 && (
          <div className="empty">No products match your filters.</div>
        )}
      </section>

      <details className="debug">
        <summary>Last PostgREST URL</summary>
        <code>{lastURL}</code>
      </details>

      {selected && (
        <ProductDetail
          productId={selected.id}
          onClose={() => setSelected(null)}
          onImageChange={() => setImageVersion((v) => v + 1)}
        />
      )}
    </main>
  )
}
