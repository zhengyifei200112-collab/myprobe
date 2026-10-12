// Real embedded frontend, authenticated API and isolated SQLite fixture.
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
(async () => {
  const browser = await chromium.launch({ channel: 'msedge', headless: true });
  try {
    const context = await browser.newContext({ baseURL: process.env.MAINTENANCE_BROWSER_URL, timezoneId: 'Asia/Shanghai' });
    await context.addInitScript(() => Object.defineProperty(crypto, 'randomUUID', { value: undefined, configurable: true }));
    const login = await context.request.post('/api/v1/auth/login', { data: { username: 'browser-admin', password: process.env.MAINTENANCE_BROWSER_PASSWORD } });
    assert.equal(login.status(), 200);
    const csrf = (await login.json()).csrf_token;
    const page = await context.newPage(), errors = [];
    page.on('pageerror', e => errors.push(e.message));
    const read = async () => { const r = await context.request.get('/api/v1/admin/maintenance-windows'); assert.equal(r.status(), 200); return (await r.json()).windows; };
    const open = async () => { await page.goto('/admin'); await page.getByRole('button', { name: '告警', exact: true }).click(); await page.getByRole('tab', { name: '维护窗口', exact: true }).click(); await page.getByRole('heading', { name: '新建一次性维护', exact: true }).waitFor(); };
    const localInput = ms => new Date(ms + 8 * 3600_000).toISOString().slice(0, 16);
    await open();
    await page.getByLabel('选择范围', { exact: true }).selectOption('tags');
    await page.getByLabel('标签（每行一个）', { exact: true }).fill('prod');
    await page.getByLabel('维护原因', { exact: true }).fill('Live planned work');
    await page.getByLabel('开始时间', { exact: true }).fill(localInput(Date.now() - 120_000));
    await page.getByLabel('结束时间', { exact: true }).fill(localInput(Date.now() + 3600_000));
    await page.getByRole('button', { name: '预览维护窗口', exact: true }).click();
    await page.getByRole('heading', { name: '确认维护窗口 · 1 个节点', exact: true }).waitFor();
    await page.getByLabel('维护原因', { exact: true }).fill('Live corrected work');
    assert.equal(await page.getByRole('button', { name: '确认创建维护窗口', exact: true }).count(), 0, 'edits invalidate preview');
    await page.getByRole('button', { name: '预览维护窗口', exact: true }).click();
    await page.getByRole('heading', { name: '确认维护窗口 · 1 个节点', exact: true }).waitFor();
    const output = await fs.mkdtemp(path.join(require('node:os').tmpdir(), 'myprobe-maintenance-live-'));
    for (const theme of ['light', 'dark']) for (const width of [360, 768, 1440]) {
      await page.setViewportSize({ width, height: 1000 });
      await page.evaluate(theme => document.documentElement.dataset.theme = theme, theme);
      await page.evaluate(() => scrollTo(0, 0));
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false, `preview overflow ${theme}/${width}`);
      await page.screenshot({ path: path.join(output, `preview-${theme}-${width}.png`), fullPage: true });
    }
    let original;
    await page.route('**/api/v1/admin/maintenance-windows', async route => {
      if (route.request().method() !== 'POST') return route.continue();
      original = route.request().postDataJSON();
      const r = await route.fetch(); assert.equal(r.status(), 201);
      await route.abort('failed');
    }, { times: 1 });
    await page.getByRole('button', { name: '确认创建维护窗口', exact: true }).click();
    await page.getByRole('button', { name: '重试确认维护结果', exact: true }).waitFor();
    assert.equal((await read()).length, 1);
    await open();
    assert.equal(await page.getByLabel('维护原因', { exact: true }).isDisabled(), true, 'pending locks new mutations');
    const replayRequest = page.waitForRequest(r => r.method() === 'POST' && r.url().endsWith('/maintenance-windows'));
    await page.getByRole('button', { name: '重试确认维护结果', exact: true }).click();
    assert.deepEqual((await replayRequest).postDataJSON(), original);
    await page.getByText('已确认此前创建结果：', { exact: false }).waitFor();
    assert.equal((await read()).length, 1);
    assert.equal(await page.evaluate(() => sessionStorage.getItem('myprobe-maintenance-pending-v1')), null);
    await page.getByRole('tab', { name: '故障事件', exact: true }).click();
    await page.getByRole('button', { name: '查看 Maintenance node 的投递记录', exact: true }).click();
    await page.getByText('维护窗口已取消此投递任务，故障事件继续记录。已发出的请求无法撤回；维护结束后依据新鲜观测恢复提醒。', { exact: true }).waitFor();
    await page.getByRole('button', { name: '查看每次尝试', exact: true }).click();
    if (process.env.MAINTENANCE_BROWSER_SENT === '1') {
      await page.getByText('任务已取消，但已开始的一次发送获得渠道成功确认。取消不会撤回已发送的通知。', {exact:true}).waitFor();
      await page.getByText('第 1 次：已送达', {exact:false}).waitFor();
    } else {
      await page.getByText('尚未开始投递尝试。', {exact:true}).waitFor();
      assert.equal(await page.getByText('任务已取消，但已开始的一次发送获得渠道成功确认。取消不会撤回已发送的通知。', {exact:true}).count(),0);
    }
    const incidentContext = page.getByRole('region', { name: '事件维护关联', exact: true });
    await incidentContext.getByText('查询时节点处于维护中，通知受到抑制；故障状态继续记录。', { exact: true }).waitFor();
    await incidentContext.getByText('Live corrected work', { exact: true }).waitFor();
    await page.getByText('此通知关联的维护窗口', { exact: true }).click();
    const deliveryContext = page.getByRole('region', { name: '通知维护关联', exact: true });
    await deliveryContext.getByText('Live corrected work', { exact: true }).waitFor();
    for (const theme of ['light','dark']) for (const width of [360,768,1440]) {
      await page.setViewportSize({width,height:1000}); await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme); await page.evaluate(()=>scrollTo(0,0));
      assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1),false,`context overflow ${theme}/${width}`);
      await page.screenshot({path:path.join(output,`context-${theme}-${width}.png`),fullPage:true});
    }
    await page.getByRole('button', { name: '查看维护窗口', exact: true }).click();
    await page.getByRole('heading', { name: '新建一次性维护', exact: true }).waitFor();
    await page.getByRole('button', { name: '取消此维护', exact: true }).click();
    const windowID = (await read())[0].id;
    let cancelOriginal;
    await page.route(`**/maintenance-windows/${windowID}/cancel`, async route => {
      cancelOriginal = route.request().postDataJSON();
      const r = await route.fetch(); assert.equal(r.status(), 200); await route.abort('failed');
    }, { times: 1 });
    await page.getByRole('button', { name: '确认取消维护窗口', exact: true }).click();
    await page.getByRole('button', { name: '重试确认维护结果', exact: true }).waitFor();
    await open();
    const replayCancel = page.waitForRequest(r => r.url().endsWith(`/${windowID}/cancel`));
    await page.getByRole('button', { name: '重试确认维护结果', exact: true }).click();
    assert.deepEqual((await replayCancel).postDataJSON(), cancelOriginal);
    await page.getByText('已确认此前取消结果', { exact: false }).waitFor();
    assert.equal((await read())[0].revision, 2);
    await page.getByRole('tab', { name: '故障事件', exact: true }).click();
    await page.getByRole('button', { name: '查看 Maintenance node 的投递记录', exact: true }).click();
    await page.getByRole('region', { name: '事件维护关联', exact: true }).getByText('查询时节点没有生效的维护窗口。', {exact:true}).waitFor();
    await page.getByRole('region', { name: '事件维护关联', exact: true }).getByText('Live corrected work',{exact:true}).waitFor();
    await page.getByRole('tab', { name: '维护窗口', exact:true }).click();
    await page.getByText('查看冻结成员与记录', { exact: true }).click();
    for (const theme of ['light', 'dark']) for (const width of [360, 768, 1440]) {
      await page.setViewportSize({ width, height: 1000 }); await page.evaluate(theme => document.documentElement.dataset.theme = theme, theme);
      await page.evaluate(() => scrollTo(0, 0));
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false, `history overflow ${theme}/${width}`);
      await page.screenshot({ path: path.join(output, `history-${theme}-${width}.png`), fullPage: true });
    }
    // Corrupt records retain uncertainty and cannot reach the renderer or mutate.
    for (const corrupted of ['{', JSON.stringify({ kind: 'create', preview: { draft: null } }), JSON.stringify({ kind: 'cancel', windowID, revision: -1, reason: 'corrupted' })]) {
      await page.evaluate(raw => sessionStorage.setItem('myprobe-maintenance-pending-v1', raw), corrupted); await open();
      await page.getByText('无法读取待确认维护记录。', { exact: false }).waitFor();
      assert.equal(await page.getByLabel('维护原因', { exact: true }).isDisabled(), true);
      assert.equal(await page.evaluate(() => sessionStorage.getItem('myprobe-maintenance-pending-v1')), corrupted);
      await page.getByText('核对结果后处理本地记录', { exact: true }).click();
      await page.getByLabel('我已核对窗口和审计记录', { exact: true }).check();
      await page.getByRole('button', { name: '清除本地待确认记录', exact: true }).click();
    }
    // A changed tag scope must require a fresh preview, then allow corrected input.
    await page.getByLabel('选择范围', { exact: true }).selectOption('tags');
    await page.getByLabel('标签（每行一个）', { exact: true }).fill('prod');
    await page.getByLabel('维护原因', { exact: true }).fill('Scope changes');
    await page.getByRole('button', { name: '预览维护窗口', exact: true }).click();
    await page.getByRole('heading', { name: '确认维护窗口 · 1 个节点', exact: true }).waitFor();
    const added = await context.request.post('/api/v1/admin/nodes', { headers: { 'X-CSRF-Token': csrf }, data: { name: 'New matching node', tags: ['prod'] } });
    assert.equal(added.status(), 201);
    await page.getByRole('button', { name: '确认创建维护窗口', exact: true }).click();
    await page.getByText('确认内容或成员已变化，请重新预览。', { exact: true }).waitFor();
    assert.equal((await read()).length, 1);
    assert.equal(await page.getByLabel('维护原因', { exact: true }).isDisabled(), false);
    await page.getByRole('button', { name: '预览维护窗口', exact: true }).click();
    await page.getByRole('heading', { name: '确认维护窗口 · 2 个节点', exact: true }).waitFor();
    await page.getByLabel('选择范围', { exact: true }).selectOption('nodes');
    assert.equal(await page.getByRole('button', { name: '确认创建维护窗口', exact: true }).count(), 0);
    await page.getByRole('button', { name: '刷新维护列表', exact: true }).click();
    await page.getByRole('option', { name: 'New matching node', exact: true }).waitFor({ state: 'attached' });
    const nodesResponse = await context.request.get('/api/v1/admin/nodes');
    assert.equal(nodesResponse.status(), 200);
    const nodeIDs = (await nodesResponse.json()).nodes.map(n => n.id);
    await page.getByLabel('维护节点（可多选）', { exact: true }).selectOption(nodeIDs);
    await page.getByRole('button', { name: '预览维护窗口', exact: true }).click();
    await page.getByRole('heading', { name: '确认维护窗口 · 2 个节点', exact: true }).waitFor();
    await page.evaluate(() => {
      window.maintenanceSetItem = Storage.prototype.setItem;
      Storage.prototype.setItem = function(key, value) { if (key === 'myprobe-maintenance-pending-v1') throw new Error('fixture storage unavailable'); return window.maintenanceSetItem.call(this,key,value); };
    });
    await page.getByRole('button', { name: '确认创建维护窗口', exact: true }).click();
    await page.getByText('fixture storage unavailable', { exact: true }).waitFor();
    assert.equal((await read()).length, 1, 'storage must persist before sending');
    await page.evaluate(() => { Storage.prototype.setItem = window.maintenanceSetItem; delete window.maintenanceSetItem; });
    await page.getByRole('button', { name: '确认创建维护窗口', exact: true }).click();
    await page.getByText('维护窗口已创建：', { exact: false }).waitFor();
    await page.getByRole('button', { name: '取消此维护', exact: true }).click();
    await page.getByRole('button', { name: '确认取消维护窗口', exact: true }).click();
    await page.getByText('维护窗口已取消', { exact: false }).waitFor();
    assert.deepEqual(errors, [], 'browser rendering errors');
    console.log('MAINTENANCE_BROWSER_OK', JSON.stringify({ windows: (await read()).length, screenshots: output, themes: 2, widths: [360, 768, 1440], lateAck: process.env.MAINTENANCE_BROWSER_SENT === '1', lostCreateResponse: true, lostCancelResponse: true, changedMembers: true, explicitMembers: 2, blockedStorage: true, corruptedRecovery: 3 }));
  } finally { await browser.close(); }
})().catch(e => { console.error(e); process.exit(1); });
