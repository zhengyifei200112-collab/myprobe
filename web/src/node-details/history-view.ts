import type { HistorySelection } from '../types.ts'

export const historyRanges = ['1h', '12h', '1d', '3d', '7d', '30d', '1y'] as const

export function readHistorySelection(search: string): HistorySelection {
  const query = new URLSearchParams(search)
  for (const key of ['range', 'start', 'end']) {
    if (query.getAll(key).length > 1) throw new Error('时间参数重复，请重新选择时间范围。')
  }
  if (query.has('start') || query.has('end')) {
    const start = query.get('start') || ''
    const end = query.get('end') || ''
    const timestamp = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/
    const duration = Date.parse(end) - Date.parse(start)
    if (query.has('range') || !timestamp.test(start) || !timestamp.test(end) || !Number.isFinite(duration) || duration <= 0 || duration > 365 * 86400000) {
      throw new Error('请提供有效的起止时间，范围最长为 365 天。')
    }
    return { start, end }
  }
  const range = query.get('range') || '1h'
  if (!historyRanges.includes(range as typeof historyRanges[number])) throw new Error('时间范围无效，请重新选择。')
  return range as typeof historyRanges[number]
}

export function selectionQuery(selection: HistorySelection): string {
  return new URLSearchParams(typeof selection === 'string' ? { range: selection } : selection).toString()
}

export function localDateTime(value: string): string {
  const date = new Date(value)
  const shifted = new Date(date.getTime() - date.getTimezoneOffset() * 60000)
  return shifted.toISOString().slice(0, 19)
}

// Insert a null separator wherever an output bucket has no observation. Single
// observations stay visible as dots; missing measurements are never zeros.
export function withGaps(points: Array<[string, number | null]>, bucketSeconds: number): Array<[number, number | null]> {
  const sorted = points.map(([time, value]) => [Date.parse(time), value] as [number, number | null]).sort((a, b) => a[0] - b[0])
  const result: Array<[number, number | null]> = []
  const width = bucketSeconds * 1000
  for (const point of sorted) {
    const previous = result.at(-1)
    if (previous && point[0] - previous[0] > width) result.push([previous[0] + width, null])
    result.push(point)
  }
  return result
}
