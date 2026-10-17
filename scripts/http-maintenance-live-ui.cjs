const { launchFixtureBrowser } = require('./browser-fixture.cjs');
const assert=require('node:assert/strict'),fs=require('node:fs/promises'),path=require('node:path');
(async()=>{
 const browser=await launchFixtureBrowser();
 try{
  const page=await browser.newPage(),errors=[];let privateReads=0;
  page.on('pageerror',e=>errors.push(e.message));page.on('request',r=>{if(r.url().includes('/api/v1/admin/service-monitors'))privateReads++;});
  await page.goto(`${process.env.UI_BASE_URL}/admin`);
  await page.getByRole('button',{name:'使用密码登录',exact:true}).waitFor();assert.equal(privateReads,0);
  await page.getByLabel('用户名',{exact:true}).fill('fixture-admin');await page.getByLabel('密码',{exact:true}).fill(process.env.HTTP_MAINTENANCE_PASSWORD);
  await page.getByRole('button',{name:'使用密码登录',exact:true}).click();
  await page.getByRole('button',{name:'HTTP 服务',exact:true}).click();
  await page.getByText('服务正常',{exact:true}).waitFor();
  await page.getByRole('button',{name:'检测统计',exact:true}).click();
  const panel=page.getByRole('region',{name:'服务检测统计',exact:true});
  async function query(){
   const response=page.waitForResponse(r=>new URL(r.url()).pathname.endsWith('/statistics'));
   await panel.getByRole('button',{name:'查询统计',exact:true}).click();const r=await response;assert.equal(r.status(),200);
   const data=await r.json();assert.match(r.headers()['cache-control'],/no-store/);return data;
  }
  const output=await fs.mkdtemp(path.join(require('node:os').tmpdir(),'myprobe-http-maintenance-'));
  for(const theme of ['light','dark'])for(const width of [360,768,1440]){
   await page.setViewportSize({width,height:1000});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);
   assert.equal(await panel.getByLabel('排除维护窗口',{exact:true}).isChecked(),true);
   const adjusted=await query();assert.equal(adjusted.maintenance_excluded,true);
   assert.ok(adjusted.statistics.raw.expected>0);assert.equal(adjusted.statistics.raw.success,adjusted.statistics.raw.expected);
   assert.equal(adjusted.statistics.maintenance_excluded_counts.expected,adjusted.statistics.raw.expected);
   assert.equal(adjusted.statistics.expected,0);assert.equal(adjusted.statistics.success_rate,null);assert.equal(adjusted.statistics.coverage,null);
   await panel.getByText('全部成熟计划槽均被维护排除',{exact:false}).waitFor();await panel.getByText('无有效样本',{exact:true}).waitFor();
   assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1),false);
   await page.screenshot({path:path.join(output,`${theme}-${width}-excluded.png`),fullPage:true});
   await panel.getByLabel('排除维护窗口',{exact:true}).uncheck();assert.equal(await panel.getByText('无有效样本',{exact:true}).count(),0);
   const raw=await query();assert.equal(raw.maintenance_excluded,false);assert.equal(raw.statistics.success,raw.statistics.expected);
   assert.equal(raw.statistics.success_rate,1);assert.equal(raw.statistics.coverage,1);
   await panel.getByText('尚未排除维护窗口',{exact:false}).waitFor();assert.equal(await panel.getByText('100.00%',{exact:true}).count(),2);
   assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1),false);
   await page.screenshot({path:path.join(output,`${theme}-${width}-raw.png`),fullPage:true});
   await panel.getByLabel('排除维护窗口',{exact:true}).check();assert.equal(await panel.getByText('100.00%',{exact:true}).count(),0);
  }
  assert.deepEqual(errors,[]);console.log(`Actual mature Agent samples, overlapping/canceled maintenance, authenticated API, raw/excluded switching and twelve theme/width views passed: ${output}`);
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
