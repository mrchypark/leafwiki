import { afterEach, describe, expect, it } from 'vitest'
import { redirectToExternal } from './redirectToExternal'

describe('redirectToExternal', () => {
  const originalLocation = window.location

  afterEach(() => {
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: originalLocation,
    })
  })

  it('preserves the configured query and hash when adding the return URL', () => {
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: { ...originalLocation, href: '' },
    })

    redirectToExternal(
      'https://idp.example.com/login?tenant=a#section',
      '/protected?tab=x#anchor',
    )

    const redirected = new URL(window.location.href)
    expect(redirected.searchParams.get('tenant')).toBe('a')
    expect(redirected.searchParams.get('redirect_uri')).toBe(
      `${originalLocation.origin}/protected?tab=x#anchor`,
    )
    expect(redirected.hash).toBe('#section')
  })
})
