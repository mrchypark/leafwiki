import { useDialogsStore } from '@/stores/dialogs'
import { useConfigStore } from '@/stores/config'
import { useSessionStore } from '@/stores/session'
import { createPage, suggestSlug } from '@/lib/api/pages'
import { DIALOG_ADD_PAGE } from '@/lib/registries'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AddPageDialog } from './AddPageDialog'

vi.mock('@/lib/api/pages', async () => {
  const actual =
    await vi.importActual<typeof import('@/lib/api/pages')>('@/lib/api/pages')
  return { ...actual, createPage: vi.fn(), suggestSlug: vi.fn() }
})

describe('AddPageDialog draft creation', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(suggestSlug).mockResolvedValue('private-note')
    vi.mocked(createPage).mockResolvedValue(null)
    useDialogsStore.setState({ dialogType: DIALOG_ADD_PAGE, dialogProps: null })
    useConfigStore.setState({ authDisabled: false })
    useSessionStore.setState({
      user: {
        id: 'editor-1',
        username: 'editor',
        email: 'editor@example.test',
        role: 'editor',
      },
    })
  })

  it('creates a checked page draft', async () => {
    const user = userEvent.setup()
    render(
      <MemoryRouter>
        <AddPageDialog parentId="root" />
      </MemoryRouter>,
    )

    await user.type(screen.getByTestId('add-page-title-input'), 'Private note')
    await waitFor(() =>
      expect(screen.getByTestId('add-page-slug-input')).toHaveValue(
        'private-note',
      ),
    )
    await user.click(screen.getByTestId('add-page-draft-checkbox'))
    await user.click(screen.getByTestId('add-page-dialog-button-no-redirect'))

    await waitFor(() =>
      expect(createPage).toHaveBeenCalledWith({
        title: 'Private note',
        slug: 'private-note',
        parentId: 'root',
        kind: 'page',
        draft: true,
      }),
    )
  })

  it('does not offer draft creation for a section', () => {
    render(
      <MemoryRouter>
        <AddPageDialog parentId="root" nodeKind="section" />
      </MemoryRouter>,
    )

    expect(screen.queryByTestId('add-page-draft-checkbox')).toBeNull()
  })
})
