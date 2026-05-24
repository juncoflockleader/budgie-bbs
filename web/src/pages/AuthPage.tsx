import { useState, FormEvent } from 'react'
import * as api from '../api/client'
import type { AuthState } from '../hooks/useAuth'

interface Props {
  onLogin: (token: string, user: AuthState['user']) => void
}

export function AuthPage({ onLogin }: Props) {
  const [mode, setMode] = useState<'login' | 'register'>('login')
  const [name, setName] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)

    const fn = mode === 'login' ? api.login : api.register
    const res = await fn(name, password)

    setBusy(false)
    if (res.error) {
      setError(res.error.message)
    } else if (res.data) {
      onLogin(res.data.token, res.data.user)
    }
  }

  return (
    <div className="auth-page">
      <h1 className="auth-title">🐦 Budgie BBS</h1>
      <form className="auth-form" onSubmit={submit}>
        <h2>{mode === 'login' ? 'Sign in' : 'Create account'}</h2>
        <label>
          Username
          <input
            autoFocus
            value={name}
            onChange={e => setName(e.target.value)}
            required
            minLength={1}
            maxLength={64}
          />
        </label>
        <label>
          Password
          <input
            type="password"
            value={password}
            onChange={e => setPassword(e.target.value)}
            required
            minLength={8}
          />
        </label>
        {error && <p className="error">{error}</p>}
        <button type="submit" disabled={busy}>
          {busy ? '…' : mode === 'login' ? 'Sign in' : 'Register'}
        </button>
        <button
          type="button"
          className="link-btn"
          onClick={() => { setMode(m => m === 'login' ? 'register' : 'login'); setError(null) }}
        >
          {mode === 'login' ? 'No account? Register' : 'Have an account? Sign in'}
        </button>
      </form>
    </div>
  )
}
