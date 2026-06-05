import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { en, type MessageKey } from './messages/en'
import { zhCN } from './messages/zh-CN'
import { zhTW } from './messages/zh-TW'

export type LocaleCode = 'en' | 'zh-CN' | 'zh-TW'

type Values = Record<string, string | number | boolean | null | undefined>

const dictionaries = {
  en,
  'zh-CN': zhCN,
  'zh-TW': zhTW,
} satisfies Record<LocaleCode, Record<MessageKey, string>>

const storageKey = 'budgie:ui-locale'

interface I18nContextValue {
  locale: LocaleCode
  setLocale: (locale: LocaleCode) => void
  t: (key: MessageKey, values?: Values) => string
}

const I18nContext = createContext<I18nContextValue | null>(null)

function detectInitialLocale(): LocaleCode {
  if (typeof window === 'undefined') return 'en'
  const saved = window.localStorage.getItem(storageKey)
  if (saved === 'en' || saved === 'zh-CN' || saved === 'zh-TW') return saved

  const browser = (window.navigator.language || '').toLowerCase()
  if (browser === 'zh' || browser.startsWith('zh-cn') || browser.startsWith('zh-hans')) return 'zh-CN'
  if (browser.startsWith('zh-tw') || browser.startsWith('zh-hant')) return 'zh-TW'
  return 'en'
}

function interpolate(template: string, values?: Values) {
  if (!values) return template
  return template.replace(/\{(\w+)\}/g, (_, name: string) => {
    const value = values[name]
    if (value === null || value === undefined) return `{${name}}`
    return String(value)
  })
}

export function I18nProvider({ children }: { children: ReactNode }) {
  const [locale, setLocaleState] = useState<LocaleCode>(() => detectInitialLocale())

  useEffect(() => {
    if (typeof document === 'undefined') return
    document.documentElement.lang = locale
  }, [locale])

  const value = useMemo<I18nContextValue>(() => ({
    locale,
    setLocale(next) {
      setLocaleState(next)
      window.localStorage.setItem(storageKey, next)
    },
    t(key, values) {
      const template = dictionaries[locale][key] ?? dictionaries.en[key] ?? key
      return interpolate(template, values)
    },
  }), [locale])

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>
}

export function useI18n() {
  const value = useContext(I18nContext)
  if (!value) {
    throw new Error('useI18n must be used inside I18nProvider')
  }
  return value
}
