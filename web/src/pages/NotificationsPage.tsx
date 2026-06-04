import { useEffect, useState } from 'react'
import * as api from '../api/client'
import type { Notification } from '../api/types'
import { Spinner } from '../components/Spinner'

interface Props {
  token: string
  onBack: () => void
}

const KIND_LABEL: Record<Notification['kind'], string> = {
  mention: '@ mention',
  reply: '↩ reply',
  watched: '👁 watched',
  login: 'login',
}

export function NotificationsPage({ token, onBack }: Props) {
  const [notifs, setNotifs] = useState<Notification[]>([])
  const [unreadCount, setUnreadCount] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  async function load() {
    setLoading(true)
    const res = await api.listNotifications(token)
    setLoading(false)
    if (res.error) {
      setError(res.error.message)
    } else if (res.data) {
      setNotifs(res.data.notifications)
      setUnreadCount(res.data.unreadCount)
    }
  }

  useEffect(() => { void load() }, [token])

  async function markRead(id: string) {
    const target = notifs.find(n => n.id === id)
    const res = await api.markNotificationRead(token, id)
    if (res.error) {
      setError(res.error.message)
      return
    }
    setNotifs(prev => prev.map(n => n.id === id ? { ...n, read: true } : n))
    if (target && !target.read) setUnreadCount(prev => Math.max(0, prev - 1))
  }

  async function markAll() {
    const res = await api.markAllNotificationsRead(token)
    if (res.error) {
      setError(res.error.message)
      return
    }
    setNotifs(prev => prev.map(n => ({ ...n, read: true })))
    setUnreadCount(0)
  }

  async function deleteNotif(id: string) {
    const target = notifs.find(n => n.id === id)
    const res = await api.deleteNotification(token, id)
    if (res.error) {
      setError(res.error.message)
      return
    }
    setNotifs(prev => prev.filter(n => n.id !== id))
    if (target && !target.read) setUnreadCount(prev => Math.max(0, prev - 1))
  }

  async function clearRead() {
    const res = await api.clearNotifications(token, true)
    if (res.error) {
      setError(res.error.message)
      return
    }
    setNotifs(prev => prev.filter(n => !n.read))
  }

  async function clearAll() {
    if (!window.confirm('Clear all notifications?')) return
    const res = await api.clearNotifications(token)
    if (res.error) {
      setError(res.error.message)
      return
    }
    setNotifs([])
    setUnreadCount(0)
  }

  if (loading) return <Spinner />
  if (error) return <p className="error">{error}</p>

  const hasRead = notifs.some(n => n.read)

  return (
    <div className="notifications-page">
      <div className="page-header">
        <button className="back-btn" onClick={onBack}>← Back</button>
        <h2>Notifications {unreadCount > 0 && <span className="notif-badge">{unreadCount}</span>}</h2>
        {unreadCount > 0 && (
          <button className="link-btn" onClick={markAll}>Mark all read</button>
        )}
        {hasRead && <button className="link-btn" onClick={clearRead}>Clear read</button>}
        {notifs.length > 0 && <button className="link-btn danger" onClick={clearAll}>Clear all</button>}
      </div>

      {notifs.length === 0 ? (
        <p className="muted empty-state">No notifications yet.</p>
      ) : (
        <div className="notif-list">
          {notifs.map(n => (
            <div
              key={n.id}
              className={`notif-item${n.read ? '' : ' notif-item--unread'}`}
              onClick={() => { if (!n.read) markRead(n.id) }}
            >
              <span className="notif-kind">{KIND_LABEL[n.kind]}</span>
              <span className="notif-actor">{n.actor}</span>
              {n.kind === 'login' ? (
                <span className="muted"> is online</span>
              ) : (
                <>
                  <span className="muted"> in thread </span>
                  <span className="notif-thread">{n.threadId}</span>
                </>
              )}
              <span className="notif-time muted">{new Date(n.ts).toLocaleString()}</span>
              {!n.read && <span className="notif-dot" />}
              <button
                className="link-btn danger notif-delete"
                onClick={e => {
                  e.stopPropagation()
                  void deleteNotif(n.id)
                }}
              >
                Delete
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
