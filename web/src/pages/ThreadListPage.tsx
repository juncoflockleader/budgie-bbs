import { useEffect, useState, useCallback } from 'react'
import * as api from '../api/client'
import type { Board, Thread } from '../api/types'
import type { BudgieEvent, ThreadNewPayload } from '../api/types'
import { Spinner } from '../components/Spinner'
import { useStream } from '../hooks/useStream'

interface Props {
  token: string
  board: Board
  onSelect: (thread: Thread) => void
  onBack: () => void
  onNewThread: () => void
}

export function ThreadListPage({ token, board, onSelect, onBack, onNewThread }: Props) {
  const [threads, setThreads] = useState<Thread[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    setLoading(true)
    api.listThreads(token, board.id).then(res => {
      setLoading(false)
      if (res.error) setError(res.error.message)
      else setThreads(res.data ?? [])
    })
  }, [token, board.id])

  const onEvent = useCallback((evt: BudgieEvent) => {
    if (evt.event === 'thread.new') {
      const p = evt.payload as ThreadNewPayload
      if (p.board !== board.id) return
      setThreads(prev => [{
        id: p.id,
        board: p.board,
        author: p.author,
        title: p.title,
        locked: false,
        postCount: 1,
        lastSeq: evt.seq ?? 0,
        createdTs: p.ts,
      }, ...prev])
    }
  }, [board.id])

  useStream({ token }, onEvent)

  if (loading) return <Spinner />
  if (error) return <p className="error">{error}</p>

  return (
    <div className="thread-list">
      <div className="page-header">
        <button className="back-btn" onClick={onBack}>← Boards</button>
        <h2>{board.name}</h2>
        <button className="new-btn" onClick={onNewThread}>+ New thread</button>
      </div>
      {threads.length === 0 && <p className="muted">No threads yet. Start one!</p>}
      <ul className="item-list">
        {threads.map(t => (
          <li key={t.id} className="item-row" onClick={() => onSelect(t)}>
            <span className="item-title">
              {t.locked && <span title="Locked">🔒 </span>}
              {t.title}
            </span>
            <span className="item-meta muted">{t.postCount} posts · by {t.author}</span>
          </li>
        ))}
      </ul>
    </div>
  )
}
