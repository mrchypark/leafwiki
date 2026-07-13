import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Page } from '@/lib/api/pages'
import {
  applyPageRefactor,
  getPageByPath,
  previewPageRefactor,
  updatePage,
} from '@/lib/api/pages'
import { useConfigStore } from '@/stores/config'
import { useDialogsStore } from '@/stores/dialogs'
import { useSessionStore } from '@/stores/session'
import { useTreeStore } from '@/stores/tree'
import { useProgressbarStore } from '../progressbar/progressbarStore'
import { useLinkStatusStore } from '../links/linkstatus_store'
import { useViewerStore } from '../viewer/viewer'
import { isDirtyState, usePageEditorStore } from './pageEditorStore'

vi.mock('@/lib/api/pages', async () => {
  const actual =
    await vi.importActual<typeof import('@/lib/api/pages')>('@/lib/api/pages')
  return {
    ...actual,
    applyPageRefactor: vi.fn(),
    getPageByPath: vi.fn(),
    previewPageRefactor: vi.fn(),
    updatePage: vi.fn(),
  }
})

const fakePage: Page = {
  id: 'page-1',
  title: 'Getting Started',
  slug: 'getting-started',
  path: 'docs/getting-started',
  kind: 'page',
  content: 'Hello world',
  version: 'v1',
  tags: ['guide'],
  properties: { owner: 'alice' },
  draft: false,
} as Page

describe('pageEditorStore.resetEditorState', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(updatePage).mockReset()
    vi.mocked(getPageByPath).mockReset()
    vi.mocked(previewPageRefactor).mockReset()
    vi.mocked(applyPageRefactor).mockReset()
    usePageEditorStore.setState(usePageEditorStore.getInitialState())
    useProgressbarStore.setState({ loading: false })
    useConfigStore.setState({ enableLinkRefactor: false })
    useSessionStore.setState({ user: null })
    useTreeStore.setState({
      reloadTree: vi.fn().mockResolvedValue(undefined),
      patchNodeVersion: vi.fn(),
    })
    useViewerStore.setState({ page: null })
    useLinkStatusStore.setState({
      fetchLinkStatusForPage: vi.fn().mockResolvedValue(undefined),
    })
  })

  it('clears page back to null so stale currentEditorPageId reads disappear', () => {
    usePageEditorStore.setState({
      page: fakePage,
      initialPage: fakePage,
      title: fakePage.title,
      slug: fakePage.slug,
      content: fakePage.content,
      draft: true,
      tags: fakePage.tags,
      frontmatterFields: [{ key: 'owner', value: 'alice', type: 'text' }],
      notFound: true,
      error: 'stale error',
    })

    usePageEditorStore.getState().resetEditorState()

    const state = usePageEditorStore.getState()
    expect(state.page).toBeNull()
    expect(state.initialPage).toBeNull()
    expect(state.title).toBe('')
    expect(state.slug).toBe('')
    expect(state.content).toBe('')
    expect(state.draft).toBe(false)
    expect(state.tags).toEqual([])
    expect(state.frontmatterFields).toEqual([])
    expect(state.notFound).toBe(false)
    expect(state.error).toBeNull()
  })

  it('leaves the store in a clean state when nothing was ever loaded', () => {
    usePageEditorStore.getState().resetEditorState()

    expect(usePageEditorStore.getState().page).toBeNull()
  })

  it('clears the global loading indicator when a pending editor load is reset', () => {
    usePageEditorStore.setState({ isLoading: true })
    useProgressbarStore.setState({ loading: true })

    usePageEditorStore.getState().resetEditorState()

    expect(usePageEditorStore.getState().isLoading).toBe(false)
    expect(useProgressbarStore.getState().loading).toBe(false)
  })

  it('preserves externally owned loading when an idle editor is reset', () => {
    usePageEditorStore.setState({ isLoading: false })
    useProgressbarStore.setState({ loading: true })

    usePageEditorStore.getState().resetEditorState()

    expect(useProgressbarStore.getState().loading).toBe(true)
  })

  it('treats changing draft status as an unsaved edit', () => {
    usePageEditorStore.setState({
      page: fakePage,
      initialPage: fakePage,
      title: fakePage.title,
      slug: fakePage.slug,
      content: fakePage.content,
      tags: fakePage.tags,
      frontmatterFields: [{ key: 'owner', value: 'alice', type: 'text' }],
      draft: true,
    })

    expect(isDirtyState(usePageEditorStore.getState())).toBe(true)
  })
})

function editPage(page: Page, patch: Partial<PageEditorStateForTest>) {
  usePageEditorStore.setState({
    page: { ...page },
    initialPage: { ...page },
    title: patch.title ?? page.title,
    slug: patch.slug ?? page.slug,
    content: patch.content ?? page.content,
    draft: patch.draft ?? Boolean(page.draft),
    tags: patch.tags ?? page.tags ?? [],
    frontmatterFields: Object.entries(
      patch.properties ?? page.properties ?? {},
    ).map(([key, value]) => ({ key, value, type: 'text' as const })),
  })
}

type PageEditorStateForTest = {
  title: string
  slug: string
  content: string
  draft: boolean
  tags: string[]
  properties: Record<string, string>
}

describe('pageEditorStore.savePage draft visibility ordering', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(updatePage).mockReset()
    vi.mocked(getPageByPath).mockReset()
    vi.mocked(previewPageRefactor).mockReset()
    vi.mocked(applyPageRefactor).mockReset()
    usePageEditorStore.getState().resetEditorState()
    useProgressbarStore.setState({ loading: false })
    useConfigStore.setState({ enableLinkRefactor: true })
    useDialogsStore.setState({ dialogType: null, dialogProps: null })
    useSessionStore.setState({
      user: {
        id: 'editor-1',
        username: 'editor',
        email: 'editor@example.test',
        role: 'editor',
      },
    })
    useTreeStore.setState({
      reloadTree: vi.fn().mockResolvedValue(undefined),
      patchNodeVersion: vi.fn(),
    })
    useViewerStore.setState({ page: null })
    useLinkStatusStore.setState({
      fetchLinkStatusForPage: vi.fn().mockResolvedValue(undefined),
    })
  })

  it('atomically hides a public page while renaming draft content and saving metadata', async () => {
    const calls: string[] = []
    const refactoredPage = {
      ...fakePage,
      title: 'Renamed',
      slug: 'renamed',
      path: 'docs/renamed',
      content: 'private content',
      draft: true,
      tags: ['private'],
      properties: { owner: 'bob' },
      version: 'v2',
    }

    vi.mocked(previewPageRefactor).mockImplementation(async () => {
      calls.push('preview')
      return { counts: { affectedPages: 0 }, warnings: [] } as never
    })
    vi.mocked(applyPageRefactor).mockImplementation(async () => {
      calls.push('refactor')
      return refactoredPage
    })

    editPage(fakePage, {
      title: 'Renamed',
      slug: 'renamed',
      content: 'private content',
      draft: true,
      tags: ['private'],
      properties: { owner: 'bob' },
    })

    await usePageEditorStore.getState().savePage()

    expect(calls).toEqual(['preview', 'refactor'])
    expect(updatePage).not.toHaveBeenCalled()
    expect(applyPageRefactor).toHaveBeenCalledWith(fakePage.id, {
      kind: 'rename',
      version: 'v1',
      title: 'Renamed',
      slug: 'renamed',
      content: 'private content',
      tags: ['private'],
      properties: { owner: 'bob' },
      draft: true,
      rewriteLinks: false,
    })
  })

  it('publishes and renames a draft in one refactor request', async () => {
    const calls: string[] = []
    const draftPage = { ...fakePage, draft: true }
    const refactoredPage = {
      ...draftPage,
      title: 'Published',
      slug: 'published',
      path: 'docs/published',
      content: 'ready',
      draft: false,
      version: 'v2',
    }
    vi.mocked(previewPageRefactor).mockImplementation(async () => {
      calls.push('preview')
      return { counts: { affectedPages: 0 }, warnings: [] } as never
    })
    vi.mocked(applyPageRefactor).mockImplementation(async () => {
      calls.push('refactor')
      return refactoredPage
    })

    editPage(draftPage, {
      title: 'Published',
      slug: 'published',
      content: 'ready',
      draft: false,
      tags: draftPage.tags ?? [],
      properties: draftPage.properties ?? {},
    })

    await usePageEditorStore.getState().savePage()

    expect(calls).toEqual(['preview', 'refactor'])
    expect(updatePage).not.toHaveBeenCalled()
    expect(applyPageRefactor).toHaveBeenCalledWith(draftPage.id, {
      kind: 'rename',
      version: 'v1',
      title: 'Published',
      slug: 'published',
      content: 'ready',
      tags: draftPage.tags,
      properties: draftPage.properties,
      draft: false,
      rewriteLinks: false,
    })
  })

  it('discards a delayed atomic refactor after the editor scope is reset', async () => {
    let resolveRefactor!: (page: Page) => void
    vi.mocked(previewPageRefactor).mockResolvedValue({
      counts: { affectedPages: 0 },
      warnings: [],
    } as never)
    vi.mocked(applyPageRefactor).mockReturnValue(
      new Promise<Page>((resolve) => {
        resolveRefactor = resolve
      }),
    )
    editPage(fakePage, { slug: 'renamed', draft: true })

    const save = usePageEditorStore.getState().savePage()
    await vi.waitFor(() => expect(applyPageRefactor).toHaveBeenCalledOnce())
    usePageEditorStore.getState().resetEditorState()
    resolveRefactor({
      ...fakePage,
      slug: 'renamed',
      path: 'docs/renamed',
      draft: true,
      version: 'v2',
    })
    await save

    expect(usePageEditorStore.getState().page).toBeNull()
    expect(useTreeStore.getState().reloadTree).not.toHaveBeenCalled()
    expect(useTreeStore.getState().patchNodeVersion).not.toHaveBeenCalled()
    expect(
      useLinkStatusStore.getState().fetchLinkStatusForPage,
    ).not.toHaveBeenCalled()
  })

  it('does not change visibility when refactor preview fails before approval', async () => {
    vi.mocked(previewPageRefactor).mockRejectedValue(
      new Error('preview unavailable'),
    )

    editPage(fakePage, {
      slug: 'renamed',
      content: 'private content',
      draft: true,
    })

    await expect(usePageEditorStore.getState().savePage()).rejects.toThrow(
      'preview unavailable',
    )

    expect(updatePage).not.toHaveBeenCalled()
    expect(applyPageRefactor).not.toHaveBeenCalled()
    expect(usePageEditorStore.getState().page).toMatchObject({
      id: fakePage.id,
      version: 'v1',
      draft: false,
    })
    expect(useTreeStore.getState().reloadTree).not.toHaveBeenCalled()
  })

  it('does not change visibility when the user cancels the refactor', async () => {
    vi.mocked(previewPageRefactor).mockResolvedValue({
      counts: { affectedPages: 1 },
      warnings: [],
    } as never)

    editPage(fakePage, {
      slug: 'renamed',
      content: 'private content',
      draft: true,
    })

    const save = usePageEditorStore.getState().savePage()
    await vi.waitFor(() => {
      expect(useDialogsStore.getState().dialogProps).not.toBeNull()
    })
    const onResolve = useDialogsStore.getState().dialogProps?.onResolve as (
      rewriteLinks: boolean | null,
    ) => void
    onResolve(null)

    await expect(save).resolves.toBeNull()
    expect(updatePage).not.toHaveBeenCalled()
    expect(applyPageRefactor).not.toHaveBeenCalled()
    expect(usePageEditorStore.getState().page).toMatchObject({
      id: fakePage.id,
      version: 'v1',
      draft: false,
    })
  })

  it('discards a delayed save after the editor visibility scope is reset', async () => {
    let resolveSave!: (page: Page) => void
    const delayedSave = new Promise<Page>((resolve) => {
      resolveSave = resolve
    })
    vi.mocked(updatePage).mockReturnValue(delayedSave)
    useConfigStore.setState({ enableLinkRefactor: false })
    editPage(fakePage, { content: 'private content' })

    const save = usePageEditorStore.getState().savePage()
    usePageEditorStore.getState().resetEditorState()
    useSessionStore.setState({
      user: {
        id: 'editor-2',
        username: 'other',
        email: 'other@example.test',
        role: 'editor',
      },
    })
    useProgressbarStore.setState({ loading: true })
    resolveSave({ ...fakePage, content: 'private content', draft: true })
    await save

    expect(usePageEditorStore.getState().page).toBeNull()
    expect(useTreeStore.getState().reloadTree).not.toHaveBeenCalled()
    expect(useTreeStore.getState().patchNodeVersion).not.toHaveBeenCalled()
    expect(
      useLinkStatusStore.getState().fetchLinkStatusForPage,
    ).not.toHaveBeenCalled()
    expect(useViewerStore.getState().page).toBeNull()
    expect(useProgressbarStore.getState().loading).toBe(true)
  })

  const staleContextChanges = [
    ['reset', () => usePageEditorStore.getState().resetEditorState()],
    ['logout', () => useSessionStore.setState({ user: null })],
    [
      'page switch',
      () => editPage({ ...fakePage, id: 'page-2' }, { content: 'other edit' }),
    ],
  ] as const

  it.each(staleContextChanges)(
    'does not propagate a delayed save failure after %s',
    async (_name, changeContext) => {
      let rejectSave!: (error: Error) => void
      vi.mocked(updatePage).mockReturnValue(
        new Promise<Page>((_resolve, reject) => {
          rejectSave = reject
        }),
      )
      useConfigStore.setState({ enableLinkRefactor: false })
      editPage(fakePage, { content: 'private content' })

      const save = usePageEditorStore.getState().savePage()
      changeContext()
      rejectSave(new Error('stale save failure'))

      await expect(save).resolves.toBeUndefined()
    },
  )

  it.each(staleContextChanges)(
    'does not propagate a delayed force-overwrite failure after %s',
    async (_name, changeContext) => {
      let rejectLoad!: (error: Error) => void
      vi.mocked(getPageByPath).mockReturnValue(
        new Promise<Page>((_resolve, reject) => {
          rejectLoad = reject
        }),
      )
      editPage(fakePage, { content: 'private content' })

      const overwrite = usePageEditorStore.getState().forceOverwrite()
      changeContext()
      rejectLoad(new Error('stale force-overwrite failure'))

      await expect(overwrite).resolves.toBeUndefined()
    },
  )

  it('propagates a save failure from the current editor context', async () => {
    vi.mocked(updatePage).mockRejectedValue(new Error('current save failure'))
    useConfigStore.setState({ enableLinkRefactor: false })
    editPage(fakePage, { content: 'private content' })

    await expect(usePageEditorStore.getState().savePage()).rejects.toThrow(
      'current save failure',
    )
  })

  it('propagates a force-overwrite failure from the current editor context', async () => {
    vi.mocked(getPageByPath).mockRejectedValue(
      new Error('current force-overwrite failure'),
    )
    editPage(fakePage, { content: 'private content' })

    await expect(
      usePageEditorStore.getState().forceOverwrite(),
    ).rejects.toThrow('current force-overwrite failure')
  })

  it('propagates the current save failure started by force-overwrite', async () => {
    vi.mocked(getPageByPath).mockResolvedValue({ ...fakePage, version: 'v2' })
    vi.mocked(updatePage).mockRejectedValue(
      new Error('current overwrite-save failure'),
    )
    useConfigStore.setState({ enableLinkRefactor: false })
    editPage(fakePage, { content: 'private content' })

    await expect(
      usePageEditorStore.getState().forceOverwrite(),
    ).rejects.toThrow('current overwrite-save failure')
  })
})
