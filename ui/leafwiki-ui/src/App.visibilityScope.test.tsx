import { act, render, screen, waitFor } from '@testing-library/react'
import { useConfigStore } from '@/stores/config'
import { useSessionStore } from '@/stores/session'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import App from './App'
import { useBrandingStore } from './stores/branding'

const searchHarness = vi.hoisted(() => ({ request: vi.fn() }))

vi.mock('@/features/router/router', () => ({
  createLeafWikiRouter: vi.fn(() => ({})),
}))

vi.mock('@/lib/bootstrapAuth', () => ({ useBootstrapAuth: vi.fn() }))
vi.mock('@/lib/api/favorites', () => ({
  getFavorites: vi.fn().mockResolvedValue([]),
  addFavorite: vi.fn(),
  removeFavorite: vi.fn(),
}))
vi.mock('./useApplyDesignMode', () => ({ default: vi.fn() }))

vi.mock('react-router-dom', async () => {
  const actual =
    await vi.importActual<typeof import('react-router-dom')>('react-router-dom')
  const React = await import('react')

  return {
    ...actual,
    RouterProvider: () => {
      const [result, setResult] = React.useState('')

      React.useEffect(() => {
        searchHarness.request().then(setResult)
      }, [])

      return React.createElement('div', null, result)
    },
  }
})

describe('App visibility scope', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useConfigStore.setState({
      hasLoaded: true,
      authDisabled: false,
      publicAccess: true,
      loadConfig: vi.fn().mockResolvedValue(undefined),
    })
    useBrandingStore.setState({
      loadBranding: vi.fn().mockResolvedValue(undefined),
    })
    useSessionStore.setState({
      isRefreshing: false,
      user: {
        id: 'editor-1',
        username: 'editor',
        email: 'editor@example.test',
        role: 'editor',
      },
    })
  })

  it('discards a pending search result when the visibility scope changes', async () => {
    let finishEditorSearch!: (result: string) => void
    let finishViewerSearch!: (result: string) => void
    searchHarness.request
      .mockReturnValueOnce(
        new Promise((resolve) => {
          finishEditorSearch = resolve
        }),
      )
      .mockReturnValueOnce(
        new Promise((resolve) => {
          finishViewerSearch = resolve
        }),
      )

    render(<App />)
    expect(searchHarness.request).toHaveBeenCalledTimes(1)

    act(() => {
      useSessionStore.setState({
        user: {
          id: 'viewer-1',
          username: 'viewer',
          email: 'viewer@example.test',
          role: 'viewer',
        },
      })
    })

    await waitFor(() => expect(searchHarness.request).toHaveBeenCalledTimes(2))

    await act(async () => {
      finishEditorSearch('Secret draft result')
      await Promise.resolve()
    })
    expect(screen.queryByText('Secret draft result')).toBeNull()

    await act(async () => {
      finishViewerSearch('Public result')
    })
    expect(await screen.findByText('Public result')).toBeInTheDocument()
  })
})
