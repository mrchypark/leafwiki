import { fireEvent, render, screen } from '@testing-library/react'
import { useConfigStore } from '@/stores/config'
import { useSessionStore } from '@/stores/session'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { PageFrontmatterPanel } from './PageFrontmatterPanel'

function renderPanel(
  onDraftChange = vi.fn(),
  draft = false,
  effectiveDraft = draft,
) {
  render(
    <PageFrontmatterPanel
      draft={draft}
      effectiveDraft={effectiveDraft}
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

  it('presents inherited visibility as an own-draft choice', () => {
    const onDraftChange = renderPanel(vi.fn(), false, true)

    expect(screen.getByText(/Inherited draft/)).toBeInTheDocument()
    const checkbox = screen.getByRole('checkbox', {
      name: 'Keep draft when parent is published',
    })
    expect(checkbox).not.toBeChecked()

    fireEvent.click(checkbox)

    expect(onDraftChange).toHaveBeenCalledWith(true)
  })

  it('keeps a direct draft selected while it is also inherited', () => {
    renderPanel(vi.fn(), true, true)

    expect(screen.getByRole('checkbox', { name: 'Draft' })).toBeChecked()
    expect(screen.queryByText(/Inherited draft/)).toBeNull()
  })

  it.each([
    ['viewer', false],
    ['anonymous', false],
    ['public editor', true],
  ] as const)('hides the control for %s', (_label, authDisabled) => {
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

    renderPanel()

    expect(screen.queryByRole('checkbox', { name: 'Draft' })).toBeNull()
  })
})
