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
