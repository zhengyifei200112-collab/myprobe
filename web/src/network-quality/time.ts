interface LocalParts { year: number; month: number; day: number; hour: number; minute: number; second: number }
function parts(instant: number, zone: string): LocalParts {
  const fields = new Intl.DateTimeFormat('en-CA', { timeZone: zone, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' }).formatToParts(instant)
  return Object.fromEntries(fields.filter(p => p.type !== 'literal').map(p => [p.type, Number(p.value)])) as unknown as LocalParts
}
const stamp = (p: LocalParts) => Date.UTC(p.year, p.month - 1, p.day, p.hour, p.minute, p.second)
const pad = (n: number) => String(n).padStart(2, '0')
export function localInput(instant: number, zone: string): string {
  const p = parts(instant, zone)
  return `${p.year}-${pad(p.month)}-${pad(p.day)}T${pad(p.hour)}:${pad(p.minute)}`
}
export function localInstant(value: string, zone: string): number {
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/.exec(value)
  if (!match) throw new Error('请选择完整的日期和时间。')
  const [year, month, day, hour, minute] = match.slice(1).map(Number)
  const wanted = { year, month, day, hour, minute, second: 0 }
  const naive = stamp(wanted), check = new Date(naive)
  if (check.getUTCFullYear() !== year || check.getUTCMonth() + 1 !== month || check.getUTCDate() !== day || hour > 23 || minute > 59 || year < 1970) throw new Error('日期或时间无效。')
  const offsets = new Set<number>()
  for (let delta = -48; delta <= 48; delta += 6) { const at = naive + delta * 3600000; offsets.add(stamp(parts(at, zone)) - at) }
  const matches = [...offsets].map(offset => naive - offset).filter(at => stamp(parts(at, zone)) === naive)
  if (matches.length !== 1) throw new Error(matches.length ? '所选时刻在该时区重复，请切换 UTC 后选择明确时刻。' : '所选时刻处于时区跳时，请选择其他时刻。')
  return matches[0]
}
export function lastEvening(now: number, zone: string): { start: string; end: string } {
  const p = parts(now, zone)
  const day = new Date(Date.UTC(p.year, p.month - 1, p.day))
  const make = (hour: number) => `${day.getUTCFullYear()}-${pad(day.getUTCMonth() + 1)}-${pad(day.getUTCDate())}T${pad(hour)}:00`
  let end = localInstant(make(23), zone)
  if (end > now) { day.setUTCDate(day.getUTCDate() - 1); end = localInstant(make(23), zone) }
  return { start: new Date(localInstant(make(19), zone)).toISOString(), end: new Date(end).toISOString() }
}
export function customRange(start: string, end: string, zone: string, now: number): { start: string; end: string } {
  const a = localInstant(start, zone), b = localInstant(end, zone)
  if (b <= a || b > now || b - a > 365 * 86400000) throw new Error('请选择过去 365 天内、起点早于终点的范围。')
  return { start: new Date(a).toISOString(), end: new Date(b).toISOString() }
}
