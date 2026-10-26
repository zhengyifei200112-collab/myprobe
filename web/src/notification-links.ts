export type NotificationTarget = { kind: 'incident'; id: string } | { kind: 'aggregate'; id: string; snapshot?: string }
const idPattern = /^[A-Za-z0-9][A-Za-z0-9_.:\-]{0,127}$/
const snapshotPattern = /^[a-f0-9]{64}$/
const pendingKey = 'myprobe-notification-navigation-v1'

export function parseNotificationTarget(search: string): { target: NotificationTarget | null; error: string; explicit: boolean } {
  const q = new URLSearchParams(search)
  const explicit = ['incident', 'aggregate', 'snapshot'].some(key => q.has(key))
  if (!explicit) return { target: null, error: '', explicit }
  const invalid = { target: null, error: '通知入口无效，请从故障事件或聚合记录重新选择。', explicit }
  if (['incident', 'aggregate', 'snapshot'].some(key => q.getAll(key).length > 1) || (q.has('incident') && q.has('aggregate'))) return invalid
  if (q.has('incident')) {
    const id = q.get('incident') || ''
    return idPattern.test(id) && !q.has('snapshot') ? { target: { kind: 'incident', id }, error: '', explicit } : invalid
  }
  const id = q.get('aggregate') || '', snapshot = q.get('snapshot')
  return idPattern.test(id) && (snapshot === null || snapshotPattern.test(snapshot)) ? { target: { kind: 'aggregate', id, ...(snapshot ? { snapshot } : {}) }, error: '', explicit } : invalid
}
export function notificationPath(target: NotificationTarget): string {
  const q = new URLSearchParams({ [target.kind]: target.id })
  if (target.kind === 'aggregate' && target.snapshot) q.set('snapshot', target.snapshot)
  return `/admin?${q}`
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
