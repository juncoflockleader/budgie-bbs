import { FormEvent, useEffect, useMemo, useState } from 'react'
import * as api from '../api/client'
import type { AccountRegistration, AccountRegistrationSettings, BoardSummary, Category, PasswordRecoveryRequest, UserSanction, SecuritySettings, BoardAutomodRule, BoardAutomodActivity, SiteAppearance } from '../api/types'
import { applyAppearance } from '../appearance'
import { TuiLayoutEditor } from '../components/TuiLayoutEditor'

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
  const [securitySettings, setSecuritySettings] = useState<SecuritySettings | null>(null)
  const [appearance, setAppearance] = useState<SiteAppearance | null>(null)
  const [appearanceDraft, setAppearanceDraft] = useState<SiteAppearance | null>(null)
  const [role2FA, setRole2FA] = useState<string | null>(null)
  const [automodBoard, setAutomodBoard] = useState('')
  const [automodRules, setAutomodRules] = useState<BoardAutomodRule[] | null>(null)
  const [automodActivity, setAutomodActivity] = useState<BoardAutomodActivity[]>([])
  const [ruleDraft, setRuleDraft] = useState<{ matchType: string; pattern: string; threshold: string; windowSec: string; actions: string[]; durationHours: string; reason: string }>({ matchType: 'keyword', pattern: '', threshold: '', windowSec: '', actions: ['manual_review'], durationHours: '', reason: '' })

  function toggleRuleAction(action: string) {
    setRuleDraft(p => ({ ...p, actions: p.actions.includes(action) ? p.actions.filter(a => a !== action) : [...p.actions, action] }))
  }

  async function loadAutomod(board: string) {
    setAutomodBoard(board)
    setAutomodRules(null)
    setAutomodActivity([])
    if (!board) return
    const [rulesRes, activityRes] = await Promise.all([
      api.listBoardAutomodRules(token, board),
      api.listBoardAutomodActivity(token, board),
    ])
    if (rulesRes.error) { setNotice({ kind: 'error', text: rulesRes.error.message }); return }
    setAutomodRules(rulesRes.data ?? [])
    setAutomodActivity(activityRes.data ?? [])
  }

  async function addAutomodRule(event: FormEvent) {
    event.preventDefault()
    if (!automodBoard) { setNotice({ kind: 'error', text: 'Select a board first.' }); return }
    const d = ruleDraft
    if (d.actions.length === 0) { setNotice({ kind: 'error', text: 'Select at least one action.' }); return }
    const payload: Record<string, unknown> = { board: automodBoard, matchType: d.matchType, action: d.actions.join(',') }
    if (d.matchType === 'keyword' || d.matchType === 'regex') payload.pattern = d.pattern.trim()
    if (['repeated_text', 'link_count', 'account_age', 'rate_threshold'].includes(d.matchType)) payload.threshold = Number(d.threshold) || 0
    if (d.matchType === 'rate_threshold') payload.windowSec = Number(d.windowSec) || 0
    if (d.actions.some(a => ['board_mute', 'board_ban', 'global_mute'].includes(a)) && d.durationHours.trim()) payload.durationSec = Math.round(Number(d.durationHours) * 3600)
    if (d.reason.trim()) payload.reason = d.reason.trim()
    setSaving('automod')
    setNotice(null)
    const res = await api.execCommandResolved(token, 'setBoardAutomodRule', payload)
    setSaving(null)
    if (res.error) { setNotice({ kind: 'error', text: res.error.message }); return }
    setNotice({ kind: 'ok', text: 'Automod rule added.' })
    setRuleDraft({ ...d, pattern: '', threshold: '', windowSec: '', durationHours: '', reason: '' })
    await loadAutomod(automodBoard)
  }

  async function deleteAutomodRule(id: string) {
    setSaving(`rule:${id}`)
    setNotice(null)
    const res = await api.execCommandResolved(token, 'deleteBoardAutomodRule', { board: automodBoard, id })
    setSaving(null)
    if (res.error) { setNotice({ kind: 'error', text: res.error.message }); return }
    await loadAutomod(automodBoard)
  }

  const categoryOptions = useMemo(() => buildCategoryOptions(categories), [categories])

  async function loadAdminData() {
    if (currentUserRole !== 'admin') return
    setLoading(true)
    const [boardsRes, categoriesRes, settingsRes, registrationsRes, recoveryRes, securityRes, appearanceRes] = await Promise.all([
      api.listBoardSummaries(token),
      api.listCategories(token),
      api.getAccountRegistrationSettings(token),
      api.listAccountRegistrations(token, 'pending', 50, 0),
      api.listPasswordRecoveryRequests(token, 'pending', 50, 0),
      api.getSecuritySettings(token),
      api.getSiteAppearance(),
    ])
    setLoading(false)

    const error = boardsRes.error ?? categoriesRes.error ?? settingsRes.error ?? registrationsRes.error ?? recoveryRes.error ?? securityRes.error
    if (error) {
      setNotice({ kind: 'error', text: error.message })
    }
    if (boardsRes.data) setBoards(boardsRes.data)
    if (categoriesRes.data) setCategories(categoriesRes.data)
    if (settingsRes.data) setRegistrationSettings(settingsRes.data)
    if (registrationsRes.data) setPendingRegistrations(registrationsRes.data)
    if (recoveryRes.data) setPasswordRecoveryRequests(recoveryRes.data)
    if (securityRes.data) setSecuritySettings(securityRes.data)
    if (appearanceRes.data) {
      setAppearance(appearanceRes.data)
      setAppearanceDraft(appearanceRes.data)
    }
  }

  async function saveAppearance(event: FormEvent) {
    event.preventDefault()
    if (!appearanceDraft) return
    setSaving('appearance')
    setNotice(null)
    const res = await api.setSiteAppearance(token, {
      siteTitle: appearanceDraft.siteTitle,
      logo: appearanceDraft.logo,
      tagline: appearanceDraft.tagline,
      bannerMessage: appearanceDraft.bannerMessage,
      accentColor: appearanceDraft.accentColor,
      defaultTheme: appearanceDraft.defaultTheme,
      mainMenuLayout: appearanceDraft.mainMenuLayout,
    })
    setSaving(null)
    if (res.error || !res.data) {
      setNotice({ kind: 'error', text: res.error?.message ?? 'Could not save appearance.' })
      return
    }
    setAppearance(res.data)
    setAppearanceDraft(res.data)
    applyAppearance(res.data)
    setNotice({ kind: 'ok', text: 'Site appearance updated.' })
  }

  async function setStaff2FARequired(required: boolean) {
    setSaving('security')
    setNotice(null)
    const res = await api.setSecuritySettings(token, required)
    setSaving(null)
    if (res.error) {
      setNotice({ kind: 'error', text: res.error.message })
      return
    }
    setSecuritySettings(res.data ?? null)
    setNotice({ kind: 'ok', text: required ? 'Staff 2FA is now required.' : 'Staff 2FA requirement disabled.' })
  }

  async function checkRole2FA() {
    const username = roleDraft.username.trim()
    if (!username) return
    setRole2FA('checking…')
    const res = await api.getUserTwoFactorStatus(token, username)
    if (res.error) {
      setRole2FA(`could not check: ${res.error.message}`)
      return
    }
    const s = res.data
    const enrolled = s && (s.totpEnrolled || s.emailEnrolled)
    setRole2FA(enrolled
      ? `${username} has 2FA enrolled (${[s?.totpEnrolled && 'authenticator', s?.emailEnrolled && 'email'].filter(Boolean).join(', ')}).`
      : `${username} has NOT enrolled 2FA — they would be locked out only if they cannot enroll.`)
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
            <button type="button" className="link-btn" onClick={checkRole2FA}>Check 2FA</button>
          </div>
          {role2FA && <p className="muted">{role2FA}</p>}
        </form>
      </section>

      <section className="admin-panel">
        <div className="admin-section-heading">
          <h3>Security</h3>
        </div>
        <label className="inline-toggle">
          <input
            type="checkbox"
            checked={Boolean(securitySettings?.staff2faRequired)}
            disabled={saving === 'security' || !securitySettings}
            onChange={e => setStaff2FARequired(e.target.checked)}
          />
          Require two-factor authentication for staff (admins &amp; moderators)
        </label>
        <p className="muted">Enrolled staff are prompted for a code at login. Confirm staff have enrolled (use “Check 2FA” above) before enabling, to avoid surprises.</p>
      </section>

      <section className="admin-panel">
        <div className="admin-section-heading">
          <h3>Site appearance</h3>
        </div>
        {appearanceDraft ? (
          <form className="admin-form" onSubmit={saveAppearance}>
            <div className="admin-form-grid">
              <label>
                Logo
                <input
                  type="text"
                  maxLength={16}
                  value={appearanceDraft.logo}
                  onChange={e => setAppearanceDraft({ ...appearanceDraft, logo: e.target.value })}
                  placeholder="🐦 (emoji or short glyph, shown before the title)"
                />
              </label>
              <label>
                Site title
                <input
                  type="text"
                  maxLength={80}
                  value={appearanceDraft.siteTitle}
                  onChange={e => setAppearanceDraft({ ...appearanceDraft, siteTitle: e.target.value })}
                  placeholder="Budgie BBS"
                />
              </label>
              <label>
                Tagline
                <input
                  type="text"
                  maxLength={200}
                  value={appearanceDraft.tagline}
                  onChange={e => setAppearanceDraft({ ...appearanceDraft, tagline: e.target.value })}
                  placeholder="Shown under the title on the sign-in page"
                />
              </label>
              <label>
                Default theme
                <select
                  value={appearanceDraft.defaultTheme}
                  onChange={e => setAppearanceDraft({ ...appearanceDraft, defaultTheme: e.target.value })}
                >
                  <option value="">— none (let visitors choose) —</option>
                  <option value="dark">Dark</option>
                  <option value="dim">Dim</option>
                  <option value="light">Light</option>
                  <option value="warm">Warm</option>
                </select>
              </label>
              <label>
                Accent color
                <span className="admin-color-row">
                  <input
                    type="color"
                    value={appearanceDraft.accentColor || '#000000'}
                    onChange={e => setAppearanceDraft({ ...appearanceDraft, accentColor: e.target.value })}
                  />
                  <input
                    type="text"
                    maxLength={7}
                    value={appearanceDraft.accentColor}
                    onChange={e => setAppearanceDraft({ ...appearanceDraft, accentColor: e.target.value })}
                    placeholder="#RRGGBB (blank = theme default)"
                  />
                  {appearanceDraft.accentColor && (
                    <button type="button" className="link-btn" onClick={() => setAppearanceDraft({ ...appearanceDraft, accentColor: '' })}>Clear</button>
                  )}
                </span>
              </label>
            </div>
            <label>
              Banner message
              <textarea
                maxLength={500}
                rows={2}
                value={appearanceDraft.bannerMessage}
                onChange={e => setAppearanceDraft({ ...appearanceDraft, bannerMessage: e.target.value })}
                placeholder="Site-wide announcement shown to signed-in members (blank = no banner)"
              />
            </label>
            <div className="admin-section-heading admin-subheading">
              <h4>SSH terminal main menu</h4>
            </div>
            <p className="muted">Compose what members see on the SSH/TUI main menu: stack ASCII-art banners, text, and the menu in any order. Art too wide for a member’s terminal is skipped automatically.</p>
            <TuiLayoutEditor
              value={appearanceDraft.mainMenuLayout ?? { blocks: [] }}
              onChange={(layout) => setAppearanceDraft({ ...appearanceDraft, mainMenuLayout: layout })}
            />
            <div className="admin-board-actions">
              <button type="submit" disabled={saving === 'appearance'}>
                {saving === 'appearance' ? 'Saving…' : 'Save appearance'}
              </button>
              {appearance && (
                <button
                  type="button"
                  className="link-btn"
                  onClick={() => setAppearanceDraft(appearance)}
                  disabled={saving === 'appearance'}
                >Reset</button>
              )}
            </div>
            <p className="muted">Title and accent apply site-wide immediately. The default theme only affects visitors who haven’t chosen one. The banner shows to all members until they dismiss it.</p>
          </form>
        ) : (
          <p className="muted">Loading…</p>
        )}
      </section>

      <section className="admin-panel">
        <div className="admin-section-heading">
          <h3>Automod rules</h3>
        </div>
        <label>
          Board
          <select value={automodBoard} onChange={e => loadAutomod(e.target.value)}>
            <option value="">— select a board —</option>
            {boards.map(b => <option key={b.id} value={b.id}>{b.name} ({b.id})</option>)}
          </select>
        </label>
        {automodBoard && (
          <>
            <form className="admin-form" onSubmit={addAutomodRule}>
              <div className="admin-form-grid">
                <label>
                  Match
                  <select value={ruleDraft.matchType} onChange={e => setRuleDraft(p => ({ ...p, matchType: e.target.value }))}>
                    <option value="keyword">keyword</option>
                    <option value="regex">regex</option>
                    <option value="repeated_text">repeated text</option>
                    <option value="link_count">link count</option>
                    <option value="account_age">account age (hrs)</option>
                    <option value="rate_threshold">rate threshold</option>
                  </select>
                </label>
                {(ruleDraft.matchType === 'keyword' || ruleDraft.matchType === 'regex') && (
                  <label>
                    Pattern
                    <input value={ruleDraft.pattern} onChange={e => setRuleDraft(p => ({ ...p, pattern: e.target.value }))} />
                  </label>
                )}
                {['repeated_text', 'link_count', 'account_age', 'rate_threshold'].includes(ruleDraft.matchType) && (
                  <label>
                    Threshold
                    <input value={ruleDraft.threshold} onChange={e => setRuleDraft(p => ({ ...p, threshold: e.target.value }))} inputMode="numeric" />
                  </label>
                )}
                {ruleDraft.matchType === 'rate_threshold' && (
                  <label>
                    Window (sec)
                    <input value={ruleDraft.windowSec} onChange={e => setRuleDraft(p => ({ ...p, windowSec: e.target.value }))} inputMode="numeric" />
                  </label>
                )}
                <fieldset className="automod-actions">
                  <legend>Actions</legend>
                  {[
                    ['manual_review', 'manual review'],
                    ['redact', 'redact post'],
                    ['lock_thread', 'lock thread'],
                    ['board_mute', 'board mute'],
                    ['board_ban', 'board ban'],
                    ['global_mute', 'global mute'],
                  ].map(([value, label]) => (
                    <label key={value} className="automod-action-option">
                      <input type="checkbox" checked={ruleDraft.actions.includes(value)} onChange={() => toggleRuleAction(value)} />
                      {label}
                    </label>
                  ))}
                </fieldset>
                {ruleDraft.actions.some(a => ['board_mute', 'board_ban', 'global_mute'].includes(a)) && (
                  <label>
                    Duration (hrs)
                    <input value={ruleDraft.durationHours} onChange={e => setRuleDraft(p => ({ ...p, durationHours: e.target.value }))} placeholder="blank = permanent" />
                  </label>
                )}
              </div>
              <label>
                Reason
                <input value={ruleDraft.reason} onChange={e => setRuleDraft(p => ({ ...p, reason: e.target.value }))} placeholder="optional, public/audit reason" maxLength={500} />
              </label>
              <div className="form-actions">
                <button type="submit" disabled={saving === 'automod'}>{saving === 'automod' ? 'Saving...' : 'Add rule'}</button>
              </div>
            </form>
            {automodRules !== null && (
              automodRules.length === 0 ? (
                <p className="muted">No automod rules for this board.</p>
              ) : (
                <ul className="admin-sanction-list">
                  {automodRules.map(r => (
                    <li key={r.id} className="admin-sanction-row">
                      <span>
                        <strong>{r.matchType}</strong>
                        {r.pattern ? ` “${r.pattern}”` : ''}
                        {r.threshold ? ` ≥${r.threshold}` : ''}
                        {r.windowSec ? `/${r.windowSec}s` : ''}
                        {' → '}{r.action}
                        {r.durationSec ? ` ${Math.round(r.durationSec / 3600)}h` : ''}
                        {r.enabled ? '' : ' · disabled'}
                        {r.reason ? ` · ${r.reason}` : ''}
                      </span>
                      <button type="button" className="link-btn danger" onClick={() => deleteAutomodRule(r.id)} disabled={saving === `rule:${r.id}`}>Delete</button>
                    </li>
                  ))}
                </ul>
              )
            )}
            <div className="admin-section-heading" style={{ marginTop: '0.75rem' }}>
              <h4>Recent automod activity</h4>
            </div>
            {automodActivity.length === 0 ? (
              <p className="muted">No automod actions recorded for this board yet.</p>
            ) : (
              <ul className="admin-sanction-list">
                {automodActivity.map(a => (
                  <li key={a.id} className="admin-sanction-row">
                    <span>
                      {new Date(a.ts).toLocaleString()} · <strong>{a.action}</strong> ({a.matchType})
                      {a.targetUserName ? ` · @${a.targetUserName}` : ''}
                      {a.reason ? ` · ${a.reason}` : ''}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </>
        )}
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
