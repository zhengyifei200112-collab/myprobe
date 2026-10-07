// Invoked by TestHTTPBrowserLive with a temporary database and real Agent.
// No API interception: credentials and private fixture URL stay in process memory.
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
(async () => {
  const base = process.env.UI_BASE_URL;
  assert.ok(base && process.env.HTTP_FIXTURE_TARGET && process.env.HTTPS_FIXTURE_TARGET && process.env.HTTP_FIXTURE_PASSWORD);
  const output = await fs.mkdtemp(path.join(require('node:os').tmpdir(), 'myprobe-http-live-'));
  const browser = await chromium.launch({ channel: 'msedge', headless: true });
  try {
    for (const theme of ['light', 'dark']) for (const width of [360, 768, 1440]) {
      const context = await browser.newContext({ viewport: { width, height: 1000 }, colorScheme: theme });
      const login = await context.request.post(`${base}/api/v1/auth/login`, {
        data: { username: 'fixture-admin', password: process.env.HTTP_FIXTURE_PASSWORD }
      });
      assert.equal(login.status(), 200, 'fixture login');
      const page = await context.newPage();
      const errors = [];
      page.on('pageerror', error => errors.push(error.message));
      await page.goto(`${base}/admin`);
      await page.getByRole('button', { name: 'HTTP 服务', exact: true }).click();
      await page.getByRole('button', { name: '新建服务', exact: true }).click();
      await page.getByLabel('服务名称', { exact: true }).fill('live HTTP fixture');
      await page.getByLabel('目标 URL', { exact: true }).fill(`${process.env.HTTP_FIXTURE_TARGET}/`);
      await page.getByLabel('fixture observer', { exact: true }).check();
      await page.getByRole('combobox', { name: /^内容断言/ }).selectOption('text_contains');
      await page.getByLabel('必须包含的文本', { exact: true }).fill('healthy');
      await page.getByRole('button', { name: '保存服务', exact: true }).click();
      await page.getByRole('button', { name: '最近结果', exact: true }).click();
      const panel = page.getByRole('region', { name: '最近检查结果' });
      async function waitForOutcome(label, exact = true) {
        const deadline = Date.now() + 15000;
        while (Date.now() < deadline) {
          if (await panel.getByText(label, { exact }).count()) return;
          await panel.getByRole('button', { name: '刷新结果', exact: true }).click();
          await page.waitForTimeout(250);
        }
        assert.fail(`result did not reach ${label}`);
      }
      await waitForOutcome('检查成功');
      await page.reload();
      await page.getByRole('button', { name: 'HTTP 服务', exact: true }).click();
      await page.getByRole('button', { name: '最近结果', exact: true }).click();
      await panel.getByText('检查成功', { exact: true }).waitFor();
      await page.evaluate(theme => { document.documentElement.dataset.theme = theme; }, theme);
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false);
      await page.screenshot({ path: path.join(output, `${theme}-${width}-success.png`), fullPage: true });
      await panel.getByRole('button', { name: '关闭结果', exact: true }).click();
      await page.getByRole('button', { name: '编辑服务', exact: true }).click();
      await page.getByLabel('目标 URL', { exact: true }).fill(`${process.env.HTTP_FIXTURE_TARGET}/failure`);
      await page.getByRole('button', { name: '保存服务', exact: true }).click();
      await page.getByRole('button', { name: '最近结果', exact: true }).click();
      await waitForOutcome('检查失败');
      await panel.getByText('状态码不符合要求', { exact: false }).waitFor();
      await page.screenshot({ path: path.join(output, `${theme}-${width}-failure.png`), fullPage: true });
      await panel.getByRole('button', { name: '关闭结果', exact: true }).click();
      await page.getByRole('button', { name: '编辑服务', exact: true }).click();
      await page.getByLabel('目标 URL', { exact: true }).fill(`${process.env.HTTPS_FIXTURE_TARGET}/`);
      await page.getByRole('button', { name: '保存服务', exact: true }).click();
      await page.getByRole('button', { name: '最近结果', exact: true }).click();
      await waitForOutcome('TLS 验证失败', false);
      assert.equal(await panel.locator('summary').count(), 0, 'untrusted TLS must not display verified certificates');
      await page.screenshot({ path: path.join(output, `${theme}-${width}-tls-failure.png`), fullPage: true });
      await page.getByRole('button', { name: '删除服务', exact: true }).click();
      await page.getByRole('button', { name: '删除服务和历史', exact: true }).click();
      await page.getByText('暂无 HTTP 服务。', { exact: false }).waitFor();
      assert.deepEqual(errors, []);
      await context.close();
    }
    console.log(`Live HTTP create/execute/reload/edit/delete and HTTPS rejection passed six viewport/theme combinations. Screenshots: ${output}`);
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
