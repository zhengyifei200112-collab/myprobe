import assert from 'node:assert/strict'
import test from 'node:test'
import { readHistorySelection, selectionQuery, withGaps } from './history-view.ts'
import { adminDetailDestination } from './navigation.ts'

test('absolute windows round-trip timezone offsets through the URL', () => {
  const selection = { start: '2026-10-01T12:00:00+08:00', end: '2026-10-01T13:00:00+08:00' }
  assert.deepEqual(readHistorySelection(selectionQuery(selection)), selection)
  assert.equal(readHistorySelection(''), '1h')
  for (const query of ['range=invalid', 'range=1h&range=1d', 'start=', 'start=2026-01-01&end=2026-01-02', `range=1h&${selectionQuery(selection)}`, selectionQuery({ start: selection.end, end: selection.start })]) {
    assert.throws(() => readHistorySelection(query))
  }
})

test('OAuth continuation is restricted to local administrator node paths', () => {
  assert.equal(adminDetailDestination('/admin/nodes/example?range=7d'), '/admin/nodes/example?range=7d')
  for (const value of [null, '', 'https://example.com/admin/nodes/test', '//example.com/admin/nodes/test', '/admin/nodes/../settings', '/admin/nodes/test/other', '/admin/nodes/\\example.com', '/api/v1/admin/nodes/test']) assert.equal(adminDetailDestination(value), null)
})

test('missing buckets break curves and preserve explicit failed observations', () => {
  const base = Date.parse('2026-10-01T00:00:00Z')
  const at = (seconds) => new Date(base + seconds * 1000).toISOString()
  assert.deepEqual(withGaps([[at(60), 8], [at(0), 0], [at(15), null]], 15), [
    [base, 0], [base + 15000, null], [base + 30000, null], [base + 60000, 8],
  ])
  assert.deepEqual(withGaps([], 15), [])
})
