import { useTranslation } from 'react-i18next'

export function DraftBadge({ inherited = false }: { inherited?: boolean }) {
  const { t } = useTranslation('common')

  return (
    <span className="draft-badge">
      {inherited ? t('draft.inherited') : t('draft.label')}
    </span>
  )
}
