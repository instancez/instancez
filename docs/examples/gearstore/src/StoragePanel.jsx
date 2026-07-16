import { useEffect, useState, useRef } from 'react'
import { supabase, INSTANCEZ_URL } from './supabase.js'

const BUCKET = 'product_images'

export function StoragePanel() {
  const [files, setFiles] = useState([])
  const [uploading, setUploading] = useState(false)
  const [error, setError] = useState(null)
  const [prefix, setPrefix] = useState('')
  const inputRef = useRef()

  async function loadFiles() {
    setError(null)
    const { data, error } = await supabase.storage
      .from(BUCKET)
      .list(prefix || '', { limit: 50, sortBy: { column: 'name', order: 'asc' } })
    if (error) {
      setError(error.message)
      setFiles([])
    } else {
      setFiles(data ?? [])
    }
  }

  useEffect(() => {
    loadFiles()
  }, [prefix])

  async function handleUpload(e) {
    e.preventDefault()
    const file = inputRef.current?.files?.[0]
    if (!file) return

    setUploading(true)
    setError(null)

    const path = (prefix ? prefix + '/' : '') + file.name
    const { error } = await supabase.storage
      .from(BUCKET)
      .upload(path, file, { upsert: true, contentType: file.type })

    setUploading(false)
    if (error) {
      setError(error.message)
    } else {
      inputRef.current.value = ''
      loadFiles()
    }
  }

  async function handleDelete(name) {
    const path = (prefix ? prefix + '/' : '') + name
    const { error } = await supabase.storage.from(BUCKET).remove([path])
    if (error) {
      setError(error.message)
    } else {
      loadFiles()
    }
  }

  function getPublicUrl(name) {
    const path = (prefix ? prefix + '/' : '') + name
    const { data } = supabase.storage.from(BUCKET).getPublicUrl(path)
    return data?.publicUrl ?? ''
  }

  async function handleDownload(name) {
    const path = (prefix ? prefix + '/' : '') + name
    const { data, error } = await supabase.storage.from(BUCKET).download(path)
    if (error) {
      setError(error.message)
      return
    }
    const url = URL.createObjectURL(data)
    const a = document.createElement('a')
    a.href = url
    a.download = name
    a.click()
    URL.revokeObjectURL(url)
  }

  return (
    <section className="storage-panel">
      <h2>File Storage</h2>
      <p className="hint small">
        Upload, list, download, and delete files in the{' '}
        <code>{BUCKET}</code> bucket via{' '}
        <code>supabase.storage</code>. Bucket is public, so files get a
        direct public URL.
      </p>

      <div className="storage-toolbar">
        <label>
          Prefix / folder
          <input
            type="text"
            value={prefix}
            onChange={(e) => setPrefix(e.target.value)}
            placeholder="e.g. thumbnails"
          />
        </label>
        <form onSubmit={handleUpload} className="upload-form">
          <input ref={inputRef} type="file" />
          <button type="submit" disabled={uploading}>
            {uploading ? 'Uploading…' : 'Upload'}
          </button>
        </form>
      </div>

      {error && <div className="error inline">{error}</div>}

      {files.length === 0 && !error && (
        <p className="muted" style={{ fontSize: '0.9rem' }}>
          No files yet. Upload one above.
        </p>
      )}

      {files.length > 0 && (
        <table className="stat-table storage-table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Created</th>
              <th style={{ textAlign: 'right' }}>Actions</th>
            </tr>
          </thead>
          <tbody>
            {files.map((f) => {
              const pubUrl = getPublicUrl(f.name)
              const isImage = /\.(jpe?g|png|gif|webp|svg)$/i.test(f.name)
              return (
                <tr key={f.id || f.name}>
                  <td>
                    {isImage && (
                      <img
                        src={pubUrl}
                        alt=""
                        className="storage-thumb"
                      />
                    )}
                    <a
                      href={pubUrl}
                      target="_blank"
                      rel="noopener noreferrer"
                    >
                      {f.name}
                    </a>
                  </td>
                  <td className="muted" style={{ fontSize: '0.8rem' }}>
                    {f.created_at
                      ? new Date(f.created_at).toLocaleString()
                      : '—'}
                  </td>
                  <td style={{ textAlign: 'right' }}>
                    <div className="storage-actions">
                      <button onClick={() => handleDownload(f.name)}>
                        Download
                      </button>
                      <button
                        className="danger-btn"
                        onClick={() => handleDelete(f.name)}
                      >
                        Delete
                      </button>
                    </div>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      )}
    </section>
  )
}
