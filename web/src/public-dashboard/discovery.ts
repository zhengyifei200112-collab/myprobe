import type { PublicNode } from '../types'

export type NodeState = 'waiting' | 'interrupted' | 'stale' | 'online'
export interface DiscoveryQuery { q: string; tag: string; status: string; sort: string; target: string; view: string }
export const defaults: DiscoveryQuery = { q: '', tag: '__all__', status: 'all', sort: 'default', target: '', view: 'cards' }
export const sortOptions = [
  { value: 'default', label: '后台顺序' }, { value: 'name', label: '名称' },
  { value: 'cpu', label: 'CPU 从高到低' }, { value: 'memory', label: '内存从高到低' },
  { value: 'latency', label: '目标延迟从高到低' }, { value: 'expiry', label: '最早到期' },
]
export function readQuery(search: string, saved: Partial<DiscoveryQuery> = {}): DiscoveryQuery {
  const params = new URLSearchParams(search)
  const result = { ...defaults }
  // Only presentation preferences persist across unrelated visits.
  for (const key of ['sort', 'target', 'view'] as const) if (typeof saved[key] === 'string') result[key] = saved[key]!
  for (const key of Object.keys(defaults) as Array<keyof DiscoveryQuery>) if (params.has(key)) result[key] = params.get(key)!
  if (!['all', 'attention', 'interrupted', 'expiring', 'waiting'].includes(result.status)) result.status = 'all'
  if (!sortOptions.some(option => option.value === result.sort)) result.sort = 'default'
  if (!['cards', 'table'].includes(result.view)) result.view = 'cards'
  if (result.sort === 'latency' && !result.target) result.sort = 'default'
  return result
}
export function queryURL(url: URL, query: DiscoveryQuery): string {
  // Include defaults so a shared link overrides the recipient's local preferences.
  for (const key of Object.keys(defaults) as Array<keyof DiscoveryQuery>) url.searchParams.set(key, query[key])
  return url.pathname + url.search + url.hash
}
export function nodeState(item: PublicNode): NodeState {
  if (!item.report && !item.node.last_seen_at) return 'waiting'
  if (!item.online) return 'interrupted'
  if (item.stale || !item.report) return 'stale'
  return 'online'
}
export const stateLabels: Record<NodeState, string> = { waiting: '等待接入', interrupted: '上报中断', stale: '数据陈旧', online: '在线' }
export function expiring(item: PublicNode, now: number): boolean {
  const expiry = Date.parse(item.node.expires_at || '')
  return Number.isFinite(expiry) && expiry >= now && expiry - now <= 7 * 86400000
}
export function attentionReasons(item: PublicNode, now: number): string[] {
  const reasons: string[] = []
  const state = nodeState(item)
  if (state === 'interrupted' || state === 'stale') reasons.push(stateLabels[state])
  if (state === 'online') {
    if ((item.report?.cpu.usage_percent ?? 0) >= 90) reasons.push(`CPU ${item.report!.cpu.usage_percent.toFixed(1)}%`)
    if ((item.report?.memory.usage_percent ?? 0) >= 90) reasons.push(`内存 ${item.report!.memory.usage_percent.toFixed(1)}%`)
    if (item.report?.disks?.some(disk => disk.usage_percent >= 90)) reasons.push('磁盘使用率 ≥ 90%')
  }
  const expiry = Date.parse(item.node.expires_at || '')
  if (expiry < now) reasons.push('已到期')
  else if (expiring(item, now)) reasons.push('7 天内到期')
  return reasons
}
export function matchesQuery(item: PublicNode, query: DiscoveryQuery, now: number): boolean {
  const search = query.q.trim().toLocaleLowerCase()
  if (search && ![item.node.name, ...(item.node.tags || [])].some(value => value.toLocaleLowerCase().includes(search))) return false
  if (query.tag !== '__all__' && !item.node.tags?.includes(query.tag)) return false
  if (query.status === 'attention') return attentionReasons(item, now).length > 0
  if (query.status === 'interrupted' || query.status === 'waiting') return nodeState(item) === query.status
  if (query.status === 'expiring') return expiring(item, now)
  return true
}
export function latencyValue(item: PublicNode, target: string): number | undefined {
  if (nodeState(item) !== 'online') return undefined
  const point = item.latency?.find(point => point.target_id === target)
  return point?.success && Number.isFinite(point.latency_ms) ? point.latency_ms : undefined
}
function numericValue(item: PublicNode, query: DiscoveryQuery): number | undefined {
  let value: number | undefined
  if (query.sort === 'expiry') value = Date.parse(item.node.expires_at || '')
  else if (query.sort === 'default') value = item.node.sort_order
  else if (nodeState(item) === 'online') {
    if (query.sort === 'cpu') value = item.report?.cpu.usage_percent
    if (query.sort === 'memory') value = item.report?.memory.usage_percent
    if (query.sort === 'latency') value = latencyValue(item, query.target)
  }
  return Number.isFinite(value) ? value : undefined
}
export function sortNodes(items: PublicNode[], query: DiscoveryQuery): PublicNode[] {
  return [...items].sort((a, b) => {
    if (query.sort === 'name') return a.node.name.localeCompare(b.node.name, 'zh-CN', { numeric: true }) || a.node.id.localeCompare(b.node.id)
    const av = numericValue(a, query), bv = numericValue(b, query)
    if (av === undefined && bv !== undefined) return 1
    if (bv === undefined && av !== undefined) return -1
    const difference = av === undefined || bv === undefined ? 0 : av - bv
    return (['cpu', 'memory', 'latency'].includes(query.sort) ? -difference : difference) || a.node.id.localeCompare(b.node.id)
  })
}
