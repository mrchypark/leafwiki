import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fetchWithAuth } from './auth'
import { applyPageRefactor, createPage, ensurePage, updatePage } from './pages'

vi.mock('./auth', () => ({ fetchWithAuth: vi.fn() }))

describe('updatePage', () => {
  beforeEach(() => vi.clearAllMocks())

  it('sends the draft status with the page update', async () => {
    vi.mocked(fetchWithAuth).mockResolvedValue(null)

    await updatePage('page-1', 'v1', 'Title', 'title', 'Content', [], {}, true)

    expect(fetchWithAuth).toHaveBeenCalledWith('/api/pages/page-1', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        version: 'v1',
        title: 'Title',
        slug: 'title',
        content: 'Content',
        tags: [],
        properties: {},
        draft: true,
      }),
    })
  })
})

describe('applyPageRefactor', () => {
  beforeEach(() => vi.clearAllMocks())

  it('sends final visibility and metadata with a rename', async () => {
    vi.mocked(fetchWithAuth).mockResolvedValue(null)

    await applyPageRefactor('page-1', {
      kind: 'rename',
      version: 'v1',
      title: 'Published',
      slug: 'published',
      content: 'Ready',
      tags: ['ready'],
      properties: { owner: 'alice' },
      draft: false,
      rewriteLinks: true,
    })

    expect(fetchWithAuth).toHaveBeenCalledWith(
      '/api/pages/page-1/refactor/apply',
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          kind: 'rename',
          version: 'v1',
          title: 'Published',
          slug: 'published',
          content: 'Ready',
          tags: ['ready'],
          properties: { owner: 'alice' },
          draft: false,
          rewriteLinks: true,
        }),
      },
    )
  })
})

describe('draft creation', () => {
  beforeEach(() => vi.clearAllMocks())

  it('sends the draft status when creating a page', async () => {
    vi.mocked(fetchWithAuth).mockResolvedValue(null)

    await createPage({
      title: 'Private Draft',
      slug: 'private-draft',
      parentId: null,
      kind: 'page',
      draft: true,
    })

    expect(fetchWithAuth).toHaveBeenCalledWith('/api/pages', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        title: 'Private Draft',
        slug: 'private-draft',
        parentId: null,
        kind: 'page',
        draft: true,
      }),
    })
  })

  it('sends the draft status when creating a page by path', async () => {
    vi.mocked(fetchWithAuth).mockResolvedValue(null)

    await ensurePage('notes/private', 'Private Draft', true)

    expect(fetchWithAuth).toHaveBeenCalledWith('/api/pages/ensure', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        path: 'notes/private',
        title: 'Private Draft',
        draft: true,
      }),
    })
  })
})
