export function getVisibilityScope(
  authDisabled: boolean,
  userId: string | null,
  userRole: string | null,
) {
  if (authDisabled) return 'public-editor'
  return userId ? `${userId}:${userRole ?? 'unknown'}` : 'guest'
}

export function hasVisibilityScopeChanged(
  previous: string | undefined,
  current: string,
) {
  return previous !== undefined && previous !== current
}
