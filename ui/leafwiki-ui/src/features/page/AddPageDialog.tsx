import BaseDialog, { BaseDialogConfirmButton } from '@/components/BaseDialog'
import { DraftBadge } from '@/components/DraftBadge'
import { FormInput } from '@/components/FormInput'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { createPage, isEffectivelyDraft, NODE_KIND_PAGE } from '@/lib/api/pages'
import { canManageDrafts } from '@/lib/drafts'
import { handleFieldErrors } from '@/lib/handleFieldErrors'
import i18next from '@/lib/i18n'
import { DIALOG_ADD_PAGE } from '@/lib/registries'
import { buildEditUrl } from '@/lib/routePath'
import { useTreeStore } from '@/stores/tree'
import { useConfigStore } from '@/stores/config'
import { useSessionStore } from '@/stores/session'
import { CalendarDays } from 'lucide-react'
import { useCallback, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router'
import { toast } from 'sonner'
import { SlugInputWithSuggestion } from './SlugInputWithSuggestion'

const DIALOG_INPUT_ALLOWED_HOTKEYS = 'Enter'

type AddPageDialogProps = {
  parentId: string
  nodeKind?: 'page' | 'section'
}

export function AddPageDialog({
  parentId,
  nodeKind = NODE_KIND_PAGE,
}: AddPageDialogProps) {
  const { t } = useTranslation('page')
  const [title, setTitle] = useState('')
  const [slug, setSlug] = useState('')
  const [loading, setLoading] = useState(false)
  const [draft, setDraft] = useState(false)
  const [slugLoading, setSlugLoading] = useState(false)
  const [lastSlugTitle, setLastSlugTitle] = useState('')
  const [slugTouched, setSlugTouched] = useState(false)
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
  const reloadTree = useTreeStore((s) => s.reloadTree)
  const authDisabled = useConfigStore((s) => s.authDisabled)
  const userRole = useSessionStore((s) => s.user?.role)
  const showDraft =
    nodeKind === NODE_KIND_PAGE && canManageDrafts(authDisabled, userRole)
  const parent = useTreeStore((s) => s.getPageById(parentId))
  const inheritedDraft = isEffectivelyDraft(parent)
  const parentPath = useTreeStore((s) => s.getPathById(parentId) || '')
  const navigate = useNavigate()
  const itemLabel =
    nodeKind === NODE_KIND_PAGE ? t('common.page') : t('common.section')
  const itemLabelCapitalized =
    nodeKind === NODE_KIND_PAGE
      ? t('common.pageCapitalized')
      : t('common.sectionCapitalized')

  const isCreateButtonDisabled =
    !title ||
    !slug ||
    loading ||
    (!slugTouched && (slugLoading || title !== lastSlugTitle))

  const handleTitleChange = (val: string) => {
    setTitle(val)
    setFieldErrors((prev) => ({ ...prev, title: '' }))
  }

  const resetForm = useCallback(() => {
    setTitle('')
    setSlug('')
    setSlugTouched(false)
    setLastSlugTitle('')
    setFieldErrors({})
    setLoading(false)
    setDraft(false)
  }, [])

  const handleSlugChange = useCallback((val: string) => {
    setSlug(val)
    setFieldErrors((prev) => ({ ...prev, slug: '' }))
  }, [])

  const handleCreate = useCallback(
    async (
      redirect: boolean = true,
      nodeKind?: 'page' | 'section',
    ): Promise<boolean> => {
      if (!nodeKind) nodeKind = NODE_KIND_PAGE
      if (!title) return false

      if (!slug) {
        toast.error(t('addDialog.slugNotGenerated'))
        return false
      }

      if (!slugTouched && (slugLoading || title !== lastSlugTitle)) {
        toast.warning(t('addDialog.slugStillGenerating'))
        return false
      }

      setLoading(true)
      setFieldErrors({})
      try {
        await createPage({
          title,
          slug,
          parentId,
          kind: nodeKind,
          draft: showDraft && draft,
        })
        toast.success(
          t('addDialog.createdToast', { item: itemLabelCapitalized }),
        )
        await reloadTree()
        if (redirect) {
          const fullPath = parentPath !== '' ? `${parentPath}/${slug}` : slug
          navigate(buildEditUrl(fullPath))
        }
        return true
      } catch (err: unknown) {
        console.warn(err)
        handleFieldErrors(
          err,
          setFieldErrors,
          t('addDialog.createErrorFallback', { item: itemLabel }),
        )
        return false
      } finally {
        setLoading(false)
      }
    },
    [
      title,
      slug,
      parentId,
      slugTouched,
      slugLoading,
      lastSlugTitle,
      reloadTree,
      parentPath,
      navigate,
      itemLabel,
      itemLabelCapitalized,
      showDraft,
      draft,
      t,
    ],
  )

  const handleCancel = useCallback(() => {
    resetForm()
    return true
  }, [resetForm])

  const buttons = useMemo(() => {
    const b: BaseDialogConfirmButton[] = [
      {
        label: t('addDialog.create'),
        actionType: 'no-redirect',
        autoFocus: true,
        loading,
        disabled: isCreateButtonDisabled,
        variant: nodeKind === NODE_KIND_PAGE ? 'secondary' : 'default',
      },
    ]
    if (nodeKind === NODE_KIND_PAGE) {
      b.push({
        label: t('addDialog.createAndEdit', { item: itemLabelCapitalized }),
        actionType: 'confirm',
        autoFocus: false,
        loading,
        disabled: isCreateButtonDisabled,
        variant: 'default',
      })
    }
    return b
  }, [isCreateButtonDisabled, loading, nodeKind, itemLabelCapitalized, t])

  return (
    <BaseDialog
      dialogTitle={
        nodeKind === 'page'
          ? t('addDialog.titlePage')
          : t('addDialog.titleSection')
      }
      dialogDescription={
        nodeKind === 'page'
          ? t('addDialog.descriptionPage')
          : t('addDialog.descriptionSection')
      }
      dialogType={DIALOG_ADD_PAGE}
      onClose={handleCancel}
      onConfirm={async (actionType: string): Promise<boolean> => {
        return await handleCreate(actionType !== 'no-redirect', nodeKind)
      }}
      testidPrefix="add-page-dialog"
      cancelButton={{
        label: t('common.cancel'),
        variant: 'outline',
        disabled: loading,
        autoFocus: false,
      }}
      buttons={buttons}
    >
      <div className="page-dialog__fields">
        <div className="page-dialog__title-row">
          <FormInput
            autoFocus={true}
            label={t('addDialog.titleLabel')}
            value={title}
            onChange={(val) => {
              handleTitleChange(val)
              setFieldErrors((prev) => ({ ...prev, title: '' }))
            }}
            testid="add-page-title-input"
            placeholder={t('addDialog.titlePlaceholder', {
              item: itemLabelCapitalized,
            })}
            error={fieldErrors.title}
            allowedHotkeys={DIALOG_INPUT_ALLOWED_HOTKEYS}
          />
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="page-dialog__date-btn"
            title={i18next.t('addPageDialog.dateTitleTooltip', {
              ns: 'editor',
            })}
            onClick={() =>
              handleTitleChange(new Date().toISOString().slice(0, 10))
            }
          >
            <CalendarDays size={15} />
          </Button>
        </div>
        <SlugInputWithSuggestion
          title={title}
          slug={slug}
          testid="add-page-slug-input"
          parentId={parentId}
          onSlugChange={handleSlugChange}
          onSlugTouchedChange={setSlugTouched}
          onSlugLoadingChange={setSlugLoading}
          onLastSlugTitleChange={setLastSlugTitle}
          error={fieldErrors.slug}
          allowedHotkeys={DIALOG_INPUT_ALLOWED_HOTKEYS}
        />
        {showDraft && (
          <div className="page-dialog__draft">
            {inheritedDraft && <DraftBadge inherited />}
            <label className="page-dialog__draft">
              <Checkbox
                checked={draft}
                onCheckedChange={(checked) => setDraft(checked === true)}
                data-testid="add-page-draft-checkbox"
              />
              {inheritedDraft
                ? t('addDialog.keepDraftWhenParentPublished')
                : t('addDialog.draft')}
            </label>
          </div>
        )}
      </div>
      <span className="dialog__path" data-testid="add-page-path-display">
        {t('addDialog.pathPrefix')} {parentPath !== '' && `${parentPath}/`}
        {slug && `${slug}`}
      </span>
    </BaseDialog>
  )
}
