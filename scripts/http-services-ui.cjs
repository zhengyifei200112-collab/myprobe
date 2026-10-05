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
      let service = null, conflict = false, deletes = 0, lastWrite = '', observationMode = 'normal', statisticsMode = 'normal';
      const errors = [];
      page.on('pageerror', error => { errors.push(error.message); console.error(error.message); });
      await page.route('**/api/**', async route => {
        const request = route.request(), url = new URL(request.url());
        let body = {}, status = 200;
        if (url.pathname.includes('/service-monitors')) {
          if (url.pathname.endsWith('/statistics')) {
            assert.equal(url.searchParams.get('node_id'), 'node');
            body = { node_id: 'node', server_time: '2026-10-05T12:00:00Z', maintenance_excluded: false, execution_enabled: false, statistics: { requested_start: url.searchParams.get('start'), requested_end: url.searchParams.get('end'), start: '2026-10-05T10:00:00Z', end: '2026-10-05T11:57:54Z', retained_from: '2026-09-05T00:00:00Z', schedule_known_from: '2026-10-05T10:00:00Z', expected: 8, success: 1, failure: 1, missing: 6, unobserved: 2, success_rate: 0.5, coverage: 0.25 } };
            if (statisticsMode === 'empty') Object.assign(body.statistics, { end: body.statistics.start, expected: 0, success: 0, failure: 0, missing: 0, unobserved: 0, success_rate: null, coverage: null });
            if (statisticsMode === 'error') { status = 500; body = { error: 'fixture statistics unavailable' }; }
          } else if (url.pathname.endsWith('/results')) {
            const now = new Date('2026-10-05T12:00:00Z');
            const observed = (id, result, expires) => ({ task_id: id, node_id: 'node', revision: 1, scheduled_at: '2026-10-05T11:55:00Z', expires_at: expires || '2026-10-05T11:55:05Z', result });
            body = { execution_enabled: false, server_time: now.toISOString(), observations: [
              observed('success', { outcome: 'success', status_code: 200, duration_ms: 42.5, completed_at: '2026-10-05T11:55:01Z', certificates: [{ sha256: 'ab'.repeat(32), not_before: '2026-09-01T00:00:00Z', not_after: '2026-12-01T00:00:00Z', verified: true }] }),
              observed('failure', { outcome: 'failure', status_code: 500, duration_ms: 12, error_class: 'status_mismatch', completed_at: '2026-10-05T11:55:01Z' }),
              observed('busy', { outcome: 'unobserved', duration_ms: 0, error_class: 'busy', completed_at: '2026-10-05T11:55:01Z' }),
              observed('missing', null), observed('pending', null, '2026-10-05T12:00:05Z')
            ] };
            if (observationMode === 'empty') body.observations = [];
            if (observationMode === 'error') { status = 500; body = { error: 'fixture results unavailable' }; }
          } else if (request.method() === 'POST') { service = { ...request.postDataJSON(), id: 'fixture', revision: 1 }; status = 201; body = { service, execution_enabled: false }; }
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
      await page.getByRole('button', { name: '最近结果', exact: true }).click();
      const panel = page.getByRole('region', { name: '最近检查结果' });
      for (const text of ['检查成功', '检查失败', '未执行有效检查', '未收到结果', '等待回报']) await panel.getByText(text, { exact: true }).waitFor();
      await panel.locator('summary').click();
      await panel.getByText('SHA-256 ' + 'ab'.repeat(32), { exact: true }).waitFor();
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false, 'result overflow');
      await panel.scrollIntoViewIfNeeded();
      await page.screenshot({ path: path.join(output, `${theme}-${width}-results.png`), fullPage: true });
      observationMode = 'error';
      await panel.getByRole('button', { name: '刷新结果', exact: true }).click();
      await page.getByRole('alert').getByText('fixture results unavailable').waitFor();
      await panel.getByText('检查成功', { exact: true }).waitFor();
      observationMode = 'empty';
      await panel.getByRole('button', { name: '刷新结果', exact: true }).click();
      await panel.getByText('暂无保留的检查任务；这不表示服务正常。', { exact: true }).waitFor();
      await page.getByRole('button', { name: '关闭结果', exact: true }).click();
      await page.getByRole('button', { name: '检测统计', exact: true }).click();
      const statistics = page.getByRole('region', { name: '服务检测统计' });
      await statistics.getByRole('button', { name: '查询统计', exact: true }).click();
      await statistics.getByText('50.00%', { exact: true }).waitFor();
      await statistics.getByText('25.00%', { exact: true }).waitFor();
      await statistics.getByText('尚未排除维护窗口', { exact: false }).waitFor();
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false, 'statistics overflow');
      await statistics.scrollIntoViewIfNeeded();
      await page.screenshot({ path: path.join(output, `${theme}-${width}-statistics.png`), fullPage: true });
      await statistics.getByRole('combobox', { name: /^统计时间范围/ }).selectOption('1');
      assert.equal(await statistics.getByText('50.00%', { exact: true }).count(), 0, 'stale range data');
      statisticsMode = 'empty';
      await statistics.getByRole('button', { name: '查询统计', exact: true }).click();
      await statistics.getByText('无有效样本', { exact: true }).waitFor();
      await statistics.getByText('无计划样本', { exact: true }).waitFor();
      statisticsMode = 'error';
      await statistics.getByRole('button', { name: '查询统计', exact: true }).click();
      await statistics.getByRole('alert').waitFor();
      assert.equal(await statistics.getByText('无有效样本', { exact: true }).count(), 0, 'failed query kept old rates');
      await statistics.getByRole('button', { name: '关闭统计', exact: true }).click();
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
