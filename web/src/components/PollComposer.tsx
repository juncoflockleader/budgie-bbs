import { useMemo, useState } from 'react'
import { formatDateTime } from '../i18n/format'
import { useI18n } from '../i18n'

interface Props {
  disabled?: boolean
  disabledHint?: string
  onInsert: (pollMarkup: string) => void
}

function buildPollMarkup(question: string, options: string[], expiresAt: string): string {
  const openTag = expiresAt ? `[poll expires=${expiresAt}]` : '[poll]'
  const lines = [question.trim(), ...options.map(o => o.trim()).filter(Boolean)]
  return `${openTag}\n${lines.join('\n')}\n[/poll]`
}

function normalizeOptions(values: string[]) {
  return values.map(v => v.trim())
}

function toDatetimeLocalOffset(date: Date): string {
  const pad2 = (value: number) => String(value).padStart(2, '0')
  return `${date.getFullYear()}-${pad2(date.getMonth() + 1)}-${pad2(date.getDate())}T${pad2(date.getHours())}:${pad2(date.getMinutes())}`
}

export function PollComposer({ disabled, disabledHint, onInsert }: Props) {
  const { t, locale } = useI18n()
  const [open, setOpen] = useState(false)
  const [question, setQuestion] = useState('')
  const [options, setOptions] = useState(['', ''])
  const [expiresAt, setExpiresAt] = useState('')

  const validQuestion = question.trim() !== ''
  const normalizedOptions = useMemo(() => normalizeOptions(options), [options])
  const optionCount = normalizedOptions.filter(Boolean).length
  const canInsert = validQuestion && optionCount >= 2

  function resetBuilder() {
    setQuestion('')
    setOptions(['', ''])
    setExpiresAt('')
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
    const markup = buildPollMarkup(question, normalizedOptions, expiresAt)
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
        {t('compose.addPoll')}
      </button>
    )
  }

  return (
    <div className="poll-composer">
      <div className="compose-field">
        <label>
          {t('compose.pollQuestion')}
          <input
            value={question}
            onChange={e => setQuestion(e.target.value)}
            placeholder={t('compose.pollQuestionHint')}
          />
        </label>
      </div>
      <div className="compose-field">
        <label>
          {t('compose.pollCloseLabel')}
          <input
            type="datetime-local"
            value={expiresAt}
            onChange={e => setExpiresAt(e.target.value)}
            title={expiresAt ? t('compose.pollCloseAtHint', { at: formatDateTime(new Date(expiresAt).getTime(), locale) }) : t('compose.pollCloseLeaveBlank')}
          />
          <div className="poll-composer-quick-times">
            <button type="button" className="link-btn" onClick={() => setExpiresAt(toDatetimeLocalOffset(new Date(Date.now() + 60 * 60 * 1000)))}>
              {t('compose.pollCloseTime1h')}
            </button>
            <button type="button" className="link-btn" onClick={() => setExpiresAt(toDatetimeLocalOffset(new Date(Date.now() + 24 * 60 * 60 * 1000)))}>
              {t('compose.pollCloseTime1d')}
            </button>
            <button type="button" className="link-btn" onClick={() => setExpiresAt(toDatetimeLocalOffset(new Date(Date.now() + 7 * 24 * 60 * 60 * 1000)))}>
              {t('compose.pollCloseTime1w')}
            </button>
            <button type="button" className="link-btn" onClick={() => setExpiresAt('')}>
              {t('compose.pollCloseClear')}
            </button>
          </div>
        </label>
      </div>
      <div className="compose-field">
        <label>{t('compose.options')}</label>
        <div className="poll-composer-options">
          {options.map((value, index) => (
            <div key={index} className="poll-composer-option-row">
              <input
                value={value}
                onChange={e => updateOption(index, e.target.value)}
                placeholder={t('compose.option', { index: index + 1 })}
              />
              <button
                type="button"
                className="link-btn"
                onClick={() => removeOption(index)}
                disabled={options.length <= 2}
                title={options.length <= 2 ? t('compose.pollNeedTwoOption') : t('compose.optionRemove')}
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
          {t('compose.optionAdd')}
        </button>
        <button
          type="button"
          onClick={insertPoll}
          disabled={!canInsert}
          className="link-btn"
        >
          {t('compose.insertPoll')}
        </button>
        <button
          type="button"
          className="link-btn"
          onClick={() => { setOpen(false); resetBuilder() }}
        >
          {t('compose.cancel')}
        </button>
        <span className="muted compose-hint">{t('compose.validOptions', { count: optionCount })}</span>
      </div>
      {!canInsert && (
        <p className="muted compose-hint">
          {t('compose.pollNeeds')}
        </p>
      )}
    </div>
  )
}
