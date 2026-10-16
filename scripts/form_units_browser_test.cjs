// Synthetic fixtures on a disposable loopback Server; never sends notifications.
const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict'), fs=require('node:fs/promises'), path=require('node:path');
let csrf='';
(async()=>{
 const base=process.env.MYPROBE_QA_URL||'http://127.0.0.1:25776', output=process.env.QA_OUTPUT_DIR;
 assert.ok(['127.0.0.1','localhost','[::1]'].includes(new URL(base).hostname),'Disposable loopback Server required');
 assert.ok(process.env.MYPROBE_QA_PASSWORD&&output,'Synthetic password and output directory required');
 const channel=process.env.BROWSER_CHANNEL||(process.platform==='win32'?'msedge':undefined);
 const browser=await chromium.launch({...channel?{channel}:{},headless:true});
 const context=await browser.newContext({baseURL:base,timezoneId:'America/New_York'}), page=await context.newPage();
 const errors=[],apiErrors=[],screenshots=[]; let node,channelID,ruleID,policyID,policyRevision;
 page.on('pageerror',e=>errors.push(e.message));
 page.on('response',r=>{if(r.url().startsWith(base+'/api/')&&r.status()>=400)apiErrors.push(r.status())});
 const headers=()=>({'X-CSRF-Token':csrf});
 async function json(r,name){assert.ok(r.ok(),`${name} HTTP ${r.status()}`);return r.json()}
 const get=async p=>json(await context.request.get(p),'read fixture');
 const readNode=async()=>(await get('/api/v1/admin/nodes')).nodes.find(n=>n.id===node.id);
 async function quantity(label,factor,value){await page.getByLabel(label+'单位',{exact:true}).selectOption(String(factor));await page.getByLabel(label,{exact:true}).fill(value)}
 async function views(kind,locator){for(const theme of ['light','dark'])for(const width of [360,768,1440]){
  await page.setViewportSize({width,height:1000});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);await locator.scrollIntoViewIfNeeded();await page.evaluate(()=>new Promise(requestAnimationFrame));
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1),false,`${kind} overflow ${theme}/${width}`);
  if(await page.locator('.edit-dialog').count()){assert.equal(await page.locator('.edit-dialog').evaluate(e=>e.scrollWidth>e.clientWidth+1),false,'dialog overflow');assert.equal(await page.locator('.edit-dialog').evaluate(e=>{const r=e.getBoundingClientRect();return r.top>=-1&&r.bottom<=innerHeight+1}),true,'dialog remains in viewport');}
  const name=`${kind}-${theme}-${width}.png`;await page.screenshot({path:path.join(output,name),fullPage:true});screenshots.push(name);
 }}
 async function saveNode(){await page.getByRole('button',{name:'保存节点',exact:true}).click();await page.waitForSelector('.edit-dialog',{state:'hidden'})}
 try{
  csrf=(await json(await context.request.post('/api/v1/auth/login',{data:{username:'admin',password:process.env.MYPROBE_QA_PASSWORD}}),'login')).csrf_token;assert.ok(csrf);
  const name='Units QA '+Date.now();
  ({node}=await json(await context.request.post('/api/v1/admin/nodes',{headers:headers(),data:{name,tags:[],country_code:'US',collection_seconds:5,report_seconds:5}}),'create node'));
  const expiry='2030-11-03T06:30:56Z';
  await json(await context.request.patch('/api/v1/admin/nodes/'+node.id,{headers:headers(),data:{...node,currency:'USD',price_minor:499,billing_cycle:'legacy custom',expires_at:expiry,latency_mode:'ping'}}),'seed metadata');
  channelID=(await json(await context.request.post('/api/v1/admin/notification-channels',{headers:headers(),data:{name:'Synthetic units channel',kind:'webhook',config:{url:'http://127.0.0.1:9',method:'POST'}}}),'create channel')).channel.id;
  await page.goto('/admin');
  const editNode=()=>page.locator('.admin-node-card').filter({hasText:name}).getByRole('button',{name:'编辑',exact:true}).click();
  await editNode();assert.equal(await page.getByLabel('价格（金额）').inputValue(),'4.99');assert.equal(await page.getByLabel('自定义周期',{exact:true}).inputValue(),'legacy custom');await saveNode();
  let saved=await readNode();assert.equal(saved.price_minor,499);assert.equal(saved.expires_at,expiry);assert.equal(saved.billing_cycle,'legacy custom');
  for(const [currency,amount,minor]of[['JPY','499',499],['KWD','4.999',4999],['USD','0',0],['USD','4.99',499]]){
   await editNode();await page.getByLabel('货币',{exact:true}).fill(currency);await page.getByLabel('价格（金额）').fill(amount);await saveNode();saved=await readNode();assert.equal(saved.price_minor,minor);assert.equal(saved.currency,currency);assert.equal(saved.expires_at,expiry);
  }
  saved=await readNode();await json(await context.request.patch('/api/v1/admin/nodes/'+node.id,{headers:headers(),data:{...saved,currency:''}}),'seed legacy missing currency');
  await page.reload();await editNode();assert.equal(await page.getByLabel('价格（原始最小单位）').inputValue(),'499');await saveNode();
  saved=await readNode();assert.equal(saved.currency,'');assert.equal(saved.price_minor,499);
  await editNode();await page.getByLabel('货币',{exact:true}).fill('USD');assert.equal(await page.getByLabel('价格（金额）').inputValue(),'4.99');await saveNode();
  await editNode();await page.getByLabel('货币',{exact:true}).fill('JPY');
  assert.equal(await page.getByLabel('价格（金额）').evaluate(e=>e.checkValidity()),false);
  await page.getByRole('alert').filter({hasText:'精度超出最小单位，请调整数值。'}).waitFor();
  assert.equal(await page.getByRole('button',{name:'保存节点',exact:true}).isDisabled(),true);
  saved=await readNode();assert.equal(saved.currency,'USD');assert.equal(saved.price_minor,499);
  await page.getByLabel('货币',{exact:true}).fill('USD');await saveNode();
  await editNode();await views('node',page.getByLabel('价格（金额）'));await page.getByRole('button',{name:'取消',exact:true}).last().click();
  await page.getByRole('button',{name:'告警',exact:true}).click();await page.getByRole('tab',{name:'告警规则',exact:true}).click();
  await page.getByRole('combobox',{name:/^节点/}).selectOption(node.id);await page.getByRole('combobox',{name:/^通知通道/}).selectOption(channelID);await page.getByRole('combobox',{name:/^事件类型/}).selectOption('bandwidth');
  await quantity('阈值',125000,'1.5');await quantity('持续时间（0 为立即）',60,'1.5');await page.getByLabel('恢复窗口与触发持续时间相同',{exact:true}).uncheck();await quantity('恢复持续时间',60,'0.5');await quantity('重复提醒',60,'0.5');await quantity('冷却时间',60,'1.5');
  await page.getByRole('button',{name:'创建规则',exact:true}).click();await page.locator('.rule-list').getByRole('button',{name:'编辑',exact:true}).waitFor();
  const rule=(await get('/api/v1/admin/alert-rules')).rules.find(r=>r.node_id===node.id&&r.kind==='bandwidth');assert.ok(rule);ruleID=rule.id;
  assert.equal(rule.config.threshold_bytes_per_second,187500);assert.equal(rule.config.duration_seconds,90);assert.equal(rule.config.recovery_seconds,30);assert.equal(rule.config.repeat_seconds,30);assert.equal(rule.cooldown_seconds,90);
  await page.locator('.rule-list').getByRole('button',{name:'编辑',exact:true}).click();await page.getByLabel('阈值单位',{exact:true}).selectOption('125000');assert.equal(await page.getByLabel('阈值',{exact:true}).inputValue(),'1.5');
  await page.getByLabel('阈值',{exact:true}).fill('9007199254740992');assert.equal(await page.getByLabel('阈值',{exact:true}).evaluate(e=>e.checkValidity()),false);await page.getByLabel('阈值',{exact:true}).fill('1.5');await views('rule',page.getByLabel('阈值',{exact:true}));
  await page.getByRole('tab',{name:'范围策略',exact:true}).click();await page.getByRole('button',{name:'新建范围策略',exact:true}).click();
  await page.getByLabel('策略名称',{exact:true}).fill('Units policy');await page.getByLabel('策略键',{exact:false}).fill('units.bandwidth');await page.getByRole('combobox',{name:/^通知通道/}).selectOption(channelID);await page.getByRole('combobox',{name:/^告警类型/}).selectOption('bandwidth');
  await quantity('带宽阈值',125000,'1.5');await quantity('持续时间',60,'1.5');await page.getByLabel('恢复持续时间与触发一致',{exact:true}).uncheck();await quantity('恢复持续时间',60,'0.5');await page.getByLabel('重复提醒（0 使用冷却间隔）',{exact:true}).fill('0');await quantity('聚合等待',60,'0.5');await quantity('冷却间隔',60,'1.5');
  await page.getByRole('button',{name:'保存范围策略',exact:true}).click();await page.getByRole('button',{name:'编辑策略',exact:true}).waitFor();
  const policy=(await get('/api/v1/admin/alert-policies')).policies.find(p=>p.policy_key==='units.bandwidth');assert.ok(policy);policyID=policy.id;policyRevision=policy.revision;
  assert.equal(policy.config.threshold_bytes_per_second,187500);assert.equal(policy.config.duration_seconds,90);assert.equal(policy.config.recovery_seconds,30);assert.equal(policy.config.repeat_seconds??0,0);assert.equal(policy.config.aggregation_seconds,30);assert.equal(policy.cooldown_seconds,90);
  await page.getByRole('button',{name:'编辑策略',exact:true}).click();assert.equal(await page.getByLabel('恢复持续时间',{exact:true}).inputValue(),'30');assert.equal(await page.getByLabel('聚合等待',{exact:true}).inputValue(),'30');
  await page.getByLabel('持续时间',{exact:true}).fill('61');await page.getByLabel('持续时间单位',{exact:true}).selectOption('60');assert.equal(await page.getByLabel('持续时间单位',{exact:true}).inputValue(),'1');await page.getByText('此数值无法用所选单位精确表示，请保留原单位。',{exact:true}).waitFor();await page.getByLabel('持续时间',{exact:true}).fill('90');
  await views('policy',page.getByLabel('带宽阈值',{exact:true}));await page.getByRole('button',{name:'保存范围策略',exact:true}).click();await page.getByRole('button',{name:'编辑策略',exact:true}).waitFor();
  const roundtrip=(await get('/api/v1/admin/alert-policies/'+policyID)).policy;policyRevision=roundtrip.revision;assert.deepEqual(roundtrip.config,policy.config);assert.equal(roundtrip.cooldown_seconds,policy.cooldown_seconds);
  saved=await readNode();await json(await context.request.patch('/api/v1/admin/nodes/'+node.id,{headers:headers(),data:{...saved,price_minor:Number.MAX_SAFE_INTEGER}}),'seed boundary price');await page.goto('/');await page.getByRole('button',{name:'切换到详情显示模式',exact:true}).click();await page.getByText(/90071992547409\.91/).waitFor();
  assert.deepEqual(errors,[]);assert.deepEqual(apiErrors,[]);
  await fs.writeFile(path.join(output,'result.json'),JSON.stringify({format:'myprobe.form-units-browser.v1',passed:true,platform:process.platform,browser:await browser.version(),browser_timezone:'America/New_York',screenshots,checks:['exact legacy amount/cycle/expiry roundtrip','JPY/USD/KWD and zero persistence','legacy byte/time/recovery persistence','policy byte/time/recovery/aggregation persistence','unsafe byte integer refusal','nonterminating unit switch preserves seconds','unchanged policy roundtrip','safe integer public currency precision','eighteen viewport/theme views','no page/API errors']},null,2),{flag:'wx',mode:0o600});
  console.log('PASS: actual Server quantity persistence and eighteen responsive views');
 }catch(error){await page.screenshot({path:path.join(output,'failure.png'),fullPage:true}).catch(()=>{});throw error;}finally{
  if(policyID)await context.request.delete('/api/v1/admin/alert-policies/'+policyID+'?revision='+policyRevision,{headers:headers()}).catch(()=>{});
  if(ruleID)await context.request.delete('/api/v1/admin/alert-rules/'+ruleID,{headers:headers()}).catch(()=>{});
  if(node)await context.request.delete('/api/v1/admin/nodes/'+node.id,{headers:headers()}).catch(()=>{});
  if(channelID)await context.request.delete('/api/v1/admin/notification-channels/'+channelID,{headers:headers()}).catch(()=>{});
  await browser.close();
 }
})().catch(e=>{let message=e.message.replaceAll(process.env.MYPROBE_QA_PASSWORD||'\0','[synthetic-password]');if(csrf)message=message.replaceAll(csrf,'[csrf]');console.error(message);process.exitCode=1});
