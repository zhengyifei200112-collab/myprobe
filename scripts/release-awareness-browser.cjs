const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict'), fs = require('node:fs/promises'), path = require('node:path');
(async () => {
  const output = path.join(process.env.QA_ARTIFACT_DIR || require('node:os').tmpdir(), 'release-awareness');
  await fs.mkdir(output, { recursive: true });
  const browser = await chromium.launch({ headless: true, ...(process.env.PLAYWRIGHT_CHANNEL ? { channel: process.env.PLAYWRIGHT_CHANNEL } : {}), args: process.platform === 'linux' ? ['--no-sandbox'] : [] });
  const base = process.env.UI_BASE_URL;
  const evidence = { format: 'myprobe.release-awareness-browser.v1', source_commit: process.env.MYPROBE_TEST_SOURCE_COMMIT || 'working-tree', browser: browser.version(), backend: 'actual Cookie/CSRF/Gin/Store/checker/embedded assets; owned upstream transport', views: [], synthetic_fault_checks: [] };
  let active;
  try {
    for (const theme of ['light', 'dark']) for (const width of [360, 768, 1440]) {
      const context = await browser.newContext({ viewport: { width, height: 1100 }, colorScheme: theme });
      await context.addInitScript(t => localStorage.setItem('myprobe-theme', t), theme);
      const login = await context.request.post(base + '/api/v1/auth/login', { data: { username: 'admin', password: 'correct horse battery staple' } });
      assert.equal(login.status(), 200);
      const page = await context.newPage(); active = page;
      const errors = []; page.on('pageerror', e => errors.push(e.message));
      await page.goto(base + '/admin?tab=health');
      const panel = page.locator('.release-awareness');
      await panel.getByRole('heading', { name: '版本与升级' }).waitFor();
      const check = panel.getByRole('button', { name: '检查官方稳定版', exact: true });
      if (!evidence.views.length) {
        await panel.getByText('尚未检查', { exact: true }).waitFor();
        await check.focus();
        const checked = page.waitForResponse(r => r.url().endsWith('/api/v1/admin/system/releases/check') && r.request().method() === 'POST');
        await page.keyboard.press('Enter');
        const response = await checked;
        assert.equal(response.status(), 200);
        assert.ok(response.request().headers()['x-csrf-token']);
      }
      await panel.getByText('v1.2.3 · 发布说明', { exact: true }).waitFor();
      const reread = page.waitForResponse(r => new URL(r.url()).pathname === '/api/v1/admin/system/releases' && r.request().method() === 'GET');
      await panel.getByRole('button', { name: '刷新版本状态', exact: true }).click();
      assert.equal((await reread).status(), 200);
      await panel.getByText('v1.2.3 · 发布说明', { exact: true }).waitFor();
      assert.ok(await check.isDisabled());
      assert.match(await panel.innerText(), /v1.2.1 · 有新稳定版可用/);
      assert.match(await panel.innerText(), /已关闭，可手动检查/);
      const health = await (await context.request.get(base + '/api/v1/admin/system/health')).json();
      if (health.server.os === 'linux') assert.match(await panel.innerText(), /sudo \.\/install.sh update server --version v1.2.3/);
      else assert.equal(await panel.locator('pre').count(), 0);
      assert.equal(await panel.locator('a[href="https://github.com/zhengyifei200112-collab/myprobe/releases/tag/v1.2.3"]').count(), 1);
      await page.getByLabel('选择节点', { exact: true }).selectOption(process.env.QA_RELEASE_NODE);
      await panel.getByText('v1.2.0 · 有新稳定版可用', { exact: true }).waitFor();
      assert.match(await panel.innerText(), /Agent 版本来自最近一次握手/);
      await page.getByLabel('选择节点', { exact: true }).selectOption(process.env.QA_RELEASE_UNKNOWN_NODE);
      await panel.getByText('未提供可比较的版本号 · 当前版本无法比较', { exact: true }).waitFor();
      assert.equal(await panel.getByText('v1.2.0 · 有新稳定版可用', { exact: true }).count(), 0);
      await page.getByLabel('选择节点', { exact: true }).selectOption(process.env.QA_RELEASE_NODE);
      await panel.getByText('v1.2.0 · 有新稳定版可用', { exact: true }).waitFor();
      const bounds = await panel.boundingBox(); assert.ok(bounds && bounds.x >= 0 && bounds.x + bounds.width <= width + 1);
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1));
      await panel.screenshot({ path: path.join(output, `${theme}-${width}.png`) });
      assert.deepEqual(errors, []);
      evidence.views.push({ theme, width, actual_csrf_check: evidence.views.length === 0, shared_cached_result: true, cache_reread_during_cooldown: true, node_selection: true, unknown_version: true, cooldown: true, overflow: false, page_errors: 0 });
      if (theme === 'light' && width === 360) {
        // Explicit response faults for display boundaries; these do not replace
        // the actual upstream/cache/auth contract tests in Go.
        let fault = 'expired';
        await page.route('**/api/v1/admin/system/releases?*', async route => {
          if (fault === 'hung') return; // Keep this owned response pending until the client aborts.
          if (fault === 'failed') { await route.fulfill({ status: 503, json: { error: 'synthetic' } }); return; }
          if (fault === 'unauthorized') { await route.fulfill({ status: 401, json: { error: 'synthetic' } }); return; }
          const response = await route.fetch(); const body = await response.json();
          body.upstream.last_success_at = new Date(Date.now() - 49 * 3600000).toISOString();
          body.server.status = 'up_to_date';
          await route.fulfill({ response, json: body });
        });
        await page.getByLabel('选择节点', { exact: true }).selectOption(process.env.QA_RELEASE_UNKNOWN_NODE);
        await panel.getByText(/缓存已过期或最近检查失败/).waitFor();
        assert.equal(await panel.getByText(/与最新稳定版一致/).count(), 0);
        assert.match(await panel.innerText(), /暂时无法判断是否需要更新/);
        fault = 'failed';
        await page.getByLabel('选择节点', { exact: true }).selectOption(process.env.QA_RELEASE_NODE);
        await panel.getByText(/版本信息读取失败/).waitFor();
        fault = 'expired';
        await panel.getByRole('button', { name: '重试读取' }).click();
        await panel.getByText(/缓存已过期或最近检查失败/).waitFor();
        fault = 'hung';
        await page.clock.install();
        const pending = page.waitForRequest(request => request.url().includes('/api/v1/admin/system/releases?'));
        await panel.getByRole('button', { name: '刷新版本状态', exact: true }).click();
        await pending;
        await page.clock.fastForward(12001);
        await panel.getByText(/版本信息读取失败/).waitFor();
        assert.equal(await panel.getByRole('button', { name: '刷新版本状态', exact: true }).isEnabled(), true);
        fault = 'expired';
        await panel.getByRole('button', { name: '重试读取' }).click();
        await panel.getByText(/缓存已过期或最近检查失败/).waitFor();
        fault = 'unauthorized';
        await page.getByLabel('选择节点', { exact: true }).selectOption(process.env.QA_RELEASE_UNKNOWN_NODE);
        await page.getByRole('button', { name: '使用密码登录' }).waitFor();
        assert.equal(await page.locator('.release-awareness').count(), 0);
        evidence.synthetic_fault_checks = ['expired cache hides up-to-date', '503 clears data', 'retry', '12-second request abort reenables retry', '401 clears private panel'];
      }
      await context.close(); active = undefined;
    }
    await fs.writeFile(path.join(output, 'result.json'), JSON.stringify(evidence, null, 2) + '\n');
    console.log(JSON.stringify(evidence));
  } catch (error) { if (active) await active.screenshot({ path: path.join(output, 'failure.png'), fullPage: true }).catch(() => {}); throw error; }
  finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
