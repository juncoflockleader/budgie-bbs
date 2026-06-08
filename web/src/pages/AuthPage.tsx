import { useState, FormEvent } from 'react'
import * as api from '../api/client'
import type { AuthState } from '../hooks/useAuth'
import { useI18n } from '../i18n'

interface Props {
  onLogin: (token: string, user: AuthState['user']) => void
}

export function AuthPage({ onLogin }: Props) {
  const { t, locale, setLocale } = useI18n()
  const [mode, setMode] = useState<'login' | 'register' | 'recover'>('login')
  const [name, setName] = useState('')
  const [password, setPassword] = useState('')
  const [submittedName, setSubmittedName] = useState('')
  const [email, setEmail] = useState('')
  const [note, setNote] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)

    if (mode === 'recover') {
      const res = await api.requestPasswordRecovery({
        name: name.trim(),
        submittedName: submittedName.trim(),
        email: email.trim(),
        note: note.trim(),
      })
      setBusy(false)
      if (res.error) {
        setError(t('common.errorPrefix', { message: res.error.message }))
      } else {
        setError(t('auth.recoverySubmitted'))
        setSubmittedName('')
        setEmail('')
        setNote('')
      }
      return
    }

    const fn = mode === 'login' ? api.login : api.register
    const res = await fn(name, password)

    setBusy(false)
    if (res.error) {
      setError(t('common.errorPrefix', { message: res.error.message }))
    } else if (res.data?.status === 'pending' || !res.data?.token) {
      setError(t('auth.registrationPending'))
    } else if (res.data) {
      onLogin(res.data.token, res.data.user)
    }
  }

  return (
    <div className="auth-page">
      <h1 className="auth-title">{t('app.name')}</h1>
      <div className="auth-locale">
        <select
          value={locale}
          onChange={e => setLocale(e.currentTarget.value as typeof locale)}
          aria-label={t('settings.language')}
        >
          <option value="en">EN</option>
          <option value="zh-CN">中文</option>
          <option value="zh-TW">中文（繁）</option>
        </select>
      </div>
      <form className="auth-form" onSubmit={submit}>
        <h2>{mode === 'login' ? t('auth.modeSignIn') : mode === 'register' ? t('auth.modeRegister') : t('auth.modeRecover')}</h2>
        <label>
          {t('auth.username')}
          <input
            autoFocus
            value={name}
            onChange={e => setName(e.target.value)}
            required
            minLength={1}
            maxLength={64}
          />
        </label>
        {mode === 'recover' ? (
          <>
            <label>
              {t('auth.realName')}
              <input value={submittedName} onChange={e => setSubmittedName(e.target.value)} />
            </label>
            <label>
              {t('auth.email')}
              <input value={email} onChange={e => setEmail(e.target.value)} />
            </label>
            <label>
              {t('auth.note')}
              <textarea value={note} onChange={e => setNote(e.target.value)} rows={3} />
            </label>
          </>
        ) : (
          <label>
            {t('auth.password')}
            <input
              type="password"
              value={password}
              onChange={e => setPassword(e.target.value)}
              required
              minLength={8}
            />
          </label>
        )}
        {error && <p className="error">{error}</p>}
        <button type="submit" disabled={busy}>
          {busy ? '…' : mode === 'login' ? t('auth.modeSignIn') : mode === 'register' ? t('auth.register') : t('auth.submitRequest')}
        </button>
        <button
          type="button"
          className="link-btn"
          onClick={() => { setMode(m => m === 'login' ? 'register' : 'login'); setError(null) }}
        >
          {mode === 'login' ? t('auth.switchRegister') : t('auth.switchSignIn')}
        </button>
        {mode !== 'recover' && (
          <button
            type="button"
            className="link-btn"
            onClick={() => { setMode('recover'); setError(null) }}
          >
            {t('auth.forgotPassword')}
          </button>
        )}
      </form>
    </div>
  )
}
