import type {
  AckResult,
  ApiResponse,
  AttachmentPayload,
  AuthResponse,
  AuthPolicy,
  CaptchaChallenge,
  Board,
  BoardInfo,
  BoardMemberApplication,
  BoardMemberRequirements,
  BoardSettings,
  BoardSummary,
  ChatLine,
  ChatRoom,
  BoardRanking,
  BlessingRanking,
  ArchiveRanking,
  Category,
  CommandStatus,
  CommunityStatHistory,
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
  ReplyRanking,
  RecommendedBoard,
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
  UserSanction,
  TwoFactorStatus,
  SecuritySettings,
  AISettings,
  BoardAIConfig,
  BoardAIConfigPatch,
  SiteAppearance,
  TUIStockArt,
  BoardAutomodRule,
  BoardAutomodActivity,
} from './types'

const BASE = '/api/v1'

export interface FreshReadOptions {
  minSeq?: number
  useLatestAckSeq?: boolean
  retryProjectionStale?: boolean
  projectionRetryLimit?: number
}

export interface CommandResolveOptions {
  intervalMs?: number
  timeoutMs?: number
  onQueued?: (ack: AckResult) => void
  onStatus?: (status: CommandStatus) => void
}

let latestDurableAckSeq = 0

function authHeaders(token: string | null): Record<string, string> {
  return token ? { Authorization: `Bearer ${token}` } : {}
}

function freshnessSeq(options: FreshReadOptions = {}): number {
  if (options.minSeq !== undefined) return Math.max(0, Math.trunc(options.minSeq))
  if (options.useLatestAckSeq === false) return 0
  return latestDurableAckSeq
}

function readHeaders(token: string | null, options: FreshReadOptions = {}): Record<string, string> {
  const headers = authHeaders(token)
  const minSeq = freshnessSeq(options)
  if (minSeq > 0) headers['X-Budgie-Min-Seq'] = String(minSeq)
  return headers
}

function normalizeError(body: unknown): ApiResponse<unknown>['error'] {
  if (body && typeof body === 'object') {
    const record = body as Record<string, unknown>
    if (record.error && typeof record.error === 'object') {
      return record.error as ApiResponse<unknown>['error']
    }
    if (typeof record.code === 'string' || typeof record.message === 'string') {
      return {
        code: typeof record.code === 'string' ? record.code : 'request_failed',
        message: typeof record.message === 'string' ? record.message : 'Request failed',
        retryable: Boolean(record.retryable),
      }
    }
  }
  return { code: 'request_failed', message: 'Request failed', retryable: false }
}

export function rememberDurableAck(ack: AckResult | undefined | null): void {
  const seq = ack?.seq ?? 0
  if (Number.isFinite(seq) && seq > latestDurableAckSeq) latestDurableAckSeq = Math.trunc(seq)
}

export function latestDurableWriteSeq(): number {
  return latestDurableAckSeq
}

function rememberTopLevelAckSeq(body: unknown): void {
  if (!body || typeof body !== 'object') return
  const seq = (body as Record<string, unknown>).seq
  if (typeof seq === 'number') rememberDurableAck({ seq })
}

function isAckEnvelope(body: unknown): body is {
  kind?: string
  ok?: boolean
  result?: AckResult
  error?: ApiResponse<unknown>['error']
  code?: string
  message?: string
  retryable?: boolean
} {
  return Boolean(body)
    && typeof body === 'object'
    && (body as Record<string, unknown>).kind === 'ack'
    && typeof (body as Record<string, unknown>).ok === 'boolean'
}

function unwrapAckEnvelope<T>(body: unknown): ApiResponse<T> | undefined {
  if (!isAckEnvelope(body)) return undefined
  if (body.ok === false) {
    return {
      error: body.error ?? {
        code: body.code ?? 'request_failed',
        message: body.message ?? 'Request failed',
        retryable: Boolean(body.retryable),
      },
    }
  }
  const ack = body.result ?? { id: undefined, seq: undefined }
  rememberDurableAck(ack)
  return { data: ack as T }
}

async function json<T>(res: Response): Promise<ApiResponse<T>> {
  if (res.status === 204) return { data: undefined }
  const body = await res.json() as Record<string, unknown>
  if (!res.ok) return { error: normalizeError(body) as ApiResponse<T>['error'] }
  const ack = unwrapAckEnvelope<T>(body)
  if (ack) return ack
  rememberTopLevelAckSeq(body)
  return { data: body as T }
}

async function projectionJSON<T>(
  request: () => Promise<Response>,
  options: FreshReadOptions = {},
): Promise<ApiResponse<T>> {
  const retryLimit = options.retryProjectionStale === false ? 0 : (options.projectionRetryLimit ?? 3)
  for (let attempt = 0; attempt <= retryLimit; attempt += 1) {
    const result = await json<T>(await request())
    if (result.error?.code !== 'projection_stale' || attempt === retryLimit) return result
    const retryAfterMs = result.error.retryAfterMs ?? 1000
    await sleep(Math.max(100, Math.min(retryAfterMs, 3000)))
  }
  return { error: { code: 'projection_stale', message: 'Projection is still stale', retryable: true } }
}

async function jsonAck(res: Response): Promise<ApiResponse<AckResult>> {
  const body = await res.json() as {
    ok?: boolean
    result?: AckResult
    error?: ApiResponse<AckResult>['error']
    code?: string
    message?: string
    retryable?: boolean
  }
  if (!res.ok || body.ok === false) {
    return { error: body.error ?? { code: body.code ?? 'request_failed', message: body.message ?? 'Request failed', retryable: Boolean(body.retryable) } }
  }
  const ack = unwrapAckEnvelope<AckResult>(body)
  if (ack) return ack
  const result = body.result ?? { id: undefined, seq: undefined }
  rememberDurableAck(result)
  return { data: result }
}

function sleep(ms: number): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, ms))
}

export function isPendingCommandAck(ack: AckResult | undefined | null): ack is AckResult {
  return ack?.status === 'pending'
    && Boolean(ack.commandId || ack.id)
    && Boolean(ack.commandPartitionKind)
    && Boolean(ack.commandPartitionKey)
    && (ack.commandOffset ?? 0) > 0
}

export async function getCommandStatus(token: string, ack: AckResult): Promise<ApiResponse<CommandStatus>> {
  const commandId = ack.commandId || ack.id || ''
  if (!commandId || !ack.commandPartitionKind || !ack.commandPartitionKey || !ack.commandOffset) {
    return {
      error: {
        code: 'validation_failed',
        message: 'Command id, partition, and offset are required',
        retryable: false,
      },
    }
  }
  const params = new URLSearchParams({
    commandPartitionKind: ack.commandPartitionKind,
    commandPartitionKey: ack.commandPartitionKey,
    commandOffset: String(ack.commandOffset),
  })
  const res = await fetch(`${BASE}/commands/${encodeURIComponent(commandId)}?${params}`, { headers: authHeaders(token) })
  return json<CommandStatus>(res)
}

export async function resolveCommandResult(
  token: string,
  ack: AckResult | undefined,
  options: CommandResolveOptions = {},
): Promise<ApiResponse<AckResult>> {
  if (!ack) {
    return {
      error: {
        code: 'validation_failed',
        message: 'Command acknowledgement is missing',
        retryable: false,
      },
    }
  }
  if (!isPendingCommandAck(ack)) {
    rememberDurableAck(ack)
    return { data: ack }
  }

  const intervalMs = options.intervalMs ?? 600
  const deadline = Date.now() + (options.timeoutMs ?? 60_000)
  while (Date.now() <= deadline) {
    const status = await getCommandStatus(token, ack)
    if (status.error) return { error: status.error }
    if (status.data) {
      options.onStatus?.(status.data)
      if (status.data.status === 'applied') {
        const applied = status.data.result ?? ack
        rememberDurableAck(applied)
        return { data: applied }
      }
      if (status.data.status === 'failed') {
        return {
          error: status.data.error ?? {
            code: 'command_failed',
            message: 'Command failed',
            retryable: false,
          },
        }
      }
      if (status.data.status === 'committed') {
        return {
          error: {
            code: 'command_result_missing',
            message: 'Command committed before a materialized result was available',
            retryable: true,
          },
        }
      }
    }
    await sleep(intervalMs)
  }

  return {
    error: {
      code: 'command_status_timeout',
      message: 'Command is still queued; keep this draft open and check again shortly.',
      retryable: true,
    },
  }
}

async function jsonResolvedAck(
  token: string,
  res: Response,
  options: CommandResolveOptions = {},
): Promise<ApiResponse<AckResult>> {
  const ack = await json<AckResult>(res)
  if (ack.error || !ack.data) return ack
  if (isPendingCommandAck(ack.data)) options.onQueued?.(ack.data)
  return resolveCommandResult(token, ack.data, options)
}

export async function getAuthPolicy(): Promise<ApiResponse<AuthPolicy>> {
  const res = await fetch(`${BASE}/auth/policy`)
  return json<AuthPolicy>(res)
}

export async function getCaptchaChallenge(): Promise<ApiResponse<CaptchaChallenge>> {
  const res = await fetch(`${BASE}/auth/captcha`)
  return json<CaptchaChallenge>(res)
}

export async function register(
  name: string,
  password: string,
  extras?: {
    email?: string
    realName?: string
    affiliation?: string
    note?: string
    acceptPolicy?: boolean
    policyVersion?: string
    captchaChallengeId?: string
    captchaAnswer?: string
    captchaToken?: string
  },
): Promise<ApiResponse<AuthResponse>> {
  const res = await fetch(`${BASE}/auth/register`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      name,
      password,
      email: extras?.email,
      realName: extras?.realName,
      affiliation: extras?.affiliation,
      note: extras?.note,
      acceptPolicy: extras?.acceptPolicy,
      policyVersion: extras?.policyVersion,
      captchaChallengeId: extras?.captchaChallengeId,
      captchaAnswer: extras?.captchaAnswer,
      captchaToken: extras?.captchaToken,
    }),
  })
  return json<AuthResponse>(res)
}

export async function getPrivacyPolicy(): Promise<ApiResponse<{ markdown: string; version: string }>> {
  const res = await fetch(`${BASE}/auth/privacy-policy`)
  return json<{ markdown: string; version: string }>(res)
}

export async function resendVerification(name: string): Promise<ApiResponse<unknown>> {
  const res = await fetch(`${BASE}/auth/resend-verification`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }),
  })
  return json<unknown>(res)
}

export async function login(name: string, password: string): Promise<ApiResponse<AuthResponse>> {
  const res = await fetch(`${BASE}/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, password }),
  })
  return json<AuthResponse>(res)
}

// ── Two-factor authentication ────────────────────────────────────────────────

export async function verifyTwoFactor(challengeToken: string, method: string, code: string): Promise<ApiResponse<AuthResponse>> {
  const res = await fetch(`${BASE}/auth/2fa/verify`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ challengeToken, method, code }),
  })
  return json<AuthResponse>(res)
}

export async function requestEmailTwoFactor(challengeToken: string): Promise<ApiResponse<{ ok: boolean }>> {
  const res = await fetch(`${BASE}/auth/2fa/email`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ challengeToken }),
  })
  return json<{ ok: boolean }>(res)
}

export async function getTwoFactorStatus(token: string): Promise<ApiResponse<TwoFactorStatus>> {
  const res = await fetch(`${BASE}/account/2fa`, { headers: authHeaders(token) })
  return json<TwoFactorStatus>(res)
}

export async function initTOTP(token: string): Promise<ApiResponse<{ secret: string; otpauthUri: string; qr: string }>> {
  const res = await fetch(`${BASE}/account/2fa/totp`, { method: 'POST', headers: authHeaders(token) })
  return json<{ secret: string; otpauthUri: string; qr: string }>(res)
}

export async function confirmTOTP(token: string, code: string): Promise<ApiResponse<{ ok: boolean }>> {
  const res = await fetch(`${BASE}/account/2fa/totp/confirm`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify({ code }),
  })
  return json<{ ok: boolean }>(res)
}

export async function disableTOTP(token: string): Promise<ApiResponse<{ ok: boolean }>> {
  const res = await fetch(`${BASE}/account/2fa/totp`, { method: 'DELETE', headers: authHeaders(token) })
  return json<{ ok: boolean }>(res)
}

export async function enableEmailTwoFactor(token: string): Promise<ApiResponse<{ ok: boolean }>> {
  const res = await fetch(`${BASE}/account/2fa/email`, { method: 'POST', headers: authHeaders(token) })
  return json<{ ok: boolean }>(res)
}

export async function disableEmailTwoFactor(token: string): Promise<ApiResponse<{ ok: boolean }>> {
  const res = await fetch(`${BASE}/account/2fa/email`, { method: 'DELETE', headers: authHeaders(token) })
  return json<{ ok: boolean }>(res)
}

export async function generateBackupCodes(token: string): Promise<ApiResponse<{ codes: string[] }>> {
  const res = await fetch(`${BASE}/account/2fa/backup-codes`, { method: 'POST', headers: authHeaders(token) })
  return json<{ codes: string[] }>(res)
}

export async function getSiteAppearance(): Promise<ApiResponse<SiteAppearance>> {
  const res = await fetch(`${BASE}/site/appearance`)
  return json<SiteAppearance>(res)
}

export async function setSiteAppearance(token: string, payload: Partial<SiteAppearance>): Promise<ApiResponse<SiteAppearance>> {
  const res = await fetch(`${BASE}/admin/site-appearance`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  return json<SiteAppearance>(res)
}

export async function getTUIStockArt(): Promise<ApiResponse<{ arts: TUIStockArt[] }>> {
  const res = await fetch(`${BASE}/site/tui-stock-art`)
  return json<{ arts: TUIStockArt[] }>(res)
}

// siteAssetURL is the app endpoint for an uploaded site image (logo|banner);
// used for admin previews. Append a cache-busting query for the freshest copy.
export function siteAssetURL(name: string): string {
  return `${BASE}/site/asset/${name}`
}

// buildSiteAssetURL returns the public URL for a site image: the CDN/base URL
// (immutable, versioned) when configured, else the app endpoint with a version
// query. Returns null when the asset is unset so callers can show a fallback.
export function buildSiteAssetURL(
  appearance: { assetBaseURL?: string; assetVersions?: Record<string, number> } | null | undefined,
  name: string,
): string | null {
  const version = appearance?.assetVersions?.[name] ?? 0
  if (!version) return null
  const base = appearance?.assetBaseURL
  if (base) return `${base}/site/${name}-${version}.png`
  return `${BASE}/site/asset/${name}?v=${version}`
}

export async function uploadSiteAsset(token: string, name: string, file: File): Promise<ApiResponse<{ ok: boolean; byteSize: number }>> {
  const res = await fetch(`${BASE}/admin/site-asset/${name}`, {
    method: 'POST',
    headers: { 'Content-Type': 'image/png', ...authHeaders(token) },
    body: file,
  })
  return json<{ ok: boolean; byteSize: number }>(res)
}

export async function deleteSiteAsset(token: string, name: string): Promise<ApiResponse<{ ok: boolean }>> {
  const res = await fetch(`${BASE}/admin/site-asset/${name}`, {
    method: 'DELETE',
    headers: { ...authHeaders(token) },
  })
  return json<{ ok: boolean }>(res)
}

export async function getSecuritySettings(token: string): Promise<ApiResponse<SecuritySettings>> {
  const res = await fetch(`${BASE}/admin/security-settings`, { headers: authHeaders(token) })
  return json<SecuritySettings>(res)
}

export async function setSecuritySettings(token: string, staff2faRequired: boolean): Promise<ApiResponse<SecuritySettings>> {
  const res = await fetch(`${BASE}/admin/security-settings`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify({ staff2faRequired }),
  })
  return json<SecuritySettings>(res)
}

export async function getAISettings(token: string): Promise<ApiResponse<AISettings>> {
  const res = await fetch(`${BASE}/ai/settings`, { headers: authHeaders(token) })
  return json<AISettings>(res)
}

export async function setAISettings(token: string, enabled: boolean): Promise<ApiResponse<AISettings>> {
  const res = await fetch(`${BASE}/admin/ai-settings`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify({ enabled }),
  })
  return json<AISettings>(res)
}

export async function getBoardAIConfig(token: string, board: string): Promise<ApiResponse<BoardAIConfig>> {
  const res = await fetch(`${BASE}/boards/${board}/ai`, { headers: authHeaders(token) })
  return json<BoardAIConfig>(res)
}

export async function setBoardAIConfig(token: string, board: string, patch: BoardAIConfigPatch): Promise<ApiResponse<BoardAIConfig>> {
  const res = await fetch(`${BASE}/boards/${board}/ai`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(patch),
  })
  return json<BoardAIConfig>(res)
}

export async function getUserTwoFactorStatus(token: string, name: string): Promise<ApiResponse<TwoFactorStatus>> {
  const res = await fetch(`${BASE}/users/${name}/2fa`, { headers: authHeaders(token) })
  return json<TwoFactorStatus>(res)
}

export async function listBoardAutomodRules(token: string, board: string): Promise<ApiResponse<BoardAutomodRule[]>> {
  const res = await fetch(`${BASE}/boards/${board}/automod-rules`, { headers: authHeaders(token) })
  const r = await json<{ rules: BoardAutomodRule[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.rules ?? [] }
}

export async function listBoardAutomodActivity(token: string, board: string): Promise<ApiResponse<BoardAutomodActivity[]>> {
  const res = await fetch(`${BASE}/boards/${board}/automod-activity`, { headers: authHeaders(token) })
  const r = await json<{ activity: BoardAutomodActivity[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.activity ?? [] }
}

export async function logout(): Promise<ApiResponse<unknown>> {
  // Cookie-authenticated; the HttpOnly session cookie is sent automatically
  // same-origin, and the server clears it.
  const res = await fetch(`${BASE}/auth/logout`, {
    method: 'POST',
    keepalive: true,
  })
  return json<unknown>(res)
}

// getMe restores the SPA session from the HttpOnly session cookie (no token in
// JS). Returns the current user, or an error/401 when not signed in.
export async function getMe(): Promise<ApiResponse<{ id: string; name: string; role: string; registrationStatus?: string }>> {
  const res = await fetch(`${BASE}/auth/me`)
  return json<{ id: string; name: string; role: string; registrationStatus?: string }>(res)
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

export async function listBoards(token: string, options: FreshReadOptions = {}): Promise<ApiResponse<Board[]>> {
  const r = await projectionJSON<{ boards: Board[] }>(
    () => fetch(`${BASE}/boards`, { headers: readHeaders(token, options) }),
    options,
  )
  if (r.error) return { error: r.error }
  return { data: r.data?.boards ?? [] }
}

export async function getCommunityStats(token: string, options: FreshReadOptions = {}): Promise<ApiResponse<CommunityStats>> {
  return projectionJSON<CommunityStats>(
    () => fetch(`${BASE}/stats/community`, { headers: readHeaders(token, options) }),
    options,
  )
}

export async function setGuestPresence(payload: {
  sessionId: string
  status?: string
  location?: string
  fromHost?: string
}): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/presence/guest`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
  return json<AckResult>(res)
}

export async function listCommunityStatHistory(token: string, limit = 30, options: FreshReadOptions = {}): Promise<ApiResponse<CommunityStatHistory[]>> {
  const params = new URLSearchParams({ limit: String(limit) })
  const r = await projectionJSON<{ days: CommunityStatHistory[] }>(
    () => fetch(`${BASE}/stats/community/history?${params}`, { headers: readHeaders(token, options) }),
    options,
  )
  if (r.error) return { error: r.error }
  return { data: r.data?.days ?? [] }
}

export async function publishStatsSnapshot(token: string, date?: string): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/stats/community/snapshot`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(date ? { date } : {}),
  })
  return jsonResolvedAck(token, res)
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
  return jsonResolvedAck(token, res)
}

export async function listBoardRankings(token: string, limit = 20, options: FreshReadOptions = {}): Promise<ApiResponse<BoardRanking[]>> {
  const params = new URLSearchParams({ limit: String(limit) })
  const r = await projectionJSON<{ boards: BoardRanking[] }>(
    () => fetch(`${BASE}/rankings/boards?${params}`, { headers: readHeaders(token, options) }),
    options,
  )
  if (r.error) return { error: r.error }
  return { data: r.data?.boards ?? [] }
}

export async function listRecommendedBoards(token: string, limit = 20, options: FreshReadOptions = {}): Promise<ApiResponse<RecommendedBoard[]>> {
  const params = new URLSearchParams({ limit: String(limit) })
  const r = await projectionJSON<{ boards: RecommendedBoard[] }>(
    () => fetch(`${BASE}/boards/recommended?${params}`, { headers: readHeaders(token, options) }),
    options,
  )
  if (r.error) return { error: r.error }
  return { data: r.data?.boards ?? [] }
}

export async function setRecommendedBoard(
  token: string,
  board: string,
  recommended: boolean,
  payload: { note?: string; position?: number } = {},
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/boards/${board}/recommended`, {
    method: recommended ? 'PUT' : 'DELETE',
    headers: recommended ? { 'Content-Type': 'application/json', ...authHeaders(token) } : authHeaders(token),
    body: recommended ? JSON.stringify(payload) : undefined,
  })
  return jsonResolvedAck(token, res)
}

export async function listThreadRankings(token: string, limit = 20, board = '', options: FreshReadOptions = {}): Promise<ApiResponse<ThreadRanking[]>> {
  const params = new URLSearchParams({ limit: String(limit) })
  if (board) params.set('board', board)
  const r = await projectionJSON<{ threads: ThreadRanking[] }>(
    () => fetch(`${BASE}/rankings/threads?${params}`, { headers: readHeaders(token, options) }),
    options,
  )
  if (r.error) return { error: r.error }
  return { data: r.data?.threads ?? [] }
}

export async function listReplyRankings(token: string, limit = 20, options: FreshReadOptions = {}): Promise<ApiResponse<ReplyRanking[]>> {
  const params = new URLSearchParams({ limit: String(limit) })
  const r = await projectionJSON<{ replies: ReplyRanking[] }>(
    () => fetch(`${BASE}/rankings/replies?${params}`, { headers: readHeaders(token, options) }),
    options,
  )
  if (r.error) return { error: r.error }
  return { data: r.data?.replies ?? [] }
}

export async function listUserRankings(token: string, limit = 20, options: FreshReadOptions = {}): Promise<ApiResponse<UserRanking[]>> {
  const params = new URLSearchParams({ limit: String(limit) })
  const r = await projectionJSON<{ users: UserRanking[] }>(
    () => fetch(`${BASE}/rankings/users?${params}`, { headers: readHeaders(token, options) }),
    options,
  )
  if (r.error) return { error: r.error }
  return { data: r.data?.users ?? [] }
}

export async function listBlessingRankings(token: string, limit = 20, options: FreshReadOptions = {}): Promise<ApiResponse<BlessingRanking[]>> {
  const params = new URLSearchParams({ limit: String(limit) })
  const r = await projectionJSON<{ blessings: BlessingRanking[] }>(
    () => fetch(`${BASE}/rankings/blessings?${params}`, { headers: readHeaders(token, options) }),
    options,
  )
  if (r.error) return { error: r.error }
  return { data: r.data?.blessings ?? [] }
}

export async function blessUser(token: string, name: string, message = ''): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/users/${encodeURIComponent(name)}/bless`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify({ message }),
  })
  return jsonResolvedAck(token, res)
}

export async function listArchiveRankings(token: string, limit = 20, kind = 'archive', options: FreshReadOptions = {}): Promise<ApiResponse<ArchiveRanking[]>> {
  const params = new URLSearchParams({ limit: String(limit) })
  if (kind) params.set('kind', kind)
  const r = await projectionJSON<{ archives: ArchiveRanking[] }>(
    () => fetch(`${BASE}/rankings/archive?${params}`, { headers: readHeaders(token, options) }),
    options,
  )
  if (r.error) return { error: r.error }
  return { data: r.data?.archives ?? [] }
}

export async function getBoardInfo(token: string, board: string, options: FreshReadOptions = {}): Promise<ApiResponse<BoardInfo>> {
  return projectionJSON<BoardInfo>(
    () => fetch(`${BASE}/boards/${board}`, { headers: readHeaders(token, options) }),
    options,
  )
}

export async function listDigestEntries(
  token: string,
  board: string,
  kind = '',
  path = '',
  options: FreshReadOptions = {},
): Promise<ApiResponse<DigestEntry[]>> {
  const params = new URLSearchParams()
  if (kind) params.set('kind', kind)
  if (path) params.set('path', path)
  const suffix = params.toString() ? `?${params}` : ''
  const r = await projectionJSON<{ entries: DigestEntry[] }>(
    () => fetch(`${BASE}/boards/${board}/digest${suffix}`, { headers: readHeaders(token, options) }),
    options,
  )
  if (r.error) return { error: r.error }
  return { data: r.data?.entries ?? [] }
}

export async function listDigestPathTree(
  token: string,
  board: string,
  kind = '',
  options: FreshReadOptions = {},
): Promise<ApiResponse<DigestPathNode[]>> {
  const params = new URLSearchParams()
  if (kind) params.set('kind', kind)
  const suffix = params.toString() ? `?${params}` : ''
  const r = await projectionJSON<{ nodes: DigestPathNode[] }>(
    () => fetch(`${BASE}/boards/${board}/digest/tree${suffix}`, { headers: readHeaders(token, options) }),
    options,
  )
  if (r.error) return { error: r.error }
  return { data: r.data?.nodes ?? [] }
}

export async function listSiteDigestEntries(
  token: string,
  kind = '',
  path = '',
  options: FreshReadOptions = {},
): Promise<ApiResponse<DigestEntry[]>> {
  const params = new URLSearchParams()
  if (kind) params.set('kind', kind)
  if (path) params.set('path', path)
  const suffix = params.toString() ? `?${params}` : ''
  const r = await projectionJSON<{ entries: DigestEntry[] }>(
    () => fetch(`${BASE}/digest${suffix}`, { headers: readHeaders(token, options) }),
    options,
  )
  if (r.error) return { error: r.error }
  return { data: r.data?.entries ?? [] }
}

export async function listAnnouncements(
  token: string,
  path = '',
  options: FreshReadOptions = {},
): Promise<ApiResponse<DigestEntry[]>> {
  const params = new URLSearchParams()
  if (path) params.set('path', path)
  const suffix = params.toString() ? `?${params}` : ''
  const r = await projectionJSON<{ entries: DigestEntry[] }>(
    () => fetch(`${BASE}/announcements${suffix}`, { headers: readHeaders(token, options) }),
    options,
  )
  if (r.error) return { error: r.error }
  return { data: r.data?.entries ?? [] }
}

export async function searchDigestEntries(
  token: string,
  q: string,
  options: { board?: string; kind?: string; path?: string; limit?: number; offset?: number } & FreshReadOptions = {},
): Promise<ApiResponse<DigestEntry[]>> {
  const params = new URLSearchParams({ q })
  if (options.board) params.set('board', options.board)
  if (options.kind) params.set('kind', options.kind)
  if (options.path) params.set('path', options.path)
  if (options.limit) params.set('limit', String(options.limit))
  if (options.offset) params.set('offset', String(options.offset))
  const r = await projectionJSON<{ entries: DigestEntry[] }>(
    () => fetch(`${BASE}/digest/search?${params}`, { headers: readHeaders(token, options) }),
    options,
  )
  if (r.error) return { error: r.error }
  return { data: r.data?.entries ?? [] }
}

export async function listFavoriteBoards(token: string, options: FreshReadOptions = {}): Promise<ApiResponse<Board[]>> {
  const r = await projectionJSON<{ boards: Board[] }>(
    () => fetch(`${BASE}/boards/favorites`, { headers: readHeaders(token, options) }),
    options,
  )
  if (r.error) return { error: r.error }
  return { data: r.data?.boards ?? [] }
}

export async function listFavoriteTree(token: string, options: FreshReadOptions = {}): Promise<ApiResponse<FavoriteTree>> {
  return projectionJSON<FavoriteTree>(
    () => fetch(`${BASE}/boards/favorites/tree`, { headers: readHeaders(token, options) }),
    options,
  )
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
  const imported = await json<FavoriteTree | AckResult>(res)
  if (imported.error) return { error: imported.error }
  if (isPendingCommandAck(imported.data as AckResult)) {
    const resolved = await resolveCommandResult(token, imported.data as AckResult)
    if (resolved.error) return { error: resolved.error }
    return listFavoriteTree(token, { minSeq: resolved.data?.seq })
  }
  return { data: imported.data as FavoriteTree }
}

export async function listBoardSummaries(
  token: string,
  options: { q?: string; sort?: string; newOnly?: boolean; newDays?: number } & FreshReadOptions = {},
): Promise<ApiResponse<BoardSummary[]>> {
  const params = new URLSearchParams()
  if (options.q) params.set('q', options.q)
  if (options.sort) params.set('sort', options.sort)
  if (options.newOnly) params.set('new', '1')
  if (options.newDays) params.set('newDays', String(options.newDays))
  const suffix = params.toString() ? `?${params}` : ''
  const r = await projectionJSON<{ boards: BoardSummary[] }>(
    () => fetch(`${BASE}/boards/summary${suffix}`, { headers: readHeaders(token, options) }),
    options,
  )
  if (r.error) return { error: r.error }
  return { data: r.data?.boards ?? [] }
}

export async function listUnreadBoards(token: string, options: FreshReadOptions = {}): Promise<ApiResponse<BoardSummary[]>> {
  const r = await projectionJSON<{ boards: BoardSummary[] }>(
    () => fetch(`${BASE}/boards/unread`, { headers: readHeaders(token, options) }),
    options,
  )
  if (r.error) return { error: r.error }
  return { data: r.data?.boards ?? [] }
}

export async function createBoard(
  token: string,
  payload: { id: string; name: string; description?: string; parentId?: string; position?: number },
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/boards`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  return jsonResolvedAck(token, res)
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
  return jsonResolvedAck(token, res)
}

export async function setBoardZap(token: string, board: string, zapped: boolean): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/boards/${board}/zap`, {
    method: zapped ? 'PUT' : 'DELETE',
    headers: authHeaders(token),
  })
  return jsonResolvedAck(token, res)
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
  return jsonResolvedAck(token, res)
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
  return jsonResolvedAck(token, res)
}

export async function deleteFavoriteFolder(token: string, folder: string): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/boards/favorites/folders/${folder}`, {
    method: 'DELETE',
    headers: authHeaders(token),
  })
  return jsonResolvedAck(token, res)
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
  return jsonResolvedAck(token, res)
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
  return jsonResolvedAck(token, res)
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
  return jsonResolvedAck(token, res)
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
  return jsonResolvedAck(token, res)
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
    canManagePolls?: boolean
    canSetBoardSettings?: boolean
  } = {},
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/boards/${board}/members/${encodeURIComponent(user)}`, {
    method: member ? 'PUT' : 'DELETE',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: member ? JSON.stringify({ title, ...permissions }) : undefined,
  })
  return jsonResolvedAck(token, res)
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
  return jsonResolvedAck(token, res)
}

export async function listBoardMemberApplications(
  token: string,
  board: string,
  status = '',
  options: FreshReadOptions = {},
): Promise<ApiResponse<BoardMemberApplication[]>> {
  const params = new URLSearchParams()
  if (status) params.set('status', status)
  const suffix = params.toString() ? `?${params}` : ''
  const r = await projectionJSON<{ applications: BoardMemberApplication[] }>(
    () => fetch(`${BASE}/boards/${board}/member-applications${suffix}`, { headers: readHeaders(token, options) }),
    options,
  )
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
  return jsonResolvedAck(token, res)
}

export async function leaveBoardMembership(token: string, board: string): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/boards/${board}/members/leave`, {
    method: 'POST',
    headers: authHeaders(token),
  })
  return jsonResolvedAck(token, res)
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
  return jsonResolvedAck(token, res)
}

export async function uploadPostAttachment(token: string, post: string, file: File): Promise<ApiResponse<AckResult>> {
  const body = new FormData()
  body.append('file', file)
  const res = await fetch(`${BASE}/posts/${post}/attachments`, {
    method: 'POST',
    headers: authHeaders(token),
    body,
  })
  return jsonResolvedAck(token, res)
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
  return jsonResolvedAck(token, res)
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
  return jsonResolvedAck(token, res)
}

export async function removeDigestEntry(token: string, entry: string): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/digest/${entry}`, {
    method: 'DELETE',
    headers: authHeaders(token),
  })
  return jsonResolvedAck(token, res)
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
  return jsonResolvedAck(token, res)
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
  return jsonResolvedAck(token, res)
}

export async function resetDigestEntryBody(token: string, entry: string): Promise<ApiResponse<AckResult>> {
	const res = await fetch(`${BASE}/digest/${entry}/body`, {
		method: 'DELETE',
		headers: authHeaders(token),
	})
	return jsonResolvedAck(token, res)
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
  return jsonResolvedAck(token, res)
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
  return jsonResolvedAck(token, res)
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
  return jsonResolvedAck(token, res)
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
  return jsonResolvedAck(token, res)
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
  return jsonResolvedAck(token, res)
}

export async function mailPostAuthor(
  token: string,
  post: string,
  payload: { subject?: string; body: string; saveSent?: boolean },
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/posts/${post}/mail`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  return jsonResolvedAck(token, res)
}

export async function listMail(
  token: string,
  mailbox = 'inbox',
  unreadOnly = false,
  options: FreshReadOptions = {},
): Promise<ApiResponse<{ mail: MailItem[]; unreadCount: number }>> {
  const params = new URLSearchParams({ mailbox })
  if (unreadOnly) params.set('unread', '1')
  return projectionJSON<{ mail: MailItem[]; unreadCount: number }>(
    () => fetch(`${BASE}/mail?${params}`, { headers: readHeaders(token, options) }),
    options,
  )
}

export async function getMail(token: string, mail: string, options: FreshReadOptions = {}): Promise<ApiResponse<MailItem>> {
  return projectionJSON<MailItem>(
    () => fetch(`${BASE}/mail/${mail}`, { headers: readHeaders(token, options) }),
    options,
  )
}

export async function listMailThread(token: string, mail: string, options: FreshReadOptions = {}): Promise<ApiResponse<{ mail: MailItem[] }>> {
  return projectionJSON<{ mail: MailItem[] }>(
    () => fetch(`${BASE}/mail/thread/${mail}`, { headers: readHeaders(token, options) }),
    options,
  )
}

export async function listMailByAuthor(token: string, mail: string, options: FreshReadOptions = {}): Promise<ApiResponse<{ mail: MailItem[] }>> {
  return projectionJSON<{ mail: MailItem[] }>(
    () => fetch(`${BASE}/mail/author/${mail}`, { headers: readHeaders(token, options) }),
    options,
  )
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
  return jsonResolvedAck(token, res)
}

export async function forwardMail(
  token: string,
  mail: string,
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
  const res = await fetch(`${BASE}/mail/${mail}/forward`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  return jsonResolvedAck(token, res)
}

export async function postMailToBoard(
  token: string,
  mail: string,
  payload: { board?: string; thread?: string; subject?: string; note?: string },
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/mail/${mail}/board`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  return jsonResolvedAck(token, res)
}

export async function listMailGroups(token: string, options: FreshReadOptions = {}): Promise<ApiResponse<{ groups: MailGroup[] }>> {
  return projectionJSON<{ groups: MailGroup[] }>(
    () => fetch(`${BASE}/mail/groups`, { headers: readHeaders(token, options) }),
    options,
  )
}

export async function getMailUsage(token: string, options: FreshReadOptions = {}): Promise<ApiResponse<MailUsage>> {
  return projectionJSON<MailUsage>(
    () => fetch(`${BASE}/mail/usage`, { headers: readHeaders(token, options) }),
    options,
  )
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
  return jsonResolvedAck(token, res)
}

export async function deleteMailGroup(token: string, group: string): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/mail/groups/${encodeURIComponent(group)}`, {
    method: 'DELETE',
    headers: authHeaders(token),
  })
  return jsonResolvedAck(token, res)
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
  return jsonResolvedAck(token, res)
}

export async function deleteMail(token: string, mail: string): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/mail/${mail}`, {
    method: 'DELETE',
    headers: authHeaders(token),
  })
  return jsonResolvedAck(token, res)
}

export async function deleteMailRange(token: string, mail: string[]): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/mail/range-delete`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify({ mail }),
  })
  return jsonResolvedAck(token, res)
}

export async function listDirectConversations(
  token: string,
  options: FreshReadOptions = {},
): Promise<ApiResponse<{ conversations: DirectMessageConversation[]; unreadCount: number }>> {
  return projectionJSON<{ conversations: DirectMessageConversation[]; unreadCount: number }>(
    () => fetch(`${BASE}/messages`, { headers: readHeaders(token, options) }),
    options,
  )
}

export async function listDirectMessages(
  token: string,
  username: string,
  options: FreshReadOptions = {},
): Promise<ApiResponse<{ messages: DirectMessage[] }>> {
  return projectionJSON<{ messages: DirectMessage[] }>(
    () => fetch(`${BASE}/messages/${encodeURIComponent(username)}`, { headers: readHeaders(token, options) }),
    options,
  )
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
  return jsonResolvedAck(token, res)
}

export async function getDirectMessageSettings(token: string, options: FreshReadOptions = {}): Promise<ApiResponse<DirectMessageSettings>> {
  return projectionJSON<DirectMessageSettings>(
    () => fetch(`${BASE}/messages/settings`, { headers: readHeaders(token, options) }),
    options,
  )
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
  return jsonResolvedAck(token, res)
}

export async function markDirectMessageRead(token: string, message: string): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/messages/${message}/read`, {
    method: 'POST',
    headers: authHeaders(token),
  })
  return jsonResolvedAck(token, res)
}

export async function deleteDirectMessage(token: string, message: string): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/messages/${message}`, {
    method: 'DELETE',
    headers: authHeaders(token),
  })
  return jsonResolvedAck(token, res)
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

export async function listBoardOnlineUsers(token: string, board: string, options: FreshReadOptions = {}): Promise<ApiResponse<SocialUser[]>> {
  const r = await projectionJSON<{ users: SocialUser[] }>(
    () => fetch(`${BASE}/boards/${board}/online`, { headers: readHeaders(token, options) }),
    options,
  )
  if (r.error) return { error: r.error }
  return { data: r.data?.users ?? [] }
}

export async function listChatRooms(token: string): Promise<ApiResponse<ChatRoom[]>> {
  const res = await fetch(`${BASE}/chat/rooms`, { headers: authHeaders(token) })
  const r = await json<{ rooms: ChatRoom[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.rooms ?? [] }
}

export async function listChatRecent(token: string, room: string, limit = 50): Promise<ApiResponse<ChatLine[]>> {
  const q = new URLSearchParams({ limit: String(limit) })
  const res = await fetch(`${BASE}/chat/${encodeURIComponent(room)}/recent?${q}`, { headers: authHeaders(token) })
  const r = await json<{ lines: ChatLine[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.lines ?? [] }
}

export async function listChatOnlineUsers(token: string, room: string): Promise<ApiResponse<SocialUser[]>> {
  const res = await fetch(`${BASE}/chat/${encodeURIComponent(room)}/online`, { headers: authHeaders(token) })
  const r = await json<{ users: SocialUser[] }>(res)
  if (r.error) return { error: r.error }
  return { data: r.data?.users ?? [] }
}

export async function sendChatLine(token: string, room: string, text: string): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/chat/${encodeURIComponent(room)}/lines`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify({ text }),
  })
  return jsonResolvedAck(token, res)
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
  return jsonResolvedAck(token, res)
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
  return jsonResolvedAck(token, res)
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
  return jsonResolvedAck(token, res)
}

export async function markBoardRead(token: string, board: string): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/boards/${board}/read`, {
    method: 'POST',
    headers: authHeaders(token),
  })
  return jsonResolvedAck(token, res)
}

export async function restoreBoardRead(token: string, board: string): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/boards/${board}/read/restore`, {
    method: 'POST',
    headers: authHeaders(token),
  })
  return jsonResolvedAck(token, res)
}

export async function markFavoriteFolderRead(token: string, folder = ''): Promise<ApiResponse<AckResult>> {
  const path = folder ? `/boards/favorites/folders/${folder}/read` : '/boards/favorites/read'
  const res = await fetch(`${BASE}${path}`, {
    method: 'POST',
    headers: authHeaders(token),
  })
  return jsonResolvedAck(token, res)
}

export async function restoreFavoriteFolderRead(token: string, folder = ''): Promise<ApiResponse<AckResult>> {
  const path = folder ? `/boards/favorites/folders/${folder}/read/restore` : '/boards/favorites/read/restore'
  const res = await fetch(`${BASE}${path}`, {
    method: 'POST',
    headers: authHeaders(token),
  })
  return jsonResolvedAck(token, res)
}

export async function markThreadRead(token: string, thread: string): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/threads/${thread}/read`, {
    method: 'POST',
    headers: authHeaders(token),
  })
  return jsonResolvedAck(token, res)
}

export async function restoreThreadRead(token: string, thread: string): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/threads/${thread}/read/restore`, {
    method: 'POST',
    headers: authHeaders(token),
  })
  return jsonResolvedAck(token, res)
}

export async function markPostRead(token: string, post: string): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/posts/${post}/read`, {
    method: 'POST',
    headers: authHeaders(token),
  })
  return jsonResolvedAck(token, res)
}

export async function listThreads(
  token: string,
  board: string,
  limit = 30,
  offset = 0,
  unreadOnly = false,
  filters: { q?: string; author?: string } = {},
  options: FreshReadOptions = {},
): Promise<ApiResponse<ThreadSummary[]>> {
  const params = new URLSearchParams({ limit: String(limit), offset: String(offset) })
  if (unreadOnly) params.set('unread', '1')
  if (filters.q?.trim()) params.set('q', filters.q.trim())
  if (filters.author?.trim()) params.set('author', filters.author.trim())
  const r = await projectionJSON<{ threads: ThreadSummary[] }>(
    () => fetch(`${BASE}/boards/${board}/threads?${params}`, {
      headers: readHeaders(token, options),
    }),
    options,
  )
  if (r.error) return { error: r.error }
  return { data: r.data?.threads ?? [] }
}

export async function listUnreadThreads(
  token: string,
  limit = 50,
  offset = 0,
  favoritesOnly = false,
  folder = '',
  options: FreshReadOptions = {},
): Promise<ApiResponse<ThreadSummary[]>> {
  const params = new URLSearchParams({ limit: String(limit), offset: String(offset) })
  if (favoritesOnly) params.set('favorites', '1')
  if (folder) params.set('folder', folder)
  const r = await projectionJSON<{ threads: ThreadSummary[] }>(
    () => fetch(`${BASE}/threads/unread?${params}`, {
      headers: readHeaders(token, options),
    }),
    options,
  )
  if (r.error) return { error: r.error }
  return { data: r.data?.threads ?? [] }
}

export async function getThread(token: string, id: string, options: FreshReadOptions = {}): Promise<ApiResponse<{ thread: Thread; posts: Post[] }>> {
  return projectionJSON<{ thread: Thread; posts: Post[] }>(
    () => fetch(`${BASE}/threads/${id}`, { headers: readHeaders(token, options) }),
    options,
  )
}

export async function listPosts(token: string, thread: string, limit = 50, offset = 0, options: FreshReadOptions = {}): Promise<ApiResponse<Post[]>> {
  const r = await projectionJSON<{ posts: Post[] }>(
    () => fetch(`${BASE}/threads/${thread}/posts?limit=${limit}&offset=${offset}`, {
      headers: readHeaders(token, options),
    }),
    options,
  )
  if (r.error) return { error: r.error }
  return { data: r.data?.posts ?? [] }
}

export async function listReplyTree(token: string, post: string, limit = 50, offset = 0, options: FreshReadOptions = {}): Promise<ApiResponse<Post[]>> {
  const r = await projectionJSON<{ posts: Post[] }>(
    () => fetch(`${BASE}/posts/${post}/reply-tree?limit=${limit}&offset=${offset}`, {
      headers: readHeaders(token, options),
    }),
    options,
  )
  if (r.error) return { error: r.error }
  return { data: r.data?.posts ?? [] }
}

export async function listThreadPolls(
  token: string,
  thread: string,
  limit = 50,
  offset = 0,
  options: FreshReadOptions = {},
): Promise<ApiResponse<PollMap>> {
  const r = await projectionJSON<{ polls: PollMap }>(
    () => fetch(`${BASE}/threads/${thread}/polls?limit=${limit}&offset=${offset}`, {
      headers: readHeaders(token, options),
    }),
    options,
  )
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

export async function search(token: string, q: string, board?: string, options: FreshReadOptions = {}): Promise<ApiResponse<Post[]>> {
  const params = new URLSearchParams({ q, limit: '30' })
  if (board) params.set('board', board)
  const r = await projectionJSON<{ posts: Post[] }>(
    () => fetch(`${BASE}/search?${params}`, { headers: readHeaders(token, options) }),
    options,
  )
  if (r.error) return { error: r.error }
  return { data: r.data?.posts ?? [] }
}

// ── M8: Notifications ─────────────────────────────────────────────────────

export async function listNotifications(token: string, unreadOnly = false, options: FreshReadOptions = {}): Promise<ApiResponse<{ notifications: Notification[]; unreadCount: number }>> {
  const params = new URLSearchParams({ limit: '50' })
  if (unreadOnly) params.set('unread', '1')
  return projectionJSON<{ notifications: Notification[]; unreadCount: number }>(
    () => fetch(`${BASE}/notifications?${params}`, { headers: readHeaders(token, options) }),
    options,
  )
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

export async function deleteNotification(token: string, id: string): Promise<ApiResponse<unknown>> {
  const res = await fetch(`${BASE}/notifications/${id}`, {
    method: 'DELETE',
    headers: authHeaders(token),
  })
  return json<unknown>(res)
}

export async function clearNotifications(token: string, readOnly = false): Promise<ApiResponse<unknown>> {
  const params = readOnly ? '?read=1' : ''
  const res = await fetch(`${BASE}/notifications${params}`, {
    method: 'DELETE',
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
  return jsonResolvedAck(token, res)
}

export async function unreactPost(token: string, postId: string): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/posts/${postId}/react`, {
    method: 'DELETE',
    headers: authHeaders(token),
  })
  return jsonResolvedAck(token, res)
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
  return jsonResolvedAck(token, res)
}

export async function publishPollResult(token: string, pollId: string): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/polls/${pollId}/publish-result`, {
    method: 'POST',
    headers: authHeaders(token),
  })
  return jsonResolvedAck(token, res)
}

// ── M9: Trust ──────────────────────────────────────────────────────────────

export async function getTrust(token: string, username: string): Promise<ApiResponse<TrustInfo>> {
  const res = await fetch(`${BASE}/users/${encodeURIComponent(username)}/trust`, { headers: authHeaders(token) })
  return json<TrustInfo>(res)
}

export async function getUserProfile(token: string | null, username: string): Promise<ApiResponse<UserProfile>> {
  const res = await fetch(`${BASE}/users/${encodeURIComponent(username)}`, { headers: authHeaders(token) })
  const r = await json<UserProfile>(res)
  if (r.error) return { error: r.error }
  return { data: r.data ? { ...r.data, pubkeys: r.data.pubkeys ?? [] } : r.data }
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

export async function grantRole(
  token: string,
  username: string,
  role: 'trusted' | 'moderator' | 'admin',
): Promise<ApiResponse<AckResult>> {
  return execCommandResolved(token, 'grantRole', { user: username, role })
}

export async function revokeRole(
  token: string,
  username: string,
  role: 'trusted' | 'moderator' | 'admin',
): Promise<ApiResponse<AckResult>> {
  return execCommandResolved(token, 'revokeRole', { user: username, role })
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
  const res = await fetch(`${BASE}/users/${encodeURIComponent(username)}/posts?${params}`, { headers: authHeaders(token) })
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

export async function listResidentBoardPosts(
  token: string,
  limit = 30,
  offset = 0,
  options: FreshReadOptions = {},
): Promise<ApiResponse<Post[]>> {
  const params = new URLSearchParams({ limit: String(limit), offset: String(offset) })
  const r = await projectionJSON<{ posts: Post[] }>(
    () => fetch(`${BASE}/boards/resident-feed?${params}`, { headers: readHeaders(token, options) }),
    options,
  )
  if (r.error) return { error: r.error }
  return { data: r.data?.posts ?? [] }
}

export async function listLatestFeedPosts(
  token: string,
  limit = 30,
  offset = 0,
  options: FreshReadOptions = {},
): Promise<ApiResponse<Post[]>> {
  const params = new URLSearchParams({ limit: String(limit), offset: String(offset) })
  const r = await projectionJSON<{ posts: Post[] }>(
    () => fetch(`${BASE}/feed/latest?${params}`, { headers: readHeaders(token, options) }),
    options,
  )
  if (r.error) return { error: r.error }
  return { data: r.data?.posts ?? [] }
}

export async function updateMyProfile(
  token: string,
  payload: { displayName?: string; title?: string; bio?: string; avatar?: string; signature?: string; plan?: string; homepage?: string },
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
  const r = await json<UserSignatureBundle>(res)
  if (r.error) return { error: r.error }
  return { data: r.data ? { ...r.data, signatures: r.data.signatures ?? [] } : r.data }
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
  const r = await json<UserLoginACLBundle>(res)
  if (r.error) return { error: r.error }
  return { data: r.data ? { ...r.data, rules: r.data.rules ?? [] } : r.data }
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
  return jsonResolvedAck(token, res)
}

export async function setPostFlag(
  token: string,
  postId: string,
  payload: { marked?: boolean; recommended?: boolean; noReply?: boolean; tex?: boolean; mailBack?: boolean },
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/posts/${postId}/flags`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  return jsonResolvedAck(token, res)
}

export async function repostPost(
  token: string,
  postId: string,
  payload: { board: string; title?: string },
): Promise<ApiResponse<AckResult>> {
  const res = await fetch(`${BASE}/posts/${postId}/repost`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders(token) },
    body: JSON.stringify(payload),
  })
  return jsonResolvedAck(token, res)
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
  return jsonResolvedAck(token, res)
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
  return jsonResolvedAck(token, res)
}

export async function listUserSanctions(
  token: string,
  username: string,
  limit = 50,
  offset = 0,
): Promise<ApiResponse<{ sanctions: UserSanction[] }>> {
  const res = await fetch(
    `${BASE}/users/${username}/sanctions?limit=${limit}&offset=${offset}`,
    { headers: authHeaders(token) },
  )
  return json<{ sanctions: UserSanction[] }>(res)
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
  return jsonResolvedAck(token, res)
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
  return jsonResolvedAck(token, res)
}

// ── Commands ───────────────────────────────────────────────────────────────

export type CommandName =
  | 'createThread' | 'appendPost' | 'repostPost' | 'postBoardMail' | 'editPost' | 'setPostFlag' | 'redactPost' | 'restorePost'
  | 'setThreadTitle' | 'lockThread' | 'moveThread' | 'sanctionUser' | 'clearUserSanction' | 'setContentFilter' | 'grantRole' | 'revokeRole' | 'publishStatsSnapshot'
  | 'setBoardAutomodRule' | 'deleteBoardAutomodRule'
  | 'sendChatLine' | 'setPresence' | 'createBoard' | 'purgePost'
  | 'setBoardSettings' | 'setBoardMemberRequirements' | 'setBoardModerator'
  | 'setBoardMember' | 'applyBoardMembership' | 'reviewBoardMembership' | 'leaveBoardMembership'
  | 'curatePost' | 'curateThread' | 'removeDigestEntry' | 'updateDigestEntry' | 'setDigestEntryBody' | 'createDigestDirectory'
  | 'moveDigestPath' | 'copyDigestPath' | 'deleteDigestPath' | 'sendDigestEntryMail'
  | 'mailPostAuthor' | 'sendMail' | 'forwardMail' | 'postMailToBoard' | 'setMailGroup' | 'deleteMailGroup' | 'attachMail' | 'updateMail' | 'deleteMail' | 'deleteMailRange'
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
  return jsonAck(res)
}

export async function execCommandResolved(
  token: string,
  name: CommandName,
  payload: unknown,
  options: CommandResolveOptions = {},
  cid?: string,
): Promise<ApiResponse<AckResult>> {
  const queued = await execCommand(token, name, payload, cid)
  if (queued.error) return queued
  if (isPendingCommandAck(queued.data)) options.onQueued?.(queued.data)
  return resolveCommandResult(token, queued.data, options)
}
