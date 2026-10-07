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
      await page.getByRole('button', { name: '检测统计', exact: true }).click();
      const statisticsPanel = page.getByRole('region', { name: '服务检测统计' });
      async function queryStatistics() {
        const response = page.waitForResponse(r => new URL(r.url()).pathname.endsWith('/statistics'));
        await statisticsPanel.getByRole('button', { name: '查询统计', exact: true }).click();
        const result = await response;
        assert.equal(result.status(), 200);
        const body = await result.json();
        await statisticsPanel.getByText('尚未排除维护窗口', { exact: false }).waitFor();
        return body;
      }
      const immature = await queryStatistics();
      assert.equal(immature.statistics.expected, 0, 'fresh slots must wait for the grace window');
      assert.equal(immature.statistics.success_rate, null);
      assert.equal(immature.statistics.coverage, null);
      await statisticsPanel.getByText('无有效样本', { exact: true }).waitFor();
      await statisticsPanel.getByText('无计划样本', { exact: true }).waitFor();
      if (process.env.MYPROBE_TEST_HTTP_MATURE === '1' && theme === 'light' && width === 360) {
        const deadline = Date.now() + 145000;
        let mature = immature;
        while (mature.statistics.expected === 0 && Date.now() < deadline) {
          await page.waitForTimeout(2000);
          mature = await queryStatistics();
        }
        const stats = mature.statistics;
        assert.ok(stats.expected > 0, 'real clock must reach a mature slot');
        // Validate counts against actual retained observations, not fixed fixtures.
        const services = await (await context.request.get(`${base}/api/v1/admin/service-monitors`)).json();
        const observations = await (await context.request.get(`${base}/api/v1/admin/service-monitors/${services.services[0].id}/results`)).json();
        const included = observations.observations.filter(item => Date.parse(item.scheduled_at) >= Date.parse(stats.start) && Date.parse(item.scheduled_at) < Date.parse(stats.end));
        assert.equal(included.length, stats.expected);
        assert.equal(included.filter(item => item.result?.outcome === 'success').length, stats.success);
        assert.equal(stats.success, stats.expected);
        assert.equal(stats.failure, 0);
        assert.equal(stats.missing, 0);
        assert.equal(stats.success_rate, 1);
        assert.equal(stats.coverage, 1);
        assert.equal(mature.maintenance_excluded, false);
        assert.equal(await statisticsPanel.getByText('100.00%', { exact: true }).count(), 2);
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false);
        await page.screenshot({ path: path.join(output, 'live-mature-statistics.png'), fullPage: true });
        console.log('Real-clock mature statistics match retained observations and rendered success/coverage rates.');
      }
      await statisticsPanel.getByRole('button', { name: '关闭统计', exact: true }).click();
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
