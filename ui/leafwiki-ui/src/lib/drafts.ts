export function canManageDrafts(
  authDisabled: boolean,
  role: string | undefined,
) {
  return !authDisabled && (role === 'admin' || role === 'editor')
}
