import { useEffect } from 'react'
import { startEventPoller } from './poller'
import { api } from './api'
import { POLL_INTERVAL_MS } from '@/App.config'
import type { RunStatus } from './types'
import { useRunStore } from '@/store/run-store'
import { useUiStore } from '@/store/ui-store'

export function useEventStream() {
  const activeRunId = useRunStore((s) => s.activeRunId)

  useEffect(() => {
    if (!activeRunId) return

    const getAfterSeq = () => {
      const events = useRunStore.getState().events
      return events.length ? events[events.length - 1].seq : 0
    }

    const getRunStatus = async (runId: string): Promise<RunStatus> => {
      const { run } = await api.getRun(runId)
      return run.status
    }

    const stop = startEventPoller({
      getEvents: (runId, afterSeq) => api.getEvents(runId, afterSeq),
      getRunStatus,
      runId: activeRunId,
      intervalMs: POLL_INTERVAL_MS,
      getAfterSeq,
      onEvent: (e) => useRunStore.getState().appendEvent(e),
      onStatus: (s) => useUiStore.getState().setSseStatus(s),
    })

    return stop
  }, [activeRunId])
}
