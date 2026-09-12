import type { PublicNode } from '../types'

export function aggregateNode(item: PublicNode) {
  const disks = item.report?.disks ?? []
  const networks = item.report?.networks ?? []
  const diskTotal = disks.reduce((sum, disk) => sum + disk.total_bytes, 0)
  const diskUsed = disks.reduce((sum, disk) => sum + disk.used_bytes, 0)

  return {
    diskTotal,
    diskPercent: diskTotal ? diskUsed / diskTotal * 100 : 0,
    rxRate: networks.reduce((sum, network) => sum + network.rx_bytes_per_second, 0),
    txRate: networks.reduce((sum, network) => sum + network.tx_bytes_per_second, 0),
    rxTotal: networks.reduce((sum, network) => sum + network.rx_total_bytes, 0),
    txTotal: networks.reduce((sum, network) => sum + network.tx_total_bytes, 0),
  }
}

export function formatBytes(value: number, suffix = '') {
  if (!Number.isFinite(value) || value <= 0) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1)
  const scaled = value / 1024 ** index
  return `${scaled >= 100 ? scaled.toFixed(0) : scaled >= 10 ? scaled.toFixed(1) : scaled.toFixed(2)} ${units[index]}${suffix}`
}

export function formatUptime(seconds = 0) {
  if (!seconds) return '—'
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor(seconds % 86400 / 3600)
  const minutes = Math.floor(seconds % 3600 / 60)
  return days ? `${days} 天 ${hours} 小时` : hours ? `${hours} 小时 ${minutes} 分钟` : `${minutes} 分钟`
}

export function maskedIP(value?: string) {
  if (!value) return '—'
  const parts = value.split('.')
  return parts.length === 4 ? `${parts[0]}.${parts[1]}.**` : value.replace(/:[^:]+$/, ':****')
}

export function percent(value = 0) {
  return `${Math.max(0, Math.min(100, value)).toFixed(value >= 10 ? 0 : 1)}%`
}

export function resourceTone(value = 0): 'accent' | 'warning' | 'danger' {
  return value >= 90 ? 'danger' : value >= 75 ? 'warning' : 'accent'
}

export function price(item: PublicNode) {
  if (item.node.price_minor == null || !item.node.currency) return '未设置价格'
  return new Intl.NumberFormat('zh-CN', {
    style: 'currency',
    currency: item.node.currency,
    currencyDisplay: 'narrowSymbol',
    minimumFractionDigits: 0,
    maximumFractionDigits: 2,
    useGrouping: false,
  }).format(item.node.price_minor / 100)
}

export function expiry(item: PublicNode) {
  if (!item.node.expires_at) return '无到期时间'
  if (!item.commercial) return '到期状态未知'
  return item.commercial.expired ? `已过期 ${item.commercial.days} 天` : `剩余 ${item.commercial.days} 天`
}

export function expiryDate(item: PublicNode) {
  if (!item.node.expires_at) return ''
  const parsed = new Date(item.node.expires_at)
  if (Number.isNaN(parsed.getTime())) return ''
  return `${parsed.getFullYear()}/${String(parsed.getMonth() + 1).padStart(2, '0')}/${String(parsed.getDate()).padStart(2, '0')} 到期`
}

export function osName(item: PublicNode) {
  const value = item.node.agent?.platform || item.node.agent?.operating_system || ''
  if (!value) return '等待 Agent 上报'
  return value.charAt(0).toUpperCase() + value.slice(1)
}

export function latencyText(success?: boolean, latency?: number, errorClass?: string) {
  if (success === undefined) return '等待首次探测'
  if (!success) return errorClass ? `失败 · ${errorClass}` : '探测失败'
  if (latency === undefined) return '已连通'
  return `${latency < 10 ? latency.toFixed(2) : latency.toFixed(1)} ms`
}

export function lastReport(item: PublicNode) {
  if (!item.report?.captured_at) return '暂无上报数据'
  const captured = new Date(item.report.captured_at)
  if (Number.isNaN(captured.getTime())) return '暂无上报数据'
  return captured.toLocaleString('zh-CN', { hour12: false })
}

export function offlineDuration(item: PublicNode, now: Date) {
  if (!item.report?.captured_at) return '持续时间未知'
  const captured = new Date(item.report.captured_at)
  if (Number.isNaN(captured.getTime())) return '持续时间未知'
  const seconds = Math.max(0, Math.floor((now.getTime() - captured.getTime()) / 1000))
  return seconds < 60 ? '不足 1 分钟' : formatUptime(seconds)
}
