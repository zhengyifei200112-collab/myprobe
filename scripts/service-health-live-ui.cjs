const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict'), fs = require('node:fs/promises'), path = require('node:path'), os = require('node:os');
(async () => {
 const browser = await chromium.launch({channel:'msedge',headless:true});
 try {
  const phase=process.env.SERVICE_HEALTH_PHASE, label={healthy:'服务正常',degraded:'服务降级',unknown:'服务状态未知'}[phase];
  assert.ok(label); const page=await browser.newPage(),errors=[];let privateReads=0;
  page.on('pageerror',e=>errors.push(e.message));page.on('request',r=>{if(r.url().includes('/api/v1/admin/service-monitors'))privateReads++;});
  await page.goto(process.env.SERVICE_HEALTH_ORIGIN+'/admin');
  await page.getByRole('button',{name:'使用密码登录',exact:true}).waitFor();assert.equal(privateReads,0);
  await page.getByLabel('用户名',{exact:true}).fill('service-admin');await page.getByLabel('密码',{exact:true}).fill(process.env.SERVICE_HEALTH_PASSWORD);
  await page.getByRole('button',{name:'使用密码登录',exact:true}).click();await page.getByRole('button',{name:'HTTP 服务',exact:true}).click();
  const card=page.locator('.service-monitoring .entity-card').filter({hasText:'Lifecycle website'});
  await card.getByText(label,{exact:true}).waitFor();
  await card.getByRole('button',{name:'服务状态',exact:true}).click();
  const panel=page.getByRole('region',{name:'服务当前状态',exact:true});
  await panel.getByText(label,{exact:true}).waitFor();
  await panel.getByText('Service observer',{exact:true}).waitFor();await panel.getByText('Second service observer',{exact:true}).waitFor();
  if(phase!=='healthy')await panel.getByText('Agent 本地策略拒绝',{exact:true}).waitFor();
  const endpoint=process.env.SERVICE_HEALTH_ORIGIN+'/api/v1/admin/service-monitors/'+process.env.SERVICE_HEALTH_ID+'/health';
  const response=await page.request.get(endpoint);assert.equal(response.status(),200);
  const value=await response.json();assert.equal(value.health.state,phase);assert.equal(value.health.policy,'any_required_observer_failure');
  assert.equal(value.health.required_observers,2);assert.equal(value.health.current_coverage,phase==='healthy'?1:.5);
  const output=await fs.mkdtemp(path.join(os.tmpdir(),'myprobe-service-health-'+phase+'-'));
  for(const theme of ['light','dark'])for(const width of [360,768,1440]){
   await page.setViewportSize({width,height:1000});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);
   assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1),false);
   await page.screenshot({path:path.join(output,theme+'-'+width+'.png'),fullPage:true});
  }
  if(phase==='healthy'){
   // Actual elapsed time: no browser clock or synthetic health response.
   await page.waitForTimeout(21000);
   await panel.getByText('状态快照已过期',{exact:true}).waitFor();await card.getByText('状态快照已过期',{exact:true}).waitFor();
   assert.equal(await panel.getByText('服务正常',{exact:true}).count(),0);
   await panel.getByRole('button',{name:'刷新当前状态',exact:true}).click();await panel.getByText('服务正常',{exact:true}).waitFor();
   await page.route('**/api/v1/admin/service-monitors/*/health',r=>r.fulfill({status:503,contentType:'application/json',body:'{"error":"temporary read failure"}'}));
   await panel.getByRole('button',{name:'刷新当前状态',exact:true}).click();await panel.getByRole('alert').waitFor();
   assert.equal(await panel.getByText('服务正常',{exact:true}).count(),0);
   await page.unroute('**/api/v1/admin/service-monitors/*/health');
   await panel.getByRole('button',{name:'刷新当前状态',exact:true}).click();await panel.getByText('服务正常',{exact:true}).waitFor();
   await panel.getByRole('button',{name:'查看服务告警与事件',exact:true}).click();await page.getByRole('region',{name:'服务告警与事件',exact:true}).waitFor();
  }
  assert.deepEqual(errors,[]);console.log('Real Agent '+phase+' current coverage, private login, six theme/width views'+(phase==='healthy'?', actual expiry and read-error recovery':'')+' passed: '+output);
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
