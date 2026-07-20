export interface ApiResponse {
  nodes: PublicNode[]
  server_time: string
}

export interface PublicNode {
  node: NodeMetadata
  online: boolean
  stale: boolean
  report?: Report
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
  collection_seconds: number
  report_seconds: number
  last_seen_at?: string
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
