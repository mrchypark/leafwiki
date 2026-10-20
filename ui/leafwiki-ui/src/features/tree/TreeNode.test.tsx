import {
  DIALOG_ADD_PAGE,
  DIALOG_DELETE_PAGE_CONFIRMATION,
} from '@/lib/registries'
import type { PageNode } from '@/lib/api/pages'
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

vi.mock('@/lib/useIsMobile', () => ({ useIsMobile: () => false }))
vi.mock('@/lib/useIsReadOnly', () => ({ useIsReadOnly: () => false }))
vi.mock('@/components/TooltipWrapper', () => ({
  TooltipWrapper: ({ children }: { children: React.ReactNode }) => children,
}))

vi.mock('./TreeNodeActionsMenu', () => ({
  default: () => <div />,
}))

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

const otherNode: PageNode = {
  ...node,
  id: 'page-2',
  title: 'Other Page',
  path: 'docs/other-page',
}

function renderNode(target: PageNode = node) {
  return render(
    <MemoryRouter>
      <TreeNode node={target} />
    </MemoryRouter>,
  )
}

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

describe('TreeNode delete-target highlight', () => {
  it('has no delete-target styling when no dialog is open', () => {
    renderNode()

    const row = screen.getByTestId(`tree-node-${node.id}`)
    expect(row).not.toHaveAttribute('data-delete-target')
    expect(row.className).not.toContain('tree-node--delete-target')
  })

  it('stays highlighted while the delete confirmation dialog targets this node', () => {
    useDialogsStore.setState({
      dialogType: DIALOG_DELETE_PAGE_CONFIRMATION,
      dialogProps: { pageId: node.id, redirectTo: '/' },
    })

    renderNode()

    const row = screen.getByTestId(`tree-node-${node.id}`)
    expect(row).toHaveAttribute('data-delete-target', 'true')
    expect(row.className).toContain('tree-node--delete-target')
  })

  it('does not highlight a row when the dialog targets a different node', () => {
    useDialogsStore.setState({
      dialogType: DIALOG_DELETE_PAGE_CONFIRMATION,
      dialogProps: { pageId: otherNode.id, redirectTo: '/' },
    })

    renderNode(node)

    const row = screen.getByTestId(`tree-node-${node.id}`)
    expect(row).not.toHaveAttribute('data-delete-target')
    expect(row.className).not.toContain('tree-node--delete-target')
  })

  it('does not highlight the row once the dialog is closed again', () => {
    useDialogsStore.setState({
      dialogType: DIALOG_DELETE_PAGE_CONFIRMATION,
      dialogProps: { pageId: node.id, redirectTo: '/' },
    })
    const { rerender } = renderNode()
    expect(screen.getByTestId(`tree-node-${node.id}`)).toHaveAttribute(
      'data-delete-target',
      'true',
    )

    useDialogsStore.setState({ dialogType: null, dialogProps: null })
    rerender(
      <MemoryRouter>
        <TreeNode node={node} />
      </MemoryRouter>,
    )

    expect(screen.getByTestId(`tree-node-${node.id}`)).not.toHaveAttribute(
      'data-delete-target',
    )
  })
})

describe('TreeNode draft status', () => {
  it('labels a draft inherited from an ancestor', () => {
    renderNode({ ...node, draft: false, effectiveDraft: true })

    expect(screen.getByText('draft.inherited')).toBeInTheDocument()
  })

  it('labels an own draft as Draft', () => {
    renderNode({ ...node, draft: true, effectiveDraft: true })

    expect(screen.getByText('draft.label')).toBeInTheDocument()
  })

  it('allows adding a child to a draft page', () => {
    renderNode({ ...node, draft: true, effectiveDraft: true })

    fireEvent.mouseEnter(screen.getByTestId(`tree-node-${node.id}`))
    fireEvent.click(screen.getByTestId('tree-view-action-button-add'))

    expect(useDialogsStore.getState()).toMatchObject({
      dialogType: DIALOG_ADD_PAGE,
      dialogProps: { parentId: node.id },
    })
  })
})
