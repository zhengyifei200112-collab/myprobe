import type { NodeMetadata } from './types'

export interface AdminTarget {
  id: string
  name: string
  kind: 'ping' | 'tcping'
  host: string
  port?: number
  interval_seconds: number
  timeout_ms: number
  enabled: boolean
  sort_order: number
}

export interface AdminGroup {
  id: string
  name: string
  kind: 'ping' | 'tcping'
}

export interface LatencyConfig {
  targets: AdminTarget[]
  groups: AdminGroup[]
  group_members: Array<{ group_id: string; target_id: string }>
  node_groups: Array<{ node_id: string; group_id: string }>
}

export interface NotificationChannel {
  id: string
  name: string
  kind: 'webhook' | 'telegram'
  enabled: boolean
  created_at: string
  updated_at: string
}

export type AlertKind = 'offline' | 'cpu' | 'bandwidth' | 'cycle_traffic' | 'expiry'

export interface AlertRule {
  id: string
  node_id: string
  channel_id: string
  kind: AlertKind
  config: {
    offline_seconds?: number
    threshold_percent?: number
    threshold_bytes_per_second?: number
    threshold_bytes?: number
    days_before?: number
  }
  enabled: boolean
  cooldown_seconds: number
  created_at: string
  updated_at: string
}

export interface AlertEvent {
  id: string
  rule_id: string
  node_id?: string
  state: 'firing' | 'resolved' | 'failed'
  message: string
  delivery_error?: string
  created_at: string
  delivered_at?: string
}

export interface ChartShare {
  id: string
  name: string
  node_ids: string[]
  enabled: boolean
  created_at: string
  updated_at: string
}

export interface ConfigImportResult {
  nodes_created: number
  nodes_updated: number
  targets_created: number
  targets_updated: number
  groups_created: number
  groups_updated: number
  memberships_created: number
  agent_tokens?: Record<string, string>
  dry_run: boolean
}

let csrfToken = ''

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const method = (options.method || 'GET').toUpperCase()
  const headers = new Headers(options.headers)
  headers.set('Accept', 'application/json')
  if (options.body !== undefined) headers.set('Content-Type', 'application/json')
  if (!['GET', 'HEAD', 'OPTIONS'].includes(method) && csrfToken) headers.set('X-CSRF-Token', csrfToken)
  const response = await fetch(path, { ...options, headers, credentials: 'same-origin', cache: 'no-store' })
  if (!response.ok) {
    const payload = await response.json().catch(() => ({})) as { error?: string }
    throw new Error(payload.error || `请求失败（${response.status}）`)
  }
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

export async function restoreSession(): Promise<boolean> {
  try {
    const result = await request<{ csrf_token: string }>('/api/v1/auth/me')
    csrfToken = result.csrf_token
    return true
  } catch {
    csrfToken = ''
    return false
  }
}

export async function login(username: string, password: string): Promise<void> {
  const result = await request<{ csrf_token: string }>('/api/v1/auth/login', {
    method: 'POST', body: JSON.stringify({ username, password }),
  })
  csrfToken = result.csrf_token
}

export async function logout(): Promise<void> {
  await request<void>('/api/v1/auth/logout', { method: 'POST' })
  csrfToken = ''
}

export const loadNodes = () => request<{ nodes: NodeMetadata[] }>('/api/v1/admin/nodes')
export const loadLatencyConfig = () => request<LatencyConfig>('/api/v1/admin/latency-config')

export const createNode = (payload: unknown) => request<{ node: NodeMetadata; agent_token: string }>('/api/v1/admin/nodes', { method: 'POST', body: JSON.stringify(payload) })
export const updateNode = (id: string, payload: unknown) => request<{ node: NodeMetadata }>(`/api/v1/admin/nodes/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(payload) })
export const deleteNode = (id: string) => request<void>(`/api/v1/admin/nodes/${encodeURIComponent(id)}`, { method: 'DELETE' })
export const rotateNodeToken = (id: string) => request<{ agent_token: string }>(`/api/v1/admin/nodes/${encodeURIComponent(id)}/rotate-token`, { method: 'POST' })

export const createTarget = (payload: unknown) => request<{ target: AdminTarget }>('/api/v1/admin/targets', { method: 'POST', body: JSON.stringify(payload) })
export const updateTarget = (id: string, payload: unknown) => request<{ target: AdminTarget }>(`/api/v1/admin/targets/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(payload) })
export const deleteTarget = (id: string) => request<void>(`/api/v1/admin/targets/${encodeURIComponent(id)}`, { method: 'DELETE' })

export const createGroup = (payload: unknown) => request<{ group: AdminGroup }>('/api/v1/admin/target-groups', { method: 'POST', body: JSON.stringify(payload) })
export const updateGroup = (id: string, payload: unknown) => request<{ group: AdminGroup }>(`/api/v1/admin/target-groups/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(payload) })
export const deleteGroup = (id: string) => request<void>(`/api/v1/admin/target-groups/${encodeURIComponent(id)}`, { method: 'DELETE' })

export const setGroupTarget = (groupID: string, targetID: string, assigned: boolean) => request<void>(`/api/v1/admin/target-groups/${encodeURIComponent(groupID)}/targets/${encodeURIComponent(targetID)}`, { method: assigned ? 'PUT' : 'DELETE' })
export const setNodeGroup = (nodeID: string, groupID: string, assigned: boolean) => request<void>(`/api/v1/admin/nodes/${encodeURIComponent(nodeID)}/target-groups/${encodeURIComponent(groupID)}`, { method: assigned ? 'PUT' : 'DELETE' })

export const loadChannels = () => request<{ channels: NotificationChannel[] }>('/api/v1/admin/notification-channels')
export const createChannel = (payload: unknown) => request<{ channel: NotificationChannel }>('/api/v1/admin/notification-channels', { method: 'POST', body: JSON.stringify(payload) })
export const updateChannel = (id: string, payload: unknown) => request<{ channel: NotificationChannel }>(`/api/v1/admin/notification-channels/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(payload) })
export const deleteChannel = (id: string) => request<void>(`/api/v1/admin/notification-channels/${encodeURIComponent(id)}`, { method: 'DELETE' })
export const testChannel = (id: string) => request<void>(`/api/v1/admin/notification-channels/${encodeURIComponent(id)}/test`, { method: 'POST' })

export const loadAlertRules = () => request<{ rules: AlertRule[] }>('/api/v1/admin/alert-rules')
export const createAlertRule = (payload: unknown) => request<{ rule: AlertRule }>('/api/v1/admin/alert-rules', { method: 'POST', body: JSON.stringify(payload) })
export const updateAlertRule = (id: string, payload: unknown) => request<{ rule: AlertRule }>(`/api/v1/admin/alert-rules/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(payload) })
export const deleteAlertRule = (id: string) => request<void>(`/api/v1/admin/alert-rules/${encodeURIComponent(id)}`, { method: 'DELETE' })
export const loadAlertEvents = () => request<{ events: AlertEvent[] }>('/api/v1/admin/alert-events')

export const loadChartShares = () => request<{ shares: ChartShare[] }>('/api/v1/admin/chart-shares')
export const createChartShare = (payload: unknown) => request<{ share: ChartShare; path: string }>('/api/v1/admin/chart-shares', { method: 'POST', body: JSON.stringify(payload) })
export const updateChartShare = (id: string, payload: unknown) => request<{ share: ChartShare; path: string }>(`/api/v1/admin/chart-shares/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(payload) })
export const deleteChartShare = (id: string) => request<void>(`/api/v1/admin/chart-shares/${encodeURIComponent(id)}`, { method: 'DELETE' })

async function downloadRequest(path: string, options: RequestInit = {}): Promise<{ blob: Blob; filename: string }> {
  const headers = new Headers(options.headers)
  if (csrfToken) headers.set('X-CSRF-Token', csrfToken)
  const response = await fetch(path, { ...options, headers, credentials: 'same-origin', cache: 'no-store' })
  if (!response.ok) {
    const payload = await response.json().catch(() => ({})) as { error?: string }
    throw new Error(payload.error || `请求失败（${response.status}）`)
  }
  const disposition = response.headers.get('Content-Disposition') || ''
  const filename = disposition.match(/filename="([^"]+)"/)?.[1] || 'myprobe-download'
  return { blob: await response.blob(), filename }
}

export const downloadConfiguration = () => downloadRequest('/api/v1/admin/maintenance/config')
export const importConfiguration = (config: unknown, dryRun: boolean) => request<{ result: ConfigImportResult }>('/api/v1/admin/maintenance/config/import', { method: 'POST', body: JSON.stringify({ dry_run: dryRun, config }) })
export const downloadDatabaseBackup = (passphrase: string) => downloadRequest('/api/v1/admin/maintenance/backup', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ passphrase }) })

export async function uploadDatabaseRestore(file: File, passphrase: string): Promise<{ staged: boolean; restart_required: boolean }> {
  const form = new FormData()
  form.set('file', file)
  form.set('passphrase', passphrase)
  const headers = new Headers()
  if (csrfToken) headers.set('X-CSRF-Token', csrfToken)
  const response = await fetch('/api/v1/admin/maintenance/restore', { method: 'POST', body: form, headers, credentials: 'same-origin', cache: 'no-store' })
  if (!response.ok) {
    const payload = await response.json().catch(() => ({})) as { error?: string }
    throw new Error(payload.error || `请求失败（${response.status}）`)
  }
  return response.json()
}
