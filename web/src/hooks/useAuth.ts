import { useState, useCallback } from 'react'

export interface AuthState {
  token: string | null
  user: { id: string; name: string; role: string } | null
}

const STORAGE_KEY = 'budgie_token'

function loadFromStorage(): AuthState {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (raw) {
      const parsed = JSON.parse(raw) as AuthState
      return parsed
    }
  } catch { /* ignore */ }
  return { token: null, user: null }
}

export function useAuth() {
  const [auth, setAuth] = useState<AuthState>(loadFromStorage)

  const login = useCallback((token: string, user: AuthState['user']) => {
    const next = { token, user }
    setAuth(next)
    localStorage.setItem(STORAGE_KEY, JSON.stringify(next))
  }, [])

  const logout = useCallback(() => {
    setAuth({ token: null, user: null })
    localStorage.removeItem(STORAGE_KEY)
  }, [])

  return { auth, login, logout }
}
