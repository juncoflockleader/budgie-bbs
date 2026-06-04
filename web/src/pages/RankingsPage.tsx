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

type HistoryMetricKey = 'posts' | 'users' | 'reactions' | 'onlineTime' | 'maxOnline' | 'maxGuests'

interface HistoryMetric {
  key: HistoryMetricKey
  label: string
  value: (day: CommunityStatHistory) => number
  format: (value: number) => string
}

const historyMetrics: HistoryMetric[] = [
  { key: 'posts', label: 'Posts', value: day => day.deltaPosts, format: formatCompactNumber },
  { key: 'users', label: 'Users', value: day => day.totalUsers, format: formatCompactNumber },
  { key: 'reactions', label: 'Reactions', value: day => day.deltaReactions, format: formatCompactNumber },
  { key: 'onlineTime', label: 'Online Time', value: day => day.deltaOnlineSeconds, format: formatDuration },
  { key: 'maxOnline', label: 'Max Online', value: day => day.maxOnlineUsers, format: formatCompactNumber },
  { key: 'maxGuests', label: 'Max Guests', value: day => day.maxOnlineGuests, format: formatCompactNumber },
]

export function RankingsPage({ token, onBack, onOpenBoard, onOpenThread }: Props) {
  const [stats, setStats] = useState<CommunityStats | null>(null)
  const [history, setHistory] = useState<CommunityStatHistory[]>([])
  const [historyMetricKey, setHistoryMetricKey] = useState<HistoryMetricKey>('posts')
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
      api.listCommunityStatHistory(token, 30),
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

  const selectedHistoryMetric = historyMetrics.find(metric => metric.key === historyMetricKey) ?? historyMetrics[0]
  const recentHistory = history.slice(0, 12)

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
          <Stat label="Guests" value={stats.onlineGuests} />
          <Stat label="Max Online" value={stats.maxOnlineUsers ?? 0} />
          <Stat label="Max Guests" value={stats.maxOnlineGuests ?? 0} />
          <Stat label="Online Time" value={formatDuration(stats.totalOnlineSeconds)} />
        </section>
      )}
      <RankingPanel title="History Chart">
        {history.length === 0 && <p className="muted empty-state">No daily stat history yet.</p>}
        {history.length > 0 && (
          <HistoryChart
            history={history}
            metric={selectedHistoryMetric}
            selectedKey={historyMetricKey}
            onSelectMetric={setHistoryMetricKey}
          />
        )}
      </RankingPanel>
      <RankingPanel title="Daily History">
        {history.length === 0 && <p className="muted empty-state">No daily stat history yet.</p>}
        {recentHistory.map((day, index) => (
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
                {day.onlineUsers} users online / {day.onlineGuests} guests {formatDelta(day.deltaGuests)}
              </span>
              <span className="item-meta muted">
                max {day.maxOnlineUsers} users {formatDateTime(day.maxOnlineAt)} / max {day.maxOnlineGuests} guests {formatDateTime(day.maxOnlineGuestsAt)}
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

function HistoryChart({
  history,
  metric,
  selectedKey,
  onSelectMetric,
}: {
  history: CommunityStatHistory[]
  metric: HistoryMetric
  selectedKey: HistoryMetricKey
  onSelectMetric: (key: HistoryMetricKey) => void
}) {
  const days = history.slice().reverse()
  const values = days.map(day => metric.value(day))
  const rawMax = Math.max(...values, 0)
  const rawMin = Math.min(...values, 0)
  const range = Math.max(rawMax - rawMin, 1)
  const max = rawMax
  const min = rawMin
  const width = 640
  const height = 180
  const padX = 34
  const padY = 24
  const plotWidth = width - padX * 2
  const plotHeight = height - padY * 2
  const pointFor = (value: number, index: number) => {
    const x = days.length <= 1 ? width / 2 : padX + (index / (days.length - 1)) * plotWidth
    const y = padY + (1 - ((value - min) / range)) * plotHeight
    return { x, y }
  }
  const points = values.map(pointFor)
  const zeroY = padY + (1 - ((0 - min) / range)) * plotHeight
  const path = points.map((point, index) => `${index === 0 ? 'M' : 'L'} ${point.x.toFixed(1)} ${point.y.toFixed(1)}`).join(' ')
  const areaPath = points.length > 0
    ? `${path} L ${points[points.length - 1].x.toFixed(1)} ${zeroY.toFixed(1)} L ${points[0].x.toFixed(1)} ${zeroY.toFixed(1)} Z`
    : ''
  const latest = values[values.length - 1] ?? 0
  const previous = values[values.length - 2] ?? latest
  const firstDay = days[0]?.day ?? ''
  const lastDay = days[days.length - 1]?.day ?? ''

  return (
    <div className="history-chart-panel">
      <div className="history-metric-tabs" role="tablist" aria-label="History metric">
        {historyMetrics.map(option => (
          <button
            key={option.key}
            type="button"
            className={`history-metric-tab${option.key === selectedKey ? ' active' : ''}`}
            onClick={() => onSelectMetric(option.key)}
            role="tab"
            aria-selected={option.key === selectedKey}
          >
            {option.label}
          </button>
        ))}
      </div>
      <div className="history-chart">
        <svg className="history-chart-svg" viewBox={`0 0 ${width} ${height}`} role="img" aria-label={`${metric.label} history`}>
          <line className="history-chart-grid" x1={padX} y1={padY} x2={width - padX} y2={padY} />
          <line className="history-chart-grid" x1={padX} y1={height - padY} x2={width - padX} y2={height - padY} />
          <line className="history-chart-zero" x1={padX} y1={zeroY} x2={width - padX} y2={zeroY} />
          {areaPath && <path className="history-chart-area" d={areaPath} />}
          {path && <path className="history-chart-line" d={path} />}
          {points.map((point, index) => (
            <circle
              key={`${days[index]?.day ?? index}:${selectedKey}`}
              className="history-chart-point"
              cx={point.x}
              cy={point.y}
              r={index === points.length - 1 ? 4.5 : 3}
            />
          ))}
          <text className="history-chart-axis" x={padX} y={padY - 8}>{metric.format(max)}</text>
          <text className="history-chart-axis" x={padX} y={height - 6}>{firstDay}</text>
          <text className="history-chart-axis history-chart-axis-end" x={width - padX} y={height - 6}>{lastDay}</text>
        </svg>
      </div>
      <div className="history-chart-summary">
        <Stat label="Latest" value={metric.format(latest)} />
        <Stat label="Previous" value={metric.format(previous)} />
        <Stat label="Low" value={metric.format(min)} />
        <Stat label="High" value={metric.format(max)} />
      </div>
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

function formatCompactNumber(value: number) {
  return new Intl.NumberFormat(undefined, { maximumFractionDigits: 1, notation: 'compact' }).format(value)
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
