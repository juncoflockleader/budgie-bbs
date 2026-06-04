import { useState, useEffect, useCallback, FormEvent, useRef } from 'react'
import { useAuth } from './hooks/useAuth'
import { AuthPage } from './pages/AuthPage'
import { BoardListPage } from './pages/BoardListPage'
import { ThreadListPage } from './pages/ThreadListPage'
import { ThreadPage } from './pages/ThreadPage'
import { NewThreadPage } from './pages/NewThreadPage'
import { ChatPage } from './pages/ChatPage'
import { SearchPage } from './pages/SearchPage'
import { NotificationsPage } from './pages/NotificationsPage'
import { UserProfilePage } from './pages/UserProfilePage'
import * as api from './api/client'
import type { Board, Thread, BudgieEvent } from './api/types'
import { useStream } from './hooks/useStream'

type Page =
  | { name: 'boards' }
  | { name: 'threads'; board: Board }
  | { name: 'thread'; board: Board; thread: Thread }
  | { name: 'new-thread'; board: Board }
  | { name: 'chat' }
  | { name: 'search'; query: string }
  | { name: 'notifications' }
  | { name: 'user-profile'; username: string }

export function App() {
  const { auth, login, logout } = useAuth()
  const [page, setPage] = useState<Page>({ name: 'boards' })
  const [searchDraft, setSearchDraft] = useState('')
  const [unreadCount, setUnreadCount] = useState(0)
  const searchRef = useRef<HTMLInputElement>(null)

  // Load initial unread notification count
  useEffect(() => {
    if (!auth.token) return
    api.listNotifications(auth.token).then(res => {
      if (res.data) setUnreadCount(res.data.unreadCount)
    })
  }, [auth.token])

  // Live update unread count from stream events
  const onEvent = useCallback((evt: BudgieEvent) => {
    if (evt.event === 'user.joined' || evt.event === 'user.left') return
    // Any event that might be a notification for us — re-fetch count
    if (evt.event === 'post.appended' || evt.event === 'post.edited') {
      if (auth.token) {
        api.listNotifications(auth.token).then(res => {
          if (res.data) setUnreadCount(res.data.unreadCount)
        })
      }
    }
  }, [auth.token])

  useStream({ token: auth.token ?? null }, onEvent)

  if (!auth.token || !auth.user) {
    return <AuthPage onLogin={login} />
  }

  const { token, user } = auth

  function nav(p: Page) { setPage(p) }

  function submitSearch(e: FormEvent) {
    e.preventDefault()
    if (searchDraft.trim()) {
      nav({ name: 'search', query: searchDraft.trim() })
      setSearchDraft('')
      searchRef.current?.blur()
    }
  }

  return (
    <div className="app">
      <nav className="top-nav">
        <span className="nav-logo" onClick={() => nav({ name: 'boards' })}>🐦 Budgie</span>
        <form className="nav-search-form" onSubmit={submitSearch}>
          <input
            ref={searchRef}
            className="nav-search-input"
            value={searchDraft}
            onChange={e => setSearchDraft(e.target.value)}
            placeholder="Search…"
            aria-label="Search posts"
          />
        </form>
        <button className="link-btn nav-chat" onClick={() => nav({ name: 'chat' })}>Chat</button>
        <button
          className={`link-btn nav-notifications${unreadCount > 0 ? ' nav-notifications--unread' : ''}`}
          onClick={() => { nav({ name: 'notifications' }); setUnreadCount(0) }}
          title="Notifications"
        >
          🔔{unreadCount > 0 && <span className="notif-badge">{unreadCount > 99 ? '99+' : unreadCount}</span>}
        </button>
        <button
          className="link-btn nav-user"
          onClick={() => nav({ name: 'user-profile', username: user.name })}
          title="Open your profile"
        >
          {user.name}
        </button>
        <button className="link-btn nav-logout" onClick={logout}>Logout</button>
      </nav>

      <main className="main-content">
        {page.name === 'boards' && (
          <BoardListPage
            token={token}
            onSelect={board => nav({ name: 'threads', board })}
          />
        )}
        {page.name === 'threads' && (
          <ThreadListPage
            token={token}
            board={page.board}
            onSelect={thread => nav({ name: 'thread', board: page.board, thread })}
            onBack={() => nav({ name: 'boards' })}
            onNewThread={() => nav({ name: 'new-thread', board: page.board })}
          />
        )}
        {page.name === 'thread' && (
          <ThreadPage
            token={token}
            thread={page.thread}
            currentUserId={user.id}
            currentUsername={user.name}
            currentUserRole={user.role}
            onBack={() => nav({ name: 'threads', board: page.board })}
            onOpenProfile={username => nav({ name: 'user-profile', username })}
          />
        )}
        {page.name === 'user-profile' && (
          <UserProfilePage
            token={token}
            username={page.username}
            isOwnProfile={page.username === user.name}
            onBack={() => nav({ name: 'boards' })}
          />
        )}
        {page.name === 'new-thread' && (
          <NewThreadPage
            token={token}
            board={page.board}
            currentUsername={user.name}
            onCreated={threadId => {
              void threadId
              nav({ name: 'threads', board: page.board })
            }}
            onBack={() => nav({ name: 'threads', board: page.board })}
          />
        )}
        {page.name === 'chat' && (
          <ChatPage token={token} onBack={() => nav({ name: 'boards' })} />
        )}
        {page.name === 'search' && (
          <SearchPage
            token={token}
            initialQuery={page.query}
            onBack={() => nav({ name: 'boards' })}
          />
        )}
        {page.name === 'notifications' && (
          <NotificationsPage
            token={token}
            onBack={() => nav({ name: 'boards' })}
          />
        )}
      </main>
    </div>
  )
}
