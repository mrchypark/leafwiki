import { fetchWithAuth } from '@/lib/api/auth'
import { DIALOG_ADD_PAGE, DIALOG_CREATE_PAGE_BY_PATH } from '@/lib/registries'
import { useConfigStore } from '@/stores/config'
import { useDialogsStore } from '@/stores/dialogs'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AddPageDialog } from './AddPageDialog'
import { CreatePageByPathDialog } from './CreatePageByPathDialog'

vi.mock('@/lib/api/auth', () => ({ fetchWithAuth: vi.fn() }))

function renderDialog(dialog: React.ReactNode) {
  return render(<MemoryRouter>{dialog}</MemoryRouter>)
}

function pendingResponse() {
  return new Promise<never>(() => {})
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => {
    resolve = done
  })
  return { promise, resolve }
}

function requestBody(path: string) {
  const request = vi
    .mocked(fetchWithAuth)
    .mock.calls.find(
      ([requestPath, options]) =>
        requestPath === path && options?.method === 'POST',
    )

  if (!request) return undefined
  return JSON.parse(String(request[1]?.body)) as Record<string, unknown>
}

describe('draft creation dialogs', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useConfigStore.setState({ authDisabled: false })
    vi.mocked(fetchWithAuth).mockImplementation((path, options) => {
      if (path.startsWith('/api/pages/slug-suggestion')) {
        return Promise.resolve({ slug: 'private-draft' })
      }
      if (path.startsWith('/api/pages/lookup')) {
        return Promise.resolve({
          path: 'notes/private',
          exists: false,
          canCreate: true,
          segments: [],
        })
      }
      if (options?.method === 'POST') return pendingResponse()
      return Promise.resolve(null)
    })
  })

  it('creates a checked add-page draft', async () => {
    const user = userEvent.setup()
    useDialogsStore.setState({ dialogType: DIALOG_ADD_PAGE, dialogProps: null })
    renderDialog(<AddPageDialog parentId="root" />)

    await user.type(screen.getByTestId('add-page-title-input'), 'Private Draft')
    await waitFor(() =>
      expect(screen.getByTestId('add-page-slug-input')).toHaveValue(
        'private-draft',
      ),
    )
    await user.click(screen.getByTestId('add-page-draft-checkbox'))
    await user.click(screen.getByTestId('add-page-dialog-button-no-redirect'))

    await waitFor(() =>
      expect(requestBody('/api/pages')).toEqual({
        title: 'Private Draft',
        slug: 'private-draft',
        parentId: 'root',
        kind: 'page',
        draft: true,
      }),
    )
  })

  it('hides the add-page draft control when authentication is disabled', () => {
    useConfigStore.setState({ authDisabled: true })
    useDialogsStore.setState({ dialogType: DIALOG_ADD_PAGE, dialogProps: null })

    renderDialog(<AddPageDialog parentId="root" />)

    expect(
      screen.queryByTestId('add-page-draft-checkbox'),
    ).not.toBeInTheDocument()
  })

  it('creates a checked draft by path', async () => {
    const user = userEvent.setup()
    useDialogsStore.setState({
      dialogType: DIALOG_CREATE_PAGE_BY_PATH,
      dialogProps: null,
    })
    renderDialog(
      <CreatePageByPathDialog
        initialPath="notes/private"
        initialTitle="Private Draft"
      />,
    )

    await waitFor(() =>
      expect(
        screen.getByTestId('create-page-by-path-draft-checkbox'),
      ).toBeInTheDocument(),
    )
    await user.click(screen.getByTestId('create-page-by-path-draft-checkbox'))
    await user.click(
      screen.getByTestId('create-page-by-path-dialog-button-confirm'),
    )

    await waitFor(() =>
      expect(requestBody('/api/pages/ensure')).toEqual({
        path: 'notes/private',
        title: 'Private Draft',
        draft: true,
      }),
    )
  })

  it('hides the create-by-path draft control when authentication is disabled', () => {
    useConfigStore.setState({ authDisabled: true })
    useDialogsStore.setState({
      dialogType: DIALOG_CREATE_PAGE_BY_PATH,
      dialogProps: null,
    })

    renderDialog(
      <CreatePageByPathDialog
        initialPath="notes/private"
        initialTitle="Private Draft"
      />,
    )

    expect(
      screen.queryByTestId('create-page-by-path-draft-checkbox'),
    ).not.toBeInTheDocument()
  })

  it('does not offer draft mode while the current path lookup is pending', async () => {
    const lookup = deferred<unknown>()
    vi.mocked(fetchWithAuth).mockImplementation((path) => {
      if (path.startsWith('/api/pages/lookup')) return lookup.promise
      return Promise.resolve(null)
    })
    useDialogsStore.setState({
      dialogType: DIALOG_CREATE_PAGE_BY_PATH,
      dialogProps: null,
    })

    renderDialog(
      <CreatePageByPathDialog
        initialPath="notes/private"
        initialTitle="Private Draft"
        readOnlyPath
      />,
    )

    await waitFor(() =>
      expect(fetchWithAuth).toHaveBeenCalledWith(
        '/api/pages/lookup?path=notes%2Fprivate',
      ),
    )
    expect(
      screen.queryByTestId('create-page-by-path-draft-checkbox'),
    ).not.toBeInTheDocument()
  })

  it('does not offer or submit draft mode for an existing path', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchWithAuth).mockImplementation((path, options) => {
      if (path.startsWith('/api/pages/lookup')) {
        return Promise.resolve({
          path: 'notes/private',
          exists: true,
          canCreate: false,
          segments: [],
        })
      }
      if (options?.method === 'POST') return pendingResponse()
      return Promise.resolve(null)
    })
    useDialogsStore.setState({
      dialogType: DIALOG_CREATE_PAGE_BY_PATH,
      dialogProps: null,
    })
    renderDialog(
      <CreatePageByPathDialog
        initialPath="notes/private"
        initialTitle="Private Draft"
        readOnlyPath
      />,
    )

    await waitFor(() =>
      expect(
        screen.queryByTestId('create-page-by-path-draft-checkbox'),
      ).not.toBeInTheDocument(),
    )
    await user.click(
      screen.getByTestId('create-page-by-path-dialog-button-confirm'),
    )

    await waitFor(() =>
      expect(requestBody('/api/pages/ensure')).toEqual({
        path: 'notes/private',
        title: 'Private Draft',
        draft: false,
      }),
    )
  })

  it('ignores an older path lookup that resolves after the current path', async () => {
    const user = userEvent.setup()
    const firstLookup = deferred<unknown>()
    const secondLookup = deferred<unknown>()
    vi.mocked(fetchWithAuth).mockImplementation((requestPath, options) => {
      if (requestPath.startsWith('/api/pages/lookup')) {
        const requestedPath = new URL(
          requestPath,
          'http://leafwiki.test',
        ).searchParams.get('path')
        return requestedPath === 'a'
          ? firstLookup.promise
          : secondLookup.promise
      }
      if (options?.method === 'POST') return pendingResponse()
      return Promise.resolve(null)
    })
    useDialogsStore.setState({
      dialogType: DIALOG_CREATE_PAGE_BY_PATH,
      dialogProps: null,
    })
    renderDialog(
      <CreatePageByPathDialog initialPath="a" initialTitle="Private Draft" />,
    )

    await waitFor(() =>
      expect(fetchWithAuth).toHaveBeenCalledWith('/api/pages/lookup?path=a'),
    )
    const pathInput = screen.getByTestId('create-page-by-path-path-input')
    await user.clear(pathInput)
    await user.type(pathInput, 'b')
    expect(
      screen.queryByTestId('create-page-by-path-draft-checkbox'),
    ).not.toBeInTheDocument()
    await waitFor(() =>
      expect(fetchWithAuth).toHaveBeenCalledWith('/api/pages/lookup?path=b'),
    )

    await act(async () => {
      secondLookup.resolve({
        path: 'b',
        exists: false,
        canCreate: true,
        segments: [],
      })
      await secondLookup.promise
    })
    await user.click(screen.getByTestId('create-page-by-path-draft-checkbox'))

    await act(async () => {
      firstLookup.resolve({
        path: 'a',
        exists: true,
        canCreate: false,
        segments: [],
      })
      await firstLookup.promise
    })
    expect(
      screen.getByTestId('create-page-by-path-draft-checkbox'),
    ).toBeChecked()

    await user.click(
      screen.getByTestId('create-page-by-path-dialog-button-confirm'),
    )
    await waitFor(() =>
      expect(requestBody('/api/pages/ensure')).toEqual({
        path: 'b',
        title: 'Private Draft',
        draft: true,
      }),
    )
  })
})
