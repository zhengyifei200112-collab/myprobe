import type { NodeEvent } from '../types.ts'

const labels: Record<string, string> = {
  report_observed: '留存范围内观测到上报',
  report_gap: '上报间隔异常',
  report_resumed: '再次观测到上报',
  reboot_hint: '检测到重启迹象',
  incident_observed: '首次观测到异常',
  incident_fired: '达到告警门槛',
  incident_recovered: '告警已恢复',
  incident_closed: '告警已结束',
  maintenance_started: '维护开始',
  maintenance_ended: '维护结束',
  maintenance_canceled: '维护已取消',
  configuration_saved: '节点配置已保存',
  node_order_saved: '节点排序已保存',
}
export function eventLabel(event: NodeEvent): string { return labels[event.kind] || '节点事件' }

// The viewport is clipped to the selected history window. ECharts' time axis
// works in milliseconds; exact source nanoseconds remain in the API evidence.
export function eventViewport(at: string, start: string, end: string): { startValue: number; endValue: number; marker: number } | undefined {
  const marker = Date.parse(at), left = Date.parse(start), right = Date.parse(end)
  if (![marker, left, right].every(Number.isFinite) || right <= left || marker < left || marker > right) return
  const width = Math.min(right - left, Math.max(60000, (right - left) / 10))
  const startValue = Math.max(left, Math.min(marker - width / 2, right - width))
  return { marker, startValue, endValue: Math.min(right, startValue + width) }
}
