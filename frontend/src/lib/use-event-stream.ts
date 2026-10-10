import { useEffect } from 'react'
import { startEventPoller } from './poller'
import { startEventStream } from './sse-client'
import { api } from './api'
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
    const onEvent = (e: Parameters<Parameters<typeof startEventStream>[0]['onEvent']>[0]) =>
      useRunStore.getState().appendEvent(e)

    let stopFallback: (() => void) | null = null
    const stopStream = startEventStream({
      runId: activeRunId,
      getAfterSeq,
      getRunStatus,
      onEvent,
      onStatus: (s) => useUiStore.getState().setSseStatus(s === 'streaming' ? 'polling' : s),
      onFallback: () => {
        stopFallback = startEventPoller({
          getEvents: (runId, afterSeq) => api.getEvents(runId, afterSeq),
          getRunStatus,
          runId: activeRunId,
          getAfterSeq,
          onEvent,
          onStatus: (s) => useUiStore.getState().setSseStatus(s),
        })
      },
    })
    return () => {
      stopStream()
      stopFallback?.()
    }
  }, [activeRunId])
}
