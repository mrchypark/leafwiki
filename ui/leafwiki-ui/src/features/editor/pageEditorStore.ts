// zustand store to manage the PageEditor state
// e.g. loading, error, page, dirty, ...

import {
  applyPageRefactor,
  getPageByPath,
  Page,
  previewPageRefactor,
  updatePage,
  updatePageDraft,
} from '@/lib/api/pages'
import { isPageNotFoundError, mapApiError } from '@/lib/api/errors'
import { useConfigStore } from '@/stores/config'
import { useTreeStore } from '@/stores/tree'
import { create } from 'zustand'
import { useLinkStatusStore } from '../links/linkstatus_store'
import { confirmPageRefactor } from '../page/pageRefactorDialogState'
import { useProgressbarStore } from '../progressbar/progressbarStore'
import { useViewerStore } from '../viewer/viewer'
import {
  EditorFrontmatterField,
  validateEditorFrontmatterMetadata,
} from './frontmatter'

export interface PageEditorState {
  title: string // current title in the editor
  slug: string // current slug in the editor
  content: string // current markdown content in the editor
  draft: boolean
  tags: string[] // convenient tag editor state
  frontmatterFields: EditorFrontmatterField[]
  frontmatterUnsupported: string
  frontmatterErrors: Record<string, string>
  error: string | null // error message, if any
  isLoading: boolean
  notFound: boolean
  page: Page | null // current page being edited
  initialPage: Page | null // initial page data when loaded
  setTitle: (title: string) => void // set the current title
  setSlug: (slug: string) => void // set the current slug
  setContent: (content: string) => void // set the current markdown content
  setDraft: (draft: boolean) => void
  setTags: (tags: string[]) => void
  setFrontmatterFields: (fields: EditorFrontmatterField[]) => void
  setFrontmatterErrors: (errors: Record<string, string>) => void
  setError: (error: string | null) => void // set the error message
  setPage: (page: Page | null) => void // set the current page
  savePage: (options?: { silent?: boolean }) => Promise<Page | null | undefined> // save the current page
  forceOverwrite: () => Promise<Page | null | undefined> // re-fetch server version, then save
  loadPageData: (path: string) => Promise<void> // load page data by path
  resetEditorState: () => void // clear the store back to its pristine (no page loaded) shape
}

function tagsChanged(current: string[], original: string[]): boolean {
  if (current.length !== original.length) return true
  const a = [...current].sort()
  const b = [...original].sort()
  return a.some((v, i) => v !== b[i])
}

function propertiesChanged(
  fields: EditorFrontmatterField[],
  original: Record<string, unknown>,
): boolean {
  const editable = fields.filter((f) => !f.internal && f.type === 'text')
  const origKeys = Object.keys(original)
  if (editable.length !== origKeys.length) return true
  return editable.some((f) => String(original[f.key] ?? '') !== f.value)
}

function buildEditableProperties(
  fields: EditorFrontmatterField[],
): Record<string, string> {
  const properties: Record<string, string> = {}

  for (const field of fields) {
    if (!field.internal && field.type === 'text' && field.key) {
      properties[field.key] = field.value
    }
  }

  return properties
}

export const isDirtyState = (s: PageEditorState) => {
  const { page, title, slug, content, draft, tags, frontmatterFields } = s
  if (!page) return false
  return (
    page.title !== title ||
    page.slug !== slug ||
    page.content !== content ||
    Boolean(page.draft) !== draft ||
    tagsChanged(tags, page.tags ?? []) ||
    propertiesChanged(frontmatterFields, page.properties ?? {})
  )
}

// Module-level mutex: prevents concurrent auto-saves from stacking.
// Manual saves (silent=false) bypass this so Ctrl+S is never blocked by an in-flight auto-save.
let isSavingMutex = false

let loadController: AbortController | null = null

export const usePageEditorStore = create<PageEditorState>((set, get) => ({
  error: null,
  isLoading: false,
  notFound: false,
  page: null,
  title: '',
  path: '',
  slug: '',
  content: '',
  draft: false,
  tags: [],
  frontmatterFields: [],
  frontmatterUnsupported: '',
  frontmatterErrors: {},
  lastStoredPage: null,
  initialPage: null,
  setTitle: (title) => set({ title }),
  setSlug: (slug) => set({ slug }),
  setContent: (content) => set({ content }),
  setDraft: (draft) => set({ draft }),
  setTags: (tags) =>
    set((state) => {
      const nextErrors = { ...state.frontmatterErrors }
      delete nextErrors.tags
      return { tags, frontmatterErrors: nextErrors }
    }),
  setFrontmatterFields: (frontmatterFields) =>
    set((state) => {
      const nextErrors = { ...state.frontmatterErrors }
      for (const key of Object.keys(nextErrors)) {
        if (key.startsWith('properties.')) {
          delete nextErrors[key]
        }
      }

      return {
        frontmatterFields,
        frontmatterErrors: nextErrors,
      }
    }),
  setFrontmatterErrors: (frontmatterErrors) => set({ frontmatterErrors }),
  setError: (error) => set({ error }),
  setPage: (page) => set({ page }),
  savePage: async (options?: { silent?: boolean }) => {
    const {
      page,
      initialPage,
      title,
      slug,
      content,
      draft,
      tags,
      frontmatterFields,
    } = get()
    if (!page || !isDirtyState(get())) return

    const isCurrentSave = () =>
      get().initialPage === initialPage && get().page?.id === page.id

    const frontmatterErrors = validateEditorFrontmatterMetadata(
      tags,
      frontmatterFields,
    )
    if (Object.keys(frontmatterErrors).length > 0) {
      set({ frontmatterErrors })
      throw new Error('Please fix metadata errors before saving.')
    }

    // Only block concurrent auto-saves; manual saves always proceed
    if (isSavingMutex && options?.silent) return
    isSavingMutex = true

    const properties = buildEditableProperties(frontmatterFields)

    try {
      if (!options?.silent) useProgressbarStore.getState().setLoading(true)
      set({ frontmatterErrors: {} })
      const titleChanged = page.title !== title
      const slugChanged = page.slug !== slug
      const wasDraft = Boolean(page.draft)
      const draftChanged = wasDraft !== draft
      const enableLinkRefactor = useConfigStore.getState().enableLinkRefactor
      const frontmatterChanged =
        tagsChanged(tags, page.tags ?? []) ||
        propertiesChanged(frontmatterFields, page.properties ?? {})

      const applyServerPage = (serverPage: Page) => {
        set((state) => {
          if (
            state.initialPage !== initialPage ||
            !state.page ||
            state.page.id !== serverPage.id
          ) {
            return {}
          }
          return {
            page: {
              ...state.page,
              ...serverPage,
              content: serverPage.content ?? state.page.content,
              tags: serverPage.tags ?? state.page.tags,
              properties: serverPage.properties ?? state.page.properties,
            },
          }
        })
      }

      let rewriteLinks = false
      if (slugChanged && enableLinkRefactor) {
        const preview = await previewPageRefactor(page.id, {
          kind: 'rename',
          title,
          slug,
        })
        if (!isCurrentSave()) return
        const confirmedRewriteLinks = await confirmPageRefactor(preview, {
          allowSkipRewrite: true,
        })
        if (!isCurrentSave()) return
        if (confirmedRewriteLinks === null) {
          return null
        }
        rewriteLinks = confirmedRewriteLinks
      }

      let workingPage = page
      if (draftChanged && draft) {
        workingPage = await updatePageDraft(page.id, page.version, true)
        if (!isCurrentSave()) return
        applyServerPage(workingPage)
        await useTreeStore.getState().reloadTree()
        if (!isCurrentSave()) return
      }

      let updatedPage: Page | null = null

      if (slugChanged && enableLinkRefactor) {
        updatedPage = await applyPageRefactor(workingPage.id, {
          kind: 'rename',
          version: workingPage.version,
          title,
          slug,
          content,
          rewriteLinks,
        })
        if (!isCurrentSave()) return

        if (updatedPage && frontmatterChanged) {
          updatedPage = await updatePage(
            updatedPage.id,
            updatedPage.version,
            title,
            slug,
            content,
            tags,
            properties,
          )
          if (!isCurrentSave()) return
        }
      } else {
        updatedPage = await updatePage(
          workingPage.id,
          workingPage.version,
          title,
          slug,
          content,
          tags,
          properties,
        )
        if (!isCurrentSave()) return
      }

      if (!updatedPage) {
        throw new Error('Page update returned no page.')
      }
      applyServerPage(updatedPage)

      if (draftChanged && !draft) {
        updatedPage = await updatePageDraft(
          updatedPage.id,
          updatedPage.version,
          false,
        )
        if (!isCurrentSave()) return
        applyServerPage(updatedPage)
      }

      const nextTags = updatedPage.tags ?? tags
      const nextProperties = updatedPage.properties ?? properties

      // Keep the local page snapshot canonical after save so metadata-only
      // updates do not remain dirty when the API omits empty collections.
      set((state) => {
        if (
          state.initialPage !== initialPage ||
          !state.page ||
          state.page.id !== page.id
        ) {
          return {}
        }
        state.page.title = updatedPage.title
        state.page.slug = updatedPage.slug
        state.page.content = updatedPage.content
        state.page.path = updatedPage.path
        state.page.version = updatedPage.version
        state.page.tags = nextTags
        state.page.properties = nextProperties
        state.page.draft = updatedPage.draft ?? draft

        return {
          page: state.page,
          tags: nextTags,
          frontmatterFields: state.frontmatterFields.map((field) => {
            if (field.internal || field.type !== 'text') {
              return field
            }

            return {
              ...field,
              value: nextProperties[field.key] ?? field.value,
            }
          }),
        }
      })

      // sync tree: full reload on structural changes, version-only patch otherwise
      if (titleChanged || slugChanged || draftChanged) {
        await useTreeStore.getState().reloadTree()
        if (!isCurrentSave()) return
      } else if (updatedPage?.id && updatedPage?.version) {
        useTreeStore
          .getState()
          .patchNodeVersion(updatedPage.id, updatedPage.version)
      }

      const viewerPage = useViewerStore.getState().page
      if (viewerPage?.id && viewerPage.id === updatedPage?.id && updatedPage) {
        useViewerStore.setState({
          page: updatedPage,
          notFound: false,
          error: null,
        })
      }

      // reload backlinks
      const editorPageID = get().page?.id
      if (editorPageID) {
        const fetchLinkStatusForPage =
          useLinkStatusStore.getState().fetchLinkStatusForPage
        await fetchLinkStatusForPage(editorPageID)
        if (!isCurrentSave()) return
      }

      return updatedPage
    } finally {
      isSavingMutex = false
      if (!options?.silent) useProgressbarStore.getState().setLoading(false)
    }
  },
  forceOverwrite: async () => {
    const { page, initialPage } = get()
    if (!page?.path) return

    const fresh = await getPageByPath(page.path)
    if (get().initialPage !== initialPage || get().page?.id !== page.id) return
    set((state) => {
      if (state.initialPage !== initialPage || state.page?.id !== page.id) {
        return {}
      }
      state.page.version = fresh.version
      state.page.draft = fresh.draft
      state.page.effectiveDraft = fresh.effectiveDraft
      return { page: state.page }
    })
    return get().savePage()
  },
  loadPageData: async (path: string) => {
    loadController?.abort()
    loadController = new AbortController()
    const { signal } = loadController

    useProgressbarStore.getState().setLoading(true)
    set({
      error: null,
      isLoading: true,
      notFound: false,
      page: null,
      initialPage: null,
      frontmatterErrors: {},
    })
    try {
      const page = await getPageByPath(path, signal)
      const fields: EditorFrontmatterField[] = Object.entries(
        page.properties ?? {},
      ).map(([key, value]) => ({
        key,
        value: String(value ?? ''),
        type: 'text' as const,
      }))
      set({
        page,
        initialPage: { ...page },
        notFound: false,
        title: page.title,
        slug: page.slug,
        content: page.content,
        draft: Boolean(page.draft),
        tags: page.tags ?? [],
        frontmatterFields: fields,
        frontmatterUnsupported: '',
      })
    } catch (err) {
      if (signal.aborted) return

      if (isPageNotFoundError(err)) {
        set({
          error: null,
          notFound: true,
        })
        return
      }

      const mapped = mapApiError(err, 'An unknown error occurred')
      set({
        error: mapped.message,
        notFound: false,
      })
    } finally {
      if (!signal.aborted) {
        set({ isLoading: false })
        useProgressbarStore.getState().setLoading(false)
      }
    }
  },
  // Called when PageEditor unmounts so `page` (and thus currentEditorPageId
  // reads elsewhere, e.g. TreeNodeActionsMenu's rename/delete guards) doesn't
  // keep pointing at the last-edited page indefinitely after the editor closes.
  resetEditorState: () => {
    const wasLoading = get().isLoading
    loadController?.abort()
    if (wasLoading) useProgressbarStore.getState().setLoading(false)
    set({
      error: null,
      isLoading: false,
      notFound: false,
      page: null,
      title: '',
      slug: '',
      content: '',
      draft: false,
      tags: [],
      frontmatterFields: [],
      frontmatterUnsupported: '',
      frontmatterErrors: {},
      initialPage: null,
    })
  },
}))
