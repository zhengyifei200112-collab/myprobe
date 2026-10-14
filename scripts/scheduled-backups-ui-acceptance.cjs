// Controlled UI boundary fixtures; these are not backup throughput evidence.
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright')
const assert = require('node:assert/strict')
const fs = require('node:fs/promises')
const path = require('node:path')
const os = require('node:os')
const base = process.env.UI_BASE_URL || 'http://127.0.0.1:5175'
;(async () => {
  const source = await (await fetch(base + '/src/ScheduledBackups.vue')).text()
  const vueURL = source.match(/from "([^\"]*\/vue\.js[^\"]*)"/)[1]
  const browser = await chromium.launch({ channel: 'msedge', headless: true, args: ['--disable-features=LocalNetworkAccessChecks'] })
  const output = path.join(os.tmpdir(), 'myprobe-backups-ui')
  await fs.mkdir(output, { recursive: true })
  try {
    for (const theme of ['light', 'dark']) for (const width of [360, 768, 1440]) {
      const page = await browser.newPage({ viewport: { width, height: 1000 } })
      const errors = []
      page.on('pageerror', error => errors.push(error.message))
      const channels = Array.from({ length: 1000 }, (_, i) => ({ id: `synthetic-${i}`, name: `渠道 ${String(i).padStart(4, '0')}`, kind: 'webhook', enabled: true }))
      const fixture = { config: { enabled: true, directory: '/synthetic/archives', daily_time: '03:00', timezone: 'UTC', keep: 7, failure_channel_ids: ['synthetic-0', 'missing-channel'] }, passphrase_ready: true, runtime: { running: true, available: true }, runs: Array.from({ length: 50 }, (_, i) => ({ id: `run-${i}`, status: 'failed', trigger: 'daily', started_at: '2026-10-14T00:00:00Z', error_code: 'backup_insufficient_space', artifact: null })), notifications: Array.from({ length: 50 }, (_, i) => ({ id: `notice-${i}`, channel_name: `提醒 ${i}`, provider: 'webhook', status: 'pending', attempt_count: 1, occurred_at: '2026-10-14T00:00:00Z', available_at: '2026-10-14T00:01:00Z', error_code: 'backup_insufficient_space' })) }
      let statusCode = 200, channelStatus = 200, submitted
      await page.route('**/api/v1/admin/maintenance/backup-schedule', route => {
        if (route.request().method() === 'PUT') { submitted = route.request().postDataJSON(); return route.fulfill({ json: { saved: true } }) }
        return route.fulfill({ status: statusCode, json: statusCode === 200 ? fixture : { error: 'backup_status_unavailable' } })
      })
      await page.route('**/api/v1/admin/notification-channels', route => route.fulfill({ status: channelStatus, json: channelStatus === 200 ? { channels } : { error: 'authentication_required' } }))
      await page.route('**/__backup_acceptance', route => route.fulfill({ contentType: 'text/html', body: `<!doctype html><html lang="zh-CN" data-theme="${theme}"><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"><div id="app" style="padding:16px"></div><script type="module">import {createApp} from '${vueURL}';import Backup from '/src/ScheduledBackups.vue';import '/src/design-system/tokens.css';import '/src/design-system/base.css';import '/src/style.css';createApp(Backup,{onUnauthorized:()=>document.body.dataset.unauthorized='true'}).mount('#app');</script></html>` }))
      await page.goto(base + '/__backup_acceptance')
      await page.getByLabel('渠道 0000 · webhook').waitFor()
      assert.equal(await page.locator('.channel-list input').count(), 25)
      assert.equal(await page.locator('.backup-row').count(), 20)
      await page.getByRole('button', { name: '移除不可用渠道' }).click()
      await page.getByRole('button', { name: '下一页渠道' }).click()
      await page.getByLabel('渠道 0025 · webhook').check()
      await page.getByLabel('搜索渠道', { exact: true }).fill('0999')
      await page.getByLabel('渠道 0999 · webhook').check()
      await page.getByLabel('服务端备份目录').fill('/synthetic/unsaved-edit')
      await page.getByRole('button', { name: '刷新备份状态' }).click()
      await page.waitForFunction(() => !document.querySelector('button.soft-button')?.disabled)
      assert.equal(await page.getByLabel('服务端备份目录').inputValue(), '/synthetic/unsaved-edit')
      await page.getByRole('button', { name: '保存备份设置' }).click()
      await page.getByText('设置已保存。', { exact: true }).waitFor()
      assert.deepEqual(submitted.failure_channel_ids.sort(), ['synthetic-0', 'synthetic-25', 'synthetic-999'].sort())
      assert.equal(submitted.directory, '/synthetic/unsaved-edit')
      assert.equal('passphrase' in submitted, false)
      await page.getByRole('button', { name: '下一页任务' }).click()
      await page.getByRole('button', { name: '下一页提醒' }).click()
      assert.equal(await page.locator('.backup-row').count(), 20)
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `${theme}/${width} overflow`)
      await page.screenshot({ path: path.join(output, `${theme}-${width}.png`), fullPage: true })
      statusCode = 503
      await page.getByRole('button', { name: '刷新备份状态' }).click()
      await page.getByText('当前显示的是上次读取结果，状态可能已经变化。刷新成功后再操作。', { exact: true }).waitFor()
      assert.equal(await page.getByRole('button', { name: '保存备份设置' }).isDisabled(), true)
      statusCode = 200
      await page.getByRole('button', { name: '刷新备份状态' }).click()
      await page.waitForFunction(() => !document.querySelector('[role="alert"]'))
      statusCode = width === 768 ? 403 : 401
      if (width === 1440) { statusCode = 503; channelStatus = 401 }
      await page.getByRole('button', { name: '刷新备份状态' }).click()
      await page.waitForFunction(() => document.body.dataset.unauthorized === 'true')
      assert.equal(await page.locator('.backup-fields').count(), 0)
      assert.equal(await page.locator('.backup-row').count(), 0)
      assert.deepEqual(errors, [])
      await page.close()
    }
    console.log(`PASS: bounded 1000-channel picker, cross-page selection, missing-channel removal, 50-record pagination, unsaved edits, 503/retry, 401/403 clearing and six theme/viewport combinations. Screenshots: ${output}`)
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
