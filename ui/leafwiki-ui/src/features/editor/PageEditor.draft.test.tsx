import { useConfigStore } from '@/stores/config'
import { TooltipProvider } from '@/components/ui/tooltip'
import { usePageEditorStore } from './pageEditorStore'
import { useSessionStore } from '@/stores/session'
import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import PageEditor from './PageEditor'
import { EditorTitleBar } from './EditorTitleBar'

vi.mock('./MarkdownEditor', () => ({ default: () => null }))
vi.mock('./useAutoSave', () => ({ useAutoSave: () => undefined }))
vi.mock('./useNavigationGuard', () => ({ default: () => undefined }))
vi.mock('./useToolbarActions', () => ({ useToolbarActions: () => undefined }))
vi.mock('@/lib/useIsMobile', () => ({ useIsMobile: () => false }))

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
  ancestorDraft: true,
}

function renderEditor() {
  return render(
    <TooltipProvider>
      <MemoryRouter initialEntries={['/e/parent/child']}>
        <Routes>
          <Route
            path="/e/*"
            element={
              <>
                <EditorTitleBar />
                <PageEditor />
              </>
            }
          />
        </Routes>
      </MemoryRouter>
    </TooltipProvider>,
  )
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
      page: initialPage,
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
    renderEditor()

    fireEvent.click(screen.getByRole('button', { name: /Metadata/ }))

    expect(screen.getAllByText(/Inherited draft/)).toHaveLength(2)
    expect(
      screen.getByRole('checkbox', {
        name: 'Keep draft when parent is published',
      }),
    ).not.toBeChecked()
  })

  it('shows a pending own-draft removal as published under a public parent', () => {
    usePageEditorStore.setState({
      page: { ...initialPage, ancestorDraft: false },
      draft: false,
    })

    const { container } = renderEditor()

    fireEvent.click(screen.getByRole('button', { name: /Metadata/ }))

    expect(screen.getByText(/Published/)).toBeInTheDocument()
    expect(screen.queryByText(/Inherited draft/)).toBeNull()
    expect(container.querySelector('.editor-title-bar .draft-badge')).toBeNull()
  })
})
