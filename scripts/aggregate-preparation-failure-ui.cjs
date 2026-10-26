// Actual Store failure/worker transition and protected embedded UI; zero sends.
const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict'),fs=require('node:fs/promises'),path=require('node:path');
(async()=>{
 const browser=await chromium.launch({channel:'msedge',headless:true});
 try{
  const context=await browser.newContext({baseURL:process.env.AGG_BROWSER_URL});
  const login=await context.request.post('/api/v1/auth/login',{data:{username:'browser-admin',password:process.env.AGG_BROWSER_PASSWORD}});assert.equal(login.status(),200);
  const page=await context.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));await page.setViewportSize({width:360,height:1000});await page.goto(`/admin?aggregate=${process.env.AGG_BROWSER_GROUP}`);
  const detail=page.getByRole('region',{name:'聚合详情',exact:true});await detail.getByText('未发起外部发送：聚合正文无效或超限。请检查配置和数据；本次任务不会自动重试。',{exact:true}).waitFor();
  await detail.getByRole('heading',{name:'第 1 次 · 发送失败',exact:true}).waitFor();await detail.getByText('本次未生成可发送正文。',{exact:true}).waitFor();
  await detail.getByText('本次成员关联',{exact:true}).click();assert.equal(await detail.getByRole('listitem').filter({hasText:'未纳入本次发送内容'}).count(),2);assert.equal(await detail.getByText('结果未知，可能已送达',{exact:false}).count(),0);
  assert.equal((await page.locator('body').textContent()).includes('private-render-marker'),false);
  const output=await fs.mkdtemp(path.join(require('node:os').tmpdir(),'myprobe-aggregate-unsent-'));
  for(const theme of ['light','dark'])for(const width of [360,768,1440]){await page.setViewportSize({width,height:1000});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);await page.getByRole('tab',{name:'通知聚合',exact:true}).focus();assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1),false);await page.screenshot({path:path.join(output,`${theme}-${width}.png`),fullPage:true});}
  assert.deepEqual(errors,[]);console.log(`Known-unsent failure, member authorization, privacy, both themes and three widths passed: ${output}`);
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
