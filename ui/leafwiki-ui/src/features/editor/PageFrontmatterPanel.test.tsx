import { fireEvent, render, screen } from '@testing-library/react'
import { useConfigStore } from '@/stores/config'
import { useSessionStore } from '@/stores/session'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { PageFrontmatterPanel } from './PageFrontmatterPanel'

vi.mock('@/stores/session', async () => {
  const { create } = await import('zustand')
  return { useSessionStore: create(() => ({ user: null })) }
})

function renderPanel(
  creatorId = 'owner-id',
  onDraftChange = vi.fn(),
) {
  render(
    <PageFrontmatterPanel
      creatorId={creatorId}
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
  return onDraftChange
}

describe('PageFrontmatterPanel', () => {
  beforeEach(() => {
    useConfigStore.setState({ authDisabled: false })
    useSessionStore.setState({
      user: {
        id: 'owner-id',
        username: 'owner',
        email: 'owner@example.com',
        role: 'editor',
      },
    })
  })

  it('lets an editor mark the page as a draft', () => {
    const onDraftChange = renderPanel()

    fireEvent.click(screen.getByRole('button', { name: /Metadata/ }))
    fireEvent.click(screen.getByRole('checkbox', { name: 'Draft' }))

    expect(onDraftChange).toHaveBeenCalledWith(true)
  })

  it('lets an admin change another users draft status', () => {
    useSessionStore.setState({
      user: {
        id: 'admin-id',
        username: 'admin',
        email: 'admin@example.com',
        role: 'admin',
      },
    })

    renderPanel()

    fireEvent.click(screen.getByRole('button', { name: /Metadata/ }))

    expect(screen.getByRole('checkbox', { name: 'Draft' })).toBeInTheDocument()
  })

  it('hides the draft control from an editor who does not own the page', () => {
    renderPanel('another-user-id')

    fireEvent.click(screen.getByRole('button', { name: /Metadata/ }))

    expect(
      screen.queryByRole('checkbox', { name: 'Draft' }),
    ).not.toBeInTheDocument()
  })

  it('hides the draft control when authentication is disabled', () => {
    useConfigStore.setState({ authDisabled: true })

    renderPanel()

    fireEvent.click(screen.getByRole('button', { name: /Metadata/ }))

    expect(
      screen.queryByRole('checkbox', { name: 'Draft' }),
    ).not.toBeInTheDocument()
  })
})
