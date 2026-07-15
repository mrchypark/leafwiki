import { usePageEditorStore } from '@/features/editor/pageEditorStore'
import { useLinkStatusStore } from '@/features/links/linkstatus_store'
import { useViewerStore } from '@/features/viewer/viewer'
import type { Page, PageNode } from '@/lib/api/pages'
import { fetchLinkStatus, type LinkStatusResult } from '@/lib/api/links'
import { useDialogsStore } from '@/stores/dialogs'
import { useFavoritesStore } from '@/stores/favorites'
import { useTreeStore } from '@/stores/tree'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  clearPrivilegedVisibilityState,
  getVisibilityScope,
} from './visibilityScope'

vi.mock('@/lib/api/links', () => ({ fetchLinkStatus: vi.fn() }))

const draftNode: Page & PageNode = {
  id: 'draft-1',
  title: 'Draft',
  slug: 'draft',
  path: 'draft',
  version: 'v1',
  kind: 'page',
  draft: true,
  children: null,
  content: 'private',
}

const draftLinks: LinkStatusResult = {
  backlinks: [
    {
      from_page_id: 'secret-source',
      from_path: 'secret-source',
      to_page_id: draftNode.id,
      from_title: 'Secret source',
      broken: false,
    },
  ],
  broken_incoming: [],
  outgoings: [],
  broken_outgoings: [],
  counts: {
    backlinks: 1,
    broken_incoming: 0,
    outgoings: 0,
    broken_outgoings: 0,
  },
}

describe('visibility scope', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useDialogsStore.getState().closeDialog()
    useLinkStatusStore.getState().clear()
  })

  it('distinguishes users and public editor mode', () => {
    expect(getVisibilityScope(false, 'alice', 'editor')).toBe('alice:editor')
    expect(getVisibilityScope(false, 'bob', 'editor')).toBe('bob:editor')
    expect(getVisibilityScope(true, 'alice', 'editor')).toBe('public-editor')
  })

  it('clears page data loaded under the previous scope', () => {
    usePageEditorStore.setState({ page: draftNode })
    useViewerStore.setState({ page: draftNode })
    useTreeStore.setState({
      tree: draftNode,
      byId: { [draftNode.id]: draftNode },
      byPath: { [draftNode.path]: draftNode },
      pinnedPages: [draftNode],
    })
    useDialogsStore.getState().openDialog('page-permalink', {
      page: draftNode,
    })
    useFavoritesStore.setState({
      favoritePageIds: new Set([draftNode.id]),
      loaded: true,
    })
    useLinkStatusStore.setState({ status: draftLinks })

    clearPrivilegedVisibilityState()

    expect(usePageEditorStore.getState().page).toBeNull()
    expect(useViewerStore.getState().page).toBeNull()
    expect(useTreeStore.getState()).toMatchObject({
      tree: null,
      byId: {},
      byPath: {},
      pinnedPages: [],
    })
    expect(useDialogsStore.getState()).toMatchObject({
      dialogType: null,
      dialogProps: null,
    })
    expect(useLinkStatusStore.getState()).toMatchObject({
      status: null,
      loading: false,
      error: null,
    })
    expect(useFavoritesStore.getState()).toMatchObject({
      favoritePageIds: new Set(),
      loaded: false,
    })
  })

  it('does not restore link data when a previous-scope request finishes', async () => {
    let finishRequest!: (status: LinkStatusResult) => void
    vi.mocked(fetchLinkStatus).mockReturnValue(
      new Promise((resolve) => {
        finishRequest = resolve
      }),
    )

    const pending = useLinkStatusStore
      .getState()
      .fetchLinkStatusForPage(draftNode.id)
    clearPrivilegedVisibilityState()

    const signal = vi.mocked(fetchLinkStatus).mock.calls[0][1]
    expect(signal).toBeInstanceOf(AbortSignal)
    expect(signal?.aborted).toBe(true)

    finishRequest(draftLinks)
    await pending

    expect(useLinkStatusStore.getState()).toMatchObject({
      status: null,
      loading: false,
      error: null,
    })
  })

  it('keeps only the latest link request result', async () => {
    let finishFirst!: (status: LinkStatusResult) => void
    let finishSecond!: (status: LinkStatusResult) => void
    const publicLinks = {
      ...draftLinks,
      backlinks: [],
      counts: { ...draftLinks.counts, backlinks: 0 },
    }
    vi.mocked(fetchLinkStatus)
      .mockReturnValueOnce(
        new Promise((resolve) => {
          finishFirst = resolve
        }),
      )
      .mockReturnValueOnce(
        new Promise((resolve) => {
          finishSecond = resolve
        }),
      )

    const first = useLinkStatusStore
      .getState()
      .fetchLinkStatusForPage(draftNode.id)
    const firstSignal = vi.mocked(fetchLinkStatus).mock.calls[0][1]
    const second = useLinkStatusStore
      .getState()
      .fetchLinkStatusForPage('public-page')

    expect(firstSignal?.aborted).toBe(true)
    finishFirst(draftLinks)
    finishSecond(publicLinks)
    await Promise.all([first, second])

    expect(useLinkStatusStore.getState().status).toEqual(publicLinks)
  })
})
