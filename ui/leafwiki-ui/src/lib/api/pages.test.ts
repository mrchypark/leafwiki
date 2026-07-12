import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fetchWithAuth } from './auth'
import { updatePage } from './pages'

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
