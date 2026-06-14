/**
 * useStream — WebSocket-first real-time event stream with SSE fallback.
 *
 * Returns a stable `subscribe` function; callers push event handlers via a
 * React ref pattern so they don't need to re-subscribe on every render.
 */
import { useEffect, useRef, useCallback } from 'react'
import type { BudgieEvent } from '../api/types'
import type { EventCursor } from '../api/types'

type Handler = (evt: BudgieEvent) => void

interface Options {
  token: string | null
  /** Scalar fallback cursor for servers that do not understand partition cursors. */
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
    let seenCursor: EventCursor | null = null
    const seenPartitions = new Map<string, { kind: string; key: string; offset: number }>()

    function rememberPartition(kind?: string, key?: string, offset?: number) {
      if (!kind || !key || !offset) return
      const id = `${kind}\u0000${key}`
      const current = seenPartitions.get(id)
      if (!current || offset > current.offset) {
        seenPartitions.set(id, { kind, key, offset })
      }
    }

    function updateSeenCursor() {
      const partitions = Array.from(seenPartitions.values()).sort((a, b) => {
        if (a.kind === b.kind) return a.key.localeCompare(b.key)
        return a.kind.localeCompare(b.kind)
      })
      seenCursor = {
        ...(seenSeq > 0 ? { seq: seenSeq } : {}),
        ...(partitions.length > 0 ? { partitions } : {}),
      }
    }

    function rememberCursor(evt: BudgieEvent) {
      if (evt.seq) seenSeq = Math.max(seenSeq, evt.seq)
      if (evt.cursor) {
        if (evt.cursor.seq) seenSeq = Math.max(seenSeq, evt.cursor.seq)
        evt.cursor.partitions?.forEach((part) => rememberPartition(part.kind, part.key, part.offset))
      }
      rememberPartition(evt.partitionKind, evt.partitionKey, evt.partitionOffset)
      updateSeenCursor()
    }

    function partitionResumeCursor(): EventCursor | undefined {
      if (!seenCursor?.partitions?.length) return undefined
      return { partitions: seenCursor.partitions }
    }

    function resumePayload() {
      return { after: seenSeq, cursor: partitionResumeCursor(), subscriptions: [] }
    }

    function tryWs() {
      const proto = location.protocol === 'https:' ? 'wss' : 'ws'
      const url = `${proto}://${location.host}/api/v1/ws?token=${encodeURIComponent(token!)}`
      ws = new WebSocket(url)

      ws.onopen = () => {
        // Server sends welcome first; full-duplex so we can send resume immediately.
        ws!.send(JSON.stringify({
          kind: 'control',
          control: 'resume',
          payload: resumePayload(),
        }))
      }

      ws.onmessage = (e: MessageEvent<string>) => {
        const msg = JSON.parse(e.data) as { kind?: string; event?: string; seq?: number; eseq?: number; ts?: number; payload?: unknown }
        if (msg.kind === 'event' && msg.event) {
          const evt = msg as unknown as BudgieEvent
          rememberCursor(evt)
          dispatch(evt)
        }
        // Ignore control messages (welcome, ping, etc.)
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
      const params = new URLSearchParams({ token: token!, after: String(seenSeq) })
      const cursor = partitionResumeCursor()
      if (cursor) params.set('cursor', JSON.stringify(cursor))
      const url = `/api/v1/events/stream?${params.toString()}`
      es = new EventSource(url)

      es.onmessage = (e) => {
        const evt = JSON.parse(e.data) as BudgieEvent
        rememberCursor(evt)
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
