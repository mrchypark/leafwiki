import { fireEvent, render, screen } from '@testing-library/react'
import { useConfigStore } from '@/stores/config'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { PageFrontmatterPanel } from './PageFrontmatterPanel'

describe('PageFrontmatterPanel', () => {
  beforeEach(() => {
    useConfigStore.setState({ authDisabled: false })
  })

  it('lets an editor mark the page as a draft', () => {
    const onDraftChange = vi.fn()

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

    fireEvent.click(screen.getByRole('button', { name: /Metadata/ }))
    fireEvent.click(screen.getByRole('checkbox', { name: 'Draft' }))

    expect(onDraftChange).toHaveBeenCalledWith(true)
  })

  it('hides the draft control when authentication is disabled', () => {
    useConfigStore.setState({ authDisabled: true })

    render(
      <PageFrontmatterPanel
        draft={false}
        tags={[]}
        fields={[]}
        errors={{}}
        hasUnsupportedFields={false}
        onDraftChange={vi.fn()}
        onTagsChange={vi.fn()}
        onFieldsChange={vi.fn()}
      />,
    )

    fireEvent.click(screen.getByRole('button', { name: /Metadata/ }))

    expect(
      screen.queryByRole('checkbox', { name: 'Draft' }),
    ).not.toBeInTheDocument()
  })
})
