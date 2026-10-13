const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict'), fs = require('node:fs/promises'), path = require('node:path');
const os = require('node:os');
(async () => {
  const baseline = process.env.MYPROBE_CAPACITY_BASELINE === '1';
  const browser = await chromium.launch({ channel: 'msedge', headless: true, args: ['--enable-precise-memory-info'] });
  const output = await fs.mkdtemp(path.join(require('node:os').tmpdir(), 'myprobe-transfer-capacity-'));
  const measurements = [];
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
    assert.equal((await context.request.post(`${process.env.UI_BASE_URL}/api/v1/auth/login`, { data: { username: 'admin', password: 'correct-password' } })).status(), 200);
    const source = await (await context.request.get(`${process.env.UI_BASE_URL}/api/v1/admin/service-alert-transfers/export`)).json();
    assert.equal(source.alerts.length, 1000); assert.equal(source.channels.length, 1000);
    if (!baseline) for (const alert of source.alerts) alert.settings.cooldown_seconds = 301;
    const page = await context.newPage();
    await page.goto(`${process.env.UI_BASE_URL}/admin`); await page.getByRole('button', { name: 'HTTP 服务', exact: true }).click(); await page.getByRole('button', { name: '服务告警迁移', exact: true }).click();
    const panel = page.getByRole('region', { name: '服务告警迁移', exact: true });
    // Before optimization, measure representative samples only. The original
    // repeated catalogs would expand the full limit to two million option nodes.
    for (const count of baseline ? [10, 50] : [10, 50, 1000]) {
      const bundle = { ...source, alerts: source.alerts.slice(0, count) };
      const refs = new Set(bundle.alerts.map(v => v.channel_id)); bundle.channels = source.channels.filter(v => refs.has(v.id));
      const started = performance.now();
      await panel.getByLabel('服务告警文件', { exact: true }).setInputFiles({ name: 'capacity-alerts.json', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(bundle)) });
      await panel.getByText('来源 → 当前环境', { exact: true }).waitFor();
      await page.waitForFunction(() => document.querySelector('[aria-label="服务告警迁移"] input[type=file]')?.disabled === false);
      const metrics = await panel.evaluate(region => ({ elements: region.querySelectorAll('*').length, options: region.querySelectorAll('option').length, heapBytes: performance.memory?.usedJSHeapSize ?? null }));
      measurements.push({ sourceServices: count, sourceChannels: bundle.channels.length, destinationServices: 1000, destinationChannels: 1000, renderMS: +(performance.now() - started).toFixed(1), ...metrics });
      if (!baseline) assert.ok(metrics.elements < 5000, 'rendering must be bounded independently of the full graph');
    }
    const api = [];
    if (!baseline) {
      await panel.getByLabel('搜索来源服务', { exact: true }).fill('Capacity service 0999');
      await panel.getByRole('button', { name: '映射服务 Capacity service 0999', exact: true }).click();
      let picker = page.getByRole('dialog', { name: '选择目标服务', exact: true });
      await picker.getByLabel('搜索目标名称或 ID', { exact: true }).fill('capacity-service-0999');
      await picker.getByRole('button', { name: '选择 Capacity service 0999（capacity-service-0999）', exact: true }).click();
      await panel.getByLabel('搜索来源服务', { exact: true }).fill('');
      const mapping = { format: 'myprobe-service-alert-mapping', version: 1, services: Object.fromEntries(source.alerts.map(v => [v.source_id, v.source_id])), channels: Object.fromEntries(source.channels.map(v => [v.id, v.id])) };
      await panel.getByLabel('服务告警映射文件', { exact: true }).setInputFiles({ name: 'explicit-mapping.json', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(mapping)) });
      await panel.getByText('已选服务 1000 / 1000，已选通道 1000 / 1000。', { exact: false }).waitFor();
      const lastSource = source.alerts[999]; const unmapped = { ...mapping, services: { [lastSource.source_id]: '' }, channels: {} };
      await panel.getByLabel('服务告警映射文件', { exact: true }).setInputFiles({ name: 'partial-mapping.json', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(unmapped)) });
      await panel.getByText('已选服务 999 / 1000，已选通道 1000 / 1000。', { exact: false }).waitFor();
      assert.equal(await panel.getByRole('button', { name: '预览服务告警导入', exact: true }).isDisabled(), true);
      await panel.getByLabel('服务告警映射文件', { exact: true }).setInputFiles({ name: 'explicit-mapping.json', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(mapping)) });
      await panel.getByRole('button', { name: '预览服务告警导入', exact: true }).click();
      const previewRegion = panel.getByRole('region', { name: '服务告警导入预览', exact: true });
      await previewRegion.getByRole('heading', { name: '新建 0 个 · 覆盖 1000 个', exact: true }).waitFor();
      assert.equal(await previewRegion.locator('article').count(), 25);
      await previewRegion.getByRole('button', { name: '预览下一页', exact: true }).click();
      await previewRegion.getByText('预览第 2 / 40 页 · 1000 项', { exact: true }).waitFor();
      assert.ok(await panel.locator('*').count() < 5000);
      const badMappings = [
        { ...mapping, extra: true },
        { ...mapping, services: { unknown: source.alerts[0].source_id } },
        { ...mapping, services: { [source.alerts[0].source_id]: 'unknown-destination' } },
        { ...mapping, services: { [source.alerts[0].source_id]: source.alerts[1].source_id } },
      ];
      for (const bad of badMappings) {
        await panel.getByLabel('服务告警映射文件', { exact: true }).setInputFiles({ name: 'invalid-mapping.json', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(bad)) });
        await panel.getByRole('alert').waitFor();
        await panel.getByText('已选服务 1000 / 1000，已选通道 1000 / 1000。', { exact: false }).waitFor();
        assert.equal(await previewRegion.count(), 0, 'rejected mapping must invalidate the prior preview');
      }
      const login = await (await context.request.post(`${process.env.UI_BASE_URL}/api/v1/auth/login`, { data: { username: 'admin', password: 'correct-password' } })).json();
      // Login rotates this context's session; reload obtains its current CSRF token.
      await page.reload(); await page.getByRole('button', { name: 'HTTP 服务', exact: true }).click(); await page.getByRole('button', { name: '服务告警迁移', exact: true }).click();
      await panel.getByLabel('服务告警文件', { exact: true }).setInputFiles({ name: 'capacity-alerts.json', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(source)) });
      await panel.getByLabel('服务告警映射文件', { exact: true }).setInputFiles({ name: 'explicit-mapping.json', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(mapping)) });
      const typedMapping = { services: mapping.services, channels: mapping.channels };
      for (let i = 0; i < 5; i++) {
        const started = performance.now();
        const response = await context.request.post(`${process.env.UI_BASE_URL}/api/v1/admin/service-alert-transfers/preview`, { headers: { 'X-CSRF-Token': login.csrf_token }, data: { bundle: source, mapping: typedMapping } });
        assert.equal(response.status(), 200); assert.equal((await response.json()).preview.items.length, 1000);
        api.push({ operation: 'preview-1000', milliseconds: +(performance.now() - started).toFixed(1) });
      }
      await panel.getByRole('button', { name: '预览服务告警导入', exact: true }).click();
      await panel.getByRole('button', { name: '确认覆盖服务告警', exact: true }).waitFor();
      let posted;
      await page.route('**/api/v1/admin/service-alert-transfers/apply', async route => {
        posted = route.request().postDataJSON(); const started = performance.now(); const response = await route.fetch();
        assert.equal(response.status(), 201); assert.equal((await response.json()).service_ids.length, 1000);
        api.push({ operation: 'apply-1000', milliseconds: +(performance.now() - started).toFixed(1) });
        await route.fulfill({ status: 503, contentType: 'application/json', body: '{"error":"capacity_lost_response"}' });
      });
      await panel.getByRole('button', { name: '确认覆盖服务告警', exact: true }).click();
      await panel.getByRole('alert').getByText('capacity_lost_response', { exact: false }).waitFor();
      await panel.getByRole('button', { name: '确认上次服务告警导入结果', exact: true }).waitFor();
      const cached = await page.evaluate(() => JSON.parse(sessionStorage.getItem('myprobe-service-alert-import-pending-v1')));
      assert.equal(cached.version, 2); assert.equal(cached.preview.items, undefined); assert.equal(cached.bundle.alerts.length, 1000);
      measurements.push({ pendingRequestBytes: Buffer.byteLength(JSON.stringify(cached)) });
      await page.unroute('**/api/v1/admin/service-alert-transfers/apply'); await page.reload();
      await page.getByRole('button', { name: 'HTTP 服务', exact: true }).click(); await page.getByRole('button', { name: '服务告警迁移', exact: true }).click();
      const request = page.waitForRequest(r => r.url().endsWith('/service-alert-transfers/apply'));
      await panel.getByRole('button', { name: '确认上次服务告警导入结果', exact: true }).click();
      const replay = (await request).postDataJSON(); assert.equal(replay.request_id, posted.request_id); assert.equal(replay.preview_digest, posted.preview_digest);
      await panel.getByText('已确认此前导入结果：新建 0 个，更新 1000 个。', { exact: false }).waitFor();
      assert.equal(await page.evaluate(() => sessionStorage.getItem('myprobe-service-alert-import-pending-v1')), null);
      // Render the full-source mapping panel in all required views.
      for (const theme of ['light', 'dark']) for (const width of [360, 768, 1440]) {
        await page.setViewportSize({ width, height: 1000 }); await page.evaluate(t => { document.documentElement.dataset.theme = t; }, theme);
        await panel.getByLabel('服务告警文件', { exact: true }).setInputFiles({ name: 'capacity-alerts.json', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(source)) });
        await panel.getByLabel('搜索来源服务', { exact: true }).fill('0999'); await panel.getByLabel('搜索来源通道', { exact: true }).fill('0999');
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false);
        await page.evaluate(() => window.scrollTo(0, 0)); await page.screenshot({ path: path.join(output, `${theme}-${width}.png`) });
        await panel.getByRole('button', { name: '映射服务 Capacity service 0999', exact: true }).click();
        picker = page.getByRole('dialog', { name: '选择目标服务', exact: true });
        assert.equal(await picker.locator('.destination-row').count(), 25);
        await picker.getByRole('button', { name: '目标下一页', exact: true }).click();
        await picker.getByText('目标第 2 / 40 页', { exact: true }).waitFor();
        await picker.getByLabel('搜索目标名称或 ID', { exact: true }).fill('capacity-service-0999');
        assert.equal(await picker.locator('.destination-row').count(), 1);
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false);
        await page.screenshot({ path: path.join(output, `${theme}-${width}-picker.png`) });
        await page.keyboard.press('Escape'); await picker.waitFor({ state: 'hidden' });
        assert.equal(await panel.getByRole('button', { name: '映射服务 Capacity service 0999', exact: true }).evaluate(el => el === document.activeElement), true);
      }
    }
    const environment = { platform: os.platform(), osRelease: os.release(), architecture: os.arch(), cpu: os.cpus()[0]?.model, logicalCPUs: os.cpus().length, systemRAMBytes: os.totalmem(), node: process.version, browser: await browser.version() };
    await fs.writeFile(path.join(output, 'measurements.json'), JSON.stringify({ baseline, environment, measurements, api }, null, 2));
    console.log(JSON.stringify({ baseline, environment, measurements, api, output }));
    await context.close();
  } finally { await browser.close(); }
})().catch(e => { console.error(e); process.exitCode = 1; });
