import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fetchWithAuth } from './auth'
import {
  createPage,
  isEffectivelyDraft,
  isInheritedDraft,
  updatePageDraft,
} from './pages'

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

describe('draft status compatibility', () => {
  it('uses the legacy draft field when effectiveDraft is absent', () => {
    expect(isEffectivelyDraft({ draft: true })).toBe(true)
    expect(isInheritedDraft({ draft: true })).toBe(false)
  })

  it('distinguishes inherited-only drafts', () => {
    const page = { draft: false, effectiveDraft: true }

    expect(isEffectivelyDraft(page)).toBe(true)
    expect(isInheritedDraft(page)).toBe(true)
  })
})
