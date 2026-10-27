import type { HTTPCurrentHealth } from '../admin-api'

export function healthIsFresh(health: HTTPCurrentHealth | undefined, elapsedMS: number): boolean {
  if (!health || health.policy !== 'any_required_observer_failure' || !Number.isFinite(elapsedMS) || elapsedMS < 0) return false
  const asOf = Date.parse(health.as_of)
  const counts = [health.required_observers, health.success, health.failure, health.unknown]
  if (!Number.isFinite(asOf) || counts.some(n => !Number.isInteger(n) || n < 0 || n > 100) || health.required_observers !== health.success + health.failure + health.unknown) return false
  if (health.state === 'healthy' && (health.required_observers === 0 || health.success !== health.required_observers)) return false
  if (health.state === 'degraded' && health.failure === 0) return false
  if (health.state === 'unknown' && (health.failure > 0 || (health.required_observers > 0 && health.unknown === 0))) return false
  if (!['healthy', 'degraded', 'unknown'].includes(health.state)) return false
  if ((health.success + health.failure > 0) && !health.valid_until) return false
  const deadline = health.valid_until ? Date.parse(health.valid_until) : asOf + 30000
  return Number.isFinite(deadline) && elapsedMS <= Math.min(30000, deadline - asOf)
}

export function healthLabel(health: HTTPCurrentHealth | undefined, fresh: boolean) {
  if (!health) return '状态未加载'
  if (!fresh) return '状态快照已过期'
  return { healthy: '服务正常', degraded: '服务降级', unknown: '服务状态未知' }[health.state] || '服务状态未知'
}

export const observationReasons: Record<string, string> = {
  service_disabled: '服务配置已停用', execution_disabled: '服务端未启用 HTTP 执行', agent_offline: 'Agent 未在线上报',
  unsupported: 'Agent 不支持 HTTP 检查', agent_unavailable: '当前 Agent 连接不可执行 HTTP',
  not_observed: '当前配置尚无检查任务', pending: '等待本次任务回报', missing_result: '回报宽限期已结束，未收到结果',
  stale_result: '观测已过期或时间无效', unobserved: '未执行有效检查', invalid_result: '无法确认本次结果',
  dns: 'DNS 解析失败', refused: '连接被拒绝', timeout: '请求超时', tls_invalid: 'TLS 验证失败',
  certificate_expired: '证书已过期', status_mismatch: '状态码不符合要求', content_mismatch: '内容断言不符合要求',
  response_too_large: '响应超过大小限制', redirect_limit: '重定向超过限制', busy: 'Agent 繁忙', cancelled: '任务已取消',
  invalid_task: '任务无效', policy_denied: 'Agent 本地策略拒绝', internal: '执行器内部错误',
}
