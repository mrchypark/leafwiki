import { createPage, createPendingDraft } from '@/lib/api/pages'
import { DIALOG_ADD_PAGE } from '@/lib/registries'
import { useDialogsStore } from '@/stores/dialogs'
import { useTreeStore } from '@/stores/tree'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AddPageDialog } from './AddPageDialog'

const navigateMock = vi.fn()
vi.mock('react-router', () => ({
  useNavigate: () => navigateMock,
}))

vi.mock('@/lib/api/pages', async () => {
  const actual =
    await vi.importActual<typeof import('@/lib/api/pages')>('@/lib/api/pages')
  return {
    ...actual,
    createPage: vi.fn(),
    createPendingDraft: vi.fn(),
  }
})

describe('AddPageDialog', () => {
  const callOrder: string[] = []

  beforeEach(() => {
    vi.clearAllMocks()
    callOrder.length = 0
    navigateMock.mockImplementation(() => callOrder.push('navigate'))
    useDialogsStore.setState({
      dialogType: DIALOG_ADD_PAGE,
      dialogProps: null,
      closeDialog: () => {
        callOrder.push('closeDialog')
        useDialogsStore.setState({ dialogType: null, dialogProps: null })
      },
    })
    useTreeStore.setState({
      reloadTree: vi.fn().mockResolvedValue(undefined),
      getPathById: () => '',
    })
    vi.mocked(createPage).mockResolvedValue(
      undefined as unknown as ReturnType<typeof createPage>,
    )
    vi.mocked(createPendingDraft).mockResolvedValue({
      page: { id: 'pending-1' },
    } as Awaited<ReturnType<typeof createPendingDraft>>)
  })

  it('closes the dialog before navigating to a new page', async () => {
    const user = userEvent.setup()
    render(<AddPageDialog parentId="" />)
    await user.type(screen.getByTestId('add-page-title-input'), 'My Page')
    await user.type(screen.getByTestId('add-page-slug-input'), 'my-page')
    await user.click(screen.getByTestId('add-page-dialog-button-confirm'))

    expect(callOrder.indexOf('closeDialog')).toBeLessThan(
      callOrder.indexOf('navigate'),
    )
  })

  it('creates a pending draft and opens its editor', async () => {
    const user = userEvent.setup()
    render(<AddPageDialog parentId="" />)
    await user.click(screen.getByRole('checkbox', { name: 'Create as draft' }))
    await user.type(screen.getByTestId('add-page-title-input'), 'Draft')
    await user.type(screen.getByTestId('add-page-slug-input'), 'draft')
    await user.click(screen.getByTestId('add-page-dialog-button-confirm'))

    expect(createPendingDraft).toHaveBeenCalledWith({
      title: 'Draft',
      slug: 'draft',
      parentId: '',
    })
    expect(createPage).not.toHaveBeenCalled()
    expect(callOrder.indexOf('closeDialog')).toBeLessThan(
      callOrder.indexOf('navigate'),
    )
  })
})
