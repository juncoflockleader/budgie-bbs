import { useEffect, useState } from 'react'
import * as api from '../api/client'
import type { Board } from '../api/types'
import { Spinner } from '../components/Spinner'

interface Props {
  token: string
  onSelect: (board: Board) => void
}

export function BoardListPage({ token, onSelect }: Props) {
  const [boards, setBoards] = useState<Board[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    setLoading(true)
    api.listBoards(token).then(res => {
      setLoading(false)
      if (res.error) setError(res.error.message)
      else setBoards(res.data ?? [])
    })
  }, [token])

  if (loading) return <Spinner />
  if (error) return <p className="error">{error}</p>

  return (
    <div className="board-list">
      <h2>Boards</h2>
      {boards.length === 0 && <p className="muted">No boards yet.</p>}
      <ul className="item-list">
        {boards.map(b => (
          <li key={b.id} className="item-row" onClick={() => onSelect(b)}>
            <span className="item-title">{b.name}</span>
            {b.description && <span className="item-desc muted">{b.description}</span>}
          </li>
        ))}
      </ul>
    </div>
  )
}
