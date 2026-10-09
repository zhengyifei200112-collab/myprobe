// Real embedded UI/API/database/evaluator; fixtures and receiver are local to Go test.
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
(async () => {
  const browser = await chromium.launch({channel:'msedge',headless:true});
  try {
    const context = await browser.newContext({baseURL:process.env.POLICY_BROWSER_URL});
    const login = await context.request.post('/api/v1/auth/login',{data:{username:'browser-admin',password:process.env.POLICY_BROWSER_PASSWORD}});
    assert.equal(login.status(),200,'fixture authentication');
    const page = await context.newPage(), errors=[];
    page.on('pageerror',error=>errors.push(error.message));
    const read = async url => {const r=await context.request.get(url);assert.equal(r.status(),200,url);return r.json();};
    const until = async action => {const deadline=Date.now()+40000;do {const result=await action();if(result)return result;await new Promise(resolve=>setTimeout(resolve,250));}while(Date.now()<deadline);throw new Error('runtime condition timed out');};
    await page.goto('/admin');
    await page.getByRole('button',{name:'告警',exact:true}).click();
    await page.getByRole('tab',{name:'范围策略',exact:true}).click();
    await page.getByRole('button',{name:'新建范围策略',exact:true}).click();
    await page.getByLabel('策略名称',{exact:true}).fill('Live expiry policy');
    await page.getByLabel('策略键',{exact:false}).fill('browser.expiry');
    await page.getByRole('combobox',{name:/^匹配范围/}).selectOption('tags');
    await page.getByLabel('标签（每行一个）',{exact:true}).fill('browser');
    await page.getByRole('combobox',{name:/^通知通道/}).selectOption(process.env.POLICY_BROWSER_CHANNEL);
    await page.getByRole('combobox',{name:'告警类型',exact:true}).selectOption('expiry');
    await page.getByLabel('提前提醒（天）',{exact:true}).fill('1');
    await page.getByRole('button',{name:'保存范围策略',exact:true}).click();
    await page.getByRole('button',{name:'编辑策略',exact:true}).waitFor();
    const incident = await until(async()=> (await read('/api/v1/admin/incidents?state=firing')).incidents[0]);
    await until(async()=> (await read(`/api/v1/admin/incidents/${incident.id}/deliveries`)).deliveries?.some(job=>job.status==='delivered'));
    await page.getByRole('button',{name:'刷新策略',exact:true}).click();
    await page.getByText('策略评估已运行。',{exact:true}).waitFor();
    const output=await fs.mkdtemp(path.join(require('node:os').tmpdir(),'myprobe-policies-live-'));
    for(const theme of ['light','dark']) for(const width of [360,768,1440]) {
      await page.setViewportSize({width,height:1000});
      await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);
      assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1),false,'live policy overflow');
      await page.screenshot({path:path.join(output,`${theme}-${width}.png`),fullPage:true});
    }
    await page.reload();
    await page.getByRole('button',{name:'告警',exact:true}).click();
    await page.getByRole('tab',{name:'告警规则',exact:true}).click();
    await page.getByRole('button',{name:'编辑所属策略',exact:true}).click();
    await page.getByRole('heading',{name:'编辑范围策略',exact:true}).waitFor();
    assert.equal(await page.getByLabel('策略名称',{exact:true}).inputValue(),'Live expiry policy');
    await page.getByLabel('启用此策略配置',{exact:true}).uncheck();
    await page.getByRole('button',{name:'保存范围策略',exact:true}).click();
    const closed=await until(async()=> (await read('/api/v1/admin/incidents?state=resolved')).incidents.find(item=>item.id===incident.id));
    assert.equal(closed.resolution_reason,'rule_disabled');
    const jobs=(await read(`/api/v1/admin/incidents/${incident.id}/deliveries`)).deliveries;
    assert.equal(jobs.length,1);assert.equal(jobs[0].status,'delivered');
    assert.deepEqual(errors,[]);
    console.log(`Live policy create, evaluation, webhook delivery, owner navigation and disable passed: ${output}`);
  } finally {await browser.close();}
})().catch(error=>{console.error(error);process.exitCode=1;});
