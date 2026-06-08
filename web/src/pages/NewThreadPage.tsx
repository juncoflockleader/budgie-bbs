import { FormEvent, useEffect, useLayoutEffect, useMemo, useState } from 'react'
import * as api from '../api/client'
import type { AttachmentPayload, Board } from '../api/types'
import { AttachmentComposer } from '../components/AttachmentComposer'
import { Markup } from '../components/Markup'
import { PollComposer } from '../components/PollComposer'
import { hasComposeDraft, loadComposeDraft, removeComposeDraft, saveComposeDraft } from '../draftStorage'
import { validatePollMarkup } from '../pollValidation'
import { useI18n } from '../i18n'

interface Props {
  token: string
  board: Board
  currentUsername: string
  onCreated: (threadId: string) => void
  onBack: () => void
}

export function NewThreadPage({ token, board, currentUsername, onCreated, onBack }: Props) {
  const [title, setTitle] = useState('')
  const [body, setBody] = useState('')
  const [anonymous, setAnonymous] = useState(false)
  const [attachments, setAttachments] = useState<AttachmentPayload[]>([])
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [previewOpen, setPreviewOpen] = useState(false)
  const [fullScreen, setFullScreen] = useState(false)
  const [loadedDraftKey, setLoadedDraftKey] = useState('')
  const [isTrustLoaded, setIsTrustLoaded] = useState(false)
  const [canCreatePoll, setCanCreatePoll] = useState(false)
  const pollValidation = useMemo(() => validatePollMarkup(body), [body])
  const draftKey = useMemo(() => `budgie:compose:new:${currentUsername}:${board.id}`, [currentUsername, board.id])
  const hasDraft = hasComposeDraft({ title, body, anonymous, attachments })
  const { t } = useI18n()

  function appendPoll(markup: string) {
    setBody(prev => {
      const trimmed = prev.trimEnd()
      return trimmed ? `${trimmed}\n\n${markup}` : markup
    })
  }

  useEffect(() => {
    ;(async () => {
      setIsTrustLoaded(false)
      const trustRes = await api.getTrust(token, currentUsername)
      if (trustRes.data) {
        setCanCreatePoll(trustRes.data.trustLevel >= 2)
      } else {
        setCanCreatePoll(false)
      }
      setIsTrustLoaded(true)
    })()
  }, [token, currentUsername])

  useLayoutEffect(() => {
    const draft = loadComposeDraft(draftKey)
    setTitle(draft?.title ?? '')
    setBody(draft?.body ?? '')
    setAnonymous(board.anonymousAllowed ? (draft?.anonymous ?? false) : false)
    setAttachments(draft?.attachments ?? [])
    setError(null)
    setPreviewOpen(false)
    setFullScreen(false)
    setLoadedDraftKey(draftKey)
  }, [draftKey, board.anonymousAllowed])

  useLayoutEffect(() => {
    if (loadedDraftKey !== draftKey) return
    const draft = { title, body, anonymous, attachments, updatedAt: Date.now() }
    if (hasComposeDraft(draft)) {
      saveComposeDraft(draftKey, draft)
    } else {
      removeComposeDraft(draftKey)
    }
  }, [loadedDraftKey, draftKey, title, body, anonymous, attachments])

  function discardDraft() {
    setTitle('')
    setBody('')
    setAnonymous(false)
    setAttachments([])
    setPreviewOpen(false)
    removeComposeDraft(draftKey)
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    if (pollValidation.hasPollTag && !pollValidation.valid) {
      setError(pollValidation.message ?? t('error.pollSyntax'))
      setBusy(false)
      return
    }
    const res = await api.execCommand(token, 'createThread', {
      board: board.id,
      title,
      body,
      anonymous: board.anonymousAllowed ? anonymous : false,
      attachments: cleanAttachments(attachments),
    })
    setBusy(false)
    if (res.error) {
      setError(res.error.message)
    } else {
      removeComposeDraft(draftKey)
      onCreated(res.data?.id ?? '')
    }
  }

  return (
    <div className={`new-thread-page${fullScreen ? ' compose-fullscreen' : ''}`}>
      <div className="page-header">
        <button className="back-btn" onClick={onBack}>← {board.name}</button>
        <h2>{t('compose.createThread')}</h2>
      </div>
      <form className="new-thread-form" onSubmit={submit}>
        <label>
          {t('compose.title')}
          <input
            autoFocus
            value={title}
            onChange={e => setTitle(e.target.value)}
            required
            maxLength={200}
          />
        </label>
        <div className={previewOpen ? 'compose-layout' : undefined}>
          <label>
            {t('compose.body')}
            <textarea
              value={body}
              onChange={e => {
                setBody(e.target.value)
                if (error) setError(null)
              }}
              required
              rows={8}
              placeholder={t('compose.markdownHint')}
            />
          </label>
          {previewOpen && (
            <section className="compose-preview" aria-label={t('compose.preview')}>
              <Markup body={body} />
            </section>
          )}
        </div>
        {pollValidation.hasPollTag && !pollValidation.valid && (
          <p className="error">{pollValidation.message}</p>
        )}
        <div className="compose-actions">
          <PollComposer
            onInsert={appendPoll}
            disabled={!isTrustLoaded || !canCreatePoll}
            disabledHint={
              !isTrustLoaded
                ? t('compose.pollPermissionChecking')
                : (!canCreatePoll ? t('compose.pollPermissionRestricted') : undefined)
            }
          />
          <button type="button" className="link-btn" onClick={() => setPreviewOpen(open => !open)} disabled={!body.trim()}>
            {previewOpen ? t('compose.preview') : t('common.preview')}
          </button>
          <button type="button" className="link-btn" onClick={() => setFullScreen(open => !open)}>
            {fullScreen ? t('common.exit') : t('common.fullscreen')}
          </button>
          {hasDraft && <button type="button" className="link-btn danger" onClick={discardDraft}>{t('compose.discardDraft')}</button>}
          {board.anonymousAllowed && (
            <label className="inline-toggle">
              <input type="checkbox" checked={anonymous} onChange={e => setAnonymous(e.target.checked)} />
              {t('compose.anonymous')}
            </label>
          )}
        </div>
        {board.attachmentsAllowed && (
          <AttachmentComposer attachments={attachments} onChange={setAttachments} disabled={busy} />
        )}
        {error && <p className="error">{error}</p>}
      <div className="form-actions">
          <button type="submit" disabled={busy || !title.trim() || !body.trim()}>
            {busy ? '…' : t('compose.create')}
          </button>
          <button type="button" className="link-btn" onClick={onBack}>{t('common.cancel')}</button>
        </div>
      </form>
    </div>
  )
}

function cleanAttachments(items: AttachmentPayload[]) {
  return items
    .map(item => ({
      filename: item.filename.trim(),
      contentType: item.contentType?.trim() || undefined,
      sizeBytes: item.sizeBytes ?? 0,
      url: item.url?.trim() || undefined,
    }))
    .filter(item => item.filename)
}
