// Disposable loopback Server only. Checks explicit routes against stale login state.
const assert = require('node:assert/strict')
const fs = require('node:fs/promises'), path = require('node:path')
const { chromium, request } = require(process.env.PLAYWRIGHT_MODULE || 'playwright')
const base = process.env.PROBE_TEST_URL, password = process.env.PROBE_TEST_PASSWORD
assert(base && password && ['127.0.0.1', 'localhost', '[::1]'].includes(new URL(base).hostname))
;(async () => {
  const api = await request.newContext({ baseURL: base })
  const login = await api.post('/api/v1/auth/login', { data: { username: 'admin', password } })
  assert.equal(login.status(), 200)
  const csrf = (await login.json()).csrf_token, headers = { 'X-CSRF-Token': csrf }
  const created = await api.post('/api/v1/admin/nodes', { headers, data: { name: 'Navigation fixture' } })
  assert.equal(created.status(), 201)
  const { node } = await created.json(), browser = await chromium.launch({ channel: 'msedge', headless: true })
  try {
    for (const kind of ['incident', 'node', 'service', 'absolute-node']) {
      const context = await browser.newContext()
      try {
        await context.addInitScript(({ id }) => {
          sessionStorage.setItem('myprobe-admin-detail-return', `/admin/nodes/${id}?range=7d`)
          sessionStorage.setItem('myprobe-notification-navigation-v1', JSON.stringify({ path: '/admin?incident=missing-fixture', savedAt: Date.now() }))
        }, { id: node.id })
        const page = await context.newPage(), errors = []
        page.on('pageerror', error => errors.push(error.message))
        const end = new Date(Date.now() - 1000).toISOString()
        const start = new Date(Date.now() - 3600000).toISOString()
        const bounds = new URLSearchParams({ start, end }).toString()
        const destination = kind === 'incident' ? '/admin?incident=missing-fixture'
          : kind === 'service' ? `/admin?service=missing-service&observer=${node.id}&${bounds}`
          : kind === 'absolute-node' ? `/admin/nodes/${node.id}?${bounds}`
          : `/admin/nodes/${node.id}?range=12h`
        await page.goto(base + destination)
        await page.getByRole('heading', { name: '登录管理中心' }).waitFor()
        await page.getByLabel('用户名', { exact: true }).fill('admin')
        await page.getByLabel('密码', { exact: true }).fill(password)
        await page.getByRole('button', { name: '使用密码登录', exact: true }).click()
        if (kind === 'incident') {
          await page.getByRole('tab', { name: '故障事件', exact: true }).waitFor()
          assert.equal(new URL(page.url()).pathname, '/admin')
          assert.equal(new URL(page.url()).searchParams.get('incident'), 'missing-fixture')
          assert.equal(await page.getByRole('tab', { name: '故障事件', exact: true }).getAttribute('aria-selected'), 'true')
        } else if (kind === 'service') {
          await page.getByRole('heading', { name: 'HTTP 服务', exact: true }).waitFor()
          assert.equal(new URL(page.url()).pathname, '/admin')
          assert.equal(new URL(page.url()).searchParams.get('service'), 'missing-service')
          assert.equal(new URL(page.url()).searchParams.get('observer'), node.id)
        } else {
          await page.getByRole('heading', { name: 'Navigation fixture', exact: true }).waitFor()
          assert.equal(new URL(page.url()).pathname, `/admin/nodes/${node.id}`)
          if (kind === 'node') assert.equal(new URL(page.url()).searchParams.get('range'), '12h')
          else {
            assert.equal(new URL(page.url()).searchParams.get('start'), start)
            assert.equal(new URL(page.url()).searchParams.get('end'), end)
          }
        }
        assert.deepEqual(errors, [])
        if (process.env.PROBE_SCREENSHOT_DIR) {
          await fs.mkdir(process.env.PROBE_SCREENSHOT_DIR, { recursive: true })
          await page.screenshot({ path: path.join(process.env.PROBE_SCREENSHOT_DIR, `navigation-${kind}.png`), fullPage: true, animations: 'disabled' })
        }
        console.log(`PASS explicit ${kind} destination overrides stale login destinations`)
      } finally { await context.close() }
    }
  } finally {
    await browser.close()
    assert.equal((await api.delete(`/api/v1/admin/nodes/${node.id}`, { headers })).status(), 204)
    await api.dispose()
  }
})().catch(error => { console.error(error); process.exitCode = 1 })
