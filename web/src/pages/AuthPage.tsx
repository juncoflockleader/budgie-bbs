import { useState, useEffect, useCallback, FormEvent } from 'react'
import * as api from '../api/client'
import type { AuthState } from '../hooks/useAuth'
import type { CaptchaChallenge } from '../api/types'
import { useI18n } from '../i18n'

interface Props {
  onLogin: (token: string, user: AuthState['user']) => void
}

const providerScripts: Record<string, { src: string; cls: string }> = {
  turnstile: { src: 'https://challenges.cloudflare.com/turnstile/v0/api.js', cls: 'cf-turnstile' },
  hcaptcha: { src: 'https://js.hcaptcha.com/1/api.js', cls: 'h-captcha' },
  recaptcha: { src: 'https://www.google.com/recaptcha/api.js', cls: 'g-recaptcha' },
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

  const [captchaMode, setCaptchaMode] = useState<'off' | 'native' | 'provider'>('off')
  const [captchaProvider, setCaptchaProvider] = useState('')
  const [captchaSiteKey, setCaptchaSiteKey] = useState('')
  const [challenge, setChallenge] = useState<CaptchaChallenge | null>(null)
  const [captchaAnswer, setCaptchaAnswer] = useState('')
  const [captchaToken, setCaptchaToken] = useState('')

  const loadChallenge = useCallback(async () => {
    const res = await api.getCaptchaChallenge()
    setChallenge(res.data ?? null)
    setCaptchaAnswer('')
  }, [])

  // Load captcha policy when entering register mode.
  useEffect(() => {
    if (mode !== 'register') return
    let cancelled = false
    api.getAuthPolicy().then(res => {
      if (cancelled || !res.data) return
      const c = res.data.captcha
      setCaptchaMode(c.mode)
      setCaptchaProvider(c.provider ?? '')
      setCaptchaSiteKey(c.siteKey ?? '')
      if (c.mode === 'native') loadChallenge()
    })
    return () => { cancelled = true }
  }, [mode, loadChallenge])

  // Provider mode: load the provider script and capture the token via a global callback.
  useEffect(() => {
    if (mode !== 'register' || captchaMode !== 'provider') return
    const spec = providerScripts[captchaProvider]
    if (!spec) return
    ;(window as unknown as Record<string, unknown>).budgieCaptchaToken = (tok: string) => setCaptchaToken(tok)
    if (!document.querySelector(`script[src="${spec.src}"]`)) {
      const s = document.createElement('script')
      s.src = spec.src
      s.async = true
      s.defer = true
      document.head.appendChild(s)
    }
  }, [mode, captchaMode, captchaProvider])

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

    const res = mode === 'login'
      ? await api.login(name, password)
      : await api.register(name, password, {
          challengeId: challenge?.id,
          answer: captchaAnswer,
          token: captchaToken,
        })

    setBusy(false)
    if (res.error) {
      setError(t('common.errorPrefix', { message: res.error.message }))
      // A used/failed native challenge is consumed; fetch a fresh one.
      if (mode === 'register' && captchaMode === 'native') loadChallenge()
    } else if (res.data?.status === 'pending' || !res.data?.token) {
      setError(t('auth.registrationPending'))
    } else if (res.data) {
      onLogin(res.data.token, res.data.user)
    }
  }

  const showCaptcha = mode === 'register' && captchaMode !== 'off'

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

        {showCaptcha && captchaMode === 'native' && (
          <div className="captcha-block">
            <span className="captcha-label">Verification</span>
            <div className="captcha-image">
              {challenge
                ? <img alt="captcha challenge" src={`data:image/svg+xml;utf8,${encodeURIComponent(challenge.svg)}`} />
                : <span className="captcha-loading">…</span>}
              <button type="button" className="link-btn captcha-refresh" onClick={loadChallenge} aria-label="new captcha">↻</button>
            </div>
            <input
              value={captchaAnswer}
              onChange={e => setCaptchaAnswer(e.target.value)}
              placeholder="Type the characters above"
              autoComplete="off"
              autoCapitalize="characters"
              required
            />
          </div>
        )}

        {showCaptcha && captchaMode === 'provider' && captchaSiteKey && (
          <div className="captcha-block">
            <span className="captcha-label">Verification</span>
            <div
              className={providerScripts[captchaProvider]?.cls}
              data-sitekey={captchaSiteKey}
              data-callback="budgieCaptchaToken"
            />
          </div>
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
