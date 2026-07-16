import { useState } from 'react'
import { supabase } from './supabase.js'

const BUCKET = 'product_images'

function priceDollars(cents) {
  return (cents / 100).toLocaleString(undefined, {
    style: 'currency',
    currency: 'USD',
  })
}

function avgRating(reviews) {
  if (!reviews || reviews.length === 0) return null
  const sum = reviews.reduce((a, r) => a + r.rating, 0)
  return (sum / reviews.length).toFixed(1)
}

export function productImageUrl(productId) {
  const { data } = supabase.storage.from(BUCKET).getPublicUrl(`${productId}`)
  return data?.publicUrl ?? ''
}

export function ProductCard({ product, onOpen, imageVersion }) {
  const [imgFailed, setImgFailed] = useState(false)
  const avg = avgRating(product.reviews)
  const imgSrc = productImageUrl(product.id) + (imageVersion ? `?v=${imageVersion}` : '')
  return (
    <article className="card" onClick={onOpen}>
      {imgFailed ? (
        <div className="card-img-placeholder">No image</div>
      ) : (
        <img
          src={imgSrc}
          alt={product.name}
          className="card-img"
          onError={() => setImgFailed(true)}
        />
      )}
      <div className="card-head">
        <h3>{product.name}</h3>
        <div className="price">{priceDollars(product.price_cents)}</div>
      </div>

      <div className="badges">
        {product.featured && <span className="badge featured">Featured</span>}
        {product.on_sale && <span className="badge sale">On sale</span>}
        {product.stock === 0 ? (
          <span className="badge oos">Out of stock</span>
        ) : (
          <span className="badge stock">{product.stock} in stock</span>
        )}
      </div>

      <p className="desc">{product.description}</p>

      <div className="meta">
        <span>{product.category?.name ?? '—'}</span>
        {product.metadata?.brand && <span>· {product.metadata.brand}</span>}
        {avg && (
          <span>
            · ★ {avg} ({product.reviews.length})
          </span>
        )}
      </div>

      {product.tags?.length > 0 && (
        <div className="tags">
          {product.tags.map((t) => (
            <span key={t} className="tag">
              {t}
            </span>
          ))}
        </div>
      )}
    </article>
  )
}
