import { useI18n } from '../i18n'

export function Spinner() {
  const { t } = useI18n()
  return <span className="spinner" aria-label={t('common.loading')}>⏳</span>
}
