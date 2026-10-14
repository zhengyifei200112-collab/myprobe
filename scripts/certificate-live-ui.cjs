const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict'), fs = require('node:fs/promises'), path = require('node:path');
(async () => {
 const browser=await chromium.launch({channel:'msedge',headless:true});
 try {
  const page=await browser.newPage(),errors=[];let privateReads=0;
  page.on('pageerror',e=>errors.push(e.message));
  page.on('request',r=>{if(r.url().includes('/api/v1/admin/incidents/'))privateReads++;});
  await page.goto(process.env.SERVICE_INCIDENT_URL);
  await page.getByRole('button',{name:'使用密码登录',exact:true}).waitFor();assert.equal(privateReads,0);
  await page.getByLabel('用户名',{exact:true}).fill('service-admin');
  await page.getByLabel('密码',{exact:true}).fill(process.env.SERVICE_INCIDENT_PASSWORD);
  await page.getByRole('button',{name:'使用密码登录',exact:true}).click();
  await page.getByRole('heading',{name:'Certificate observer · 事件详情',exact:true}).waitFor();
  await page.getByRole('region',{name:'事件投递记录',exact:true}).getByText(/^当前事件状态：证书到期预警/).waitFor();
  await page.getByRole('button',{name:'知悉此证书提醒',exact:true}).waitFor();
  await page.getByRole('button',{name:'HTTP 服务',exact:true}).click();
  await page.getByRole('button',{name:'服务告警与事件',exact:true}).click();
  const panel=page.getByRole('region',{name:'服务告警与事件',exact:true});
  await panel.getByRole('heading',{name:'证书提醒记录',exact:true}).waitFor();
  assert.equal(await panel.getByLabel('启用服务故障通知',{exact:true}).isChecked(),false);
  assert.equal(await panel.getByLabel('启用证书到期提醒（14／7／1 天）',{exact:true}).isChecked(),true);
  await panel.getByText('1 天档 · 已送达',{exact:true}).waitFor();
  await panel.getByText('7 天档 · 已送达',{exact:true}).waitFor();
  assert.equal(await panel.getByRole('article').count(),7);
  await panel.getByRole('button',{name:'保存服务告警',exact:true}).click();
  await panel.getByText('服务告警配置已保存，下一轮评估后生效。',{exact:true}).waitFor();
  await panel.getByRole('button',{name:'刷新服务告警',exact:true}).click();
  await panel.getByText('1 天档 · 已送达',{exact:true}).waitFor();
  assert.equal(await panel.getByLabel('启用证书到期提醒（14／7／1 天）',{exact:true}).isChecked(),true);
  const output=await fs.mkdtemp(path.join(require('node:os').tmpdir(),'myprobe-certificate-'));
  for(const theme of ['light','dark'])for(const width of [360,768,1440]) {
   await page.setViewportSize({width,height:1000});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);
   assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1),false);
   await page.screenshot({path:path.join(output,`${theme}-${width}.png`),fullPage:true});
  }
  await panel.getByRole('link',{name:'查看证书提醒事件',exact:true}).first().click();
  await page.getByRole('heading',{name:'Certificate observer · 事件详情',exact:true}).waitFor();
  assert.deepEqual(errors,[]);
  console.log(`Real TLS Agent certificate rotation, authenticated warning link, settings, ledger and six theme/width views passed: ${output}`);
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
