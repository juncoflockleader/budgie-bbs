import { useState, useEffect, useCallback, FormEvent } from 'react'
import * as api from '../api/client'
import type { AuthState } from '../hooks/useAuth'
import type { CaptchaChallenge } from '../api/types'
import { useI18n } from '../i18n'

interface Props {
  onLogin: (token: string, user: AuthState['user']) => void
  siteTitle?: string
  tagline?: string
  bannerURL?: string | null
}

const providerScripts: Record<string, { src: string; cls: string }> = {
  turnstile: { src: 'https://challenges.cloudflare.com/turnstile/v0/api.js', cls: 'cf-turnstile' },
  hcaptcha: { src: 'https://js.hcaptcha.com/1/api.js', cls: 'h-captcha' },
  recaptcha: { src: 'https://www.google.com/recaptcha/api.js', cls: 'g-recaptcha' },
}

export function AuthPage({ onLogin, siteTitle, tagline, bannerURL }: Props) {
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
  const [emailRequired, setEmailRequired] = useState(false)
  const [verificationSent, setVerificationSent] = useState(false)
  const [devInboxUrl, setDevInboxUrl] = useState('')
  const [realName, setRealName] = useState('')
  const [affiliation, setAffiliation] = useState('')
  const [joinNote, setJoinNote] = useState('')
  const [policyRequired, setPolicyRequired] = useState(false)
  const [policyVersion, setPolicyVersion] = useState('')
  const [policyAccepted, setPolicyAccepted] = useState(false)
  const [policyText, setPolicyText] = useState<string | null>(null)
  const [policyOpen, setPolicyOpen] = useState(false)

  // Two-factor login challenge.
  const [twoFAChallenge, setTwoFAChallenge] = useState('')
  const [twoFAMethods, setTwoFAMethods] = useState<string[]>([])
  const [twoFAMethod, setTwoFAMethod] = useState('totp')
  const [twoFACode, setTwoFACode] = useState('')
  const [twoFANotice, setTwoFANotice] = useState('')

  async function sendEmail2FACode() {
    setTwoFANotice('')
    await api.requestEmailTwoFactor(twoFAChallenge)
    setTwoFANotice('A code has been emailed to you.')
  }

  async function verify2FA(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    const res = await api.verifyTwoFactor(twoFAChallenge, twoFAMethod, twoFACode.trim())
    setBusy(false)
    if (res.error) {
      setError(t('common.errorPrefix', { message: res.error.message }))
      setTwoFACode('')
    } else if (res.data?.token) {
      onLogin(res.data.token, res.data.user)
    }
  }

  async function togglePolicy() {
    if (!policyOpen && policyText === null) {
      const res = await api.getPrivacyPolicy()
      setPolicyText(res.data?.markdown ?? 'Could not load the policy.')
    }
    setPolicyOpen(o => !o)
  }

  const loadChallenge = useCallback(async () => {
    const res = await api.getCaptchaChallenge()
    setChallenge(res.data ?? null)
    setCaptchaAnswer('')
  }, [])

  async function resend() {
    setBusy(true)
    await api.resendVerification(name)
    setBusy(false)
    setError('Verification email resent.')
  }

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
      setEmailRequired(res.data.emailVerification?.required ?? false)
      setDevInboxUrl(res.data.emailVerification?.devInboxUrl ?? '')
      setPolicyRequired(res.data.privacyPolicy?.required ?? false)
      setPolicyVersion(res.data.privacyPolicy?.version ?? '')
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
          email: email.trim(),
          realName: realName.trim(),
          affiliation: affiliation.trim(),
          note: joinNote.trim(),
          acceptPolicy: policyAccepted,
          policyVersion,
          captchaChallengeId: challenge?.id,
          captchaAnswer,
          captchaToken,
        })

    setBusy(false)
    if (res.error) {
      setError(t('common.errorPrefix', { message: res.error.message }))
      // A used/failed native challenge is consumed; fetch a fresh one.
      if (mode === 'register' && captchaMode === 'native') loadChallenge()
    } else if (mode === 'register' && res.data?.status === 'verification_required') {
      setVerificationSent(true)
    } else if (res.data?.status === '2fa_required') {
      const methods = res.data.methods ?? ['totp']
      setTwoFAChallenge(res.data.challengeToken ?? '')
      setTwoFAMethods(methods)
      setTwoFAMethod(methods[0] ?? 'totp')
      setTwoFACode('')
      setTwoFANotice('')
    } else if (res.data?.status === 'pending' || !res.data?.token) {
      setError(t('auth.registrationPending'))
    } else if (res.data) {
      onLogin(res.data.token, res.data.user)
    }
  }

  const showCaptcha = mode === 'register' && captchaMode !== 'off'

  return (
    <div className="auth-page">
      {bannerURL && <img className="auth-banner" src={bannerURL} alt="" onError={e => { e.currentTarget.style.display = 'none' }} />}
      <h1 className="auth-title">{siteTitle || t('app.name')}</h1>
      {tagline && <p className="auth-tagline">{tagline}</p>}
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
      {twoFAChallenge ? (
        <form className="auth-form" onSubmit={verify2FA}>
          <h2>Two-factor verification</h2>
          {twoFAMethods.length > 1 && (
            <div className="twofa-methods">
              {twoFAMethods.includes('totp') && (
                <button type="button" className={`link-btn${twoFAMethod === 'totp' ? ' twofa-active' : ''}`} onClick={() => setTwoFAMethod('totp')}>Authenticator app</button>
              )}
              {twoFAMethods.includes('email') && (
                <button type="button" className={`link-btn${twoFAMethod === 'email' ? ' twofa-active' : ''}`} onClick={() => setTwoFAMethod('email')}>Email code</button>
              )}
              {twoFAMethods.includes('backup') && (
                <button type="button" className={`link-btn${twoFAMethod === 'backup' ? ' twofa-active' : ''}`} onClick={() => setTwoFAMethod('backup')}>Backup code</button>
              )}
            </div>
          )}
          <p className="muted">
            {twoFAMethod === 'email'
              ? 'Enter the 6-digit code sent to your email.'
              : twoFAMethod === 'backup'
                ? 'Enter one of your saved backup codes.'
                : 'Enter the 6-digit code from your authenticator app.'}
          </p>
          {twoFAMethod === 'email' && (
            <button type="button" className="link-btn" onClick={sendEmail2FACode}>Send a code to my email</button>
          )}
          {twoFANotice && <p className="muted">{twoFANotice}</p>}
          <label>
            Code
            <input
              autoFocus
              value={twoFACode}
              onChange={e => setTwoFACode(e.target.value)}
              inputMode={twoFAMethod === 'backup' ? 'text' : 'numeric'}
              autoComplete="one-time-code"
              placeholder={twoFAMethod === 'backup' ? 'abcd-efgh' : '123456'}
              required
            />
          </label>
          {error && <p className="error">{error}</p>}
          <button type="submit" disabled={busy}>{busy ? '…' : 'Verify'}</button>
          <button
            type="button"
            className="link-btn"
            onClick={() => { setTwoFAChallenge(''); setTwoFACode(''); setError(null) }}
          >
            Cancel
          </button>
        </form>
      ) : verificationSent ? (
        <div className="auth-form">
          <h2>Check your email</h2>
          <p>We sent a verification link to {email || 'your email'}. Open it to finish creating your account, then sign in.</p>
          {devInboxUrl && (
            <p className="dev-inbox-hint">
              Dev mode: emails are captured locally —{' '}
              <a href={devInboxUrl} target="_blank" rel="noreferrer">open the inbox ↗</a>
            </p>
          )}
          {error && <p className="error">{error}</p>}
          <button type="button" disabled={busy} onClick={resend}>{busy ? '…' : 'Resend email'}</button>
          <button
            type="button"
            className="link-btn"
            onClick={() => { setVerificationSent(false); setMode('login'); setError(null) }}
          >
            {t('auth.switchSignIn')}
          </button>
        </div>
      ) : (
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
        {mode === 'register' && emailRequired && (
          <label>
            {t('auth.email')}
            <input
              type="email"
              value={email}
              onChange={e => setEmail(e.target.value)}
              required
            />
          </label>
        )}
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

        {mode === 'register' && (
          <>
            <label>
              Real name <span className="auth-optional">(optional)</span>
              <input value={realName} onChange={e => setRealName(e.target.value)} maxLength={200} />
            </label>
            <label>
              School or affiliation <span className="auth-optional">(optional)</span>
              <input value={affiliation} onChange={e => setAffiliation(e.target.value)} maxLength={200} />
            </label>
            <label>
              Reason for joining <span className="auth-optional">(optional)</span>
              <textarea value={joinNote} onChange={e => setJoinNote(e.target.value)} rows={2} maxLength={1000} />
            </label>
          </>
        )}

        {mode === 'register' && policyRequired && (
          <div className="policy-block">
            <label className="policy-accept">
              <input type="checkbox" checked={policyAccepted} onChange={e => setPolicyAccepted(e.target.checked)} />
              <span>I have read and accept the{' '}
                <button type="button" className="link-btn" onClick={togglePolicy}>privacy policy</button>.
              </span>
            </label>
            {policyOpen && <pre className="policy-text">{policyText ?? '…'}</pre>}
          </div>
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
        <button type="submit" disabled={busy || (mode === 'register' && policyRequired && !policyAccepted)}>
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
      )}
    </div>
  )
}
