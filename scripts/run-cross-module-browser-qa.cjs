// Synthetic lifecycle checks only. Keep databases/logs private; export PNGs and result.json.
const fs = require('node:fs'), path = require('node:path'), cp = require('node:child_process');
const crypto = require('node:crypto'), assert = require('node:assert/strict');
const { launchFixtureBrowser } = require('./browser-fixture.cjs');
const repo = path.resolve(__dirname, '..');
const [outputArg, adminArg, servicesArg] = process.argv.slice(2);
assert.ok(outputArg && adminArg, 'Usage: node script.cjs <empty output> <httpapi.test> [agentclient.test]');
const output = path.resolve(outputArg), admin = path.resolve(adminArg);
const services = servicesArg ? path.resolve(servicesArg) : undefined;
assert.ok(fs.statSync(output).isDirectory() && fs.readdirSync(output).length === 0, 'Output must be an empty owned directory');
assert.ok(fs.statSync(admin).isFile() && (!services || fs.statSync(services).isFile()), 'Native test executables required');
if (services) {
  assert.equal(process.platform, 'linux', 'Private target checks require an isolated Linux network namespace');
  assert.notEqual(fs.readlinkSync('/proc/self/ns/net'), fs.readlinkSync('/proc/1/ns/net'), 'Refuse the host network namespace');
  assert.deepEqual(Object.keys(require('node:os').networkInterfaces()), ['lo'], 'Only the private loopback interface is allowed');
}
const sha = data => crypto.createHash('sha256').update(data).digest('hex');
const fileHash = file => sha(fs.readFileSync(file));
const git = args => cp.execFileSync('git', args, {cwd:repo, encoding:'utf8'}).trim();
const sourceNames = [...new Set([...git(['ls-files','-z','--','*.go','go.mod','go.sum','scripts/*.cjs','scripts/*.sh','internal/webui/dist','web/package-lock.json','.github/workflows/ci.yml']).split('\0'),
  'scripts/browser-fixture.cjs','scripts/run-cross-module-browser-qa.cjs','scripts/run-cross-module-browser-qa.sh'])].filter(Boolean).sort();
const sources = () => sourceNames.map(name => ({path:name, sha256:fileHash(path.join(repo,name))}));
const before = sources(), commit = git(['rev-parse','HEAD']);
const clean = git(['status','--porcelain']).length === 0;
const scratch = path.join(output,'private');
fs.mkdirSync(scratch,{mode:0o700});
const expectedAdmin = ['TestLivePolicyBrowserLifecycle','TestLiveMaintenanceBrowserLifecycle','TestLiveIncidentAcknowledgementBrowser','TestLiveIncidentSilenceBrowser','TestLiveNotificationAggregationBrowser','TestLiveSingleIncidentLinkBrowser'];
const expectedServices = ['TestHTTPMaintenanceStatisticsThroughRealAgent','TestServiceIncidentThroughRealAgent'];
const evidence = {format:'myprobe.cross-module-browser.v1', source_commit:commit, source_clean:clean,
  node:process.version, platform:process.platform, arch:process.arch, source_files:before,
  executables:{httpapi:fileHash(admin),...(services?{agentclient:fileHash(services)}:{})},
  private_network_namespace:!!services, packages:[], passed:false,
  scope:'Synthetic embedded UI/API/SQLite/evaluator/worker/receiver lifecycles; service profile uses real Client.Run and production private-target policy in an isolated namespace. No production data, external OAuth/Provider, full main-process capacity or 24-hour assertion.'};
async function main() {
  const browser = await launchFixtureBrowser();
  try { evidence.browser = browser.version(); } finally { await browser.close(); }
  const jobs = [{name:'httpapi',binary:admin,tests:expectedAdmin},...(services?[{name:'agentclient',binary:services,tests:expectedServices}]:[])];
  for (const job of jobs) {
    const env = {...process.env, TMPDIR:scratch, TMP:scratch, TEMP:scratch,
      MYPROBE_TEST_POLICY_BROWSER:'1', MYPROBE_TEST_MAINTENANCE_BROWSER:'1', MYPROBE_TEST_ACK_BROWSER:'1',
      MYPROBE_TEST_SILENCE_BROWSER:'1', MYPROBE_TEST_AGGREGATION_BROWSER:'1',
      MYPROBE_TEST_HTTP_BROWSER:services?'1':'0', MYPROBE_TEST_PRIVATE_HTTP:services?'1':'0'};
    const args = [`-test.run=^(${job.tests.join('|')})$`,'-test.count=1','-test.timeout=10m','-test.v'];
    const start = Date.now();
    const result = cp.spawnSync(job.binary,args,{cwd:path.join(repo,'internal',job.name),env,encoding:'utf8',timeout:11*60*1000,maxBuffer:8*1024*1024});
    const log = (result.stdout || '') + (result.stderr || '');
    fs.writeFileSync(path.join(scratch,job.name+'.log'),log,{mode:0o600,flag:'wx'});
    const assertions = job.tests.map(name => ({name, passed:log.includes(`--- PASS: ${name} (`), skipped:log.includes(`--- SKIP: ${name} (`)}));
    const record = {name:job.name, exit_code:result.status, signal:result.signal, elapsed_seconds:(Date.now()-start)/1000,
      log_sha256:sha(log), tests:assertions, passed:result.status===0 && assertions.every(t=>t.passed && !t.skipped)};
    evidence.packages.push(record);
    console.log(JSON.stringify(record));
    if (!record.passed) throw new Error(job.name+' lifecycle acceptance failed; inspect the private fixture log');
  }
  assert.deepEqual(sources(),before,'Source changed during execution');
  assert.equal(git(['rev-parse','HEAD']),commit,'Commit changed during execution');
  assert.equal(git(['status','--porcelain']).length===0,clean,'Worktree status changed during execution');
  const screenshots = [];
  function collect(folder) {
    for(const item of fs.readdirSync(folder,{withFileTypes:true})) {
      const file = path.join(folder,item.name);
      if(item.isDirectory()) collect(file);
      else if(item.isFile() && item.name.endsWith('.png')) screenshots.push({path:path.relative(scratch,file).split(path.sep).join('/'),sha256:fileHash(file),bytes:fs.statSync(file).size});
    }
  }
  collect(scratch);
  assert.ok(screenshots.length>=6,'Lifecycle screenshots were not produced');
  const imageRoot = path.join(output,'screenshots');
  fs.mkdirSync(imageRoot,{mode:0o700});
  for(const item of screenshots) {
    const destination = path.join(imageRoot,item.path);
    fs.mkdirSync(path.dirname(destination),{recursive:true,mode:0o700});
    fs.copyFileSync(path.join(scratch,item.path),destination,fs.constants.COPYFILE_EXCL);
  }
  evidence.screenshots = screenshots;
  evidence.executables_unchanged = fileHash(admin)===evidence.executables.httpapi && (!services || fileHash(services)===evidence.executables.agentclient);
  assert.ok(evidence.executables_unchanged,'A test executable changed');
  evidence.passed = true;
}
main().catch(error=>{evidence.error=error.message;process.exitCode=1;}).finally(()=>{
  evidence.finished_at = new Date().toISOString();
  fs.writeFileSync(path.join(output,'result.json'),JSON.stringify(evidence,null,2)+'\n',{mode:0o600,flag:'wx'});
  console.log(JSON.stringify({passed:evidence.passed,source_commit:commit,source_clean:clean,screenshots:evidence.screenshots?.length||0,result:path.join(output,'result.json')}));
});
