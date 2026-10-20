import type { PageNode } from '@/lib/api/pages'
import { DIALOG_DELETE_PAGE_CONFIRMATION } from '@/lib/registries'
import { useDialogsStore } from '@/stores/dialogs'
import { useTreeStore } from '@/stores/tree'
import { fireEvent, render, screen } from '@testing-library/react'
import type React from 'react'
import { MemoryRouter } from 'react-router'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { TreeNode } from './TreeNode'

vi.mock('react-i18next', () => ({
  initReactI18next: { type: '3rdParty', init: () => {} },
  useTranslation: () => ({ t: (key: string) => key }),
}))
vi.mock('@/components/TooltipWrapper', () => ({
  TooltipWrapper: ({ children }: { children: React.ReactNode }) => children,
}))
vi.mock('./TreeNodeActionsMenu', () => ({ default: () => <div /> }))

const node: PageNode = {
  id: 'page-1',
  title: 'Getting Started',
  slug: 'getting-started',
  path: 'docs/getting-started',
  version: 'v1',
  parentId: 'docs',
  children: null,
  kind: 'page',
}

function renderNode(target: PageNode = node) {
  return render(
    <MemoryRouter>
      <TreeNode node={target} />
    </MemoryRouter>,
  )
}

describe('TreeNode', () => {
  beforeEach(() => {
    window.matchMedia = vi.fn().mockImplementation(() => ({
      matches: false,
      media: '(max-width: 767px)',
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }))
    useDialogsStore.setState({ dialogType: null, dialogProps: null })
    useTreeStore.setState({ activeNodeId: null, openNodeIdSet: {} })
  })

  it('highlights only the row targeted by a delete dialog', () => {
    useDialogsStore.setState({
      dialogType: DIALOG_DELETE_PAGE_CONFIRMATION,
      dialogProps: { pageId: node.id, redirectTo: '/' },
    })
    renderNode()
    expect(screen.getByTestId(`tree-node-${node.id}`)).toHaveAttribute(
      'data-delete-target',
      'true',
    )
  })

  it('shows pending drafts as private non-draggable children', () => {
    const pending: PageNode = {
      ...node,
      id: 'pending-1',
      title: 'Pending',
      slug: 'pending',
      path: 'published/pending',
      version: 'pending',
      parentId: node.id,
      children: [],
      draft: 'pending',
    }
    const parent: PageNode = {
      ...node,
      id: 'parent-1',
      kind: 'page',
      children: [pending],
    }
    renderNode(parent)

    fireEvent.click(screen.getByTestId('tree-node-toggle-icon-parent-1'))
    expect(screen.getByTestId('tree-node-link-pending-1')).toHaveAttribute(
      'href',
      '/pending-drafts/pending-1/edit',
    )
    expect(
      screen.getByLabelText('treeActions.pendingDraftMarker'),
    ).toBeInTheDocument()
  })
})
