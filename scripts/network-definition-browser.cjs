const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict'), fs = require('node:fs/promises'), path = require('node:path');
(async () => {
 const output = path.join(process.env.QA_ARTIFACT_DIR || require('node:os').tmpdir(), 'requested-profiles');
 await fs.mkdir(output, { recursive: true });
 const browser = await chromium.launch({ headless: true, ...(process.env.PLAYWRIGHT_CHANNEL ? { channel: process.env.PLAYWRIGHT_CHANNEL } : {}), args: process.platform === 'linux' ? ['--no-sandbox'] : [] });
 const evidence = { format: 'myprobe.network-definition-browser.v1', source_commit: process.env.MYPROBE_TEST_SOURCE_COMMIT || 'working-tree', browser: browser.version(), backend: 'actual Gin/Store/embedded assets; no intercepted APIs', views: [] };
 let activePage;
 try {
  for (const theme of ['light', 'dark']) for (const width of [360, 768, 1440]) {
   const context = await browser.newContext({ viewport: { width, height: 1000 }, colorScheme: theme });
   await context.addInitScript(theme => localStorage.setItem('myprobe-theme', theme), theme);
   const login = await context.request.post(`${process.env.UI_BASE_URL}/api/v1/auth/login`, { data: { username: 'admin', password: 'correct horse battery staple' } }); assert.equal(login.status(), 200);
   const auth = await login.json(), page = await context.newPage(); activePage = page; const errors = [];
   page.on('pageerror', error => errors.push(error.message));
   await page.goto(`${process.env.UI_BASE_URL}/admin`); await page.getByRole('button', { name: '探测目标', exact: true }).click();
   const form = page.locator('form').filter({ has: page.getByRole('heading', { name: '添加目标', exact: true }) });
   const name = `Synthetic profile ${theme} ${width}`;
   await form.getByLabel('名称', { exact: true }).fill(name); await form.getByLabel('主机', { exact: true }).fill('example.test');
   await form.getByLabel('请求地址族', { exact: true }).selectOption('ipv6'); await form.getByLabel('每次 ICMP 包数', { exact: true }).fill('3');
   await form.locator(`input[value="${process.env.QA_PROFILE_NODE_ID}"]`).check();
   const created = page.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname.endsWith('/admin/targets'));
   await form.getByRole('button', { name: '创建目标', exact: true }).click();
   const response = await created; assert.equal(response.status(), 201); const target = (await response.json()).target;
   assert.equal(target.address_family, 'ipv6'); assert.equal(target.packet_count, 3);
   const card = page.locator('article.entity-card').filter({ has: page.getByText(name, { exact: true }) }); await card.waitFor();
   await card.getByRole('button', { name: '编辑', exact: true }).click();
   const edit = page.locator('form').filter({ has: page.getByRole('heading', { name: '编辑目标', exact: true }) });
   assert.equal(await edit.getByLabel('请求地址族', { exact: true }).inputValue(), 'ipv6'); assert.equal(await edit.getByLabel('每次 ICMP 包数', { exact: true }).inputValue(), '3');
   await edit.getByRole('heading', { name: '编辑目标', exact: true }).evaluate(el => el.scrollIntoView({ block: 'start' }));
   assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), true, 'requested-profile viewport overflow');
   await page.screenshot({ path: path.join(output, `profile-${theme}-${width}.png`) });
   await edit.locator('.assignment-box').scrollIntoViewIfNeeded();
   await page.screenshot({ path: path.join(output, `assignment-${theme}-${width}.png`) });
   const legacy = await context.request.patch(`${process.env.UI_BASE_URL}/api/v1/admin/targets/${target.id}`, { headers: { 'X-CSRF-Token': auth.csrf_token }, data: { name: `${name} renamed`, kind: 'ping', host: 'example.test', interval_seconds: 60, timeout_ms: 3000, enabled: true, sort_order: 0 } });
   assert.equal(legacy.status(), 200); const retained = (await legacy.json()).target; assert.equal(retained.address_family, 'ipv6'); assert.equal(retained.packet_count, 3);
   const exported = await context.request.get(`${process.env.UI_BASE_URL}/api/v1/admin/maintenance/config`); assert.equal(exported.status(), 200);
   const snapshot = await exported.json(); assert.equal(snapshot.version, 3); const portable = snapshot.targets.find(t => t.id === target.id); assert.equal(portable.address_family, 'ipv6'); assert.equal(portable.packet_count, 3);
   await page.getByRole('button', { name: '设置', exact: true }).click(); await page.getByRole('button', { name: /维护.*迁移与备份/ }).click(); await page.getByRole('button', { name: '打开迁移与备份' }).click();
   await page.getByText('配置版本 3', { exact: true }).waitFor();
   assert.deepEqual(errors, []); evidence.views.push({ theme, width, create_family: 'ipv6', packets: 3, edit_retained: true, legacy_edit_retained: true, export_version: 3, errors });
   await context.close();
  }
  await fs.writeFile(path.join(output, 'result.json'), JSON.stringify(evidence, null, 2)); console.log(JSON.stringify(evidence));
 } catch (error) { if (activePage && !activePage.isClosed()) await activePage.screenshot({ path: path.join(output, 'failure.png') }).catch(() => {}); throw error; } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exit(1); });
