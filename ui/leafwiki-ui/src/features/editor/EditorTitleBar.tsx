import { DIALOG_EDIT_PAGE_METADATA } from '@/lib/registries'
import { getParentWikiRoutePath, toWikiLookupPath } from '@/lib/wikiPath'
import { useAppMode } from '@/lib/useAppMode'
import { useIsMobile } from '@/lib/useIsMobile'
import { useDialogsStore } from '@/stores/dialogs'
import { useTreeStore } from '@/stores/tree'
import { Pencil } from 'lucide-react'
import { DraftBadge } from '@/components/DraftBadge'
import { TooltipWrapper } from '../../components/TooltipWrapper'
import {
  isDirtyState,
  isPendingEffectivelyDraft,
  usePageEditorStore,
} from './pageEditorStore'

export function EditorTitleBar() {
  const isMobile = useIsMobile()
  const appMode = useAppMode()
  const page = usePageEditorStore((state) => state.page)
  const title = usePageEditorStore((state) => state.title)
  const slug = usePageEditorStore((state) => state.slug)
  const draft = usePageEditorStore((state) => state.draft)
  const setTitle = usePageEditorStore((state) => state.setTitle)
  const setSlug = usePageEditorStore((state) => state.setSlug)
  const openDialog = useDialogsStore((s) => s.openDialog)
  const getPageByPath = useTreeStore((state) => state.getPageByPath)
  const dirty = usePageEditorStore(isDirtyState)

  const onEditClicked = () => {
    if (!page) return

    const parentId = () => {
      const parentPath = toWikiLookupPath(getParentWikiRoutePath(page.path))
      const p = getPageByPath(parentPath)
      if (!p) return ''
      return p.id
    }

    openDialog(DIALOG_EDIT_PAGE_METADATA, {
      title: title,
      currentId: page.id,
      itemKind: page.kind,
      slug: slug,
      parentId: parentId(),
      onChange: (title: string, slug: string) => {
        setTitle(title)
        setSlug(slug)
      },
      slugReadOnly: false,
    })
  }

  if (appMode !== 'edit') {
    return null
  }

  if (page == null) {
    return null
  }

  const effectiveDraft = isPendingEffectivelyDraft(page, draft)

  return (
    <div className="editor-title-bar">
      <button
        onClick={onEditClicked}
        className="editor-title-bar__button"
        data-testid="edit-page-metadata-button"
      >
        <TooltipWrapper label={title} side="top" align="start">
          {title && <span className="editor-title-bar__title">{title}</span>}
          {effectiveDraft && <DraftBadge inherited={!draft} />}
          <Pencil size={16} className="editor-title-bar__icon" />
          {dirty && !isMobile && (
            <span className="editor-title-bar__dirty-indicator">(Changes)</span>
          )}

          {dirty && isMobile && (
            <span className="editor-title-bar__dirty-indicator">*</span>
          )}
        </TooltipWrapper>
      </button>
      <span className="editor-title-bar__slug">{slug}</span>
    </div>
  )
}
