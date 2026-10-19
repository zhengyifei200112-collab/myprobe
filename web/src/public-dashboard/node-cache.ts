import type { PublicNode } from '../types'

type CacheClock = {
  setTimeout: (callback: () => void, milliseconds: number) => number
  clearTimeout: (timer: number) => void
}

// Rendering stays immediate. Only the optional local fallback is coalesced.
export function createNodeCache(
  readNodes: () => PublicNode[],
  write: (text: string) => void,
  clock: CacheClock = globalThis,
) {
  let timer: number | undefined
  let pending = false
  let disposed = false

  function flush() {
    if (timer !== undefined) clock.clearTimeout(timer)
    timer = undefined
    if (disposed || !pending) return
    pending = false
    // Read at flush time: a privacy snapshot must replace any queued old nodes.
    try { write(JSON.stringify(readNodes())) } catch { /* optional cache unavailable */ }
  }

  return {
    schedule() {
      if (disposed) return
      pending = true
      // Keep the first deadline, including during a continuous event stream.
      if (timer === undefined) timer = clock.setTimeout(flush, 250)
    },
    writeNow() {
      if (disposed) return
      pending = true
      flush()
    },
    flush,
    dispose() {
      flush()
      disposed = true
    },
  }
}
