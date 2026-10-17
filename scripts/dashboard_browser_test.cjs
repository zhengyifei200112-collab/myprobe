// The shared QA runner serves committed embedded assets from a disposable Server.
// Public API and WebSocket responses below are isolated synthetic fixtures.
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict');
const path = require('node:path');
const os = require('node:os');
const fs = require('node:fs/promises');

(async () => {
  const baseURL = process.env.MYPROBE_QA_URL || 'http://127.0.0.1:4173';
  assert.ok(['127.0.0.1', 'localhost', '[::1]'].includes(new URL(baseURL).hostname));
  const channel = process.env.BROWSER_CHANNEL || (process.platform === 'win32' ? 'msedge' : undefined);
  const browser = await chromium.launch({ ...(channel ? { channel } : {}), headless: true });
  const output = process.env.QA_OUTPUT_DIR || os.tmpdir();
  const screenshots = [];
  try {
    const context = await browser.newContext({ baseURL });
    context.setDefaultTimeout(15000);
    const page = await context.newPage();
    const now = new Date('2030-01-01T00:00:00Z');
    await page.clock.install({ time: now });
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
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
    await page.getByRole('tab', { name: 'Berlin 50', exact: true }).click();
    await page.getByText('没有匹配的节点', { exact: true }).waitFor();
    assert.equal(await rows.count(), 0, 'name, tag and status filters intersect');
    await page.getByRole('button', { name: '清空筛选', exact: true }).click();
    await page.getByLabel('节点状态', { exact: true }).selectOption('waiting');
    assert.equal(await rows.count(), 1);
    assert.equal(await rows.first().getAttribute('data-node-id'), '000');
    assert.ok((await rows.first().innerText()).includes('等待接入'));
    await page.getByLabel('节点状态', { exact: true }).selectOption('interrupted');
    assert.equal(await rows.count(), 1);
    assert.equal(await rows.first().getAttribute('data-node-id'), '001');
    await page.getByLabel('节点状态', { exact: true }).selectOption('all');
    await page.getByLabel('搜索节点', { exact: true }).fill('nothing-matches');
    await page.getByText('没有匹配的节点', { exact: true }).waitFor();
    await page.getByRole('button', { name: '清空筛选', exact: true }).click();
    assert.equal(await rows.count(), 100);
    await page.getByLabel('对比目标', { exact: true }).selectOption('target-a');
    await page.getByLabel('排序方式', { exact: true }).selectOption('latency');
    assert.equal(await rows.first().getAttribute('data-node-id'), '099');
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
    const openHistory = rows.first().getByRole('button');
    await openHistory.focus();
    await page.keyboard.press('Enter');
    await page.getByRole('dialog').waitFor();
    await page.keyboard.press('Escape');
    await page.getByRole('dialog').waitFor({ state: 'hidden' });
    assert.equal(await openHistory.evaluate(el => el === document.activeElement), true, 'history returns keyboard focus');
    await page.getByRole('button', { name: '网络质量', exact: true }).click();
    await page.getByRole('dialog', { name: '网络质量对比', exact: true }).waitFor();
    await page.keyboard.press('Escape');
    await page.getByRole('dialog').waitFor({ state: 'hidden' });
    await page.getByLabel('搜索节点', { exact: true }).fill('Node 00');
    for (const theme of ['light', 'dark']) for (const width of [360, 768, 1440]) {
      await page.setViewportSize({ width, height: 1000 });
      await page.evaluate(value => { document.documentElement.dataset.theme = value; }, theme);
      for (const view of ['table', 'cards']) {
        await page.getByLabel('显示方式', { exact: true }).selectOption(view);
        await page.evaluate(() => { document.activeElement?.blur(); window.scrollTo(0, 0); });
        const name = `myprobe-discovery-${view}-${theme}-${width}.png`;
        await page.screenshot({ path: path.join(output, name), fullPage: true });
        screenshots.push(name);
        assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), 'page must not overflow horizontally');
        assert.ok(await page.getByRole('button', { name: '清空筛选', exact: true }).evaluate(el => parseFloat(getComputedStyle(el).fontSize) >= 12), 'mobile action must have visible text');
        assert.ok(await page.getByRole('button', { name: '网络质量', exact: true }).locator('svg').isVisible(), 'network comparison entry retains a visible icon on mobile');
      }
    }
    wire.close();
    await page.getByText('实时连接中断，正在重连', { exact: true }).waitFor();
    assert.ok(await page.getByText('等待接入', { exact: true }).count());
    assert.deepEqual(errors, []);
    await fs.writeFile(path.join(output, 'result.json'), JSON.stringify({
      format: 'myprobe.dashboard-discovery-browser.v1', passed: true,
      fixture: '100 synthetic public API/WebSocket nodes; disposable real Server serves embedded assets',
      browser: browser.version(), screenshots,
      checks: ['combined-search-tag-status', 'waiting-interrupted-stale', 'URL-reload-target-sort', 'throttled-pointer-keyboard-order', 'history-focus-return', 'network-comparison-entry', 'reconnect-semantics', 'viewport-overflow'],
    }, null, 2), { mode: 0o600, flag: 'wx' });
    console.log('PASS: 100-node discovery, URL reload, missing data, throttled ordering, pointer/keyboard freeze, reconnect semantics and 12 viewport/theme/view screenshots');
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
