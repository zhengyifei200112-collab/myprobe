// Disposable Windows main-Server fixtures. Only synthetic PNGs and the manifest
// leave the private directory; database, downloads and Go output stay private.
const fs = require('node:fs');
const path = require('node:path');
const cp = require('node:child_process');
const crypto = require('node:crypto');
const assert = require('node:assert/strict');
const repo = path.resolve(__dirname, '..');
const output = path.resolve(process.argv[2] || '.');
const relative = path.relative(repo, output);
assert.equal(process.platform, 'win32', 'These existing fixtures require Windows Edge');
assert.ok(path.isAbsolute(relative) || relative.startsWith('..' + path.sep), 'Use an output directory outside the checkout');
assert.ok(fs.statSync(output).isDirectory() && fs.readdirSync(output).length === 0, 'Output must be an empty owned directory');
const git = args => cp.execFileSync('git', args, { cwd: repo, encoding: 'utf8' }).trim();
const hash = bytes => crypto.createHash('sha256').update(bytes).digest('hex');
const fileHash = file => hash(fs.readFileSync(file));
const names = git(['ls-files', '-z']).split('\0').filter(Boolean).sort();
const sources = () => names.map(name => ({ path: name, sha256: fileHash(path.join(repo, name)) }));
const before = sources();
const commit = git(['rev-parse', 'HEAD']);
assert.equal(git(['status', '--porcelain']), '', 'Commit the candidate before recording acceptance');
const privateRoot = path.join(output, 'private');
const screened = path.join(output, 'screened');
fs.mkdirSync(privateRoot);
fs.mkdirSync(screened);
const evidence = {
  format: 'myprobe.operations-main-browser.v1',
  source_commit: commit, source_clean: true, source_files: before,
  platform: process.platform, arch: process.arch, node: process.version,
  go: cp.execFileSync('go', ['version'], { encoding: 'utf8' }).trim(),
  playwright: require(path.join(process.env.PLAYWRIGHT_MODULE || 'playwright', 'package.json')).version,
  started_at: new Date().toISOString(), cases: [], screenshots: [], passed: false,
  scope: 'Synthetic actual-main/embedded-UI checks only. The Go helpers build and start their disposable Server; its executable digest is not captured. No real provider, graceful shutdown, capacity, 24-hour or production assertion.'
};
const profiles = [
  { name: 'diagnostics', test: 'TestLiveDiagnosticExportMainBrowser', folder: 'myprobe-diagnostic-export-live-ui', suffix: '-content' },
  { name: 'backups', test: 'TestLiveScheduledBackupMainBrowser', folder: 'myprobe-backups-live-ui', suffix: '-tasks' }
];
async function main() {
  const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
  const browser = await chromium.launch({ channel: 'msedge', headless: true });
  try { evidence.browser = browser.version(); } finally { await browser.close(); }
  for (const profile of profiles) {
    const scratch = path.join(privateRoot, profile.name);
    fs.mkdirSync(scratch);
    const start = Date.now();
    const result = cp.spawnSync('go', ['test', './cmd/server', '-run', '^' + profile.test + '$', '-count=1', '-timeout=5m', '-json'], {
      cwd: repo, encoding: 'utf8', timeout: 6 * 60 * 1000, maxBuffer: 8 * 1024 * 1024,
      env: { ...process.env, TEMP: scratch, TMP: scratch, TMPDIR: scratch,
        CGO_ENABLED: '0', MYPROBE_RELEASE_CHECKS_AUTO: 'false',
        MYPROBE_TEST_DIAGNOSTIC_EXPORT: '1', MYPROBE_TEST_SCHEDULED_BACKUPS: '1' }
    });
    const stdout = result.stdout || '', stderr = result.stderr || '';
    fs.writeFileSync(path.join(scratch, 'events.jsonl'), stdout, { flag: 'wx' });
    fs.writeFileSync(path.join(scratch, 'stderr.log'), stderr, { flag: 'wx' });
    const events = stdout.split(/\r?\n/).filter(Boolean).map(line => JSON.parse(line));
    const actions = events.filter(event => event.Test === profile.test).map(event => event.Action);
    const record = { name: profile.test, exit_code: result.status, signal: result.signal,
      elapsed_seconds: (Date.now() - start) / 1000,
      events_sha256: hash(stdout), stderr_sha256: hash(stderr),
      run: actions.filter(action => action === 'run').length,
      pass: actions.filter(action => action === 'pass').length,
      skip: events.filter(event => event.Action === 'skip').length,
      fail: events.filter(event => event.Action === 'fail').length };
    evidence.cases.push(record);
    assert.equal(result.error, undefined, profile.name + ' process error');
    assert.equal(record.exit_code, 0, profile.name + ': inspect private Go output');
    assert.equal(record.run, 1);
    assert.equal(record.pass, 1);
    assert.equal(record.skip, 0);
    assert.equal(record.fail, 0);
    assert.ok(events.some(event => event.Action === 'pass' && !event.Test), 'Package did not pass');
    const imageRoot = path.join(scratch, profile.folder);
    const expected = [];
    for (const theme of ['light', 'dark']) for (const width of [360, 768, 1440]) {
      for (const suffix of ['', profile.suffix]) expected.push(`${theme}-${width}${suffix}.png`);
    }
    assert.deepEqual(fs.readdirSync(imageRoot).sort(), expected.sort(), 'Every theme/viewport/state must have a fresh PNG');
    fs.mkdirSync(path.join(screened, profile.name));
    for (const name of expected) {
      const file = path.join(imageRoot, name), bytes = fs.readFileSync(file);
      assert.ok(fs.lstatSync(file).isFile() && !fs.lstatSync(file).isSymbolicLink());
      assert.equal(bytes.subarray(0, 8).toString('hex'), '89504e470d0a1a0a');
      const destination = path.join(screened, profile.name, name);
      fs.copyFileSync(file, destination, fs.constants.COPYFILE_EXCL);
      assert.equal(fileHash(destination), hash(bytes));
      evidence.screenshots.push({ path: `${profile.name}/${name}`, bytes: bytes.length, sha256: hash(bytes) });
    }
    console.log(JSON.stringify(record));
  }
  assert.deepEqual(sources(), before, 'Source changed during execution');
  assert.equal(git(['rev-parse', 'HEAD']), commit);
  assert.equal(git(['status', '--porcelain']), '');
  evidence.sources_unchanged = true;
  evidence.passed = true;
}
main().catch(error => { evidence.error = error.message; process.exitCode = 1; }).finally(() => {
  evidence.finished_at = new Date().toISOString();
  fs.writeFileSync(path.join(screened, 'result.json'), JSON.stringify(evidence, null, 2) + '\n', { flag: 'wx' });
  console.log(JSON.stringify({ passed: evidence.passed, cases: evidence.cases.length, screenshots: evidence.screenshots.length }));
});
