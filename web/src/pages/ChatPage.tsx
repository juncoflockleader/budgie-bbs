import { useState, useRef, useEffect, useCallback, FormEvent } from 'react'
import * as api from '../api/client'
import type { BudgieEvent, ChatLinePayload, SocialUser, UserJoinedPayload, UserLeftPayload } from '../api/types'
import { useStream } from '../hooks/useStream'

interface ChatLine {
  id: string
  user: string
  text: string
  ts: number
  system?: boolean
}

interface Props {
  token: string
  onBack: () => void
  onMessageUser: (username: string) => void
}

function presenceMode(user: SocialUser) {
  return user.mode || user.status || 'online'
}

function presenceLocation(user: SocialUser) {
  return user.locationLabel || user.boardName || user.boardId || ''
}

export function ChatPage({ token, onBack, onMessageUser }: Props) {
  const [lines, setLines] = useState<ChatLine[]>([])
  const [draft, setDraft] = useState('')
  const [sending, setSending] = useState(false)
  const [onlineFriends, setOnlineFriends] = useState<SocialUser[]>([])
  const [friendsError, setFriendsError] = useState<string | null>(null)
  const bottomRef = useRef<HTMLDivElement>(null)

  const onEvent = useCallback((evt: BudgieEvent) => {
    if (evt.event === 'chat.line') {
      const p = evt.payload as ChatLinePayload
      if (p.room !== 'lobby') return
      setLines(prev => {
        if (prev.find(l => l.id === p.id)) return prev
        return [...prev, { id: p.id, user: p.user, text: p.text, ts: p.ts }]
      })
    } else if (evt.event === 'user.joined') {
      const p = evt.payload as UserJoinedPayload
      setLines(prev => [...prev, { id: `join-${p.ts}`, user: p.user, text: 'joined the chat', ts: p.ts, system: true }])
    } else if (evt.event === 'user.left') {
      const p = evt.payload as UserLeftPayload
      setLines(prev => [...prev, { id: `left-${p.ts}`, user: p.user, text: 'left', ts: p.ts, system: true }])
    }
  }, [])

  useStream({ token }, onEvent)

  useEffect(() => {
    let cancelled = false
    async function loadOnlineFriends() {
      const res = await api.listSocialUsers(token, 'online-friends')
      if (cancelled) return
      if (res.error) {
        setFriendsError(res.error.message)
        setOnlineFriends([])
        return
      }
      setFriendsError(null)
      setOnlineFriends(res.data ?? [])
    }
    void loadOnlineFriends()
    const id = window.setInterval(loadOnlineFriends, 30000)
    return () => {
      cancelled = true
      window.clearInterval(id)
    }
  }, [token])

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [lines])

  async function send(e: FormEvent) {
    e.preventDefault()
    if (!draft.trim()) return
    setSending(true)
    const res = await api.execCommand(token, 'sendChatLine', { room: 'lobby', text: draft })
    setSending(false)
    if (res.error) alert(res.error.message)
    else setDraft('')
  }

  return (
    <div className="chat-page">
      <div className="page-header">
        <button className="back-btn" onClick={onBack}>← Boards</button>
        <h2>Lobby chat</h2>
      </div>
      <div className="chat-shell">
        <section className="chat-main">
          <div className="chat-log">
            {lines.length === 0 && <p className="muted">No messages yet. Say hello!</p>}
            {lines.map(l => (
              <div key={l.id} className={`chat-line ${l.system ? 'chat-system' : ''}`}>
                {l.system ? (
                  <span className="muted">* {l.user} {l.text}</span>
                ) : (
                  <>
                    <span className="chat-user">{l.user}</span>
                    <span className="chat-sep">: </span>
                    <span className="chat-text">{l.text}</span>
                  </>
                )}
              </div>
            ))}
            <div ref={bottomRef} />
          </div>
          <form className="chat-form" onSubmit={send}>
            <input
              className="chat-input"
              value={draft}
              onChange={e => setDraft(e.target.value)}
              placeholder="Say something…"
              disabled={sending}
            />
            <button type="submit" disabled={sending || !draft.trim()}>Send</button>
          </form>
        </section>
        <aside className="chat-friends">
          <h3>Friends online</h3>
          {friendsError && <p className="error">{friendsError}</p>}
          {!friendsError && onlineFriends.length === 0 && <p className="muted">No friends online.</p>}
          {onlineFriends.map(friend => (
            <div key={`${friend.userId}-${friend.sessionId ?? 'summary'}`} className="chat-friend">
              <button className="post-author post-author-link chat-friend-name" onClick={() => onMessageUser(friend.name)}>
                {friend.displayName || friend.name}
              </button>
              <span className="policy-badge">{presenceMode(friend)}</span>
              {presenceLocation(friend) && <span className="muted chat-friend-location">{presenceLocation(friend)}</span>}
              {friend.fromHost && <span className="muted chat-friend-host">{friend.fromHost}</span>}
              <button className="link-btn" onClick={() => onMessageUser(friend.name)}>Message</button>
            </div>
          ))}
        </aside>
      </div>
    </div>
  )
}
