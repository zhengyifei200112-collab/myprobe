// Only opens the supplied disposable loopback Server. Credentials are unnecessary.
const fs = require('node:fs/promises')
const assert = require('node:assert/strict')
const config = JSON.parse(process.argv[2])
const { chromium } = require(config.playwrightModule || 'playwright')
if (new URL(config.base).hostname !== '127.0.0.1') throw new Error('Disposable loopback Server required')
;(async () => {
  const browser = await chromium.launch({ channel: config.channel || (process.platform === 'win32' ? 'msedge' : undefined), headless: true })
  const stats = { mode: 'playwright-rendered', pages: 5, snapshots: 0, events: 0, pageErrors: 0, convergedNodes: 0, convergedPages: 0 }
  const pages = []
  try {
    for (let i = 0; i < 5; i++) {
      const context = await browser.newContext({ viewport: { width: 1440, height: 900 } })
      const page = await context.newPage()
      pages.push(page)
      page.on('pageerror', () => stats.pageErrors++)
      page.on('websocket', socket => {
        socket.on('framereceived', ({ payload }) => {
          try {
            const message = JSON.parse(payload.toString())
            if (message.type === 'snapshot') stats.snapshots++
            else stats.events++
          } catch { stats.pageErrors++ }
        })
      })
      await page.goto(config.base)
      await page.getByText('Synthetic load 000', { exact: true }).first().waitFor()
    }
    await fs.writeFile(config.readyFile, 'ready\n')
    const deadline = Date.now() + config.timeoutMs
    while (Date.now() < deadline) {
      try { await fs.access(config.stopFile); break } catch {}
      await new Promise(resolve => setTimeout(resolve, 250))
    }
    assert.ok(stats.snapshots >= 5, 'all rendered pages need a real snapshot')
    assert.ok(stats.events > 0, 'rendered pages need live events')
    assert.equal(stats.pageErrors, 0)
    let expected
    try { expected = JSON.parse(await fs.readFile(config.expectedFile, 'utf8')) } catch (error) { if (error.code !== 'ENOENT') throw error }
    if (expected) {
      const expectedCount = Object.keys(expected).length
      for (const page of pages) {
        await page.waitForFunction(expected => {
          const cards = [...document.querySelectorAll('article.public-node-card')]
          if (cards.length !== Object.keys(expected).length) return false
          return cards.every(card => {
            const name = card.querySelector('h3')?.textContent
            const progress = card.querySelector('[role="progressbar"][aria-label="CPU 使用率"]')
            return Object.hasOwn(expected, name) && Number(progress?.getAttribute('aria-valuenow')) === expected[name]
          })
        }, expected, { timeout: 20000 })
        stats.convergedPages++
      }
      stats.convergedNodes = expectedCount
    }
    await fs.writeFile(config.resultFile, JSON.stringify(stats, null, 2) + '\n')
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
