import { useTranslation } from 'react-i18next'

export function DraftBadge() {
  const { t } = useTranslation('editor')

  return (
    <span className="tree-node__draft-badge">{t('frontmatter.draft')}</span>
  )
}
