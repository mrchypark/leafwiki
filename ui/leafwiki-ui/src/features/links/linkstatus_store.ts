import { fetchLinkStatus, type LinkStatusResult } from '@/lib/api/links'
import { create } from 'zustand'

type LinkStatusStore = {
  status: LinkStatusResult | null
  loading: boolean
  error: string | null
  fetchLinkStatusForPage: (pageId: string) => Promise<void>
  clear: () => void
}

let loadController: AbortController | null = null

export const useLinkStatusStore = create<LinkStatusStore>((set) => ({
  status: null,
  loading: false,
  error: null,

  clear: () => {
    loadController?.abort()
    loadController = null
    set({ status: null, loading: false, error: null })
  },

  fetchLinkStatusForPage: async (pageId: string) => {
    loadController?.abort()
    loadController = null
    if (!pageId) {
      set({ status: null, loading: false, error: 'Page ID is required' })
      return
    }
    loadController = new AbortController()
    const { signal } = loadController
    set({ status: null, loading: true, error: null })
    try {
      const data = await fetchLinkStatus(pageId, signal)
      if (signal.aborted) return
      set({ status: data, loading: false })
    } catch (err: unknown) {
      if (signal.aborted) return
      const msg =
        err instanceof Error ? err.message : 'Failed to fetch link status'
      set({ error: msg, loading: false })
    }
  },
}))
