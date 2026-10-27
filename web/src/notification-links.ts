export type ServiceDiagnosticTarget = { kind: 'service'; id: string; observer: string; start: string; end: string }
export type NotificationTarget = { kind: 'incident'; id: string } | { kind: 'aggregate'; id: string; snapshot?: string } | ServiceDiagnosticTarget
const idPattern = /^[A-Za-z0-9][A-Za-z0-9_.:\-]{0,127}$/
const snapshotPattern = /^[a-f0-9]{64}$/
const pendingKey = 'myprobe-notification-navigation-v1'

function diagnosticInstant(value: string): bigint | null {
  const match = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d{1,9}))?Z$/.exec(value)
  if (!match) return null
  const ms = Date.parse(`${match[1]}Z`)
  if (!Number.isFinite(ms) || new Date(ms).toISOString().slice(0, 19) !== match[1] || Number(value.slice(0, 4)) < 1970 || Number(value.slice(0, 4)) > 2100) return null
  return BigInt(ms) * BigInt(1000000) + BigInt((match[2] || '').padEnd(9, '0'))
}

export function parseNotificationTarget(search: string): { target: NotificationTarget | null; error: string; explicit: boolean } {
  const q = new URLSearchParams(search)
  const keys = ['incident', 'aggregate', 'snapshot', 'service', 'observer', 'start', 'end']
  const explicit = keys.some(key => q.has(key))
  if (!explicit) return { target: null, error: '', explicit }
  const invalid = { target: null, error: '通知入口无效，请从故障事件或聚合记录重新选择。', explicit }
  if (keys.some(key => q.getAll(key).length > 1) || ['incident', 'aggregate', 'service'].filter(key => q.has(key)).length !== 1) return invalid
  if (q.has('service')) {
    if (q.has('snapshot')) return invalid
    const id = q.get('service') || '', observer = q.get('observer') || '', start = q.get('start') || '', end = q.get('end') || ''
    const first = diagnosticInstant(start), last = diagnosticInstant(end)
    return idPattern.test(id) && idPattern.test(observer) && first !== null && last !== null && last > first && last - first <= BigInt(2678400000000000)
      ? { target: { kind: 'service', id, observer, start, end }, error: '', explicit } : invalid
  }
  if (['observer', 'start', 'end'].some(key => q.has(key))) return invalid
  if (q.has('incident')) {
    const id = q.get('incident') || ''
    return idPattern.test(id) && !q.has('snapshot') ? { target: { kind: 'incident', id }, error: '', explicit } : invalid
  }
  const id = q.get('aggregate') || '', snapshot = q.get('snapshot')
  return idPattern.test(id) && (snapshot === null || snapshotPattern.test(snapshot)) ? { target: { kind: 'aggregate', id, ...(snapshot ? { snapshot } : {}) }, error: '', explicit } : invalid
}
export function notificationPath(target: NotificationTarget): string {
  const q = new URLSearchParams({ [target.kind]: target.id })
  if (target.kind === 'service') { q.set('observer', target.observer); q.set('start', target.start); q.set('end', target.end) }
  if (target.kind === 'aggregate' && target.snapshot) q.set('snapshot', target.snapshot)
  return `/admin?${q}`
}
export function serviceDiagnosticAt(serviceID: string, observer: string, at: string): string {
  const stamp = Date.parse(at)
  if (!Number.isFinite(stamp) || stamp < Date.UTC(1970, 0, 1) || stamp >= Date.UTC(2101, 0, 1)) return ''
  const target: ServiceDiagnosticTarget = { kind: 'service', id: serviceID, observer, start: new Date(stamp - 1800000).toISOString(), end: new Date(stamp + 300000).toISOString() }
  const path = notificationPath(target)
  return parseNotificationTarget(path.slice('/admin'.length)).target ? path : ''
}
export function rememberNotificationTarget(target: NotificationTarget | null) {
  try {
    if (target) sessionStorage.setItem(pendingKey, JSON.stringify({ path: notificationPath(target), savedAt: Date.now() }))
    else sessionStorage.removeItem(pendingKey)
  } catch { /* Storage restrictions must not prevent login. */ }
}
export function pendingNotificationTarget(): NotificationTarget | null {
  try {
    const raw = sessionStorage.getItem(pendingKey)
    if (!raw) return null
    if (raw.length > 1024) throw new Error('invalid navigation')
    const value = JSON.parse(raw)
    if (typeof value.path !== 'string' || !value.path.startsWith('/admin?') || !Number.isSafeInteger(value.savedAt) || Date.now() - value.savedAt < 0 || Date.now() - value.savedAt > 3600000) throw new Error('expired navigation')
    const parsed = parseNotificationTarget(value.path.slice('/admin'.length))
    if (!parsed.target || notificationPath(parsed.target) !== value.path) throw new Error('invalid navigation')
    return parsed.target
  } catch { rememberNotificationTarget(null); return null }
}
