import { useState, useCallback, useEffect } from 'react'
import * as api from '../api/client'

export interface AuthState {
  // token is retained for API compatibility but is always '' in cookie mode:
  // the HttpOnly session cookie carries auth, so client calls send no Bearer
  // header and rely on the same-origin cookie. Logged-in state is `user`.
  token: string
  user: { id: string; name: string; role: string; registrationStatus?: string } | null
}

export function useAuth() {
  const [auth, setAuth] = useState<AuthState>({ token: '', user: null })
  const [loading, setLoading] = useState(true)

  // Bootstrap the session from the HttpOnly cookie (no token in localStorage).
  useEffect(() => {
    // Migration cleanup: purge any token left by the old localStorage scheme so
    // it no longer lingers in the browser.
    try { localStorage.removeItem('budgie_token') } catch { /* ignore */ }
    let cancelled = false
    api.getMe().then(res => {
      if (cancelled) return
      setAuth({ token: '', user: res.data ?? null })
      setLoading(false)
    })
    return () => { cancelled = true }
  }, [])

  // login is called after a successful login/2FA: the server has already set the
  // session cookie, so we only record the user (never the token).
  const login = useCallback((_token: string, user: AuthState['user']) => {
    setAuth({ token: '', user })
  }, [])

  const logout = useCallback(() => {
    void api.logout()
    setAuth({ token: '', user: null })
  }, [])

  return { auth, login, logout, loading }
}
