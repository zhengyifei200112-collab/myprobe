import type { ApiResponse, HistorySelection, HistoryResponse, RealtimeEvent, SiteSettings } from './types'

export class SessionExpiredError extends Error {
  constructor() { super('管理会话已过期，请重新登录。') }
}

export async function fetchSiteSettings(signal?: AbortSignal): Promise<SiteSettings> {
  const response = await fetch('/api/v1/public/settings', { cache: 'no-cache', headers: { Accept: 'application/json' }, signal })
  if (!response.ok) throw new Error(`settings request failed: ${response.status}`)
  return ((await response.json()) as { settings: SiteSettings }).settings
}

export async function fetchNodes(signal?: AbortSignal): Promise<ApiResponse> {
  const response = await fetch('/api/v1/public/nodes', {
    cache: 'no-cache',
    headers: { Accept: 'application/json' },
    signal,
  })
  if (!response.ok) throw new Error(`nodes request failed: ${response.status}`)
  return response.json() as Promise<ApiResponse>
}

export async function fetchHistory(nodeID: string, selection: HistorySelection, signal?: AbortSignal, admin = false): Promise<HistoryResponse> {
  const query = new URLSearchParams(typeof selection === 'string' ? { range: selection } : selection)
  const response = await fetch(`/api/v1/${admin ? 'admin' : 'public'}/nodes/${encodeURIComponent(nodeID)}/history?${query}`, {
    cache: 'no-cache',
    headers: { Accept: 'application/json' },
    signal,
  })
  if (admin && response.status === 401) throw new SessionExpiredError()
  if (!response.ok) throw new Error(response.status === 400 ? '时间范围无效：请检查起止时间，结束时间不能晚于服务器当前时间。' : response.status === 404 ? '节点不存在或已隐藏。' : '暂时无法读取历史数据，请重试。')
  return response.json() as Promise<HistoryResponse>
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
