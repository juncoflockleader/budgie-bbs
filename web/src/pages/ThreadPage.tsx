import { useEffect, useState, useCallback, useRef } from 'react'
import * as api from '../api/client'
import type { Thread, Post, BudgieEvent } from '../api/types'
import type { PostAppendedPayload, PostEditedPayload, PostRedactedPayload, PostRestoredPayload } from '../api/types'
import { Markup } from '../components/Markup'
import { Spinner } from '../components/Spinner'
import { useStream } from '../hooks/useStream'

interface Props {
  token: string
  thread: Thread
  currentUserId: string
  currentUserRole: string
  onBack: () => void
}

export function ThreadPage({ token, thread, currentUserId, currentUserRole, onBack }: Props) {
  const [posts, setPosts] = useState<Post[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [composing, setComposing] = useState(false)
  const [draftBody, setDraftBody] = useState('')
  const [replyTo, setReplyTo] = useState<string | undefined>(undefined)
  const [submitting, setSubmitting] = useState(false)
  const bottomRef = useRef<HTMLDivElement>(null)
  const isMod = currentUserRole === 'moderator' || currentUserRole === 'admin'

  useEffect(() => {
    setLoading(true)
    api.listPosts(token, thread.id).then(res => {
      setLoading(false)
      if (res.error) setError(res.error.message)
      else {
        setPosts(res.data ?? [])
        setTimeout(() => bottomRef.current?.scrollIntoView({ behavior: 'smooth' }), 50)
      }
    })
  }, [token, thread.id])

  const onEvent = useCallback((evt: BudgieEvent) => {
    if (evt.event === 'post.appended') {
      const p = evt.payload as PostAppendedPayload
      if (p.thread !== thread.id) return
      setPosts(prev => {
        if (prev.find(x => x.id === p.id)) return prev
        const next = [...prev, {
          id: p.id, thread: p.thread, author: p.author,
          body: p.body, contentType: p.contentType,
          replyTo: p.replyTo, version: 1, redacted: false,
          createdSeq: evt.seq ?? 0, updatedSeq: evt.seq ?? 0,
        }]
        setTimeout(() => bottomRef.current?.scrollIntoView({ behavior: 'smooth' }), 50)
        return next
      })
    } else if (evt.event === 'post.edited') {
      const p = evt.payload as PostEditedPayload
      setPosts(prev => prev.map(post =>
        post.id === p.id ? { ...post, body: p.newBody, version: p.version } : post
      ))
    } else if (evt.event === 'post.redacted') {
      const p = evt.payload as PostRedactedPayload
      setPosts(prev => prev.map(post =>
        post.id === p.id ? { ...post, redacted: true } : post
      ))
    } else if (evt.event === 'post.restored') {
      const p = evt.payload as PostRestoredPayload
      setPosts(prev => prev.map(post =>
        post.id === p.id ? { ...post, redacted: false } : post
      ))
    }
  }, [thread.id])

  useStream({ token }, onEvent)

  async function submitPost() {
    if (!draftBody.trim()) return
    setSubmitting(true)
    const res = await api.execCommand(token, 'appendPost', {
      thread: thread.id,
      body: draftBody,
      replyTo,
    })
    setSubmitting(false)
    if (res.error) {
      alert(res.error.message)
    } else {
      setDraftBody('')
      setReplyTo(undefined)
      setComposing(false)
    }
  }

  async function redactPost(postId: string) {
    if (!confirm('Redact this post?')) return
    const res = await api.execCommand(token, 'redactPost', { post: postId })
    if (res.error) alert(res.error.message)
  }

  if (loading) return <Spinner />
  if (error) return <p className="error">{error}</p>

  const replyToPost = replyTo ? posts.find(p => p.id === replyTo) : undefined

  return (
    <div className="thread-page">
      <div className="page-header">
        <button className="back-btn" onClick={onBack}>← Threads</button>
        <h2 className="thread-title">{thread.title}</h2>
        {thread.locked && <span className="locked-badge">🔒 Locked</span>}
      </div>

      <div className="post-list">
        {posts.map(post => (
          <div key={post.id} className="post-card">
            <div className="post-meta">
              <span className="post-author">{post.author}</span>
              <span className="muted post-time">#{post.createdSeq}</span>
              <span className="post-actions">
                {!thread.locked && !post.redacted && (
                  <button className="link-btn" onClick={() => {
                    setReplyTo(post.id)
                    setComposing(true)
                  }}>Reply</button>
                )}
                {(isMod || post.author === currentUserId) && !post.redacted && (
                  <button className="link-btn danger" onClick={() => redactPost(post.id)}>Redact</button>
                )}
              </span>
            </div>
            {post.replyTo && (
              <div className="post-reply-context muted">
                ↩ replying to #{posts.find(p => p.id === post.replyTo)?.createdSeq ?? post.replyTo}
              </div>
            )}
            <div className="post-body">
              <Markup body={post.body} redacted={post.redacted} />
            </div>
          </div>
        ))}
        <div ref={bottomRef} />
      </div>

      {!thread.locked && (
        composing ? (
          <div className="compose-box">
            {replyToPost && (
              <div className="compose-reply-to muted">
                Replying to {replyToPost.author}:
                <button className="link-btn" onClick={() => setReplyTo(undefined)}>× clear</button>
              </div>
            )}
            <textarea
              autoFocus
              className="compose-textarea"
              value={draftBody}
              onChange={e => setDraftBody(e.target.value)}
              placeholder="Write your reply…"
              rows={5}
              onKeyDown={e => {
                if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) submitPost()
              }}
            />
            <div className="compose-actions">
              <button onClick={submitPost} disabled={submitting || !draftBody.trim()}>
                {submitting ? '…' : 'Post reply'}
              </button>
              <button className="link-btn" onClick={() => { setComposing(false); setDraftBody(''); setReplyTo(undefined) }}>
                Cancel
              </button>
              <span className="muted compose-hint">Ctrl+Enter to submit</span>
            </div>
          </div>
        ) : (
          <div className="compose-trigger">
            <button onClick={() => setComposing(true)}>Write a reply…</button>
          </div>
        )
      )}
    </div>
  )
}
