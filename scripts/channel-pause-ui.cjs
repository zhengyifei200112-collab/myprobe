const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict'), fs = require('node:fs/promises'), path = require('node:path');
(async () => {
  const browser = await chromium.launch({ channel: 'msedge', headless: true });
  try {
    const context = await browser.newContext({ baseURL: process.env.PAUSE_BROWSER_URL });
    const login = await context.request.post('/api/v1/auth/login', { data: { username: 'pause-admin', password: process.env.PAUSE_BROWSER_PASSWORD } });
    assert.equal(login.status(), 200);
    const page = await context.newPage(), errors = [];
    page.on('pageerror', e => errors.push(e.message));
    const output = await fs.mkdtemp(path.join(require('node:os').tmpdir(), 'myprobe-channel-pause-'));
    async function screenshots(label, tab) {
      for (const theme of ['light', 'dark']) for (const width of [360, 768, 1440]) {
        await page.setViewportSize({ width, height: 1000 });
        await page.evaluate(t => document.documentElement.dataset.theme = t, theme);
        await page.getByRole('tab', { name: tab, exact: true }).focus();
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false);
        await page.screenshot({ path: path.join(output, `${label}-${theme}-${width}.png`), fullPage: true });
      }
    }
    await page.goto(`/admin?incident=${process.env.PAUSE_BROWSER_INCIDENT}`);
    await page.getByText('故障通知 · 发送预算已使用 0 / 5 · 已记录 26 次领取', { exact: true }).waitFor();
    await page.getByRole('button', { name: '查看每次尝试', exact: true }).click();
    await page.getByRole('button', { name: '加载较早尝试', exact: true }).waitFor();
    await page.getByRole('button', { name: '加载较早尝试', exact: true }).click();
    await page.getByText(/^第 1 次：已取消/).waitFor();
    assert.equal(await page.getByRole('button', { name: '加载较早尝试', exact: true }).count(), 0);
    await page.getByText(/渠道等待期限：/).waitFor();
    await screenshots('incident', '故障事件');
    await page.goto(`/admin?aggregate=${process.env.PAUSE_BROWSER_AGGREGATE}`);
    await page.getByText(/渠道等待期限：/).waitFor();
    await screenshots('aggregate', '通知聚合');
    await page.getByRole('tab', { name: '通知通道', exact: true }).click();
    await page.getByText(/最近一次渠道等待期限：/).waitFor();
    await page.getByRole('button', { name: '发送测试', exact: true }).click();
    await page.getByText('该通知通道正在等待重试，请刷新通道状态后查看等待期限。', { exact: true }).waitFor();
    await page.getByRole('button', { name: '刷新通道状态', exact: true }).click();
    await screenshots('channel', '通知通道');
    assert.equal((await page.locator('body').textContent()).includes('private-provider-marker'), false);
    assert.deepEqual(errors, []);
    console.log(`Shared wait, blocked test send, retained budget, 26-entry pagination and 18 theme/width views passed: ${output}`);
  } finally { await browser.close(); }
})().catch(e => { console.error(e); process.exitCode = 1; });
