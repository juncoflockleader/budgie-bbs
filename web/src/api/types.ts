// ── Projections ────────────────────────────────────────────────────────────

export interface Board {
  id: string
  name: string
  description: string
}

export interface Category {
  id: string
  name: string
  description: string
  parentId?: string
  position: number
  visibility: string
  createdAt: number
  updatedAt: number
}

export interface Thread {
  id: string
  board: string
  author: string
  authorId?: string
  title: string
  locked: boolean
  postCount: number
  lastSeq: number
  createdTs: number
  createdAt?: number
  updatedAt?: number
}

export interface Post {
  id: string
  thread: string
  author: string
  authorId?: string
  body: string
  contentType: string
  replyTo?: string
  version: number
  reactionCount: number
  redacted: boolean
  createdSeq: number
  updatedSeq: number
  createdAt?: number
  updatedAt?: number
}

// ── Wire ───────────────────────────────────────────────────────────────────

export interface AckResult {
  id?: string
  seq?: number
}

export interface ErrorDetail {
  code: string
  message: string
  retryable: boolean
}

export interface ApiResponse<T> {
  data?: T
  error?: ErrorDetail
}

// ── Auth ───────────────────────────────────────────────────────────────────

export interface AuthResponse {
  token: string
  expiresAt: number
  user: { id: string; name: string; role: string }
}

// ── Events (incoming from WS/SSE) ─────────────────────────────────────────

export type EventKind =
  | 'thread.new'
  | 'post.appended'
  | 'post.edited'
  | 'post.redacted'
  | 'post.restored'
  | 'thread.locked'
  | 'thread.moved'
  | 'user.sanctioned'
  | 'role.granted'
  | 'role.revoked'
  | 'board.created'
  | 'chat.line'
  | 'presence.update'
  | 'user.joined'
  | 'user.left'
  | 'post.reacted'
  | 'post.unreacted'
  | 'poll.voted'
  | 'user.mentioned'
  | 'trust.changed'
  | 'post.flagged'
  | 'review.resolved'

export interface BudgieEvent {
  event: EventKind
  seq?: number
  eseq?: number
  ts: number
  payload: unknown
}

export interface ThreadNewPayload {
  id: string; board: string; author: string; authorId?: string; title: string; ts: number
}
export interface PostAppendedPayload {
  id: string; thread: string; author: string; body: string; rawBody?: string
  authorId?: string; contentType: string; replyTo?: string; ts: number
}
export interface PostEditedPayload {
  id: string; thread: string; newBody: string; version: number; ts: number
}
export interface PostRedactedPayload {
  id: string; thread: string; by: string; reason?: string; ts: number
}
export interface PostRestoredPayload {
  id: string; thread: string; by: string; ts: number
}
export interface ThreadLockedPayload {
  thread: string; locked: boolean; by: string; ts: number
}
export interface PostFlaggedPayload {
  reviewId: string
  postId: string
  thread: string
  reporter: string
  reason?: string
  ts: number
}
export interface ReviewResolvedPayload {
  reviewId: string
  resolution: string
  by: string
  ts: number
}
export interface ChatLinePayload {
  id: string; room: string; user: string; text: string; ts: number
}
export interface PresenceUpdatePayload {
  user: string; status: string; ts: number
}
export interface UserJoinedPayload { user: string; ts: number }
export interface UserLeftPayload   { user: string; ts: number }

// ── M10: Reactions ──────────────────────────────────────────────────────────
export interface PostReactedPayload {
  postId: string; thread: string; user: string; emoji: string
  reactionCount: number; ts: number
}
export interface PostUnreactedPayload {
  postId: string; thread: string; user: string; emoji: string
  reactionCount: number; ts: number
}

// ── M11: Polls ──────────────────────────────────────────────────────────────
export interface PollOption {
  id: string
  text: string
  voteCount: number
}
export interface Poll {
  id: string
  postId: string
  question?: string
  expiresAt?: number
  ts: number
  options: PollOption[]
  voted?: string // option id the viewer voted for
}
export type PollMap = Record<string, Poll>
export interface PollVotedPayload {
  poll: string; option: string; user: string; ts: number
}

// ── M8: Notifications ───────────────────────────────────────────────────────
export interface Notification {
  id: string
  kind: 'mention' | 'reply' | 'watched'
  threadId: string
  postId: string
  actor: string
  read: boolean
  ts: number
}

export interface UserProfile {
  id: string
  name: string
  role: string
  displayName: string
  bio: string
  avatar: string
  created: number
  lastSeen: number
  postsCreated: number
  reactionsReceived: number
  trustLevel: number
  pubkeys: string[]
}

export interface ModerationReview {
  id: string
  kind: string
  status: string
  targetId: string
  targetKind: string
  reporter: string
  reason: string
  resolution: string
  actor: string
  createdAt: number
  updatedAt: number
}

// ── M9: Trust ───────────────────────────────────────────────────────────────
export interface TrustInfo {
  postsCreated: number
  daysVisited: number
  reactionsReceived: number
  trustLevel: number
}
