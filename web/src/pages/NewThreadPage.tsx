import { FormEvent, useEffect, useMemo, useState } from 'react'
import * as api from '../api/client'
import type { Board } from '../api/types'
import { PollComposer } from '../components/PollComposer'
import { validatePollMarkup } from '../pollValidation'

interface Props {
  token: string
  board: Board
  currentUsername: string
  onCreated: (threadId: string) => void
  onBack: () => void
}

export function NewThreadPage({ token, board, currentUsername, onCreated, onBack }: Props) {
  const [title, setTitle] = useState('')
  const [body, setBody] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [isTrustLoaded, setIsTrustLoaded] = useState(false)
  const [canCreatePoll, setCanCreatePoll] = useState(false)
  const pollValidation = useMemo(() => validatePollMarkup(body), [body])

  function appendPoll(markup: string) {
    setBody(prev => {
      const trimmed = prev.trimEnd()
      return trimmed ? `${trimmed}\n\n${markup}` : markup
    })
  }

  useEffect(() => {
    ;(async () => {
      setIsTrustLoaded(false)
      const trustRes = await api.getTrust(token, currentUsername)
      if (trustRes.data) {
        setCanCreatePoll(trustRes.data.trustLevel >= 2)
      } else {
        setCanCreatePoll(false)
      }
      setIsTrustLoaded(true)
    })()
  }, [token, currentUsername])

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    if (pollValidation.hasPollTag && !pollValidation.valid) {
      setError(pollValidation.message ?? 'Poll syntax is invalid')
      setBusy(false)
      return
    }
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
            onChange={e => {
              setBody(e.target.value)
              if (error) setError(null)
            }}
            required
            rows={8}
            placeholder="Markdown-light markup: **bold**, `code`, > quote"
          />
        </label>
        {pollValidation.hasPollTag && !pollValidation.valid && (
          <p className="error">{pollValidation.message}</p>
        )}
        <div className="compose-actions">
          <PollComposer
            onInsert={appendPoll}
            disabled={!isTrustLoaded || !canCreatePoll}
            disabledHint={!isTrustLoaded ? 'Checking permission…' : (!canCreatePoll ? 'Polls require trust level 2+' : undefined)}
          />
        </div>
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
