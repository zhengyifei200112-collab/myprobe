import assert from 'node:assert/strict'
import test from 'node:test'
import { createNodeCache } from './node-cache.ts'
import type { PublicNode } from '../types.ts'

function fixture() {
  let nodes = [{ node: { id: 'first' } }] as PublicNode[]
  let reads = 0
  const writes: PublicNode[][] = []
  let current = 0
  let nextID = 0
  const timers = new Map<number, { at: number; run: () => void }>()
  const clock = {
    setTimeout(run: () => void, milliseconds: number) {
      const id = ++nextID
      timers.set(id, { at: current + milliseconds, run })
      return id
    },
    clearTimeout(id: number) { timers.delete(id) },
  }
  const cache = createNodeCache(() => { reads++; return nodes }, text => writes.push(JSON.parse(text)), clock)
  function advance(milliseconds: number) {
    const until = current + milliseconds
    for (;;) {
      const due = [...timers].filter(([, timer]) => timer.at <= until).sort((a, b) => a[1].at - b[1].at)[0]
      if (!due) break
      current = due[1].at
      timers.delete(due[0])
      due[1].run()
    }
    current = until
  }
  return { cache, writes, timers, advance, reads: () => reads, replace: (value: PublicNode[]) => { nodes = value } }
}

test('a burst renders elsewhere immediately and serializes only the latest nodes at 250 ms', () => {
  const f = fixture()
  for (let index = 0; index < 500; index++) {
    f.replace([{ node: { id: String(index) } }] as PublicNode[])
    f.cache.schedule()
  }
  assert.equal(f.reads(), 0, 'serialization must be deferred too')
  assert.equal(f.timers.size, 1)
  f.advance(249)
  assert.equal(f.writes.length, 0)
  f.advance(1)
  assert.equal(f.reads(), 1)
  assert.equal(f.writes[0][0].node.id, '499')
  f.advance(1000)
  assert.equal(f.writes.length, 1, 'idle pages must not rewrite the cache')
})

test('continuous events retain the first deadline instead of starving persistence', () => {
  const f = fixture()
  for (let index = 0; index < 10; index++) { f.cache.schedule(); f.advance(100) }
  assert.equal(f.writes.length, 3)
  f.advance(150)
  assert.equal(f.writes.length, 4)
})

test('an immediate empty/privacy snapshot cancels pending data and never resurrects it', () => {
  const f = fixture()
  f.cache.schedule()
  f.replace([])
  f.cache.writeNow()
  assert.deepEqual(f.writes, [[]])
  assert.equal(f.timers.size, 0)
  f.advance(1000)
  assert.deepEqual(f.writes, [[]])
  f.replace([{ node: { id: 'new-visible' } }] as PublicNode[])
  f.cache.schedule()
  f.advance(250)
  assert.equal(f.writes[1][0].node.id, 'new-visible')
})

test('page exit flushes once and disposal cancels future writes', () => {
  const f = fixture()
  f.cache.schedule()
  f.cache.flush()
  assert.equal(f.writes.length, 1)
  f.cache.flush()
  f.cache.schedule()
  f.cache.dispose()
  f.cache.dispose()
  f.cache.schedule()
  f.cache.writeNow()
  f.advance(1000)
  assert.equal(f.writes.length, 2)
  assert.equal(f.timers.size, 0)
})

test('storage failures leave the cache usable for later events', () => {
  let fail = true
  const writes: string[] = []
  const cache = createNodeCache(() => [], text => { if (fail) throw new Error('quota'); writes.push(text) })
  assert.doesNotThrow(() => cache.writeNow())
  fail = false
  cache.writeNow()
  assert.deepEqual(writes, ['[]'])
  cache.dispose()
})
