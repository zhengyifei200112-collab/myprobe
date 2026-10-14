const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict'), fs = require('node:fs/promises'), path = require('node:path');
(async () => {
  const base = process.env.UI_BASE_URL;
  const browser = await chromium.launch({ channel: 'msedge', headless: true });
  const output = await fs.mkdtemp(path.join(require('node:os').tmpdir(), 'myprobe-service-transfer-'));
  try {
    for (const theme of ['light', 'dark']) for (const width of [360, 768, 1440]) {
      const context = await browser.newContext({ viewport: { width, height: 1000 }, colorScheme: theme });
      const login = await context.request.post(`${base}/api/v1/auth/login`, { data: { username: 'admin', password: 'correct-password' } });
      assert.equal(login.status(), 200);
      const auth = await login.json();
      const response = await context.request.get(`${base}/api/v1/admin/service-alert-transfers/export`);
      assert.equal(response.status(), 200);
      const bundle = await response.json();
      const destinations = await (await context.request.get(`${base}/api/v1/admin/service-alert-transfers/destinations`)).json();
      const selectedChannel = theme === 'light' && width === 360 ? destinations.channels.find(v => v.name === 'Destination telegram').id : bundle.channels[0].id;
      assert.equal(bundle.alerts.length, 1);
      assert.equal(JSON.stringify(bundle).includes('private-marker'), false);
      assert.equal(JSON.stringify(bundle).includes('private-channel-credential-marker'), false);
      // Change the imported parameters, retaining an actual exported schema.
      bundle.alerts[0].settings.enabled = true;
      bundle.alerts[0].settings.certificate_reminders_enabled = false;
      bundle.alerts[0].settings.duration_seconds += 1;
      const page = await context.newPage(), errors = [];
      page.on('pageerror', e => errors.push(e.message));
      await page.goto(`${base}/admin`);
      await page.getByRole('button', { name: 'HTTP 服务', exact: true }).click();
      await page.getByRole('button', { name: '服务告警迁移', exact: true }).click();
      const panel = page.getByRole('region', { name: '服务告警迁移', exact: true });
      async function selectBundle() {
        await panel.getByLabel('服务告警文件', { exact: true }).setInputFiles({ name: 'service-alerts.json', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(bundle)) });
        await panel.getByRole('button', { name: `映射服务 ${bundle.alerts[0].name}`, exact: true }).click();
        let picker = page.getByRole('dialog', { name: '选择目标服务', exact: true });
        await picker.getByLabel('搜索目标名称或 ID', { exact: true }).fill(bundle.alerts[0].source_id);
        await picker.getByRole('button', { name: `选择 ${bundle.alerts[0].name}（${bundle.alerts[0].source_id}）`, exact: true }).click();
        await panel.getByRole('button', { name: `映射通道 ${bundle.channels[0].name}`, exact: true }).click();
        picker = page.getByRole('dialog', { name: '选择目标通道', exact: true });
        await picker.getByLabel('搜索目标名称或 ID', { exact: true }).fill(selectedChannel);
        await picker.getByRole('button', { name: `选择 ${destinations.channels.find(v => v.id === selectedChannel).name}（${selectedChannel}）`, exact: true }).click();
        await panel.getByRole('button', { name: '预览服务告警导入', exact: true }).click();
        await panel.getByRole('heading', { name: '新建 0 个 · 覆盖 1 个', exact: true }).waitFor();
      }
      await selectBundle();
      await panel.getByText('原配置：', { exact: false }).waitFor();
      await page.evaluate(t => { document.documentElement.dataset.theme = t; }, theme);
      await page.evaluate(() => window.scrollTo(0, 0));
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false);
      const overflow = await panel.evaluate(region => [...region.querySelectorAll('select, article, button')].filter(el => el.getBoundingClientRect().right > region.getBoundingClientRect().right - 8).map(el => ({ tag: el.tagName, className: el.className, right: el.getBoundingClientRect().right, panelRight: region.getBoundingClientRect().right })));
      assert.deepEqual(overflow, [], 'controls stay within panel padding');
      await page.screenshot({ path: path.join(output, `${theme}-${width}-preview.png`), fullPage: true });
      if (theme === 'light' && width === 360) {
        await panel.getByText('来源与目标 Provider 类型不同，请核对选择。', { exact: true }).waitFor();
        // A preview must fail after a concurrent setting update.
        const existing = await (await context.request.get(`${base}/api/v1/admin/service-monitors/${bundle.alerts[0].source_id}/alerting`)).json();
        const update = await context.request.put(`${base}/api/v1/admin/service-monitors/${bundle.alerts[0].source_id}/alerting`, { headers: { 'X-CSRF-Token': auth.csrf_token }, data: { ...existing.alerting, service_id: undefined, cooldown_seconds: existing.alerting.cooldown_seconds + 1 } });
        assert.equal(update.status(), 200);
        await panel.getByRole('button', { name: '确认覆盖服务告警', exact: true }).click();
        await panel.getByRole('alert').getByText('本次请求未更新配置', { exact: false }).waitFor();
        assert.equal(await panel.getByRole('region', { name: '服务告警导入预览', exact: true }).count(), 0);
        await panel.getByRole('button', { name: '预览服务告警导入', exact: true }).click();
        await panel.getByRole('button', { name: '确认覆盖服务告警', exact: true }).waitFor();
        let attempts = 0, requestID = '', digest = '';
        await page.route('**/api/v1/admin/service-alert-transfers/apply', async route => {
          attempts++; const request = route.request().postDataJSON(); requestID = request.request_id; digest = request.preview_digest;
          const actual = await route.fetch(); assert.equal(actual.status(), 201);
          await route.fulfill({ status: 503, contentType: 'application/json', body: '{"error":"fixture_lost_response"}' });
        });
        await panel.getByRole('button', { name: '确认覆盖服务告警', exact: true }).click();
        await panel.getByRole('alert').getByText('fixture_lost_response', { exact: false }).waitFor();
        await panel.getByText('上次请求的结果尚待确认', { exact: false }).waitFor();
        assert.equal(attempts, 1);
        assert.equal(await panel.getByRole('button', { name: /^映射服务/ }).isDisabled(), true);
        await page.unroute('**/api/v1/admin/service-alert-transfers/apply');
        await page.reload(); await page.getByRole('button', { name: 'HTTP 服务', exact: true }).click(); await page.getByRole('button', { name: '服务告警迁移', exact: true }).click();
        const applied = page.waitForRequest(r => r.url().endsWith('/service-alert-transfers/apply'));
        await panel.getByRole('button', { name: '确认上次服务告警导入结果', exact: true }).click();
        const repeated = (await applied).postDataJSON(); assert.equal(repeated.request_id, requestID); assert.equal(repeated.preview_digest, digest);
        await panel.getByText('已确认此前导入结果', { exact: false }).waitFor();
        assert.equal(await page.evaluate(() => sessionStorage.getItem('myprobe-service-alert-import-pending-v1')), null);
        const saved = await (await context.request.get(`${base}/api/v1/admin/service-monitors/${bundle.alerts[0].source_id}/alerting`)).json();
        assert.equal(saved.alerting.enabled, true); assert.equal(saved.alerting.certificate_reminders_enabled, false);
        assert.equal(saved.alerting.channel_id, selectedChannel);
        // Corrupt browser recovery must prevent submission.
        for (const bad of ['{', JSON.stringify({ bundle, mapping: {}, preview: {}, requestID: 'invalid' }), JSON.stringify({ bundle, mapping: { services: { [bundle.alerts[0].source_id]: bundle.alerts[0].source_id }, channels: { [bundle.channels[0].id]: bundle.channels[0].id } }, preview: { items: [{ service_name: 'corrupt', channel_name: 'corrupt', after: bundle.alerts[0].settings, before: null }], created: 1, updated: 0, preview_digest: 'a'.repeat(64) }, requestID: 'a'.repeat(32) })]) {
          await page.evaluate(value => sessionStorage.setItem('myprobe-service-alert-import-pending-v1', value), bad);
          await panel.getByRole('button', { name: '关闭迁移', exact: true }).click(); await page.getByRole('button', { name: '服务告警迁移', exact: true }).click();
          await panel.getByText('待确认记录损坏，提交已暂停。', { exact: true }).waitFor();
          assert.equal(await panel.getByRole('button', { name: '确认覆盖服务告警', exact: true }).count(), 0);
          await panel.getByRole('button', { name: '清除损坏的导入记录', exact: true }).click();
        }
        // A pending full-preview cache written by the previous UI remains usable.
        const legacyMapping = { services: { [bundle.alerts[0].source_id]: bundle.alerts[0].source_id }, channels: { [bundle.channels[0].id]: selectedChannel } };
        const legacyResponse = await context.request.post(`${base}/api/v1/admin/service-alert-transfers/preview`, { headers: { 'X-CSRF-Token': auth.csrf_token }, data: { bundle, mapping: legacyMapping } });
        assert.equal(legacyResponse.status(), 200);
        const legacy = { bundle, mapping: legacyMapping, preview: (await legacyResponse.json()).preview, requestID: require('node:crypto').randomBytes(16).toString('hex') };
        await page.evaluate(value => sessionStorage.setItem('myprobe-service-alert-import-pending-v1', JSON.stringify(value)), legacy);
        await panel.getByRole('button', { name: '关闭迁移', exact: true }).click(); await page.getByRole('button', { name: '服务告警迁移', exact: true }).click();
        await panel.getByText('原配置：', { exact: false }).waitFor();
        const legacyRequest = page.waitForRequest(r => r.url().endsWith('/service-alert-transfers/apply'));
        await panel.getByRole('button', { name: '确认上次服务告警导入结果', exact: true }).click();
        const restoredRequest = (await legacyRequest).postDataJSON();
        assert.equal(restoredRequest.request_id, legacy.requestID); assert.equal(restoredRequest.preview_digest, legacy.preview.preview_digest);
        await panel.getByText('服务告警导入成功', { exact: false }).waitFor();
        await selectBundle();
        let sent = 0;
        page.on('request', r => { if (r.url().endsWith('/service-alert-transfers/apply')) sent++; });
        await page.evaluate(() => { Storage.prototype.setItem = () => { throw new Error('fixture_storage_blocked'); }; });
        await panel.getByRole('button', { name: '确认覆盖服务告警', exact: true }).click();
        await panel.getByRole('alert').waitFor(); assert.equal(sent, 0, 'storage failure prevents sending');
      } else {
        await panel.getByRole('button', { name: '确认覆盖服务告警', exact: true }).click();
        await panel.getByText('服务告警导入成功', { exact: false }).waitFor();
      }
      assert.deepEqual(errors, []); await context.close();
    }
    console.log(`Service alert mapping, overwrite preview, stale/lost response, reload/replay, corrupt/blocked storage and six theme/width views passed: ${output}`);
  } finally { await browser.close(); }
})().catch(e => { console.error(e); process.exitCode = 1; });
