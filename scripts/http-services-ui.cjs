// Local browser fixture: all APIs are intercepted, no real probes are dispatched.
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const fs = require('node:fs/promises');
const path = require('node:path');
const assert = require('node:assert/strict');
(async () => {
  const output = path.join(require('node:os').tmpdir(), 'myprobe-http-services-ui');
  await fs.mkdir(output, { recursive: true });
  const browser = await chromium.launch({ channel: 'msedge', headless: true });
  try {
    for (const theme of ['light', 'dark']) for (const width of [360, 768, 1440]) {
      const page = await browser.newPage({ viewport: { width, height: 1000 }, colorScheme: theme });
      let service = null, conflict = false, deletes = 0, lastWrite = '';
      const errors = [];
      page.on('pageerror', error => { errors.push(error.message); console.error(error.message); });
      page.on('console', message => { if (message.type() === 'error') console.error(message.text()); });
      await page.route('**/api/**', async route => {
        const request = route.request(), url = new URL(request.url());
        let body = {}, status = 200;
        if (url.pathname.includes('/service-monitors')) {
          if (request.method() === 'POST') { service = { ...request.postDataJSON(), id: 'fixture', revision: 1 }; status = 201; body = { service, execution_enabled: false }; }
          else if (request.method() === 'PUT') {
            lastWrite = request.postData();
            if (conflict) { status = 409; body = { error: 'service configuration changed; reload before saving' }; }
            else { service = { ...request.postDataJSON(), id: 'fixture', revision: 2 }; body = { service, execution_enabled: false }; }
          } else if (request.method() === 'DELETE') { deletes++; service = null; status = 204; }
          else if (url.pathname.endsWith('/fixture')) body = { service, execution_enabled: false };
          else body = { services: service ? [{ ...service, node_count: service.node_ids.length }] : [], next_cursor: '', execution_enabled: false };
        } else if (url.pathname.endsWith('/auth/me')) body = { csrf_token: 'fixture' };
        else if (url.pathname.endsWith('/settings') || url.pathname.endsWith('/site-settings')) body = { settings: { theme_mode: theme } };
        else if (url.pathname.endsWith('/nodes')) body = { nodes: [{ id: 'node', name: '测试节点', tags: [], latency_mode: 'ping', sort_order: 0, hidden: false, country_code: '', currency: '', billing_cycle: '', use_since_boot: false, custom_badges: [], custom_links: [], collection_seconds: 5, report_seconds: 5 }] };
        else if (url.pathname.endsWith('/latency-config')) body = { targets: [], groups: [], group_members: [], node_groups: [], node_targets: [] };
        else if (url.pathname.endsWith('/shares')) body = { shares: [] };
        else if (url.pathname.endsWith('/audit')) body = { entries: [], next_before_id: 0 };
        await route.fulfill({ status, contentType: 'application/json', body: status === 204 ? '' : JSON.stringify(body).replace('"expected":9007199254740992', '"expected":9007199254740993') });
      });
      await page.goto(`${process.env.UI_BASE_URL || 'http://127.0.0.1:4173'}/admin`);
      await page.getByRole('button', { name: 'HTTP 服务', exact: true }).click({ timeout: 5000 }).catch(async error => { console.error(await page.locator('body').innerText()); throw error; });
      await page.getByText('服务端 HTTP 探测尚未启用。', { exact: false }).waitFor();
      await page.getByRole('button', { name: '新建服务', exact: true }).click();
      await page.getByLabel('服务名称', { exact: true }).fill('网站健康检查');
      await page.getByLabel('目标 URL', { exact: true }).fill('https://example.com/health');
      await page.getByLabel('测试节点', { exact: true }).check();
      await page.getByRole('combobox', { name: /^内容断言/ }).selectOption('text_contains');
      await page.getByLabel('必须包含的文本', { exact: true }).fill('healthy');
      await page.evaluate(theme => { document.documentElement.dataset.theme = theme; }, theme);
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false, 'horizontal overflow');
      await page.screenshot({ path: path.join(output, `${theme}-${width}-form.png`), fullPage: true });
      await page.getByRole('button', { name: '保存服务', exact: true }).click();
      await page.getByRole('button', { name: '编辑服务', exact: true }).waitFor();
      assert.equal(service.spec.assertion.text, 'healthy');
      assert.equal(service.spec.timeout_ms, 5000);
      await page.getByRole('button', { name: '编辑服务', exact: true }).click();
      await page.getByRole('combobox', { name: /^内容断言/ }).selectOption('json_equals');
      await page.getByLabel('预期 JSON 值', { exact: true }).fill('9007199254740993');
      await page.getByRole('button', { name: '保存服务', exact: true }).click();
      await page.getByRole('button', { name: '编辑服务', exact: true }).click();
      assert.equal(await page.getByLabel('预期 JSON 值', { exact: true }).inputValue(), '9007199254740993');
      assert.match(lastWrite, /"expected":9007199254740993/);
      await page.getByLabel('服务名称', { exact: true }).fill('未覆盖的编辑');
      conflict = true;
      await page.getByRole('button', { name: '保存服务', exact: true }).click();
      await page.getByRole('alert').waitFor();
      assert.equal(await page.getByLabel('服务名称', { exact: true }).inputValue(), '未覆盖的编辑');
      assert.equal(service.name, '网站健康检查');
      await page.getByRole('button', { name: '取消编辑', exact: true }).click();
      await page.getByRole('button', { name: '删除服务', exact: true }).click();
      await page.getByRole('dialog').getByRole('button', { name: '取消', exact: true }).click();
      assert.equal(deletes, 0);
      await page.getByRole('button', { name: '删除服务', exact: true }).click();
      await page.getByRole('button', { name: '删除服务和历史', exact: true }).click();
      await page.getByText('暂无 HTTP 服务。', { exact: false }).waitFor();
      assert.equal(deletes, 1);
      assert.deepEqual(errors, []);
      await page.close();
    }
    console.log(`HTTP CRUD fixture passed six theme/viewport combinations: ${output}`);
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
