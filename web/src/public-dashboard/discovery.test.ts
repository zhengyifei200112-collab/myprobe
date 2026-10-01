import assert from 'node:assert/strict'
import test from 'node:test'
import { attentionReasons, defaults, expiring, matchesQuery, nodeState, queryURL, readQuery, sortNodes } from './discovery.ts'
import type { PublicNode } from '../types.ts'

const now = Date.parse('2030-01-01T00:00:00Z')
function fixture(id: string, cpu = 10): PublicNode {
  return { node: { id, name: `Host ${id}`, tags: ['Tokyo'], sort_order: 0, last_seen_at: '2030-01-01T00:00:00Z' }, online: true, stale: false, report: { cpu: { usage_percent: cpu }, memory: { usage_percent: 20 }, disks: [] } } as unknown as PublicNode
}
test('search, tag and state intersect across 100 nodes without mutating inputs', () => {
  const nodes = Array.from({ length: 100 }, (_, i) => fixture(String(i), i))
  const query = { ...defaults, q: 'TOKYO', tag: 'Tokyo', status: 'attention' }
  assert.equal(nodes.filter(node => matchesQuery(node, query, now)).length, 10)
  assert.equal(nodes.filter(node => matchesQuery(node, { ...query, tag: 'other' }, now)).length, 0)
  assert.equal(sortNodes(nodes, { ...defaults, sort: 'cpu' })[0]!.node.id, '99')
  assert.equal(nodes[0]!.node.id, '0')
})
test('waiting, interrupted and stale data remain distinct; stale resources do not create threshold reasons', () => {
  const node = fixture('a', 99)
  node.online = false
  assert.equal(nodeState(node), 'interrupted')
  assert.deepEqual(attentionReasons(node, now), ['上报中断'])
  node.online = true; node.stale = true
  assert.deepEqual(attentionReasons(node, now), ['数据陈旧'])
  node.report = undefined; node.node.last_seen_at = undefined
  assert.equal(nodeState(node), 'waiting')
  assert.deepEqual(attentionReasons(node, now), [])
})
test('expiry boundary is inclusive at seven days; past dates are attention only', () => {
  const node = fixture('a')
  node.node.expires_at = new Date(now + 7 * 86400000).toISOString()
  assert.equal(expiring(node, now), true)
  assert.equal(expiring(node, now - 1), false)
  node.node.expires_at = new Date(now - 1).toISOString()
  assert.equal(expiring(node, now), false)
  assert.deepEqual(attentionReasons(node, now), ['已到期'])
})
test('missing values sort last in both directions and ties use node IDs', () => {
  const a = fixture('a', 90), b = fixture('b', 90), missing = fixture('0')
  missing.stale = true
  assert.deepEqual(sortNodes([b, missing, a], { ...defaults, sort: 'cpu' }).map(n => n.node.id), ['a', 'b', '0'])
  a.node.expires_at = new Date(now).toISOString()
  assert.equal(sortNodes([missing, a], { ...defaults, sort: 'expiry' })[0], a)
  a.latency = [{ target_id: 'x', name: 'X', kind: 'ping', success: true, latency_ms: 2 }, { target_id: 'y', name: 'Y', kind: 'ping', success: true, latency_ms: 200 }]
  b.latency = [{ target_id: 'x', name: 'X', kind: 'ping', success: true, latency_ms: 3 }]
  assert.equal(sortNodes([a, b], { ...defaults, sort: 'latency', target: 'x' })[0], b)
})
test('URL overrides local preferences, preserves unrelated parameters and validates enums', () => {
  assert.equal(readQuery('?sort=default&view=cards', { sort: 'cpu', view: 'table' }).sort, 'default')
  assert.equal(readQuery('', { sort: 'cpu', q: 'ignored' }).q, '')
  assert.equal(readQuery('?sort=garbage&status=garbage&view=garbage').sort, 'default')
  const url = queryURL(new URL('https://example.test/?unrelated=1#anchor'), { ...defaults, q: '東京 &' })
  assert.ok(url.includes('unrelated=1')); assert.ok(url.endsWith('#anchor'))
  assert.equal(readQuery(new URL(url, 'https://example.test').search).q, '東京 &')
})
