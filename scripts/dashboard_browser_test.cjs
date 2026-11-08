// Build first, start `vite preview --host 127.0.0.1 --port 4173` in web/.
// API and WebSocket responses below are isolated synthetic public fixtures.
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict');
const path = require('node:path');
const os = require('node:os');

(async () => {
  const baseURL = process.env.MYPROBE_QA_URL || 'http://127.0.0.1:4173';
  assert.ok(['127.0.0.1', 'localhost', '[::1]'].includes(new URL(baseURL).hostname));
  const browser = await chromium.launch({ channel: process.env.BROWSER_CHANNEL || 'msedge', headless: true });
  try {
    const context = await browser.newContext({ baseURL });
    const page = await context.newPage();
    await page.clock.install();
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    const now = new Date();
    const nodes = Array.from({ length: 100 }, (_, i) => ({
      node: { id: String(i).padStart(3, '0'), name: `Node ${String(i).padStart(3, '0')}`, tags: [i % 2 ? 'Tokyo' : 'Berlin'], country_code: 'US', sort_order: i, currency: 'USD', price_minor: 499, billing_cycle: 'monthly', last_seen_at: now.toISOString(), expires_at: new Date(now.getTime() + (i + 1) * 86400000).toISOString(), report_seconds: 5 },
      online: true, stale: false,
      report: { captured_at: now.toISOString(), cpu: { usage_percent: i, logical_cores: 2, model: 'Fixture', architecture: 'amd64' }, memory: { usage_percent: 20, total_bytes: 1000000000, used_bytes: 200000000 }, swap: { total_bytes: 0, used_bytes: 0, usage_percent: 0 }, disks: [{ mount: '/', total_bytes: 1000000000, used_bytes: 100000000, usage_percent: 10 }], networks: [], load: { one: 0, five: 0, fifteen: 0 }, uptime_seconds: 3600, processes: 50 },
      traffic: { rx_bytes: 0, tx_bytes: 0 },
      latency: [{ target_id: 'target-a', name: 'Test target A', kind: 'ping', success: true, latency_ms: i + 1, updated_at: now.toISOString() }],
    }));
    // Authenticated contact alone is not a first sample or an extra online
    // dashboard row. Keep the waiting state even while the Agent is reachable.
    nodes[0].report = undefined;
    nodes[1].online = false;
    nodes[2].stale = true;
    nodes[99].latency.push(
      { target_id: 'target-b', name: 'A longer secondary network target', kind: 'ping', success: false, error_class: 'timeout', latency_ms: 0, updated_at: now.toISOString() },
      { target_id: 'target-c', name: 'Test target C', kind: 'ping', success: true, latency_ms: 42, updated_at: now.toISOString() },
    );
    const payload = { nodes, server_time: now.toISOString() };
    await page.route('**/api/v1/public/nodes', route => route.fulfill({ json: payload }));
    await page.route('**/api/v1/public/nodes/*/history?*', route => route.fulfill({ json: { range: '1h', bucket_seconds: 60, metrics: [], latency: [], traffic: [] } }));
    let wire;
    await page.routeWebSocket('**/api/v1/public/ws', socket => { wire = socket; socket.send(JSON.stringify({ type: 'snapshot', nodes })); });
    await page.goto('/?view=table&sort=cpu&status=all');
    await page.waitForSelector('.node-table tbody tr');
    const rows = page.locator('.node-table tbody tr');
    assert.equal(await rows.count(), 100);
    assert.equal(await rows.first().getAttribute('data-node-id'), '099');
    await page.getByLabel('搜索节点', { exact: true }).fill('TOKYO');
    await page.getByLabel('节点状态', { exact: true }).selectOption('attention');
    assert.equal(await rows.count(), 8); // 5 hot CPUs + 3 upcoming expiry, interrupted overlaps expiry
    await page.getByLabel('搜索节点', { exact: true }).fill('nothing-matches');
    await page.getByText('没有匹配的节点', { exact: true }).waitFor();
    await page.getByRole('button', { name: '清空筛选', exact: true }).click();
    assert.equal(await rows.count(), 100);
    await page.getByLabel('对比目标', { exact: true }).selectOption('target-a');
    await page.getByLabel('排序方式', { exact: true }).selectOption('latency');
    await page.reload();
    await page.waitForSelector('.node-table tbody tr');
    assert.equal(await page.getByLabel('排序方式', { exact: true }).inputValue(), 'latency');
    assert.equal(await page.getByLabel('对比目标', { exact: true }).inputValue(), 'target-a');
    await page.getByLabel('排序方式', { exact: true }).selectOption('cpu');
    await page.mouse.move(0, 0);
    nodes[3].report.cpu.usage_percent = 100;
    wire.send(JSON.stringify({ type: 'node_metrics', node: nodes[3] }));
    assert.equal(await rows.first().getAttribute('data-node-id'), '099');
    await page.clock.fastForward(5100);
    assert.equal(await rows.first().getAttribute('data-node-id'), '003');
    await rows.first().hover();
    nodes[4].report.cpu.usage_percent = 101;
    wire.send(JSON.stringify({ type: 'node_metrics', node: nodes[4] }));
    await page.clock.fastForward(5100);
    assert.equal(await rows.first().getAttribute('data-node-id'), '003');
    await page.mouse.move(0, 0);
    await rows.first().getByRole('button').focus();
    await page.clock.fastForward(5100);
    assert.equal(await rows.first().getAttribute('data-node-id'), '003');
    await page.getByLabel('搜索节点', { exact: true }).focus();
    await page.clock.fastForward(5100);
    assert.equal(await rows.first().getAttribute('data-node-id'), '004');
    await page.getByLabel('搜索节点', { exact: true }).fill('Node 00');
    for (const theme of ['light', 'dark']) for (const width of [360, 768, 1440]) {
      await page.setViewportSize({ width, height: 1000 });
      await page.evaluate(value => { document.documentElement.dataset.theme = value; }, theme);
      for (const view of ['table', 'cards']) {
        await page.getByLabel('显示方式', { exact: true }).selectOption(view);
        await page.evaluate(() => { document.activeElement?.blur(); window.scrollTo(0, 0); });
        await page.screenshot({ path: path.join(process.env.QA_OUTPUT_DIR || os.tmpdir(), `myprobe-discovery-${view}-${theme}-${width}.png`), fullPage: true });
        assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), 'page must not overflow horizontally');
        assert.ok(await page.getByRole('button', { name: '清空筛选', exact: true }).evaluate(el => parseFloat(getComputedStyle(el).fontSize) >= 12), 'mobile action must have visible text');
      }
    }
    await page.getByLabel('显示方式', { exact: true }).selectOption('cards');
    await page.getByRole('button', { name: '切换到详情显示模式', exact: true }).click();
    await page.getByLabel('搜索节点', { exact: true }).fill('Node 099');
    const detailed = page.locator('.public-node-card--detailed');
    assert.equal(await detailed.count(), 1);
    for (const theme of ['light', 'dark']) for (const width of [360, 768, 1440, 1920]) {
      await page.setViewportSize({ width, height: 1000 });
      await page.evaluate(value => { document.documentElement.dataset.theme = value; }, theme);
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), 'detailed cards must fit the viewport');
      assert.equal(await detailed.locator('.public-node-card__latency-grid > div').count(), 3);
      assert.ok(await detailed.locator('h3').evaluate(el => Number(getComputedStyle(el).fontWeight) >= 600), 'node name stays prominent');
      assert.ok(await detailed.locator('strong, b, dd, button, .ds-status, .ds-badge').evaluateAll(elements => elements.every(el => Number(getComputedStyle(el).fontWeight) === 400)), 'card details use regular weight');
      assert.ok(await detailed.locator('.public-node-card__rates strong').evaluateAll(elements => elements.every(el => el.scrollWidth <= el.clientWidth + 1)), 'both transfer rates must remain readable');
      if (width >= 1440) {
        assert.equal(await page.locator('.node-grid').evaluate(el => getComputedStyle(el).gridTemplateColumns.split(' ').length), width >= 1800 ? 4 : 3);
        const box = await detailed.boundingBox();
        assert.ok(box.height < 400 && box.height < box.width, `routine details must fit a horizontal rectangle at ${width}px: ${JSON.stringify(box)}`);
        assert.ok(box.width < width / 2, 'filtered detailed card retains the multi-column card size');
      }
      await detailed.getByText('更多信息', { exact: true }).click();
      await detailed.getByText('公网 IP', { exact: true }).waitFor();
      await detailed.locator('summary').focus();
      await page.keyboard.press('Enter');
      assert.equal(await detailed.locator('details').getAttribute('open'), null);
      await detailed.screenshot({ path: path.join(process.env.QA_OUTPUT_DIR || os.tmpdir(), `myprobe-detailed-${theme}-${width}.png`) });
    }
    await page.getByRole('button', { name: '切换到简洁显示模式', exact: true }).click();
    assert.equal(await page.locator('.public-node-card--detailed').count(), 0);
    wire.close();
    await page.getByText('实时连接中断，正在重连', { exact: true }).waitFor();
    assert.ok(await page.getByText('等待接入', { exact: true }).count());
    assert.deepEqual(errors, []);
    console.log('PASS: 100-node discovery, URL reload, ordering, reconnect; 12 overview and 8 detailed-card views; three/four columns, readable rates, three latency results and keyboard disclosure');
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
