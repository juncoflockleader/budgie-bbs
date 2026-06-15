import { FormEvent, useEffect, useMemo, useState } from 'react'
import * as api from '../api/client'
import type { AccountRegistration, AccountRegistrationSettings, BoardSummary, Category, PasswordRecoveryRequest, UserSanction } from '../api/types'

type Props = {
  token: string
  currentUserRole: string
  onBack: () => void
  onOpenBoard: (board: BoardSummary) => void
}

type RoleName = 'trusted' | 'moderator' | 'admin'

type Notice = {
  kind: 'ok' | 'error'
  text: string
}

export function AdminPage({ token, currentUserRole, onBack, onOpenBoard }: Props) {
  const [boards, setBoards] = useState<BoardSummary[]>([])
  const [categories, setCategories] = useState<Category[]>([])
  const [registrationSettings, setRegistrationSettings] = useState<AccountRegistrationSettings | null>(null)
  const [pendingRegistrations, setPendingRegistrations] = useState<AccountRegistration[]>([])
  const [passwordRecoveryRequests, setPasswordRecoveryRequests] = useState<PasswordRecoveryRequest[]>([])
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState<string | null>(null)
  const [notice, setNotice] = useState<Notice | null>(null)
  const [boardDraft, setBoardDraft] = useState({ id: '', name: '', description: '', parentId: '', position: '' })
  const [roleDraft, setRoleDraft] = useState<{ username: string; role: RoleName }>({ username: '', role: 'admin' })
  const [sanctionUserName, setSanctionUserName] = useState('')
  const [sanctionKind, setSanctionKind] = useState<'mute' | 'ban'>('mute')
  const [sanctionHours, setSanctionHours] = useState('')
  const [sanctionReason, setSanctionReason] = useState('')
  const [sanctions, setSanctions] = useState<UserSanction[] | null>(null)

  const categoryOptions = useMemo(() => buildCategoryOptions(categories), [categories])

  async function loadAdminData() {
    if (currentUserRole !== 'admin') return
    setLoading(true)
    const [boardsRes, categoriesRes, settingsRes, registrationsRes, recoveryRes] = await Promise.all([
      api.listBoardSummaries(token),
      api.listCategories(token),
      api.getAccountRegistrationSettings(token),
      api.listAccountRegistrations(token, 'pending', 50, 0),
      api.listPasswordRecoveryRequests(token, 'pending', 50, 0),
    ])
    setLoading(false)

    const error = boardsRes.error ?? categoriesRes.error ?? settingsRes.error ?? registrationsRes.error ?? recoveryRes.error
    if (error) {
      setNotice({ kind: 'error', text: error.message })
    }
    if (boardsRes.data) setBoards(boardsRes.data)
    if (categoriesRes.data) setCategories(categoriesRes.data)
    if (settingsRes.data) setRegistrationSettings(settingsRes.data)
    if (registrationsRes.data) setPendingRegistrations(registrationsRes.data)
    if (recoveryRes.data) setPasswordRecoveryRequests(recoveryRes.data)
  }

  useEffect(() => {
    loadAdminData()
  }, [token, currentUserRole])

  async function submitBoard(event: FormEvent) {
    event.preventDefault()
    const id = boardDraft.id.trim()
    const name = boardDraft.name.trim()
    const description = boardDraft.description.trim()
    const parentId = boardDraft.parentId.trim()
    const position = boardDraft.position.trim()
    if (!id || !name) {
      setNotice({ kind: 'error', text: 'Board id and name are required.' })
      return
    }
    if (position && (!Number.isInteger(Number(position)) || Number(position) < 0)) {
      setNotice({ kind: 'error', text: 'Position must be a non-negative integer.' })
      return
    }

    setSaving('board')
    setNotice(null)
    const res = await api.createBoard(token, {
      id,
      name,
      description,
      parentId,
      ...(position ? { position: Number(position) } : {}),
    })
    setSaving(null)
    if (res.error) {
      setNotice({ kind: 'error', text: res.error.message })
      return
    }
    setNotice({ kind: 'ok', text: `Board ${id} created.` })
    setBoardDraft({ id: '', name: '', description: '', parentId: '', position: '' })
    await loadAdminData()
  }

  async function submitGrantRole(event: FormEvent) {
    event.preventDefault()
    const username = roleDraft.username.trim()
    if (!username) {
      setNotice({ kind: 'error', text: 'Username is required.' })
      return
    }
    setSaving('role')
    setNotice(null)
    const res = await api.grantRole(token, username, roleDraft.role)
    setSaving(null)
    if (res.error) {
      setNotice({ kind: 'error', text: res.error.message })
      return
    }
    setNotice({ kind: 'ok', text: `${username} is now ${roleDraft.role}.` })
  }

  async function resetRole() {
    const username = roleDraft.username.trim()
    if (!username) {
      setNotice({ kind: 'error', text: 'Username is required.' })
      return
    }
    setSaving('role')
    setNotice(null)
    const res = await api.revokeRole(token, username, roleDraft.role)
    setSaving(null)
    if (res.error) {
      setNotice({ kind: 'error', text: res.error.message })
      return
    }
    setNotice({ kind: 'ok', text: `${username} reset to user.` })
  }

  async function lookupSanctions() {
    const username = sanctionUserName.trim()
    if (!username) {
      setNotice({ kind: 'error', text: 'Username is required.' })
      return
    }
    setSaving('sanction')
    setNotice(null)
    const res = await api.listUserSanctions(token, username)
    setSaving(null)
    if (res.error) {
      setNotice({ kind: 'error', text: res.error.message })
      setSanctions(null)
      return
    }
    setSanctions(res.data?.sanctions ?? [])
  }

  async function applySiteSanction(event: FormEvent) {
    event.preventDefault()
    const username = sanctionUserName.trim()
    if (!username) {
      setNotice({ kind: 'error', text: 'Username is required.' })
      return
    }
    let durationSec = 0
    if (sanctionHours.trim()) {
      const hours = Number(sanctionHours.trim())
      if (!Number.isFinite(hours) || hours <= 0) {
        setNotice({ kind: 'error', text: 'Duration must be a positive number of hours, or blank for permanent.' })
        return
      }
      durationSec = Math.round(hours * 3600)
    }
    setSaving('sanction')
    setNotice(null)
    const res = await api.sanctionUser(token, username, { kind: sanctionKind, scope: 'global', durationSec, reason: sanctionReason.trim() })
    setSaving(null)
    if (res.error) {
      setNotice({ kind: 'error', text: res.error.message })
      return
    }
    setNotice({ kind: 'ok', text: `${sanctionKind === 'ban' ? 'Ban' : 'Mute'} applied to ${username} site-wide.` })
    setSanctionReason('')
    setSanctionHours('')
    await lookupSanctions()
  }

  async function clearSanction(target: UserSanction) {
    const username = sanctionUserName.trim()
    if (!username) return
    setSaving(`sanction:${target.id}`)
    setNotice(null)
    const res = await api.clearUserSanction(token, username, { kind: target.kind, scope: target.scope })
    setSaving(null)
    if (res.error) {
      setNotice({ kind: 'error', text: res.error.message })
      return
    }
    setNotice({ kind: 'ok', text: `Lifted ${target.kind} for ${username}.` })
    await lookupSanctions()
  }

  function scopeLabel(scope: string): string {
    if (scope === 'global') return 'site-wide'
    return `board: ${boards.find(b => b.id === scope)?.name ?? scope}`
  }

  async function setRegistrationApprovalMode(requireApproval: boolean) {
    setSaving('registration')
    setNotice(null)
    const res = await api.setAccountRegistrationSettings(token, { requireApproval })
    setSaving(null)
    if (res.error) {
      setNotice({ kind: 'error', text: res.error.message })
      return
    }
    setRegistrationSettings(res.data ?? null)
    setNotice({ kind: 'ok', text: requireApproval ? 'Registration approval enabled.' : 'Registration approval disabled.' })
    await loadAdminData()
  }

  async function reviewRegistration(username: string, decision: 'approved' | 'rejected') {
    const reason = decision === 'rejected' ? window.prompt('Rejection reason:', '') ?? '' : ''
    setSaving(`registration:${username}`)
    setNotice(null)
    const res = await api.reviewAccountRegistration(token, username, { decision, reason })
    setSaving(null)
    if (res.error) {
      setNotice({ kind: 'error', text: res.error.message })
      return
    }
    setNotice({ kind: 'ok', text: decision === 'approved' ? `${username} approved.` : `${username} rejected.` })
    await loadAdminData()
  }

  async function reviewPasswordRecovery(request: PasswordRecoveryRequest, decision: 'reset' | 'rejected') {
    const newPassword = decision === 'reset' ? window.prompt(`New password for ${request.userName}:`, '') ?? '' : ''
    if (decision === 'reset' && !newPassword) return
    const note = decision === 'rejected' ? window.prompt('Review note:', '') ?? '' : ''
    setSaving(`recovery:${request.id}`)
    setNotice(null)
    const res = await api.reviewPasswordRecoveryRequest(token, request.id, { decision, newPassword, note })
    setSaving(null)
    if (res.error) {
      setNotice({ kind: 'error', text: res.error.message })
      return
    }
    setNotice({ kind: 'ok', text: decision === 'reset' ? `${request.userName} password reset.` : `${request.userName} recovery rejected.` })
    await loadAdminData()
  }

  if (currentUserRole !== 'admin') {
    return (
      <div className="admin-page">
        <header className="page-header">
          <button className="back-btn" onClick={onBack}>←</button>
          <h2>Admin</h2>
        </header>
        <section className="admin-panel">
          <p className="error">Admin role required.</p>
        </section>
      </div>
    )
  }

  return (
    <div className="admin-page">
      <header className="page-header">
        <button className="back-btn" onClick={onBack}>←</button>
        <h2>Admin</h2>
        <button type="button" className="link-btn" onClick={loadAdminData} disabled={loading}>Refresh</button>
      </header>

      {notice && <p className={notice.kind === 'ok' ? 'admin-notice admin-notice--ok' : 'admin-notice admin-notice--error'}>{notice.text}</p>}

      <section className="admin-panel">
        <div className="admin-section-heading">
          <h3>Boards</h3>
          <span className="muted">{boards.length}</span>
        </div>
        <form className="admin-form" onSubmit={submitBoard}>
          <div className="admin-form-grid">
            <label>
              ID
              <input
                value={boardDraft.id}
                onChange={e => setBoardDraft(prev => ({ ...prev, id: e.target.value }))}
                placeholder="linux"
              />
            </label>
            <label>
              Name
              <input
                value={boardDraft.name}
                onChange={e => setBoardDraft(prev => ({ ...prev, name: e.target.value }))}
                placeholder="Linux"
              />
            </label>
            <label>
              Parent
              <select value={boardDraft.parentId} onChange={e => setBoardDraft(prev => ({ ...prev, parentId: e.target.value }))}>
                <option value="">Root</option>
                {categoryOptions.map(({ category, depth }) => (
                  <option key={category.id} value={category.id}>{`${'  '.repeat(depth)}${category.name} (${category.id})`}</option>
                ))}
              </select>
            </label>
            <label>
              Position
              <input
                type="number"
                min="0"
                value={boardDraft.position}
                onChange={e => setBoardDraft(prev => ({ ...prev, position: e.target.value }))}
                placeholder="auto"
              />
            </label>
          </div>
          <label>
            Description
            <textarea
              rows={3}
              value={boardDraft.description}
              onChange={e => setBoardDraft(prev => ({ ...prev, description: e.target.value }))}
              placeholder="Board description"
            />
          </label>
          <div className="form-actions">
            <button type="submit" disabled={saving === 'board'}>{saving === 'board' ? 'Creating...' : 'Create board'}</button>
          </div>
        </form>

        <div className="admin-board-list">
          {loading && <p className="muted">Loading...</p>}
          {!loading && boards.length === 0 && <p className="muted">No boards yet.</p>}
          {!loading && boards.slice(0, 12).map(board => (
            <article key={board.id} className="admin-board-row">
              <button type="button" className="link-btn admin-board-name" onClick={() => onOpenBoard(board)}>
                {board.name}
              </button>
              <span className="muted">{board.id}</span>
              <span className="muted">{board.threadCount} threads / {board.postCount} posts</span>
            </article>
          ))}
        </div>
      </section>

      <section className="admin-panel">
        <div className="admin-section-heading">
          <h3>Roles</h3>
        </div>
        <form className="admin-form" onSubmit={submitGrantRole}>
          <div className="admin-form-grid admin-form-grid--role">
            <label>
              User
              <input
                value={roleDraft.username}
                onChange={e => setRoleDraft(prev => ({ ...prev, username: e.target.value }))}
                placeholder="username"
              />
            </label>
            <label>
              Role
              <select value={roleDraft.role} onChange={e => setRoleDraft(prev => ({ ...prev, role: e.target.value as RoleName }))}>
                <option value="admin">admin</option>
                <option value="moderator">moderator</option>
                <option value="trusted">trusted</option>
              </select>
            </label>
          </div>
          <div className="form-actions">
            <button type="submit" disabled={saving === 'role'}>{saving === 'role' ? 'Saving...' : 'Set role'}</button>
            <button type="button" className="link-btn danger" onClick={resetRole} disabled={saving === 'role'}>Reset to user</button>
          </div>
        </form>
      </section>

      <section className="admin-panel">
        <div className="admin-section-heading">
          <h3>Sanctions</h3>
        </div>
        <form className="admin-form" onSubmit={applySiteSanction}>
          <div className="admin-form-grid admin-form-grid--role">
            <label>
              User
              <input
                value={sanctionUserName}
                onChange={e => { setSanctionUserName(e.target.value); setSanctions(null) }}
                placeholder="username"
              />
            </label>
            <label>
              Kind
              <select value={sanctionKind} onChange={e => setSanctionKind(e.target.value as 'mute' | 'ban')}>
                <option value="mute">mute</option>
                <option value="ban">ban</option>
              </select>
            </label>
            <label>
              Duration (hours)
              <input
                value={sanctionHours}
                onChange={e => setSanctionHours(e.target.value)}
                placeholder="blank = permanent"
              />
            </label>
          </div>
          <label>
            Reason
            <input
              value={sanctionReason}
              onChange={e => setSanctionReason(e.target.value)}
              placeholder="optional, recorded in the audit log"
              maxLength={500}
            />
          </label>
          <div className="form-actions">
            <button type="submit" disabled={saving === 'sanction'}>{saving === 'sanction' ? 'Saving...' : 'Apply site sanction'}</button>
            <button type="button" className="link-btn" onClick={lookupSanctions} disabled={saving === 'sanction'}>View active</button>
          </div>
        </form>
        {sanctions !== null && (
          sanctions.length === 0 ? (
            <p className="muted">No active sanctions for {sanctionUserName.trim()}.</p>
          ) : (
            <ul className="admin-sanction-list">
              {sanctions.map(s => (
                <li key={s.id} className="admin-sanction-row">
                  <span>
                    <strong>{s.kind}</strong> · {scopeLabel(s.scope)}
                    {s.expiresAt ? ` · until ${new Date(s.expiresAt).toLocaleString()}` : ' · permanent'}
                    {s.reason ? ` · ${s.reason}` : ''}
                  </span>
                  <button type="button" className="link-btn danger" onClick={() => clearSanction(s)} disabled={saving === `sanction:${s.id}`}>Lift</button>
                </li>
              ))}
            </ul>
          )
        )}
      </section>

      <section className="admin-panel">
        <div className="admin-section-heading">
          <h3>Registration</h3>
          <label className="inline-toggle">
            <input
              type="checkbox"
              checked={Boolean(registrationSettings?.requireApproval)}
              onChange={e => setRegistrationApprovalMode(e.target.checked)}
              disabled={saving === 'registration' || !registrationSettings}
            />
            Require approval
          </label>
        </div>
        <div className="registration-review-list">
          {pendingRegistrations.length === 0 ? (
            <p className="muted">No pending registrations.</p>
          ) : pendingRegistrations.map(row => (
            <article key={row.id} className="registration-review-item">
              <div>
                <strong>{row.name}</strong>
                <p className="muted">{formatDate(row.created)}</p>
                {(row.realName || row.affiliation || row.email) && (
                  <p className="muted">
                    {[row.realName, row.affiliation, row.email].filter(Boolean).join(' · ')}
                  </p>
                )}
                {row.note && <p className="muted">{row.note}</p>}
              </div>
              <div className="form-actions profile-form-actions">
                <button type="button" onClick={() => reviewRegistration(row.name, 'approved')} disabled={saving === `registration:${row.name}`}>Approve</button>
                <button type="button" className="link-btn danger" onClick={() => reviewRegistration(row.name, 'rejected')} disabled={saving === `registration:${row.name}`}>Reject</button>
              </div>
            </article>
          ))}
        </div>
      </section>

      <section className="admin-panel">
        <div className="admin-section-heading">
          <h3>Password Recovery</h3>
          <span className="muted">{passwordRecoveryRequests.length}</span>
        </div>
        <div className="registration-review-list">
          {passwordRecoveryRequests.length === 0 ? (
            <p className="muted">No pending recovery requests.</p>
          ) : passwordRecoveryRequests.map(row => (
            <article key={row.id} className="registration-review-item">
              <div>
                <strong>{row.userName}</strong>
                <p className="muted">{row.submittedName || 'No real name'} / {row.submittedEmail || 'No email'}</p>
                {row.note && <p className="muted">{row.note}</p>}
              </div>
              <div className="form-actions profile-form-actions">
                <button type="button" onClick={() => reviewPasswordRecovery(row, 'reset')} disabled={saving === `recovery:${row.id}`}>Reset</button>
                <button type="button" className="link-btn danger" onClick={() => reviewPasswordRecovery(row, 'rejected')} disabled={saving === `recovery:${row.id}`}>Reject</button>
              </div>
            </article>
          ))}
        </div>
      </section>
    </div>
  )
}

function buildCategoryOptions(categories: Category[]) {
  const children: Record<string, Category[]> = {}
  categories.forEach(category => {
    const parent = category.parentId ?? ''
    children[parent] = children[parent] ?? []
    children[parent].push(category)
  })
  Object.values(children).forEach(group => group.sort(compareCategories))

  const out: Array<{ category: Category; depth: number }> = []
  const seen = new Set<string>()
  const walk = (parent: string, depth: number) => {
    for (const category of children[parent] ?? []) {
      if (seen.has(category.id)) continue
      seen.add(category.id)
      out.push({ category, depth })
      walk(category.id, depth + 1)
    }
  }
  walk('', 0)
  categories.filter(category => !seen.has(category.id)).sort(compareCategories).forEach(category => {
    out.push({ category, depth: 0 })
  })
  return out
}

function compareCategories(a: Category, b: Category) {
  if (a.position !== b.position) return a.position - b.position
  return a.name.localeCompare(b.name)
}

function formatDate(ms: number) {
  if (!ms) return 'Never'
  return new Date(ms).toLocaleString()
}
