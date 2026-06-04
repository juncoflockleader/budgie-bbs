import { FormEvent, useEffect, useState } from 'react'
import { Markup } from '../components/Markup'
import { Spinner } from '../components/Spinner'
import * as api from '../api/client'
import type { Post, UserProfile } from '../api/types'

interface Props {
  token: string
  username: string
  isOwnProfile: boolean
  onBack: () => void
}

const TL_LABEL = ['TL0', 'TL1', 'TL2', 'TL3', 'TL4']

export function UserProfilePage({ token, username, isOwnProfile, onBack }: Props) {
  const [profile, setProfile] = useState<UserProfile | null>(null)
  const [recentPosts, setRecentPosts] = useState<Post[]>([])
  const [loading, setLoading] = useState(true)
  const [loadingPosts, setLoadingPosts] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [editMode, setEditMode] = useState(false)
  const [saving, setSaving] = useState(false)
  const [displayName, setDisplayName] = useState('')
  const [bio, setBio] = useState('')
  const [avatar, setAvatar] = useState('')
  const [saveError, setSaveError] = useState<string | null>(null)

  async function loadProfile() {
    setLoading(true)
    setError(null)
    const res = await api.getUserProfile(token, username)
    setLoading(false)
    if (res.error) {
      setError(res.error.message)
      setProfile(null)
      return
    }
    if (res.data) {
      setProfile(res.data)
      setDisplayName(res.data.displayName)
      setBio(res.data.bio)
      setAvatar(res.data.avatar)
    }
  }

  async function loadPosts() {
    setLoadingPosts(true)
    const res = await api.listUserPosts(token, username, 20, 0)
    setLoadingPosts(false)
    if (res.error) return
    setRecentPosts(res.data ?? [])
  }

  useEffect(() => {
    setRecentPosts([])
    loadProfile()
    loadPosts()
  }, [token, username])

  async function submitProfile(event: FormEvent) {
    event.preventDefault()
    if (!profile) return

    setSaving(true)
    setSaveError(null)
    const res = await api.updateMyProfile(token, {
      displayName: displayName.trim(),
      bio: bio.trim(),
      avatar: avatar.trim(),
    })

    setSaving(false)
    if (res.error) {
      setSaveError(res.error.message)
      return
    }
    setProfile(prev => prev ? {
      ...prev,
      displayName: displayName.trim() || profile.name,
      bio: bio.trim(),
      avatar: avatar.trim(),
    } : prev)
    setEditMode(false)
  }

  if (loading) return <Spinner />
  if (error) return <p className="error">{error}</p>
  if (!profile) return <p className="muted">Profile not found.</p>

  const joinDate = new Date(profile.created).toLocaleDateString()
  const lastSeen = profile.lastSeen ? new Date(profile.lastSeen).toLocaleString() : 'Never'
  const trustLabel = TL_LABEL[profile.trustLevel] ?? `TL${profile.trustLevel}`

  return (
    <div className="user-profile-page">
      <div className="page-header">
        <button className="back-btn" onClick={onBack}>← Back</button>
        <h2>User Profile</h2>
        {isOwnProfile && !editMode && (
          <button className="link-btn" onClick={() => setEditMode(true)}>Edit</button>
        )}
      </div>

      <section className="profile-card">
        <div className="profile-identity">
          <div className="profile-avatar" aria-label="avatar">
            {profile.avatar || profile.displayName?.trim()?.[0]?.toUpperCase() || '@'}
          </div>
          <div className="profile-title">
            <h3>{profile.displayName || profile.name}</h3>
            <p className="muted">@{profile.name}</p>
            <span className={`trust-badge trust-badge--tl${profile.trustLevel}`} title={`Trust level ${profile.trustLevel}`}>
              {trustLabel}
            </span>
          </div>
        </div>

        <div className="profile-bio">
          <h4>Bio</h4>
          <Markup body={profile.bio || '*No bio yet.*'} />
        </div>

        <dl className="profile-stats">
          <div>
            <dt>Role</dt>
            <dd>{profile.role}</dd>
          </div>
          <div>
            <dt>Joined</dt>
            <dd>{joinDate}</dd>
          </div>
          <div>
            <dt>Last seen</dt>
            <dd>{lastSeen}</dd>
          </div>
          <div>
            <dt>Posts</dt>
            <dd>{profile.postsCreated}</dd>
          </div>
          <div>
            <dt>Reactions Received</dt>
            <dd>{profile.reactionsReceived}</dd>
          </div>
        </dl>
      </section>

      <section className="profile-pubkeys">
        <h4>SSH pubkeys</h4>
        {profile.pubkeys.length === 0 ? (
          <p className="muted">No SSH keys registered.</p>
        ) : (
          <ul className="profile-key-list">
            {profile.pubkeys.map((pubkey, index) => (
              <li key={`${pubkey}-${index}`}>{pubkey}</li>
            ))}
          </ul>
        )}
      </section>

      {isOwnProfile && editMode && (
        <form className="profile-edit-form" onSubmit={submitProfile}>
          <label>
            Display name
            <input value={displayName} onChange={e => setDisplayName(e.target.value)} placeholder="Display name" />
          </label>
          <label>
            Avatar
            <input value={avatar} onChange={e => setAvatar(e.target.value)} placeholder="Emoji or short ASCII art" />
          </label>
          <label>
            Bio (markup)
            <textarea value={bio} onChange={e => setBio(e.target.value)} rows={5} placeholder="Tell people about yourself" />
          </label>
          {saveError && <p className="error">{saveError}</p>}
          <div className="form-actions profile-form-actions">
            <button type="submit" disabled={saving}>{saving ? 'Saving…' : 'Save'}</button>
            <button className="link-btn" type="button" onClick={() => {
              setEditMode(false)
              if (profile) {
                setDisplayName(profile.displayName)
                setBio(profile.bio)
                setAvatar(profile.avatar)
              }
            }}>Cancel</button>
          </div>
        </form>
      )}

      <section className="profile-recent-posts">
        <h4>Recent Posts</h4>
        {loadingPosts ? (
          <Spinner />
        ) : recentPosts.length === 0 ? (
          <p className="muted">No visible posts yet.</p>
        ) : (
          <div className="recent-posts">
            {recentPosts.map(post => (
              <article key={post.id} className="recent-post-card">
                <header className="recent-post-meta">
                  <span className="muted">Thread {post.thread}</span>
                  <span className="muted">#{post.createdSeq}</span>
                </header>
                <div className="post-body post-body--small">
                  <Markup body={post.body} redacted={post.redacted} />
                </div>
              </article>
            ))}
          </div>
        )}
      </section>
    </div>
  )
}
