import { useEffect, useState } from 'react'
import * as api from '../api/client'
import type { SocialUser } from '../api/types'
import { Spinner } from '../components/Spinner'

interface Props {
  token: string
  onBack: () => void
  onOpenProfile: (username: string) => void
  onMessageUser: (username: string) => void
}

type SocialTab = 'online-users' | 'online-friends' | 'friends' | 'fans' | 'ignores'

const TABS: Array<{ id: SocialTab; label: string }> = [
  { id: 'online-users', label: 'Online' },
  { id: 'online-friends', label: 'Friends online' },
  { id: 'friends', label: 'Friends' },
  { id: 'fans', label: 'Fans' },
  { id: 'ignores', label: 'Ignores' },
]

function presenceMode(user: SocialUser) {
  return user.mode || user.status || 'online'
}

function presenceLocation(user: SocialUser) {
  return user.locationLabel || user.boardName || user.boardId || ''
}

function formatIdle(seconds: number) {
  if (seconds < 60) return 'active now'
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m idle`
  return `${Math.floor(minutes / 60)}h idle`
}

export function SocialPage({ token, onBack, onOpenProfile, onMessageUser }: Props) {
  const [tab, setTab] = useState<SocialTab>('online-users')
  const [users, setUsers] = useState<SocialUser[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  async function load(nextTab = tab) {
    setLoading(true)
    setError(null)
    const res = nextTab === 'online-users'
      ? await api.listOnlineUsers(token)
      : await api.listSocialUsers(token, nextTab)
    setLoading(false)
    if (res.error) {
      setError(res.error.message)
      setUsers([])
      return
    }
    setUsers(res.data ?? [])
  }

  useEffect(() => { void load(tab) }, [token, tab])

  async function setRelation(username: string, kind: 'friend' | 'ignore', active: boolean) {
    const note = active && kind === 'friend' ? prompt('Friend note:', '') ?? '' : ''
    const res = await api.setUserRelationship(token, username, kind, active, note)
    if (res.error) {
      setError(res.error.message)
      return
    }
    await load(tab)
  }

  return (
    <div className="social-page">
      <div className="page-header">
        <button className="back-btn" onClick={onBack}>Back</button>
        <h2>People</h2>
        <div className="social-tabs">
          {TABS.map(t => (
            <button key={t.id} className={`social-tab${tab === t.id ? ' social-tab--active' : ''}`} onClick={() => setTab(t.id)}>
              {t.label}
            </button>
          ))}
        </div>
      </div>

      {error && <p className="error">{error}</p>}
      {loading ? (
        <Spinner />
      ) : users.length === 0 ? (
        <p className="muted empty-state">No users in this list.</p>
      ) : (
        <div className="social-list">
          {users.map(user => (
            <article key={`${tab}-${user.userId}-${user.sessionId ?? 'summary'}`} className={`social-row${user.online ? ' social-row--online' : ''}`}>
              <button className="post-author post-author-link social-name" onClick={() => onOpenProfile(user.name)}>
                {user.displayName || user.name}
              </button>
              <span className="muted">@{user.name}</span>
              {user.mutual && <span className="policy-badge">mutual</span>}
              {user.online && <span className="policy-badge">{presenceMode(user)}</span>}
              {presenceLocation(user) && <span className="policy-badge">{presenceLocation(user)}</span>}
              {user.idleSeconds > 0 && <span className="muted">{formatIdle(user.idleSeconds)}</span>}
              {user.fromHost && <span className="muted">{user.fromHost}</span>}
              {user.note && <span className="social-note">{user.note}</span>}
              <span className="social-spacer" />
              {tab !== 'ignores' ? (
                <>
                  <button className="link-btn" onClick={() => onMessageUser(user.name)}>Message</button>
                  <button className="link-btn" onClick={() => setRelation(user.name, 'friend', tab !== 'friends' && !user.mutual)}>
                    {tab === 'friends' || user.mutual ? 'Update friend' : 'Add friend'}
                  </button>
                  <button className="link-btn danger" onClick={() => setRelation(user.name, 'ignore', true)}>Ignore</button>
                </>
              ) : (
                <button className="link-btn" onClick={() => setRelation(user.name, 'ignore', false)}>Unignore</button>
              )}
              {tab === 'friends' && <button className="link-btn danger" onClick={() => setRelation(user.name, 'friend', false)}>Remove</button>}
            </article>
          ))}
        </div>
      )}
    </div>
  )
}
