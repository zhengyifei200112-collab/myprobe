const {chromium}=require(process.env.PLAYWRIGHT_MODULE);
const assert=require('node:assert/strict');
const fs=require('node:fs');const path=require('node:path');
const base=process.env.MYPROBE_TEST_URL||'http://127.0.0.1:25777';
if(!/^http:\/\/127\.0\.0\.1:\d+$/.test(base))throw Error('Use an isolated loopback test server');
(async()=>{
 const browser=await chromium.launch({channel:'msedge',headless:true});
 const context=await browser.newContext({viewport:{width:1440,height:1000}});
 const page=await context.newPage();const errors=[];page.on('pageerror',error=>errors.push(error.message));
 const output=process.env.MYPROBE_TEST_OUTPUT||path.join(require('node:os').tmpdir(),'myprobe-order-browser');fs.mkdirSync(output,{recursive:true});
 try{
 await page.goto(base+'/admin');
 await page.getByLabel('密码',{exact:true}).fill(process.env.MYPROBE_TEST_PASSWORD||'local-browser-test-only');
 await page.getByRole('button',{name:'使用密码登录'}).click();await page.getByRole('heading',{name:'节点管理',exact:true}).waitFor();
 const me=await (await context.request.get(base+'/api/v1/auth/me')).json();
 async function api(url,method='GET',data){const res=await context.request.fetch(base+url,{method,data,headers:{'X-CSRF-Token':me.csrf_token}});assert(res.ok(),`${method} ${url} ${res.status()} ${await res.text()}`);return res.status()===204?null:res.json();}
 async function nodes(){return (await api('/api/v1/admin/nodes')).nodes;}
 assert.equal((await nodes()).length,0,'Use a fresh database');
 for(let i=0;i<6;i++)await api('/api/v1/admin/nodes','POST',{name:`Node ${i+1}`,tags:['preserved'],collection_seconds:5,report_seconds:5});
 let initial=await nodes();await api('/api/v1/admin/nodes/'+initial[2].id,'PATCH',{...initial[2],hidden:true});
 initial=await nodes();const initialIDs=initial.map(n=>n.id);
 await page.reload();
 const trigger=page.getByRole('button',{name:'调整节点顺序',exact:true});
 async function show(){await trigger.focus();await page.keyboard.press('Enter');await page.getByRole('dialog',{name:'调整节点顺序',exact:true}).waitFor();}
 const dialog=page.getByRole('dialog',{name:'调整节点顺序',exact:true});
 const ids=()=>dialog.locator('li[data-node-id]').evaluateAll(rows=>rows.map(row=>row.dataset.nodeId));
 await show();
 assert(await dialog.getByRole('button',{name:'上移 Node 1',exact:true}).isDisabled());
 assert(await dialog.getByRole('button',{name:'下移 Node 6',exact:true}).isDisabled());
 assert(await dialog.getByRole('button',{name:'保存顺序',exact:true}).isDisabled());
 const second=dialog.locator(`[data-node-id="${initialIDs[1]}"]`);
 await second.focus();await page.keyboard.press('Alt+ArrowUp');
 assert.equal((await ids())[0],initialIDs[1]);
 assert.equal(await page.evaluate(()=>document.activeElement.dataset.nodeId),initialIDs[1]);
 assert.match(await dialog.getByRole('status').innerText(),/Node 2 已移至第 1 位/);
 await dialog.getByRole('button',{name:'下移 Node 2',exact:true}).focus();await page.keyboard.press('Enter');
 assert.deepEqual(await ids(),initialIDs);
 assert.equal(await page.evaluate(()=>document.activeElement.getAttribute('aria-label')),'下移 Node 2');
 assert(await dialog.getByRole('button',{name:'保存顺序',exact:true}).isDisabled());
 await dialog.getByRole('button',{name:'上移 Node 3',exact:true}).click();
 await dialog.getByRole('button',{name:'取消',exact:true}).click();await dialog.waitFor({state:'hidden'});
 assert.deepEqual((await nodes()).map(n=>n.id),initialIDs);
 await show();await dialog.locator(`[data-node-id="${initialIDs[2]}"]`).focus();await page.keyboard.press('Alt+ArrowUp');
 const desired=await ids();let firstRequest;
 await page.route('**/api/v1/admin/nodes/reorder',async route=>{firstRequest=route.request().postDataJSON();await route.fetch();await route.abort('failed');},{times:1});
 await dialog.getByRole('button',{name:'保存顺序',exact:true}).click();await dialog.getByRole('alert').waitFor();
 assert.deepEqual((await nodes()).map(n=>n.id),desired);
 const replay=page.waitForRequest(r=>r.url().endsWith('/nodes/reorder'));
 await dialog.getByRole('button',{name:'保存顺序',exact:true}).click();
 assert.deepEqual((await replay).postDataJSON(),firstRequest);await dialog.waitFor({state:'hidden'});
 const audit=await api('/api/v1/admin/audit?limit=50');assert.equal(audit.entries.filter(e=>e.action==='reorder').length,1);
 const saved=await nodes();assert(saved.every((n,i)=>n.sort_order===i&&n.tags.includes('preserved')));assert(saved.find(n=>n.id===initialIDs[2]).hidden);
 await page.reload();await show();assert.deepEqual(await ids(),desired);
 await dialog.getByRole('button',{name:'下移 Node 1',exact:true}).click();
 await api('/api/v1/admin/nodes','POST',{name:'Concurrent node',tags:['other'],collection_seconds:5,report_seconds:5});
 const beforeConflict=await nodes();
 await dialog.getByRole('button',{name:'保存顺序',exact:true}).click();await dialog.getByRole('alert').filter({hasText:'节点或排序已变化'}).waitFor();
 assert.deepEqual((await nodes()).map(n=>[n.id,n.sort_order]),beforeConflict.map(n=>[n.id,n.sort_order]));
 await dialog.getByRole('button',{name:'重新载入',exact:true}).click();await dialog.getByRole('status').filter({hasText:'已载入最新顺序'}).waitFor();assert.equal((await ids()).length,7);
 assert(await dialog.evaluate(el=>el.contains(document.activeElement)), 'reload must retain modal focus');
 assert(await dialog.getByRole('button',{name:'保存顺序',exact:true}).isDisabled());
 for(const theme of ['light','dark'])for(const width of [360,768,1440]){
  await page.emulateMedia({colorScheme:theme});await page.setViewportSize({width,height:1000});await page.evaluate(theme=>{document.documentElement.dataset.theme=theme;document.documentElement.style.colorScheme=theme;},theme);
  assert(await dialog.evaluate(el=>el.getBoundingClientRect().left>=0&&el.getBoundingClientRect().right<=innerWidth+1),`${theme}/${width}: dialog overflow`);
  await page.evaluate(() => Promise.all(document.getAnimations().map(animation => animation.finished.catch(() => {}))));
  await page.screenshot({path:path.join(output,`${theme}-${width}.png`)});
  assert(await dialog.getByRole('button',{name:'取消',exact:true}).evaluate(el=>el.getBoundingClientRect().bottom<=innerHeight), 'footer should remain reachable in viewport');
 }
 await page.keyboard.press('Escape');await dialog.waitFor({state:'hidden'});
 assert.equal(await page.evaluate(()=>document.activeElement.textContent.trim()),'调整节点顺序');
 assert.deepEqual(errors,[]);
 console.log(JSON.stringify({passed:true,checks:['keyboard moves and focus','boundary controls','cancel/no-op','atomic save','lost-response retry','single audit','metadata preserved','reload persistence','concurrent addition conflict','reload recovery','dialog focus restoration'],screenshots:output},null,2));
 }finally{await browser.close();}
})().catch(error=>{console.error(error);process.exit(1)});
