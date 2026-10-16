export type ProbeKind = 'ping' | 'tcping'
export interface QualityObserver { id: string; name: string }
export interface QualityTarget { id: string; name: string; kind: ProbeKind }
export interface QualityCounts { planned: number | null; pending: number | null; success: number | null; failure: number | null; missing: number | null; not_executed: number | null; unobserved_tasks: number | null }
export interface QualityBounds { lower_ms: number; upper_ms: number }
export interface QualityFamily { status: 'unknown' | 'partial' | 'mixed' | 'ipv4' | 'ipv6'; reason?: string; ipv4_tasks: number | null; ipv6_tasks: number | null; unknown_tasks: number | null }
export interface QualityPacketLoss { value_pct: number | null; status: 'available' | 'partial' | 'unavailable' | 'not_applicable'; reason?: string; measured_tasks: number | null; unknown_tasks: number | null; sent: number | null; received: number | null }
export interface QualityPoint {
  time: string; end: string; effective_start: string | null; effective_end: string | null; availability: string; counts: QualityCounts
  coverage_pct: number | null; execution_failure_pct: number | null
  rtt_mean_ms: number | null; rtt_min_ms: number | null; rtt_max_ms: number | null; rtt_p50_ms: number | null; rtt_p95_ms: number | null
  rtt_p50_bounds: QualityBounds | null; rtt_p95_bounds: QualityBounds | null; quantile_method: string
  address_family: QualityFamily; packet_loss: QualityPacketLoss
}
export interface QualityNode extends Omit<QualityPoint, 'time' | 'end'> {
  node_id: string; name: string; available_from: string | null; granularity_seconds: number; jitter_ms: number | null; jitter_pairs: number | null; points: QualityPoint[]
}
export interface QualityResponse {
  version: number; aggregation: 'time_series'; direction: 'observer_to_target'; target: QualityTarget
  requested_start: string; requested_end: string; as_of: string; timezone: string; bucket_seconds: number; point_limit: number; nodes: QualityNode[]
  address_family: { status: 'unknown' | 'per_node'; reason: string }; packet_loss: { value_pct: null; status: 'unavailable' | 'per_node' | 'not_applicable'; reason: string }
}
export interface QualityTargetsPage { targets: QualityTarget[]; next_cursor: string; page_size: number; observed_at: string }

export class QualityRequestError extends Error {
  constructor(public status: number) { super(status === 401 ? '会话已失效，请重新登录后打开对比。' : status === 404 ? '所选节点或目标已不可见，请重新选择。' : status === 503 ? '当前范围暂不可用，请缩小范围后重试。' : status === 400 ? '时间或节点选择无效，请检查后重试。' : '查询失败，请稍后重试。') }
}
export async function qualityRequest<T>(base: string, suffix: string, parameters: URLSearchParams, signal: AbortSignal): Promise<T> {
  const response = await fetch(`${base}/network-quality/${suffix}?${parameters}`, { credentials: 'same-origin', cache: 'no-store', headers: { Accept: 'application/json' }, signal })
  if (!response.ok) throw new QualityRequestError(response.status)
  return response.json() as Promise<T>
}
export const targetKey = (t: QualityTarget) => `${t.kind}:${t.id}`
