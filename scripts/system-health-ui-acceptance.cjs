// Run against local Vite with PLAYWRIGHT_MODULE pointing to an installed Playwright.
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright')
const assert = require('node:assert/strict')
const fs = require('node:fs/promises')
const path = require('node:path')
const os = require('node:os')
const base = process.env.UI_BASE_URL || 'http://127.0.0.1:5173'
const job = { state: 'success', completed_runs: 3, failed_runs: 0, last_success_at: '2026-10-04T00:00:00Z' }
const health = {
  observed_at: '2026-10-04T00:00:00Z', server: { version: 'acceptance-fixture', uptime_seconds: 3600, os: 'linux', arch: 'amd64' },
  database: { status: 'ok', schema_version: '013', database_file: { status: 'ok', bytes: 1048576 }, wal_file: { status: 'absent', bytes: 0 } },
  retention: { job, configuration: { status: 'available', raw_seconds: 604800, one_minute_seconds: 2592000, five_minute_seconds: 31536000, run_interval_seconds: 3600 } }, backup: { job: { ...job, state: 'never_run', completed_runs: 0, last_success_at: undefined } }, scheduler: { job },
  transport: { agent_connections: 2, pending_results: 3, expired_results: 1 }, browser_subscriptions: 1,
}
;(async () => {
  const source = await (await fetch(base + '/src/SystemHealth.vue')).text()
  const vueURL = source.match(/from "([^\"]*\/vue\.js[^\"]*)"/)[1]
  const browser = await chromium.launch({ channel: 'msedge', headless: true, args: ['--disable-features=LocalNetworkAccessChecks'] })
  const output = path.join(os.tmpdir(), 'myprobe-system-health-ui')
  await fs.mkdir(output, { recursive: true })
  try {
    for (const theme of ['light', 'dark']) for (const width of [360, 768, 1440]) {
      const page = await browser.newPage({ viewport: { width, height: 1000 } })
      let status = 200
      const errors = []
      page.on('pageerror', error => { errors.push(error.message); console.error(error.message) })
      page.on('console', message => { if (message.type() === 'error') console.error(message.text()) })
      await page.route('**/api/v1/admin/system/health', route => route.fulfill({ status, json: status === 200 ? health : { error: 'unavailable' } }))
      await page.route('**/api/v1/admin/nodes/*/diagnostics', route => route.fulfill({ json: { observed_at: health.observed_at, websocket_connected: false, node: { report_seconds: 5, agent_evidence_status: 'advertised', agent_version: 'fixture-agent', capabilities: ['metrics.v1'] } } }))
      await page.route('**/__health_acceptance', route => route.fulfill({ contentType: 'text/html', body: `<!doctype html><html lang="zh-CN" data-theme="${theme}"><meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1"><div id="app" style="padding:16px"></div><script type="module">
        import {createApp} from '${vueURL}';
        import Health from '/src/SystemHealth.vue';
        import '/src/design-system/tokens.css'; import '/src/design-system/base.css'; import '/src/style.css';
        createApp(Health,{nodes:[{id:'fixture',name:'验收节点'}],onUnauthorized:()=>document.body.dataset.unauthorized='true'}).mount('#app');
      </script></html>` }))
      await page.goto(base + '/__health_acceptance')
      await page.getByText('acceptance-fixture', { exact: true }).waitFor()
      await page.getByLabel('选择节点').selectOption('fixture')
      await page.getByText('fixture-agent', { exact: true }).waitFor()
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `overflow ${theme}/${width}`)
      await page.screenshot({ path: path.join(output, `${theme}-${width}.png`), fullPage: true })
      status = 500
      await page.getByRole('button', { name: '刷新诊断', exact: true }).click()
      await page.getByText('以下为上次读取的数据，可能已经过时。').waitFor()
      status = 200
      await page.getByRole('button', { name: '刷新诊断', exact: true }).click()
      await page.waitForFunction(() => !document.querySelector('[role="alert"]'))
      status = 401
      await page.getByRole('button', { name: '刷新诊断', exact: true }).click()
      await page.waitForFunction(() => document.body.dataset.unauthorized === 'true')
      assert.equal(await page.getByText('acceptance-fixture', { exact: true }).count(), 0)
      assert.deepEqual(errors, [])
      await page.close()
    }
    console.log(`PASS: six theme/viewport combinations, node selection, retry and 401. Screenshots: ${output}`)
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
