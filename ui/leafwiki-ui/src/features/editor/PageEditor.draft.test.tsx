import { useConfigStore } from '@/stores/config'
import { usePageEditorStore } from './pageEditorStore'
import { useSessionStore } from '@/stores/session'
import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import PageEditor from './PageEditor'

vi.mock('./MarkdownEditor', () => ({ default: () => null }))
vi.mock('./useAutoSave', () => ({ useAutoSave: () => undefined }))
vi.mock('./useNavigationGuard', () => ({ default: () => undefined }))
vi.mock('./useToolbarActions', () => ({ useToolbarActions: () => undefined }))

const initialPage = {
  id: 'child',
  title: 'Child',
  slug: 'child',
  path: 'parent/child',
  content: '',
  version: 'v1',
  kind: 'page' as const,
  draft: true,
  effectiveDraft: true,
}

describe('PageEditor inherited draft state', () => {
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
    usePageEditorStore.setState({
      initialPage,
      page: { ...initialPage, draft: false },
      title: initialPage.title,
      slug: initialPage.slug,
      content: '',
      draft: false,
      tags: [],
      frontmatterFields: [],
      frontmatterUnsupported: '',
      frontmatterErrors: {},
      error: null,
      notFound: false,
      loadPageData: vi.fn(),
    })
  })

  it('remains inherited after its own draft is removed', () => {
    render(
      <MemoryRouter initialEntries={['/e/parent/child']}>
        <Routes>
          <Route path="/e/*" element={<PageEditor />} />
        </Routes>
      </MemoryRouter>,
    )

    fireEvent.click(screen.getByRole('button', { name: /Metadata/ }))

    expect(screen.getByText(/Inherited draft/)).toBeInTheDocument()
    expect(
      screen.getByRole('checkbox', {
        name: 'Keep draft when parent is published',
      }),
    ).not.toBeChecked()
  })
})
