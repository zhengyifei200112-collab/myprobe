import assert from 'node:assert/strict'
import test from 'node:test'
import { AdminAPIError, AdminRequestError, previewNodeBatch } from './admin-api.ts'

test('management conflicts retain both policy codes and node conflicts', async t => {
  const original = globalThis.fetch
  t.after(() => { globalThis.fetch = original })
  globalThis.fetch = async () => new Response(JSON.stringify({
    error: 'Preview changed', code: 'preview_changed', conflict_node_ids: ['node-a'],
  }), { status: 409, headers: { 'Content-Type': 'application/json' } })
  await assert.rejects(previewNodeBatch({ node_ids: ['node-a'], hidden: true }), error => {
    assert(error instanceof AdminRequestError)
    assert(error instanceof AdminAPIError)
    assert.equal(error.status, 409)
    assert.equal(error.code, 'preview_changed')
    assert.deepEqual(error.conflictNodeIDs, ['node-a'])
    assert.equal(error.message, 'Preview changed')
    return true
  })
})

test('a non-JSON management failure remains an actionable HTTP error', async t => {
  const original = globalThis.fetch
  t.after(() => { globalThis.fetch = original })
  globalThis.fetch = async () => new Response('upstream unavailable', { status: 502 })
  await assert.rejects(previewNodeBatch({ node_ids: ['node-a'], hidden: true }), error => {
    assert(error instanceof AdminAPIError)
    assert(error instanceof AdminRequestError)
    assert.equal(error.status, 502)
    assert.equal(error.code, undefined)
    assert.deepEqual(error.conflictNodeIDs, [])
    assert.match(error.message, /502/)
    return true
  })
})
