import type { AuthResponse, Board, Thread, Post, AckResult, ApiResponse } from './types'

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
  return json<Board[]>(res)
}

export async function listThreads(token: string, board: string, limit = 30, offset = 0): Promise<ApiResponse<Thread[]>> {
  const res = await fetch(`${BASE}/boards/${board}/threads?limit=${limit}&offset=${offset}`, {
    headers: authHeaders(token),
  })
  return json<Thread[]>(res)
}

export async function getThread(token: string, id: string): Promise<ApiResponse<Thread>> {
  const res = await fetch(`${BASE}/threads/${id}`, { headers: authHeaders(token) })
  return json<Thread>(res)
}

export async function listPosts(token: string, thread: string, limit = 50, offset = 0): Promise<ApiResponse<Post[]>> {
  const res = await fetch(`${BASE}/threads/${thread}/posts?limit=${limit}&offset=${offset}`, {
    headers: authHeaders(token),
  })
  return json<Post[]>(res)
}

export async function search(token: string, q: string, board?: string): Promise<ApiResponse<Post[]>> {
  const params = new URLSearchParams({ q, limit: '30' })
  if (board) params.set('board', board)
  const res = await fetch(`${BASE}/search?${params}`, { headers: authHeaders(token) })
  return json<Post[]>(res)
}

// ── Commands ───────────────────────────────────────────────────────────────

export type CommandName =
  | 'createThread' | 'appendPost' | 'editPost' | 'redactPost' | 'restorePost'
  | 'lockThread' | 'moveThread' | 'sanctionUser' | 'grantRole' | 'revokeRole'
  | 'sendChatLine' | 'setPresence' | 'createBoard'

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
