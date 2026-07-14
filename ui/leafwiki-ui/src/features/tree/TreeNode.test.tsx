import { DIALOG_ADD_PAGE } from '@/lib/registries'
import { useDialogsStore } from '@/stores/dialogs'
import { fireEvent, render, screen } from '@testing-library/react'
import type React from 'react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
import { TreeNode } from './TreeNode'

vi.mock('@/lib/useIsMobile', () => ({ useIsMobile: () => false }))
vi.mock('@/lib/useIsReadOnly', () => ({ useIsReadOnly: () => false }))
vi.mock('@/components/TooltipWrapper', () => ({
  TooltipWrapper: ({ children }: { children: React.ReactNode }) => children,
}))
vi.mock('./TreeNodeActionsMenu', () => ({ default: () => null }))

const node = {
  id: 'child',
  title: 'Child',
  slug: 'child',
  path: 'parent/child',
  version: 'v1',
  children: null,
  kind: 'page' as const,
}

describe('TreeNode draft status', () => {
  it('labels a draft inherited from an ancestor', () => {
    render(
      <MemoryRouter>
        <TreeNode node={{ ...node, draft: false, effectiveDraft: true }} />
      </MemoryRouter>,
    )

    expect(screen.getByText('Inherited draft')).toBeInTheDocument()
  })

  it('labels an own draft as Draft', () => {
    render(
      <MemoryRouter>
        <TreeNode node={{ ...node, draft: true, effectiveDraft: true }} />
      </MemoryRouter>,
    )

    expect(screen.getByText('Draft')).toBeInTheDocument()
  })

  it('allows adding a child to a draft page', () => {
    render(
      <MemoryRouter>
        <TreeNode node={{ ...node, draft: true, effectiveDraft: true }} />
      </MemoryRouter>,
    )

    fireEvent.mouseEnter(screen.getByTestId('tree-node-child'))
    fireEvent.click(screen.getByTestId('tree-view-action-button-add'))

    expect(useDialogsStore.getState()).toMatchObject({
      dialogType: DIALOG_ADD_PAGE,
      dialogProps: { parentId: node.id },
    })
  })
})
