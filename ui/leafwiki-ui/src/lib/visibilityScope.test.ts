import { describe, expect, it } from 'vitest'
import { useTreeStore } from '@/stores/tree'
import {
  getVisibilityScope,
  hasVisibilityScopeChanged,
} from './visibilityScope'

describe('getVisibilityScope', () => {
  it('changes when the same user loses permissions', () => {
    const adminScope = getVisibilityScope(false, 'user-1', 'admin')
    const editorScope = getVisibilityScope(false, 'user-1', 'editor')

    expect(editorScope).not.toBe(adminScope)
  })

  it('preserves hydrated tree expansion state on the first scope observation', () => {
    useTreeStore.setState({
      openNodeIds: ['persisted-section'],
      openNodeIdSet: { 'persisted-section': true },
    })

    if (hasVisibilityScopeChanged(undefined, 'user-1:editor')) {
      useTreeStore.getState().clearVisibilityData()
    }

    expect(useTreeStore.getState().openNodeIds).toEqual(['persisted-section'])
  })

  it('clears privileged tree data after a real scope change', () => {
    useTreeStore.setState({
      tree: {
        id: 'draft-1',
        title: 'Draft',
        slug: 'draft',
        path: 'draft',
        version: 'v1',
        kind: 'page',
        children: [],
        draft: true,
      },
      openNodeIds: ['draft-1'],
      openNodeIdSet: { 'draft-1': true },
    })

    if (hasVisibilityScopeChanged('user-1:editor', 'guest')) {
      useTreeStore.getState().clearVisibilityData()
    }

    expect(useTreeStore.getState()).toMatchObject({
      tree: null,
      openNodeIds: [],
      openNodeIdSet: {},
    })
  })
})
