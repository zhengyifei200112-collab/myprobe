import assert from 'node:assert/strict'
import test from 'node:test'
import { createLocalSampleTimeFormatter } from './sample-time.ts'
import { lastContact, lastReport } from './metrics.ts'
import type { PublicNode } from '../types.ts'

test('sample text exactly matches the original locale formatter across timezones and boundaries', () => {
  const previous = process.env.TZ
  const format = createLocalSampleTimeFormatter()
  try {
    // Reuse one cache while changing zones, including DST folds/gaps and midnight.
    for (const zone of ['UTC', 'Asia/Shanghai', 'America/New_York', 'Pacific/Chatham', 'UTC']) {
      process.env.TZ = zone
      for (const instant of ['2030-01-01T00:00:00Z', '2030-01-01T00:00:00.999Z', '2030-01-01T23:59:59.999Z', '2030-03-10T06:59:59Z', '2030-03-10T07:00:00Z', '2030-11-03T05:30:00Z', '2030-11-03T06:30:00Z']) {
        const value = new Date(instant)
        assert.equal(format(value), value.toLocaleString('zh-CN', { hour12: false }), `${zone} ${instant}`)
      }
    }
  } finally {
    if (previous === undefined) delete process.env.TZ
    else process.env.TZ = previous
  }
})

test('same local second formats once while the cache retains at most 256 entries', t => {
  const original = Date.prototype.toLocaleString
  let calls = 0
  t.mock.method(Date.prototype, 'toLocaleString', function (this: Date, ...args: Parameters<Date['toLocaleString']>) {
    calls++
    return original.apply(this, args)
  })
  const format = createLocalSampleTimeFormatter()
  const base = Date.parse('2030-01-01T00:00:00Z')
  for (let index = 0; index < 500; index++) format(new Date(base + index % 1000))
  assert.equal(calls, 1)
  for (let index = 1; index <= 256; index++) format(new Date(base + index * 1000))
  assert.equal(calls, 257)
  format(new Date(base))
  assert.equal(calls, 258, 'the oldest entry must have been evicted')
})

test('missing and invalid capture/contact timestamps retain their independent placeholders', () => {
  const item = { node: { last_seen_at: 'invalid' }, report: { captured_at: 'invalid' } } as unknown as PublicNode
  assert.equal(lastReport(item), '暂无上报数据')
  assert.equal(lastContact(item), '尚无联系记录')
  item.node.last_seen_at = '2030-01-01T00:00:00Z'
  assert.equal(lastContact(item), new Date(item.node.last_seen_at).toLocaleString('zh-CN', { hour12: false }))
  assert.equal(lastReport(item), '暂无上报数据')
})
