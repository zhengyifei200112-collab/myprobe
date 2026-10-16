const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict'), fs = require('node:fs/promises'), path = require('node:path');
(async () => {
 const base = process.env.UI_BASE_URL, ids = process.env.QA_PUBLIC_IDS.split(','), output = process.env.QA_ARTIFACT_DIR || path.join(require('node:os').tmpdir(), 'myprobe-network-comparison');
 await fs.mkdir(output, { recursive: true });
 const browser = await chromium.launch({ headless: true, ...(process.env.PLAYWRIGHT_CHANNEL ? { channel: process.env.PLAYWRIGHT_CHANNEL } : {}), args: process.platform === 'linux' ? ['--no-sandbox'] : [] });
 const evidence = { format: 'myprobe.network-comparison-browser.v1', source_commit: process.env.MYPROBE_TEST_SOURCE_COMMIT || '', browser_version: browser.version(), started_at: new Date().toISOString(), transport: 'real Gin/Store APIs and built embedded assets; synthetic persisted task history; one intentional network abort for failure UI', views: [], roles: [] };
 let activePage;
 try {
  for (const theme of ['light', 'dark']) for (const width of [360, 768, 1440]) {
   const context = await browser.newContext({ viewport: { width, height: 1000 }, colorScheme: theme, locale: 'zh-CN' });
   await context.addInitScript(theme => localStorage.setItem('myprobe-theme', theme), theme);
   const page = await context.newPage(); activePage = page; const errors = [], calls = [], series = [];
   page.on('pageerror', e => errors.push(e.message));
   page.on('request', r => { const u = new URL(r.url()); if (u.pathname.includes('/network-quality')) calls.push(u.pathname); });
   page.on('response', async r => { if (new URL(r.url()).pathname.endsWith('/network-quality/series') && r.status() === 200) series.push(await r.json()); });
   await page.goto(base); assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), true, 'dashboard viewport overflow'); await page.getByRole('button', { name: '网络质量', exact: true }).click();
   const dialog = page.getByRole('dialog', { name: '网络质量对比' }); await dialog.waitFor();
   const compare = dialog.getByRole('button', { name: '比较', exact: true });
   await dialog.locator('.quality-result-heading').waitFor(); await compare.waitFor({ state: 'visible' }); await assertEnabled(compare);
   const search = dialog.getByLabel('搜索观测节点', { exact: true }), searchCalls = calls.length;
   await search.fill('观测节点 01'); assert.equal(await dialog.locator('.quality-observers input[type="checkbox"]').count(), 1); await search.fill('');
   assert.equal(calls.length, searchCalls, 'local observer search does not query');
   for (const box of await dialog.locator('.quality-observers input[type="checkbox"]').all()) if (await box.isChecked()) await box.uncheck();
   for (const id of ids) await dialog.locator(`input[value="${id}"]`).check();
   assert.equal(await dialog.locator('.quality-observers input[type="checkbox"]:checked').count(), 8);
   assert.equal(await dialog.locator(`input[value="${process.env.QA_EMPTY_ID}"]`).isDisabled(), true);
   const before = calls.length; await compare.click(); await dialog.locator('.quality-table tbody tr').nth(7).waitFor(); await assertEnabled(compare);
   assert.equal(calls.length - before, 2, 'one scoped target query and one batched series query');
   const data = series.at(-1); assert.equal(data.nodes.length, 8); assert.equal(data.aggregation, 'time_series');
   assert.equal(data.address_family.status, 'per_node'); assert.equal(data.packet_loss.value_pct, null);
   const proof = data.nodes.find(n => n.node_id === ids[0]);
   assert.equal(proof.address_family.status, 'partial'); assert.equal(proof.address_family.ipv4_tasks, 1); assert.equal(proof.address_family.ipv6_tasks, 1);
   assert.equal(proof.packet_loss.status, 'partial'); assert.equal(proof.packet_loss.value_pct, 50); assert.equal(proof.packet_loss.sent, 2); assert.equal(proof.packet_loss.received, 1);
   assert.ok((await dialog.locator('.quality-table tbody tr').first().locator('.quality-packets').textContent()).includes('部分观测'));
   assert.ok(data.nodes.slice(1).every(n => n.address_family.status === 'unknown' && n.packet_loss.value_pct === null));
   assert.ok(data.nodes.every(n => n.points.length > 0 && n.points.length <= 2000));
   assert.equal(await page.getByText('隐藏观测节点', { exact: true }).count(), 0);
   assert.equal(await dialog.locator('.quality-chart canvas').count(), 1);
   // Custom two-hour range contains successful samples, failed probes and real
   // planned gaps from the persisted fixture rather than mocked API payloads.
   await dialog.getByLabel('时间范围', { exact: true }).selectOption('custom');
   await dialog.getByLabel('显示时区', { exact: true }).fill('Asia/Shanghai'); await dialog.getByLabel('显示时区', { exact: true }).press('Tab');
   const local = ms => new Date(ms + 8 * 3600000).toISOString().slice(0, 16);
   await dialog.getByLabel('起点（所选时区）', { exact: true }).fill(local(Date.now() - 119 * 60000));
   await dialog.getByLabel('终点（所选时区）', { exact: true }).fill(local(Date.now() - 60000));
   await compare.click(); await dialog.locator('.quality-table tbody tr').nth(7).waitFor(); await assertEnabled(compare);
   const custom = series.at(-1); assert.equal(custom.timezone, 'Asia/Shanghai');
   assert.ok(custom.nodes.some(n => n.points.some(p => p.counts.planned > 0 && p.coverage_pct === 0 && p.rtt_mean_ms === null)));
   assert.ok(custom.nodes.some(n => n.counts.failure > 0 && n.counts.missing > 0));
   assert.equal(await dialog.locator('.quality-chart canvas').count(), 1, 'chart recreated after query');
   assert.equal(await dialog.evaluate(el => el.scrollWidth <= el.clientWidth + 1), true, 'dialog overflow');
   await dialog.locator('.quality-result-heading').scrollIntoViewIfNeeded();
   await page.screenshot({ path: path.join(output, `public-${theme}-${width}.png`) });
   await dialog.locator('.quality-table-wrap').scrollIntoViewIfNeeded();
   await page.screenshot({ path: path.join(output, `table-${theme}-${width}.png`) });
   await dialog.locator('.quality-packets').first().scrollIntoViewIfNeeded();
   await dialog.locator('.quality-table-wrap').evaluate(el => {
    const row = el.querySelector('tbody tr'), cell = row.querySelector('.quality-packets'), pinned = row.querySelector('th');
    // Native scrollIntoView does not account for the sticky observer column.
    el.scrollLeft += cell.getBoundingClientRect().left - pinned.getBoundingClientRect().right;
   });
   await page.screenshot({ path: path.join(output, `packet-evidence-${theme}-${width}.png`) });
   if (width === 1440 && theme === 'light') {
    await dialog.getByLabel('探测目标', { exact: true }).selectOption({ label: 'IPv6 配置目标 · TCP' }); await compare.click(); await dialog.locator('.quality-table').waitFor(); await assertEnabled(compare);
    const unsupported = series.at(-1).nodes.find(n => n.node_id === ids[0]);
    assert.equal(unsupported.counts.success, 0); assert.equal(unsupported.counts.failure, 0); assert.ok(unsupported.counts.not_executed > 0); assert.equal(unsupported.execution_failure_pct, null); assert.equal(unsupported.rtt_mean_ms, null);
    evidence.ipv6_case = { synthetic_configured_observer: true, unsupported_is_not_execution_failure: true, exact_packet_loss_unavailable: true, actual_ipv6_network_execution: false };
    // Only this observer is unsupported. After local 23:00 the last completed
    // peak can overlap the other seven observers' real successful fixture data.
    // Select the explicit no-success population; never infer empty data from
    // the wall-clock-dependent peak preset.
    for (const id of ids.slice(1)) await dialog.locator(`input[value="${id}"]`).uncheck();
    await compare.click(); await dialog.locator('.quality-table tbody tr').waitFor(); await assertEnabled(compare);
    assert.equal(series.at(-1).nodes.length, 1); assert.equal(series.at(-1).nodes[0].node_id, ids[0]);
    assert.equal(series.at(-1).nodes[0].counts.success, 0); assert.ok(series.at(-1).nodes[0].counts.not_executed > 0);
    await dialog.getByText('所选范围没有可信的成功 RTT 样本；空白不代表 0 ms。').waitFor({ state: 'visible' });
    await dialog.getByLabel('时间范围', { exact: true }).selectOption('peak'); await compare.click(); await dialog.locator('.quality-table').waitFor(); await assertEnabled(compare);
    const peak = series.at(-1); assert.equal(Date.parse(peak.requested_end) - Date.parse(peak.requested_start), 4 * 3600000);
    assert.ok(Date.parse(peak.requested_end) <= Date.parse(peak.as_of));
    await dialog.getByText('所选范围没有可信的成功 RTT 样本；空白不代表 0 ms。').waitFor({ state: 'visible' });
    let abort = true;
    await page.route('**/network-quality/series?**', async route => { if (abort) { abort = false; await route.abort(); } else await route.continue(); });
    await compare.click(); await dialog.getByRole('alert').waitFor(); assert.ok((await dialog.getByRole('alert').textContent()).includes('网络请求未完成')); await assertEnabled(compare);
    await compare.click(); await dialog.locator('.quality-table').waitFor(); await assertEnabled(compare); await page.unroute('**/network-quality/series?**');
    for (const box of await dialog.locator('.quality-observers input[type="checkbox"]').all()) if (await box.isChecked()) await box.uncheck();
    await dialog.locator(`input[value="${process.env.QA_EMPTY_ID}"]`).check(); await compare.click();
    await dialog.getByText('所选节点暂无可比较目标，请由管理员配置或分配探测目标。').waitFor();
   }
   await page.keyboard.press('Tab'); assert.equal(await dialog.evaluate(el => el.contains(document.activeElement)), true, 'focus stays in modal after async work');
   await page.keyboard.press('Escape'); await dialog.waitFor({ state: 'hidden' });
   assert.equal(await page.getByRole('button', { name: '网络质量', exact: true }).evaluate(el => el === document.activeElement), true, 'focus returns to entry');
   assert.deepEqual(errors, []);
   evidence.views.push({ theme, width, nodes: 8, api_mode: 'time_series', requests_per_explicit_node_change: 2, canvas: true, mixed_legacy_and_native_evidence: true, partial_packet_loss_pct: 50, unknown_history_preserved: true, errors: [] });
   await context.close();
  }
  // Authentication roles use the same real backend; no payload interception.
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
  const login = await context.request.post(`${base}/api/v1/auth/login`, { data: { username: 'admin', password: 'correct horse battery staple' } }); assert.equal(login.status(), 200);
  const page = await context.newPage(); activePage = page; await page.goto(`${base}/admin`); await page.getByRole('button', { name: '网络质量', exact: true }).click();
  let dialog = page.getByRole('dialog', { name: '网络质量对比' }); await dialog.locator('.quality-result-heading').waitFor();
  assert.equal(await dialog.locator(`input[value="${process.env.QA_HIDDEN_ID}"]`).count(), 1);
  evidence.roles.push({ role: 'administrator', hidden_observer_selectable: true }); await page.keyboard.press('Escape');
  const shareBase = `${base}/api/v1/share/${process.env.QA_SHARE_ID}`;
  const shareLogin = await context.request.post(`${shareBase}/login`, { data: { password: process.env.QA_SHARE_PASSWORD } }); assert.equal(shareLogin.status(), 200);
  await page.goto(`${base}/share/${process.env.QA_SHARE_ID}`); await page.getByRole('button', { name: '网络质量', exact: true }).click();
  dialog = page.getByRole('dialog', { name: '网络质量对比' }); await dialog.locator('.quality-result-heading').waitFor();
  assert.equal(await dialog.locator('.quality-observers input[type="checkbox"]').count(), 2);
  assert.equal(await dialog.locator(`input[value="${process.env.QA_HIDDEN_ID}"]`).count(), 1);
  const forbidden = await context.request.get(`${shareBase}/network-quality/series?node_id=${ids[1]}&target_id=unknown&kind=ping&range=1h`); assert.equal(forbidden.status(), 404);
  evidence.roles.push({ role: 'password_share', observers: 2, selected_private_observer: true, outside_scope_status: 404 }); await context.close();
  await fs.writeFile(path.join(output, 'result.json'), JSON.stringify(evidence, null, 2));
  console.log(JSON.stringify(evidence));
 } catch (error) { if (activePage && !activePage.isClosed()) await activePage.screenshot({ path: path.join(output, 'failure.png') }).catch(() => {}); throw error; } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exit(1); });
async function assertEnabled(locator) { await locator.evaluate(async el => { for (let i = 0; i < 200 && el.disabled; i++) await new Promise(r => setTimeout(r, 25)); if (el.disabled) throw new Error('comparison remained busy'); }); }
