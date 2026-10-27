import test from 'node:test'
import assert from 'node:assert/strict'
import { eventLabel, eventViewport } from './timeline-view.ts'

test('timeline labels distinguish observed anomaly, trigger threshold and closure', () => {
  assert.equal(eventLabel({ kind: 'incident_observed' }), '首次观测到异常')
  assert.equal(eventLabel({ kind: 'incident_fired' }), '达到告警门槛')
  assert.equal(eventLabel({ kind: 'incident_closed' }), '告警已结束')
  assert.equal(eventLabel({ kind: 'reboot_hint' }), '检测到重启迹象')
  assert.equal(eventLabel({ kind: '<script>' }), '节点事件')
})
test('event curve viewport clamps boundary events and rejects unknown dates', () => {
  const start = '2026-01-01T00:00:00Z', end = '2026-01-01T01:00:00Z'
  const a = eventViewport(start, start, end), b = eventViewport(end, start, end)
  assert.equal(a.startValue, Date.parse(start))
  assert.equal(b.endValue, Date.parse(end))
  assert.equal(a.endValue - a.startValue, 360000)
  assert.equal(eventViewport('invalid', start, end), undefined)
  assert.equal(eventViewport('2025-01-01T00:00:00Z', start, end), undefined)
  assert.equal(eventViewport(start, start, start), undefined)
})
