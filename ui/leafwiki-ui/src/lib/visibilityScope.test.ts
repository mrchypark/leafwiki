import { describe, expect, it } from 'vitest'
import { getVisibilityScope } from './visibilityScope'

describe('getVisibilityScope', () => {
  it('changes when the same user loses permissions', () => {
    const adminScope = getVisibilityScope(false, 'user-1', 'admin')
    const editorScope = getVisibilityScope(false, 'user-1', 'editor')

    expect(editorScope).not.toBe(adminScope)
  })
})
