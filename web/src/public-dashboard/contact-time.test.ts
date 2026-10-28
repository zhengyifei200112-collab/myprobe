import assert from 'node:assert/strict'
import test from 'node:test'
import { lastContact, lastReport, offlineDuration } from './metrics.ts'
import type { PublicNode } from '../types.ts'

test('contact duration and capture timestamp remain independent', () => {
  const now = new Date('2030-01-01T01:00:00Z')
  const node = { node: { last_seen_at: '2030-01-01T00:59:30Z' }, report: { captured_at: '2030-01-01T00:00:00Z' } } as unknown as PublicNode
  assert.equal(offlineDuration(node, now), '不足 1 分钟')
  assert.notEqual(lastContact(node), lastReport(node))
  node.node.last_seen_at = undefined
  assert.equal(offlineDuration(node, now), '持续时间未知')
  assert.equal(lastContact(node), '尚无联系记录')
  node.node.last_seen_at = 'invalid'
  assert.equal(offlineDuration(node, now), '持续时间未知')
})
