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
import { PrivatePage } from './pages/PrivatePage'
import { SocialPage } from './pages/SocialPage'
import { RankingsPage } from './pages/RankingsPage'
import { UnreadPage } from './pages/UnreadPage'
import { UserProfilePage } from './pages/UserProfilePage'
import { AuthorPostsPage } from './pages/AuthorPostsPage'
import { ResidentFeedPage } from './pages/ResidentFeedPage'
import * as api from './api/client'
import type { Board, Thread, ThreadSummary, BudgieEvent } from './api/types'
import { useStream } from './hooks/useStream'

type Page =
  | { name: 'boards' }
  | { name: 'threads'; board: Board }
  | { name: 'thread'; board: Board; thread: Thread; initialPostId?: string }
  | { name: 'new-thread'; board: Board }
  | { name: 'chat' }
  | { name: 'search'; query: string }
  | { name: 'notifications' }
  | { name: 'unread' }
  | { name: 'resident-feed' }
  | { name: 'private'; messageTo?: string }
  | { name: 'social' }
  | { name: 'rankings' }
  | { name: 'user-profile'; username: string }
  | { name: 'author-posts'; username: string }

export function App() {
  const { auth, login, logout } = useAuth()
  const [page, setPage] = useState<Page>({ name: 'boards' })
  const [searchDraft, setSearchDraft] = useState('')
  const [unreadCount, setUnreadCount] = useState(0)
  const [privateUnreadCount, setPrivateUnreadCount] = useState(0)
  const searchRef = useRef<HTMLInputElement>(null)

  const refreshPrivateUnread = useCallback(() => {
    if (!auth.token) return
    Promise.all([api.listMail(auth.token), api.listDirectConversations(auth.token)]).then(([mailRes, messageRes]) => {
      const mailUnread = mailRes.data?.unreadCount ?? 0
      const messageUnread = messageRes.data?.unreadCount ?? 0
      setPrivateUnreadCount(mailUnread + messageUnread)
    })
  }, [auth.token])

  useEffect(() => {
    if (!auth.token) return
    api.listNotifications(auth.token).then(res => {
      if (res.data) setUnreadCount(res.data.unreadCount)
    })
    refreshPrivateUnread()
  }, [auth.token, refreshPrivateUnread])

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
    if (evt.event === 'mail.sent' || evt.event === 'direct_message.sent') {
      refreshPrivateUnread()
    }
  }, [auth.token, refreshPrivateUnread])

  useStream({ token: auth.token ?? null }, onEvent)

  useEffect(() => {
    if (auth.token) return
    const sessionId = getGuestSessionId()
    const ping = () => {
      void api.setGuestPresence({
        sessionId,
        status: document.visibilityState === 'hidden' ? 'idle' : 'active',
        location: 'web',
      })
    }
    ping()
    const interval = window.setInterval(ping, 60_000)
    document.addEventListener('visibilitychange', ping)
    return () => {
      window.clearInterval(interval)
      document.removeEventListener('visibilitychange', ping)
      void api.setGuestPresence({ sessionId, status: 'offline', location: 'web' })
    }
  }, [auth.token])

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
        <button className="link-btn nav-unread" onClick={() => nav({ name: 'unread' })}>Unread</button>
        <button className="link-btn nav-resident" onClick={() => nav({ name: 'resident-feed' })}>Resident</button>
        <button className="link-btn nav-chat" onClick={() => nav({ name: 'chat' })}>Chat</button>
        <button className="link-btn nav-social" onClick={() => nav({ name: 'social' })}>People</button>
        <button className="link-btn nav-rankings" onClick={() => nav({ name: 'rankings' })}>Rankings</button>
        <button
          className={`link-btn nav-private${privateUnreadCount > 0 ? ' nav-private--unread' : ''}`}
          onClick={() => { nav({ name: 'private' }); setPrivateUnreadCount(0) }}
        >
          Inbox{privateUnreadCount > 0 && <span className="notif-badge">{privateUnreadCount > 99 ? '99+' : privateUnreadCount}</span>}
        </button>
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
            currentUserRole={user.role}
            onSelect={board => nav({ name: 'threads', board })}
          />
        )}
        {page.name === 'threads' && (
          <ThreadListPage
            token={token}
            board={page.board}
            currentUserId={user.id}
            currentUserRole={user.role}
            onSelect={(thread, initialPostId) => nav({ name: 'thread', board: page.board, thread, initialPostId })}
            onBack={() => nav({ name: 'boards' })}
            onNewThread={() => nav({ name: 'new-thread', board: page.board })}
            onMessageUser={username => nav({ name: 'private', messageTo: username })}
          />
        )}
        {page.name === 'thread' && (
          <ThreadPage
            token={token}
            thread={page.thread}
            currentUserId={user.id}
            currentUsername={user.name}
            currentUserRole={user.role}
            initialPostId={page.initialPostId}
            onBack={() => nav({ name: 'threads', board: page.board })}
            onOpenThread={(thread: ThreadSummary, initialPostId?: string) => nav({ name: 'thread', board: page.board, thread, initialPostId })}
            onOpenProfile={username => nav({ name: 'user-profile', username })}
            onOpenAuthorPosts={username => nav({ name: 'author-posts', username })}
          />
        )}
        {page.name === 'user-profile' && (
          <UserProfilePage
            token={token}
            username={page.username}
            isOwnProfile={page.username === user.name}
            currentUserRole={user.role}
            onBack={() => nav({ name: 'boards' })}
            onOpenAuthorPosts={username => nav({ name: 'author-posts', username })}
          />
        )}
        {page.name === 'author-posts' && (
          <AuthorPostsPage
            token={token}
            username={page.username}
            onBack={() => nav({ name: 'user-profile', username: page.username })}
            onOpenThread={(board, thread, initialPostId) => nav({ name: 'thread', board, thread, initialPostId })}
          />
        )}
        {page.name === 'resident-feed' && (
          <ResidentFeedPage
            token={token}
            onBack={() => nav({ name: 'boards' })}
            onOpenThread={(board, thread, initialPostId) => nav({ name: 'thread', board, thread, initialPostId })}
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
          <ChatPage
            token={token}
            onBack={() => nav({ name: 'boards' })}
            onMessageUser={username => nav({ name: 'private', messageTo: username })}
          />
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
        {page.name === 'unread' && (
          <UnreadPage
            token={token}
            onBack={() => nav({ name: 'boards' })}
            onOpenThread={(board, thread, initialPostId) => nav({ name: 'thread', board, thread, initialPostId })}
          />
        )}
        {page.name === 'private' && (
          <PrivatePage
            token={token}
            onBack={() => nav({ name: 'boards' })}
            currentUserRole={auth.user.role}
            initialMessageTo={page.messageTo}
          />
        )}
        {page.name === 'social' && (
          <SocialPage
            token={token}
            onBack={() => nav({ name: 'boards' })}
            onOpenProfile={username => nav({ name: 'user-profile', username })}
            onMessageUser={username => nav({ name: 'private', messageTo: username })}
          />
        )}
        {page.name === 'rankings' && (
          <RankingsPage
            token={token}
            onBack={() => nav({ name: 'boards' })}
            onOpenBoard={board => nav({ name: 'threads', board })}
            onOpenThread={(board, thread) => nav({ name: 'thread', board, thread })}
          />
        )}
      </main>
    </div>
  )
}

function getGuestSessionId() {
  const key = 'budgieGuestSessionId'
  const existing = window.localStorage.getItem(key)
  if (existing) return existing
  const generated = `guest_${window.crypto?.randomUUID?.() ?? `${Date.now()}_${Math.random().toString(36).slice(2)}`}`
  window.localStorage.setItem(key, generated)
  return generated
}
