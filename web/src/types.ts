export interface ApiResponse {
  nodes: PublicNode[]
  settings: SiteSettings
  server_time: string
}

export interface SiteSettings {
  agent_url: string
  site_name: string
  site_summary: string
  browser_title: string
  site_title: string
  site_description: string
  dashboard_title: string
  dashboard_description: string
  logo_url: string
  favicon_url: string
  footer_text: string
  copyright_text: string
  github_url: string
  blog_url: string
  contact: string
  custom_links: SiteLink[]
  theme_mode: ThemeMode
  accent_color: AccentColor
  public_background: BackgroundSettings
  admin_background: BackgroundSettings
  login_background: BackgroundSettings
  header_html: string
  footer_html: string
}

export type ThemeMode = 'light' | 'dark' | 'system'
export type AccentColor = 'blue' | 'purple' | 'green' | 'orange' | 'pink'
export interface SiteLink { label: string; url: string }
export interface BackgroundSettings {
  url: string
  fit: 'cover' | 'contain' | 'original'
  position: 'center' | 'top' | 'bottom'
  blur: number
  opacity: number
  overlay: number
}

const defaultBackground = (): BackgroundSettings => ({ url: '', fit: 'cover', position: 'center', blur: 0, opacity: 1, overlay: 0.12 })

export function defaultSiteSettings(): SiteSettings {
  return {
    agent_url: '', site_name: 'MyProbe', site_summary: '', browser_title: 'MyProbe · 服务器探针',
    site_title: '服务器运行概览', site_description: '节点状态、资源占用、实时速率与网络延迟集中展示。',
    dashboard_title: '服务器运行概览', dashboard_description: '节点状态、资源占用、实时速率与网络延迟集中展示。',
    logo_url: '', favicon_url: '', footer_text: '', copyright_text: '', github_url: '', blog_url: '', contact: '', custom_links: [],
    theme_mode: 'system', accent_color: 'blue', public_background: defaultBackground(), admin_background: defaultBackground(), login_background: defaultBackground(),
    header_html: '', footer_html: '',
  }
}

export function normalizeSiteSettings(value?: Partial<SiteSettings>): SiteSettings {
  const defaults = defaultSiteSettings()
  return {
    ...defaults,
    ...value,
    custom_links: value?.custom_links || [],
    public_background: { ...defaults.public_background, ...value?.public_background },
    admin_background: { ...defaults.admin_background, ...value?.admin_background },
    login_background: { ...defaults.login_background, ...value?.login_background },
  }
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

export type HistoryRange = '1h' | '12h' | '1d' | '3d' | '7d' | '30d' | '1y'

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
  | { type: 'snapshot'; nodes: PublicNode[]; settings?: SiteSettings }
  | { type: 'node_metrics'; node: PublicNode }
