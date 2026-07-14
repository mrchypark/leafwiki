import { beforeEach, describe, expect, it } from 'vitest'
import { useSidebarPanelsStore } from './sidebarPanels'

describe('useSidebarPanelsStore', () => {
  beforeEach(() => {
    useSidebarPanelsStore.setState({ openSections: ['pinned', 'pages'] })
  })

  it('starts with pinned and pages expanded by default', () => {
    expect(useSidebarPanelsStore.getState().openSections).toEqual([
      'pinned',
      'pages',
    ])
  })

  it('setOpenSections replaces the open section ids', () => {
    useSidebarPanelsStore.getState().setOpenSections(['pages'])
    expect(useSidebarPanelsStore.getState().openSections).toEqual(['pages'])
  })

  it('setOpenSections can close all sections', () => {
    useSidebarPanelsStore.getState().setOpenSections([])
    expect(useSidebarPanelsStore.getState().openSections).toEqual([])
  })

  it('setOpenSections can add a new section id (e.g. a future "favorites" section)', () => {
    useSidebarPanelsStore
      .getState()
      .setOpenSections(['pinned', 'pages', 'favorites'])
    expect(useSidebarPanelsStore.getState().openSections).toEqual([
      'pinned',
      'pages',
      'favorites',
    ])
  })

  it('rehydration restores a usable default store when persisted state is missing', async () => {
    localStorage.removeItem('leafwiki-sidebar-panels')

    await useSidebarPanelsStore.persist.rehydrate()

    expect(useSidebarPanelsStore.getState().openSections).toEqual([
      'pinned',
      'pages',
    ])
    expect(useSidebarPanelsStore.persist.hasHydrated()).toBe(true)
    expect(useSidebarPanelsStore.getState().setOpenSections).toBeTypeOf(
      'function',
    )
  })

  it.each([
    { name: 'persisted state is null', state: null },
    { name: 'openSections is null', state: { openSections: null } },
    { name: 'openSections is an object', state: { openSections: {} } },
    {
      name: 'openSections contains a non-string value',
      state: { openSections: ['pages', 1] },
    },
  ])(
    'rehydration restores the default sections when $name',
    async ({ state }) => {
      localStorage.setItem(
        'leafwiki-sidebar-panels',
        JSON.stringify({ state, version: 0 }),
      )

      await useSidebarPanelsStore.persist.rehydrate()

      expect(useSidebarPanelsStore.getState().openSections).toEqual([
        'pinned',
        'pages',
      ])
      expect(useSidebarPanelsStore.persist.hasHydrated()).toBe(true)
      expect(useSidebarPanelsStore.getState().setOpenSections).toBeTypeOf(
        'function',
      )
    },
  )

  it('rehydration preserves an empty persisted section list', async () => {
    localStorage.setItem(
      'leafwiki-sidebar-panels',
      JSON.stringify({ state: { openSections: [] }, version: 0 }),
    )

    await useSidebarPanelsStore.persist.rehydrate()

    expect(useSidebarPanelsStore.getState().openSections).toEqual([])
  })
})
