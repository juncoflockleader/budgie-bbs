import { useState, FormEvent, useRef } from 'react'
import { useAuth } from './hooks/useAuth'
import { AuthPage } from './pages/AuthPage'
import { BoardListPage } from './pages/BoardListPage'
import { ThreadListPage } from './pages/ThreadListPage'
import { ThreadPage } from './pages/ThreadPage'
import { NewThreadPage } from './pages/NewThreadPage'
import { ChatPage } from './pages/ChatPage'
import { SearchPage } from './pages/SearchPage'
import type { Board, Thread } from './api/types'

type Page =
  | { name: 'boards' }
  | { name: 'threads'; board: Board }
  | { name: 'thread'; board: Board; thread: Thread }
  | { name: 'new-thread'; board: Board }
  | { name: 'chat' }
  | { name: 'search'; query: string }

export function App() {
  const { auth, login, logout } = useAuth()
  const [page, setPage] = useState<Page>({ name: 'boards' })
  const [searchDraft, setSearchDraft] = useState('')
  const searchRef = useRef<HTMLInputElement>(null)

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
        <span className="nav-user muted">{user.name}</span>
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
            currentUserRole={user.role}
            onBack={() => nav({ name: 'threads', board: page.board })}
          />
        )}
        {page.name === 'new-thread' && (
          <NewThreadPage
            token={token}
            board={page.board}
            onCreated={threadId => {
              // After creation, go back to thread list; the thread will appear via event.
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
      </main>
    </div>
  )
}
