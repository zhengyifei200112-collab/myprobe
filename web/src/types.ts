export interface ApiResponse {
  nodes: PublicNode[]
  server_time: string
}

export interface PublicNode {
  node: NodeMetadata
  online: boolean
  stale: boolean
  report?: Report
  latency?: LatestLatency[]
  traffic: { period_start: string; period_end: string; rx_bytes: number; tx_bytes: number }
  commercial?: { expired: boolean; days: number }
}

export interface LatestLatency {
  target_id: string
  name: string
  kind: 'ping' | 'tcping'
  success?: boolean
  latency_ms?: number
  error_class?: string
  updated_at?: string
}

export type HistoryRange = '1h' | '12h' | '1d' | '3d' | '7d' | '30d'

export interface HistoryResponse {
  range: HistoryRange
  bucket_seconds: number
  metrics: Array<{
    time: string
    cpu_percent: number
    memory_percent: number
    disk_percent: number
    rx_bytes_per_second: number
    tx_bytes_per_second: number
  }>
  latency: Array<{
    time: string
    target_id: string
    name: string
    kind: 'ping' | 'tcping'
    latency_ms?: number
    success_rate: number
  }>
  traffic: Array<{ time: string; rx_bytes: number; tx_bytes: number; total_bytes: number }>
}

export interface NodeMetadata {
  id: string
  name: string
  sort_order: number
  hidden: boolean
  tags: string[]
  country_code: string
  currency: string
  price_minor?: number
  billing_cycle: string
  expires_at?: string
  traffic_reset_day?: number
  use_since_boot: boolean
  latency_mode: 'ping' | 'tcping'
  custom_html?: string
  custom_badges?: Array<{ label: string; color: 'gray' | 'blue' | 'green' | 'orange' | 'red' }>
  custom_links?: Array<{ label: string; url: string }>
  collection_seconds: number
  report_seconds: number
  last_seen_at?: string
  agent?: { hostname?: string; operating_system?: string; platform?: string; platform_version?: string; kernel_version?: string; architecture?: string; agent_version?: string; capabilities?: string[]; updated_at: string }
}

export interface Report {
  captured_at: string
  cpu: {
    model: string
    logical_cores: number
    architecture: string
    usage_percent: number
  }
  memory: MemoryMetric
  swap: MemoryMetric
  disks: DiskMetric[]
  networks: NetworkMetric[]
  load: { one: number; five: number; fifteen: number }
  uptime_seconds: number
  processes: number
  public_ip?: string
}

export interface MemoryMetric {
  total_bytes: number
  used_bytes: number
  usage_percent: number
}

export interface DiskMetric {
  mount: string
  filesystem?: string
  total_bytes: number
  used_bytes: number
  usage_percent: number
}

export interface NetworkMetric {
  interface: string
  rx_total_bytes: number
  tx_total_bytes: number
  rx_bytes_per_second: number
  tx_bytes_per_second: number
}

export type RealtimeEvent =
  | { type: 'snapshot'; nodes: PublicNode[] }
  | { type: 'node_metrics'; node: PublicNode }
