import { type FormEvent, type MouseEvent, useEffect, useState, useCallback } from 'react'
import * as api from '../api/client'
import type { Board, BoardInfo, BoardMember, BoardMemberApplication, BoardMemberRequirements, BoardSettings, DigestEntry, SocialUser, ThreadSummary } from '../api/types'
import type { BudgieEvent, ThreadNewPayload, ThreadTitleSetPayload } from '../api/types'
import { Spinner } from '../components/Spinner'
import { useStream } from '../hooks/useStream'
import { useI18n } from '../i18n'

interface Props {
  token: string
  board: Board
  currentUserId: string
  currentUserRole: string
  onSelect: (thread: ThreadSummary, initialPostId?: string) => void
  onBack: () => void
  onNewThread: () => void
  onMessageUser: (username: string) => void
}

export function ThreadListPage({ token, board, currentUserId, currentUserRole, onSelect, onBack, onNewThread, onMessageUser }: Props) {
  const { t } = useI18n()
  const [threads, setThreads] = useState<ThreadSummary[]>([])
  const [pinnedEntries, setPinnedEntries] = useState<DigestEntry[]>([])
  const [digestEntries, setDigestEntries] = useState<DigestEntry[]>([])
  const [onlineUsers, setOnlineUsers] = useState<SocialUser[]>([])
  const [memberApplications, setMemberApplications] = useState<BoardMemberApplication[]>([])
  const [boardInfo, setBoardInfo] = useState<BoardInfo | null>(null)
  const [settingsDraft, setSettingsDraft] = useState<BoardSettings | null>(null)
  const [requirementsDraft, setRequirementsDraft] = useState<BoardMemberRequirements | null>(null)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [moderatorName, setModeratorName] = useState('')
  const [memberName, setMemberName] = useState('')
  const [memberTitle, setMemberTitle] = useState('')
  const [memberCanManageMembers, setMemberCanManageMembers] = useState(false)
  const [memberCanCurate, setMemberCanCurate] = useState(false)
  const [memberCanModeratePosts, setMemberCanModeratePosts] = useState(false)
  const [memberCanModerateThreads, setMemberCanModerateThreads] = useState(false)
  const [memberCanAnnounce, setMemberCanAnnounce] = useState(false)
  const [memberCanManagePolls, setMemberCanManagePolls] = useState(false)
  const [memberCanSetBoardSettings, setMemberCanSetBoardSettings] = useState(false)
  const [titleQuery, setTitleQuery] = useState('')
  const [authorQuery, setAuthorQuery] = useState('')
  const [activeTitleQuery, setActiveTitleQuery] = useState('')
  const [activeAuthorQuery, setActiveAuthorQuery] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const approvalModeLabel: Record<string, string> = {
    manual: t('board.approvalMode.manual'),
    auto: t('board.approvalMode.auto'),
  }

  useEffect(() => {
    void api.setPresence(token, {
      status: `reading:${board.id}`,
      mode: 'reading',
      board: board.id,
      location: board.name,
    })
  }, [token, board.id, board.name])

  useEffect(() => {
    setLoading(true)
    setError(null)
    Promise.all([
      api.listThreads(token, board.id, 30, 0, false, { q: activeTitleQuery, author: activeAuthorQuery }),
      api.getBoardInfo(token, board.id),
      api.listDigestEntries(token, board.id, 'pinned'),
      api.listDigestEntries(token, board.id),
      api.listBoardMemberApplications(token, board.id, 'pending'),
      api.listBoardOnlineUsers(token, board.id),
    ]).then(([threadsRes, boardRes, pinnedRes, digestRes, applicationsRes, onlineRes]) => {
      setLoading(false)
      if (threadsRes.error) {
        setError(threadsRes.error.message)
        return
      }
      if (boardRes.error) {
        setError(boardRes.error.message)
        return
      }
      if (pinnedRes.error) {
        setError(pinnedRes.error.message)
        return
      }
      if (digestRes.error) {
        setError(digestRes.error.message)
        return
      }
      setThreads(threadsRes.data ?? [])
      setBoardInfo(boardRes.data ?? null)
      setSettingsDraft(boardRes.data?.settings ?? null)
      setRequirementsDraft(boardRes.data?.requirements ?? null)
      setPinnedEntries(pinnedRes.data ?? [])
      setDigestEntries((digestRes.data ?? []).filter(entry => entry.kind !== 'pinned'))
      setMemberApplications(applicationsRes.data ?? [])
      setOnlineUsers(onlineRes.data ?? [])
    })
  }, [token, board.id, activeTitleQuery, activeAuthorQuery])

  const onEvent = useCallback((evt: BudgieEvent) => {
    if (evt.event === 'thread.new') {
      const p = evt.payload as ThreadNewPayload
      if (p.board !== board.id) return
      setThreads(prev => {
        if (prev.find(t => t.id === p.id)) return prev
        if (!matchesThreadSearch({ title: p.title, author: p.author, authorId: p.authorId }, activeTitleQuery, activeAuthorQuery)) return prev
        return [{
          id: p.id,
          board: p.board,
          author: p.author,
          title: p.title,
          locked: false,
          postCount: 1,
          lastSeq: evt.seq ?? 0,
          createdTs: p.ts,
          createdAt: p.ts,
          updatedAt: p.ts,
          readSeq: 0,
          unreadPosts: 1,
        }, ...prev]
      })
    } else if (evt.event === 'thread.title_set') {
      const p = evt.payload as ThreadTitleSetPayload
      setThreads(prev => prev.map(thread => (
        thread.id === p.thread ? { ...thread, title: p.title, updatedAt: p.ts } : thread
      )).filter(thread => matchesThreadSearch(thread, activeTitleQuery, activeAuthorQuery)))
    }
  }, [board.id, activeTitleQuery, activeAuthorQuery])

  useStream({ token }, onEvent)

  if (loading) return <Spinner />
  if (error) return <p className="error">{error}</p>

  const settings = settingsDraft ?? boardInfo?.settings
  const requirements = requirementsDraft ?? boardInfo?.requirements
  const canManageBoard = currentUserRole === 'admin' || currentUserRole === 'moderator' || Boolean(boardInfo?.moderators.some(m => m.userId === currentUserId))
  const currentMember = boardInfo?.members.find(m => m.userId === currentUserId)
  const currentUserIsMember = Boolean(currentMember)
  const canManageBoardMembers = canManageBoard || Boolean(currentMember?.canManageMembers)
  const canEditBoardSettings = canManageBoard || Boolean(currentMember?.canSetBoardSettings)
  const canCurateBoard = canManageBoard || Boolean(currentMember?.canCurate)
  const canAnnounceBoard = canManageBoard || Boolean(currentMember?.canAnnounce)
  const canUseMemberBoard = canManageBoard || currentUserIsMember
  const canCreateThread = (!settings?.readOnly || canManageBoard) && (!settings?.memberPostMode || canUseMemberBoard)
  const canOpenBoardSettings = canEditBoardSettings || canManageBoardMembers

  function memberHasDelegatedPermissions(member: BoardMember) {
    return member.canManageMembers ||
      member.canCurate ||
      member.canModeratePosts ||
      member.canModerateThreads ||
      member.canAnnounce ||
      member.canManagePolls ||
      member.canSetBoardSettings
  }

  function memberRequiresBoardManager(member: BoardMember) {
    return memberHasDelegatedPermissions(member) || Boolean(boardInfo?.moderators.some(moderator => moderator.userId === member.userId))
  }

  function updateThread(threadId: string, patch: Partial<ThreadSummary>) {
    setThreads(current => current.map(thread => (thread.id === threadId ? { ...thread, ...patch } : thread)))
  }

  function toggleSetting(key: keyof Pick<BoardSettings,
    'anonymousAllowed' | 'readOnly' | 'noReply' | 'attachmentsAllowed' | 'mailInAllowed' | 'relayEnabled' | 'memberReadMode' | 'memberPostMode' | 'statsExcluded' | 'zapAllowed'>) {
    setSettingsDraft(current => current ? { ...current, [key]: !current[key] } : current)
  }

  function updateRequirement(key: keyof Pick<BoardMemberRequirements,
    'minLoginCount' | 'minPostCount' | 'minTrustLevel' | 'minScore' | 'minBoardPostCount' | 'minBoardOriginalPostCount' | 'minBoardDigestCount' | 'minBoardMarkCount' | 'maxMembers'>, rawValue: string) {
    const parsed = Number.parseInt(rawValue, 10)
    const value = Number.isFinite(parsed) ? Math.max(0, parsed) : 0
    setRequirementsDraft(current => current ? { ...current, [key]: value } : current)
  }

  function updateApprovalMode(mode: BoardMemberRequirements['approvalMode']) {
    setRequirementsDraft(current => current ? { ...current, approvalMode: mode } : current)
  }

  async function saveSettings() {
    if (!settingsDraft) return
    const res = await api.setBoardSettings(token, board.id, settingsDraft)
    if (res.error) {
      setError(res.error.message)
      return
    }
    const refreshed = await api.getBoardInfo(token, board.id)
    if (refreshed.error) {
      setError(refreshed.error.message)
      return
    }
    setBoardInfo(refreshed.data ?? null)
    setSettingsDraft(refreshed.data?.settings ?? null)
    setRequirementsDraft(refreshed.data?.requirements ?? null)
  }

  async function saveMemberRequirements() {
    if (!requirementsDraft) return
    const res = await api.setBoardMemberRequirements(token, board.id, {
      minLoginCount: requirementsDraft.minLoginCount,
      minPostCount: requirementsDraft.minPostCount,
      minTrustLevel: requirementsDraft.minTrustLevel,
      minScore: requirementsDraft.minScore,
      minBoardPostCount: requirementsDraft.minBoardPostCount,
      minBoardOriginalPostCount: requirementsDraft.minBoardOriginalPostCount,
      minBoardDigestCount: requirementsDraft.minBoardDigestCount,
      minBoardMarkCount: requirementsDraft.minBoardMarkCount,
      maxMembers: requirementsDraft.maxMembers,
      approvalMode: requirementsDraft.approvalMode,
    })
    if (res.error) {
      setError(res.error.message)
      return
    }
    const refreshed = await api.getBoardInfo(token, board.id)
    if (refreshed.error) {
      setError(refreshed.error.message)
      return
    }
    setBoardInfo(refreshed.data ?? null)
    setSettingsDraft(refreshed.data?.settings ?? null)
    setRequirementsDraft(refreshed.data?.requirements ?? null)
  }

  function submitThreadSearch(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    setActiveTitleQuery(titleQuery.trim())
    setActiveAuthorQuery(authorQuery.trim())
  }

  function clearThreadSearch() {
    setTitleQuery('')
    setAuthorQuery('')
    setActiveTitleQuery('')
    setActiveAuthorQuery('')
  }

  async function addModerator() {
    const name = moderatorName.trim()
    if (!name) return
    const res = await api.setBoardModerator(token, board.id, name, true)
    if (res.error) {
      setError(res.error.message)
      return
    }
    setModeratorName('')
    const refreshed = await api.getBoardInfo(token, board.id)
    if (refreshed.error) setError(refreshed.error.message)
    else setBoardInfo(refreshed.data ?? null)
  }

  async function removeModerator(name: string) {
    const res = await api.setBoardModerator(token, board.id, name, false)
    if (res.error) {
      setError(res.error.message)
      return
    }
    const refreshed = await api.getBoardInfo(token, board.id)
    if (refreshed.error) setError(refreshed.error.message)
    else setBoardInfo(refreshed.data ?? null)
  }

  async function addMember() {
    const name = memberName.trim()
    if (!name) return
    const permissions = canManageBoard
      ? {
          canManageMembers: memberCanManageMembers,
          canCurate: memberCanCurate,
          canModeratePosts: memberCanModeratePosts,
          canModerateThreads: memberCanModerateThreads,
          canAnnounce: memberCanAnnounce,
          canManagePolls: memberCanManagePolls,
          canSetBoardSettings: memberCanSetBoardSettings,
        }
      : {}
    const res = await api.setBoardMember(token, board.id, name, true, memberTitle.trim(), permissions)
    if (res.error) {
      setError(res.error.message)
      return
    }
    setMemberName('')
    setMemberTitle('')
    setMemberCanManageMembers(false)
    setMemberCanCurate(false)
    setMemberCanModeratePosts(false)
    setMemberCanModerateThreads(false)
    setMemberCanAnnounce(false)
    setMemberCanManagePolls(false)
    setMemberCanSetBoardSettings(false)
    const refreshed = await api.getBoardInfo(token, board.id)
    if (refreshed.error) setError(refreshed.error.message)
    else setBoardInfo(refreshed.data ?? null)
  }

  async function removeMember(name: string) {
    const res = await api.setBoardMember(token, board.id, name, false)
    if (res.error) {
      setError(res.error.message)
      return
    }
    const refreshed = await api.getBoardInfo(token, board.id)
    if (refreshed.error) setError(refreshed.error.message)
    else setBoardInfo(refreshed.data ?? null)
  }

  async function toggleMemberPermission(
    name: string,
    title: string | undefined,
    permissions: {
      position?: number
      canManageMembers: boolean
      canCurate: boolean
      canModeratePosts: boolean
      canModerateThreads: boolean
      canAnnounce: boolean
      canManagePolls: boolean
      canSetBoardSettings: boolean
    },
  ) {
    const res = await api.setBoardMember(token, board.id, name, true, title ?? '', permissions)
    if (res.error) {
      setError(res.error.message)
      return
    }
    const refreshed = await api.getBoardInfo(token, board.id)
    if (refreshed.error) setError(refreshed.error.message)
    else setBoardInfo(refreshed.data ?? null)
  }

  async function moveMember(name: string, title: string | undefined, direction: 'up' | 'down') {
    const members = boardInfo?.members ?? []
    const index = members.findIndex(member => member.name === name)
    const target = direction === 'up' ? members[index - 1] : members[index + 1]
    if (!target) return
    const member = members[index]
    const nextPosition = direction === 'up' ? target.position : target.position + 1
    const res = await api.setBoardMember(token, board.id, name, true, title ?? '', {
      position: nextPosition,
      canManageMembers: member.canManageMembers,
      canCurate: member.canCurate,
      canModeratePosts: member.canModeratePosts,
      canModerateThreads: member.canModerateThreads,
      canAnnounce: member.canAnnounce,
      canManagePolls: member.canManagePolls,
      canSetBoardSettings: member.canSetBoardSettings,
    })
    if (res.error) {
      setError(res.error.message)
      return
    }
    const refreshed = await api.getBoardInfo(token, board.id)
    if (refreshed.error) setError(refreshed.error.message)
    else setBoardInfo(refreshed.data ?? null)
  }

  async function refreshMemberApplications() {
    const res = await api.listBoardMemberApplications(token, board.id, 'pending')
    if (res.error) {
      setError(res.error.message)
      return
    }
    setMemberApplications(res.data ?? [])
  }

  async function reviewMembershipApplication(app: BoardMemberApplication, status: 'approved' | 'rejected' | 'blacklisted') {
    const title = status === 'approved' ? window.prompt(t('board.reviewMemberTitle'), app.title ?? '') : ''
    if (title === null) return
    const note = status !== 'approved' ? window.prompt(t('board.reviewNote'), '') : ''
    if (note === null) return
    const res = await api.reviewBoardMembership(token, app.id, { status, title: title ?? '', note: note ?? '' })
    if (res.error) {
      setError(res.error.message)
      return
    }
    const refreshed = await api.getBoardInfo(token, board.id)
    if (refreshed.error) setError(refreshed.error.message)
    else setBoardInfo(refreshed.data ?? null)
    await refreshMemberApplications()
  }

  async function applyForMembership() {
    const note = window.prompt(t('board.applicationNote'), '')
    if (note === null) return
    const res = await api.applyBoardMembership(token, board.id, note)
    if (res.error) {
      setError(res.error.message)
      return
    }
    const refreshed = await api.getBoardInfo(token, board.id)
    if (refreshed.error) setError(refreshed.error.message)
    else setBoardInfo(refreshed.data ?? null)
    await refreshMemberApplications()
  }

  async function leaveMembership() {
    if (!window.confirm(`${t('board.leaveBoardPrompt', { board: board.name })}`)) return
    const res = await api.leaveBoardMembership(token, board.id)
    if (res.error) {
      setError(res.error.message)
      return
    }
    const refreshed = await api.getBoardInfo(token, board.id)
    if (refreshed.error) setError(refreshed.error.message)
    else {
      setBoardInfo(refreshed.data ?? null)
      setRequirementsDraft(refreshed.data?.requirements ?? null)
    }
  }

  function openDigestEntry(entry: DigestEntry) {
    const thread = threads.find(item => item.id === entry.threadId) ?? {
      id: entry.threadId,
      board: board.id,
      author: entry.author ?? '',
      title: entry.title,
      locked: false,
      postCount: 0,
      lastSeq: 0,
      createdTs: entry.createdAt,
      createdAt: entry.createdAt,
      updatedAt: entry.updatedAt,
      readSeq: 0,
      unreadPosts: 0,
    }
    onSelect(thread, entry.postId)
  }

  async function removeDigest(entry: DigestEntry, e: MouseEvent<HTMLButtonElement>) {
    e.stopPropagation()
    const res = await api.removeDigestEntry(token, entry.id)
    if (res.error) {
      setError(res.error.message)
      return
    }
    setDigestEntries(current => current.filter(item => item.id !== entry.id))
    setPinnedEntries(current => current.filter(item => item.id !== entry.id))
  }

  function renderDigestEntry(entry: DigestEntry, removeTitle: string) {
    return (
      <li key={entry.id} className={`item-row digest-row${entry.kind === 'pinned' ? ' digest-row--pinned' : ''}`} onClick={() => openDigestEntry(entry)}>
        <span className="thread-row-content">
          <span className="item-title">{entry.title}</span>
          <span className="item-meta muted">
            {entry.kind}{entry.path ? ` / ${entry.path}` : ''}{entry.author ? ` · ${t('thread.by')} ${entry.author}` : ''}
          </span>
          {entry.note && <span className="item-desc muted">{entry.note}</span>}
          {entry.excerpt && <span className="item-desc">{entry.excerpt}</span>}
        </span>
        {(canCurateBoard || (entry.kind === 'announcement' && canAnnounceBoard)) && (
          <span className="thread-row-actions">
            <button className="board-action-btn" onClick={e => removeDigest(entry, e)} title={removeTitle}>×</button>
          </span>
        )}
      </li>
    )
  }

  function markRead(thread: ThreadSummary, e: MouseEvent<HTMLButtonElement>) {
    e.stopPropagation()
    const previous = threads
    updateThread(thread.id, { unreadPosts: 0, firstUnreadPostId: undefined, readSeq: thread.lastSeq })
    api.markThreadRead(token, thread.id).then(res => {
      if (res.error) {
        setThreads(previous)
        setError(res.error.message)
      }
    })
  }

  function restoreRead(thread: ThreadSummary, e: MouseEvent<HTMLButtonElement>) {
    e.stopPropagation()
    const previous = threads
    api.restoreThreadRead(token, thread.id).then(res => {
      if (res.error) {
        setThreads(previous)
        setError(res.error.message)
        return
      }
      api.listThreads(token, board.id).then(threadRes => {
        if (threadRes.error) {
          setThreads(previous)
          setError(threadRes.error.message)
          return
        }
        setThreads(threadRes.data ?? [])
      })
    })
  }

  return (
    <div className="thread-list">
      <div className="page-header">
        <button className="back-btn" onClick={onBack}>← {t('nav.boards')}</button>
        <h2>{board.name}</h2>
        {settings?.readOnly && <span className="policy-badge">{t('board.readOnly')}</span>}
        {settings?.noReply && <span className="policy-badge">{t('board.noReplies')}</span>}
        {settings?.anonymousAllowed && <span className="policy-badge">{t('board.anonymous')}</span>}
        {settings?.memberReadMode && <span className="policy-badge">{t('board.memberRead')}</span>}
        {settings?.memberPostMode && <span className="policy-badge">{t('board.memberPost')}</span>}
        {settings?.statsExcluded && <span className="policy-badge">{t('board.hiddenFromStats')}</span>}
        {settings && !settings.zapAllowed && <span className="policy-badge">{t('board.noZap')}</span>}
        {(settings?.memberReadMode || settings?.memberPostMode) && !currentUserIsMember && !canManageBoard && <button className="link-btn" onClick={applyForMembership}>{t('board.apply')}</button>}
        {currentUserIsMember && !canManageBoard && <button className="link-btn" onClick={leaveMembership}>{t('board.leave')}</button>}
        {canOpenBoardSettings && <button className="link-btn" onClick={() => setSettingsOpen(open => !open)}>{t('board.settings')}</button>}
        <button className="new-btn" onClick={onNewThread} disabled={!canCreateThread}>{t('board.createThread')}</button>
      </div>
      <form className="search-form thread-search-form" onSubmit={submitThreadSearch}>
        <input
          className="search-input"
          type="search"
          value={titleQuery}
          onChange={e => setTitleQuery(e.currentTarget.value)}
          placeholder={t('board.searchTitle')}
          aria-label={t('board.searchTitle')}
        />
        <input
          className="search-input"
          type="search"
          value={authorQuery}
          onChange={e => setAuthorQuery(e.currentTarget.value)}
          placeholder={t('board.searchAuthor')}
          aria-label={t('board.searchAuthor')}
        />
        <button type="submit" disabled={!titleQuery.trim() && !authorQuery.trim()}>{t('board.search')}</button>
        {(activeTitleQuery || activeAuthorQuery) && <button type="button" className="link-btn" onClick={clearThreadSearch}>{t('common.clear')}</button>}
      </form>
      {settingsOpen && canOpenBoardSettings && settingsDraft && (
        <section className="board-settings-panel">
          {canEditBoardSettings && (
            <>
              <div className="settings-grid">
                {([
                  ['anonymousAllowed', 'anonymous'],
                  ['readOnly', 'readOnly'],
                  ['noReply', 'noReplies'],
                  ['attachmentsAllowed', 'attachmentsAllowed'],
                  ['mailInAllowed', 'mailInAllowed'],
                  ['relayEnabled', 'relayEnabled'],
                  ['memberReadMode', 'memberReadMode'],
                  ['memberPostMode', 'memberPostMode'],
                  ['statsExcluded', 'hiddenFromStats'],
                  ['zapAllowed', 'zapAllowed'],
                ] as const).map(([key, label]) => (
                  <label key={key} className="setting-toggle">
                    <input type="checkbox" checked={settingsDraft[key]} onChange={() => toggleSetting(key)} />
                    {t(`board.${label}`)}
                  </label>
                ))}
              </div>
              <div className="board-settings-actions">
                <button onClick={saveSettings}>{t('board.saveSettings')}</button>
              </div>
              {requirements && (
                <div className="member-requirements-grid">
                  <label className="requirement-field">
                    <span>{t('board.approval')}</span>
                    <select value={requirements.approvalMode} onChange={e => updateApprovalMode(e.target.value as BoardMemberRequirements['approvalMode'])}>
                      <option value="manual">{approvalModeLabel.manual}</option>
                      <option value="auto">{approvalModeLabel.auto}</option>
                    </select>
                  </label>
                  <label className="requirement-field">
                    <span>{t('board.minLogins')}</span>
                    <input type="number" min={0} value={requirements.minLoginCount} onChange={e => updateRequirement('minLoginCount', e.target.value)} />
                  </label>
                  <label className="requirement-field">
                    <span>{t('board.minPosts')}</span>
                    <input type="number" min={0} value={requirements.minPostCount} onChange={e => updateRequirement('minPostCount', e.target.value)} />
                  </label>
                  <label className="requirement-field">
                    <span>{t('board.minScore')}</span>
                    <input type="number" min={0} value={requirements.minScore} onChange={e => updateRequirement('minScore', e.target.value)} />
                  </label>
                  <label className="requirement-field">
                    <span>{t('board.boardPosts')}</span>
                    <input type="number" min={0} value={requirements.minBoardPostCount} onChange={e => updateRequirement('minBoardPostCount', e.target.value)} />
                  </label>
                  <label className="requirement-field">
                    <span>{t('board.boardOriginals')}</span>
                    <input type="number" min={0} value={requirements.minBoardOriginalPostCount} onChange={e => updateRequirement('minBoardOriginalPostCount', e.target.value)} />
                  </label>
                  <label className="requirement-field">
                    <span>{t('board.boardDigests')}</span>
                    <input type="number" min={0} value={requirements.minBoardDigestCount} onChange={e => updateRequirement('minBoardDigestCount', e.target.value)} />
                  </label>
                  <label className="requirement-field">
                    <span>{t('board.boardMarks')}</span>
                    <input type="number" min={0} value={requirements.minBoardMarkCount} onChange={e => updateRequirement('minBoardMarkCount', e.target.value)} />
                  </label>
                  <label className="requirement-field">
                    <span>{t('board.minTrust')}</span>
                    <input type="number" min={0} value={requirements.minTrustLevel} onChange={e => updateRequirement('minTrustLevel', e.target.value)} />
                  </label>
                  <label className="requirement-field">
                    <span>{t('board.maxMembers')}</span>
                    <input type="number" min={0} value={requirements.maxMembers} onChange={e => updateRequirement('maxMembers', e.target.value)} />
                  </label>
                  <button onClick={saveMemberRequirements}>{t('board.saveRequirements')}</button>
                </div>
              )}
            </>
          )}
          {canManageBoard && <div className="moderator-row">
            <span className="muted">{t('board.moderators')}:</span>
            {boardInfo?.moderators.map(mod => (
              <span key={mod.userId} className="moderator-chip">
                {mod.name}
                {currentUserRole === 'admin' && <button className="link-btn" onClick={() => removeModerator(mod.name)}>×</button>}
              </span>
            ))}
            {currentUserRole === 'admin' && (
              <>
                <input
                  className="moderator-input"
                  value={moderatorName}
                  onChange={e => setModeratorName(e.target.value)}
                  placeholder={t('board.username')}
                />
                <button onClick={addModerator} disabled={!moderatorName.trim()}>{t('board.add')}</button>
              </>
            )}
          </div>}
          {canManageBoardMembers && <div className="moderator-row">
            <span className="muted">{t('board.members')}:</span>
            {boardInfo?.members.map((member, memberIndex) => (
              <span key={member.userId} className="moderator-chip">
                {member.name}{member.title ? ` · ${member.title}` : ''}
                {member.canManageMembers && <span className="member-role-badge">{t('board.memberRole.manageMembers')}</span>}
                {member.canCurate && <span className="member-role-badge">{t('board.memberRole.curate')}</span>}
                {member.canModeratePosts && <span className="member-role-badge">{t('board.memberRole.moderatePosts')}</span>}
                {member.canModerateThreads && <span className="member-role-badge">{t('board.memberRole.moderateThreads')}</span>}
                {member.canAnnounce && <span className="member-role-badge">{t('board.memberRole.announce')}</span>}
                {member.canManagePolls && <span className="member-role-badge">{t('board.memberRole.managePolls')}</span>}
                {member.canSetBoardSettings && <span className="member-role-badge">{t('board.memberRole.manageSettings')}</span>}
                {canManageBoard && (
                  <>
                    <button
                      className="link-btn"
                      disabled={memberIndex === 0}
                      onClick={() => moveMember(member.name, member.title, 'up')}
                    >
                      {t('board.favoriteActions.moveCategoryUp')}
                    </button>
                    <button
                      className="link-btn"
                      disabled={memberIndex >= (boardInfo?.members.length ?? 0) - 1}
                      onClick={() => moveMember(member.name, member.title, 'down')}
                    >
                      {t('board.favoriteActions.moveCategoryDown')}
                    </button>
                    <button
                      className="link-btn"
                      onClick={() => toggleMemberPermission(member.name, member.title, { ...member, canManageMembers: !member.canManageMembers })}
                    >
                      {member.canManageMembers ? t('board.roleDisable', { role: t('board.memberRole.manageMembers') }) : t('board.roleEnable', { role: t('board.memberRole.manageMembers') })}
                    </button>
                    <button
                      className="link-btn"
                      onClick={() => toggleMemberPermission(member.name, member.title, { ...member, canCurate: !member.canCurate })}
                    >
                      {member.canCurate ? t('board.roleDisable', { role: t('board.memberRole.curate') }) : t('board.roleEnable', { role: t('board.memberRole.curate') })}
                    </button>
                    <button
                      className="link-btn"
                      onClick={() => toggleMemberPermission(member.name, member.title, { ...member, canModeratePosts: !member.canModeratePosts })}
                    >
                      {member.canModeratePosts ? t('board.roleDisable', { role: t('board.memberRole.moderatePosts') }) : t('board.roleEnable', { role: t('board.memberRole.moderatePosts') })}
                    </button>
                    <button
                      className="link-btn"
                      onClick={() => toggleMemberPermission(member.name, member.title, { ...member, canModerateThreads: !member.canModerateThreads })}
                    >
                      {member.canModerateThreads ? t('board.roleDisable', { role: t('board.memberRole.moderateThreads') }) : t('board.roleEnable', { role: t('board.memberRole.moderateThreads') })}
                    </button>
                    <button
                      className="link-btn"
                      onClick={() => toggleMemberPermission(member.name, member.title, { ...member, canAnnounce: !member.canAnnounce })}
                    >
                      {member.canAnnounce ? t('board.roleDisable', { role: t('board.memberRole.announce') }) : t('board.roleEnable', { role: t('board.memberRole.announce') })}
                    </button>
                    <button
                      className="link-btn"
                      onClick={() => toggleMemberPermission(member.name, member.title, { ...member, canManagePolls: !member.canManagePolls })}
                    >
                      {member.canManagePolls ? t('board.roleDisable', { role: t('board.memberRole.managePolls') }) : t('board.roleEnable', { role: t('board.memberRole.managePolls') })}
                    </button>
                    <button
                      className="link-btn"
                      onClick={() => toggleMemberPermission(member.name, member.title, { ...member, canSetBoardSettings: !member.canSetBoardSettings })}
                    >
                      {member.canSetBoardSettings ? t('board.roleDisable', { role: t('board.memberRole.manageSettings') }) : t('board.roleEnable', { role: t('board.memberRole.manageSettings') })}
                    </button>
                  </>
                )}
                <button
                  className="link-btn"
                  disabled={!canManageBoard && memberRequiresBoardManager(member)}
                  title={!canManageBoard && memberRequiresBoardManager(member) ? t('board.boardModeratorRequired') : undefined}
                  onClick={() => removeMember(member.name)}
                >
                  ×
                </button>
              </span>
            ))}
            <input
              className="moderator-input"
              value={memberName}
              onChange={e => setMemberName(e.target.value)}
              placeholder="username"
            />
            <input
              className="moderator-input"
              value={memberTitle}
              onChange={e => setMemberTitle(e.target.value)}
              placeholder={t('board.memberTitle')}
            />
            {canManageBoard && (
              <>
                <label className="inline-toggle">
                  <input type="checkbox" checked={memberCanManageMembers} onChange={e => setMemberCanManageMembers(e.target.checked)} />
                  {t('board.memberRole.manageMembers')}
                </label>
                <label className="inline-toggle">
                  <input type="checkbox" checked={memberCanCurate} onChange={e => setMemberCanCurate(e.target.checked)} />
                  {t('board.memberRole.curate')}
                </label>
                <label className="inline-toggle">
                  <input type="checkbox" checked={memberCanModeratePosts} onChange={e => setMemberCanModeratePosts(e.target.checked)} />
                  {t('board.memberRole.moderatePosts')}
                </label>
                <label className="inline-toggle">
                  <input type="checkbox" checked={memberCanModerateThreads} onChange={e => setMemberCanModerateThreads(e.target.checked)} />
                  {t('board.memberRole.moderateThreads')}
                </label>
                <label className="inline-toggle">
                  <input type="checkbox" checked={memberCanAnnounce} onChange={e => setMemberCanAnnounce(e.target.checked)} />
                  {t('board.memberRole.announce')}
                </label>
                <label className="inline-toggle">
                  <input type="checkbox" checked={memberCanManagePolls} onChange={e => setMemberCanManagePolls(e.target.checked)} />
                  {t('board.memberRole.managePolls')}
                </label>
                <label className="inline-toggle">
                  <input type="checkbox" checked={memberCanSetBoardSettings} onChange={e => setMemberCanSetBoardSettings(e.target.checked)} />
                  {t('board.memberRole.manageSettings')}
                </label>
              </>
            )}
            <button onClick={addMember} disabled={!memberName.trim()}>{t('board.add')}</button>
          </div>}
          {canManageBoardMembers && memberApplications.length > 0 && (
            <div className="member-application-list">
              <span className="muted">Applications:</span>
              {memberApplications.map(app => (
                <span key={app.id} className="member-application-row">
                  <span>{app.name}{app.note ? ` · ${app.note}` : ''}</span>
                  <button className="link-btn" onClick={() => reviewMembershipApplication(app, 'approved')}>Approve</button>
                  <button className="link-btn" onClick={() => reviewMembershipApplication(app, 'rejected')}>Reject</button>
                  {canManageBoard && <button className="link-btn danger" onClick={() => reviewMembershipApplication(app, 'blacklisted')}>Blacklist</button>}
                </span>
              ))}
            </div>
          )}
        </section>
      )}
      {onlineUsers.length > 0 && (
        <section className="online-panel">
          <div className="board-section-heading">
            <h3 className="board-section-title">Online Now</h3>
          </div>
          <div className="online-chip-list">
            {onlineUsers.map(user => (
              <button
                key={`${user.userId}-${user.sessionId ?? 'summary'}`}
                className="online-chip"
                title={user.fromHost || undefined}
                onClick={() => onMessageUser(user.name)}
                disabled={user.userId === currentUserId}
              >
                <span>{user.displayName || user.name}</span>
                <span className="muted">{user.mode || user.status || 'online'}</span>
              </button>
            ))}
          </div>
        </section>
      )}
      {pinnedEntries.length > 0 && (
        <section className="digest-panel pinned-panel">
          <div className="board-section-heading">
            <h3 className="board-section-title">Pinned</h3>
          </div>
          <ul className="item-list">
            {pinnedEntries.map(entry => renderDigestEntry(entry, 'Remove from pinned index'))}
          </ul>
        </section>
      )}
      {digestEntries.length > 0 && (
        <section className="digest-panel">
          <div className="board-section-heading">
            <h3 className="board-section-title">Digest</h3>
          </div>
          <ul className="item-list">
            {digestEntries.map(entry => renderDigestEntry(entry, 'Remove from digest'))}
          </ul>
        </section>
      )}
      {threads.length === 0 && (
        <p className="muted">{activeTitleQuery || activeAuthorQuery ? 'No matching threads.' : 'No threads yet. Start one!'}</p>
      )}
      <ul className="item-list">
        {threads.map(t => (
          <li key={t.id} className="item-row thread-row" onClick={() => onSelect(t, t.firstUnreadPostId)}>
            <span className="thread-row-content">
              <span className="item-title">
                {t.locked && <span title="Locked">🔒 </span>}
                {t.title}
              </span>
              <span className="item-meta muted">{t.postCount} posts · by {t.author}</span>
              {t.unreadPosts > 0 && (
                <span className="item-meta unread-meta">
                  {t.unreadPosts} unread post{t.unreadPosts === 1 ? '' : 's'}
                </span>
              )}
            </span>
            <span className="thread-row-actions">
              {t.unreadPosts > 0 && (
                <button
                  className="board-action-btn"
                  onClick={e => markRead(t, e)}
                  title="Mark thread read"
                  aria-label={`Mark ${t.title} read`}
                >
                  ✓
                </button>
              )}
              {t.readSeq > 0 && (
                <button
                  className="board-action-btn"
                  onClick={e => restoreRead(t, e)}
                  title="Restore read marker"
                  aria-label={`Restore ${t.title} read marker`}
                >
                  ↶
                </button>
              )}
            </span>
          </li>
        ))}
      </ul>
    </div>
  )
}

function matchesThreadSearch(
  thread: Pick<ThreadSummary, 'title' | 'author' | 'authorId'>,
  titleQuery: string,
  authorQuery: string,
) {
  const title = titleQuery.trim().toLowerCase()
  const author = authorQuery.trim().toLowerCase()
  if (title && !thread.title.toLowerCase().includes(title)) return false
  if (author) {
    const haystack = `${thread.author} ${thread.authorId ?? ''}`.toLowerCase()
    if (!haystack.includes(author)) return false
  }
  return true
}
