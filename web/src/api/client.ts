import type {
  AckResult,
  ApiResponse,
  AuthResponse,
  Board,
  Category,
  Notification,
  Poll,
  Post,
  TrustInfo,
  Thread,
  UserProfile,
  ModerationReview,
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

export async function listBoards(token: string): Promise<ApiResponse<Board[]>> {
  const res = await fetch(`${BASE}/boards`, { headers: authHeaders(token) })
  const r = await json<{ boards: Board[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.boards ?? [] }
}

export async function listThreads(token: string, board: string, limit = 30, offset = 0): Promise<ApiResponse<Thread[]>> {
  const res = await fetch(`${BASE}/boards/${board}/threads?limit=${limit}&offset=${offset}`, {
    headers: authHeaders(token),
  })
  const r = await json<{ threads: Thread[] }>(res)
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

export async function listCategories(token: string): Promise<ApiResponse<Category[]>> {
  const res = await fetch(`${BASE}/categories`, { headers: authHeaders(token) })
  const r = await json<{ categories: Category[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.categories ?? [] }
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

// ── M9: Trust ──────────────────────────────────────────────────────────────

export async function getTrust(token: string, username: string): Promise<ApiResponse<TrustInfo>> {
  const res = await fetch(`${BASE}/users/${username}/trust`, { headers: authHeaders(token) })
  return json<TrustInfo>(res)
}

export async function getUserProfile(token: string | null, username: string): Promise<ApiResponse<UserProfile>> {
  const res = await fetch(`${BASE}/users/${username}`, { headers: authHeaders(token) })
  return json<UserProfile>(res)
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

export async function updateMyProfile(
  token: string,
  payload: { displayName?: string; bio?: string; avatar?: string },
): Promise<ApiResponse<unknown>> {
  const res = await fetch(`${BASE}/users/me`, {
    method: 'PATCH',
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

// ── Commands ───────────────────────────────────────────────────────────────

export type CommandName =
  | 'createThread' | 'appendPost' | 'editPost' | 'redactPost' | 'restorePost'
  | 'lockThread' | 'moveThread' | 'sanctionUser' | 'grantRole' | 'revokeRole'
  | 'sendChatLine' | 'setPresence' | 'createBoard' | 'purgePost'
  | 'reactPost' | 'unreactPost' | 'votePoll' | 'setThreadPref'
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
