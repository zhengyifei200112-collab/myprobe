// Run against a dedicated, disposable local Server. Creates and deletes only its
// own synthetic node; never use production credentials or a production database.
const assert = require('node:assert/strict')
const fs = require('node:fs/promises')
const path = require('node:path')
const { chromium, request } = require(process.env.PLAYWRIGHT_MODULE || 'playwright')
const base = process.env.PROBE_TEST_URL || 'http://127.0.0.1:25778'
if (!['127.0.0.1', 'localhost', '[::1]'].includes(new URL(base).hostname)) throw new Error('A dedicated loopback Server is required')
const username = process.env.PROBE_TEST_USERNAME
const password = process.env.PROBE_TEST_PASSWORD
if (!username || !password) throw new Error('Explicit disposable test credentials are required')

async function main() {
  const api = await request.newContext({ baseURL: base })
  const login = await api.post('/api/v1/auth/login', { data: { username, password } })
  assert.equal(login.status(), 200, 'test API login')
  const csrf = (await login.json()).csrf_token
  const headers = { 'X-CSRF-Token': csrf }
  const created = await api.post('/api/v1/admin/nodes', { headers, data: { name: 'Local detail acceptance', tags: ['synthetic-test'], collection_seconds: 5, report_seconds: 5 } })
  assert.equal(created.status(), 201, 'create synthetic node')
  const { node, agent_token: token } = await created.json()
  const browser = await chromium.launch({ channel: 'msedge', headless: true })
  try {
    const start = Date.now() - 1800000
    for (let i = 0; i < 40; i++) {
      const result = await api.post('/api/v1/agent/report', {
        headers: { Authorization: `Bearer ${token}` },
        data: { version: 1, type: 'report', sequence: i + 1, sent_at: new Date().toISOString(), payload: {
          captured_at: new Date(start + i * 15000).toISOString(), cpu: { usage_percent: 20 + i % 20 }, memory: { total_bytes: 1024, used_bytes: 512, usage_percent: 50 },
          networks: [{ interface: 'synthetic', rx_total_bytes: i * 1024, tx_total_bytes: i * 512, rx_bytes_per_second: 1024, tx_bytes_per_second: 512 }], uptime_seconds: 3600,
        } },
      })
      assert.equal(result.status(), 200, 'synthetic Agent report accepted')
    }
    const publicDetail = await api.get(`/api/v1/public/nodes/${node.id}`)
    assert.equal(publicDetail.status(), 200)
    const publicContext = await browser.newContext()
    const publicPage = await publicContext.newPage()
    await publicPage.goto(`${base}/nodes/${node.id}?range=1h`)
    await publicPage.locator('.detail-chart canvas').nth(2).waitFor()
    await publicPage.getByRole('heading', { name: node.name, exact: true }).waitFor()
    const hidden = await api.patch(`/api/v1/admin/nodes/${node.id}`, { headers, data: { ...node, hidden: true } })
    assert.equal(hidden.status(), 200, 'hide synthetic node')
    assert.equal((await api.get(`/api/v1/public/nodes/${node.id}`)).status(), 404)
    await publicPage.getByRole('button', { name: '刷新数据', exact: true }).click()
    await publicPage.getByText('节点不存在或已隐藏。', { exact: true }).waitFor()
    assert.equal(await publicPage.locator('.detail-chart canvas').count(), 0)
    await publicContext.close()
    const absolute = new URLSearchParams({ start: new Date(start + 60000).toISOString(), end: new Date(start + 360000).toISOString() })
    const target = `/admin/nodes/${node.id}?${absolute}`
    let storageState
    if (process.env.PROBE_SCREENSHOT_DIR) await fs.mkdir(process.env.PROBE_SCREENSHOT_DIR, { recursive: true })
    for (const theme of ['light', 'dark']) for (const width of [360, 768, 1440]) {
      const context = await browser.newContext({ storageState, viewport: { width, height: 1000 }, locale: 'zh-CN', timezoneId: 'Asia/Shanghai', colorScheme: theme, reducedMotion: 'reduce' })
      await context.addInitScript(theme => localStorage.setItem('myprobe-theme', theme), theme)
      const page = await context.newPage()
      const errors = []
      page.on('pageerror', error => errors.push(error.message))
      const ready = async () => { await page.locator('.detail-chart canvas').nth(2).waitFor(); await page.getByRole('heading', { name: node.name, exact: true }).waitFor() }
      await page.goto(base + target)
      if (!storageState) {
        await page.getByRole('heading', { name: '登录管理中心' }).waitFor()
        assert.equal(await page.locator('.detail-chart canvas').count(), 0)
        await page.getByLabel('用户名', { exact: true }).fill(username)
        await page.getByLabel('密码', { exact: true }).fill(password)
        await page.getByRole('button', { name: '使用密码登录', exact: true }).click()
      }
      await ready()
      await page.getByText(/此节点仅管理员可见/).waitFor()
      assert.equal(new URL(page.url()).pathname + new URL(page.url()).search, target, 'login preserves absolute window')
      storageState = await context.storageState()
      await page.reload()
      await ready()
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, 'no horizontal overflow')
      await page.getByRole('link', { name: '← 返回节点列表', exact: true }).click()
      await page.getByRole('heading', { name: '节点管理', exact: true }).waitFor()
      const card = page.locator('.admin-node-card').filter({ hasText: node.name })
      await card.getByRole('link', { name: '查看详情', exact: true }).click()
      await ready()
      assert.equal(new URL(page.url()).pathname, `/admin/nodes/${node.id}`)
      await page.goto(base + target)
      await ready()
      // Exercise the local continuation after a successful OAuth callback; this
      // does not contact GitHub or claim to test the external authorization flow.
      await page.evaluate(target => sessionStorage.setItem('myprobe-admin-detail-return', target), target)
      await page.goto(`${base}/admin?oauth=github`)
      await page.waitForURL(url => url.pathname === `/admin/nodes/${node.id}`)
      await ready()
      assert.equal(new URL(page.url()).search, `?${absolute}`)
      await page.evaluate(() => window.scrollTo(0, 0))
      if (process.env.PROBE_SCREENSHOT_DIR) await page.screenshot({ path: path.join(process.env.PROBE_SCREENSHOT_DIR, `admin-detail-${theme}-${width}.png`), fullPage: true, animations: 'disabled' })
      if (theme === 'dark' && width === 1440) {
        const me = await context.request.get(`${base}/api/v1/auth/me`)
        const sessionCSRF = (await me.json()).csrf_token
        assert.equal((await context.request.post(`${base}/api/v1/auth/logout`, { headers: { 'X-CSRF-Token': sessionCSRF } })).status(), 204)
        await page.getByRole('button', { name: '刷新数据', exact: true }).click()
        await page.getByRole('heading', { name: '登录管理中心' }).waitFor()
        assert.equal(await page.locator('.detail-chart canvas').count(), 0, 'expired session removes private charts')
        await page.getByLabel('用户名', { exact: true }).fill(username)
        await page.getByLabel('密码', { exact: true }).fill(password)
        await page.getByRole('button', { name: '使用密码登录', exact: true }).click()
        await ready()
        assert.equal(new URL(page.url()).search, `?${absolute}`, 'relogin preserves time range')
      }
      assert.deepEqual(errors, [])
      await context.close()
      console.log(`PASS live admin ${theme} ${width}: hidden node, absolute history, reload, login continuation`)
    }
  } finally {
    await browser.close()
    const deleted = await api.delete(`/api/v1/admin/nodes/${node.id}`, { headers })
    assert.equal(deleted.status(), 204, 'remove only the synthetic node created by this run')
    await api.dispose()
  }
}
main().catch(error => { console.error(error); process.exitCode = 1 })
