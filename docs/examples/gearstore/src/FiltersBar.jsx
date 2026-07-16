export function FiltersBar({
  filters,
  setFilters,
  categories,
  suggestedTags,
  onReset,
}) {
  const set = (patch) => setFilters((f) => ({ ...f, ...patch }))

  return (
    <section className="filters">
      <div className="row">
        <label className="grow">
          <span>Search (websearch FTS)</span>
          <input
            type="search"
            placeholder='e.g. "split keyboard" or wireless'
            value={filters.search}
            onChange={(e) => set({ search: e.target.value })}
          />
        </label>
        <label>
          <span>Category</span>
          <select
            value={filters.categoryId}
            onChange={(e) => set({ categoryId: e.target.value })}
          >
            <option value="">All</option>
            {categories.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </select>
        </label>
        <label>
          <span>Sort</span>
          <select
            value={filters.sort}
            onChange={(e) => set({ sort: e.target.value })}
          >
            <option value="featured_first">Featured first</option>
            <option value="newest">Newest</option>
            <option value="price_asc">Price ↑</option>
            <option value="price_desc">Price ↓</option>
            <option value="name_asc">Name A–Z</option>
          </select>
        </label>
      </div>

      <div className="row">
        <label>
          <span>Min $</span>
          <input
            type="number"
            min="0"
            value={filters.minPrice}
            onChange={(e) => set({ minPrice: e.target.value })}
          />
        </label>
        <label>
          <span>Max $</span>
          <input
            type="number"
            min="0"
            value={filters.maxPrice}
            onChange={(e) => set({ maxPrice: e.target.value })}
          />
        </label>
        <label>
          <span>Tag (array contains)</span>
          <input
            list="tag-suggestions"
            value={filters.tag}
            onChange={(e) => set({ tag: e.target.value })}
            placeholder="e.g. wireless"
          />
          <datalist id="tag-suggestions">
            {suggestedTags.map((t) => (
              <option key={t} value={t} />
            ))}
          </datalist>
        </label>
        <label>
          <span>Brand (JSONB)</span>
          <input
            value={filters.brand}
            onChange={(e) => set({ brand: e.target.value })}
            placeholder="metadata->>brand"
          />
        </label>
      </div>

      <div className="row toggles">
        <label className="check">
          <input
            type="checkbox"
            checked={filters.inStockOnly}
            onChange={(e) => set({ inStockOnly: e.target.checked })}
          />
          In stock only
        </label>
        <label className="check">
          <input
            type="checkbox"
            checked={filters.onSaleOrFeatured}
            onChange={(e) => set({ onSaleOrFeatured: e.target.checked })}
          />
          On sale OR featured
        </label>
        <button type="button" className="reset" onClick={onReset}>
          Reset filters
        </button>
      </div>
    </section>
  )
}
