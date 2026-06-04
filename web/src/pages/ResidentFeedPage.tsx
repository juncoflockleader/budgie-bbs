import { useEffect, useState } from 'react'
import * as api from '../api/client'
import type { Board, Post, Thread } from '../api/types'
import { Markup } from '../components/Markup'
import { Spinner } from '../components/Spinner'

interface Props {
  token: string
  onBack: () => void
  onOpenThread: (board: Board, thread: Thread, initialPostId?: string) => void
}

const PAGE_SIZE = 30

export function ResidentFeedPage({ token, onBack, onOpenThread }: Props) {
  const [posts, setPosts] = useState<Post[]>([])
  const [offset, setOffset] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError(null)
    api.listResidentBoardPosts(token, PAGE_SIZE, offset).then(res => {
      if (cancelled) return
      setLoading(false)
      if (res.error) {
        setError(res.error.message)
        setPosts([])
        return
      }
      setPosts(res.data ?? [])
    })
    return () => {
      cancelled = true
    }
  }, [token, offset])

  function openPost(post: Post) {
    if (!post.board) return
    const board = { id: post.board, name: post.boardName || post.board, description: '' }
    const thread: Thread = {
      id: post.thread,
      board: post.board,
      boardName: post.boardName,
      author: post.author,
      authorId: post.authorId,
      title: post.threadTitle || post.thread,
      locked: false,
      postCount: 0,
      lastSeq: post.updatedSeq || post.createdSeq,
      createdTs: post.createdAt ?? post.createdSeq,
      createdAt: post.createdAt,
      updatedAt: post.updatedAt,
    }
    onOpenThread(board, thread, post.id)
  }

  return (
    <div className="author-posts-page">
      <div className="page-header">
        <button className="back-btn" onClick={onBack}>Back</button>
        <h2>Resident Boards</h2>
        <button className="link-btn" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}>Prev</button>
        <button className="link-btn" disabled={posts.length < PAGE_SIZE} onClick={() => setOffset(offset + PAGE_SIZE)}>Next</button>
      </div>
      {error && <p className="error">{error}</p>}
      {loading ? (
        <Spinner />
      ) : posts.length === 0 ? (
        <p className="muted">No resident board posts found.</p>
      ) : (
        <div className="author-post-list">
          {posts.map(post => (
            <article key={post.id} className="author-post-row">
              <button className="author-post-target" onClick={() => openPost(post)} disabled={!post.board}>
                <span className="item-title">{post.threadTitle || post.thread}</span>
                <span className="item-meta muted">
                  {post.boardName || post.board || 'board'} / {post.author} / #{post.createdSeq}
                </span>
              </button>
              <div className="post-body post-body--small">
                <Markup body={post.body} redacted={post.redacted} />
              </div>
            </article>
          ))}
        </div>
      )}
    </div>
  )
}
