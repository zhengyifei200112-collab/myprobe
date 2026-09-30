// Run only against an isolated local test server. Set MYPROBE_QA_PASSWORD and optionally PLAYWRIGHT_MODULE.
const {chromium}=require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assert=require('node:assert/strict');
(async()=>{
 const browser=await chromium.launch({channel:process.env.BROWSER_CHANNEL || 'msedge',headless:true});
 const context=await browser.newContext({baseURL:process.env.MYPROBE_QA_URL || 'http://127.0.0.1:25776'});
 const login=await context.request.post('/api/v1/auth/login',{data:{username:'admin',password:process.env.MYPROBE_QA_PASSWORD}});
 const {csrf_token}=await login.json(); assert.ok(csrf_token);
 const headers={'X-CSRF-Token':csrf_token};
 const fixtureName='Units QA '+Date.now();
 const created=await context.request.post('/api/v1/admin/nodes',{headers,data:{name:fixtureName,tags:[],country_code:'US',collection_seconds:5,report_seconds:5}});
 const {node}=await created.json(); assert.ok(node);
 const expiry='2030-01-02T03:04:56Z';
 const patch=await context.request.patch('/api/v1/admin/nodes/'+node.id,{headers,data:{...node,currency:'USD',price_minor:499,billing_cycle:'legacy custom',expires_at:expiry,latency_mode:'ping'}});assert.ok(patch.ok());
 const page=await context.newPage(); page.on('pageerror',e=>console.error('PAGE ERROR', e.message)); page.on('console',m=>{if(m.type()==='error')console.error(m.text())});
 await page.goto('/admin'); await page.locator('.admin-node-card').filter({hasText:fixtureName}).getByRole('button',{name:'编辑',exact:true}).click();
 assert.equal(await page.getByLabel('价格（金额）').inputValue(),'4.99');
 assert.equal(await page.getByLabel('自定义周期').inputValue(),'legacy custom');
 await page.getByRole('button',{name:'保存节点',exact:true}).click();
 await page.waitForSelector('.edit-dialog',{state:'hidden'});
 const nodes=await (await context.request.get('/api/v1/admin/nodes')).json();
 const saved=nodes.nodes.find(n=>n.id===node.id);assert.equal(saved.price_minor,499);assert.equal(saved.expires_at,expiry);assert.equal(saved.billing_cycle,'legacy custom');
 for(const theme of ['light','dark']) for(const width of [360,768,1440]){
  await page.setViewportSize({width,height:1000}); await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);
  await page.locator('.admin-node-card').filter({hasText:fixtureName}).getByRole('button',{name:'编辑',exact:true}).click();
  await page.screenshot({path:(process.env.QA_OUTPUT_DIR || require('node:os').tmpdir())+'/myprobe-forms-'+theme+'-'+width+'.png',fullPage:true});
  assert.ok(await page.getByLabel('价格（金额）').isVisible());
  await page.getByRole('button',{name:'取消',exact:true}).last().click();
 }
 await page.getByRole('button',{name:'告警',exact:true}).click();
 await page.getByRole('tab',{name:'告警规则',exact:true}).click();
 await page.getByLabel('事件类型').selectOption('bandwidth');
 await page.getByLabel('阈值单位',{exact:true}).selectOption('125000');
 await page.getByLabel('阈值',{exact:true}).fill('1.5');
 await page.getByLabel('阈值单位',{exact:true}).selectOption('1');
 assert.equal(await page.getByLabel('阈值',{exact:true}).inputValue(),'187500');
 await page.getByLabel('阈值',{exact:true}).fill('9007199254740992');
 assert.equal(await page.getByLabel('阈值',{exact:true}).evaluate(e=>e.checkValidity()),false);
 for(const theme of ['light','dark']) for(const width of [360,768,1440]){
  await page.setViewportSize({width,height:1000}); await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);
  await page.screenshot({path:(process.env.QA_OUTPUT_DIR || require('node:os').tmpdir())+'/myprobe-alert-units-'+theme+'-'+width+'.png',fullPage:true});
 }
 await context.request.delete('/api/v1/admin/nodes/'+node.id,{headers});
 await browser.close(); console.log('PASS: monetary round-trip, legacy cycle, expiry seconds, units, unsafe input and six viewport/theme combinations');
})().catch(e=>{console.error(e);process.exit(1)});




