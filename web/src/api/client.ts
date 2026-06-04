import type {
  AckResult,
  ApiResponse,
  AttachmentPayload,
  AuthResponse,
  Board,
  BoardInfo,
  BoardMemberApplication,
  BoardMemberRequirements,
  BoardSettings,
  BoardSummary,
  BoardRanking,
  BlessingRanking,
  ArchiveRanking,
  Category,
  CommunityStats,
  DirectMessage,
  DirectMessageConversation,
  DirectMessageSettings,
  DigestEntry,
  DigestPathNode,
  FavoriteTree,
  MailGroup,
  MailItem,
  MailUsage,
  Notification,
  Poll,
  Post,
  SocialUser,
  TrustInfo,
  Thread,
  ThreadRanking,
  ThreadSummary,
  UserRanking,
  UserProfile,
  AccountRegistration,
  AccountRegistrationSettings,
  PasswordRecoveryRequest,
  UserPrivateProfile,
  UserPersonalFile,
  UserLoginACLBundle,
  UserLoginACLRule,
  UserSignature,
  UserSignatureBundle,
  UserSignatureRecount,
  ModerationReview,
  ContentFilter,
  PollMap,
} from './types'

const BASE = '/api/v1'

function authHeaders(token: string | null): Record<string, string> {
  return token ? { Authorization: `Bearer ${token}` } : {}
}

async function json<T>(res: Response): Promise<ApiResponse<T>> {
  if (res.status === 204) return { data: undefined }
  const body = await res.json() as Record<string, unknown>
  if (!res.ok) return { error: body as unknown as ApiResponse<T>['error'] }
  return { data: body as T }
}

export async function register(name: string, password: string): Promise<ApiResponse<AuthResponse>> {
  const res = await fetch(`${BASE}/auth/register`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, password }),
  })
  return json<AuthResponse>(res)
}

export async function login(name: string, password: string): Promise<ApiResponse<AuthResponse>> {
  const res = await fetch(`${BASE}/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, password }),
  })
  return json<AuthResponse>(res)
}

export async function requestPasswordRecovery(
  payload: { name: string; submittedName?: string; email?: string; note?: string },
): Promise<ApiResponse<unknown>> {
  const res = await fetch(`${BASE}/auth/password-recovery`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
  return json<unknown>(res)
}

export async function listBoards(token: string): Promise<ApiResponse<Board[]>> {
  const res = await fetch(`${BASE}/boards`, { headers: authHeaders(token) })
  const r = await json<{ boards: Board[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.boards ?? [] }
}

export async function getCommunityStats(token: string): Promise<ApiResponse<CommunityStats>> {
  const res = await fetch(`${BASE}/stats/community`, { headers: authHeaders(token) })
  return json<CommunityStats>(res)
}

export async function publishStatsSnapshot(token: string, date?: string): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/stats/community/snapshot`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(date ? { date } : {}),
  })
  return json<AckResult>(res)
}

export async function publishSystemNotice(
  token: string,
  payload: { board?: string; title: string; body: string; source?: string },
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/admin/notices`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  return json<AckResult>(res)
}

export async function listBoardRankings(token: string, limit = 20): Promise<ApiResponse<BoardRanking[]>> {
  const params = new URLSearchParams({ limit: String(limit) })
  const res = await fetch(`${BASE}/rankings/boards?${params}`, { headers: authHeaders(token) })
  const r = await json<{ boards: BoardRanking[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.boards ?? [] }
}

export async function listThreadRankings(token: string, limit = 20, board = ''): Promise<ApiResponse<ThreadRanking[]>> {
  const params = new URLSearchParams({ limit: String(limit) })
  if (board) params.set('board', board)
  const res = await fetch(`${BASE}/rankings/threads?${params}`, { headers: authHeaders(token) })
  const r = await json<{ threads: ThreadRanking[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.threads ?? [] }
}

export async function listUserRankings(token: string, limit = 20): Promise<ApiResponse<UserRanking[]>> {
  const params = new URLSearchParams({ limit: String(limit) })
  const res = await fetch(`${BASE}/rankings/users?${params}`, { headers: authHeaders(token) })
  const r = await json<{ users: UserRanking[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.users ?? [] }
}

export async function listBlessingRankings(token: string, limit = 20): Promise<ApiResponse<BlessingRanking[]>> {
  const params = new URLSearchParams({ limit: String(limit) })
  const res = await fetch(`${BASE}/rankings/blessings?${params}`, { headers: authHeaders(token) })
  const r = await json<{ blessings: BlessingRanking[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.blessings ?? [] }
}

export async function blessUser(token: string, name: string, message = ''): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/users/${encodeURIComponent(name)}/bless`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify({ message }),
  })
  return json<AckResult>(res)
}

export async function listArchiveRankings(token: string, limit = 20, kind = 'archive'): Promise<ApiResponse<ArchiveRanking[]>> {
  const params = new URLSearchParams({ limit: String(limit) })
  if (kind) params.set('kind', kind)
  const res = await fetch(`${BASE}/rankings/archive?${params}`, { headers: authHeaders(token) })
  const r = await json<{ archives: ArchiveRanking[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.archives ?? [] }
}

export async function getBoardInfo(token: string, board: string): Promise<ApiResponse<BoardInfo>> {
  const res = await fetch(`${BASE}/boards/${board}`, { headers: authHeaders(token) })
  return json<BoardInfo>(res)
}

export async function listDigestEntries(
  token: string,
  board: string,
  kind = '',
  path = '',
): Promise<ApiResponse<DigestEntry[]>> {
  const params = new URLSearchParams()
  if (kind) params.set('kind', kind)
  if (path) params.set('path', path)
  const suffix = params.toString() ? `?${params}` : ''
  const res = await fetch(`${BASE}/boards/${board}/digest${suffix}`, { headers: authHeaders(token) })
  const r = await json<{ entries: DigestEntry[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.entries ?? [] }
}

export async function listDigestPathTree(
  token: string,
  board: string,
  kind = '',
): Promise<ApiResponse<DigestPathNode[]>> {
  const params = new URLSearchParams()
  if (kind) params.set('kind', kind)
  const suffix = params.toString() ? `?${params}` : ''
  const res = await fetch(`${BASE}/boards/${board}/digest/tree${suffix}`, { headers: authHeaders(token) })
  const r = await json<{ nodes: DigestPathNode[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.nodes ?? [] }
}

export async function listSiteDigestEntries(
  token: string,
  kind = '',
  path = '',
): Promise<ApiResponse<DigestEntry[]>> {
  const params = new URLSearchParams()
  if (kind) params.set('kind', kind)
  if (path) params.set('path', path)
  const suffix = params.toString() ? `?${params}` : ''
  const res = await fetch(`${BASE}/digest${suffix}`, { headers: authHeaders(token) })
  const r = await json<{ entries: DigestEntry[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.entries ?? [] }
}

export async function listAnnouncements(
  token: string,
  path = '',
): Promise<ApiResponse<DigestEntry[]>> {
  const params = new URLSearchParams()
  if (path) params.set('path', path)
  const suffix = params.toString() ? `?${params}` : ''
  const res = await fetch(`${BASE}/announcements${suffix}`, { headers: authHeaders(token) })
  const r = await json<{ entries: DigestEntry[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.entries ?? [] }
}

export async function searchDigestEntries(
  token: string,
  q: string,
  options: { board?: string; kind?: string; path?: string; limit?: number; offset?: number } = {},
): Promise<ApiResponse<DigestEntry[]>> {
  const params = new URLSearchParams({ q })
  if (options.board) params.set('board', options.board)
  if (options.kind) params.set('kind', options.kind)
  if (options.path) params.set('path', options.path)
  if (options.limit) params.set('limit', String(options.limit))
  if (options.offset) params.set('offset', String(options.offset))
  const res = await fetch(`${BASE}/digest/search?${params}`, { headers: authHeaders(token) })
  const r = await json<{ entries: DigestEntry[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.entries ?? [] }
}

export async function listFavoriteBoards(token: string): Promise<ApiResponse<Board[]>> {
  const res = await fetch(`${BASE}/boards/favorites`, { headers: authHeaders(token) })
  const r = await json<{ boards: Board[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.boards ?? [] }
}

export async function listFavoriteTree(token: string): Promise<ApiResponse<FavoriteTree>> {
  const res = await fetch(`${BASE}/boards/favorites/tree`, { headers: authHeaders(token) })
  return json<FavoriteTree>(res)
}

export async function exportFavoriteTree(token: string): Promise<ApiResponse<FavoriteTree>> {
  const res = await fetch(`${BASE}/boards/favorites/export`, { headers: authHeaders(token) })
  return json<FavoriteTree>(res)
}

export async function importFavoriteTree(
  token: string,
  tree: FavoriteTree & { replace?: boolean },
): Promise<ApiResponse<FavoriteTree>> {
  const res = await fetch(`${BASE}/boards/favorites/import`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(tree),
  })
  return json<FavoriteTree>(res)
}

export async function listBoardSummaries(token: string): Promise<ApiResponse<BoardSummary[]>> {
  const res = await fetch(`${BASE}/boards/summary`, { headers: authHeaders(token) })
  const r = await json<{ boards: BoardSummary[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.boards ?? [] }
}

export async function listUnreadBoards(token: string): Promise<ApiResponse<BoardSummary[]>> {
  const res = await fetch(`${BASE}/boards/unread`, { headers: authHeaders(token) })
  const r = await json<{ boards: BoardSummary[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.boards ?? [] }
}

export async function setBoardFavorite(
  token: string,
  board: string,
  favorite: boolean,
  folderId = '',
  position?: number,
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/boards/${board}/favorite`, {
    method: favorite ? 'PUT' : 'DELETE',
    headers: favorite ? { 'Content-Type': 'application/json', ...authHeaders(token) } : authHeaders(token),
    body: favorite ? JSON.stringify({ favorite, folderId, position }) : undefined,
  })
  return json<AckResult>(res)
}

export async function createFavoriteFolder(
  token: string,
  name: string,
  parentId = '',
  position?: number,
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/boards/favorites/folders`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify({ name, parentId, position }),
  })
  return json<AckResult>(res)
}

export async function updateFavoriteFolder(
  token: string,
  folder: string,
  patch: { name?: string; parentId?: string; position?: number },
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/boards/favorites/folders/${folder}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(patch),
  })
  return json<AckResult>(res)
}

export async function deleteFavoriteFolder(token: string, folder: string): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/boards/favorites/folders/${folder}`, {
    method: 'DELETE',
    headers: authHeaders(token),
  })
  return json<AckResult>(res)
}

export async function moveBoardFavorite(
  token: string,
  board: string,
  folderId = '',
  position?: number,
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/boards/${board}/favorite`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify({ folderId, position }),
  })
  return json<AckResult>(res)
}

export async function setBoardSettings(
  token: string,
  board: string,
  patch: Partial<Omit<BoardSettings, 'boardId' | 'updatedAt'>>,
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/boards/${board}/settings`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(patch),
  })
  return json<AckResult>(res)
}

export async function setBoardMemberRequirements(
  token: string,
  board: string,
  patch: Partial<Omit<BoardMemberRequirements, 'boardId' | 'updatedAt'>>,
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/boards/${board}/member-requirements`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(patch),
  })
  return json<AckResult>(res)
}

export async function setBoardModerator(
  token: string,
  board: string,
  user: string,
  moderator: boolean,
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/boards/${board}/moderators/${encodeURIComponent(user)}`, {
    method: moderator ? 'PUT' : 'DELETE',
    headers: authHeaders(token),
  })
  return json<AckResult>(res)
}

export async function setBoardMember(
  token: string,
  board: string,
  user: string,
  member: boolean,
  title = '',
  permissions: {
    position?: number
    canManageMembers?: boolean
    canCurate?: boolean
    canModeratePosts?: boolean
    canModerateThreads?: boolean
    canAnnounce?: boolean
    canSetBoardSettings?: boolean
  } = {},
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/boards/${board}/members/${encodeURIComponent(user)}`, {
    method: member ? 'PUT' : 'DELETE',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: member ? JSON.stringify({ title, ...permissions }) : undefined,
  })
  return json<AckResult>(res)
}

export async function applyBoardMembership(
  token: string,
  board: string,
  note = '',
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/boards/${board}/member-applications`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify({ note }),
  })
  return json<AckResult>(res)
}

export async function listBoardMemberApplications(
  token: string,
  board: string,
  status = '',
): Promise<ApiResponse<BoardMemberApplication[]>> {
  const params = new URLSearchParams()
  if (status) params.set('status', status)
  const suffix = params.toString() ? `?${params}` : ''
  const res = await fetch(`${BASE}/boards/${board}/member-applications${suffix}`, { headers: authHeaders(token) })
  const r = await json<{ applications: BoardMemberApplication[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.applications ?? [] }
}

export async function reviewBoardMembership(
  token: string,
  application: string,
  payload: { status: 'approved' | 'rejected' | 'blacklisted'; title?: string; note?: string },
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/board-member-applications/${application}/review`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  return json<AckResult>(res)
}

export async function leaveBoardMembership(token: string, board: string): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/boards/${board}/members/leave`, {
    method: 'POST',
    headers: authHeaders(token),
  })
  return json<AckResult>(res)
}

export async function curatePost(
  token: string,
  post: string,
  payload: { kind?: string; title?: string; path?: string; note?: string },
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/posts/${post}/digest`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  return json<AckResult>(res)
}

export async function uploadPostAttachment(token: string, post: string, file: File): Promise<ApiResponse<AckResult>> {
  const body = new FormData()
  body.append('file', file)
  const res = await fetch(`${BASE}/posts/${post}/attachments`, {
    method: 'POST',
    headers: authHeaders(token),
    body,
  })
  return json<AckResult>(res)
}

export async function downloadAttachment(token: string, attachment: string, filename: string): Promise<ApiResponse<void>> {
  const res = await fetch(`${BASE}/attachments/${attachment}`, { headers: authHeaders(token) })
  if (!res.ok) {
    const body = await res.json() as ApiResponse<void>['error']
    return { error: body }
  }
  const blob = await res.blob()
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
  return { data: undefined }
}

export async function uploadMailAttachment(token: string, mail: string, file: File): Promise<ApiResponse<AckResult>> {
  const body = new FormData()
  body.append('file', file)
  const res = await fetch(`${BASE}/mail/${mail}/attachments`, {
    method: 'POST',
    headers: authHeaders(token),
    body,
  })
  return json<AckResult>(res)
}

export async function downloadMailAttachment(token: string, attachment: string, filename: string): Promise<ApiResponse<void>> {
  const res = await fetch(`${BASE}/mail/attachments/${attachment}`, { headers: authHeaders(token) })
  if (!res.ok) {
    const body = await res.json() as ApiResponse<void>['error']
    return { error: body }
  }
  const blob = await res.blob()
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
  return { data: undefined }
}

export async function curateThread(
  token: string,
  thread: string,
  payload: { kind?: string; title?: string; path?: string; note?: string },
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/threads/${thread}/digest`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  return json<AckResult>(res)
}

export async function removeDigestEntry(token: string, entry: string): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/digest/${entry}`, {
    method: 'DELETE',
    headers: authHeaders(token),
  })
  return json<AckResult>(res)
}

export async function updateDigestEntry(
  token: string,
  entry: string,
  payload: { title?: string; path?: string; note?: string },
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/digest/${entry}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  return json<AckResult>(res)
}

export async function setDigestEntryBody(
  token: string,
  entry: string,
  body: string,
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/digest/${entry}/body`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify({ body }),
  })
  return json<AckResult>(res)
}

export async function resetDigestEntryBody(token: string, entry: string): Promise<ApiResponse<AckResult>> {
	const res = await fetch(`${BASE}/digest/${entry}/body`, {
		method: 'DELETE',
		headers: authHeaders(token),
	})
	return json<AckResult>(res)
}

export async function createDigestDirectory(
  token: string,
  board: string,
  payload: { kind?: string; path: string },
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/boards/${board}/digest/directories`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  return json<AckResult>(res)
}

export async function moveDigestPath(
  token: string,
  board: string,
  payload: { kind?: string; fromPath: string; toPath: string },
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/boards/${board}/digest/paths/move`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  return json<AckResult>(res)
}

export async function copyDigestPath(
  token: string,
  board: string,
  payload: { kind?: string; fromPath: string; toPath: string },
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/boards/${board}/digest/paths/copy`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  return json<AckResult>(res)
}

export async function deleteDigestPath(
  token: string,
  board: string,
  kind: string,
  path: string,
): Promise<ApiResponse<AckResult>> {
  const params = new URLSearchParams()
  if (kind) params.set('kind', kind)
  params.set('path', path)
  const res = await fetch(`${BASE}/boards/${board}/digest/paths?${params.toString()}`, {
    method: 'DELETE',
    headers: authHeaders(token),
  })
  return json<AckResult>(res)
}

export async function downloadDigestEntry(
  token: string,
  entry: string,
  filename = 'archive.txt',
): Promise<ApiResponse<void>> {
  const res = await fetch(`${BASE}/digest/${entry}/download`, { headers: authHeaders(token) })
  if (!res.ok) {
    const body = await res.json() as ApiResponse<void>['error']
    return { error: body }
  }
  const blob = await res.blob()
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
  return { data: undefined }
}

export async function sendDigestEntryMail(
  token: string,
  entry: string,
  payload: {
    to: string[]
    toGroups?: string[]
    toFriends?: boolean
    toAll?: boolean
    subject?: string
    note?: string
    saveSent?: boolean
  },
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/digest/${entry}/mail`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  return json<AckResult>(res)
}

export async function listMail(
  token: string,
  mailbox = 'inbox',
  unreadOnly = false,
): Promise<ApiResponse<{ mail: MailItem[]; unreadCount: number }>> {
  const params = new URLSearchParams({ mailbox })
  if (unreadOnly) params.set('unread', '1')
  const res = await fetch(`${BASE}/mail?${params}`, { headers: authHeaders(token) })
  return json<{ mail: MailItem[]; unreadCount: number }>(res)
}

export async function getMail(token: string, mail: string): Promise<ApiResponse<MailItem>> {
  const res = await fetch(`${BASE}/mail/${mail}`, { headers: authHeaders(token) })
  return json<MailItem>(res)
}

export async function sendMail(
  token: string,
  payload: {
    to: string[]
    toGroups?: string[]
    toFriends?: boolean
    toAll?: boolean
    subject: string
    body: string
    replyTo?: string
    saveSent?: boolean
    attachments?: AttachmentPayload[]
  },
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/mail`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  return json<AckResult>(res)
}

export async function listMailGroups(token: string): Promise<ApiResponse<{ groups: MailGroup[] }>> {
  const res = await fetch(`${BASE}/mail/groups`, { headers: authHeaders(token) })
  return json<{ groups: MailGroup[] }>(res)
}

export async function getMailUsage(token: string): Promise<ApiResponse<MailUsage>> {
  const res = await fetch(`${BASE}/mail/usage`, { headers: authHeaders(token) })
  return json<MailUsage>(res)
}

export async function setMailGroup(
  token: string,
  payload: { group?: string; name: string; members: string[] },
): Promise<ApiResponse<AckResult>> {
  const path = payload.group ? `/mail/groups/${encodeURIComponent(payload.group)}` : '/mail/groups'
  const res = await fetch(`${BASE}${path}`, {
    method: payload.group ? 'PATCH' : 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  return json<AckResult>(res)
}

export async function deleteMailGroup(token: string, group: string): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/mail/groups/${encodeURIComponent(group)}`, {
    method: 'DELETE',
    headers: authHeaders(token),
  })
  return json<AckResult>(res)
}

export async function updateMail(
  token: string,
  mail: string,
  patch: { mailbox?: string; read?: boolean; kept?: boolean },
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/mail/${mail}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(patch),
  })
  return json<AckResult>(res)
}

export async function deleteMail(token: string, mail: string): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/mail/${mail}`, {
    method: 'DELETE',
    headers: authHeaders(token),
  })
  return json<AckResult>(res)
}

export async function listDirectConversations(
  token: string,
): Promise<ApiResponse<{ conversations: DirectMessageConversation[]; unreadCount: number }>> {
  const res = await fetch(`${BASE}/messages`, { headers: authHeaders(token) })
  return json<{ conversations: DirectMessageConversation[]; unreadCount: number }>(res)
}

export async function listDirectMessages(
  token: string,
  username: string,
): Promise<ApiResponse<{ messages: DirectMessage[] }>> {
  const res = await fetch(`${BASE}/messages/${encodeURIComponent(username)}`, { headers: authHeaders(token) })
  return json<{ messages: DirectMessage[] }>(res)
}

export async function sendDirectMessage(
  token: string,
  payload: { to: string; body: string },
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/messages`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  return json<AckResult>(res)
}

export async function getDirectMessageSettings(token: string): Promise<ApiResponse<DirectMessageSettings>> {
  const res = await fetch(`${BASE}/messages/settings`, { headers: authHeaders(token) })
  return json<DirectMessageSettings>(res)
}

export async function setDirectMessageSettings(
  token: string,
  payload: { policy: DirectMessageSettings['policy'] },
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/messages/settings`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  return json<AckResult>(res)
}

export async function markDirectMessageRead(token: string, message: string): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/messages/${message}/read`, {
    method: 'POST',
    headers: authHeaders(token),
  })
  return json<AckResult>(res)
}

export async function deleteDirectMessage(token: string, message: string): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/messages/${message}`, {
    method: 'DELETE',
    headers: authHeaders(token),
  })
  return json<AckResult>(res)
}

export async function listSocialUsers(
  token: string,
  list: 'friends' | 'fans' | 'ignores' | 'online-friends',
): Promise<ApiResponse<SocialUser[]>> {
  const res = await fetch(`${BASE}/social/${list}`, { headers: authHeaders(token) })
  const r = await json<{ users: SocialUser[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.users ?? [] }
}

export async function listOnlineUsers(token: string): Promise<ApiResponse<SocialUser[]>> {
  const res = await fetch(`${BASE}/presence/online`, { headers: authHeaders(token) })
  const r = await json<{ users: SocialUser[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.users ?? [] }
}

export async function listBoardOnlineUsers(token: string, board: string): Promise<ApiResponse<SocialUser[]>> {
  const res = await fetch(`${BASE}/boards/${board}/online`, { headers: authHeaders(token) })
  const r = await json<{ users: SocialUser[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.users ?? [] }
}

export async function setPresence(
  token: string,
  payload: {
    status: string
    sessionId?: string
    mode?: string
    board?: string
    thread?: string
    location?: string
    fromHost?: string
  },
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/presence`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  return json<AckResult>(res)
}

export async function setUserRelationship(
  token: string,
  username: string,
  kind: 'friend' | 'ignore',
  active: boolean,
  note = '',
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/users/${encodeURIComponent(username)}/${kind}`, {
    method: active ? 'PUT' : 'DELETE',
    headers: active ? { 'Content-Type': 'application/json', ...authHeaders(token) } : authHeaders(token),
    body: active ? JSON.stringify({ note }) : undefined,
  })
  return json<AckResult>(res)
}

export async function setLoginWatch(
  token: string,
  username: string,
  active: boolean,
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/users/${encodeURIComponent(username)}/login-watch`, {
    method: active ? 'PUT' : 'DELETE',
    headers: authHeaders(token),
  })
  return json<AckResult>(res)
}

export async function markBoardRead(token: string, board: string): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/boards/${board}/read`, {
    method: 'POST',
    headers: authHeaders(token),
  })
  return json<AckResult>(res)
}

export async function restoreBoardRead(token: string, board: string): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/boards/${board}/read/restore`, {
    method: 'POST',
    headers: authHeaders(token),
  })
  return json<AckResult>(res)
}

export async function markFavoriteFolderRead(token: string, folder = ''): Promise<ApiResponse<AckResult>> {
  const path = folder ? `/boards/favorites/folders/${folder}/read` : '/boards/favorites/read'
  const res = await fetch(`${BASE}${path}`, {
    method: 'POST',
    headers: authHeaders(token),
  })
  return json<AckResult>(res)
}

export async function restoreFavoriteFolderRead(token: string, folder = ''): Promise<ApiResponse<AckResult>> {
  const path = folder ? `/boards/favorites/folders/${folder}/read/restore` : '/boards/favorites/read/restore'
  const res = await fetch(`${BASE}${path}`, {
    method: 'POST',
    headers: authHeaders(token),
  })
  return json<AckResult>(res)
}

export async function markThreadRead(token: string, thread: string): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/threads/${thread}/read`, {
    method: 'POST',
    headers: authHeaders(token),
  })
  return json<AckResult>(res)
}

export async function restoreThreadRead(token: string, thread: string): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/threads/${thread}/read/restore`, {
    method: 'POST',
    headers: authHeaders(token),
  })
  return json<AckResult>(res)
}

export async function markPostRead(token: string, post: string): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/posts/${post}/read`, {
    method: 'POST',
    headers: authHeaders(token),
  })
  return json<AckResult>(res)
}

export async function listThreads(
  token: string,
  board: string,
  limit = 30,
  offset = 0,
  unreadOnly = false,
): Promise<ApiResponse<ThreadSummary[]>> {
  const params = new URLSearchParams({ limit: String(limit), offset: String(offset) })
  if (unreadOnly) params.set('unread', '1')
  const res = await fetch(`${BASE}/boards/${board}/threads?${params}`, {
    headers: authHeaders(token),
  })
  const r = await json<{ threads: ThreadSummary[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.threads ?? [] }
}

export async function listUnreadThreads(
  token: string,
  limit = 50,
  offset = 0,
  favoritesOnly = false,
  folder = '',
): Promise<ApiResponse<ThreadSummary[]>> {
  const params = new URLSearchParams({ limit: String(limit), offset: String(offset) })
  if (favoritesOnly) params.set('favorites', '1')
  if (folder) params.set('folder', folder)
  const res = await fetch(`${BASE}/threads/unread?${params}`, {
    headers: authHeaders(token),
  })
  const r = await json<{ threads: ThreadSummary[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.threads ?? [] }
}

export async function getThread(token: string, id: string): Promise<ApiResponse<{ thread: Thread; posts: Post[] }>> {
  const res = await fetch(`${BASE}/threads/${id}`, { headers: authHeaders(token) })
  return json<{ thread: Thread; posts: Post[] }>(res)
}

export async function listPosts(token: string, thread: string, limit = 50, offset = 0): Promise<ApiResponse<Post[]>> {
  const res = await fetch(`${BASE}/threads/${thread}/posts?limit=${limit}&offset=${offset}`, {
    headers: authHeaders(token),
  })
  const r = await json<{ posts: Post[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.posts ?? [] }
}

export async function listReplyTree(token: string, post: string, limit = 50, offset = 0): Promise<ApiResponse<Post[]>> {
  const res = await fetch(`${BASE}/posts/${post}/reply-tree?limit=${limit}&offset=${offset}`, {
    headers: authHeaders(token),
  })
  const r = await json<{ posts: Post[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.posts ?? [] }
}

export async function listThreadPolls(
  token: string,
  thread: string,
  limit = 50,
  offset = 0,
): Promise<ApiResponse<PollMap>> {
  const res = await fetch(`${BASE}/threads/${thread}/polls?limit=${limit}&offset=${offset}`, {
    headers: authHeaders(token),
  })
  const r = await json<{ polls: PollMap }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.polls ?? {} }
}

export async function listCategories(token: string): Promise<ApiResponse<Category[]>> {
  const res = await fetch(`${BASE}/categories`, { headers: authHeaders(token) })
  const r = await json<{ categories: Category[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.categories ?? [] }
}

export async function updateCategory(
  token: string,
  category: string,
  patch: { name?: string; description?: string; parentId?: string; position?: number; visibility?: string },
): Promise<ApiResponse<Category>> {
  const res = await fetch(`${BASE}/categories/${encodeURIComponent(category)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(patch),
  })
  const r = await json<{ category: Category }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.category }
}

export async function search(token: string, q: string, board?: string): Promise<ApiResponse<Post[]>> {
  const params = new URLSearchParams({ q, limit: '30' })
  if (board) params.set('board', board)
  const res = await fetch(`${BASE}/search?${params}`, { headers: authHeaders(token) })
  const r = await json<{ posts: Post[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.posts ?? [] }
}

// ── M8: Notifications ─────────────────────────────────────────────────────

export async function listNotifications(token: string, unreadOnly = false): Promise<ApiResponse<{ notifications: Notification[]; unreadCount: number }>> {
  const params = new URLSearchParams({ limit: '50' })
  if (unreadOnly) params.set('unread', '1')
  const res = await fetch(`${BASE}/notifications?${params}`, { headers: authHeaders(token) })
  return json<{ notifications: Notification[]; unreadCount: number }>(res)
}

export async function markNotificationRead(token: string, id: string): Promise<ApiResponse<unknown>> {
  const res = await fetch(`${BASE}/notifications/${id}/read`, {
    method: 'POST',
    headers: authHeaders(token),
  })
  return json<unknown>(res)
}

export async function markAllNotificationsRead(token: string): Promise<ApiResponse<unknown>> {
  const res = await fetch(`${BASE}/notifications/read-all`, {
    method: 'POST',
    headers: authHeaders(token),
  })
  return json<unknown>(res)
}

// ── M10: Reactions ─────────────────────────────────────────────────────────

export async function reactPost(token: string, postId: string, emoji = 'heart'): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/posts/${postId}/react`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify({ emoji }),
  })
  return json<AckResult>(res)
}

export async function unreactPost(token: string, postId: string): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/posts/${postId}/react`, {
    method: 'DELETE',
    headers: authHeaders(token),
  })
  return json<AckResult>(res)
}

// ── M11: Polls ─────────────────────────────────────────────────────────────

export async function getPoll(token: string, pollId: string): Promise<ApiResponse<Poll>> {
  const res = await fetch(`${BASE}/polls/${pollId}`, { headers: authHeaders(token) })
  return json<Poll>(res)
}

export async function getPollByPost(token: string, postId: string): Promise<ApiResponse<Poll>> {
  const res = await fetch(`${BASE}/posts/${postId}/poll`, { headers: authHeaders(token) })
  return json<Poll>(res)
}

export async function votePoll(token: string, pollId: string, option: string): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/polls/${pollId}/vote`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify({ option }),
  })
  return json<AckResult>(res)
}

export async function publishPollResult(token: string, pollId: string): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/polls/${pollId}/publish-result`, {
    method: 'POST',
    headers: authHeaders(token),
  })
  return json<AckResult>(res)
}

// ── M9: Trust ──────────────────────────────────────────────────────────────

export async function getTrust(token: string, username: string): Promise<ApiResponse<TrustInfo>> {
  const res = await fetch(`${BASE}/users/${username}/trust`, { headers: authHeaders(token) })
  return json<TrustInfo>(res)
}

export async function getUserProfile(token: string | null, username: string): Promise<ApiResponse<UserProfile>> {
  const res = await fetch(`${BASE}/users/${username}`, { headers: authHeaders(token) })
  return json<UserProfile>(res)
}

export async function getAccountRegistrationSettings(token: string): Promise<ApiResponse<AccountRegistrationSettings>> {
  const res = await fetch(`${BASE}/admin/registration-settings`, { headers: authHeaders(token) })
  return json<AccountRegistrationSettings>(res)
}

export async function setAccountRegistrationSettings(
  token: string,
  payload: { requireApproval: boolean },
): Promise<ApiResponse<AccountRegistrationSettings>> {
  const res = await fetch(`${BASE}/admin/registration-settings`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  return json<AccountRegistrationSettings>(res)
}

export async function listAccountRegistrations(
  token: string,
  status = 'pending',
  limit = 50,
  offset = 0,
): Promise<ApiResponse<AccountRegistration[]>> {
  const params = new URLSearchParams({ status, limit: String(limit), offset: String(offset) })
  const res = await fetch(`${BASE}/admin/registrations?${params}`, { headers: authHeaders(token) })
  const r = await json<{ registrations: AccountRegistration[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.registrations ?? [] }
}

export async function reviewAccountRegistration(
  token: string,
  username: string,
  payload: { decision: 'approved' | 'rejected'; reason?: string },
): Promise<ApiResponse<AccountRegistration>> {
  const res = await fetch(`${BASE}/admin/registrations/${encodeURIComponent(username)}/review`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  const r = await json<{ registration: AccountRegistration }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.registration }
}

export async function listPasswordRecoveryRequests(
  token: string,
  status = 'pending',
  limit = 50,
  offset = 0,
): Promise<ApiResponse<PasswordRecoveryRequest[]>> {
  const params = new URLSearchParams({ status, limit: String(limit), offset: String(offset) })
  const res = await fetch(`${BASE}/admin/password-recovery?${params}`, { headers: authHeaders(token) })
  const r = await json<{ requests: PasswordRecoveryRequest[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.requests ?? [] }
}

export async function reviewPasswordRecoveryRequest(
  token: string,
  requestId: string,
  payload: { decision: 'reset' | 'rejected'; newPassword?: string; note?: string },
): Promise<ApiResponse<PasswordRecoveryRequest>> {
  const res = await fetch(`${BASE}/admin/password-recovery/${encodeURIComponent(requestId)}/review`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  const r = await json<{ request: PasswordRecoveryRequest }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.request }
}

export async function transferUserId(
  token: string,
  username: string,
  newName: string,
): Promise<ApiResponse<AuthResponse['user']>> {
  const res = await fetch(`${BASE}/admin/users/${encodeURIComponent(username)}/transfer-id`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify({ newName }),
  })
  const r = await json<{ user: AuthResponse['user'] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.user }
}

export async function deleteUser(
  token: string,
  username: string,
  reason = '',
): Promise<ApiResponse<unknown>> {
  const res = await fetch(`${BASE}/admin/users/${encodeURIComponent(username)}`, {
    method: 'DELETE',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify({ reason }),
  })
  return json<unknown>(res)
}

export async function listUserPosts(
  token: string | null,
  username: string,
  limit = 20,
  offset = 0,
): Promise<ApiResponse<Post[]>> {
  const params = new URLSearchParams({ limit: String(limit), offset: String(offset) })
  const res = await fetch(`${BASE}/users/${username}/posts?${params}`, { headers: authHeaders(token) })
  const r = await json<{ posts: Post[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.posts ?? [] }
}

export async function listAuthorPosts(
  token: string,
  username: string,
  limit = 30,
  offset = 0,
): Promise<ApiResponse<Post[]>> {
  const params = new URLSearchParams({ limit: String(limit), offset: String(offset) })
  const res = await fetch(`${BASE}/authors/${encodeURIComponent(username)}/posts?${params}`, { headers: authHeaders(token) })
  const r = await json<{ posts: Post[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.posts ?? [] }
}

export async function updateMyProfile(
  token: string,
  payload: { displayName?: string; bio?: string; avatar?: string; signature?: string; plan?: string; homepage?: string },
): Promise<ApiResponse<unknown>> {
  const res = await fetch(`${BASE}/users/me`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  return json<unknown>(res)
}

export async function getMyPrivateProfile(token: string): Promise<ApiResponse<UserPrivateProfile>> {
  const res = await fetch(`${BASE}/users/me/private-profile`, { headers: authHeaders(token) })
  return json<UserPrivateProfile>(res)
}

export async function updateMyPrivateProfile(
  token: string,
  payload: Partial<Omit<UserPrivateProfile, 'userId' | 'updatedAt'>>,
): Promise<ApiResponse<unknown>> {
  const res = await fetch(`${BASE}/users/me/private-profile`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  return json<unknown>(res)
}

export async function listUserPersonalFiles(token: string | null, username: string): Promise<ApiResponse<UserPersonalFile[]>> {
  const res = await fetch(`${BASE}/users/${encodeURIComponent(username)}/files`, { headers: authHeaders(token) })
  const r = await json<{ files: UserPersonalFile[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.files ?? [] }
}

export async function listMyPersonalFiles(token: string): Promise<ApiResponse<UserPersonalFile[]>> {
  const res = await fetch(`${BASE}/users/me/files`, { headers: authHeaders(token) })
  const r = await json<{ files: UserPersonalFile[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.files ?? [] }
}

export async function saveMyPersonalFile(
  token: string,
  name: string,
  payload: { body: string; public?: boolean },
): Promise<ApiResponse<UserPersonalFile>> {
  const res = await fetch(`${BASE}/users/me/files/${encodeURIComponent(name)}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  const r = await json<{ file: UserPersonalFile }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.file }
}

export async function deleteMyPersonalFile(token: string, name: string): Promise<ApiResponse<unknown>> {
  const res = await fetch(`${BASE}/users/me/files/${encodeURIComponent(name)}`, {
    method: 'DELETE',
    headers: authHeaders(token),
  })
  return json<unknown>(res)
}

export async function listMySignatures(token: string): Promise<ApiResponse<UserSignatureBundle>> {
  const res = await fetch(`${BASE}/users/me/signatures`, { headers: authHeaders(token) })
  return json<UserSignatureBundle>(res)
}

export async function createMySignature(
  token: string,
  payload: { label?: string; body: string; position?: number; active?: boolean },
): Promise<ApiResponse<UserSignature>> {
  const res = await fetch(`${BASE}/users/me/signatures`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  const r = await json<{ signature: UserSignature }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.signature }
}

export async function updateMySignature(
  token: string,
  signatureId: string,
  payload: { label?: string; body: string; position?: number; active?: boolean },
): Promise<ApiResponse<UserSignature>> {
  const res = await fetch(`${BASE}/users/me/signatures/${encodeURIComponent(signatureId)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  const r = await json<{ signature: UserSignature }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.signature }
}

export async function deleteMySignature(token: string, signatureId: string): Promise<ApiResponse<unknown>> {
  const res = await fetch(`${BASE}/users/me/signatures/${encodeURIComponent(signatureId)}`, {
    method: 'DELETE',
    headers: authHeaders(token),
  })
  return json<unknown>(res)
}

export async function recountMySignatures(token: string): Promise<ApiResponse<UserSignatureRecount>> {
  const res = await fetch(`${BASE}/users/me/signatures/recount`, {
    method: 'POST',
    headers: authHeaders(token),
  })
  const r = await json<{ recount: UserSignatureRecount }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.recount }
}

export async function setMySignatureSettings(
  token: string,
  payload: { selectedSignatureId: string; randomEnabled: boolean },
): Promise<ApiResponse<unknown>> {
  const res = await fetch(`${BASE}/users/me/signatures/settings`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  return json<unknown>(res)
}

export async function listMyLoginACL(token: string): Promise<ApiResponse<UserLoginACLBundle>> {
  const res = await fetch(`${BASE}/users/me/login-acl`, { headers: authHeaders(token) })
  return json<UserLoginACLBundle>(res)
}

export async function createMyLoginACLRule(
  token: string,
  payload: { pattern: string; note?: string; position?: number; active?: boolean },
): Promise<ApiResponse<UserLoginACLRule>> {
  const res = await fetch(`${BASE}/users/me/login-acl/rules`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  const r = await json<{ rule: UserLoginACLRule }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.rule }
}

export async function updateMyLoginACLRule(
  token: string,
  ruleId: string,
  payload: { pattern: string; note?: string; position?: number; active?: boolean },
): Promise<ApiResponse<UserLoginACLRule>> {
  const res = await fetch(`${BASE}/users/me/login-acl/rules/${encodeURIComponent(ruleId)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  const r = await json<{ rule: UserLoginACLRule }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.rule }
}

export async function deleteMyLoginACLRule(token: string, ruleId: string): Promise<ApiResponse<unknown>> {
  const res = await fetch(`${BASE}/users/me/login-acl/rules/${encodeURIComponent(ruleId)}`, {
    method: 'DELETE',
    headers: authHeaders(token),
  })
  return json<unknown>(res)
}

export async function setMyLoginACLSettings(token: string, payload: { enabled: boolean }): Promise<ApiResponse<unknown>> {
  const res = await fetch(`${BASE}/users/me/login-acl/settings`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  return json<unknown>(res)
}

export async function changeMyPassword(
  token: string,
  payload: { currentPassword: string; newPassword: string },
): Promise<ApiResponse<unknown>> {
  const res = await fetch(`${BASE}/users/me/password`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  return json<unknown>(res)
}

export async function deactivateMyAccount(
  token: string,
  payload: { password: string; reason?: string },
): Promise<ApiResponse<unknown>> {
  const res = await fetch(`${BASE}/users/me/deactivate`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  return json<unknown>(res)
}

export async function flagPost(token: string, postId: string, reason = ''): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/posts/${postId}/flag`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify({ reason }),
  })
  return json<AckResult>(res)
}

export async function listReviewables(
  token: string,
  status = 'open',
  limit = 50,
  offset = 0,
): Promise<ApiResponse<ModerationReview[]>> {
  const params = new URLSearchParams({ status, limit: String(limit), offset: String(offset) })
  const res = await fetch(`${BASE}/mod/reviewables?${params}`, { headers: authHeaders(token) })
  const r = await json<{ reviewables: ModerationReview[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.reviewables ?? [] }
}

export async function resolveReview(
  token: string,
  reviewId: string,
  resolution: string,
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/mod/reviewables/${reviewId}/resolve`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify({ resolution }),
  })
  return json<AckResult>(res)
}

export async function listContentFilters(
  token: string,
  options: { scope?: string; includeInactive?: boolean; limit?: number; offset?: number } = {},
): Promise<ApiResponse<ContentFilter[]>> {
  const params = new URLSearchParams({
    limit: String(options.limit ?? 50),
    offset: String(options.offset ?? 0),
  })
  if (options.scope) params.set('scope', options.scope)
  if (options.includeInactive) params.set('includeInactive', '1')
  const res = await fetch(`${BASE}/admin/content-filters?${params}`, { headers: authHeaders(token) })
  const r = await json<{ filters: ContentFilter[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.filters ?? [] }
}

export async function setContentFilter(
  token: string,
  payload: { id?: string; pattern: string; scope?: string; active?: boolean },
): Promise<ApiResponse<AckResult>> {
  const id = payload.id?.trim()
  const res = await fetch(`${BASE}/admin/content-filters${id ? `/${encodeURIComponent(id)}` : ''}`, {
    method: id ? 'PATCH' : 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  return json<AckResult>(res)
}

export async function sanctionUser(
  token: string,
  username: string,
  payload: { kind: 'mute' | 'ban'; scope?: string; durationSec?: number; reason?: string },
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/users/${username}/sanctions`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  return json<AckResult>(res)
}

export async function clearUserSanction(
  token: string,
  username: string,
  payload: { kind?: 'mute' | 'ban'; scope?: string; reason?: string } = {},
): Promise<ApiResponse<AckResult>> {
  const params = new URLSearchParams()
  if (payload.kind) params.set('kind', payload.kind)
  if (payload.scope) params.set('scope', payload.scope)
  if (payload.reason) params.set('reason', payload.reason)
  const qs = params.toString()
  const res = await fetch(`${BASE}/users/${username}/sanctions${qs ? `?${qs}` : ''}`, {
    method: 'DELETE',
    headers: authHeaders(token),
  })
  return json<AckResult>(res)
}

// ── Commands ───────────────────────────────────────────────────────────────

export type CommandName =
  | 'createThread' | 'appendPost' | 'postBoardMail' | 'editPost' | 'redactPost' | 'restorePost'
  | 'lockThread' | 'moveThread' | 'sanctionUser' | 'clearUserSanction' | 'setContentFilter' | 'grantRole' | 'revokeRole' | 'publishStatsSnapshot'
  | 'sendChatLine' | 'setPresence' | 'createBoard' | 'purgePost'
  | 'setBoardSettings' | 'setBoardMemberRequirements' | 'setBoardModerator'
  | 'setBoardMember' | 'applyBoardMembership' | 'reviewBoardMembership' | 'leaveBoardMembership'
  | 'curatePost' | 'curateThread' | 'removeDigestEntry' | 'updateDigestEntry' | 'setDigestEntryBody' | 'createDigestDirectory'
  | 'moveDigestPath' | 'copyDigestPath' | 'deleteDigestPath' | 'sendDigestEntryMail'
  | 'sendMail' | 'setMailGroup' | 'deleteMailGroup' | 'attachMail' | 'updateMail' | 'deleteMail'
  | 'sendDirectMessage' | 'setDirectMessageSettings' | 'markDirectMessageRead' | 'deleteDirectMessage'
  | 'setUserRelationship' | 'setLoginWatch'
  | 'setBoardFavorite' | 'createFavoriteFolder' | 'updateFavoriteFolder'
  | 'deleteFavoriteFolder' | 'moveBoardFavorite' | 'importFavoriteTree'
  | 'markBoardRead' | 'restoreBoardRead' | 'markFavoriteFolderRead' | 'restoreFavoriteFolderRead'
  | 'markThreadRead' | 'restoreThreadRead' | 'markPostRead'
  | 'reactPost' | 'unreactPost' | 'votePoll' | 'publishPollResult' | 'setThreadPref'
  | 'flagPost' | 'resolveReview'

export async function execCommand(
  token: string,
  name: CommandName,
  payload: unknown,
  cid?: string,
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/commands`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify({ command: name, payload, cid }),
  })
  return json<AckResult>(res)
}
