// Real embedded UI with synthetic API/WebSocket traffic and a controlled clock.
const assert = require('node:assert/strict');

module.exports = async function verifyDashboardCache(browser, baseURL) {
  const context = await browser.newContext({ baseURL, viewport: { width: 1440, height: 900 } });
  try {
    await context.addInitScript(() => {
      const stats = window.__nodeCacheChecks = { writes: 0, attempts: 0, serializations: 0, localeCalls: 0, reject: false };
      const stringify = JSON.stringify;
      JSON.stringify = function (value, ...args) {
        if (Array.isArray(value) && value.length === 50 && value[0]?.node?.id) stats.serializations++;
        return stringify.call(JSON, value, ...args);
      };
      const set = Storage.prototype.setItem;
      Storage.prototype.setItem = function (key, value) {
        if (key === 'myprobe-nodes') {
          stats.attempts++;
          if (stats.reject) throw new DOMException('Synthetic quota failure', 'QuotaExceededError');
        }
        const result = set.call(this, key, value);
        if (key === 'myprobe-nodes') stats.writes++;
        return result;
      };
      const format = Date.prototype.toLocaleString;
      Date.prototype.toLocaleString = function (...args) {
        if (args[0] === 'zh-CN' && args[1]?.hour12 === false) stats.localeCalls++;
        return format.apply(this, args);
      };
    });
    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    const stamp = '2030-01-01T00:00:00Z';
    await page.clock.install({ time: new Date(stamp) });
    await page.clock.pauseAt(new Date(stamp));
    const nodes = Array.from({ length: 50 }, (_, i) => ({
      node: { id: `cache-${i}`, name: `Cache node ${i}`, tags: [], country_code: '', sort_order: i, last_seen_at: stamp, report_seconds: 15 },
      online: true, stale: false,
      report: { captured_at: stamp, cpu: { usage_percent: 17, logical_cores: 2 }, memory: { total_bytes: 2147483648, used_bytes: 314572800, usage_percent: 14.6484375 }, networks: [], uptime_seconds: 3600 },
      traffic: { rx_bytes: 1000, tx_bytes: 2000 }, latency: [],
    }));
    let wire, fresh = true;
    await page.route('**/api/v1/public/nodes', route => fresh ? route.fulfill({ json: { nodes, server_time: stamp } }) : route.fulfill({ status: 503, body: 'synthetic unavailable' }));
    await page.routeWebSocket('**/api/v1/public/ws', socket => { wire = socket; if (fresh) socket.send(JSON.stringify({ type: 'snapshot', nodes })); });
    await page.goto('/');
    await page.waitForFunction(() => window.__nodeCacheChecks.writes === 2 && document.querySelectorAll('article.public-node-card').length === 50);
    const stats = () => page.evaluate(() => ({ ...window.__nodeCacheChecks }));
    const reset = () => page.evaluate(() => { for (const key of ['writes', 'attempts', 'serializations', 'localeCalls']) window.__nodeCacheChecks[key] = 0; });
    const cached = () => page.evaluate(() => JSON.parse(localStorage.getItem('myprobe-nodes')));
    const send = value => wire.send(JSON.stringify(value));
    const rendered = () => page.waitForFunction(expected => {
      const cards = [...document.querySelectorAll('article.public-node-card')];
      return cards.length === 50 && cards.every(card => Number(card.querySelector('[role="progressbar"][aria-label="CPU 使用率"]')?.getAttribute('aria-valuenow')) === expected[card.querySelector('h3')?.textContent]);
    }, Object.fromEntries(nodes.map(node => [node.node.name, node.report.cpu.usage_percent])));

    await reset();
    for (let index = 0; index < 500; index++) {
      const node = nodes[index % 50];
      node.report.cpu.usage_percent = index % 90 + 1;
      send({ type: 'node_metrics', node });
    }
    await rendered();
    assert.equal((await stats()).serializations, 0, 'rendered burst must not serialize eagerly');
    assert.equal((await stats()).writes, 0);
    await page.clock.runFor(249);
    assert.equal((await stats()).writes, 0);
    await page.clock.runFor(1);
    assert.equal((await stats()).writes, 1);
    assert.equal((await stats()).serializations, 1);
    assert.deepEqual(await cached(), nodes, 'coalesced write must use the latest values of all nodes');

    // A pending node event must never rewrite an older visibility snapshot.
    nodes[0].node.name = 'Removed by privacy snapshot';
    send({ type: 'node_metrics', node: nodes[0] });
    send({ type: 'snapshot', nodes: nodes.slice(1) });
    await page.waitForFunction(() => document.querySelectorAll('article.public-node-card').length === 49);
    assert.deepEqual(await cached(), nodes.slice(1));
    const afterSnapshot = (await stats()).writes;
    await page.clock.runFor(1000);
    assert.equal((await stats()).writes, afterSnapshot);
    assert.deepEqual(await cached(), nodes.slice(1));
    send({ type: 'snapshot', nodes: [] });
    await page.waitForFunction(() => document.querySelectorAll('article.public-node-card').length === 0);
    assert.deepEqual(await cached(), []);
    send({ type: 'snapshot', nodes });
    await rendered();
    await reset();
    await page.clock.runFor(2100);
    assert.equal((await stats()).localeCalls, 0, 'clock-only rerenders reuse unchanged sample text');
    assert.equal((await stats()).writes, 0, 'idle pages do not rewrite cache');

    nodes[0].report.cpu.usage_percent = 77;
    send({ type: 'node_metrics', node: nodes[0] });
    await rendered();
    await page.evaluate(() => {
      Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' });
      document.dispatchEvent(new Event('visibilitychange'));
      delete document.visibilityState;
    });
    assert.deepEqual(await cached(), nodes, 'backgrounding flushes the latest pending event');
    nodes[0].report.cpu.usage_percent = 78;
    send({ type: 'node_metrics', node: nodes[0] });
    await rendered();
    await page.evaluate(() => window.dispatchEvent(new PageTransitionEvent('pagehide', { persisted: true })));
    assert.deepEqual(await cached(), nodes, 'pagehide flushes before navigation/BFCache');

    await page.evaluate(() => { window.__nodeCacheChecks.reject = true; });
    send({ type: 'snapshot', nodes });
    nodes[0].report.cpu.usage_percent = 88;
    send({ type: 'node_metrics', node: nodes[0] });
    await rendered();
    await page.clock.runFor(250);
    assert.ok((await stats()).attempts > (await stats()).writes, 'storage failure was exercised');
    await page.evaluate(() => { window.__nodeCacheChecks.reject = false; });
    nodes[0].report.cpu.usage_percent = 89;
    send({ type: 'node_metrics', node: nodes[0] });
    await rendered();
    await page.clock.runFor(250);
    assert.deepEqual(await cached(), nodes, 'later events recover persistence after a quota failure');

    // Exercise the actual reload event, then use the persisted fallback with no
    // fresh API response or WebSocket snapshot. This checks cache contents only.
    nodes[0].report.cpu.usage_percent = 90;
    send({ type: 'node_metrics', node: nodes[0] });
    await rendered();
    fresh = false;
    await page.reload();
    await page.getByRole('alert').filter({ hasText: '暂时无法获取最新数据' }).waitFor();
    await rendered();
    assert.deepEqual(await cached(), nodes);
    assert.deepEqual(errors, []);
    console.log('PASS: 500-event burst renders immediately with one cache write; privacy/empty snapshots, hidden/pagehide/reload, quota recovery and clock-only timestamp reuse');
  } finally { await context.close(); }
};
