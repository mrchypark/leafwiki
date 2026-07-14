import { isValidElement } from 'react'
import { Navigate } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import ExternalRedirect from '../auth/ExternalRedirect'
import { LoginForm } from './lazy-routes'
import { createLeafWikiRouter } from './router'

function loginRouteElement(
  authDisabled: boolean,
  loginUrl: string,
  basename?: string,
) {
  const router = createLeafWikiRouter(
    false,
    authDisabled,
    false,
    '',
    loginUrl,
    basename,
  )
  const loginRoute = router.routes.find((route) => route.path === '/login')
  const element = loginRoute?.element
  if (!isValidElement(element)) {
    throw new Error('expected /login route to render an element')
  }
  return element
}

describe('createLeafWikiRouter /login route', () => {
  it('navigates home when auth is disabled, even if loginUrl is configured', () => {
    expect(loginRouteElement(true, 'https://idp.example.com/login').type).toBe(
      Navigate,
    )
  })

  it('redirects externally when loginUrl is configured and auth is enabled', () => {
    const element = loginRouteElement(false, 'https://idp.example.com/login')

    expect(element.type).toBe(ExternalRedirect)
    expect(element.props).toMatchObject({ returnTo: '/' })
  })

  it('redirects externally to the app root when a base path is configured', () => {
    const element = loginRouteElement(
      false,
      'https://idp.example.com/login',
      '/wiki',
    )

    expect(element.type).toBe(ExternalRedirect)
    expect(element.props).toMatchObject({ returnTo: '/wiki/' })
  })

  it('renders the local login form otherwise', () => {
    expect(loginRouteElement(false, '').type).toBe(LoginForm)
  })
})
