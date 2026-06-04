import { useMemo, useState } from 'react'

interface Props {
  disabled?: boolean
  disabledHint?: string
  onInsert: (pollMarkup: string) => void
}

function buildPollMarkup(question: string, options: string[]): string {
  const lines = [question.trim(), ...options.map(o => o.trim()).filter(Boolean)]
  return `[poll]\n${lines.join('\n')}\n[/poll]`
}

function normalizeOptions(values: string[]) {
  return values.map(v => v.trim())
}

export function PollComposer({ disabled, disabledHint, onInsert }: Props) {
  const [open, setOpen] = useState(false)
  const [question, setQuestion] = useState('')
  const [options, setOptions] = useState(['', ''])

  const validQuestion = question.trim() !== ''
  const normalizedOptions = useMemo(() => normalizeOptions(options), [options])
  const optionCount = normalizedOptions.filter(Boolean).length
  const canInsert = validQuestion && optionCount >= 2

  function resetBuilder() {
    setQuestion('')
    setOptions(['', ''])
  }

  function addOption() {
    setOptions(prev => [...prev, ''])
  }

  function removeOption(index: number) {
    if (options.length <= 2) return
    setOptions(prev => prev.filter((_, i) => i !== index))
  }

  function updateOption(index: number, value: string) {
    setOptions(prev => {
      const next = [...prev]
      next[index] = value
      return next
    })
  }

  function insertPoll() {
    if (!canInsert) return
    const markup = buildPollMarkup(question, normalizedOptions)
    onInsert(markup)
    resetBuilder()
    setOpen(false)
  }

  if (!open) {
    return (
      <button
        type="button"
        className="link-btn"
        disabled={disabled}
        onClick={() => setOpen(true)}
        title={disabledHint}
      >
        Add Poll
      </button>
    )
  }

  return (
    <div className="poll-composer">
      <div className="compose-field">
        <label>
          Poll question
          <input
            value={question}
            onChange={e => setQuestion(e.target.value)}
            placeholder="What do you want to ask?"
          />
        </label>
      </div>
      <div className="compose-field">
        <label>Options</label>
        <div className="poll-composer-options">
          {options.map((value, index) => (
            <div key={index} className="poll-composer-option-row">
              <input
                value={value}
                onChange={e => updateOption(index, e.target.value)}
                placeholder={`Option ${index + 1}`}
              />
              <button
                type="button"
                className="link-btn"
                onClick={() => removeOption(index)}
                disabled={options.length <= 2}
                title={options.length <= 2 ? 'Need at least two options' : 'Remove option'}
              >
                ×
              </button>
            </div>
          ))}
        </div>
      </div>
      <div className="compose-actions">
        <button
          type="button"
          onClick={addOption}
        >
          + Add Option
        </button>
        <button
          type="button"
          onClick={insertPoll}
          disabled={!canInsert}
          className="link-btn"
        >
          Insert Poll Into Draft
        </button>
        <button
          type="button"
          className="link-btn"
          onClick={() => { setOpen(false); resetBuilder() }}
        >
          Cancel
        </button>
        <span className="muted compose-hint">{optionCount} valid options</span>
      </div>
      {!canInsert && (
        <p className="muted compose-hint">
          Poll needs a question and at least two options.
        </p>
      )}
    </div>
  )
}
