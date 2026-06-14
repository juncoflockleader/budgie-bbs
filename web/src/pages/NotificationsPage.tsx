import { useEffect, useState } from 'react'
import * as api from '../api/client'
import type { Notification } from '../api/types'
import { Spinner } from '../components/Spinner'
import { useI18n } from '../i18n'

interface Props {
  token: string
  onBack: () => void
}

export function NotificationsPage({ token, onBack }: Props) {
  const { t } = useI18n()
  const [notifs, setNotifs] = useState<Notification[]>([])
  const [unreadCount, setUnreadCount] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  async function load() {
    setLoading(true)
    const res = await api.listNotifications(token)
    setLoading(false)
    if (res.error) {
      setError(t('common.errorPrefix', { message: res.error.message }))
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
      setError(t('common.errorPrefix', { message: res.error.message }))
      return
    }
    setNotifs(prev => prev.map(n => n.id === id ? { ...n, read: true } : n))
    if (target && !target.read) setUnreadCount(prev => Math.max(0, prev - 1))
  }

  async function markAll() {
    const res = await api.markAllNotificationsRead(token)
    if (res.error) {
      setError(t('common.errorPrefix', { message: res.error.message }))
      return
    }
    setNotifs(prev => prev.map(n => ({ ...n, read: true })))
    setUnreadCount(0)
  }

  async function deleteNotif(id: string) {
    const target = notifs.find(n => n.id === id)
    const res = await api.deleteNotification(token, id)
    if (res.error) {
      setError(t('common.errorPrefix', { message: res.error.message }))
      return
    }
    setNotifs(prev => prev.filter(n => n.id !== id))
    if (target && !target.read) setUnreadCount(prev => Math.max(0, prev - 1))
  }

  async function clearRead() {
    const res = await api.clearNotifications(token, true)
    if (res.error) {
      setError(t('common.errorPrefix', { message: res.error.message }))
      return
    }
    setNotifs(prev => prev.filter(n => !n.read))
  }

  async function clearAll() {
    if (!window.confirm(t('notifications.clear')) ) return
    const res = await api.clearNotifications(token)
    if (res.error) {
      setError(t('common.errorPrefix', { message: res.error.message }))
      return
    }
    setNotifs([])
    setUnreadCount(0)
  }

  if (loading) return <Spinner />
  if (error) return <p className="error">{error}</p>

  const hasRead = notifs.some(n => n.read)

  const kindLabel: Record<Notification['kind'], string> = {
    mention: t('notifications.mention'),
    reply: t('notifications.reply'),
    watched: t('notifications.watched'),
    login: t('notifications.loginOnline'),
  }

  return (
    <div className="notifications-page">
      <div className="page-header">
        <button className="back-btn" onClick={onBack}>← {t('common.back')}</button>
        <h2>{t('notifications.title')} {unreadCount > 0 && <span className="notif-badge">{unreadCount}</span>}</h2>
        {unreadCount > 0 && (
          <button className="link-btn" onClick={markAll}>{t('notifications.markAllRead')}</button>
        )}
        {hasRead && <button className="link-btn" onClick={clearRead}>{t('notifications.clearRead')}</button>}
        {notifs.length > 0 && <button className="link-btn danger" onClick={clearAll}>{t('notifications.clearAll')}</button>}
      </div>

      {notifs.length === 0 ? (
        <p className="muted empty-state">{t('notifications.noNotifications')}</p>
      ) : (
        <div className="notif-list">
          {notifs.map(n => (
            <div
              key={n.id}
              className={`notif-item${n.read ? '' : ' notif-item--unread'}`}
              onClick={() => { if (!n.read) markRead(n.id) }}
            >
              <span className="notif-kind">{kindLabel[n.kind]}</span>
              <span className="notif-actor">{n.actor}</span>
              {n.kind === 'login' ? (
                <span className="muted">{t('notifications.inThread')}</span>
              ) : (
                <>
                  <span className="muted">{t('notifications.inThread').trim()}</span>
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
                {t('notifications.delete')}
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
