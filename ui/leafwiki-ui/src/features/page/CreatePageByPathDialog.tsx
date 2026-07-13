import BaseDialog from '@/components/BaseDialog'
import { FormInput } from '@/components/FormInput'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { ensurePage, lookupPath, PathLookupResult } from '@/lib/api/pages'
import { handleFieldErrors } from '@/lib/handleFieldErrors'
import { DIALOG_CREATE_PAGE_BY_PATH } from '@/lib/registries'
import { buildEditUrl } from '@/lib/routePath'
import { useDebounce } from '@/lib/useDebounce'
import { useTreeStore } from '@/stores/tree'
import { useConfigStore } from '@/stores/config'
import { Check, X } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { toast } from 'sonner'

const DIALOG_INPUT_ALLOWED_HOTKEYS = 'Enter'

type CreatePageByPathDialogProps = {
  initialPath?: string
  initialTitle?: string
  readOnlyPath?: boolean
  forwardToEditMode?: boolean
}

export function CreatePageByPathDialog({
  initialPath,
  initialTitle,
  readOnlyPath,
  forwardToEditMode,
}: CreatePageByPathDialogProps) {
  // Dialog state from zustand store
  const navigate = useNavigate()

  // read the last segment from the initial path as title
  const defaultTitle =
    initialTitle || initialPath?.split('/').pop() || 'unknown'

  const [title, setTitle] = useState(defaultTitle)
  const [path, setPath] = useState(initialPath || '')
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
  const [completedLookup, setCompletedLookup] = useState<{
    requestedPath: string
    result: PathLookupResult
  } | null>(null)
  const [lookupError, setLookupError] = useState<string | null>(null)
  const lookupRequest = useRef(0)
  const [loading, setLoading] = useState(false)
  const [draft, setDraft] = useState(false)
  const reloadTree = useTreeStore((s) => s.reloadTree)
  const authDisabled = useConfigStore((s) => s.authDisabled)

  const debouncedPath = useDebounce(path, 300)

  const runLookup = async (requestedPath: string) => {
    const request = ++lookupRequest.current
    setLookupError(null)
    try {
      const result = await lookupPath(requestedPath)
      if (request === lookupRequest.current && result) {
        setCompletedLookup({ requestedPath, result })
      }
    } catch {
      if (request === lookupRequest.current) {
        setCompletedLookup(null)
        setLookupError('Could not check whether this path can be created.')
      }
    }
  }

  const lookup =
    completedLookup?.requestedPath === path ? completedLookup.result : null

  const canUseLookup =
    lookup !== null && (lookup.exists || lookup.canCreate === true)
  const isCreateButtonDisabled = !title || !path || loading || !canUseLookup

  const handleCreate = async (): Promise<boolean> => {
    setLoading(true)
    setFieldErrors({})

    try {
      // Here you would call your API to create the page
      await ensurePage(path, title, draft && lookup?.exists === false)
      await reloadTree()
      // On success, close the dialog
      if (forwardToEditMode) {
        navigate(buildEditUrl(path))
      }

      toast.success('Page created successfully')
      return true // Close the dialog
    } catch (err: unknown) {
      console.warn(err)
      handleFieldErrors(err, setFieldErrors, 'Error creating page')
      return false // Keep the dialog open
    } finally {
      setLoading(false)
    }
  }

  // Run lookup for initial path if it exists
  useEffect(() => {
    lookupRequest.current += 1
    setCompletedLookup(null)
    setLookupError(null)
  }, [path])

  useEffect(() => {
    if (readOnlyPath && path) {
      // run lookup if the path exists!
      runLookup(path)
    }
  }, [path, readOnlyPath])

  // Run lookup when debounced path changes
  useEffect(() => {
    if (!readOnlyPath) {
      runLookup(debouncedPath)
    }
  }, [debouncedPath, readOnlyPath])

  const handleTitleChange = (val: string) => {
    setTitle(val)
  }

  return (
    <BaseDialog
      dialogTitle="Create a new page"
      dialogDescription="Please enter the title"
      dialogType={DIALOG_CREATE_PAGE_BY_PATH}
      testidPrefix="create-page-by-path-dialog"
      onClose={() => true}
      onConfirm={async (): Promise<boolean> => {
        return await handleCreate()
      }}
      cancelButton={{
        label: 'Cancel',
        variant: 'outline',
        disabled: loading,
        autoFocus: false,
      }}
      buttons={[
        {
          label: loading
            ? 'Creating...'
            : !forwardToEditMode
              ? 'Create'
              : 'Create & Edit',
          actionType: 'confirm',
          autoFocus: true,
          loading,
          disabled: isCreateButtonDisabled,
          variant: 'default',
        },
      ]}
    >
      <div>
        {lookupError && (
          <div className="create-page-by-path-dialog__alert" role="alert">
            <span>{lookupError}</span>{' '}
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={() => void runLookup(path)}
            >
              Retry
            </Button>
          </div>
        )}
        {lookup?.exists && (
          <div className="create-page-by-path-dialog__alert">
            A page already exists at this path.
          </div>
        )}
        {lookup && !lookup.exists && lookup.segments.length > 0 && (
          <>
            <strong className="create-page-by-path-dialog__lookup-title">
              Result of path lookup:
            </strong>
            <ul className="custom-scrollbar create-page-by-path-dialog__lookup-list">
              {lookup.segments.map((segment, index) => (
                <li
                  key={index}
                  className="create-page-by-path-dialog__lookup-item"
                >
                  {segment.exists ? (
                    <Check
                      className="create-page-by-path-dialog__lookup-item-icon--ok"
                      size={12}
                    />
                  ) : (
                    <X
                      className="create-page-by-path-dialog__lookup-item-icon--missing"
                      size={12}
                    />
                  )}{' '}
                  <span className="create-page-by-path-dialog__lookup-item-slug">
                    {segment.slug}
                  </span>{' '}
                  {segment.exists ? 'exists' : 'will be created'}
                </li>
              ))}
            </ul>
          </>
        )}
      </div>
      <div className="page-dialog__fields">
        <FormInput
          autoFocus={true}
          testid="create-page-by-path-title-input"
          label="Title"
          value={title}
          onChange={(val) => {
            handleTitleChange(val)
            setFieldErrors((prev) => ({ ...prev, title: '' }))
          }}
          placeholder="Page title"
          error={fieldErrors.title}
          allowedHotkeys={DIALOG_INPUT_ALLOWED_HOTKEYS}
        />
        <FormInput
          testid="create-page-by-path-path-input"
          label="Path"
          value={path}
          readOnly={readOnlyPath}
          onChange={(val) => {
            setPath(val)
            setFieldErrors((prev) => ({ ...prev, path: '' }))
          }}
          placeholder="Page path"
          error={fieldErrors.path}
          allowedHotkeys={DIALOG_INPUT_ALLOWED_HOTKEYS}
        />
        {!authDisabled &&
          lookup?.exists === false &&
          lookup.canCreate === true && (
            <label className="page-dialog__draft">
              <Checkbox
                checked={draft}
                onCheckedChange={(checked) => setDraft(checked === true)}
                data-testid="create-page-by-path-draft-checkbox"
              />
              Draft
            </label>
          )}
      </div>
    </BaseDialog>
  )
}
