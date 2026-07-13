import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Page } from '@/lib/api/pages'
import { getPageByPath, updatePage, updatePageDraft } from '@/lib/api/pages'
import { useLinkStatusStore } from '../links/linkstatus_store'
import { useTreeStore } from '@/stores/tree'
import { useViewerStore } from '../viewer/viewer'
import { isDirtyState, usePageEditorStore } from './pageEditorStore'

vi.mock('@/lib/api/pages', async () => {
  const actual =
    await vi.importActual<typeof import('@/lib/api/pages')>('@/lib/api/pages')
  return {
    ...actual,
    getPageByPath: vi.fn(),
    updatePage: vi.fn(),
    updatePageDraft: vi.fn(),
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
} as Page

describe('pageEditorStore.resetEditorState', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    usePageEditorStore.setState(usePageEditorStore.getInitialState())
  })

  it('clears page back to null so stale currentEditorPageId reads disappear', () => {
    usePageEditorStore.setState({
      page: fakePage,
      initialPage: fakePage,
      title: fakePage.title,
      slug: fakePage.slug,
      content: fakePage.content,
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

  it('treats a draft status change as an unsaved edit', () => {
    usePageEditorStore.setState({
      page: fakePage,
      title: fakePage.title,
      slug: fakePage.slug,
      content: fakePage.content,
      draft: true,
      tags: fakePage.tags,
      frontmatterFields: [{ key: 'owner', value: 'alice', type: 'text' }],
    })

    expect(isDirtyState(usePageEditorStore.getState())).toBe(true)
  })
})

describe('pageEditorStore draft save ordering', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    usePageEditorStore.setState(usePageEditorStore.getInitialState())
    useTreeStore.setState({
      reloadTree: vi.fn().mockResolvedValue(undefined),
      patchNodeVersion: vi.fn(),
    })
    useViewerStore.setState({ page: null })
    useLinkStatusStore.setState({
      fetchLinkStatusForPage: vi.fn().mockResolvedValue(undefined),
    })
  })

  function edit(page: Page, draft: boolean, content: string) {
    usePageEditorStore.setState({
      page: { ...page },
      initialPage: { ...page },
      title: page.title,
      slug: page.slug,
      content,
      draft,
      tags: page.tags ?? [],
      frontmatterFields: Object.entries(page.properties ?? {}).map(
        ([key, value]) => ({ key, value, type: 'text' as const }),
      ),
    })
  }

  it('hides a public page before saving private content', async () => {
    const calls: string[] = []
    vi.mocked(updatePageDraft).mockImplementation(async () => {
      calls.push('draft')
      return { ...fakePage, draft: true, version: 'v2' }
    })
    vi.mocked(updatePage).mockImplementation(async () => {
      calls.push('content')
      return {
        ...fakePage,
        content: 'private content',
        draft: true,
        version: 'v3',
      }
    })
    edit(fakePage, true, 'private content')

    await usePageEditorStore.getState().savePage()

    expect(calls).toEqual(['draft', 'content'])
    expect(updatePageDraft).toHaveBeenCalledWith(fakePage.id, 'v1', true)
    expect(updatePage).toHaveBeenCalledWith(
      fakePage.id,
      'v2',
      fakePage.title,
      fakePage.slug,
      'private content',
      fakePage.tags,
      fakePage.properties,
    )
  })

  it('saves draft content before publishing it', async () => {
    const calls: string[] = []
    const draftPage = { ...fakePage, draft: true }
    vi.mocked(updatePage).mockImplementation(async () => {
      calls.push('content')
      return { ...draftPage, content: 'ready', version: 'v2' }
    })
    vi.mocked(updatePageDraft).mockImplementation(async () => {
      calls.push('publish')
      return {
        ...draftPage,
        content: 'ready',
        draft: false,
        version: 'v3',
      }
    })
    edit(draftPage, false, 'ready')

    await usePageEditorStore.getState().savePage()

    expect(calls).toEqual(['content', 'publish'])
    expect(updatePageDraft).toHaveBeenCalledWith(fakePage.id, 'v2', false)
  })

  it('keeps private content dirty when hiding succeeds but content save fails', async () => {
    vi.mocked(updatePageDraft).mockResolvedValue({
      ...fakePage,
      draft: true,
      version: 'v2',
    })
    vi.mocked(updatePage).mockRejectedValue(new Error('content failed'))
    edit(fakePage, true, 'private content')

    await expect(usePageEditorStore.getState().savePage()).rejects.toThrow(
      'content failed',
    )

    const state = usePageEditorStore.getState()
    expect(state.page).toMatchObject({ draft: true, version: 'v2' })
    expect(state.content).toBe('private content')
    expect(state.page?.content).toBe(fakePage.content)
    expect(isDirtyState(state)).toBe(true)
  })

  it('keeps publish dirty when content save succeeds but publishing fails', async () => {
    const draftPage = { ...fakePage, draft: true }
    vi.mocked(updatePage).mockResolvedValue({
      ...draftPage,
      content: 'ready',
      version: 'v2',
    })
    vi.mocked(updatePageDraft).mockRejectedValue(new Error('publish failed'))
    edit(draftPage, false, 'ready')

    await expect(usePageEditorStore.getState().savePage()).rejects.toThrow(
      'publish failed',
    )

    const state = usePageEditorStore.getState()
    expect(state.page).toMatchObject({
      content: 'ready',
      draft: true,
      version: 'v2',
    })
    expect(state.draft).toBe(false)
    expect(isDirtyState(state)).toBe(true)
  })

  it('stops an in-flight save when the editor context is reset', async () => {
    let finishDraftUpdate!: (page: Page) => void
    vi.mocked(updatePageDraft).mockReturnValue(
      new Promise((resolve) => {
        finishDraftUpdate = resolve
      }),
    )
    edit(fakePage, true, 'private content')

    const save = usePageEditorStore.getState().savePage()
    usePageEditorStore.getState().resetEditorState()
    finishDraftUpdate({ ...fakePage, draft: true, version: 'v2' })
    await save

    expect(usePageEditorStore.getState().page).toBeNull()
    expect(updatePage).not.toHaveBeenCalled()
    expect(useTreeStore.getState().reloadTree).not.toHaveBeenCalled()
  })

  it('uses fresh draft state when forcing an overwrite', async () => {
    const localDraft = { ...fakePage, draft: true }
    vi.mocked(getPageByPath).mockResolvedValue({
      ...fakePage,
      draft: false,
      version: 'v2',
    })
    vi.mocked(updatePageDraft).mockResolvedValue({
      ...fakePage,
      draft: true,
      version: 'v3',
    })
    vi.mocked(updatePage).mockResolvedValue({
      ...fakePage,
      content: 'private content',
      draft: true,
      version: 'v4',
    })
    edit(localDraft, true, 'private content')

    await usePageEditorStore.getState().forceOverwrite()

    expect(updatePageDraft).toHaveBeenCalledWith(fakePage.id, 'v2', true)
    expect(updatePage).toHaveBeenCalledWith(
      fakePage.id,
      'v3',
      fakePage.title,
      fakePage.slug,
      'private content',
      fakePage.tags,
      fakePage.properties,
    )
  })
})
