import { create } from 'zustand'
import type { PollStatus } from '@/lib/poller'

interface UiState {
  sseStatus: PollStatus
  apiReachable: boolean
  setSseStatus: (s: PollStatus) => void
  setApiReachable: (b: boolean) => void
}

export const useUiStore = create<UiState>((set) => ({
  sseStatus: 'stopped',
  apiReachable: true,
  setSseStatus: (s) => set({ sseStatus: s }),
  setApiReachable: (b) => set({ apiReachable: b }),
}))
