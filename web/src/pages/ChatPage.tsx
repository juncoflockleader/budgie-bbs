import { useState, useRef, useEffect, useCallback, FormEvent } from 'react'
import * as api from '../api/client'
import type { BudgieEvent, ChatLine as ApiChatLine, ChatLinePayload, ChatRoom, SocialUser, UserJoinedPayload, UserLeftPayload } from '../api/types'
import { useStream } from '../hooks/useStream'

interface DisplayChatLine {
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

function formatRoomName(roomID: string) {
  if (roomID === 'lobby') return 'Lobby'
  return roomID
    .replace(/[-_]+/g, ' ')
    .split(' ')
    .filter(Boolean)
    .map(word => word.slice(0, 1).toUpperCase() + word.slice(1))
    .join(' ') || roomID
}

function toDisplayLine(line: ApiChatLine): DisplayChatLine {
  return {
    id: line.id,
    user: line.user,
    text: line.text,
    ts: line.ts || line.createdAt,
  }
}

export function ChatPage({ token, onBack, onMessageUser }: Props) {
  const [selectedRoom, setSelectedRoom] = useState('lobby')
  const [rooms, setRooms] = useState<ChatRoom[]>([])
  const [lines, setLines] = useState<DisplayChatLine[]>([])
  const [draft, setDraft] = useState('')
  const [sending, setSending] = useState(false)
  const [roomUsers, setRoomUsers] = useState<SocialUser[]>([])
  const [chatError, setChatError] = useState<string | null>(null)
  const [onlineFriends, setOnlineFriends] = useState<SocialUser[]>([])
  const [friendsError, setFriendsError] = useState<string | null>(null)
  const bottomRef = useRef<HTMLDivElement>(null)

  const onEvent = useCallback((evt: BudgieEvent) => {
    if (evt.event === 'chat.line') {
      const p = evt.payload as ChatLinePayload
      setRooms(prev => {
        if (prev.some(room => room.id === p.room)) {
          return prev.map(room => room.id === p.room
            ? { ...room, lineCount: room.lineCount + 1, updatedAt: p.ts }
            : room)
        }
        return [...prev, {
          id: p.room,
          name: formatRoomName(p.room),
          onlineUsers: 0,
          lineCount: 1,
          createdAt: p.ts,
          updatedAt: p.ts,
        }]
      })
      if (p.room !== selectedRoom) return
      setLines(prev => {
        if (prev.find(l => l.id === p.id)) return prev
        return [...prev, { id: p.id, user: p.user, text: p.text, ts: p.ts }]
      })
    } else if (evt.event === 'user.joined') {
      if (selectedRoom !== 'lobby') return
      const p = evt.payload as UserJoinedPayload
      setLines(prev => [...prev, { id: `join-${p.ts}`, user: p.user, text: 'joined the chat', ts: p.ts, system: true }])
    } else if (evt.event === 'user.left') {
      if (selectedRoom !== 'lobby') return
      const p = evt.payload as UserLeftPayload
      setLines(prev => [...prev, { id: `left-${p.ts}`, user: p.user, text: 'left', ts: p.ts, system: true }])
    }
  }, [selectedRoom])

  useStream({ enabled: true }, onEvent)

  useEffect(() => {
    let cancelled = false
    async function loadRooms() {
      const res = await api.listChatRooms(token)
      if (cancelled) return
      if (res.error) {
        setChatError(res.error.message)
        return
      }
      const data = res.data ?? []
      setRooms(data.length > 0 ? data : [{
        id: 'lobby',
        name: 'Lobby',
        onlineUsers: 0,
        lineCount: 0,
        createdAt: 0,
        updatedAt: 0,
      }])
    }
    void loadRooms()
    const id = window.setInterval(loadRooms, 30000)
    return () => {
      cancelled = true
      window.clearInterval(id)
    }
  }, [token])

  useEffect(() => {
    let cancelled = false
    async function loadRoom() {
      await api.setPresence(token, {
        status: 'active',
        mode: 'chat',
        location: selectedRoom,
      })
      if (cancelled) return
      const [recent, users] = await Promise.all([
        api.listChatRecent(token, selectedRoom, 50),
        api.listChatOnlineUsers(token, selectedRoom),
      ])
      if (cancelled) return
      if (recent.error) {
        setChatError(recent.error.message)
        setLines([])
      } else {
        setChatError(null)
        setLines((recent.data ?? []).map(toDisplayLine))
      }
      if (users.error) {
        setRoomUsers([])
      } else {
        setRoomUsers(users.data ?? [])
      }
    }
    void loadRoom()
    const id = window.setInterval(loadRoom, 15000)
    return () => {
      cancelled = true
      window.clearInterval(id)
      void api.setPresence(token, {
        status: 'active',
        mode: 'web',
        location: 'web',
      })
    }
  }, [token, selectedRoom])

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
    const text = draft.trim()
    if (!text) return
    setSending(true)
    const res = await api.sendChatLine(token, selectedRoom, text)
    setSending(false)
    if (res.error) alert(res.error.message)
    else setDraft('')
  }

  const selectedRoomInfo = rooms.find(room => room.id === selectedRoom)
  const selectedRoomName = selectedRoomInfo?.name || formatRoomName(selectedRoom)

  return (
    <div className="chat-page">
      <div className="page-header">
        <button className="back-btn" onClick={onBack}>← Boards</button>
        <h2>{selectedRoomName}</h2>
      </div>
      <div className="chat-room-strip">
        {rooms.map(room => (
          <button
            key={room.id}
            type="button"
            className={`chat-room-tab ${room.id === selectedRoom ? 'chat-room-tab--active' : ''}`}
            onClick={() => setSelectedRoom(room.id)}
          >
            <span>#{room.name}</span>
            <span className="policy-badge">{room.onlineUsers}</span>
          </button>
        ))}
      </div>
      <div className="chat-shell">
        <section className="chat-main">
          <div className="chat-log">
            {chatError && <p className="error">{chatError}</p>}
            {!chatError && lines.length === 0 && <p className="muted">No messages yet. Say hello!</p>}
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
          <h3>In {selectedRoomName}</h3>
          {roomUsers.length === 0 && <p className="muted">No one else is here.</p>}
          {roomUsers.map(user => (
            <div key={`room-${user.userId}-${user.sessionId ?? 'summary'}`} className="chat-friend">
              <button className="post-author post-author-link chat-friend-name" onClick={() => onMessageUser(user.name)}>
                {user.displayName || user.name}
              </button>
              <span className="policy-badge">{presenceMode(user)}</span>
              {user.fromHost && <span className="muted chat-friend-host">{user.fromHost}</span>}
              <button className="link-btn" onClick={() => onMessageUser(user.name)}>Message</button>
            </div>
          ))}
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
