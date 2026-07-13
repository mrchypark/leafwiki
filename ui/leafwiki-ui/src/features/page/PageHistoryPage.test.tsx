import { act, render, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useSessionStore } from '@/stores/session'
import { useViewerStore } from '../viewer/viewer'
import PageHistoryPage from './PageHistoryPage'

type SessionUser = {
  id: string
  username: string
  email: string
  role: 'admin' | 'editor' | 'viewer'
}

vi.mock('@/stores/session', async () => {
  const { create } = await import('zustand')
  return {
    useSessionStore: create<{
      user: SessionUser | null
      setUser: (user: SessionUser | null) => void
    }>((set) => ({
      user: null,
      setUser: (user) => set({ user }),
    })),
  }
})

vi.mock('@/features/history/pageHistory', () => ({
  usePageHistory: vi.fn(),
}))

vi.mock('@/features/history/PageHistoryContent', () => ({
  PageHistoryContent: () => null,
}))

function setUser(id: string, role: SessionUser['role']) {
  useSessionStore.getState().setUser({
    id,
    username: id,
    email: `${id}@example.com`,
    role,
  })
}

describe('PageHistoryPage', () => {
  const loadPageData = vi.fn()

  beforeEach(() => {
    loadPageData.mockReset()
    useViewerStore.setState({
      page: null,
      error: null,
      notFound: false,
      isLoading: false,
      loadPageData,
    })
    setUser('user-1', 'editor')
  })

  it('reloads page visibility when the authenticated user or role changes', async () => {
    render(
      <MemoryRouter initialEntries={['/history/private-draft']}>
        <PageHistoryPage />
      </MemoryRouter>,
    )

    await waitFor(() => expect(loadPageData).toHaveBeenCalledTimes(1))

    act(() => {
      setUser('user-2', 'editor')
    })
    await waitFor(() => expect(loadPageData).toHaveBeenCalledTimes(2))

    act(() => {
      setUser('user-2', 'admin')
    })
    await waitFor(() => expect(loadPageData).toHaveBeenCalledTimes(3))
  })
})
