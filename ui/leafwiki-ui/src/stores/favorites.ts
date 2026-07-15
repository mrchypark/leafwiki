// stores/favorites.ts
// A logged-in user's private set of favorited pages. Unlike Pinned Pages
// (global, part of the tree), this is per-user server truth fetched once via
// GET /api/favorites and never persisted to localStorage — it must be
// re-fetched per session and cleared on logout so a second user on the same
// browser never sees the first user's favorites. App.tsx owns calling
// loadFavorites()/clearFavorites() as the session's logged-in state changes.

import {
  addFavorite as addFavoriteAPI,
  getFavorites,
  removeFavorite as removeFavoriteAPI,
} from '@/lib/api/favorites'
import { create } from 'zustand'

type FavoritesStore = {
  favoritePageIds: Set<string>
  loaded: boolean
  loadFavorites: (userId: string) => Promise<void>
  addFavorite: (pageId: string) => Promise<void>
  removeFavorite: (pageId: string) => Promise<void>
  clearFavorites: () => void
}

let favoritesGeneration = 0
let favoritesUserId: string | null = null

export const useFavoritesStore = create<FavoritesStore>()((set, get) => ({
  favoritePageIds: new Set(),
  loaded: false,
  loadFavorites: async (userId: string) => {
    const generation = ++favoritesGeneration
    favoritesUserId = userId
    try {
      const pages = await getFavorites()
      if (generation !== favoritesGeneration || userId !== favoritesUserId)
        return
      set({ favoritePageIds: new Set(pages.map((p) => p.id)), loaded: true })
    } catch (err) {
      if (generation !== favoritesGeneration || userId !== favoritesUserId)
        return
      console.warn('Failed to load favorites:', err)
    }
  },
  addFavorite: async (pageId: string) => {
    const generation = favoritesGeneration
    const previous = get().favoritePageIds
    const wasFavorited = previous.has(pageId)
    const optimistic = new Set(previous)
    optimistic.add(pageId)
    set({ favoritePageIds: optimistic })
    try {
      await addFavoriteAPI(pageId)
    } catch (err) {
      if (generation === favoritesGeneration) {
        set((state) => {
          const next = new Set(state.favoritePageIds)
          if (wasFavorited) next.add(pageId)
          else next.delete(pageId)
          return { favoritePageIds: next }
        })
      }
      throw err
    }
  },
  removeFavorite: async (pageId: string) => {
    const generation = favoritesGeneration
    const previous = get().favoritePageIds
    const wasFavorited = previous.has(pageId)
    const optimistic = new Set(previous)
    optimistic.delete(pageId)
    set({ favoritePageIds: optimistic })
    try {
      await removeFavoriteAPI(pageId)
    } catch (err) {
      if (generation === favoritesGeneration) {
        set((state) => {
          const next = new Set(state.favoritePageIds)
          if (wasFavorited) next.add(pageId)
          else next.delete(pageId)
          return { favoritePageIds: next }
        })
      }
      throw err
    }
  },
  clearFavorites: () => {
    favoritesGeneration++
    favoritesUserId = null
    set({ favoritePageIds: new Set(), loaded: false })
  },
}))
