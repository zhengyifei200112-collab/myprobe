import type { ApiResponse, RealtimeEvent } from './types'

export async function fetchNodes(signal?: AbortSignal): Promise<ApiResponse> {
  const response = await fetch('/api/v1/public/nodes', {
    cache: 'no-cache',
    headers: { Accept: 'application/json' },
    signal,
  })
  if (!response.ok) throw new Error(`nodes request failed: ${response.status}`)
  return response.json() as Promise<ApiResponse>
}

export function connectRealtime(
  onEvent: (event: RealtimeEvent) => void,
  onState: (connected: boolean) => void,
): () => void {
  let socket: WebSocket | undefined
  let closed = false
  let retry = 1000
  let timer: number | undefined

  const connect = () => {
    const scheme = location.protocol === 'https:' ? 'wss:' : 'ws:'
    socket = new WebSocket(`${scheme}//${location.host}/api/v1/public/ws`)
    socket.addEventListener('open', () => {
      retry = 1000
      onState(true)
    })
    socket.addEventListener('message', ({ data }) => {
      try {
        onEvent(JSON.parse(String(data)) as RealtimeEvent)
      } catch {
        // Ignore malformed messages and retain the last valid snapshot.
      }
    })
    socket.addEventListener('close', () => {
      onState(false)
      if (!closed) {
        timer = window.setTimeout(connect, retry)
        retry = Math.min(retry * 2, 30_000)
      }
    })
    socket.addEventListener('error', () => socket?.close())
  }

  connect()
  return () => {
    closed = true
    if (timer !== undefined) window.clearTimeout(timer)
    socket?.close()
  }
}
