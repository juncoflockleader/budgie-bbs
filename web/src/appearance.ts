import type { SiteAppearance } from './api/types'

const THEME_KEY = 'budgieTheme'

function hexToRgba(hex: string, alpha: number): string {
  let h = hex.replace('#', '')
  if (h.length === 3) h = h.split('').map(c => c + c).join('')
  const n = parseInt(h, 16)
  const r = (n >> 16) & 255
  const g = (n >> 8) & 255
  const b = n & 255
  return `rgba(${r}, ${g}, ${b}, ${alpha})`
}

// applyAppearance applies admin-configured branding to the live page: document
// title, accent color override, and the default theme for visitors who have not
// chosen one. Safe to call repeatedly (e.g. after an admin update).
export function applyAppearance(a: SiteAppearance, setTheme?: (t: string) => void): void {
  const root = document.documentElement
  document.title = a.siteTitle || 'Budgie BBS'

  if (a.accentColor) {
    root.style.setProperty('--c-accent', a.accentColor)
    root.style.setProperty('--c-accent-sub', hexToRgba(a.accentColor, 0.14))
  } else {
    root.style.removeProperty('--c-accent')
    root.style.removeProperty('--c-accent-sub')
  }

  try {
    if (a.defaultTheme && !localStorage.getItem(THEME_KEY)) {
      if (setTheme) setTheme(a.defaultTheme)
      else root.dataset.theme = a.defaultTheme
    }
  } catch {
    /* ignore */
  }

  // Notify React-rendered chrome (sidebar title, banner) so an admin save
  // applies live without a page reload.
  window.dispatchEvent(new CustomEvent<SiteAppearance>('budgie:appearance', { detail: a }))
}
