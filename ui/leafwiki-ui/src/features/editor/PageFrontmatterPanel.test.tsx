import { fireEvent, render, screen } from '@testing-library/react'
import { useConfigStore } from '@/stores/config'
import { useSessionStore } from '@/stores/session'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { PageFrontmatterPanel } from './PageFrontmatterPanel'

function renderPanel(
  pageKind: 'page' | 'section' = 'page',
  onDraftChange = vi.fn(),
) {
  render(
    <PageFrontmatterPanel
      pageKind={pageKind}
      draft={false}
      tags={[]}
      fields={[]}
      errors={{}}
      hasUnsupportedFields={false}
      onDraftChange={onDraftChange}
      onTagsChange={vi.fn()}
      onFieldsChange={vi.fn()}
    />,
  )
  fireEvent.click(screen.getByRole('button', { name: /Metadata/ }))
  return onDraftChange
}

describe('PageFrontmatterPanel draft control', () => {
  beforeEach(() => {
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

  it('lets an authenticated editor change page draft status', () => {
    const onDraftChange = renderPanel()

    fireEvent.click(screen.getByRole('checkbox', { name: 'Draft' }))

    expect(onDraftChange).toHaveBeenCalledWith(true)
  })

  it.each([
    ['viewer', false, 'page'],
    ['anonymous', false, 'page'],
    ['public editor', true, 'page'],
    ['section', false, 'section'],
  ] as const)('hides the control for %s', (_label, authDisabled, pageKind) => {
    useConfigStore.setState({ authDisabled })
    if (_label === 'viewer') {
      useSessionStore.setState({
        user: {
          id: 'viewer-1',
          username: 'viewer',
          email: 'viewer@example.test',
          role: 'viewer',
        },
      })
    } else if (_label === 'anonymous') {
      useSessionStore.setState({ user: null })
    }

    renderPanel(pageKind)

    expect(screen.queryByRole('checkbox', { name: 'Draft' })).toBeNull()
  })
})
