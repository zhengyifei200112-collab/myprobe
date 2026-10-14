// Run against a local Vite preview with PLAYWRIGHT_MODULE and UI_BASE_URL set.
// All API requests are intercepted; this never imports into a real database.
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const fs = require('node:fs/promises');
const path = require('node:path');
const assert = require('node:assert/strict');
(async () => {
  const output = process.env.UI_OUTPUT || path.join(require('node:os').tmpdir(), 'myprobe-config-import-ui');
  await fs.mkdir(output, { recursive: true });
  const browser = await chromium.launch({ channel: 'msedge', headless: true });
  try {
    for (const theme of ['light', 'dark']) for (const width of [360, 768, 1440]) {
      const page = await browser.newPage({ viewport: { width, height: 1000 }, colorScheme: theme });
      const errors = [];
      let applied = false;
      page.on('pageerror', error => errors.push(error.message));
      await page.route('**/api/**', async route => {
        const url = new URL(route.request().url());
        let body = {};
        if (url.pathname.endsWith('/config/import')) {
          const request = route.request().postDataJSON();
          applied ||= !request.dry_run;
          body = { result: { nodes_created: 1, nodes_updated: 2, targets_created: 0, targets_updated: 0, groups_created: 0, groups_updated: 0, memberships_created: 0, http_services_created: 3, http_services_updated: 4, dry_run: request.dry_run } };
        } else if (url.pathname.endsWith('/auth/me')) body = { csrf_token: 'fixture' };
        else if (url.pathname.endsWith('/settings') || url.pathname.endsWith('/site-settings')) body = { settings: { theme_mode: theme } };
        else if (url.pathname.endsWith('/nodes')) body = { nodes: [] };
        else if (url.pathname.endsWith('/latency-config')) body = { targets: [], groups: [], group_members: [], node_groups: [], node_targets: [] };
        else if (url.pathname.endsWith('/shares')) body = { shares: [] };
        else if (url.pathname.endsWith('/audit')) body = { entries: [], next_before_id: 0 };
        await route.fulfill({ contentType: 'application/json', body: JSON.stringify(body) });
      });
      await page.goto(`${process.env.UI_BASE_URL || 'http://127.0.0.1:4173'}/admin`);
      await page.getByRole('button', { name: '设置', exact: true }).click();
      await page.getByRole('button', { name: /维护.*迁移与备份/ }).click();
      await page.getByRole('button', { name: '打开迁移与备份' }).click();
      await page.locator('input[type=file]').first().setInputFiles({ name: 'fixture.json', mimeType: 'application/json', buffer: Buffer.from('{"version":2}') });
      await page.getByRole('button', { name: '预检导入' }).click();
      await page.getByText('HTTP 服务：新增 3 / 更新 4').waitFor();
      assert.equal(applied, false, 'preview performed an import');
      await page.evaluate(theme => { document.documentElement.dataset.theme = theme; }, theme);
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false, 'horizontal overflow');
      await page.locator('.import-preview').scrollIntoViewIfNeeded();
      await page.screenshot({ path: path.join(output, `${theme}-${width}.png`), fullPage: true });
      page.once('dialog', async dialog => { assert.match(dialog.message(), /观测节点将按文件替换/); await dialog.dismiss(); });
      await page.getByRole('button', { name: '确认合并导入' }).click();
      assert.equal(applied, false, 'cancelled confirmation applied import');
      assert.deepEqual(errors, []);
      await page.close();
    }
    console.log(`Six viewport/theme checks passed: ${output}`);
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
