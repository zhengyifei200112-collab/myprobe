const { launchFixtureBrowser } = require('./browser-fixture.cjs');
const assert = require('node:assert/strict'), fs = require('node:fs/promises'), path = require('node:path');
(async () => {
  const browser = await launchFixtureBrowser();
  const output = await fs.mkdtemp(path.join(require('node:os').tmpdir(), 'myprobe-diagnostics-'));
  const target = new URL(process.env.DIAGNOSTIC_URL), base = target.origin;
  try {
    for (const theme of ['light', 'dark']) for (const width of [360, 768, 1440]) {
      const context = await browser.newContext({ viewport: { width, height: 1000 }, colorScheme: theme });
      const page = await context.newPage(), errors = [];
      let privateReads = 0;
      page.on('pageerror', e => errors.push(e.message));
      page.on('request', r => { if (r.url().includes('/api/v1/admin/service-monitors')) privateReads++; });
      await page.goto(target.href);
      await page.getByRole('button', { name: '使用密码登录', exact: true }).waitFor();
      assert.equal(privateReads, 0, 'no private query before login');
      await page.getByLabel('用户名', { exact: true }).fill(process.env.DIAGNOSTIC_USERNAME);
      await page.getByLabel('密码', { exact: true }).fill(process.env.DIAGNOSTIC_PASSWORD);
      await page.getByRole('button', { name: '使用密码登录', exact: true }).click();
      const panel = page.getByRole('region', { name: '服务诊断', exact: true });
      await panel.getByText('HTTP · 状态码不符合要求', { exact: true }).first().waitFor();
      for (const key of ['service', 'observer', 'start', 'end']) assert.equal(new URL(page.url()).searchParams.get(key), target.searchParams.get(key));
      assert.equal(await panel.getByRole('combobox', { name: /^诊断观测节点/ }).inputValue(), target.searchParams.get('observer'));
      assert.equal(await panel.getByLabel('诊断开始（UTC）', { exact: true }).inputValue(), target.searchParams.get('start'));
      assert.equal(await panel.getByLabel('诊断结束（UTC）', { exact: true }).inputValue(), target.searchParams.get('end'));
      if (process.env.DIAGNOSTIC_CONTROLLED === '1') {
        for (const label of ['DNS · 解析失败', 'TCP · 连接被拒绝', 'TLS · 验证失败', '内容断言 · 不符合要求', '超时 · 具体阶段未记录', '能力不支持', 'Agent 本地策略拒绝', '等待回报']) await panel.getByText(label, { exact: true }).waitFor();
        assert.equal(await panel.getByText('当前只记录总耗时', { exact: false }).count(), 1);
      } else {
        await panel.getByText('检查成功', { exact: true }).first().waitFor();
      }
      assert.equal((await panel.innerText()).includes('private-marker'), false);
      await page.evaluate(t => { document.documentElement.dataset.theme = t; }, theme);
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false);
      await page.screenshot({ path: path.join(output, `${theme}-${width}.png`), fullPage: true });
      await page.reload();
      await panel.getByText('HTTP · 状态码不符合要求', { exact: true }).first().waitFor();
      await panel.getByLabel('诊断开始（UTC）', { exact: true }).fill('invalid');
      assert.equal(await panel.getByText('检查成功', { exact: true }).count(), 0, 'editing clears old data');
      await panel.getByRole('button', { name: '查询诊断', exact: true }).click();
      await panel.getByText('请选择观测节点，输入 UTC 时间，范围最多 31 天。', { exact: true }).waitFor();
      if (process.env.DIAGNOSTIC_CONTROLLED === '1' && theme === 'light' && width === 360) {
        const unavailable = new URL(target); unavailable.searchParams.set('service', 'missing-service');
        await page.goto(unavailable.href);
        await panel.getByRole('alert').waitFor();
        assert.equal(await panel.getByText('检查成功', { exact: true }).count(), 0, 'missing resource must not show old data');
        await page.goto(target.href);
        await panel.getByText('检查成功', { exact: true }).waitFor();
        await page.route('**/api/v1/admin/service-monitors/*/diagnostics?*', route => route.fulfill({ status: 503, contentType: 'application/json', body: '{"error":"fixture_unavailable"}' }));
        await panel.getByRole('button', { name: '查询诊断', exact: true }).click();
        await panel.getByRole('alert').waitFor();
        assert.equal(await panel.getByText('检查成功', { exact: true }).count(), 0, 'read failure clears old results');
        await page.unroute('**/api/v1/admin/service-monitors/*/diagnostics?*');
        // Simulated browser callback tests navigation storage, not a real OAuth provider.
        // Storage requires canonical order from notificationPath.
        await page.evaluate(({ service, observer, start, end }) => sessionStorage.setItem('myprobe-notification-navigation-v1', JSON.stringify({ path: `/admin?${new URLSearchParams({ service, observer, start, end })}`, savedAt: Date.now() })), Object.fromEntries(target.searchParams));
        await page.goto(`${base}/admin?oauth=success`);
        await panel.getByText('HTTP · 状态码不符合要求', { exact: true }).first().waitFor();
        const invalid = new URL(target); invalid.searchParams.append('observer', 'duplicate');
        await page.goto(invalid.href);
        await page.getByText('通知入口无效，请从故障事件或聚合记录重新选择。', { exact: true }).waitFor();
        assert.equal(await page.getByRole('region', { name: '服务诊断', exact: true }).count(), 0);
      }
      assert.deepEqual(errors, []);
      await context.close();
    }
    console.log(`Protected diagnostics, login/reload context and six theme/width views passed: ${output}`);
  } finally { await browser.close(); }
})().catch(e => { console.error(e); process.exitCode = 1; });
