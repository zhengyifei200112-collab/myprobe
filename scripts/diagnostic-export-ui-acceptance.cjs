// Controlled export error/expiry UI contracts against a local Vite fixture.
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright')
const assert = require('node:assert/strict')
const fs = require('node:fs/promises')
const path = require('node:path')
const os = require('node:os')
const crypto = require('node:crypto')
const base = process.env.UI_BASE_URL || 'http://127.0.0.1:5176'
;(async () => {
  const source = await (await fetch(base + '/src/DiagnosticExport.vue')).text()
  const vueURL = source.match(/from "([^\"]*\/vue\.js[^\"]*)"/)[1]
  const browser = await chromium.launch({ channel: 'msedge', headless: true, args: ['--disable-features=LocalNetworkAccessChecks'] })
  const output = path.join(os.tmpdir(), 'myprobe-diagnostic-export-ui')
  await fs.mkdir(output, { recursive: true })
  try {
    for (const theme of ['light', 'dark']) for (const width of [360, 768, 1440]) {
      const page = await browser.newPage({ viewport: { width, height: 1000 }, acceptDownloads: true })
      const errors = []
      page.on('pageerror', error => errors.push(error.message))
      const bundle = { format: 'myprobe.diagnostics.v1', generated_at: new Date().toISOString(), observation_scope: 'separate_process_and_database_observations', included: ['normalized_versions', 'configuration_structure', 'configuration_counts', 'active_queue_counts'], excluded: ['credentials_and_tokens', 'database_contents', 'addresses_and_target_urls', 'names_notes_and_hostnames'], runtime: { version: { base: '1.2.3', status: 'release' } }, configuration_counts: { status: 'available', counts: { nodes: 50 } } }
      const body = JSON.stringify(bundle, null, 2) + '\n'
      const metadata = { preview_id: 'a'.repeat(32), file_name: `myprobe-diagnostics-20261015T000000Z-${'a'.repeat(32)}.json`, bytes: Buffer.byteLength(body), sha256: crypto.createHash('sha256').update(body).digest('hex') }
      let previewCode = 200, downloadCode = 200, expired = false, mismatch = false
      await page.route('**/api/v1/auth/me', route => route.fulfill({ json: { csrf_token: 'synthetic-csrf' } }))
      await page.route('**/api/v1/admin/system/diagnostic-exports/preview', route => {
        assert.equal(route.request().headers()['x-csrf-token'], 'synthetic-csrf')
        return route.fulfill({ status: previewCode, json: previewCode === 200 ? { preview: { ...metadata, expires_at: new Date(Date.now() + (expired ? -1000 : 300000)).toISOString() }, bundle } : { error: 'diagnostic_collection_unavailable' } })
      })
      await page.route('**/api/v1/admin/system/diagnostic-exports/download', route => {
        assert.equal(route.request().headers()['x-csrf-token'], 'synthetic-csrf')
        assert.deepEqual(route.request().postDataJSON(), { preview_id: metadata.preview_id })
        if (downloadCode !== 200) return route.fulfill({ status: downloadCode, json: { error: downloadCode === 503 ? 'diagnostic_audit_unavailable' : 'diagnostic_preview_missing' } })
        return route.fulfill({ contentType: 'application/json', headers: { 'X-Content-SHA256': mismatch ? 'f'.repeat(64) : metadata.sha256 }, body })
      })
      await page.route('**/__diagnostic_acceptance', route => route.fulfill({ contentType: 'text/html', body: `<!doctype html><html lang="zh-CN" data-theme="${theme}"><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"><div id="app" style="padding:16px"></div><script type="module">import{createApp}from'${vueURL}';import Diagnostic from'/src/DiagnosticExport.vue';import{restoreSession}from'/src/admin-api.ts';import'/src/design-system/tokens.css';import'/src/design-system/base.css';import'/src/style.css';await restoreSession();createApp(Diagnostic,{onUnauthorized:()=>document.body.dataset.unauthorized='true'}).mount('#app');</script></html>` }))
      await page.goto(base + '/__diagnostic_acceptance')
      const prepare = () => page.getByRole('button', { name: /生成.*预览/ }).click()
      await prepare()
      await page.getByRole('button', { name: '下载已预览的 JSON' }).waitFor()
      await page.getByText('查看本次文件内容', { exact: true }).click()
      assert.equal(await page.locator('pre').isVisible(), true)
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `${theme}/${width} overflow`)
      await page.screenshot({ path: path.join(output, `${theme}-${width}.png`) })
      downloadCode = 503
      await page.getByRole('button', { name: '下载已预览的 JSON' }).click()
      await page.getByText('审计记录暂时无法保存，文件未释放，请稍后重试。', { exact: true }).waitFor()
      downloadCode = 200; mismatch = true
      await page.getByRole('button', { name: '下载已预览的 JSON' }).click()
      await page.getByText('收到的文件与预览的大小或校验和不一致，请重新生成。', { exact: true }).waitFor()
      mismatch = false
      const downloaded = page.waitForEvent('download')
      await page.getByRole('button', { name: '下载已预览的 JSON' }).click()
      assert.equal((await downloaded).suggestedFilename(), metadata.file_name)
      expired = true; await prepare()
      await page.getByText('预览已过期，请重新生成后下载。', { exact: true }).waitFor()
      assert.equal(await page.getByRole('button', { name: '下载已预览的 JSON' }).isDisabled(), true)
      expired = false; previewCode = 503; await prepare()
      await page.getByText('诊断信息采集超时或不可用，请重试。', { exact: true }).waitFor()
      assert.equal(await page.getByRole('button', { name: '下载已预览的 JSON' }).count(), 0)
      previewCode = 200; await prepare()
      await page.getByRole('button', { name: '下载已预览的 JSON' }).waitFor()
      downloadCode = width === 768 ? 403 : 401
      await page.getByRole('button', { name: '下载已预览的 JSON' }).click()
      await page.waitForFunction(() => document.body.dataset.unauthorized === 'true')
      assert.equal(await page.locator('pre').count(), 0)
      assert.equal(await page.getByRole('button', { name: '下载已预览的 JSON' }).count(), 0)
      assert.deepEqual(errors, [])
      await page.close()
    }
    console.log(`PASS: scoped preview, CSRF, audit failure, download mismatch, expiry, retry, 401/403 clearing and six themes/viewports. Screenshots: ${output}`)
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
