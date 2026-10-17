// Owns only a disposable local Server. Synthetic credentials stay in memory.
const assert = require('node:assert/strict');
const crypto = require('node:crypto');
const fs = require('node:fs/promises');
const net = require('node:net');
const os = require('node:os');
const path = require('node:path');
const { spawn, execFileSync } = require('node:child_process');
const { once } = require('node:events');

(async () => {
  const binary = path.resolve(process.argv[2] || '');
  const output = path.resolve(process.argv[3] || '');
  const profile = process.argv[4] || 'forms';
  assert.ok(['forms', 'node-batches', 'node-order', 'node-details'].includes(profile), 'Unknown QA profile');
  assert.ok(process.argv[2] && process.argv[3], 'Usage: node run-form-units-qa.cjs /server /existing-empty-output');
  assert.ok((await fs.stat(binary)).isFile());
  assert.ok((await fs.stat(output)).isDirectory());
  assert.equal((await fs.readdir(output)).length, 0, 'Use a fresh evidence directory');
  const repo = path.resolve(__dirname, '..');
  const sourceCommit = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: repo, encoding: 'utf8', windowsHide: true }).trim();
  const sourceDirty = execFileSync('git', ['status', '--porcelain'], { cwd: repo, encoding: 'utf8', windowsHide: true }).trim() !== '';
  const hash = async file => crypto.createHash('sha256').update(await fs.readFile(file)).digest('hex');
  const sourceHashes = {};
  for (const name of ['internal/store/node_timeline.go', 'internal/store/node_timeline_test.go', 'internal/store/migrations/047_node_timeline_indexes.sql', 'internal/httpapi/node_timeline.go', 'internal/httpapi/node_timeline_test.go', 'web/src/node-details/NodeTimeline.vue', 'web/src/node-details/timeline-view.ts', 'web/src/node-details/timeline-view.test.mjs', 'web/src/charting.ts']) sourceHashes[name] = await hash(path.join(repo, name));
  for (const name of ['web/src/AdminApp.vue', 'web/src/forms/units.ts', 'web/src/forms/units.test.ts', 'web/src/forms/QuantityInput.vue', 'web/src/notification-center/NotificationCenter.vue', 'web/src/notification-center/PolicyManager.vue', 'web/src/public-dashboard/metrics.ts', 'scripts/form_units_browser_test.cjs', 'scripts/alert-policies-ui.cjs', 'scripts/run-form-units-qa.cjs']) sourceHashes[name] = await hash(path.join(repo, name));
  const assetHashes = {};
  for (const name of ['web/src/NodeDetailApp.vue','web/src/node-details/HistoryCharts.vue','web/src/node-details/history-view.ts','web/src/node-details/navigation.ts','web/src/node-details/node-details.css','web/src/api.ts','web/src/main.ts','web/src/types.ts','web/src/charting.ts','internal/store/history_bounds.go','internal/store/traffic.go','internal/httpapi/history_window.go','scripts/node-detail-live-acceptance.cjs','scripts/node-detail-ui-acceptance.cjs']) sourceHashes[name] = await hash(path.join(repo,name));
  for (const name of ['web/src/NodeOrderManager.vue','scripts/node_order_browser_test.cjs','internal/store/node_order.go','internal/store/admin.go','internal/httpapi/node_order.go']) sourceHashes[name] = await hash(path.join(repo,name));
  for (const name of ['web/src/NodeBatchManager.vue', 'web/src/admin-api.ts', 'internal/store/node_batch.go', 'internal/store/node_batch_migration.go', 'internal/store/migrations/046_node_batches.sql', 'internal/store/store.go', 'internal/agentgateway/hub.go', 'internal/agentgateway/gateway.go', 'internal/httpapi/node_batch.go', 'internal/httpapi/server.go', 'scripts/node_batch_browser_test.cjs']) sourceHashes[name] = await hash(path.join(repo, name));
  for (const name of ['web/src/App.vue', 'web/src/public-dashboard/PublicNodeCard.vue', 'web/src/public-dashboard/PublicNodeTable.vue', 'web/src/public-dashboard/discovery.ts', 'web/src/public-dashboard/discovery.test.ts', 'web/src/public-dashboard/discovery.css', 'scripts/dashboard_browser_test.cjs']) sourceHashes[name] = await hash(path.join(repo, name));
  for (const name of ['index.html', ...(await fs.readdir(path.join(repo, 'internal/webui/dist/assets'))).map(name => 'assets/' + name)]) assetHashes[name] = await hash(path.join(repo, 'internal/webui/dist', name));
  const binaryHash = await hash(binary);
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'myprobe-form-units-'));
  await fs.chmod(root, 0o700);
  const listener = net.createServer();
  listener.listen(0, '127.0.0.1');
  await once(listener, 'listening');
  const port = listener.address().port;
  await new Promise(resolve => listener.close(resolve));
  const password = crypto.randomBytes(24).toString('hex');
  const key = crypto.randomBytes(32).toString('hex');
  const base = `http://127.0.0.1:${port}`;
  const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('MYPROBE_')));
  const server = spawn(binary, [], {
    env: { ...env, MYPROBE_LISTEN: `127.0.0.1:${port}`, MYPROBE_DATABASE: path.join(root, 'fixture.db'), MYPROBE_ADMIN_USERNAME: 'admin', MYPROBE_ADMIN_PASSWORD: password, MYPROBE_ENCRYPTION_KEY: key, MYPROBE_RELEASE_CHECKS_AUTO: 'false' },
    stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true,
  });
  let serverLog = '';
  const collect = chunk => { serverLog = (serverLog + chunk.toString()).slice(-65536); };
  server.stdout.on('data', collect); server.stderr.on('data', collect);
  let serverError;
  server.on('error', error => { serverError = error; });
  const exited = new Promise(resolve => server.once('close', resolve));
  try {
    let ready = false;
    for (const deadline = Date.now() + 30000; Date.now() < deadline;) {
      if (serverError || server.exitCode !== null) throw new Error('Disposable Server exited before readiness');
      try { ready = (await fetch(base + '/healthz', { signal: AbortSignal.timeout(1000) })).ok; } catch {}
      if (ready) break;
      await new Promise(resolve => setTimeout(resolve, 50));
    }
    assert.ok(ready, 'Disposable Server readiness deadline');
    if (profile === 'forms') await fs.mkdir(path.join(output, 'discovery'), { mode: 0o700 });
    const scripts = profile === 'forms' ? ['form_units_browser_test.cjs', 'alert-policies-ui.cjs', 'dashboard_browser_test.cjs'] : profile === 'node-details' ? ['node-detail-live-acceptance.cjs', 'node-detail-ui-acceptance.cjs'] : [profile === 'node-batches' ? 'node_batch_browser_test.cjs' : 'node_order_browser_test.cjs'];
    for (const script of scripts) {
      const scriptOutput = ['form_units_browser_test.cjs', 'node_batch_browser_test.cjs', 'node_order_browser_test.cjs', 'node-detail-live-acceptance.cjs', 'node-detail-ui-acceptance.cjs'].includes(script) ? output : path.join(output, script === 'alert-policies-ui.cjs' ? 'policy-contract' : 'discovery');
      const child = spawn(process.execPath, [path.join(__dirname, script)], {
        env: { ...env, UI_BASE_URL: base, MYPROBE_QA_URL: base, MYPROBE_QA_PASSWORD: password, QA_OUTPUT_DIR: scriptOutput, MYPROBE_TEST_URL: base, MYPROBE_TEST_PASSWORD: password, MYPROBE_TEST_OUTPUT: scriptOutput, PROBE_TEST_URL: base, PROBE_TEST_USERNAME: 'admin', PROBE_TEST_PASSWORD: password, PROBE_SCREENSHOT_DIR: scriptOutput },
        stdio: 'inherit', windowsHide: true, timeout: 240000,
      });
      const [code, signal] = await once(child, 'exit');
      assert.equal(code, 0, `Browser fixture ${script} failed (${signal || code})`);
    }
    if (profile === 'node-details') {
      const live = JSON.parse(await fs.readFile(path.join(output,'live-result.json'),'utf8'));
      const ui = JSON.parse(await fs.readFile(path.join(output,'ui-result.json'),'utf8'));
      assert.equal(live.passed,true); assert.equal(ui.passed,true);
      await fs.writeFile(path.join(output,'result.json'),JSON.stringify({format:'myprobe.node-details-browser.v1',passed:true,browser:live.browser,live_result_sha256:await hash(path.join(output,'live-result.json')),ui_result_sha256:await hash(path.join(output,'ui-result.json')),screenshots:[...live.screenshots,...ui.screenshots]},null,2),{mode:0o600,flag:'wx'});
    }
    assert.equal(JSON.parse(await fs.readFile(path.join(output, 'result.json'), 'utf8')).passed, true);
    const additional = profile === 'forms' ? { discovery_result_sha256: await hash(path.join(output, 'discovery/result.json')), policy_contract_passed: true } : {};
    await fs.writeFile(path.join(output, 'run-manifest.json'), JSON.stringify({ format: profile === 'forms' ? 'myprobe.form-units-native-run.v1' : profile === 'node-batches' ? 'myprobe.node-batch-native-run.v1' : profile === 'node-details' ? 'myprobe.node-details-native-run.v1' : 'myprobe.node-order-native-run.v1', source_commit: sourceCommit, source_dirty: sourceDirty, server_sha256: binaryHash, source_sha256: sourceHashes, assets_sha256: assetHashes, browser_result_sha256: await hash(path.join(output, 'result.json')), ...additional }, null, 2), { mode: 0o600, flag: 'wx' });
  } finally {
    if (server.pid && server.exitCode === null) server.kill();
    await exited;
    // No recursive cleanup is used. This private directory can be inspected on
    // failure; only the fixed files created by this Server are removed.
    for (const name of ['fixture.db', 'fixture.db-wal', 'fixture.db-shm']) {
      await fs.unlink(path.join(root, name)).catch(error => { if (error.code !== 'ENOENT') throw error; });
    }
    await fs.rmdir(root).catch(error => { if (!['ENOENT', 'ENOTEMPTY'].includes(error.code)) throw error; });
    await fs.writeFile(path.join(output, 'server.log'), serverLog.replaceAll(password, '[synthetic-password]').replaceAll(key, '[synthetic-key]'), { mode: 0o600, flag: 'wx' });
  }
})().catch(error => { console.error(error.message); process.exitCode = 1; });
