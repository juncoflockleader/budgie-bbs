import { useEffect, useState, useCallback, useRef } from 'react'
import * as api from '../api/client'
import type { Thread, Post, Poll, BudgieEvent } from '../api/types'
import type {
  PostAppendedPayload, PostEditedPayload, PostRedactedPayload, PostRestoredPayload,
  ThreadLockedPayload, PostReactedPayload, PostUnreactedPayload, PollVotedPayload,
} from '../api/types'
import { Markup } from '../components/Markup'
import { Spinner } from '../components/Spinner'
import { PollComposer } from '../components/PollComposer'
import { PollWidget } from '../components/PollWidget'
import { useStream } from '../hooks/useStream'

interface Props {
  token: string
  thread: Thread
  currentUserId: string
  currentUserRole: string
  currentUsername: string
  onBack: () => void
  onOpenProfile: (username: string) => void
}

interface ReactionState {
  count: number
  reacted: boolean
}

const TL_LABEL = ['TL0', 'TL1', 'TL2', 'TL3', 'TL4']

function hasPollBlock(body: string) {
  return body.includes('[poll]')
}

export function ThreadPage({ token, thread, currentUserId, currentUsername, currentUserRole, onBack, onOpenProfile }: Props) {
  const [posts, setPosts] = useState<Post[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [threadLocked, setThreadLocked] = useState(thread.locked)
  const [composing, setComposing] = useState(false)
  const [draftBody, setDraftBody] = useState('')
  const [replyTo, setReplyTo] = useState<string | undefined>(undefined)
  const [submitting, setSubmitting] = useState(false)
  // postId → reaction state
  const [reactions, setReactions] = useState<Record<string, ReactionState>>({})
  // postId → Poll (null means "loading", undefined means "not loaded")
  const [polls, setPolls] = useState<Record<string, Poll | null>>({})
  // authorName → trust level
  const [trustLevels, setTrustLevels] = useState<Record<string, number>>({})
  const [canCreatePoll, setCanCreatePoll] = useState(true)
  const bottomRef = useRef<HTMLDivElement>(null)
  const isMod = currentUserRole === 'moderator' || currentUserRole === 'admin'
  const isAdmin = currentUserRole === 'admin'

  // Fetch trust level for an author if not already loaded
  async function loadTrust(author: string) {
    if (trustLevels[author] !== undefined) return
    const res = await api.getTrust(token, author)
    if (res.data) {
      setTrustLevels(prev => ({ ...prev, [author]: res.data!.trustLevel }))
    }
  }

  async function loadCurrentUserTrust() {
    const trustRes = await api.getTrust(token, currentUsername)
    if (trustRes.data) {
      setCanCreatePoll(trustRes.data.trustLevel >= 2)
    }
  }

  // Fetch poll for a post if not already loading/loaded
  async function loadPollForPost(postId: string, body = '') {
    if (body && !hasPollBlock(body)) return
    setPolls(prev => {
      if (prev[postId] !== undefined) return prev // already loading or loaded
      return { ...prev, [postId]: null } // mark as loading
    })
    const res = await api.getPollByPost(token, postId)
    setPolls(prev => {
      if (res.data) return { ...prev, [postId]: res.data }
      // If 404, remove the loading marker
      const next = { ...prev }
      delete next[postId]
      return next
    })
  }

  useEffect(() => {
    ;(async () => {
      setLoading(true)
      const [postsRes, pollsRes] = await Promise.all([
        api.listPosts(token, thread.id),
        api.listThreadPolls(token, thread.id),
      ])
      setLoading(false)

      if (postsRes.error) {
        setError(postsRes.error.message)
        return
      }

      const loadedPosts = postsRes.data ?? []
      setPosts(loadedPosts)
      setTimeout(() => bottomRef.current?.scrollIntoView({ behavior: 'smooth' }), 50)

      if (pollsRes.data) {
        setPolls(pollsRes.data)
      }

      // Init reaction state (count=0 until server pushes updates)
      const rxMap: Record<string, ReactionState> = {}
      loadedPosts.forEach(p => { rxMap[p.id] = { count: p.reactionCount, reacted: false } })
      setReactions(rxMap)

      // Lazily load trust levels
      const uniqueAuthors = [...new Set(loadedPosts.map(p => p.author))]
      uniqueAuthors.forEach(a => loadTrust(a))
    })()
  }, [token, thread.id])

  useEffect(() => {
    loadCurrentUserTrust()
  }, [token, currentUsername])

  const onEvent = useCallback((evt: BudgieEvent) => {
    if (evt.event === 'post.appended') {
      const p = evt.payload as PostAppendedPayload
      if (p.thread !== thread.id) return
      const newPost: Post = {
        id: p.id, thread: p.thread, author: p.author,
        body: p.body, contentType: p.contentType,
        replyTo: p.replyTo, version: 1, redacted: false,
        reactionCount: 0,
        createdSeq: evt.seq ?? 0, updatedSeq: evt.seq ?? 0,
      }
      setPosts(prev => {
        if (prev.find(x => x.id === p.id)) return prev
        setTimeout(() => bottomRef.current?.scrollIntoView({ behavior: 'smooth' }), 50)
        return [...prev, newPost]
      })
      setReactions(prev => ({ ...prev, [p.id]: { count: 0, reacted: false } }))
      // Load trust + poll for the new post
      loadTrust(p.author)
      loadPollForPost(p.id, p.rawBody || p.body)
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
    } else if (evt.event === 'thread.locked') {
      const p = evt.payload as ThreadLockedPayload
      if (p.thread === thread.id) setThreadLocked(p.locked)
    } else if (evt.event === 'post.reacted') {
      const p = evt.payload as PostReactedPayload
      setReactions(prev => ({
        ...prev,
        [p.postId]: {
          count: p.reactionCount,
          reacted: p.user === currentUserId ? true : (prev[p.postId]?.reacted ?? false),
        },
      }))
    } else if (evt.event === 'post.unreacted') {
      const p = evt.payload as PostUnreactedPayload
      setReactions(prev => ({
        ...prev,
        [p.postId]: {
          count: p.reactionCount,
          reacted: p.user === currentUserId ? false : (prev[p.postId]?.reacted ?? false),
        },
      }))
    } else if (evt.event === 'poll.voted') {
      const p = evt.payload as PollVotedPayload
      setPolls(prev => {
        const entry = Object.entries(prev).find(([, poll]) => poll?.id === p.poll)
        if (!entry) return prev
        const [postId, poll] = entry
        if (!poll) return prev
        return {
          ...prev,
          [postId]: {
            ...poll,
            options: poll.options.map(o =>
              o.id === p.option ? { ...o, voteCount: o.voteCount + 1 } : o
            ),
          },
        }
      })
    }
  }, [thread.id, currentUserId])

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

  function insertPollIntoDraft(markup: string) {
    setDraftBody(prev => {
      const trimmed = prev.trimEnd()
      return trimmed ? `${trimmed}\n\n${markup}` : markup
    })
  }

  async function redactPost(postId: string) {
    if (!confirm('Redact this post?')) return
    const res = await api.execCommand(token, 'redactPost', { post: postId })
    if (res.error) alert(res.error.message)
  }

  async function restorePost(postId: string) {
    const res = await api.execCommand(token, 'restorePost', { post: postId })
    if (res.error) alert(res.error.message)
  }

  async function purgePost(postId: string) {
    const reason = prompt('Reason for GDPR purge (permanently removes post body):')
    if (reason === null) return
    const res = await api.execCommand(token, 'purgePost', { post: postId, reason })
    if (res.error) alert(res.error.message)
  }

  async function toggleLock() {
    const res = await api.execCommand(token, 'lockThread', { thread: thread.id, locked: !threadLocked })
    if (res.error) alert(res.error.message)
  }

  async function toggleReact(postId: string) {
    const state = reactions[postId]
    if (state?.reacted) {
      setReactions(prev => ({
        ...prev,
        [postId]: { count: Math.max(0, (prev[postId]?.count ?? 1) - 1), reacted: false },
      }))
      const res = await api.unreactPost(token, postId)
      if (res.error) {
        setReactions(prev => ({
          ...prev,
          [postId]: { count: (prev[postId]?.count ?? 0) + 1, reacted: true },
        }))
        alert(res.error.message)
      }
    } else {
      setReactions(prev => ({
        ...prev,
        [postId]: { count: (prev[postId]?.count ?? 0) + 1, reacted: true },
      }))
      const res = await api.reactPost(token, postId)
      if (res.error) {
        setReactions(prev => ({
          ...prev,
          [postId]: { count: Math.max(0, (prev[postId]?.count ?? 1) - 1), reacted: false },
        }))
        alert(res.error.message)
      }
    }
  }

  async function handleVotePoll(postId: string, pollId: string, optionId: string) {
    const res = await api.votePoll(token, pollId, optionId)
    if (res.error) { alert(res.error.message); return }
    // Refresh from server to get accurate counts
    const pollRes = await api.getPoll(token, pollId)
    if (pollRes.data) {
      setPolls(prev => ({ ...prev, [postId]: pollRes.data! }))
    }
  }

  if (loading) return <Spinner />
  if (error) return <p className="error">{error}</p>

  const replyToPost = replyTo ? posts.find(p => p.id === replyTo) : undefined

  return (
    <div className="thread-page">
      <div className="page-header">
        <button className="back-btn" onClick={onBack}>← Threads</button>
        <h2 className="thread-title">{thread.title}</h2>
        {threadLocked && <span className="locked-badge">🔒 Locked</span>}
        {isMod && (
          <button className="link-btn" onClick={toggleLock}>
            {threadLocked ? '🔓 Unlock' : '🔒 Lock'}
          </button>
        )}
      </div>

      <div className="post-list">
        {posts.map(post => {
          const rx = reactions[post.id] ?? { count: 0, reacted: false }
          const poll = polls[post.id] // null = loading, Poll = loaded, undefined = none
          const tl = trustLevels[post.author]
          const createdAt = post.createdAt ?? post.createdSeq

          return (
            <div key={post.id} className="post-card">
              <div className="post-meta">
                <button className="post-author post-author-link" onClick={() => onOpenProfile(post.author)}>
                  {post.author}
                </button>
                {tl !== undefined && (
                  <span className={`trust-badge trust-badge--tl${tl}`} title={`Trust level ${tl}`}>
                    {TL_LABEL[tl] ?? `TL${tl}`}
                  </span>
                )}
                <span className="muted post-time">
                  {createdAt > 1_000_000_000_000 ? new Date(createdAt).toLocaleString() : `#${post.createdSeq}`}
                </span>
                <span className="post-actions">
                  <button
                    className={`link-btn react-btn${rx.reacted ? ' react-btn--active' : ''}`}
                    onClick={() => toggleReact(post.id)}
                    title={rx.reacted ? 'Remove heart' : 'Heart this post'}
                  >
                    {rx.reacted ? '❤️' : '🤍'}{rx.count > 0 ? ` ${rx.count}` : ''}
                  </button>
                  {!threadLocked && !post.redacted && (
                    <button className="link-btn" onClick={() => {
                      setReplyTo(post.id)
                      setComposing(true)
                    }}>Reply</button>
                  )}
                  {(isMod || post.authorId === currentUserId) && !post.redacted && (
                    <button className="link-btn danger" onClick={() => redactPost(post.id)}>Redact</button>
                  )}
                  {isMod && post.redacted && (
                    <button className="link-btn" onClick={() => restorePost(post.id)}>Restore</button>
                  )}
                  {isAdmin && post.redacted && (
                    <button className="link-btn danger" title="GDPR purge — permanently removes body" onClick={() => purgePost(post.id)}>Purge</button>
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

              {poll && (
                <PollWidget
                  poll={poll}
                  onVote={optionId => handleVotePoll(post.id, poll.id, optionId)}
                />
              )}
            </div>
          )
        })}
        <div ref={bottomRef} />
      </div>

      {!threadLocked && (
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
              rows={6}
              onKeyDown={e => {
                if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) submitPost()
              }}
            />
            <div className="compose-actions">
              <PollComposer
                onInsert={insertPollIntoDraft}
                disabled={!canCreatePoll}
                disabledHint={!canCreatePoll ? 'Polls require trust level 2+' : undefined}
              />
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
