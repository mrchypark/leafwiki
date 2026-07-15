import { usePageEditorStore } from '@/features/editor/pageEditorStore'
import { useLinkStatusStore } from '@/features/links/linkstatus_store'
import { useViewerStore } from '@/features/viewer/viewer'
import { useDialogsStore } from '@/stores/dialogs'
import { useFavoritesStore } from '@/stores/favorites'
import { useTreeStore } from '@/stores/tree'

export function getVisibilityScope(
  authDisabled: boolean,
  userId: string | null,
  userRole: string | null,
) {
  if (authDisabled) return 'public-editor'
  return userId ? `${userId}:${userRole ?? 'unknown'}` : 'guest'
}

export function clearPrivilegedVisibilityState() {
  useFavoritesStore.getState().clearFavorites()
  usePageEditorStore.getState().resetEditorState()
  useViewerStore.getState().clear()
  useTreeStore.getState().clearVisibilityData()
  useLinkStatusStore.getState().clear()
  useDialogsStore.getState().closeDialog()
}
