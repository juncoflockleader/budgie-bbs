import { useState, useEffect, useCallback, FormEvent, useRef } from 'react'
import { useAuth } from './hooks/useAuth'
import { useTheme, type ThemeId } from './hooks/useTheme'
import { applyAppearance } from './appearance'
import type { SiteAppearance } from './api/types'
import { LocaleCode, useI18n } from './i18n'
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
import { AdminPage } from './pages/AdminPage'
import { SidebarNavTree } from './components/SidebarNavTree'
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
  | { name: 'admin' }
  | { name: 'user-profile'; username: string }
  | { name: 'author-posts'; username: string }

export function App() {
  const { locale, setLocale, t } = useI18n()
  const { theme, setTheme } = useTheme()
  const { auth, login, logout, loading: authLoading } = useAuth()
  // Guest browsing: a visitor who clicked "Browse as guest" sees the read-only
  // app (public, non-member boards) without an account. auth.user stays null.
  // A visitor who lands on a deep link (/b/{id} or /t/{id}) — e.g. from search
  // results or a shared URL — enters guest mode automatically so the content
  // renders instead of the login wall (a logged-in user is unaffected).
  const [guestMode, setGuestMode] = useState(() => isDeepLinkPath(window.location.pathname))
  const [page, setPage] = useState<Page>({ name: 'boards' })
  const [logoImgOk, setLogoImgOk] = useState(true)
  const [searchDraft, setSearchDraft] = useState('')
  const [unreadCount, setUnreadCount] = useState(0)
  const [privateUnreadCount, setPrivateUnreadCount] = useState(0)
  const searchRef = useRef<HTMLInputElement>(null)
  const historyDepthRef = useRef(0)
  const [appearance, setAppearance] = useState<SiteAppearance | null>(null)
  const [bannerDismissed, setBannerDismissed] = useState<string | null>(() => {
    try { return localStorage.getItem('budgie.bannerDismissed') } catch { return null }
  })

  // Load public site appearance once and apply branding (title, accent, default theme).
  useEffect(() => {
    api.getSiteAppearance().then(res => {
      if (!res.data) return
      setAppearance(res.data)
      applyAppearance(res.data, t => setTheme(t as ThemeId))
    })
    // applyAppearance (e.g. from an admin save) broadcasts the new branding so
    // the sidebar title and banner update live without a reload.
    const onAppearance = (e: Event) => setAppearance((e as CustomEvent<SiteAppearance>).detail)
    window.addEventListener('budgie:appearance', onAppearance)
    return () => window.removeEventListener('budgie:appearance', onAppearance)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  function dismissBanner() {
    if (!appearance?.bannerMessage) return
    try { localStorage.setItem('budgie.bannerDismissed', appearance.bannerMessage) } catch { /* ignore */ }
    setBannerDismissed(appearance.bannerMessage)
  }
  const showBanner = Boolean(appearance?.bannerMessage) && bannerDismissed !== appearance?.bannerMessage

  const loggedIn = !!auth.user
  const refreshPrivateUnread = useCallback(() => {
    if (!loggedIn) return
    Promise.all([api.listMail(auth.token), api.listDirectConversations(auth.token)]).then(([mailRes, messageRes]) => {
      const mailUnread = mailRes.data?.unreadCount ?? 0
      const messageUnread = messageRes.data?.unreadCount ?? 0
      setPrivateUnreadCount(mailUnread + messageUnread)
    })
  }, [loggedIn, auth.token])

  useEffect(() => {
    if (!loggedIn) return
    api.listNotifications(auth.token).then(res => {
      if (res.data) setUnreadCount(res.data.unreadCount)
    })
    refreshPrivateUnread()
  }, [loggedIn, auth.token, refreshPrivateUnread])

  // Live update unread count from stream events
  const onEvent = useCallback((evt: BudgieEvent) => {
    if (evt.event === 'user.joined' || evt.event === 'user.left') return
    // Any event that might be a notification for us — re-fetch count
    if (evt.event === 'post.appended' || evt.event === 'post.edited') {
      if (loggedIn) {
        api.listNotifications(auth.token).then(res => {
          if (res.data) setUnreadCount(res.data.unreadCount)
        })
      }
    }
    if (evt.event === 'mail.sent' || evt.event === 'direct_message.sent') {
      refreshPrivateUnread()
    }
  }, [loggedIn, auth.token, refreshPrivateUnread])

  useStream({ enabled: loggedIn }, onEvent)

  useEffect(() => {
    if (loggedIn) return
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
  }, [loggedIn])

  useEffect(() => {
    const current = pageFromHistoryState(window.history.state)
    if (current) {
      historyDepthRef.current = historyDepthFromState(window.history.state)
      setPage(current)
    } else if (isDeepLinkPath(window.location.pathname)) {
      // Cold load / refresh / shared link on a /b/{id} or /t/{id} URL.
      void resolveDeepLink(window.location.pathname)
    } else {
      window.history.replaceState(historyStateForPage(page, historyDepthRef.current), '', pagePath(page))
    }

    function handlePopState(event: PopStateEvent) {
      const next = pageFromHistoryState(event.state)
      if (next) {
        historyDepthRef.current = historyDepthFromState(event.state)
        setPage(next)
        return
      }
      // No SPA state on this history entry: re-resolve from the URL path.
      if (isDeepLinkPath(window.location.pathname)) {
        void resolveDeepLink(window.location.pathname)
        return
      }
      historyDepthRef.current = 0
      setPage({ name: 'boards' })
    }

    window.addEventListener('popstate', handlePopState)
    return () => window.removeEventListener('popstate', handlePopState)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Per-page <title> and canonical link for SEO / shareable links: search
  // engines and link unfurlers read the document title and canonical URL.
  useEffect(() => {
    const site = appearance?.siteTitle || 'Budgie'
    let title = site
    if (page.name === 'threads') title = `${page.board.name} · ${site}`
    else if (page.name === 'thread') title = `${page.thread.title} · ${site}`
    document.title = title

    const href = window.location.origin + pagePath(page)
    let link = document.head.querySelector('link[rel="canonical"]') as HTMLLinkElement | null
    if (!link) {
      link = document.createElement('link')
      link.rel = 'canonical'
      document.head.appendChild(link)
    }
    link.href = href
  }, [page, appearance])

  if (authLoading) {
    // Brief: bootstrapping the session from the cookie via /auth/me.
    return null
  }
  if (!auth.user && !guestMode) {
    return <AuthPage onLogin={login} onBrowseAsGuest={() => setGuestMode(true)} siteTitle={appearance?.siteTitle} tagline={appearance?.tagline} bannerURL={api.buildSiteAssetURL(appearance, 'banner')} />
  }

  // In guest mode auth.user is null; a synthetic guest principal keeps the
  // read-only render paths working while write/personal affordances are hidden.
  const isGuest = !auth.user
  const token = auth.token
  const user = auth.user ?? { id: '', name: 'guest', role: 'guest' }
  const exitGuest = () => setGuestMode(false)

  function nav(p: Page) {
    const nextDepth = historyDepthRef.current + 1
    historyDepthRef.current = nextDepth
    window.history.pushState(historyStateForPage(p, nextDepth), '', pagePath(p))
    setPage(p)
  }

  function replacePage(p: Page) {
    historyDepthRef.current = 0
    window.history.replaceState(historyStateForPage(p, 0), '', pagePath(p))
    setPage(p)
  }

  // resolveDeepLink loads the board/thread named in a /b/{id} or /t/{id} URL
  // (cold load, refresh, or shared link) and navigates to it, falling back to
  // the board list when the target is missing or not guest-readable. Uses the
  // current token (empty for guests — the content is public either way).
  async function resolveDeepLink(path: string) {
    const tok = auth.token
    const land = (p: Page) => {
      historyDepthRef.current = 0
      window.history.replaceState(historyStateForPage(p, 0), '', pagePath(p))
      setPage(p)
    }
    try {
      const tMatch = path.match(/^\/t\/([^/]+)/)
      const bMatch = path.match(/^\/b\/([^/]+)/)
      if (tMatch) {
        const res = await api.getThread(tok, decodeURIComponent(tMatch[1]))
        if (res.data?.thread) {
          const thread = res.data.thread
          const info = await api.getBoardInfo(tok, thread.board)
          const board = info.data?.board ?? ({ id: thread.board, name: thread.boardName || thread.board } as Board)
          land({ name: 'thread', board, thread })
          return
        }
      } else if (bMatch) {
        const res = await api.getBoardInfo(tok, decodeURIComponent(bMatch[1]))
        if (res.data?.board) {
          land({ name: 'threads', board: res.data.board })
          return
        }
      }
    } catch { /* fall through to the board list */ }
    land({ name: 'boards' })
  }

  function goBack(fallback: Page = { name: 'boards' }) {
    if (historyDepthRef.current > 0) {
      window.history.back()
      return
    }
    replacePage(fallback)
  }

  function handleLogout() {
    replacePage({ name: 'boards' })
    logout() // clears state and calls the cookie-based logout endpoint
  }

  function submitSearch(e: FormEvent) {
    e.preventDefault()
    if (searchDraft.trim()) {
      nav({ name: 'search', query: searchDraft.trim() })
      setSearchDraft('')
      searchRef.current?.blur()
    }
  }

  function sidebarItem(pageName: Page['name'], icon: string, label: string, onClick: () => void, badge?: number) {
    const active = page.name === pageName
    return (
      <button
        key={pageName}
        className={`sidebar-item${active ? ' sidebar-item--active' : ''}`}
        onClick={onClick}
      >
        <span className="sidebar-icon">{icon}</span>
        {label}
        {badge != null && badge > 0 && (
          <span className="sidebar-badge">{badge > 99 ? '99+' : badge}</span>
        )}
      </button>
    )
  }

  return (
    <div className="app">
      <aside className="sidebar">
        {/* Brand */}
        <div className="sidebar-brand" onClick={() => nav({ name: 'boards' })}>
          <span className="sidebar-logo">
            {api.buildSiteAssetURL(appearance, 'logo') && logoImgOk
              ? <img className="sidebar-logo-img" src={api.buildSiteAssetURL(appearance, 'logo')!} alt="" onError={() => setLogoImgOk(false)} />
              : (appearance?.logo || '🐦')}
          </span>
          <span className="sidebar-title">{appearance?.siteTitle || 'Budgie'}</span>
        </div>

        {/* Search */}
        <form className="sidebar-search-form" onSubmit={submitSearch}>
          <span className="sidebar-search-icon">⌕</span>
          <input
            ref={searchRef}
            className="sidebar-search-input"
            value={searchDraft}
            onChange={e => setSearchDraft(e.target.value)}
            placeholder={t('nav.searchPlaceholder')}
            aria-label={t('nav.searchAria')}
          />
        </form>

        {/* Primary nav */}
        <nav className="sidebar-nav">
          <span className="sidebar-section">{t('nav.boards')}</span>
          {sidebarItem('boards',        '⊞', t('nav.boards'),     () => nav({ name: 'boards' }))}
          {!isGuest && sidebarItem('unread',        '◉', t('nav.unread'),     () => nav({ name: 'unread' }))}
          {!isGuest && sidebarItem('resident-feed', '⊛', t('nav.resident'),   () => nav({ name: 'resident-feed' }))}
          <SidebarNavTree
            token={token}
            showFavorites={!isGuest}
            activeBoardId={page.name === 'threads' ? page.board.id : undefined}
            onOpenBoard={board => nav({ name: 'threads', board })}
          />

          {!isGuest && (
            <>
              <span className="sidebar-section">{t('nav.people')}</span>
              {sidebarItem('chat',     '◎', t('nav.chat'),    () => nav({ name: 'chat' }))}
              <button
                className={`sidebar-item${page.name === 'private' ? ' sidebar-item--active' : ''}`}
                onClick={() => { nav({ name: 'private' }); setPrivateUnreadCount(0) }}
              >
                <span className="sidebar-icon">✉</span>
                {t('nav.inbox')}
                {privateUnreadCount > 0 && (
                  <span className="sidebar-badge">{privateUnreadCount > 99 ? '99+' : privateUnreadCount}</span>
                )}
              </button>
              {sidebarItem('social',   '⊘', t('nav.people'),   () => nav({ name: 'social' }))}
              {sidebarItem('rankings', '◈', t('nav.rankings'), () => nav({ name: 'rankings' }))}
              {user.role === 'admin' && sidebarItem('admin', '⚙', t('nav.admin'), () => nav({ name: 'admin' }))}
            </>
          )}
        </nav>

        {/* Bottom: notifications + user + settings */}
        <div className="sidebar-bottom">
          {!isGuest && (
            <button
              className={`sidebar-item${page.name === 'notifications' ? ' sidebar-item--active' : ''}`}
              onClick={() => { nav({ name: 'notifications' }); setUnreadCount(0) }}
            >
              <span className="sidebar-icon">🔔</span>
              {t('nav.notifications')}
              {unreadCount > 0 && (
                <span className="sidebar-badge">{unreadCount > 99 ? '99+' : unreadCount}</span>
              )}
            </button>
          )}

          {!isGuest && (
            <button
              className="sidebar-user-btn"
              onClick={() => nav({ name: 'user-profile', username: user.name })}
              title={t('nav.openProfile')}
            >
              <span className="sidebar-avatar">{user.name.charAt(0).toUpperCase()}</span>
              <span className="sidebar-username">{user.name}</span>
            </button>
          )}

          <select
            className="sidebar-locale"
            value={locale}
            onChange={event => setLocale(event.currentTarget.value as LocaleCode)}
            aria-label={t('settings.language')}
          >
            <option value="en">EN</option>
            <option value="zh-CN">中文</option>
            <option value="zh-TW">中文（繁）</option>
          </select>

          <select
            className="sidebar-theme"
            value={theme}
            onChange={event => setTheme(event.currentTarget.value as ThemeId)}
            aria-label={t('settings.theme')}
          >
            <option value="dark">{t('settings.theme.dark')}</option>
            <option value="dim">{t('settings.theme.dim')}</option>
            <option value="light">{t('settings.theme.light')}</option>
            <option value="warm">{t('settings.theme.warm')}</option>
          </select>

          {isGuest ? (
            <button className="sidebar-item sidebar-signin" onClick={exitGuest}>
              <span className="sidebar-icon">→</span>
              {t('nav.signIn')}
            </button>
          ) : (
            <button className="sidebar-item" onClick={handleLogout}>
              <span className="sidebar-icon">↩</span>
              {t('nav.logout')}
            </button>
          )}
        </div>
      </aside>

      <main className="main-content">
        {showBanner && (
          <div className="site-banner">
            <span>{appearance?.bannerMessage}</span>
            <button type="button" className="site-banner-close" aria-label="Dismiss" onClick={dismissBanner}>×</button>
          </div>
        )}
        {isGuest && (
          <div className="guest-banner">
            <span>{t('guest.banner')} {t('guest.signInPrompt')}</span>
            <button type="button" className="guest-banner-signin" onClick={exitGuest}>{t('nav.signIn')}</button>
          </div>
        )}
        {page.name === 'boards' && (
          <BoardListPage
            token={token}
            currentUserRole={user.role}
            isGuest={isGuest}
            onSelect={board => nav({ name: 'threads', board })}
          />
        )}
        {page.name === 'threads' && (
          <ThreadListPage
            token={token}
            board={page.board}
            currentUserId={user.id}
            currentUserRole={user.role}
            isGuest={isGuest}
            onSelect={(thread, initialPostId) => nav({ name: 'thread', board: page.board, thread, initialPostId })}
            onBack={() => goBack({ name: 'boards' })}
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
            isGuest={isGuest}
            onRequireLogin={exitGuest}
            initialPostId={page.initialPostId}
            onBack={() => goBack({ name: 'threads', board: page.board })}
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
            onBack={() => goBack({ name: 'boards' })}
            onOpenAuthorPosts={username => nav({ name: 'author-posts', username })}
          />
        )}
        {page.name === 'author-posts' && (
          <AuthorPostsPage
            token={token}
            username={page.username}
            onBack={() => goBack({ name: 'user-profile', username: page.username })}
            onOpenThread={(board, thread, initialPostId) => nav({ name: 'thread', board, thread, initialPostId })}
          />
        )}
        {page.name === 'resident-feed' && (
          <ResidentFeedPage
            token={token}
            onBack={() => goBack({ name: 'boards' })}
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
            onBack={() => goBack({ name: 'threads', board: page.board })}
          />
        )}
        {page.name === 'chat' && (
          <ChatPage
            token={token}
            onBack={() => goBack({ name: 'boards' })}
            onMessageUser={username => nav({ name: 'private', messageTo: username })}
          />
        )}
        {page.name === 'search' && (
          <SearchPage
            token={token}
            initialQuery={page.query}
            onBack={() => goBack({ name: 'boards' })}
          />
        )}
        {page.name === 'notifications' && (
          <NotificationsPage
            token={token}
            onBack={() => goBack({ name: 'boards' })}
          />
        )}
        {page.name === 'unread' && (
          <UnreadPage
            token={token}
            onBack={() => goBack({ name: 'boards' })}
            onOpenThread={(board, thread, initialPostId) => nav({ name: 'thread', board, thread, initialPostId })}
          />
        )}
        {page.name === 'private' && (
          <PrivatePage
            token={token}
            onBack={() => goBack({ name: 'boards' })}
            currentUserRole={auth.user.role}
            initialMessageTo={page.messageTo}
          />
        )}
        {page.name === 'social' && (
          <SocialPage
            token={token}
            onBack={() => goBack({ name: 'boards' })}
            onOpenProfile={username => nav({ name: 'user-profile', username })}
            onMessageUser={username => nav({ name: 'private', messageTo: username })}
          />
        )}
        {page.name === 'rankings' && (
          <RankingsPage
            token={token}
            onBack={() => goBack({ name: 'boards' })}
            onOpenBoard={board => nav({ name: 'threads', board })}
            onOpenThread={(board, thread) => nav({ name: 'thread', board, thread })}
          />
        )}
        {page.name === 'admin' && (
          <AdminPage
            token={token}
            currentUserRole={user.role}
            onBack={() => goBack({ name: 'boards' })}
            onOpenBoard={board => nav({ name: 'threads', board })}
          />
        )}
      </main>
    </div>
  )
}

function historyStateForPage(page: Page, depth: number) {
  return { budgiePage: page, budgieDepth: depth }
}

// pagePath maps a page to its shareable, crawlable URL. Only boards and threads
// get distinct paths (these are what the sitemap lists); every other page lives
// at "/" and is restored from history state, not the URL.
function pagePath(page: Page): string {
  switch (page.name) {
    case 'threads': return `/b/${encodeURIComponent(page.board.id)}`
    case 'thread': return `/t/${encodeURIComponent(page.thread.id)}`
    default: return '/'
  }
}

// isDeepLinkPath reports whether a URL path targets a specific board or thread
// (so a cold load should resolve it rather than show the login wall / homepage).
function isDeepLinkPath(path: string): boolean {
  return /^\/(b|t)\/[^/]+/.test(path)
}

function pageFromHistoryState(state: unknown): Page | null {
  if (!state || typeof state !== 'object') return null
  const maybe = (state as { budgiePage?: unknown }).budgiePage
  if (!maybe || typeof maybe !== 'object') return null
  const name = (maybe as { name?: unknown }).name
  if (typeof name !== 'string') return null
  return maybe as Page
}

function historyDepthFromState(state: unknown) {
  if (!state || typeof state !== 'object') return 0
  const depth = (state as { budgieDepth?: unknown }).budgieDepth
  return typeof depth === 'number' && Number.isFinite(depth) && depth > 0 ? depth : 0
}

function getGuestSessionId() {
  const key = 'budgieGuestSessionId'
  const existing = window.localStorage.getItem(key)
  if (existing) return existing
  const generated = `guest_${window.crypto?.randomUUID?.() ?? `${Date.now()}_${Math.random().toString(36).slice(2)}`}`
  window.localStorage.setItem(key, generated)
  return generated
}
