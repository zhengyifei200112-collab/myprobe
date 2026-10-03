// Local UI acceptance with synthetic API fixtures; no production data or writes.
const assert = require('node:assert/strict')
const fs = require('node:fs/promises')
const path = require('node:path')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright')

const base = process.env.PROBE_TEST_URL || 'http://127.0.0.1:5173'
if (!['127.0.0.1', 'localhost', '[::1]'].includes(new URL(base).hostname)) throw new Error('Use a loopback test server')
const durations = { '1h': 3600, '12h': 43200, '1d': 86400, '3d': 259200, '7d': 604800, '30d': 2592000, '1y': 31536000 }
const captured = new Date(Date.now() - 60000).toISOString()
const node = {
  node: { id: 'fixture', name: 'Synthetic detail node', tags: ['test'], last_seen_at: captured, report_seconds: 5 },
  online: true, stale: false, traffic: { rx_bytes: 0, tx_bytes: 0 },
  report: { captured_at: captured, cpu: { usage_percent: 24 }, memory: { usage_percent: 48 }, disks: [], networks: [{ rx_bytes_per_second: 1024, tx_bytes_per_second: 512 }], uptime_seconds: 86400 },
}

function history(url) {
  const range = url.searchParams.get('range') || 'custom'
  const end = url.searchParams.get('end') || new Date(Date.now() - 1000).toISOString()
  const start = url.searchParams.get('start') || new Date(Date.parse(end) - durations[range] * 1000).toISOString()
  const bucket = Math.ceil((Date.parse(end) - Date.parse(start)) / 1000 / 100)
  const points = Array.from({ length: 100 }, (_, i) => i).filter(i => i < 30 || i > 45).map(i => ({ time: new Date(Date.parse(start) + i * bucket * 1000).toISOString(), i }))
  return {
    range, start, end, bucket_seconds: bucket, interval: '[start,end)', rollup_boundary_policy: 'complete_buckets_only',
    metrics: points.map(p => ({ time: p.time, cpu_percent: 20 + p.i % 20, memory_percent: 48, disk_percent: 30, rx_bytes_per_second: 1024 + p.i * 20, tx_bytes_per_second: 512 })),
    latency: points.map(p => ({ time: p.time, target_id: 'target', name: 'Example target', kind: 'tcping', latency_ms: p.i % 12 === 0 ? undefined : 20 + p.i % 10, success_rate: 100 })),
    traffic: points.map(p => ({ time: p.time, rx_bytes: p.i * 1024, tx_bytes: p.i * 512, total_bytes: p.i * 1536 })),
  }
}

async function main() {
  const browser = await chromium.launch({ channel: 'msedge', headless: true })
  try {
    if (process.env.PROBE_SCREENSHOT_DIR) await fs.mkdir(process.env.PROBE_SCREENSHOT_DIR, { recursive: true })
    for (const theme of ['light', 'dark']) for (const width of [360, 768, 1440]) {
      const context = await browser.newContext({ viewport: { width, height: 1000 }, locale: 'zh-CN', timezoneId: 'Asia/Shanghai', reducedMotion: 'reduce', colorScheme: theme })
      const page = await context.newPage()
      const errors = []
      let hidden = false
      let failHistory = false
      page.on('pageerror', error => errors.push(error.message))
      await page.addInitScript(theme => { localStorage.setItem('myprobe-theme', theme) }, theme)
      await page.route('**/api/v1/**', async route => {
        const url = new URL(route.request().url())
        if (url.pathname.endsWith('/settings')) return route.fulfill({ json: { settings: { site_name: 'MyProbe UI acceptance' } } })
        if (hidden) return route.fulfill({ status: 404, json: { error: 'node not found' } })
        if (url.pathname.endsWith('/history')) {
          if (failHistory) return route.fulfill({ status: 503, json: { error: 'test failure' } })
          if (url.searchParams.get('range') === '1d') await new Promise(resolve => setTimeout(resolve, 200))
          return route.fulfill({ json: history(url) }).catch(() => {})
        }
        return route.fulfill({ json: { node, server_time: new Date().toISOString() } })
      })
      const ready = async () => { await page.locator('.detail-chart canvas').nth(3).waitFor(); await page.getByRole('heading', { name: node.node.name, exact: true }).waitFor() }
      await page.goto(`${base}/nodes/fixture?range=1h`)
      await ready()
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, `${theme}/${width}: horizontal overflow`)
      await page.getByRole('button', { name: '放大中间时段' }).click()
      await page.getByRole('button', { name: '重置缩放' }).click()
      await page.getByLabel('开始时间', { exact: true }).fill('2026-01-01T10:00')
      await page.getByLabel('结束时间', { exact: true }).fill('2026-01-01T11:00')
      await page.getByRole('button', { name: '应用时间范围' }).click()
      await page.waitForURL(url => url.searchParams.has('start'))
      await ready()
      const absoluteURL = page.url()
      await page.reload()
      await ready()
      assert.equal(page.url(), absoluteURL)
      await page.goBack()
      await ready()
      assert.equal(new URL(page.url()).searchParams.get('range'), '1h')
      await page.getByRole('button', { name: '1d', exact: true }).click()
      await page.getByRole('button', { name: '7d', exact: true }).click()
      await ready()
      assert.equal(new URL(page.url()).searchParams.get('range'), '7d')
      await page.waitForTimeout(250) // Let the deliberately delayed old response finish.
      await page.getByText(/每桶 6048 秒/).waitFor()
      const skip = await page.locator('.skip-link').evaluate(element => ({ bottom: element.getBoundingClientRect().bottom, focused: element === document.activeElement, transform: getComputedStyle(element).transform }))
      assert.ok(skip.focused || skip.bottom <= 0, `skip link should not cover navigation: ${JSON.stringify(skip)}`)
      await page.evaluate(() => window.scrollTo(0, 0))
      if (process.env.PROBE_SCREENSHOT_DIR) await page.screenshot({ path: path.join(process.env.PROBE_SCREENSHOT_DIR, `detail-${theme}-${width}.png`), fullPage: true, animations: 'disabled' })
      failHistory = true
      await page.getByRole('button', { name: '刷新数据', exact: true }).click()
      await page.getByRole('alert').waitFor()
      failHistory = false
      await page.getByRole('button', { name: '重试', exact: true }).focus()
      await page.keyboard.press('Enter')
      await ready()
      hidden = true
      await page.reload()
      await page.getByText('节点不存在或已隐藏。', { exact: true }).waitFor()
      assert.equal(await page.locator('.detail-chart canvas').count(), 0)
      assert.equal(await page.getByRole('heading', { name: node.node.name, exact: true }).count(), 0)
      assert.deepEqual(errors, [], `${theme}/${width}: browser errors`)
      await context.close()
      console.log(`PASS ${theme} ${width}: history, navigation, retry, hidden-node privacy`)
    }
  } finally { await browser.close() }
}
main().catch(error => { console.error(error); process.exitCode = 1 })
