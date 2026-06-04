import { FormEvent, useEffect, useState } from 'react'
import * as api from '../api/client'
import type { AttachmentPayload, DirectMessage, DirectMessageConversation, DirectMessageSettings, MailAttachment, MailGroup, MailItem, MailUsage } from '../api/types'
import { AttachmentComposer } from '../components/AttachmentComposer'
import { Spinner } from '../components/Spinner'

interface Props {
  token: string
  onBack: () => void
  currentUserRole: string
  initialMessageTo?: string
}

const MAILBOXES = ['inbox', 'sent', 'keep', 'trash']
const MESSAGE_POLICIES: Array<{ value: DirectMessageSettings['policy']; label: string }> = [
  { value: 'all', label: 'All users' },
  { value: 'friends', label: 'Friends only' },
  { value: 'none', label: 'No messages' },
]

export function PrivatePage({ token, onBack, currentUserRole, initialMessageTo = '' }: Props) {
  const [tab, setTab] = useState<'mail' | 'messages'>(initialMessageTo ? 'messages' : 'mail')
  const [mailbox, setMailbox] = useState('inbox')
  const [mail, setMail] = useState<MailItem[]>([])
  const [selectedMail, setSelectedMail] = useState<MailItem | null>(null)
  const [selectedMailIDs, setSelectedMailIDs] = useState<string[]>([])
  const [relatedMail, setRelatedMail] = useState<MailItem[]>([])
  const [relatedTitle, setRelatedTitle] = useState('')
  const [relatedLoading, setRelatedLoading] = useState(false)
  const [mailUnread, setMailUnread] = useState(0)
  const [mailGroups, setMailGroups] = useState<MailGroup[]>([])
  const [mailUsage, setMailUsage] = useState<MailUsage | null>(null)
  const [conversations, setConversations] = useState<DirectMessageConversation[]>([])
  const [messageUnread, setMessageUnread] = useState(0)
  const [selectedConversation, setSelectedConversation] = useState<string>('')
  const [messages, setMessages] = useState<DirectMessage[]>([])
  const [messagePolicy, setMessagePolicy] = useState<DirectMessageSettings['policy']>('all')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const [mailTo, setMailTo] = useState('')
  const [mailToGroups, setMailToGroups] = useState<string[]>([])
  const [mailToFriends, setMailToFriends] = useState(false)
  const [mailToAll, setMailToAll] = useState(false)
  const [mailSubject, setMailSubject] = useState('')
  const [mailBody, setMailBody] = useState('')
  const [mailAttachments, setMailAttachments] = useState<AttachmentPayload[]>([])
  const [replyTo, setReplyTo] = useState<string | undefined>()
  const [forwardSource, setForwardSource] = useState<string | undefined>()
  const [forwardSourceTitle, setForwardSourceTitle] = useState('')
  const [groupName, setGroupName] = useState('')
  const [groupMembers, setGroupMembers] = useState('')
  const [editingGroup, setEditingGroup] = useState<string | undefined>()
  const [messageTo, setMessageTo] = useState(initialMessageTo)
  const [messageBody, setMessageBody] = useState('')

  async function loadMail(nextMailbox = mailbox) {
    const res = await api.listMail(token, nextMailbox)
    if (res.error) {
      setError(res.error.message)
      return
    }
    const rows = res.data?.mail ?? []
    setMail(rows)
    setSelectedMailIDs(prev => prev.filter(id => rows.some(item => item.id === id)))
    setMailUnread(res.data?.unreadCount ?? 0)
  }

  async function loadMailGroups() {
    const res = await api.listMailGroups(token)
    if (res.error) {
      setError(res.error.message)
      return
    }
    setMailGroups(res.data?.groups ?? [])
  }

  async function loadMailUsage() {
    const res = await api.getMailUsage(token)
    if (res.error) {
      setError(res.error.message)
      return
    }
    setMailUsage(res.data ?? null)
  }

  async function loadConversations() {
    const res = await api.listDirectConversations(token)
    if (res.error) {
      setError(res.error.message)
      return
    }
    const rows = res.data?.conversations ?? []
    setConversations(rows)
    setMessageUnread(res.data?.unreadCount ?? 0)
    if (!selectedConversation && rows[0]) {
      setSelectedConversation(rows[0].name)
    }
  }

  async function loadMessages(name: string) {
    if (!name) {
      setMessages([])
      return
    }
    const res = await api.listDirectMessages(token, name)
    if (res.error) {
      setError(res.error.message)
      return
    }
    const rows = res.data?.messages ?? []
    setMessages(rows)
    await Promise.all(rows.filter(m => !m.mine && !m.read).map(m => api.markDirectMessageRead(token, m.id)))
    if (rows.some(m => !m.mine && !m.read)) {
      void loadConversations()
    }
  }

  async function loadMessageSettings() {
    const res = await api.getDirectMessageSettings(token)
    if (res.error) {
      setError(res.error.message)
      return
    }
    setMessagePolicy(res.data?.policy ?? 'all')
  }

  async function loadAll() {
    setLoading(true)
    setError(null)
    await Promise.all([loadMail(mailbox), loadMailGroups(), loadMailUsage(), loadConversations(), loadMessageSettings()])
    setLoading(false)
  }

  useEffect(() => { void loadAll() }, [token])
  useEffect(() => { void loadMail(mailbox) }, [mailbox])
  useEffect(() => { void loadMessages(selectedConversation) }, [selectedConversation])
  useEffect(() => {
    if (!initialMessageTo) return
    setTab('messages')
    setMessageTo(initialMessageTo)
    setSelectedConversation(initialMessageTo)
  }, [initialMessageTo])

  async function openMail(item: MailItem, keepRelated = false) {
    const res = await api.getMail(token, item.id)
    if (res.error) {
      setError(res.error.message)
      return
    }
    const full = res.data ?? item
    setSelectedMail(full)
    if (!keepRelated) {
      setRelatedMail([])
      setRelatedTitle('')
    }
    if (!full.read) {
      await api.updateMail(token, full.id, { read: true })
      void loadMail(mailbox)
    }
  }

  async function sendMail(e: FormEvent) {
    e.preventDefault()
    const to = mailTo.split(/[,\s]+/).map(v => v.trim()).filter(Boolean)
    const common = {
      to,
      toGroups: mailToGroups,
      toFriends: mailToFriends,
      toAll: currentUserRole === 'admin' && mailToAll,
      subject: mailSubject,
    }
    const res = forwardSource
      ? await api.forwardMail(token, forwardSource, { ...common, note: mailBody })
      : await api.sendMail(token, { ...common, body: mailBody, replyTo, attachments: cleanAttachments(mailAttachments) })
    if (res.error) {
      setError(res.error.message)
      return
    }
    setMailTo('')
    setMailToGroups([])
    setMailToFriends(false)
    setMailToAll(false)
    setMailSubject('')
    setMailBody('')
    setMailAttachments([])
    setReplyTo(undefined)
    setForwardSource(undefined)
    setForwardSourceTitle('')
    await Promise.all([loadMail(mailbox), loadMailUsage()])
  }

  function toggleMailGroup(groupID: string) {
    setMailToGroups(prev => prev.includes(groupID) ? prev.filter(id => id !== groupID) : [...prev, groupID])
  }

  function clearGroupForm() {
    setGroupName('')
    setGroupMembers('')
    setEditingGroup(undefined)
  }

  function editGroup(group: MailGroup) {
    if (group.builtIn) return
    setEditingGroup(group.id)
    setGroupName(group.name)
    setGroupMembers(group.members.map(member => member.name).join(', '))
  }

  async function saveMailGroup(e: FormEvent) {
    e.preventDefault()
    const members = groupMembers.split(/[,\s]+/).map(v => v.trim()).filter(Boolean)
    const res = await api.setMailGroup(token, { group: editingGroup, name: groupName, members })
    if (res.error) {
      setError(res.error.message)
      return
    }
    clearGroupForm()
    await loadMailGroups()
  }

  async function removeMailGroup(group: MailGroup) {
    if (group.builtIn) return
    const res = await api.deleteMailGroup(token, group.id)
    if (res.error) {
      setError(res.error.message)
      return
    }
    setMailToGroups(prev => prev.filter(id => id !== group.id))
    if (editingGroup === group.id) clearGroupForm()
    await loadMailGroups()
  }

  async function moveMail(item: MailItem, nextMailbox: string) {
    const res = await api.updateMail(token, item.id, { mailbox: nextMailbox })
    if (res.error) {
      setError(res.error.message)
      return
    }
    setSelectedMail(prev => prev?.id === item.id ? { ...prev, mailbox: nextMailbox } : prev)
    await Promise.all([loadMail(mailbox), loadMailUsage()])
  }

  function toggleMailSelection(item: MailItem) {
    setSelectedMailIDs(prev => prev.includes(item.id) ? prev.filter(id => id !== item.id) : [...prev, item.id])
  }

  async function trashSelectedMail() {
    if (selectedMailIDs.length === 0) return
    const ids = selectedMailIDs
    const res = await api.deleteMailRange(token, ids)
    if (res.error) {
      setError(res.error.message)
      return
    }
    setSelectedMailIDs([])
    if (selectedMail && ids.includes(selectedMail.id)) {
      setSelectedMail(null)
      setRelatedMail([])
      setRelatedTitle('')
    }
    await Promise.all([loadMail(mailbox), loadMailUsage()])
  }

  async function toggleKept(item: MailItem) {
    const res = await api.updateMail(token, item.id, { kept: !item.kept })
    if (res.error) {
      setError(res.error.message)
      return
    }
    setSelectedMail(prev => prev?.id === item.id ? { ...prev, kept: !item.kept } : prev)
    await loadMail(mailbox)
  }

  async function trashMail(item: MailItem) {
    const res = await api.deleteMail(token, item.id)
    if (res.error) {
      setError(res.error.message)
      return
    }
    if (selectedMail?.id === item.id) {
      setSelectedMail(null)
      setRelatedMail([])
      setRelatedTitle('')
    }
    await Promise.all([loadMail(mailbox), loadMailUsage()])
  }

  function startReply(item: MailItem) {
    setTab('mail')
    setMailTo(item.fromName)
    setMailSubject(item.subject.toLowerCase().startsWith('re:') ? item.subject : `Re: ${item.subject}`)
    setMailBody('')
    setReplyTo(item.id)
    setForwardSource(undefined)
    setForwardSourceTitle('')
  }

  function startForward(item: MailItem) {
    setTab('mail')
    setMailTo('')
    setMailSubject(item.subject.toLowerCase().startsWith('fwd:') ? item.subject : `Fwd: ${item.subject}`)
    setMailBody('')
    setMailAttachments([])
    setReplyTo(undefined)
    setForwardSource(item.id)
    setForwardSourceTitle(item.subject)
  }

  async function loadMailRelation(item: MailItem, mode: 'thread' | 'author') {
    setRelatedLoading(true)
    setError(null)
    const res = mode === 'thread' ? await api.listMailThread(token, item.id) : await api.listMailByAuthor(token, item.id)
    setRelatedLoading(false)
    if (res.error) {
      setError(res.error.message)
      return
    }
    setRelatedTitle(mode === 'thread' ? 'Thread' : `From ${item.fromName || 'author'}`)
    setRelatedMail(res.data?.mail ?? [])
  }

  async function uploadMailAttachment(item: MailItem) {
    const file = await pickMailAttachmentFile()
    if (!file) return
    const res = await api.uploadMailAttachment(token, item.id, file)
    if (res.error) {
      setError(res.error.message)
      return
    }
    const refreshed = await api.getMail(token, item.id)
    if (refreshed.error) {
      setError(refreshed.error.message)
      return
    }
    setSelectedMail(refreshed.data ?? item)
    await Promise.all([loadMail(mailbox), loadMailUsage()])
  }

  async function downloadMailAttachment(att: MailAttachment) {
    const res = await api.downloadMailAttachment(token, att.id, att.filename)
    if (res.error) setError(res.error.message)
  }

  async function sendMessage(e: FormEvent) {
    e.preventDefault()
    const to = messageTo || selectedConversation
    const res = await api.sendDirectMessage(token, { to, body: messageBody })
    if (res.error) {
      setError(res.error.message)
      return
    }
    setMessageTo('')
    setMessageBody('')
    if (to) setSelectedConversation(to)
    await loadConversations()
    await loadMessages(to)
  }

  async function saveMessageSettings(e: FormEvent) {
    e.preventDefault()
    const res = await api.setDirectMessageSettings(token, { policy: messagePolicy })
    if (res.error) {
      setError(res.error.message)
      return
    }
  }

  async function removeMessage(message: DirectMessage) {
    const res = await api.deleteDirectMessage(token, message.id)
    if (res.error) {
      setError(res.error.message)
      return
    }
    await loadMessages(selectedConversation)
  }

  if (loading) return <Spinner />

  return (
    <div className="private-page">
      <div className="page-header">
        <button className="back-btn" onClick={onBack}>Back</button>
        <h2>Inbox {(mailUnread + messageUnread) > 0 && <span className="notif-badge">{mailUnread + messageUnread}</span>}</h2>
        {mailUsage && <span className="policy-badge">{formatBytes(mailUsage.usedBytes)} / {formatBytes(mailUsage.quotaBytes)}</span>}
        <button className={`private-tab${tab === 'mail' ? ' private-tab--active' : ''}`} onClick={() => setTab('mail')}>
          Mail {mailUnread > 0 && <span className="notif-badge">{mailUnread}</span>}
        </button>
        <button className={`private-tab${tab === 'messages' ? ' private-tab--active' : ''}`} onClick={() => setTab('messages')}>
          Messages {messageUnread > 0 && <span className="notif-badge">{messageUnread}</span>}
        </button>
      </div>

      {error && <p className="error">{error}</p>}

      {tab === 'mail' ? (
        <div className="private-layout">
          <section className="private-pane">
            <div className="mailbox-tabs">
              {MAILBOXES.map(box => (
                <button
                  key={box}
                  className={`mailbox-tab${mailbox === box ? ' mailbox-tab--active' : ''}`}
                  onClick={() => { setMailbox(box); setSelectedMailIDs([]) }}
                >
                  {box}
                </button>
              ))}
            </div>
            {selectedMailIDs.length > 0 && (
              <div className="mail-range-toolbar">
                <span>{selectedMailIDs.length} selected</span>
                <button className="link-btn danger" onClick={trashSelectedMail}>Trash selected</button>
                <button className="link-btn" onClick={() => setSelectedMailIDs([])}>Clear</button>
              </div>
            )}
            <div className="private-list">
              {mail.length === 0 ? (
                <p className="muted empty-state">No mail.</p>
              ) : mail.map(item => (
                <div className="mail-range-row" key={`${item.id}-${item.role}`}>
                  <label className="mail-range-check" title="Select for range trash">
                    <input type="checkbox" checked={selectedMailIDs.includes(item.id)} onChange={() => toggleMailSelection(item)} />
                  </label>
                  <button
                    className={`private-list-row${selectedMail?.id === item.id ? ' private-list-row--active' : ''}${item.read ? '' : ' private-list-row--unread'}`}
                    onClick={() => openMail(item)}
                  >
                    <span className="private-row-title">{item.subject}</span>
                    <span className="private-row-meta">{item.fromName} / {item.toNames.join(', ')}</span>
                    <span className="private-row-excerpt">{item.excerpt}</span>
                  </button>
                </div>
              ))}
            </div>
            <section className="private-subsection">
              <h3>Mail groups</h3>
              <form className="private-mini-form" onSubmit={saveMailGroup}>
                <label>Name<input value={groupName} onChange={e => setGroupName(e.target.value)} required /></label>
                <label>Members<input value={groupMembers} onChange={e => setGroupMembers(e.target.value)} /></label>
                <div className="form-actions">
                  <button type="submit">{editingGroup ? 'Update' : 'Create'}</button>
                  {editingGroup && <button type="button" className="link-btn" onClick={clearGroupForm}>Cancel</button>}
                </div>
              </form>
              <div className="mail-group-list">
                {mailGroups.length === 0 ? (
                  <p className="muted empty-state">No groups.</p>
                ) : mailGroups.map(group => (
                  <div className="mail-group-row" key={group.id}>
                    <div>
                      <strong>{group.name}</strong>
                      <span className="muted">{group.members.map(member => member.name).join(', ') || 'Empty'}</span>
                    </div>
                    {group.builtIn ? (
                      <span className="muted">Built-in</span>
                    ) : (
                      <>
                        <button className="link-btn" onClick={() => editGroup(group)}>Edit</button>
                        <button className="link-btn danger" onClick={() => removeMailGroup(group)}>Delete</button>
                      </>
                    )}
                  </div>
                ))}
              </div>
            </section>
          </section>

          <section className="private-pane private-pane--detail">
            {selectedMail ? (
              <article className="mail-detail">
                <div className="mail-detail-header">
                  <h3>{selectedMail.subject}</h3>
                  <span className="muted">{new Date(selectedMail.createdAt).toLocaleString()}</span>
                </div>
                <p className="muted">From {selectedMail.fromName} to {selectedMail.toNames.join(', ')}</p>
                <pre className="mail-body">{selectedMail.body}</pre>
                {selectedMail.attachments && selectedMail.attachments.length > 0 && (
                  <div className="post-attachments">
                    {selectedMail.attachments.map(att => (
                      <span className="post-attachment" key={att.id}>
                        <strong>{att.filename}</strong>
                        {att.sizeBytes ? <span>{Math.round(att.sizeBytes / 1024)} KB</span> : null}
                        {att.url && <a href={att.url} target="_blank" rel="noreferrer">Open</a>}
                        {att.stored && <button className="link-btn" onClick={() => downloadMailAttachment(att)}>Download</button>}
                      </span>
                    ))}
                  </div>
                )}
                <div className="private-actions">
                  <button className="link-btn" onClick={() => startReply(selectedMail)}>Reply</button>
                  <button className="link-btn" onClick={() => startForward(selectedMail)}>Forward</button>
                  <button className="link-btn" onClick={() => loadMailRelation(selectedMail, 'thread')}>Thread</button>
                  <button className="link-btn" onClick={() => loadMailRelation(selectedMail, 'author')}>From author</button>
                  {selectedMail.role === 'sender' && <button className="link-btn" onClick={() => uploadMailAttachment(selectedMail)}>Upload file</button>}
                  <button className="link-btn" onClick={() => toggleKept(selectedMail)}>{selectedMail.kept ? 'Unkeep' : 'Keep'}</button>
                  <button className="link-btn" onClick={() => moveMail(selectedMail, 'inbox')}>Inbox</button>
                  <button className="link-btn" onClick={() => moveMail(selectedMail, 'keep')}>Keep box</button>
                  <button className="link-btn danger" onClick={() => trashMail(selectedMail)}>Trash</button>
                </div>
                {(relatedTitle || relatedLoading) && (
                  <section className="mail-related">
                    <div className="mail-related-header">
                      <h4>{relatedTitle || 'Mail'}</h4>
                      {relatedLoading && <span className="muted">Loading...</span>}
                    </div>
                    {!relatedLoading && relatedMail.length === 0 ? (
                      <p className="muted empty-state">No matching mail.</p>
                    ) : relatedMail.map(item => (
                      <button
                        className={`private-list-row${selectedMail.id === item.id ? ' private-list-row--active' : ''}${item.read ? '' : ' private-list-row--unread'}`}
                        key={`${item.id}-${item.role}`}
                        onClick={() => openMail(item, true)}
                      >
                        <span className="private-row-title">{item.subject}</span>
                        <span className="private-row-meta">{item.fromName} / {new Date(item.createdAt).toLocaleString()} / {item.mailbox}</span>
                        <span className="private-row-excerpt">{item.excerpt}</span>
                      </button>
                    ))}
                  </section>
                )}
              </article>
            ) : (
              <p className="muted empty-state">No message selected.</p>
            )}

            <form className="private-compose" onSubmit={sendMail}>
              <label>To<input value={mailTo} onChange={e => setMailTo(e.target.value)} /></label>
              <div className="private-options">
                {mailGroups.map(group => (
                  <label className="check-row" key={group.id}>
                    <input type="checkbox" checked={mailToGroups.includes(group.id)} onChange={() => toggleMailGroup(group.id)} />
                    <span>{group.name}</span>
                  </label>
                ))}
                <label className="check-row">
                  <input type="checkbox" checked={mailToFriends} onChange={e => setMailToFriends(e.target.checked)} />
                  <span>Friends</span>
                </label>
                {currentUserRole === 'admin' && (
                  <label className="check-row">
                    <input type="checkbox" checked={mailToAll} onChange={e => setMailToAll(e.target.checked)} />
                    <span>All users</span>
                  </label>
                )}
              </div>
              <label>Subject<input value={mailSubject} onChange={e => setMailSubject(e.target.value)} /></label>
              {forwardSource && <p className="muted">Forwarding {forwardSourceTitle}</p>}
              <label>{forwardSource ? 'Note' : 'Body'}<textarea value={mailBody} onChange={e => setMailBody(e.target.value)} required={!forwardSource} /></label>
              {!forwardSource && <AttachmentComposer attachments={mailAttachments} onChange={setMailAttachments} />}
              <div className="form-actions">
                <button type="submit">{forwardSource ? 'Forward mail' : replyTo ? 'Send reply' : 'Send mail'}</button>
                {replyTo && <button type="button" className="link-btn" onClick={() => setReplyTo(undefined)}>Clear reply</button>}
                {forwardSource && <button type="button" className="link-btn" onClick={() => { setForwardSource(undefined); setForwardSourceTitle('') }}>Clear forward</button>}
              </div>
            </form>
          </section>
        </div>
      ) : (
        <div className="private-layout">
          <section className="private-pane">
            <div className="private-list">
              {conversations.length === 0 ? (
                <p className="muted empty-state">No messages.</p>
              ) : conversations.map(c => (
                <button
                  key={c.userId}
                  className={`private-list-row${selectedConversation === c.name ? ' private-list-row--active' : ''}${c.unreadCount > 0 ? ' private-list-row--unread' : ''}`}
                  onClick={() => setSelectedConversation(c.name)}
                >
                  <span className="private-row-title">{c.name} {c.unreadCount > 0 && <span className="notif-badge">{c.unreadCount}</span>}</span>
                  <span className="private-row-meta">{c.lastFromName} / {new Date(c.lastAt).toLocaleString()}</span>
                  <span className="private-row-excerpt">{c.lastBody}</span>
                </button>
              ))}
            </div>
          </section>

          <section className="private-pane private-pane--detail">
            <form className="message-settings" onSubmit={saveMessageSettings}>
              <label>Receive
                <select value={messagePolicy} onChange={e => setMessagePolicy(e.target.value as DirectMessageSettings['policy'])}>
                  {MESSAGE_POLICIES.map(policy => <option key={policy.value} value={policy.value}>{policy.label}</option>)}
                </select>
              </label>
              <button type="submit">Save</button>
            </form>
            <div className="message-thread">
              {messages.length === 0 ? (
                <p className="muted empty-state">No conversation selected.</p>
              ) : messages.map(m => (
                <div key={m.id} className={`message-bubble${m.mine ? ' message-bubble--mine' : ''}`}>
                  <div className="message-bubble-meta">
                    <span>{m.mine ? 'You' : m.fromName}</span>
                    <span className="muted">{new Date(m.createdAt).toLocaleString()}</span>
                    <button className="link-btn danger" onClick={() => removeMessage(m)}>Delete</button>
                  </div>
                  <p>{m.body}</p>
                </div>
              ))}
            </div>
            <form className="private-compose" onSubmit={sendMessage}>
              <label>To<input value={messageTo || selectedConversation} onChange={e => setMessageTo(e.target.value)} required /></label>
              <label>Message<textarea value={messageBody} onChange={e => setMessageBody(e.target.value)} required /></label>
              <button type="submit">Send message</button>
            </form>
          </section>
        </div>
      )}
    </div>
  )
}

function cleanAttachments(items: AttachmentPayload[]) {
  return items
    .map(item => ({
      ...item,
      filename: item.filename.trim(),
      contentType: item.contentType?.trim(),
      url: item.url?.trim(),
      sizeBytes: Number(item.sizeBytes) || 0,
    }))
    .filter(item => item.filename)
}

function formatBytes(value: number) {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${Math.round(value / 1024)} KB`
  return `${(value / (1024 * 1024)).toFixed(1)} MB`
}

function pickMailAttachmentFile() {
  return new Promise<File | null>(resolve => {
    const input = document.createElement('input')
    input.type = 'file'
    input.onchange = () => resolve(input.files?.[0] ?? null)
    input.oncancel = () => resolve(null)
    input.click()
  })
}
