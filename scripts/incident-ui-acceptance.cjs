// Run against an isolated local server. Reads synthetic API fixtures; sends no notifications.
const assert = require('node:assert/strict')
const fs = require('node:fs/promises')
const path = require('node:path')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright')

async function main() {
  const base = process.env.PROBE_TEST_URL || 'http://127.0.0.1:25777'
  if (!['127.0.0.1', 'localhost', '[::1]'].includes(new URL(base).hostname)) throw Error('Use an isolated loopback server')
  if (!process.env.PROBE_TEST_PASSWORD) throw Error('PROBE_TEST_PASSWORD is required')
  const browser = await chromium.launch({ channel: 'msedge', headless: true })
  try {
    const page = await browser.newPage()
    const errors = []
    page.on('pageerror', error => errors.push(error.message))
    const timestamp = new Date().toISOString()
    const incident = { seq: 1, id: 'fixture', node_id: 'fixture-node', node_name: 'Synthetic incident', kind: 'cpu', state: 'resolved', started_at: timestamp, resolved_at: timestamp, message: 'Synthetic recovery', observation_stale: false, origin: 'live', approximate_start: false }
    const statuses = ['pending', 'inflight', 'delivered', 'failed', 'canceled']
    await page.route('**/api/v1/admin/incidents?*', route => route.fulfill({ json: { incidents: [incident], next_before: 0 } }))
    await page.route('**/api/v1/admin/incidents/fixture/deliveries?*', route => route.fulfill({ json: { deliveries: statuses.map((status, index) => ({ seq: index+1, id: `job-${status}`, channel_name: `Fixture ${status}`, provider: 'webhook', notification_type: index === 2 ? 'resolved' : 'firing', status, attempt_count: 2, created_at: timestamp, available_at: timestamp, error_class: status === 'pending' ? 'http_rate_limited' : '' })), next_before: 0 } }))
    let failAttempts = true
    await page.route('**/api/v1/admin/notification-deliveries/*/attempts', route => failAttempts
      ? route.fulfill({ status: 503, json: { error: 'synthetic attempts unavailable' } })
      : route.fulfill({ json: { attempts: [{ number: 1, started_at: timestamp, completed_at: timestamp, outcome: 'unknown', error_class: 'lease_expired' }, { number: 2, started_at: timestamp, completed_at: timestamp, outcome: 'delivered' }] } }))
    await page.goto(`${base}/admin`)
    await page.getByLabel('用户名', { exact: true }).fill(process.env.PROBE_TEST_USERNAME || 'admin')
    await page.getByLabel('密码', { exact: true }).fill(process.env.PROBE_TEST_PASSWORD)
    await page.getByRole('button', { name: '使用密码登录' }).click()
    await page.getByRole('button', { name: '告警', exact: true }).click()
    await page.getByRole('tab', { name: '故障事件', exact: true }).click()
    await page.getByRole('button', { name: '查看 Synthetic incident 的投递记录' }).click()
    await page.waitForFunction(() => document.activeElement?.matches('.delivery-detail h3'))
    for (const label of ['等待发送', '发送中', '已送达', '发送失败', '已取消']) {
      assert.ok((await page.locator('.delivery-detail').innerText()).includes(label), label)
    }
    const expand = page.getByRole('button', { name: '查看每次尝试', exact: true }).first()
    await expand.click()
    await page.getByRole('alert').filter({ hasText: 'synthetic attempts unavailable' }).waitFor()
    failAttempts = false
    await expand.click()
    await page.getByText(/第 1 次：结果未知，可能已送达/).waitFor()
    await page.getByText(/第 2 次：已送达/).waitFor()
    assert.equal(await page.getByRole('alert').count(), 0)
    for (const width of [360, 768, 1440]) for (const theme of ['light', 'dark']) {
      await page.setViewportSize({ width, height: 900 })
      await page.evaluate(value => document.documentElement.dataset.theme = value, theme)
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `${width}/${theme}`)
      if (process.env.PROBE_SCREENSHOT_DIR) {
        await fs.mkdir(process.env.PROBE_SCREENSHOT_DIR, { recursive: true })
        await page.locator('.delivery-detail').evaluate(element => element.scrollIntoView({ block: 'start' }))
        await page.screenshot({ animations: 'disabled', path: path.join(process.env.PROBE_SCREENSHOT_DIR, `incident-${width}-${theme}.png`) })
      }
    }
    assert.deepEqual(errors, [])
    console.log('PASS synthetic delivery states, attempt failure/retry, unknown outcome, focus and six viewport/theme widths')
  } finally { await browser.close() }
}
main().catch(error => { console.error(error); process.exitCode = 1 })
