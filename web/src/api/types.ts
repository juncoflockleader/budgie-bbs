// ── Projections ────────────────────────────────────────────────────────────

export interface Board {
  id: string
  name: string
  description: string
  anonymousAllowed?: boolean
  readOnly?: boolean
  noReply?: boolean
  attachmentsAllowed?: boolean
  mailInAllowed?: boolean
  relayEnabled?: boolean
  memberReadMode?: boolean
  memberPostMode?: boolean
  moderatorCount?: number
}

export interface BoardSummary extends Board {
  favorite: boolean
  unreadThreads: number
  unreadPosts: number
  threadCount: number
  postCount: number
  onlineUsers: number
  lastSeq: number
  readSeq: number
  createdAt: number
  newBoard: boolean
  anonymousAllowed: boolean
  readOnly: boolean
  noReply: boolean
  attachmentsAllowed: boolean
  mailInAllowed: boolean
  relayEnabled: boolean
  memberReadMode: boolean
  memberPostMode: boolean
  moderatorCount: number
}

export interface CommunityStats {
  totalUsers: number
  totalBoards: number
  totalThreads: number
  totalPosts: number
  totalReactions: number
  totalMail: number
  totalDirectMessages: number
  totalLogins: number
  totalOnlineSeconds: number
  onlineUsers: number
  onlineGuests: number
  maxOnlineUsers: number
  maxOnlineAt: number
  maxOnlineGuests: number
  maxOnlineGuestsAt: number
  headSeq: number
}

export interface CommunityStatHistory {
  day: string
  snapshotAt: number
  totalUsers: number
  totalBoards: number
  totalThreads: number
  totalPosts: number
  totalReactions: number
  totalMail: number
  totalDirectMessages: number
  totalLogins: number
  totalOnlineSeconds: number
  onlineUsers: number
  onlineGuests: number
  maxOnlineUsers: number
  maxOnlineAt: number
  maxOnlineGuests: number
  maxOnlineGuestsAt: number
  headSeq: number
  deltaUsers: number
  deltaBoards: number
  deltaThreads: number
  deltaPosts: number
  deltaReactions: number
  deltaMail: number
  deltaDirectMessages: number
  deltaLogins: number
  deltaOnlineSeconds: number
  deltaGuests: number
}

export interface BoardRanking {
  id: string
  name: string
  description: string
  threadCount: number
  postCount: number
  lastSeq: number
  lastPostAt: number
  moderatorCount: number
}

export interface ThreadRanking {
  id: string
  board: string
  boardName: string
  title: string
  author: string
  authorId?: string
  postCount: number
  reactionCount: number
  score: number
  lastSeq: number
  createdAt: number
  updatedAt: number
}

export interface ReplyRanking {
  postId: string
  threadId: string
  board: string
  boardName: string
  title: string
  author: string
  authorId?: string
  excerpt: string
  seq: number
  createdAt: number
}

export interface UserRanking {
  userId: string
  name: string
  role: string
  postsCreated: number
  reactionsReceived: number
  loginCount: number
  totalOnlineSeconds: number
  trustLevel: number
}

export interface BlessingRanking {
  userId: string
  name: string
  blessingCount: number
  lastBlessedAt: number
}

export interface ArchiveRanking {
  boardId: string
  boardName: string
  kind: string
  path: string
  entryCount: number
  editedCount: number
  lastUpdatedAt: number
}

export interface BoardSettings {
  boardId: string
  anonymousAllowed: boolean
  readOnly: boolean
  noReply: boolean
  attachmentsAllowed: boolean
  mailInAllowed: boolean
  relayEnabled: boolean
  memberReadMode: boolean
  memberPostMode: boolean
  updatedAt: number
}

export interface BoardModerator {
  userId: string
  name: string
  position: number
  createdAt: number
  updatedAt: number
}

export interface BoardMember {
  userId: string
  name: string
  title?: string
  position: number
  canManageMembers: boolean
  canCurate: boolean
  canModeratePosts: boolean
  canModerateThreads: boolean
  canAnnounce: boolean
  canManagePolls: boolean
  canSetBoardSettings: boolean
  createdAt: number
  updatedAt: number
}

export interface BoardMemberApplication {
  id: string
  boardId: string
  userId: string
  name: string
  status: 'pending' | 'approved' | 'rejected' | 'blacklisted'
  note?: string
  title?: string
  reviewerId?: string
  reviewerName?: string
  reviewNote?: string
  createdAt: number
  updatedAt: number
  reviewedAt?: number
}

export interface BoardMemberRequirements {
  boardId: string
  minLoginCount: number
  minPostCount: number
  minTrustLevel: number
  minScore: number
  minBoardPostCount: number
  minBoardOriginalPostCount: number
  minBoardDigestCount: number
  minBoardMarkCount: number
  maxMembers: number
  approvalMode: 'manual' | 'auto'
  updatedAt: number
}

export interface BoardInfo {
  board: Board
  settings: BoardSettings
  requirements: BoardMemberRequirements
  moderators: BoardModerator[]
  members: BoardMember[]
}

export interface DigestEntry {
  id: string
  boardId: string
  boardName?: string
  targetKind: 'post' | 'thread'
  targetId: string
  kind: string
  title: string
  path?: string
  note?: string
  bodyEdited?: boolean
  createdBy: string
  createdByName: string
  createdAt: number
  updatedAt: number
  threadId: string
  postId?: string
  author?: string
  excerpt?: string
}

export interface DigestPathNode {
  path: string
  name: string
  parentPath: string
  kind?: string
  entryCount: number
  childCount: number
  explicit?: boolean
}

export interface MailItem {
  id: string
  fromUserId: string
  fromName: string
  toUserIds: string[]
  toNames: string[]
  subject: string
  body?: string
  excerpt?: string
  parentId?: string
  mailbox: string
  role: string
  read: boolean
  kept: boolean
  attachments?: MailAttachment[]
  createdAt: number
  updatedAt: number
  seq: number
}

export interface MailUsage {
  userId: string
  usedBytes: number
  quotaBytes: number
  remainingBytes: number
}

export interface MailAttachment extends AttachmentPayload {
  id: string
  mailId: string
  stored: boolean
  createdBy?: string
  createdAt: number
}

export interface DirectMessageConversation {
  userId: string
  name: string
  lastMessageId: string
  lastBody: string
  lastFromName: string
  lastAt: number
  unreadCount: number
}

export interface MailGroupMember {
  userId: string
  name: string
  position: number
}

export interface MailGroup {
  id: string
  name: string
  members: MailGroupMember[]
  builtIn?: boolean
  createdAt: number
  updatedAt: number
}

export interface DirectMessageSettings {
  userId: string
  policy: 'all' | 'friends' | 'none'
  updatedAt: number
}

export interface SocialUser {
  userId: string
  sessionId?: string
  name: string
  displayName: string
  role: string
  note?: string
  kind: string
  mutual: boolean
  ignored: boolean
  status?: string
  mode?: string
  boardId?: string
  boardName?: string
  threadId?: string
  locationLabel?: string
  fromHost?: string
  lastSeen: number
  idleSeconds: number
  online: boolean
  createdAt: number
  updatedAt: number
}

export interface DirectMessage {
  id: string
  conversationId: string
  fromUserId: string
  fromName: string
  toUserId: string
  toName: string
  otherUserId: string
  otherName: string
  body: string
  read: boolean
  mine: boolean
  createdAt: number
  seq: number
}

export interface FavoriteFolder {
  id: string
  parentId?: string
  name: string
  position: number
  createdAt: number
  updatedAt: number
}

export interface FavoriteBoardEntry extends Board {
  folderId?: string
  position: number
  unreadThreads: number
  unreadPosts: number
  lastSeq: number
  readSeq: number
}

export interface FavoriteTree {
  folders: FavoriteFolder[]
  boards: FavoriteBoardEntry[]
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
  boardName?: string
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

export interface ThreadSummary extends Thread {
  readSeq: number
  unreadPosts: number
  firstUnreadPostId?: string
}

export interface Post {
  id: string
  thread: string
  board?: string
  boardName?: string
  threadTitle?: string
  author: string
  authorId?: string
  body: string
  signature?: string
  contentType: string
  replyTo?: string
  replyDepth?: number
  version: number
  reactionCount: number
  redacted: boolean
  marked: boolean
  recommended: boolean
  noReply: boolean
  tex: boolean
  mailBack: boolean
  sourcePost?: string
  sourceThread?: string
  sourceBoard?: string
  sourceAuthor?: string
  sourceAuthorId?: string
  sourceTitle?: string
  attachments?: PostAttachment[]
  createdSeq: number
  updatedSeq: number
  createdAt?: number
  updatedAt?: number
}

export interface AttachmentPayload {
  id?: string
  filename: string
  contentType?: string
  sizeBytes?: number
  url?: string
}

export interface PostAttachment extends AttachmentPayload {
  id: string
  postId: string
  stored: boolean
  createdBy?: string
  createdAt: number
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
  token?: string
  expiresAt?: number
  status?: string
  user: { id: string; name: string; role: string; registrationStatus?: string }
}

// ── Events (incoming from WS/SSE) ─────────────────────────────────────────

export type EventKind =
  | 'thread.new'
  | 'post.appended'
  | 'post.attachment_added'
  | 'post.edited'
  | 'post.flags_set'
  | 'post.redacted'
  | 'post.restored'
  | 'thread.title_set'
  | 'thread.locked'
  | 'thread.moved'
  | 'user.sanctioned'
  | 'user.sanction_cleared'
  | 'content_filter.set'
  | 'role.granted'
  | 'role.revoked'
  | 'board.created'
  | 'mail.sent'
  | 'mail.attachment_added'
  | 'direct_message.sent'
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
  authorId?: string; signature?: string; contentType: string; replyTo?: string
  tex?: boolean; mailBack?: boolean
  sourcePost?: string; sourceThread?: string; sourceBoard?: string; sourceAuthor?: string; sourceAuthorId?: string; sourceTitle?: string
  attachments?: AttachmentPayload[]; ts: number
}
export interface PostAttachmentAddedPayload {
  id: string; post: string; thread: string; filename: string
  contentType?: string; sizeBytes?: number; authorId?: string; ts: number
}
export interface PostEditedPayload {
  id: string; thread: string; newBody: string; version: number; ts: number
}
export interface PostFlagsSetPayload {
  id: string; thread: string; marked: boolean; recommended: boolean; noReply: boolean; tex?: boolean; mailBack?: boolean; by: string; ts: number
}
export interface PostRedactedPayload {
  id: string; thread: string; by: string; reason?: string; ts: number
}
export interface PostRestoredPayload {
  id: string; thread: string; by: string; ts: number
}
export interface ThreadTitleSetPayload {
  thread: string; title: string; by: string; ts: number
}
export interface ThreadLockedPayload {
  thread: string; locked: boolean; by: string; ts: number
}
export interface PostFlaggedPayload {
  reviewId: string
  kind?: string
  postId: string
  thread: string
  reporter: string
  reason?: string
  ts: number
}
export interface ContentFilterSetPayload {
  id: string
  pattern: string
  scope?: string
  active: boolean
  by: string
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
  user: string; userId?: string; sessionId?: string; status: string; mode?: string; board?: string; thread?: string; location?: string; fromHost?: string; ts: number
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
  kind: 'mention' | 'reply' | 'watched' | 'login'
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
  title: string
  bio: string
  avatar: string
  signature: string
  plan: string
  homepage: string
  created: number
  lastSeen: number
  postsCreated: number
  reactionsReceived: number
  trustLevel: number
  pubkeys: string[]
}

export interface AccountRegistrationSettings {
  requireApproval: boolean
  updatedAt: number
}

export interface AccountRegistration {
  id: string
  name: string
  role: string
  status: 'pending' | 'approved' | 'rejected'
  created: number
  reviewedAt: number
  reviewedBy: string
  reviewedByName?: string
  reviewReason: string
}

export interface PasswordRecoveryRequest {
  id: string
  userId: string
  userName: string
  status: 'pending' | 'resolved' | 'rejected'
  submittedName: string
  submittedEmail: string
  note: string
  reviewerId: string
  reviewerName?: string
  reviewNote: string
  createdAt: number
  updatedAt: number
}

export interface UserPrivateProfile {
  userId: string
  realName: string
  realEmail: string
  registrationEmail: string
  address: string
  phone: string
  mobile: string
  birthday: string
  school: string
  contactNote: string
  updatedAt: number
}

export interface UserPersonalFile {
  userId: string
  name: string
  body: string
  public: boolean
  updatedAt: number
}

export interface UserSignature {
  id: string
  userId: string
  label: string
  body: string
  position: number
  active: boolean
  createdAt: number
  updatedAt: number
}

export interface UserSignatureSettings {
  userId: string
  selectedSignatureId: string
  randomEnabled: boolean
  updatedAt: number
}

export interface UserSignatureBundle {
  signatures: UserSignature[]
  settings: UserSignatureSettings
  maxCount: number
}

export interface UserSignatureRecount {
  userId: string
  count: number
  activeCount: number
  selectedSignatureId: string
  randomEnabled: boolean
  currentSignature: string
  updatedAt: number
}

export interface UserLoginACLRule {
  id: string
  userId: string
  pattern: string
  note: string
  position: number
  active: boolean
  createdAt: number
  updatedAt: number
}

export interface UserLoginACLSettings {
  userId: string
  enabled: boolean
  updatedAt: number
}

export interface UserLoginACLBundle {
  rules: UserLoginACLRule[]
  settings: UserLoginACLSettings
  host?: string
  allowed: boolean
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

export interface ContentFilter {
  id: string
  pattern: string
  scope: string
  active: boolean
  createdBy: string
  createdAt: number
  updatedAt: number
}

// ── M9: Trust ───────────────────────────────────────────────────────────────
export interface TrustInfo {
  loginCount: number
  postsCreated: number
  daysVisited: number
  reactionsReceived: number
  totalOnlineSeconds: number
  trustLevel: number
}
