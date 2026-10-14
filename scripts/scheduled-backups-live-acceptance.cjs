// Disposable local Server only. No production database or real credentials.
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright')
const assert = require('node:assert/strict')
const fs = require('node:fs/promises')
const path = require('node:path')
const os = require('node:os')
const base = process.env.BACKUP_TEST_URL
const directory = process.env.BACKUP_TEST_DIRECTORY
const passFile = process.env.BACKUP_TEST_PASSPHRASE_FILE
if (!base || !directory || !passFile || !['127.0.0.1', 'localhost'].includes(new URL(base).hostname)) throw new Error('Disposable local fixture required')
;(async () => {
  const browser = await chromium.launch({ channel: 'msedge', headless: true })
  const context = await browser.newContext()
  try {
    const page = await context.newPage()
    const errors = []
    page.on('pageerror', error => errors.push(error.message))
    await page.goto(base + '/admin?tab=maintenance')
    await page.getByLabel('密码', { exact: true }).fill('backup-local-test-password')
    const response = page.waitForResponse(r => r.url().endsWith('/api/v1/auth/login'))
    await page.getByRole('button', { name: '使用密码登录' }).click()
    const csrf = (await (await response).json()).csrf_token
    assert.ok(csrf)
    const channel = await context.request.post(base + '/api/v1/admin/notification-channels', { headers: { 'X-CSRF-Token': csrf }, data: { name: '本地备份验收渠道', kind: 'webhook', config: { url: 'http://127.0.0.1:9/blocked-by-production-sender' } } })
    assert.equal(channel.status(), 201)
    await page.reload()
    const card = page.locator('.scheduled-backups')
    await card.getByRole('heading', { name: '定时备份与恢复演练' }).waitFor()
    await card.getByText('工作线程：运行中', { exact: false }).waitFor()
    await card.getByLabel('启用每日备份').check()
    await card.getByLabel('服务端备份目录').fill(directory)
    await card.getByLabel('每日时间').fill('23:59')
    await card.getByLabel('时区', { exact: true }).fill('UTC')
    await card.getByLabel('保留成功备份份数').fill('1')
    await card.getByLabel('本地备份验收渠道 · webhook').check()
    await card.getByRole('button', { name: '保存备份设置' }).click()
    await card.getByText('设置已保存。', { exact: true }).waitFor()
    const endpoint = base + '/api/v1/admin/maintenance/backup-schedule'
    const getStatus = async () => { const result = await context.request.get(endpoint); assert.equal(result.status(), 200); return result.json() }
    const waitStatus = async condition => {
      const deadline = Date.now() + 30000
      while (Date.now() < deadline) { const value = await getStatus(); if (condition(value)) return value; await new Promise(resolve => setTimeout(resolve, 100)) }
      throw new Error('Background task did not reach expected state')
    }
    await card.getByRole('button', { name: '立即执行一次' }).click()
    let state = await waitStatus(s => !s.runtime.operation && s.runs[0]?.status === 'completed')
    assert.equal(state.runs[0].artifact.recovery_verified, false)
    assert.ok(state.runs[0].artifact.sha256.match(/^[a-f0-9]{64}$/))
    await card.getByRole('button', { name: '刷新备份状态' }).click()
    await card.getByText('备份完成，尚未验证可恢复', { exact: true }).waitFor()
    await card.getByRole('button', { name: '执行隔离恢复演练' }).click()
    state = await waitStatus(s => !s.runtime.operation && s.runs[0]?.artifact?.recovery_verified)
    assert.equal(state.runs[0].artifact.recovery_evidence.channels, 1)
    assert.equal(state.runs[0].artifact.recovery_evidence.users, 1)
    await page.reload()
    await card.getByText('已验证可恢复（隔离数据库读取与通知凭据解密）', { exact: false }).waitFor()
    assert.equal(new URL(page.url()).searchParams.get('tab'), 'maintenance')
    await card.getByRole('button', { name: '立即执行一次' }).click()
    state = await waitStatus(s => !s.runtime.operation && s.runs.filter(r => r.status === 'completed').length === 2)
    const artifacts = (await fs.readdir(directory)).filter(name => name.endsWith('.mpb'))
    assert.equal(artifacts.length, 1)
    assert.equal(artifacts[0], state.runs[0].artifact.file_name)
    await fs.writeFile(passFile, 'short')
    await card.getByRole('button', { name: '刷新备份状态' }).click()
    await card.getByRole('button', { name: '立即执行一次' }).click()
    state = await waitStatus(s => !s.runtime.operation && s.runs[0]?.status === 'failed' && s.notifications.length === 1)
    assert.equal(state.runs[0].error_code, 'backup_passphrase_unavailable')
    assert.equal((await fs.readdir(directory)).filter(name => name.endsWith('.mpb')).length, 1)
    // Production Sender rejects loopback before network I/O. Actual delivery to
    // a local receiver is separately tested by the Go runtime acceptance test.
    await fs.writeFile(passFile, 'synthetic-backup-passphrase\n')
    await card.getByRole('button', { name: '刷新备份状态' }).click()
    await card.getByRole('heading', { name: '备份失败提醒', exact: true }).waitFor()
    const output = path.join(os.tmpdir(), 'myprobe-backups-live-ui')
    await fs.mkdir(output, { recursive: true })
    for (const theme of ['light', 'dark']) for (const width of [360, 768, 1440]) {
      await page.setViewportSize({ width, height: 1000 })
      await page.evaluate(theme => { document.documentElement.dataset.theme = theme }, theme)
      await card.getByRole('button', { name: '刷新备份状态' }).focus()
      await page.keyboard.press('Enter')
      await card.getByRole('heading', { name: '最近备份任务', exact: true }).waitFor()
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `${theme}/${width} overflow`)
      await card.scrollIntoViewIfNeeded()
      await page.screenshot({ path: path.join(output, `${theme}-${width}.png`), fullPage: true })
      await card.getByRole('heading', { name: '最近备份任务', exact: true }).scrollIntoViewIfNeeded()
      await page.screenshot({ path: path.join(output, `${theme}-${width}-tasks.png`) })
    }
    await page.getByRole('button', { name: '节点', exact: true }).click()
    await page.goBack()
    await card.getByRole('heading', { name: '定时备份与恢复演练' }).waitFor()
    await context.request.post(base + '/api/v1/auth/logout', { headers: { 'X-CSRF-Token': csrf } })
    await card.getByRole('button', { name: '刷新备份状态' }).click()
    await page.getByRole('heading', { name: '登录管理中心', exact: true }).waitFor()
    assert.equal(await page.getByText(directory, { exact: true }).count(), 0)
    assert.deepEqual(errors, [])
    console.log(`PASS: actual main Server, authenticated settings, asynchronous backup, isolated recovery, retention, failed task, durable reminder, reload/Back, six viewports/themes and session revocation. Screenshots: ${output}`)
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
