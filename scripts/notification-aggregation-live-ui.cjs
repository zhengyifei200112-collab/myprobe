// Real embedded UI, session/CSRF API, SQLite, evaluator and loopback HTTP sender.
// The fixture queues only after the policy editor selects 5 seconds, then uses
// wall-clock deadlines/backoff. OAuth return navigation is simulated locally;
// provider authorization remains covered separately by the auth suite.
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises'), path = require('node:path');
(async () => {
 const browser = await chromium.launch({channel:'msedge',headless:true});
 try {
  const context = await browser.newContext({baseURL:process.env.AGG_BROWSER_URL,timezoneId:'Asia/Shanghai'});
  const login = await context.request.post('/api/v1/auth/login',{data:{username:'browser-admin',password:process.env.AGG_BROWSER_PASSWORD}});assert.equal(login.status(),200);
  const read = async url => {const r=await context.request.get(url);assert.equal(r.status(),200,url);return r.json();};
  const until = async action => {const deadline=Date.now()+50000;do {const value=await action();if(value)return value;await new Promise(r=>setTimeout(r,150));}while(Date.now()<deadline);throw Error('runtime timeout');};
  const page=await context.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.goto('/admin');await page.getByRole('button',{name:'告警',exact:true}).click();await page.getByRole('tab',{name:'范围策略',exact:true}).click();
  await page.getByRole('button',{name:'新建范围策略',exact:true}).click();
  await page.getByLabel('策略名称',{exact:true}).fill('Live aggregation');await page.getByLabel('策略键',{exact:false}).fill('browser.aggregation');
  await page.getByRole('combobox',{name:/^通知通道/}).selectOption(process.env.AGG_BROWSER_CHANNEL);
  await page.getByRole('combobox',{name:'告警类型',exact:true}).selectOption('expiry');await page.getByLabel('提前提醒（天）',{exact:true}).fill('1');
  const windowInput=page.getByRole('textbox',{name:'聚合等待',exact:true});assert.equal(await windowInput.inputValue(),'30');
  await windowInput.fill('4');await page.getByRole('button',{name:'保存范围策略',exact:true}).click();await page.getByText('聚合等待须为 0，或 5 至 300 的整数秒。',{exact:true}).waitFor();
  await windowInput.fill('30');await page.getByRole('button',{name:'保存范围策略',exact:true}).click();await page.getByRole('button',{name:'编辑策略',exact:true}).waitFor();
  let policy=(await read('/api/v1/admin/alert-policies')).policies[0];assert.equal(policy.config.aggregation_seconds,30);
  await page.getByRole('button',{name:'编辑策略',exact:true}).click();assert.equal(await windowInput.inputValue(),'30');
  await windowInput.fill('5');await page.getByRole('button',{name:'保存范围策略',exact:true}).click();await page.getByRole('button',{name:'编辑策略',exact:true}).waitFor();
  const group=await until(async()=>(await read('/api/v1/admin/notification-aggregates')).aggregates[0]);
  const aggregateURL=`/api/v1/admin/notification-aggregates/${group.id}`;
  const unknown=await until(async()=>{const p=await read(aggregateURL);return p.attempts.some(a=>a.outcome==='unknown')&&p;});
  const content=unknown.attempts.find(a=>a.outcome==='unknown').content;assert.equal(content.members.length,2);assert.ok(content.group_path.startsWith(process.env.AGG_BROWSER_URL+'/admin?'));
  const target=new URL(content.group_path).pathname+new URL(content.group_path).search;
  const anonymous=await browser.newContext({baseURL:process.env.AGG_BROWSER_URL,timezoneId:'Asia/Shanghai'}),landing=await anonymous.newPage();landing.on('pageerror',e=>errors.push(e.message));
  let privateReads=0;landing.on('request',r=>{if(/\/api\/v1\/admin\/(incidents|notification-aggregates)/.test(r.url()))privateReads++;});
  await landing.setViewportSize({width:360,height:1000});await landing.goto(target);await landing.getByRole('button',{name:'使用密码登录',exact:true}).waitFor();assert.equal(privateReads,0,'unauthenticated link fetched private content');
  await landing.getByLabel('用户名',{exact:true}).fill('browser-admin');await landing.getByLabel('密码',{exact:true}).fill(process.env.AGG_BROWSER_PASSWORD);await landing.getByRole('button',{name:'使用密码登录',exact:true}).click();
  const details=landing.getByRole('region',{name:'聚合详情',exact:true});await details.getByRole('heading',{name:'通知入口对应的内容及重试',exact:true}).waitFor();
  await details.getByText('外部渠道可能已收到这次通知。重试可能重复通知；该记录不能作为确定未发送的证据。',{exact:true}).waitFor();
  const initialTabBounds=await landing.getByRole('tab',{name:'通知聚合',exact:true}).boundingBox();assert.ok(initialTabBounds.x>=0&&initialTabBounds.x+initialTabBounds.width<=361,'deep-link selected tab initially visible');
  assert.equal(new URL(landing.url()).search,new URL(content.group_path).search);assert.equal(await landing.evaluate(()=>window.aggregateExecuted),undefined);
  const output=await fs.mkdtemp(path.join(require('node:os').tmpdir(),'myprobe-aggregate-live-'));
  const screenshots=async (stage,surface=landing)=>{for(const theme of ['light','dark'])for(const width of [360,768,1440]){await surface.setViewportSize({width,height:1000});await surface.evaluate(t=>{document.documentElement.dataset.theme=t;if(document.activeElement instanceof HTMLElement)document.activeElement.blur();},theme);const active=surface.getByRole('tab',{name:stage==='policy'?'范围策略':'通知聚合',exact:true});await active.focus();const bounds=await active.boundingBox();assert.ok(bounds.x>=0&&bounds.x+bounds.width<=width+1,'selected tab visible');assert.equal(await surface.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1),false,`${stage} overflow ${theme}/${width}`);await surface.screenshot({path:path.join(output,`${stage}-${theme}-${width}.png`),fullPage:true});}};
  await screenshots('unknown');
  await details.getByRole('link',{name:'Aggregate node A · 查看事件',exact:true}).first().click();await landing.getByRole('heading',{name:'Aggregate node A · 事件详情',exact:true}).waitFor();await landing.getByRole('region',{name:'事件投递记录',exact:true}).getByText(/^当前事件状态：故障持续/).waitFor();
  await landing.getByRole('button',{name:'查看此投递的聚合内容',exact:true}).click();await details.getByRole('heading',{name:'每次发送尝试',exact:true}).waitFor();
  await details.getByRole('link',{name:`${content.members.find(m=>m.node_name!=='Aggregate node A').node_name} · 查看事件`,exact:true}).first().click();await landing.getByRole('heading',{name:`${content.members.find(m=>m.node_name!=='Aggregate node A').node_name} · 事件详情`,exact:true}).waitFor();assert.equal(await landing.evaluate(()=>window.aggregateExecuted),undefined);
  const delivered=await until(async()=>{const p=await read(aggregateURL);return p.totals.delivered===2&&p;});assert.equal(delivered.attempts.length,2);assert.equal(delivered.attempts[0].content.snapshot_id,content.snapshot_id);
  await landing.goto(target);await details.getByRole('heading',{name:'第 2 次 · 渠道已确认成功',exact:true}).waitFor();await screenshots('retry');
  assert.equal(await landing.evaluate(()=>window.aggregateExecuted),undefined);
  // Read failure with a retained deep-link identity can be retried.
  await landing.route(`**${aggregateURL}?*`,route=>route.fulfill({status:500,contentType:'application/json',body:JSON.stringify({code:'aggregate_unavailable',error:'unable to read notification aggregate'})}),{times:1});
  await landing.reload();await details.getByRole('alert').waitFor();await details.getByRole('button',{name:'刷新聚合详情',exact:true}).click();await details.getByRole('heading',{name:'第 2 次 · 渠道已确认成功',exact:true}).waitFor();
  await landing.goto(`/admin?aggregate=${group.id}&snapshot=${'a'.repeat(64)}`);await details.getByText('未找到原通知对应的内容，记录可能来自旧环境或已被清理。',{exact:true}).waitFor();assert.equal(await details.getByRole('heading',{name:/第 \d+ 次/}).count(),0,'wrong snapshot silently displayed');
  await landing.goto('/admin?aggregate=missing');await details.getByRole('alert').waitFor();
  for(const invalid of ['/admin?aggregate=bad%2Fid',`/admin?aggregate=${group.id}&aggregate=missing`,`/admin?aggregate=${group.id}&incident=missing`,'/admin?snapshot=bad']) {privateReads=0;await landing.goto(invalid);await landing.getByText('通知入口无效，请从故障事件或聚合记录重新选择。',{exact:true}).waitFor();assert.equal(privateReads,0);}
  await landing.goto('/admin?incident=missing');await landing.getByText('该通知事件不存在或已被清理，请从列表重新选择。',{exact:true}).waitFor();
  const nodeA=content.members.find(m=>m.node_name==='Aggregate node A'),nodeB=content.members.find(m=>m.node_name!=='Aggregate node A');
  let releaseEntrance,entranceStarted;const entranceGate=new Promise(r=>releaseEntrance=r),entranceSeen=new Promise(r=>entranceStarted=r);
  await landing.route(`**/api/v1/admin/incidents/${nodeA.incident_id}`,async route=>{entranceStarted();await entranceGate;await route.continue();},{times:1});
  try {await landing.goto(`/admin?incident=${nodeA.incident_id}`);await entranceSeen;await landing.getByRole('button',{name:`查看 ${nodeB.node_name} 的投递记录`,exact:true}).click();await landing.getByRole('heading',{name:`${nodeB.node_name} · 事件详情`,exact:true}).waitFor();releaseEntrance();await landing.getByText('正在打开通知事件…',{exact:true}).waitFor({state:'hidden'});assert.equal(await landing.getByRole('heading',{name:'Aggregate node A · 事件详情',exact:true}).count(),0,'late entrance replaced explicit selection');}finally{releaseEntrance();}
  // Locally simulate the provider returning to an authenticated session. Only a
  // canonical local target is saved; no arbitrary return URL is accepted.
  const oauth=await browser.newContext({baseURL:process.env.AGG_BROWSER_URL}),oauthPage=await oauth.newPage();oauthPage.on('pageerror',e=>errors.push(e.message));
  await oauthPage.route('**/api/v1/auth/github/status',route=>route.fulfill({contentType:'application/json',body:'{"enabled":true}'}));
  await oauthPage.route('**/api/v1/auth/github/start',async route=>{const r=await oauth.request.post('/api/v1/auth/login',{data:{username:'browser-admin',password:process.env.AGG_BROWSER_PASSWORD}});assert.equal(r.status(),200);await route.fulfill({status:302,headers:{location:'/admin?oauth=github'}});});
  await oauthPage.goto(target);await oauthPage.getByRole('link',{name:'使用 GitHub 登录',exact:false}).click();await oauthPage.getByRole('heading',{name:'通知入口对应的内容及重试',exact:true}).waitFor();assert.equal(new URL(oauthPage.url()).search,new URL(content.group_path).search);assert.equal(await oauthPage.evaluate(()=>sessionStorage.getItem('myprobe-notification-navigation-v1')),null);
  await oauthPage.evaluate(()=>sessionStorage.setItem('myprobe-notification-navigation-v1',JSON.stringify({path:'https://untrusted.example/admin?aggregate=missing',savedAt:Date.now()})));await oauthPage.goto('/admin?oauth=github');await oauthPage.getByRole('button',{name:'＋ 添加节点',exact:true}).waitFor();assert.equal(new URL(oauthPage.url()).pathname,'/admin');assert.equal(new URL(oauthPage.url()).search,'');
  // Editing a policy does not rewrite the existing group's 5-second window.
  await page.getByRole('button',{name:'编辑策略',exact:true}).click();assert.equal(await windowInput.inputValue(),'5');await screenshots('policy',page);await windowInput.fill('0');await page.getByRole('button',{name:'保存范围策略',exact:true}).click();await page.getByRole('button',{name:'编辑策略',exact:true}).waitFor();policy=(await read('/api/v1/admin/alert-policies')).policies[0];assert.equal(policy.config.aggregation_seconds,0);const unchanged=await read(aggregateURL);assert.equal(unchanged.aggregate.ends_at,group.ends_at);assert.equal(unchanged.aggregate.starts_at,group.starts_at);const windowMs=new Date(unchanged.aggregate.ends_at)-new Date(unchanged.aggregate.starts_at);assert.ok(windowMs>=5000&&windowMs<=5001,'window storage rounds upward');
  await landing.goto(`/admin?incident=${nodeA.incident_id}`);const eventDetail=landing.getByRole('region',{name:'事件投递记录',exact:true});await eventDetail.getByText(/^当前事件状态：故障持续/).waitFor();
  await page.getByRole('button',{name:'编辑策略',exact:true}).click();await page.getByLabel('启用此策略配置',{exact:true}).uncheck();await page.getByRole('button',{name:'保存范围策略',exact:true}).click();
  await until(async()=>(await read(`/api/v1/admin/incidents/${nodeA.incident_id}`)).incident.state==='resolved');await eventDetail.getByRole('button',{name:'刷新投递记录',exact:true}).click();await eventDetail.getByText(/^当前事件状态：已恢复或结束/).waitFor();await eventDetail.getByText('结束原因：规则已停用',{exact:true}).waitFor();
  await landing.goto(target);await details.getByRole('heading',{name:'通知入口对应的内容及重试',exact:true}).waitFor();await details.getByText(/渠道已确认成功 · 事件已恢复或结束/).first().waitFor();assert.equal((await read(aggregateURL)).attempts[0].content.snapshot_id,content.snapshot_id,'current closure rewrote old content');
  assert.deepEqual(errors,[]);console.log(`Aggregate configuration, real ambiguous receipt/retry, password and simulated OAuth return, event navigation, read retry, invalid/missing snapshot, escape and six-size/theme checks passed: ${output}`);
 } finally {await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
