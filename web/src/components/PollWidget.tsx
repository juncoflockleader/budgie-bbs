import { useState } from 'react'
import type { Poll } from '../api/types'

interface Props {
  poll: Poll
  onVote: (optionId: string) => Promise<void>
}

export function PollWidget({ poll, onVote }: Props) {
  const [voting, setVoting] = useState<string | null>(null)
  const [localPoll, setLocalPoll] = useState<Poll>(poll)

  const expired = localPoll.expiresAt ? localPoll.expiresAt < Date.now() : false
  const totalVotes = localPoll.options.reduce((s, o) => s + o.voteCount, 0)

  async function handleVote(optionId: string) {
    if (localPoll.voted || expired) return
    setVoting(optionId)
    await onVote(optionId)
    // Optimistically mark as voted and increment count
    setLocalPoll(prev => ({
      ...prev,
      voted: optionId,
      options: prev.options.map(o =>
        o.id === optionId ? { ...o, voteCount: o.voteCount + 1 } : o
      ),
    }))
    setVoting(null)
  }

  return (
    <div className="poll-widget">
      {localPoll.question && <div className="poll-question">{localPoll.question}</div>}
      <div className="poll-options">
        {localPoll.options.map(opt => {
          const pct = totalVotes > 0 ? Math.round((opt.voteCount / totalVotes) * 100) : 0
          const isVoted = localPoll.voted === opt.id
          const showResults = !!localPoll.voted || expired

          return (
            <div
              key={opt.id}
              className={`poll-option${isVoted ? ' poll-option--voted' : ''}${!localPoll.voted && !expired ? ' poll-option--clickable' : ''}`}
              onClick={() => { if (!localPoll.voted && !expired) handleVote(opt.id) }}
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
        {totalVotes} vote{totalVotes !== 1 ? 's' : ''}
        {expired && ' · Closed'}
        {localPoll.expiresAt && !expired && ` · Closes ${new Date(localPoll.expiresAt).toLocaleDateString()}`}
      </div>
    </div>
  )
}
