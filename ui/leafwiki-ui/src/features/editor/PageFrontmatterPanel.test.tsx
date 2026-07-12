import { fireEvent, render, screen } from '@testing-library/react'
import { useConfigStore } from '@/stores/config'
import { useSessionStore } from '@/stores/session'
import { afterAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { PageFrontmatterPanel } from './PageFrontmatterPanel'

vi.hoisted(() => {
  const values = new Map<string, string>()
  vi.stubGlobal('localStorage', {
    get length() {
      return values.size
    },
    clear: () => values.clear(),
    getItem: (key: string) => values.get(key) ?? null,
    key: (index: number) => [...values.keys()][index] ?? null,
    removeItem: (key: string) => values.delete(key),
    setItem: (key: string, value: string) => values.set(key, value),
  } satisfies Storage)
})

afterAll(() => vi.unstubAllGlobals())

function renderPanel(onDraftChange = vi.fn()) {
  render(
    <PageFrontmatterPanel
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

  it('shows the draft control to an admin', () => {
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

  it('hides the draft control from a viewer', () => {
    useSessionStore.setState({
      user: {
        id: 'viewer-id',
        username: 'viewer',
        email: 'viewer@example.com',
        role: 'viewer',
      },
    })

    renderPanel()

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
