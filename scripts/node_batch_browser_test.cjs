const {chromium}=require(process.env.PLAYWRIGHT_MODULE);
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const base=process.env.MYPROBE_TEST_URL||'http://127.0.0.1:25776';
if(!/^http:\/\/127\.0\.0\.1:\d+$/.test(base)) throw Error('Use an isolated local test server');
(async()=>{
const browser=await chromium.launch({channel:'msedge',headless:true});
const context=await browser.newContext({viewport:{width:1440,height:1000}});
const page=await context.newPage();
const errors=[];page.on('pageerror',e=>errors.push(e.message));
const output=process.env.MYPROBE_TEST_OUTPUT||path.join(require('node:os').tmpdir(),'myprobe-batch-browser');fs.mkdirSync(output,{recursive:true});
try{
 await page.goto(base+'/admin');
 await page.getByLabel('密码',{exact:true}).fill(process.env.MYPROBE_TEST_PASSWORD||'local-browser-test-only');
 await page.getByRole('button',{name:'使用密码登录'}).click();
 await page.getByRole('heading',{name:'节点管理',exact:true}).waitFor();
 const me=await (await context.request.get(base+'/api/v1/auth/me')).json();
 async function api(url,method='GET',data){
  const response=await context.request.fetch(base+url,{method,data,headers:{'X-CSRF-Token':me.csrf_token}});
  assert(response.ok(),`${method} ${url}: ${response.status()} ${await response.text()}`);
  return response.status()===204?null:response.json();
 }
 const existing=await api('/api/v1/admin/nodes');assert.equal(existing.nodes.length,0,'Use a fresh disposable database');
 const ids=[];
 for(let i=0;i<25;i++)ids.push((await api('/api/v1/admin/nodes','POST',{name:`Batch ${String(i+1).padStart(2,'0')}`,tags:['test'],collection_seconds:5,report_seconds:5})).node.id);
 const targets=[];
 for(let i=0;i<2;i++)targets.push((await api('/api/v1/admin/targets','POST',{name:`Target ${i+1}`,kind:'tcping',host:'example.test',port:443,interval_seconds:60,timeout_ms:1000,enabled:false,sort_order:i})).target.id);
 await page.reload();
 const batch=page.locator('.batch-manager');
 await batch.locator('summary').click();
 await batch.getByRole('button',{name:'选择本页',exact:true}).click();
 await batch.getByRole('button',{name:'下一页',exact:true}).click();
 await batch.getByRole('button',{name:'选择本页',exact:true}).click();
 assert.match(await batch.innerText(),/已选 25 个/);
 await batch.getByLabel('添加标签',{exact:true}).fill('fleet');
 async function prepare(){await batch.getByRole('button',{name:'预览批量变更',exact:true}).click();await batch.getByRole('region',{name:'批量变更预览'}).waitFor();}
 async function apply(){await batch.getByRole('button',{name:/确认执行/}).click();await page.getByText(/批量执行完成：/).waitFor();}
 await prepare();
 await batch.getByLabel('添加标签',{exact:true}).fill('fleet, ready');
 assert.equal(await batch.getByRole('region',{name:'批量变更预览'}).count(),0,'input change must invalidate preview');
 await prepare();await apply();
 assert((await api('/api/v1/admin/nodes')).nodes.every(n=>n.tags.includes('fleet')&&n.tags.includes('ready')));
 // No-op preview accurately explains already applied labels.
 await prepare();assert.equal(await batch.locator('.batch-preview li').filter({hasText:'无变化'}).count(),25);await apply();
 // Concurrent edit invalidates the entire batch.
 await batch.getByLabel('添加标签',{exact:true}).fill('should-not-apply');await prepare();
 const other=await api('/api/v1/admin/nodes/batch/preview','POST',{node_ids:[ids[0]],add_tags:['concurrent']});
 await api('/api/v1/admin/nodes/batch/apply','POST',{preview_id:other.id,idempotency_key:'browser-concurrent-key'});
 await batch.getByRole('button',{name:/确认执行/}).click();
 await batch.getByRole('alert').filter({hasText:'请重新预览整批变更'}).waitFor();
 assert((await api('/api/v1/admin/nodes')).nodes.every(n=>!n.tags.includes('should-not-apply')));
 await batch.getByLabel('批量操作类型').selectOption('targets');
 assert.equal(await batch.getByLabel('目标分配方式').inputValue(),'add');
 await batch.getByLabel('Target 1',{exact:true}).check();await prepare();await apply();
 await batch.getByLabel('Target 1',{exact:true}).uncheck();await batch.getByLabel('Target 2',{exact:true}).check();await prepare();await apply();
 assert.equal((await api('/api/v1/admin/latency-config')).node_targets.length,50);
 await batch.getByLabel('目标分配方式').selectOption('remove');await prepare();await apply();
 assert.equal((await api('/api/v1/admin/latency-config')).node_targets.length,25);
 await batch.getByLabel('目标分配方式').selectOption('replace');await batch.getByLabel('Target 2',{exact:true}).uncheck();await prepare();await apply();
 assert.equal((await api('/api/v1/admin/latency-config')).node_targets.length,0);
 // Retry after the server commits but the browser loses the response must retain its key.
 await batch.getByLabel('批量操作类型').selectOption('intervals');
 await batch.getByLabel('批量采集间隔（秒）').fill('2');await prepare();
 let firstKey;
 await page.route('**/api/v1/admin/nodes/batch/apply',async route=>{firstKey=route.request().postDataJSON().idempotency_key;await route.fetch();await route.abort('failed');},{times:1});
 await batch.getByRole('button',{name:/确认执行/}).click();await batch.getByRole('alert').waitFor();
 const replay=page.waitForRequest(r=>r.url().endsWith('/nodes/batch/apply'));
 await apply();assert.equal((await replay).postDataJSON().idempotency_key,firstKey);
 assert((await api('/api/v1/admin/nodes')).nodes.every(n=>n.collection_seconds===2&&n.report_seconds===5));
 // Public snapshot must remove hidden nodes without a reload.
 const publicPage=await context.newPage();await publicPage.goto(base);
 await publicPage.getByText('Batch 01',{exact:true}).first().waitFor();
 await batch.getByLabel('批量操作类型').selectOption('visibility');await prepare();await apply();
 await publicPage.getByText('Batch 01',{exact:true}).first().waitFor({state:'hidden'});
 const publicResult=await api('/api/v1/public/nodes');assert.equal(publicResult.nodes.length,0);
 await batch.getByLabel('设置可见性').selectOption('false');await prepare();await apply();
 await publicPage.getByText('Batch 01',{exact:true}).first().waitFor();await publicPage.close();
 // Required viewport/theme evidence; batch form plus preview stay inside the viewport.
 await batch.getByLabel('批量操作类型').selectOption('tags');await batch.getByLabel('添加标签',{exact:true}).fill('visual');await prepare();
 for(const theme of ['light','dark'])for(const width of [360,768,1440]){
  await page.emulateMedia({colorScheme:theme});await page.setViewportSize({width,height:1000});
  await page.evaluate(mode=>{document.documentElement.dataset.theme=mode;document.documentElement.style.colorScheme=mode;},theme);
  await batch.evaluate(el=>el.scrollIntoView({block:'start'}));
  assert(await batch.evaluate(el=>el.getBoundingClientRect().right<=innerWidth+1),`${theme}/${width}: batch overflows viewport`);
  await page.screenshot({path:path.join(output,`${theme}-${width}.png`)});
  await batch.locator('.batch-preview').scrollIntoViewIfNeeded();
  await page.screenshot({path:path.join(output,`${theme}-${width}-preview.png`)});
 }
 assert.deepEqual(errors,[]);
 console.log(JSON.stringify({passed:true,nodes:25,checks:['cross-page selection','preview invalidation','no-op','atomic conflict','target add/remove/replace','retry after committed response loss','interval persistence','live public visibility'],screenshots:output},null,2));
}finally{await browser.close();}
})().catch(e=>{console.error(e);process.exit(1)});
