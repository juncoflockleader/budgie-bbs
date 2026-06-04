import { useEffect, useMemo, useState } from 'react'
import * as api from '../api/client'
import type { Board, FavoriteFolder, FavoriteTree, ThreadSummary } from '../api/types'
import { Spinner } from '../components/Spinner'

interface Props {
  token: string
  onBack: () => void
  onOpenThread: (board: Board, thread: ThreadSummary, initialPostId?: string) => void
}

type Scope = 'all' | 'favorites'

export function UnreadPage({ token, onBack, onOpenThread }: Props) {
  const [threads, setThreads] = useState<ThreadSummary[]>([])
  const [favoriteTree, setFavoriteTree] = useState<FavoriteTree>({ folders: [], boards: [] })
  const [scope, setScope] = useState<Scope>('all')
  const [folder, setFolder] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const folderOptions = useMemo(() => flattenFolders(favoriteTree.folders), [favoriteTree.folders])

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError(null)
    Promise.all([
      api.listUnreadThreads(token, 50, 0, scope === 'favorites', folder),
      api.listFavoriteTree(token),
    ]).then(([threadsRes, treeRes]) => {
      if (cancelled) return
      setLoading(false)
      if (threadsRes.error) {
        setError(threadsRes.error.message)
        return
      }
      if (treeRes.error) {
        setError(treeRes.error.message)
        return
      }
      setThreads(threadsRes.data ?? [])
      setFavoriteTree(treeRes.data ?? { folders: [], boards: [] })
    })
    return () => {
      cancelled = true
    }
  }, [token, scope, folder])

  async function markThreadRead(thread: ThreadSummary) {
    const previous = threads
    setThreads(current => current.filter(item => item.id !== thread.id))
    const res = await api.markThreadRead(token, thread.id)
    if (res.error) {
      setThreads(previous)
      setError(res.error.message)
    }
  }

  if (loading) return <Spinner />

  return (
    <div className="unread-page">
      <div className="page-header">
        <button onClick={onBack}>Back</button>
        <h2>Unread</h2>
      </div>
      {error && <p className="error">{error}</p>}
      <div className="unread-filter-row">
        <button className={scope === 'all' ? 'private-tab private-tab--active' : 'private-tab'} onClick={() => { setScope('all'); setFolder('') }}>All Boards</button>
        <button className={scope === 'favorites' ? 'private-tab private-tab--active' : 'private-tab'} onClick={() => setScope('favorites')}>Favorites</button>
        {scope === 'favorites' && (
          <select value={folder} onChange={e => setFolder(e.currentTarget.value)} aria-label="Favorite folder unread scope">
            <option value="">All Favorites</option>
            {folderOptions.map(option => (
              <option key={option.id} value={option.id}>{option.label}</option>
            ))}
          </select>
        )}
      </div>
      {threads.length === 0 && <p className="muted">No unread threads.</p>}
      <div className="unread-thread-list">
        {threads.map(thread => (
          <div key={thread.id} className="unread-thread-row">
            <button
              className="unread-thread-main"
              onClick={() => onOpenThread(
                { id: thread.board, name: thread.boardName || thread.board, description: '' },
                thread,
                thread.firstUnreadPostId,
              )}
            >
              <span className="item-title">{thread.title}</span>
              <span className="item-meta muted">
                {thread.boardName || thread.board} / {thread.unreadPosts} unread post{thread.unreadPosts === 1 ? '' : 's'} / {thread.author}
              </span>
            </button>
            <button className="board-action-btn" onClick={() => { void markThreadRead(thread) }} title="Mark thread read">✓</button>
          </div>
        ))}
      </div>
    </div>
  )
}

function flattenFolders(folders: FavoriteFolder[]) {
  const byParent: Record<string, FavoriteFolder[]> = {}
  folders.forEach(folder => {
    const parent = folder.parentId ?? ''
    byParent[parent] = [...(byParent[parent] ?? []), folder]
  })
  Object.values(byParent).forEach(items => {
    items.sort((a, b) => a.position - b.position || a.name.localeCompare(b.name))
  })
  const out: Array<{ id: string; label: string }> = []
  function visit(parentId: string, prefix: string) {
    for (const folder of byParent[parentId] ?? []) {
      out.push({ id: folder.id, label: `${prefix}${folder.name}` })
      visit(folder.id, `${prefix}${folder.name} / `)
    }
  }
  visit('', '')
  return out
}
