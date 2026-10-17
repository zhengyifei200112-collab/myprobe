const { launchFixtureBrowser } = require('./browser-fixture.cjs');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises'), path = require('node:path');
(async () => {
 const browser = await launchFixtureBrowser();
 try {
  const context = await browser.newContext();
  const page = await context.newPage(), errors=[];
  let privateReads=0;
  page.on('pageerror',e=>errors.push(e.message));
  page.on('request',r=>{if(r.url().includes('/api/v1/admin/alert-incidents/')) privateReads++;});
  await page.goto(process.env.SINGLE_LINK_URL);
  await page.getByRole('button',{name:'使用密码登录',exact:true}).waitFor();
  assert.equal(privateReads,0);
  await page.getByLabel('用户名',{exact:true}).fill('single-admin');
  await page.getByLabel('密码',{exact:true}).fill(process.env.SINGLE_LINK_PASSWORD);
  await page.getByRole('button',{name:'使用密码登录',exact:true}).click();
  await page.getByRole('heading',{name:'Single linked node · 事件详情',exact:true}).waitFor();
  assert.equal(new URL(page.url()).search,new URL(process.env.SINGLE_LINK_URL).search);
  await page.getByRole('region',{name:'事件投递记录',exact:true}).getByText(/^当前事件状态：故障持续/).waitFor();
  const output=await fs.mkdtemp(path.join(require('node:os').tmpdir(),'myprobe-single-link-'));
  for(const theme of ['light','dark']) for(const width of [360,768,1440]) {
   await page.setViewportSize({width,height:1000});
   await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);
   assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1),false);
   await page.screenshot({path:path.join(output,`${theme}-${width}.png`),fullPage:true});
  }
  await page.reload();
  await page.getByRole('heading',{name:'Single linked node · 事件详情',exact:true}).waitFor();
  assert.deepEqual(errors,[]);
  console.log(`Actual single webhook link, private login, original event, reload and six theme/width views passed: ${output}`);
 } finally {await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
