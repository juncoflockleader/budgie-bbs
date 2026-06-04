import { type ReactNode, useEffect, useState } from 'react'
import * as api from '../api/client'
import type { ArchiveRanking, BlessingRanking, Board, BoardRanking, CommunityStatHistory, CommunityStats, ReplyRanking, Thread, ThreadRanking, UserRanking } from '../api/types'
import { Spinner } from '../components/Spinner'

interface Props {
  token: string
  onBack: () => void
  onOpenBoard: (board: Board) => void
  onOpenThread: (board: Board, thread: Thread) => void
}

export function RankingsPage({ token, onBack, onOpenBoard, onOpenThread }: Props) {
  const [stats, setStats] = useState<CommunityStats | null>(null)
  const [history, setHistory] = useState<CommunityStatHistory[]>([])
  const [boards, setBoards] = useState<BoardRanking[]>([])
  const [threads, setThreads] = useState<ThreadRanking[]>([])
  const [replies, setReplies] = useState<ReplyRanking[]>([])
  const [users, setUsers] = useState<UserRanking[]>([])
  const [blessings, setBlessings] = useState<BlessingRanking[]>([])
  const [archives, setArchives] = useState<ArchiveRanking[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError(null)
    Promise.all([
      api.getCommunityStats(token),
      api.listCommunityStatHistory(token, 7),
      api.listBoardRankings(token, 12),
      api.listThreadRankings(token, 12),
      api.listReplyRankings(token, 12),
      api.listUserRankings(token, 12),
      api.listBlessingRankings(token, 12),
      api.listArchiveRankings(token, 12),
    ]).then(([statsRes, historyRes, boardRes, threadRes, replyRes, userRes, blessingRes, archiveRes]) => {
      if (cancelled) return
      setLoading(false)
      const failure = statsRes.error ?? historyRes.error ?? boardRes.error ?? threadRes.error ?? replyRes.error ?? userRes.error ?? blessingRes.error ?? archiveRes.error
      if (failure) {
        setError(failure.message)
        return
      }
      setStats(statsRes.data ?? null)
      setHistory(historyRes.data ?? [])
      setBoards(boardRes.data ?? [])
      setThreads(threadRes.data ?? [])
      setReplies(replyRes.data ?? [])
      setUsers(userRes.data ?? [])
      setBlessings(blessingRes.data ?? [])
      setArchives(archiveRes.data ?? [])
    })
    return () => {
      cancelled = true
    }
  }, [token])

  if (loading) return <Spinner />

  return (
    <div className="rankings-page">
      <div className="page-header">
        <button onClick={onBack}>Back</button>
        <h2>Rankings</h2>
      </div>
      {error && <p className="error">{error}</p>}
      {stats && (
        <section className="rankings-stats-grid">
          <Stat label="Users" value={stats.totalUsers} />
          <Stat label="Boards" value={stats.totalBoards} />
          <Stat label="Threads" value={stats.totalThreads} />
          <Stat label="Posts" value={stats.totalPosts} />
          <Stat label="Reactions" value={stats.totalReactions} />
          <Stat label="Online" value={stats.onlineUsers} />
          <Stat label="Max Online" value={stats.maxOnlineUsers ?? 0} />
          <Stat label="Online Time" value={formatDuration(stats.totalOnlineSeconds)} />
        </section>
      )}
      <RankingPanel title="Daily History">
        {history.length === 0 && <p className="muted empty-state">No daily stat history yet.</p>}
        {history.map((day, index) => (
          <div key={day.day} className="ranking-row">
            <span className="ranking-index">{index + 1}</span>
            <span className="ranking-main">
              <span className="item-title">{day.day}</span>
              <span className="item-meta muted">
                {day.totalUsers} users {formatDelta(day.deltaUsers)} / {day.totalPosts} posts {formatDelta(day.deltaPosts)} / {day.totalReactions} reactions {formatDelta(day.deltaReactions)}
              </span>
              <span className="item-meta muted">
                online time {formatDuration(day.totalOnlineSeconds)} {formatDurationDelta(day.deltaOnlineSeconds)}
              </span>
              <span className="item-meta muted">
                max {day.maxOnlineUsers} online {formatDateTime(day.maxOnlineAt)}
              </span>
            </span>
            <span className="ranking-score">{day.onlineUsers}</span>
          </div>
        ))}
      </RankingPanel>
      <section className="rankings-grid">
        <RankingPanel title="Active Boards">
          {boards.map((board, index) => (
            <button
              key={board.id}
              className="ranking-row"
              onClick={() => onOpenBoard(board)}
            >
              <span className="ranking-index">{index + 1}</span>
              <span className="ranking-main">
                <span className="item-title">{board.name}</span>
                <span className="item-meta muted">{board.postCount} posts / {board.threadCount} threads</span>
              </span>
            </button>
          ))}
        </RankingPanel>
        <RankingPanel title="Hot Threads">
          {threads.map((thread, index) => (
            <button
              key={thread.id}
              className="ranking-row"
              onClick={() => onOpenThread(
                { id: thread.board, name: thread.boardName, description: '' },
                rankingToThread(thread),
              )}
            >
              <span className="ranking-index">{index + 1}</span>
              <span className="ranking-main">
                <span className="item-title">{thread.title}</span>
                <span className="item-meta muted">{thread.boardName} / {thread.postCount} posts / {thread.reactionCount} reactions</span>
              </span>
              <span className="ranking-score">{thread.score}</span>
            </button>
          ))}
        </RankingPanel>
        <RankingPanel title="Latest Replies">
          {replies.map((reply, index) => (
            <button
              key={reply.postId}
              className="ranking-row"
              onClick={() => onOpenThread(
                { id: reply.board, name: reply.boardName, description: '' },
                replyToThread(reply),
              )}
            >
              <span className="ranking-index">{index + 1}</span>
              <span className="ranking-main">
                <span className="item-title">{reply.title}</span>
                <span className="item-meta muted">{reply.boardName} / {reply.author} / {formatDateTime(reply.createdAt)}</span>
                <span className="item-meta muted">{reply.excerpt}</span>
              </span>
            </button>
          ))}
        </RankingPanel>
        <RankingPanel title="Top Posters">
          {users.map((user, index) => (
            <div key={user.userId} className="ranking-row">
              <span className="ranking-index">{index + 1}</span>
              <span className="ranking-main">
                <span className="item-title">{user.name}</span>
                <span className="item-meta muted">{user.postsCreated} posts / {user.reactionsReceived} reactions / {user.loginCount} logins / {formatDuration(user.totalOnlineSeconds)} online</span>
              </span>
              <span className="ranking-score">TL{user.trustLevel}</span>
            </div>
          ))}
        </RankingPanel>
        <RankingPanel title="Blessings">
          {blessings.map((user, index) => (
            <div key={user.userId} className="ranking-row">
              <span className="ranking-index">{index + 1}</span>
              <span className="ranking-main">
                <span className="item-title">{user.name}</span>
                <span className="item-meta muted">last blessed {formatDate(user.lastBlessedAt)}</span>
              </span>
              <span className="ranking-score">{user.blessingCount}</span>
            </div>
          ))}
        </RankingPanel>
        <RankingPanel title="Archive Paths">
          {archives.map((archive, index) => (
            <button
              key={`${archive.boardId}:${archive.kind}:${archive.path}`}
              className="ranking-row"
              onClick={() => onOpenBoard({ id: archive.boardId, name: archive.boardName, description: '' })}
            >
              <span className="ranking-index">{index + 1}</span>
              <span className="ranking-main">
                <span className="item-title">{archive.path || '/'}</span>
                <span className="item-meta muted">{archive.boardName} / {archive.entryCount} entries / {archive.editedCount} edited</span>
              </span>
            </button>
          ))}
        </RankingPanel>
      </section>
    </div>
  )
}

function RankingPanel({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="ranking-panel">
      <h3 className="board-section-title">{title}</h3>
      <div className="ranking-list">{children}</div>
    </section>
  )
}

function Stat({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="rankings-stat">
      <span className="rankings-stat-value">{value}</span>
      <span className="rankings-stat-label">{label}</span>
    </div>
  )
}

function formatDate(ts: number) {
  if (!ts) return 'never'
  return new Date(ts).toLocaleDateString()
}

function formatDateTime(ts: number) {
  if (!ts) return 'never'
  return new Date(ts).toLocaleString()
}

function formatDelta(value: number) {
  if (!value) return ''
  return `(${value > 0 ? '+' : ''}${value})`
}

function formatDurationDelta(value: number) {
  if (!value) return ''
  const sign = value > 0 ? '+' : '-'
  return `(${sign}${formatDuration(Math.abs(value))})`
}

function formatDuration(seconds: number) {
  const normalized = Math.max(0, Math.floor(seconds || 0))
  if (normalized < 60) return `${normalized}s`
  const minutes = Math.floor(normalized / 60)
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  const minuteRemainder = minutes % 60
  if (hours < 24) return minuteRemainder ? `${hours}h ${minuteRemainder}m` : `${hours}h`
  const days = Math.floor(hours / 24)
  const hourRemainder = hours % 24
  return hourRemainder ? `${days}d ${hourRemainder}h` : `${days}d`
}

function rankingToThread(thread: ThreadRanking): Thread {
  return {
    id: thread.id,
    board: thread.board,
    author: thread.author,
    authorId: thread.authorId,
    title: thread.title,
    locked: false,
    postCount: thread.postCount,
    lastSeq: thread.lastSeq,
    createdTs: thread.createdAt,
    createdAt: thread.createdAt,
    updatedAt: thread.updatedAt,
  }
}

function replyToThread(reply: ReplyRanking): Thread {
  return {
    id: reply.threadId,
    board: reply.board,
    author: reply.author,
    authorId: reply.authorId,
    title: reply.title,
    locked: false,
    postCount: 0,
    lastSeq: reply.seq,
    createdTs: reply.createdAt,
    createdAt: reply.createdAt,
    updatedAt: reply.createdAt,
  }
}
