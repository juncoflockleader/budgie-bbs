import { useState, FormEvent, useEffect } from 'react'
import * as api from '../api/client'
import type { Post } from '../api/types'
import { Markup } from '../components/Markup'
import { Spinner } from '../components/Spinner'
import { useI18n } from '../i18n'
import { formatCount } from '../i18n/format'

interface Props {
  token: string
  initialQuery?: string
  onBack: () => void
}

export function SearchPage({ token, initialQuery = '', onBack }: Props) {
  const { locale, t } = useI18n()
  const [query, setQuery] = useState(initialQuery)
  const [results, setResults] = useState<Post[]>([])
  const [loading, setLoading] = useState(false)
  const [searched, setSearched] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (initialQuery) runSearch(initialQuery)
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  async function runSearch(q: string) {
    if (!q.trim()) return
    setLoading(true)
    setError(null)
    setSearched(true)
    const res = await api.search(token, q.trim())
    setLoading(false)
    if (res.error) setError(t('common.errorPrefix', { message: res.error.message }))
    else setResults(res.data ?? [])
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    runSearch(query)
  }

  return (
    <div className="search-page">
      <div className="page-header">
        <button className="back-btn" onClick={onBack}>← {t('common.back')}</button>
        <h2>{t('search.title')}</h2>
      </div>
      <form className="search-form" onSubmit={submit}>
        <input
          autoFocus
          className="search-input"
          value={query}
          onChange={e => setQuery(e.target.value)}
          placeholder={t('search.postsPlaceholder')}
        />
        <button type="submit" disabled={loading || !query.trim()}>
          {loading ? '…' : t('search.title')}
        </button>
      </form>
      {error && <p className="error">{error}</p>}
      {loading && <Spinner />}
      {searched && !loading && (
        results.length === 0
          ? <p className="muted">{t('search.noResults', { query })}</p>
          : (
            <div className="search-results">
              <p className="muted search-count">{t('search.resultsCount', { count: formatCount(results.length, locale) })}</p>
              {results.map(post => (
                <div key={post.id} className="post-card">
                  <div className="post-meta">
                    <span className="post-author">{post.author}</span>
                    <span className="muted">{t('search.inThread')}{' '}{post.thread}</span>
                  </div>
                  <div className="post-body">
                    <Markup body={post.body} redacted={post.redacted} />
                  </div>
                </div>
              ))}
            </div>
          )
      )}
    </div>
  )
}
