import { useState, FormEvent } from 'react'
import * as api from '../api/client'
import type { Board } from '../api/types'

interface Props {
  token: string
  board: Board
  onCreated: (threadId: string) => void
  onBack: () => void
}

export function NewThreadPage({ token, board, onCreated, onBack }: Props) {
  const [title, setTitle] = useState('')
  const [body, setBody] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    const res = await api.execCommand(token, 'createThread', {
      board: board.id,
      title,
      body,
    })
    setBusy(false)
    if (res.error) {
      setError(res.error.message)
    } else {
      onCreated(res.data?.id ?? '')
    }
  }

  return (
    <div className="new-thread-page">
      <div className="page-header">
        <button className="back-btn" onClick={onBack}>← {board.name}</button>
        <h2>New thread</h2>
      </div>
      <form className="new-thread-form" onSubmit={submit}>
        <label>
          Title
          <input
            autoFocus
            value={title}
            onChange={e => setTitle(e.target.value)}
            required
            maxLength={200}
          />
        </label>
        <label>
          Body
          <textarea
            value={body}
            onChange={e => setBody(e.target.value)}
            required
            rows={8}
            placeholder="Markdown-light markup: **bold**, `code`, > quote"
          />
        </label>
        {error && <p className="error">{error}</p>}
        <div className="form-actions">
          <button type="submit" disabled={busy || !title.trim() || !body.trim()}>
            {busy ? '…' : 'Create thread'}
          </button>
          <button type="button" className="link-btn" onClick={onBack}>Cancel</button>
        </div>
      </form>
    </div>
  )
}
