import { create } from 'zustand'
import type { PollStatus } from '@/lib/poller'

interface UiState {
  eventStreamOpen: boolean
  sseStatus: PollStatus
  apiReachable: boolean
  toggleEventStream: () => void
  setSseStatus: (s: PollStatus) => void
  setApiReachable: (b: boolean) => void
}

export const useUiStore = create<UiState>((set) => ({
  eventStreamOpen: true,
  sseStatus: 'stopped',
  apiReachable: true,
  toggleEventStream: () =>
    set((s) => ({ eventStreamOpen: !s.eventStreamOpen })),
  setSseStatus: (s) => set({ sseStatus: s }),
  setApiReachable: (b) => set({ apiReachable: b }),
}))
