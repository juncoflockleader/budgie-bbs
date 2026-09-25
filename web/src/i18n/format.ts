import type { LocaleCode } from './index'

export function formatDateTime(ms: number, locale: LocaleCode) {
  return new Intl.DateTimeFormat(locale, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date(ms))
}

export function formatCount(value: number, locale: LocaleCode) {
  return new Intl.NumberFormat(locale).format(value)
}
