import { beforeEach, describe, expect, it } from 'vitest'
import { useProgressbarStore } from '../progressbar/progressbarStore'
import { useViewerStore } from './viewer'

describe('viewer visibility reset', () => {
  beforeEach(() => {
    useViewerStore.setState(useViewerStore.getInitialState())
    useProgressbarStore.setState({ loading: false })
  })

  it('clears progress when resetting an active viewer load', () => {
    useViewerStore.setState({ isLoading: true })
    useProgressbarStore.setState({ loading: true })

    useViewerStore.getState().clear()

    expect(useViewerStore.getState().isLoading).toBe(false)
    expect(useProgressbarStore.getState().loading).toBe(false)
  })

  it('preserves progress owned elsewhere when the viewer is idle', () => {
    useViewerStore.setState({ isLoading: false })
    useProgressbarStore.setState({ loading: true })

    useViewerStore.getState().clear()

    expect(useProgressbarStore.getState().loading).toBe(true)
  })
})
