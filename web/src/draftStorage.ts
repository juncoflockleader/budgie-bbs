import type { AttachmentPayload } from './api/types'

export interface ComposeDraft {
  title: string
  body: string
  anonymous: boolean
  replyTo?: string
  attachments: AttachmentPayload[]
  updatedAt: number
}

export function loadComposeDraft(key: string): ComposeDraft | null {
  if (typeof window === 'undefined') return null
  try {
    const raw = window.localStorage.getItem(key)
    if (!raw) return null
    const parsed = JSON.parse(raw) as Record<string, unknown>
    return {
      title: typeof parsed.title === 'string' ? parsed.title : '',
      body: typeof parsed.body === 'string' ? parsed.body : '',
      anonymous: parsed.anonymous === true,
      replyTo: typeof parsed.replyTo === 'string' && parsed.replyTo ? parsed.replyTo : undefined,
      attachments: Array.isArray(parsed.attachments)
        ? parsed.attachments.map(normalizeAttachment).filter((item): item is AttachmentPayload => item !== null)
        : [],
      updatedAt: typeof parsed.updatedAt === 'number' ? parsed.updatedAt : 0,
    }
  } catch {
    return null
  }
}

export function saveComposeDraft(key: string, draft: ComposeDraft) {
  if (typeof window === 'undefined') return
  window.localStorage.setItem(key, JSON.stringify(draft))
}

export function removeComposeDraft(key: string) {
  if (typeof window === 'undefined') return
  window.localStorage.removeItem(key)
}

export function hasComposeDraft(draft: Pick<ComposeDraft, 'title' | 'body' | 'anonymous' | 'attachments' | 'replyTo'>) {
  return Boolean(
    draft.title.trim() ||
    draft.body.trim() ||
    draft.anonymous ||
    draft.replyTo ||
    draft.attachments.length > 0,
  )
}

function normalizeAttachment(raw: unknown): AttachmentPayload | null {
  if (!raw || typeof raw !== 'object') return null
  const item = raw as Record<string, unknown>
  const filename = typeof item.filename === 'string' ? item.filename : ''
  if (!filename.trim()) return null
  return {
    filename,
    contentType: typeof item.contentType === 'string' ? item.contentType : undefined,
    sizeBytes: typeof item.sizeBytes === 'number' ? item.sizeBytes : undefined,
    url: typeof item.url === 'string' ? item.url : undefined,
  }
}
