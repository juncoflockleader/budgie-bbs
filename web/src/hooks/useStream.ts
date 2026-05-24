/**
 * useStream — WebSocket-first real-time event stream with SSE fallback.
 *
 * Returns a stable `subscribe` function; callers push event handlers via a
 * React ref pattern so they don't need to re-subscribe on every render.
 */
import { useEffect, useRef, useCallback } from 'react'
import type { BudgieEvent } from '../api/types'

type Handler = (evt: BudgieEvent) => void

interface Options {
  token: string | null
  /** Cursor: replay events with seq > after. */
  after?: number
}

export function useStream({ token, after = 0 }: Options, onEvent: Handler) {
  const onEventRef = useRef<Handler>(onEvent)
  onEventRef.current = onEvent

  const dispatch = useCallback((evt: BudgieEvent) => {
    onEventRef.current(evt)
  }, [])

  useEffect(() => {
    if (!token) return

    let cancelled = false
    let ws: WebSocket | null = null
    let es: EventSource | null = null
    let seenSeq = after

    function tryWs() {
      const proto = location.protocol === 'https:' ? 'wss' : 'ws'
      const url = `${proto}://${location.host}/api/v1/ws?token=${encodeURIComponent(token!)}`
      ws = new WebSocket(url)

      ws.onopen = () => {
        // Send resume to replay missed events.
        ws!.send(JSON.stringify({ type: 'resume', after: seenSeq }))
      }

      ws.onmessage = (e: MessageEvent<string>) => {
        const msg = JSON.parse(e.data) as { type?: string; event?: string; seq?: number; eseq?: number; ts?: number; payload?: unknown }
        if (msg.type === 'event' || msg.event) {
          const evt = msg as unknown as BudgieEvent
          if (evt.seq) seenSeq = Math.max(seenSeq, evt.seq)
          dispatch(evt)
        }
      }

      ws.onerror = () => {
        if (!cancelled) {
          ws?.close()
          fallbackSse()
        }
      }

      ws.onclose = () => {
        if (!cancelled) {
          // Attempt reconnect after 3s.
          setTimeout(() => { if (!cancelled) tryWs() }, 3000)
        }
      }
    }

    function fallbackSse() {
      const url = `/api/v1/events/stream?token=${encodeURIComponent(token!)}`
      es = new EventSource(url)

      es.onmessage = (e) => {
        const evt = JSON.parse(e.data) as BudgieEvent
        if (evt.seq) seenSeq = Math.max(seenSeq, evt.seq)
        dispatch(evt)
      }

      es.onerror = () => {
        if (!cancelled) {
          es?.close()
          // Reconnect with updated cursor.
          setTimeout(() => { if (!cancelled) fallbackSse() }, 5000)
        }
      }
    }

    tryWs()

    return () => {
      cancelled = true
      ws?.close()
      es?.close()
    }
  }, [token, after, dispatch])
}
