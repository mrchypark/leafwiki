import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fetchWithAuth } from './auth'
import { createPage, updatePageDraft } from './pages'

vi.mock('./auth', () => ({ fetchWithAuth: vi.fn() }))

describe('draft page API', () => {
  beforeEach(() => vi.clearAllMocks())

  it('creates a leaf page as a draft', async () => {
    vi.mocked(fetchWithAuth).mockResolvedValue(null)

    await createPage({
      title: 'Private note',
      slug: 'private-note',
      parentId: null,
      kind: 'page',
      draft: true,
    })

    expect(fetchWithAuth).toHaveBeenCalledWith('/api/pages', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        title: 'Private note',
        slug: 'private-note',
        parentId: null,
        kind: 'page',
        draft: true,
      }),
    })
  })

  it('changes draft status through the dedicated endpoint', async () => {
    vi.mocked(fetchWithAuth).mockResolvedValue(null)

    await updatePageDraft('page-1', 'v1', false)

    expect(fetchWithAuth).toHaveBeenCalledWith('/api/pages/page-1/draft', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ version: 'v1', draft: false }),
    })
  })
})
