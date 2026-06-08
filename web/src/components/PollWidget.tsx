import { useState } from 'react'
import type { Poll } from '../api/types'
import { useI18n } from '../i18n'

interface Props {
  poll: Poll
  onVote: (optionId: string) => Promise<void>
  onPublishResult?: () => Promise<void>
}

export function PollWidget({ poll, onVote, onPublishResult }: Props) {
  const { t } = useI18n()
  const [voting, setVoting] = useState<string | null>(null)
  const [publishing, setPublishing] = useState(false)

  const expired = poll.expiresAt ? poll.expiresAt < Date.now() : false
  const totalVotes = poll.options.reduce((s, o) => s + o.voteCount, 0)
  const voteSuffix = totalVotes > 1 ? 's' : ''

  async function handleVote(optionId: string) {
    if (expired) return
    if (poll.voted === optionId) return
    setVoting(optionId)
    try {
      await onVote(optionId)
    } finally {
      setVoting(null)
    }
  }

  async function handlePublishResult() {
    if (!onPublishResult) return
    setPublishing(true)
    try {
      await onPublishResult()
    } finally {
      setPublishing(false)
    }
  }

  return (
    <div className="poll-widget">
      {poll.question && <div className="poll-question">{poll.question}</div>}
      <div className="poll-options">
        {poll.options.map(opt => {
          const pct = totalVotes > 0 ? Math.round((opt.voteCount / totalVotes) * 100) : 0
          const isVoted = poll.voted === opt.id
          const showResults = !!poll.voted || expired

          return (
            <div
              key={opt.id}
              className={`poll-option${isVoted ? ' poll-option--voted' : ''}${!expired ? ' poll-option--clickable' : ''}`}
              onClick={() => {
                if (!expired) handleVote(opt.id)
              }}
            >
              {showResults && (
                <div className="poll-bar" style={{ width: `${pct}%` }} />
              )}
              <span className="poll-option-text">
                {isVoted && '✓ '}
                {opt.text}
              </span>
              {showResults && (
                <span className="poll-option-pct muted">{pct}% ({opt.voteCount})</span>
              )}
              {voting === opt.id && <span className="poll-option-spinner muted">…</span>}
            </div>
          )
        })}
      </div>
      <div className="poll-footer muted">
        <span>
          {t('pollWidget.totalVotes', { count: totalVotes, plural: voteSuffix })}
          {expired && ` · ${t('pollWidget.closed')}`}
          {poll.expiresAt && !expired && ` · ${t('pollWidget.closesAt', { time: new Date(poll.expiresAt).toLocaleString() })}`}
        </span>
        {onPublishResult && (
          <button type="button" className="link-btn" onClick={handlePublishResult} disabled={publishing}>
            {publishing ? t('pollWidget.publishing') : t('pollWidget.publishResult')}
          </button>
        )}
      </div>
    </div>
  )
}
