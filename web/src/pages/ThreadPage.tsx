import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import * as api from '../api/client'
import type { AttachmentPayload, BoardInfo, Thread, ThreadSummary, Post, Poll, BudgieEvent, PostAttachment } from '../api/types'
import type {
  PostAppendedPayload, PostAttachmentAddedPayload, PostEditedPayload, PostFlagsSetPayload, PostRedactedPayload, PostRestoredPayload,
  ThreadTitleSetPayload, ThreadLockedPayload, PostReactedPayload, PostUnreactedPayload, PollVotedPayload,
} from '../api/types'
import { Markup } from '../components/Markup'
import { Spinner } from '../components/Spinner'
import { PollComposer } from '../components/PollComposer'
import { AttachmentComposer } from '../components/AttachmentComposer'
import { PollWidget } from '../components/PollWidget'
import { validatePollMarkup } from '../pollValidation'
import { useStream } from '../hooks/useStream'

interface Props {
  token: string
  thread: Thread & Partial<Pick<ThreadSummary, 'readSeq' | 'unreadPosts' | 'firstUnreadPostId'>>
  currentUserId: string
  currentUserRole: string
  currentUsername: string
  initialPostId?: string
  onBack: () => void
  onOpenThread: (thread: ThreadSummary, initialPostId?: string) => void
  onOpenProfile: (username: string) => void
  onOpenAuthorPosts: (username: string) => void
}

interface ReactionState {
  count: number
  reacted: boolean
}

const TL_LABEL = ['TL0', 'TL1', 'TL2', 'TL3', 'TL4']

function formatBytes(size = 0) {
  if (size <= 0) return ''
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`
  return `${(size / (1024 * 1024)).toFixed(1)} MB`
}

function hasPollBlock(body: string) {
  return body.toLowerCase().includes('[poll')
}

function formatQuotePrefix(post: Post) {
  const author = post.author?.trim() || 'Unknown'
  const body = (post.body || '[empty article]').trim()
  const lines = body.split('\n')
  const quoted: string[] = [`> ${author} wrote:`]
  let bytes = quoted[0].length
  for (let i = 0; i < lines.length; i += 1) {
    const line = lines[i].replace(/\r$/, '')
    if (i >= 24 || bytes + line.length > 2400) {
      quoted.push('> ...')
      break
    }
    quoted.push(line ? `> ${line}` : '>')
    bytes += line.length + 3
  }
  return `${quoted.join('\n')}\n\n`
}

function prependQuoteToDraft(post: Post, draft: string) {
  const quote = formatQuotePrefix(post)
  if (!draft.trim()) return quote
  if (draft.trimStart().startsWith(quote.trim())) return draft
  return `${quote}${draft}`
}

function promptDigestPayload(defaultTitle: string, defaultKind = 'digest') {
  const kind = prompt('Digest kind:', defaultKind)
  if (kind === null) return null
  const title = prompt('Digest title:', defaultTitle)
  if (title === null) return null
  const path = prompt('Archive path:', '')
  if (path === null) return null
  const note = prompt('Note:', '')
  if (note === null) return null
  return {
    kind: kind.trim() || 'digest',
    title: title.trim() || defaultTitle,
    path: path.trim(),
    note: note.trim(),
  }
}

export function ThreadPage({
  token,
  thread,
  currentUserId,
  currentUsername,
  currentUserRole,
  initialPostId,
  onBack,
  onOpenThread,
  onOpenProfile,
  onOpenAuthorPosts,
}: Props) {
  const [posts, setPosts] = useState<Post[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [threadTitle, setThreadTitle] = useState(thread.title)
  const [threadLocked, setThreadLocked] = useState(thread.locked)
  const [composing, setComposing] = useState(false)
  const [draftBody, setDraftBody] = useState('')
  const [draftAttachments, setDraftAttachments] = useState<AttachmentPayload[]>([])
  const [replyAnonymous, setReplyAnonymous] = useState(false)
  const [replyTo, setReplyTo] = useState<string | undefined>(undefined)
  const [submitting, setSubmitting] = useState(false)
  const [composeError, setComposeError] = useState<string | null>(null)
  // postId → reaction state
  const [reactions, setReactions] = useState<Record<string, ReactionState>>({})
  // postId → Poll (null means "loading", undefined means "not loaded")
  const [polls, setPolls] = useState<Record<string, Poll | null>>({})
  // authorName → trust level
  const [trustLevels, setTrustLevels] = useState<Record<string, number>>({})
  const [canCreatePoll, setCanCreatePoll] = useState(false)
  const [isTrustLoaded, setIsTrustLoaded] = useState(false)
  const [readSeq, setReadSeq] = useState(thread.readSeq ?? 0)
  const [focusedPostId, setFocusedPostId] = useState<string | undefined>(initialPostId)
  const [authorFocus, setAuthorFocus] = useState<string | undefined>(undefined)
  const [replyTreeRoot, setReplyTreeRoot] = useState<string | undefined>(undefined)
  const [replyTreePosts, setReplyTreePosts] = useState<Post[] | null>(null)
  const [replyTreeLoading, setReplyTreeLoading] = useState(false)
  const [boardUnreadThreads, setBoardUnreadThreads] = useState<ThreadSummary[]>([])
  const [boardInfo, setBoardInfo] = useState<BoardInfo | null>(null)
  const bottomRef = useRef<HTMLDivElement>(null)
  const postRefs = useRef<Record<string, HTMLDivElement | null>>({})
  const isAdmin = currentUserRole === 'admin'
  const draftPollValidation = useMemo(() => validatePollMarkup(draftBody), [draftBody])

  useEffect(() => {
    setThreadTitle(thread.title)
  }, [thread.id, thread.title])

  // Fetch trust level for an author if not already loaded
  async function loadTrust(author: string) {
    if (trustLevels[author] !== undefined) return
    const res = await api.getTrust(token, author)
    if (res.data) {
      setTrustLevels(prev => ({ ...prev, [author]: res.data!.trustLevel }))
    }
  }

  async function loadCurrentUserTrust() {
    setIsTrustLoaded(false)
    const trustRes = await api.getTrust(token, currentUsername)
    if (trustRes.data) {
      setCanCreatePoll(trustRes.data.trustLevel >= 2)
    } else {
      setCanCreatePoll(false)
    }
    setIsTrustLoaded(true)
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

  const refreshPoll = useCallback(async (pollId: string) => {
    const pollRes = await api.getPoll(token, pollId)
    if (!pollRes.data) return

    setPolls(prev => {
      for (const [postId, poll] of Object.entries(prev)) {
        if (poll?.id === pollId) {
          return { ...prev, [postId]: pollRes.data! }
        }
      }
      return prev
    })
  }, [token])

  const refreshUnreadThreads = useCallback(async () => {
    const res = await api.listThreads(token, thread.board, 100, 0, true)
    if (res.error) {
      setError(res.error.message)
      return []
    }
    const summaries = res.data ?? []
    setBoardUnreadThreads(summaries)
    return summaries
  }, [token, thread.board])

  useEffect(() => {
    setReadSeq(thread.readSeq ?? 0)
    setFocusedPostId(initialPostId)
    setAuthorFocus(undefined)
    setReplyTreeRoot(undefined)
    setReplyTreePosts(null)
    setReplyTreeLoading(false)
    void refreshUnreadThreads()
    ;(async () => {
      setLoading(true)
      const [postsRes, pollsRes] = await Promise.all([
        api.listPosts(token, thread.id),
        api.listThreadPolls(token, thread.id),
      ])
      const boardRes = await api.getBoardInfo(token, thread.board)
      setLoading(false)

      if (postsRes.error) {
        setError(postsRes.error.message)
        return
      }

      const loadedPosts = postsRes.data ?? []
      setPosts(loadedPosts)
      if (boardRes.data) setBoardInfo(boardRes.data)
      setTimeout(() => {
        if (initialPostId && postRefs.current[initialPostId]) {
          postRefs.current[initialPostId]?.scrollIntoView({ behavior: 'smooth', block: 'start' })
        } else {
          bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
        }
      }, 50)
      if (pollsRes.data) {
        setPolls(pollsRes.data)
      }

      // Init reaction state (count=0 until server pushes updates)
      const rxMap: Record<string, ReactionState> = {}
      loadedPosts.forEach(p => { rxMap[p.id] = { count: p.reactionCount, reacted: false } })
      setReactions(rxMap)

      // Lazily load trust levels
      const uniqueAuthors = [...new Set(loadedPosts.map(p => p.author).filter(author => author !== 'Anonymous'))]
      uniqueAuthors.forEach(a => loadTrust(a))
    })()
  }, [token, thread.id, initialPostId, refreshUnreadThreads])

  useEffect(() => {
    void api.setPresence(token, {
      status: `reading:thread:${thread.id}`,
      mode: 'reading',
      board: thread.board,
      thread: thread.id,
      location: threadTitle,
    })
  }, [token, thread.id, thread.board, threadTitle])

  useEffect(() => {
    loadCurrentUserTrust()
  }, [token, currentUsername])

  const onEvent = useCallback((evt: BudgieEvent) => {
    if (evt.event === 'post.appended') {
      const p = evt.payload as PostAppendedPayload
      if (p.thread !== thread.id) return
      const newPost: Post = {
        id: p.id, thread: p.thread, author: p.author,
        body: p.body, signature: p.signature, contentType: p.contentType,
        replyTo: p.replyTo, version: 1, redacted: false,
        reactionCount: 0,
        marked: false,
        recommended: false,
        noReply: false,
        tex: Boolean(p.tex),
        mailBack: Boolean(p.mailBack),
        sourcePost: p.sourcePost,
        sourceThread: p.sourceThread,
        sourceBoard: p.sourceBoard,
        sourceAuthor: p.sourceAuthor,
        sourceAuthorId: p.sourceAuthorId,
        sourceTitle: p.sourceTitle,
        attachments: eventAttachments(p),
        createdSeq: evt.seq ?? 0, updatedSeq: evt.seq ?? 0,
      }
      setPosts(prev => {
        if (prev.find(x => x.id === p.id)) return prev
        setTimeout(() => bottomRef.current?.scrollIntoView({ behavior: 'smooth' }), 50)
        return [...prev, newPost]
      })
      setReplyTreePosts(prev => {
        if (!prev) return prev
        const parent = prev.find(post => post.id === p.replyTo)
        if (!parent) return prev
        return [...prev, { ...newPost, replyDepth: (parent.replyDepth ?? 0) + 1 }]
      })
      setReactions(prev => ({ ...prev, [p.id]: { count: 0, reacted: false } }))
      // Load trust + poll for the new post
      if (p.author !== 'Anonymous') loadTrust(p.author)
      loadPollForPost(p.id, p.rawBody || p.body)
    } else if (evt.event === 'post.attachment_added') {
      const p = evt.payload as PostAttachmentAddedPayload
      if (p.thread !== thread.id) return
      setPosts(prev => prev.map(post => post.id === p.post ? {
        ...post,
        attachments: [...(post.attachments ?? []), {
          id: p.id,
          postId: p.post,
          filename: p.filename,
          contentType: p.contentType,
          sizeBytes: p.sizeBytes,
          stored: true,
          createdBy: p.authorId,
          createdAt: p.ts,
        }],
      } : post))
      setReplyTreePosts(prev => prev?.map(post => post.id === p.post ? {
        ...post,
        attachments: [...(post.attachments ?? []), {
          id: p.id,
          postId: p.post,
          filename: p.filename,
          contentType: p.contentType,
          sizeBytes: p.sizeBytes,
          stored: true,
          createdBy: p.authorId,
          createdAt: p.ts,
        }],
      } : post) ?? prev)
    } else if (evt.event === 'post.edited') {
      const p = evt.payload as PostEditedPayload
      setPosts(prev => prev.map(post =>
        post.id === p.id ? { ...post, body: p.newBody, version: p.version } : post
      ))
      setReplyTreePosts(prev => prev?.map(post =>
        post.id === p.id ? { ...post, body: p.newBody, version: p.version } : post
      ) ?? prev)
    } else if (evt.event === 'post.flags_set') {
      const p = evt.payload as PostFlagsSetPayload
      setPosts(prev => prev.map(post =>
        post.id === p.id ? { ...post, marked: p.marked, recommended: p.recommended, noReply: p.noReply, tex: Boolean(p.tex), mailBack: Boolean(p.mailBack) } : post
      ))
      setReplyTreePosts(prev => prev?.map(post =>
        post.id === p.id ? { ...post, marked: p.marked, recommended: p.recommended, noReply: p.noReply, tex: Boolean(p.tex), mailBack: Boolean(p.mailBack) } : post
      ) ?? prev)
    } else if (evt.event === 'post.redacted') {
      const p = evt.payload as PostRedactedPayload
      setPosts(prev => prev.map(post =>
        post.id === p.id ? { ...post, redacted: true } : post
      ))
      setReplyTreePosts(prev => prev?.map(post =>
        post.id === p.id ? { ...post, redacted: true } : post
      ) ?? prev)
    } else if (evt.event === 'post.restored') {
      const p = evt.payload as PostRestoredPayload
      setPosts(prev => prev.map(post =>
        post.id === p.id ? { ...post, redacted: false } : post
      ))
      setReplyTreePosts(prev => prev?.map(post =>
        post.id === p.id ? { ...post, redacted: false } : post
      ) ?? prev)
    } else if (evt.event === 'thread.title_set') {
      const p = evt.payload as ThreadTitleSetPayload
      if (p.thread === thread.id) setThreadTitle(p.title)
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
      void refreshPoll(p.poll)
    }
  }, [thread.id, currentUserId, refreshPoll])

  useStream({ token }, onEvent)

  async function submitPost() {
    if (!draftBody.trim()) return
    setSubmitting(true)
    setComposeError(null)
    if (draftPollValidation.hasPollTag && !draftPollValidation.valid) {
      setComposeError(draftPollValidation.message ?? 'Poll syntax is invalid')
      setSubmitting(false)
      return
    }
    const res = await api.execCommand(token, 'appendPost', {
      thread: thread.id,
      body: draftBody,
      replyTo,
      anonymous: replyAnonymous,
      attachments: cleanAttachments(draftAttachments),
    })
    setSubmitting(false)
    if (res.error) {
      setComposeError(res.error.message)
      alert(res.error.message)
    } else {
      setDraftBody('')
      setDraftAttachments([])
      setReplyAnonymous(false)
      setReplyTo(undefined)
      setComposing(false)
      setComposeError(null)
    }
  }

  function startReply(post: Post, quoted = false) {
    setReplyTo(post.id)
    if (quoted) {
      setDraftBody(prev => prependQuoteToDraft(post, prev))
    }
    setComposing(true)
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

  async function renameThreadTitle() {
    const next = prompt('Thread title:', threadTitle)
    if (next === null) return
    const title = next.trim()
    if (!title || title === threadTitle) return
    const previous = threadTitle
    setThreadTitle(title)
    const res = await api.execCommand(token, 'setThreadTitle', { thread: thread.id, title })
    if (res.error) {
      setThreadTitle(previous)
      alert(res.error.message)
    }
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

  async function handleVotePoll(pollId: string, optionId: string) {
    const res = await api.votePoll(token, pollId, optionId)
    if (res.error) {
      alert(res.error.message)
      return
    }
    void refreshPoll(pollId)
  }

  async function handlePublishPollResult(pollId: string) {
    const res = await api.publishPollResult(token, pollId)
    if (res.error) {
      alert(res.error.message)
      return
    }
    alert('Poll result published.')
  }

  function patchPostState(postId: string, patch: Partial<Post>) {
    setPosts(prev => prev.map(post => post.id === postId ? { ...post, ...patch } : post))
    setReplyTreePosts(prev => prev?.map(post => post.id === postId ? { ...post, ...patch } : post) ?? prev)
  }

  async function setArticleFlag(post: Post, patch: { marked?: boolean; recommended?: boolean; noReply?: boolean; tex?: boolean; mailBack?: boolean }) {
    const res = await api.setPostFlag(token, post.id, patch)
    if (res.error) {
      alert(res.error.message)
      return
    }
    patchPostState(post.id, patch)
  }

  if (loading) return <Spinner />
  if (error) return <p className="error">{error}</p>

  const displayedPosts = replyTreePosts ?? posts
  const replyTreeRootPost = replyTreeRoot ? (posts.find(p => p.id === replyTreeRoot) ?? replyTreePosts?.find(p => p.id === replyTreeRoot)) : undefined
  const replyToPost = replyTo ? (posts.find(p => p.id === replyTo) ?? replyTreePosts?.find(p => p.id === replyTo)) : undefined
  const unreadPosts = displayedPosts.filter(post => !post.redacted && post.createdSeq > readSeq)
  const focusedUnreadIndex = focusedPostId ? unreadPosts.findIndex(post => post.id === focusedPostId) : -1
  const readablePosts = displayedPosts.filter(post => !post.redacted)
  const threadStarterNoReply = Boolean(posts[0]?.noReply)
  const sameAuthorPosts = authorFocus ? readablePosts.filter(post => post.author === authorFocus) : []
  const focusedAuthorIndex = focusedPostId ? sameAuthorPosts.findIndex(post => post.id === focusedPostId) : -1
  const otherUnreadThreadCount = boardUnreadThreads.filter(item => item.id !== thread.id).length
  const canManageBoard = currentUserRole === 'admin' || currentUserRole === 'moderator' || Boolean(boardInfo?.moderators.some(m => m.userId === currentUserId))
  const currentMember = boardInfo?.members.find(m => m.userId === currentUserId)
  const canCurateBoard = canManageBoard || Boolean(currentMember?.canCurate)
  const canAnnounceBoard = canManageBoard || Boolean(currentMember?.canAnnounce)
  const canUseCurationAction = canCurateBoard || canAnnounceBoard
  const curationDefaultKind = canCurateBoard ? 'digest' : 'announcement'
  const canModeratePosts = canManageBoard || Boolean(currentMember?.canModeratePosts)
  const canModerateThreads = canManageBoard || Boolean(currentMember?.canModerateThreads)
  const canRenameThread = canModerateThreads || thread.authorId === currentUserId || thread.author === currentUsername
  const canManagePolls = canManageBoard || Boolean(currentMember?.canManagePolls)
  const boardBlocksReplies = Boolean(boardInfo?.settings.readOnly || boardInfo?.settings.noReply)
  const canReplyInBoard = !threadLocked && (!boardBlocksReplies || canManageBoard)
  const canAttach = Boolean(boardInfo?.settings.attachmentsAllowed || canManageBoard)

  function scrollToPost(postId: string) {
    setFocusedPostId(postId)
    postRefs.current[postId]?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }

  function jumpUnread(direction: 'next' | 'previous') {
    if (unreadPosts.length === 0) return
    if (direction === 'previous') {
      const target = focusedUnreadIndex <= 0 ? unreadPosts[unreadPosts.length - 1] : unreadPosts[focusedUnreadIndex - 1]
      scrollToPost(target.id)
      return
    }
    const target = focusedUnreadIndex < 0 || focusedUnreadIndex >= unreadPosts.length - 1
      ? unreadPosts[0]
      : unreadPosts[focusedUnreadIndex + 1]
    scrollToPost(target.id)
  }

  function jumpTopicBoundary(boundary: 'first' | 'last') {
    const target = boundary === 'first' ? readablePosts[0] : readablePosts[readablePosts.length - 1]
    if (target) scrollToPost(target.id)
  }

  function pickUnreadThread(source: ThreadSummary[], direction: 'next' | 'previous') {
    const otherUnread = source.filter(item => item.id !== thread.id)
    if (otherUnread.length === 0) return undefined
    const currentIndex = source.findIndex(item => item.id === thread.id)
    if (currentIndex < 0) {
      return direction === 'next' ? otherUnread[0] : otherUnread[otherUnread.length - 1]
    }
    const step = direction === 'next' ? 1 : -1
    for (let i = 1; i <= source.length; i += 1) {
      const idx = (currentIndex + (step * i) + source.length) % source.length
      const candidate = source[idx]
      if (candidate.id !== thread.id && candidate.unreadPosts > 0) return candidate
    }
    return undefined
  }

  async function jumpUnreadThread(direction: 'next' | 'previous') {
    const summaries = await refreshUnreadThreads()
    const target = pickUnreadThread(summaries, direction)
    if (target) onOpenThread(target, target.firstUnreadPostId)
  }

  function startAuthorTrail(post: Post) {
    setAuthorFocus(post.author)
    scrollToPost(post.id)
  }

  async function startReplyTree(post: Post) {
    setReplyTreeRoot(post.id)
    setReplyTreeLoading(true)
    setFocusedPostId(post.id)
    const res = await api.listReplyTree(token, post.id, 100, 0)
    setReplyTreeLoading(false)
    if (res.error) {
      setReplyTreeRoot(undefined)
      setReplyTreePosts(null)
      alert(res.error.message)
      return
    }
    const tree = res.data ?? [post]
    setReplyTreePosts(tree)
    setReactions(prev => {
      const next = { ...prev }
      tree.forEach(item => {
        if (!next[item.id]) next[item.id] = { count: item.reactionCount, reacted: false }
      })
      return next
    })
    tree.forEach(item => {
      if (item.author !== 'Anonymous') loadTrust(item.author)
      loadPollForPost(item.id, item.body)
    })
    setTimeout(() => postRefs.current[post.id]?.scrollIntoView({ behavior: 'smooth', block: 'start' }), 50)
  }

  function jumpAuthor(direction: 'next' | 'previous') {
    if (sameAuthorPosts.length === 0) return
    if (direction === 'previous') {
      const target = focusedAuthorIndex <= 0 ? sameAuthorPosts[sameAuthorPosts.length - 1] : sameAuthorPosts[focusedAuthorIndex - 1]
      scrollToPost(target.id)
      return
    }
    const target = focusedAuthorIndex < 0 || focusedAuthorIndex >= sameAuthorPosts.length - 1
      ? sameAuthorPosts[0]
      : sameAuthorPosts[focusedAuthorIndex + 1]
    scrollToPost(target.id)
  }

  async function curateThreadDigest() {
    const payload = promptDigestPayload(threadTitle, curationDefaultKind)
    if (!payload) return
    const res = await api.curateThread(token, thread.id, payload)
    if (res.error) alert(res.error.message)
  }

  async function curatePostDigest(post: Post) {
    const payload = promptDigestPayload(`${threadTitle} #${post.createdSeq}`, curationDefaultKind)
    if (!payload) return
    const res = await api.curatePost(token, post.id, payload)
    if (res.error) alert(res.error.message)
  }

  async function repostArticle(post: Post) {
    const board = prompt('Repost to board:', thread.board)
    if (board === null) return
    const title = prompt('Repost title:', threadTitle)
    if (title === null) return
    const res = await api.repostPost(token, post.id, {
      board: board.trim(),
      title: title.trim(),
    })
    if (res.error) {
      alert(res.error.message)
      return
    }
    alert(`Reposted as thread ${res.data?.id ?? ''}`.trim())
  }

  async function setAuthorRelationship(post: Post, kind: 'friend' | 'ignore') {
    const note = kind === 'friend' ? prompt('Friend note:', '') ?? '' : ''
    const res = await api.setUserRelationship(token, post.author, kind, true, note)
    if (res.error) {
      alert(res.error.message)
      return
    }
    alert(kind === 'friend' ? `${post.author} added as friend.` : `${post.author} ignored.`)
  }

  async function uploadAttachment(post: Post) {
    const file = await pickAttachmentFile()
    if (!file) return
    const res = await api.uploadPostAttachment(token, post.id, file)
    if (res.error) {
      alert(res.error.message)
      return
    }
    const refreshed = await api.listPosts(token, thread.id)
    if (refreshed.error) {
      alert(refreshed.error.message)
      return
    }
    setPosts(refreshed.data ?? [])
  }

  async function downloadAttachment(att: PostAttachment) {
    const res = await api.downloadAttachment(token, att.id, att.filename)
    if (res.error) alert(res.error.message)
  }

  async function markThreadRead() {
    const previous = readSeq
    const nextSeq = posts.reduce((max, post) => Math.max(max, post.createdSeq), readSeq)
    setReadSeq(nextSeq)
    const res = await api.markThreadRead(token, thread.id)
    if (res.error) {
      setReadSeq(previous)
      alert(res.error.message)
      return
    }
    void refreshUnreadThreads()
  }

  async function restoreThreadRead() {
    const previous = readSeq
    const res = await api.restoreThreadRead(token, thread.id)
    if (res.error) {
      alert(res.error.message)
      return
    }
    const summaries = await api.listThreads(token, thread.board)
    if (summaries.error) {
      setReadSeq(previous)
      alert(summaries.error.message)
      return
    }
    const summary = summaries.data?.find(item => item.id === thread.id)
    setReadSeq(summary?.readSeq ?? 0)
    void refreshUnreadThreads()
  }

  async function markPostReadThrough(post: Post) {
    const previous = readSeq
    setReadSeq(post.createdSeq)
    setFocusedPostId(post.id)
    const res = await api.markPostRead(token, post.id)
    if (res.error) {
      setReadSeq(previous)
      alert(res.error.message)
      return
    }
    void refreshUnreadThreads()
  }

  return (
    <div className="thread-page">
      <div className="page-header">
        <button className="back-btn" onClick={onBack}>← Threads</button>
        <h2 className="thread-title">{threadTitle}</h2>
        {threadLocked && <span className="locked-badge">🔒 Locked</span>}
        <span className="unread-pill">{unreadPosts.length} unread</span>
        <button className="link-btn" disabled={unreadPosts.length === 0} onClick={() => jumpUnread('previous')}>Prev unread</button>
        <button className="link-btn" disabled={unreadPosts.length === 0} onClick={() => jumpUnread('next')}>Next unread</button>
        <button className="link-btn" disabled={readablePosts.length === 0} onClick={() => jumpTopicBoundary('first')}>First post</button>
        <button className="link-btn" disabled={readablePosts.length === 0} onClick={() => jumpTopicBoundary('last')}>Last post</button>
        <button className="link-btn" disabled={otherUnreadThreadCount === 0} onClick={() => jumpUnreadThread('previous')}>Prev unread thread</button>
        <button className="link-btn" disabled={otherUnreadThreadCount === 0} onClick={() => jumpUnreadThread('next')}>Next unread thread</button>
        {authorFocus && (
          <>
            <span className="reading-mode-pill">{authorFocus}</span>
            <button className="link-btn" disabled={sameAuthorPosts.length < 2} onClick={() => jumpAuthor('previous')}>Prev author</button>
            <button className="link-btn" disabled={sameAuthorPosts.length < 2} onClick={() => jumpAuthor('next')}>Next author</button>
            <button className="link-btn" onClick={() => setAuthorFocus(undefined)}>Clear author</button>
          </>
        )}
        {replyTreeRoot && (
          <>
            <span className="reading-mode-pill">Replies #{replyTreeRootPost?.createdSeq ?? ''}</span>
            <button className="link-btn" onClick={() => { setReplyTreeRoot(undefined); setReplyTreePosts(null) }}>Clear replies</button>
          </>
        )}
        {replyTreeLoading && <span className="muted">Loading replies...</span>}
        {canUseCurationAction && <button className="link-btn" onClick={curateThreadDigest}>Digest thread</button>}
        <button className="link-btn" disabled={unreadPosts.length === 0} onClick={markThreadRead}>Mark all read</button>
        {readSeq > 0 && <button className="link-btn" onClick={restoreThreadRead}>Restore marker</button>}
        {canRenameThread && <button className="link-btn" onClick={renameThreadTitle}>Rename</button>}
        {canModerateThreads && (
          <button className="link-btn" onClick={toggleLock}>
            {threadLocked ? '🔓 Unlock' : '🔒 Lock'}
          </button>
        )}
      </div>

      <div className="post-list">
        {displayedPosts.map(post => {
          const rx = reactions[post.id] ?? { count: 0, reacted: false }
          const poll = polls[post.id] // null = loading, Poll = loaded, undefined = none
          const tl = trustLevels[post.author]
          const createdAt = post.createdAt ?? post.createdSeq
          const canPublishPollResult = canManagePolls || post.authorId === currentUserId || thread.authorId === currentUserId
          const canReplyToPost = canReplyInBoard && (!threadStarterNoReply || canModerateThreads) && (!post.noReply || canModerateThreads)
          const canSetArticleMetadata = post.authorId === currentUserId || canModerateThreads

          return (
            <div
              key={post.id}
              ref={el => { postRefs.current[post.id] = el }}
              className={`post-card${post.id === focusedPostId ? ' post-card--target' : ''}`}
            >
              <div className="post-meta">
                {post.author === 'Anonymous' && !post.authorId ? (
                  <span className="post-author">{post.author}</span>
                ) : (
                  <button className="post-author post-author-link" onClick={() => onOpenProfile(post.author)}>
                    {post.author}
                  </button>
                )}
                {tl !== undefined && (
                  <span className={`trust-badge trust-badge--tl${tl}`} title={`Trust level ${tl}`}>
                    {TL_LABEL[tl] ?? `TL${tl}`}
                  </span>
                )}
                <span className="muted post-time">
                  {createdAt > 1_000_000_000_000 ? new Date(createdAt).toLocaleString() : `#${post.createdSeq}`}
                </span>
                {!post.redacted && post.marked && <span className="post-flag-badge">Marked</span>}
                {!post.redacted && post.recommended && <span className="post-flag-badge">Recommended</span>}
                {!post.redacted && post.noReply && <span className="post-flag-badge">No replies</span>}
                {!post.redacted && post.tex && <span className="post-flag-badge">TeX</span>}
                {!post.redacted && post.mailBack && <span className="post-flag-badge">Mail-back</span>}
                {!post.redacted && post.sourcePost && <span className="post-flag-badge">Repost</span>}
                <span className="post-actions">
                  {post.createdSeq > readSeq && !post.redacted && (
                    <button className="link-btn" onClick={() => markPostReadThrough(post)}>Mark to here</button>
                  )}
                  {!post.redacted && post.author !== 'Anonymous' && (
                    <>
                      <button className="link-btn" onClick={() => startAuthorTrail(post)}>Same topic author</button>
                      <button className="link-btn" onClick={() => onOpenAuthorPosts(post.author)}>Author posts</button>
                    </>
                  )}
                  {!post.redacted && (
                    <button className="link-btn" onClick={() => startReplyTree(post)}>Reply tree</button>
                  )}
                  {!post.redacted && post.author !== 'Anonymous' && post.authorId !== currentUserId && (
                    <>
                      <button className="link-btn" onClick={() => setAuthorRelationship(post, 'friend')}>Friend</button>
                      <button className="link-btn danger" onClick={() => setAuthorRelationship(post, 'ignore')}>Ignore</button>
                    </>
                  )}
                  {canUseCurationAction && !post.redacted && (
                    <button className="link-btn" onClick={() => curatePostDigest(post)}>Digest</button>
                  )}
                  {!post.redacted && (
                    <button className="link-btn" onClick={() => repostArticle(post)}>Repost</button>
                  )}
                  {canCurateBoard && !post.redacted && (
                    <>
                      <button className="link-btn" onClick={() => setArticleFlag(post, { marked: !post.marked })}>{post.marked ? 'Unmark' : 'Mark'}</button>
                      <button className="link-btn" onClick={() => setArticleFlag(post, { recommended: !post.recommended })}>{post.recommended ? 'Unrecommend' : 'Recommend'}</button>
                    </>
                  )}
                  {canModerateThreads && !post.redacted && (
                    <button className="link-btn" onClick={() => setArticleFlag(post, { noReply: !post.noReply })}>{post.noReply ? 'Allow replies' : 'No replies'}</button>
                  )}
                  {canSetArticleMetadata && !post.redacted && (
                    <>
                      <button className="link-btn" onClick={() => setArticleFlag(post, { tex: !post.tex })}>{post.tex ? 'Plain text' : 'TeX'}</button>
                      <button className="link-btn" onClick={() => setArticleFlag(post, { mailBack: !post.mailBack })}>{post.mailBack ? 'Stop mail-back' : 'Mail-back'}</button>
                    </>
                  )}
                  {canAttach && !post.redacted && (canManageBoard || post.authorId === currentUserId) && (
                    <button className="link-btn" onClick={() => uploadAttachment(post)}>Upload file</button>
                  )}
                  <button
                    className={`link-btn react-btn${rx.reacted ? ' react-btn--active' : ''}`}
                    onClick={() => toggleReact(post.id)}
                    title={rx.reacted ? 'Remove heart' : 'Heart this post'}
                  >
                    {rx.reacted ? '❤️' : '🤍'}{rx.count > 0 ? ` ${rx.count}` : ''}
                  </button>
                  {canReplyToPost && !post.redacted && (
                    <>
                      <button className="link-btn" onClick={() => startReply(post)}>Reply</button>
                      <button className="link-btn" onClick={() => startReply(post, true)}>Quote</button>
                    </>
                  )}
                  {(canModeratePosts || post.authorId === currentUserId) && !post.redacted && (
                    <button className="link-btn danger" onClick={() => redactPost(post.id)}>Redact</button>
                  )}
                  {canModeratePosts && post.redacted && (
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
              {!post.redacted && post.sourcePost && (
                <div className="post-reply-context muted">
                  Reposted from {post.sourceBoard || 'source'} / {post.sourceTitle || post.sourceThread || post.sourcePost}
                  {post.sourceAuthor && post.sourceAuthor !== 'Anonymous' && (
                    <>
                      {' by '}
                      <button className="link-btn" onClick={() => onOpenProfile(post.sourceAuthor!)}>
                        {post.sourceAuthor}
                      </button>
                    </>
                  )}
                </div>
              )}

              <div className="post-body">
                <Markup body={post.body} redacted={post.redacted} />
              </div>

              {!post.redacted && post.signature && (
                <div className="post-signature">
                  <Markup body={post.signature} />
                </div>
              )}

              {!post.redacted && post.attachments && post.attachments.length > 0 && (
                <div className="post-attachments">
                  {post.attachments.map(att => (
                    <span className="post-attachment" key={att.id}>
                      {att.url ? (
                        <a href={att.url} target="_blank" rel="noreferrer">{att.filename}</a>
                      ) : (
                        <span>{att.filename}</span>
                      )}
                      {att.stored && <button className="link-btn" onClick={() => downloadAttachment(att)}>Download</button>}
                      {(att.contentType || att.sizeBytes) && (
                        <span className="muted">
                          {att.contentType}{att.contentType && att.sizeBytes ? ' · ' : ''}{formatBytes(att.sizeBytes)}
                        </span>
                      )}
                    </span>
                  ))}
                </div>
              )}

              {poll && (
                <PollWidget
                  poll={poll}
                  onVote={optionId => handleVotePoll(poll.id, optionId)}
                  onPublishResult={canPublishPollResult ? () => handlePublishPollResult(poll.id) : undefined}
                />
              )}
            </div>
          )
        })}
        <div ref={bottomRef} />
      </div>

      {canReplyInBoard && (
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
              onChange={e => {
                setDraftBody(e.target.value)
                if (composeError) setComposeError(null)
              }}
              placeholder="Write your reply…"
              rows={6}
              onKeyDown={e => {
                if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) submitPost()
              }}
            />
            {draftPollValidation.hasPollTag && !draftPollValidation.valid && (
              <p className="error">{draftPollValidation.message}</p>
            )}
            {composeError && <p className="error">{composeError}</p>}
            {canAttach && (
              <AttachmentComposer attachments={draftAttachments} onChange={setDraftAttachments} disabled={submitting} />
            )}
            <div className="compose-actions">
              <PollComposer
                onInsert={insertPollIntoDraft}
                disabled={!isTrustLoaded || !canCreatePoll}
                disabledHint={!isTrustLoaded ? 'Checking permission…' : (!canCreatePoll ? 'Polls require trust level 2+' : undefined)}
              />
              {boardInfo?.settings.anonymousAllowed && (
                <label className="inline-toggle">
                  <input type="checkbox" checked={replyAnonymous} onChange={e => setReplyAnonymous(e.target.checked)} />
                  Anonymous
                </label>
              )}
              <button onClick={submitPost} disabled={submitting || !draftBody.trim()}>
                {submitting ? '…' : 'Post reply'}
              </button>
              <button className="link-btn" onClick={() => { setComposing(false); setDraftBody(''); setDraftAttachments([]); setReplyAnonymous(false); setReplyTo(undefined) }}>
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

function eventAttachments(payload: PostAppendedPayload): PostAttachment[] | undefined {
  if (!payload.attachments?.length) return undefined
  return payload.attachments.map(att => ({
    id: att.id ?? `${payload.id}-${att.filename}`,
    postId: payload.id,
    filename: att.filename,
    contentType: att.contentType,
    sizeBytes: att.sizeBytes,
    url: att.url,
    stored: false,
    createdBy: payload.authorId,
    createdAt: payload.ts,
  }))
}

function pickAttachmentFile() {
  return new Promise<File | null>(resolve => {
    const input = document.createElement('input')
    input.type = 'file'
    input.onchange = () => resolve(input.files?.[0] ?? null)
    input.oncancel = () => resolve(null)
    input.click()
  })
}
