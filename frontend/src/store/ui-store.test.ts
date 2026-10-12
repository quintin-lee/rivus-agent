import { describe, it, expect, beforeEach } from 'vitest'
import { useUiStore } from '@/store/ui-store'

describe('ui-store', () => {
  beforeEach(() => {
    useUiStore.setState({
      sseStatus: 'stopped',
      apiReachable: true,
    })
  })

  it('setSseStatus updates sseStatus', () => {
    useUiStore.getState().setSseStatus('polling')
    expect(useUiStore.getState().sseStatus).toBe('polling')
  })

  it('setApiReachable updates apiReachable', () => {
    useUiStore.getState().setApiReachable(false)
    expect(useUiStore.getState().apiReachable).toBe(false)
  })
})
