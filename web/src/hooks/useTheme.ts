import { useState, useEffect } from 'react'

export type ThemeId = 'dark' | 'dim' | 'light' | 'warm'

const STORAGE_KEY = 'budgieTheme'
const DEFAULT: ThemeId = 'dark'

export function useTheme() {
  const [theme, setThemeState] = useState<ThemeId>(() => {
    try { return (localStorage.getItem(STORAGE_KEY) as ThemeId) ?? DEFAULT } catch { return DEFAULT }
  })

  useEffect(() => {
    document.documentElement.dataset.theme = theme
  }, [theme])

  function setTheme(t: ThemeId) {
    try { localStorage.setItem(STORAGE_KEY, t) } catch { /* ignore */ }
    document.documentElement.dataset.theme = t
    setThemeState(t)
  }

  return { theme, setTheme }
}
