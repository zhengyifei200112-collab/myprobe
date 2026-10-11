// Real embedded UI/API/database/evaluator; fixtures and receiver are local to Go test.
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
(async () => {
  const browser = await chromium.launch({channel:'msedge',headless:true});
  try {
    const context = await browser.newContext({baseURL:process.env.POLICY_BROWSER_URL});
    // Exercise the browser capability boundary of non-local HTTP deployment.
    // The backend still uses loopback; no fixture credentials leave this machine.
    await context.addInitScript(()=>Object.defineProperty(crypto,'randomUUID',{value:undefined,configurable:true}));
    const login = await context.request.post('/api/v1/auth/login',{data:{username:'browser-admin',password:process.env.POLICY_BROWSER_PASSWORD}});
    assert.equal(login.status(),200,'fixture authentication');
    const csrf=(await login.json()).csrf_token;
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
    // Import through the real API, losing only the first successful response.
    const exported=await read('/api/v1/admin/alert-policies/export');
    assert.equal(exported.policies.length,1);
    exported.policies[0].channel_id='source-channel';
    await page.getByText('策略导入与导出',{exact:true}).click();
    await page.getByLabel('导入策略文件',{exact:true}).setInputFiles({name:'policies.json',mimeType:'application/json',buffer:Buffer.from(JSON.stringify(exported))});
    await page.getByRole('combobox',{name:'渠道：source-channel',exact:true}).selectOption(process.env.POLICY_BROWSER_CHANNEL);
    await page.getByRole('button',{name:'预览导入',exact:true}).click();
    await page.getByRole('heading',{name:'将新增 1 条策略',exact:true}).waitFor();
    for(const theme of ['light','dark']) for(const width of [360,768,1440]) {
      await page.setViewportSize({width,height:1000});
      await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);
      assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1),false,'import overflow');
      await page.screenshot({path:path.join(output,`import-${theme}-${width}.png`),fullPage:true});
    }
    let originalRequest;
    await page.route('**/alert-policies/import/apply',async route=>{
      originalRequest=route.request().postDataJSON();
      const response=await route.fetch();assert.equal(response.status(),201);
      await route.abort('failed');
    },{times:1});
    await page.getByRole('button',{name:'确认新增策略',exact:true}).click();
    await page.getByRole('button',{name:'重试确认导入结果',exact:true}).waitFor();
    await until(async()=> (await read('/api/v1/admin/alert-policies')).policies.length===2);
    await page.reload();
    await page.getByRole('button',{name:'告警',exact:true}).click();
    await page.getByRole('tab',{name:'范围策略',exact:true}).click();
    await page.getByText('策略导入与导出',{exact:true}).click();
    const replayRequest=page.waitForRequest(r=>r.url().endsWith('/alert-policies/import/apply'));
    await page.getByRole('button',{name:'重试确认导入结果',exact:true}).click();
    assert.deepEqual((await replayRequest).postDataJSON(),originalRequest,'refresh must preserve request identity');
    await page.getByText('已确认此前导入结果：1 条策略。',{exact:false}).waitFor();
    assert.equal((await read('/api/v1/admin/alert-policies')).policies.length,2,'retry must not duplicate policies');
    assert.equal(await page.evaluate(()=>sessionStorage.getItem('myprobe-policy-import-pending-v1')),null);
    // Malformed browser data must not reach the renderer or trigger a request.
    for (const corrupted of ['{', JSON.stringify({bundle:{policies:[null]}}), JSON.stringify({...originalRequest,requestID:'bad key',preview:{bundle:exported,create_count:1,preview_digest:'broken'}})]) {
      await page.evaluate(raw=>sessionStorage.setItem('myprobe-policy-import-pending-v1',raw),corrupted);
      await page.reload();
      await page.getByRole('button',{name:'告警',exact:true}).click();
      await page.getByRole('tab',{name:'范围策略',exact:true}).click();
      await page.getByText('策略导入与导出',{exact:true}).click();
      await page.getByText('无法读取待确认导入记录。',{exact:false}).waitFor();
      assert.equal(await page.getByLabel('导入策略文件',{exact:true}).isDisabled(),true);
      assert.equal(await page.evaluate(()=>sessionStorage.getItem('myprobe-policy-import-pending-v1')),corrupted,'do not silently discard uncertainty');
      await page.getByRole('button',{name:'已核对导入结果，清除损坏的本地记录',exact:true}).click();
      assert.equal(await page.getByLabel('导入策略文件',{exact:true}).isEnabled(),true);
      assert.equal(await page.evaluate(()=>sessionStorage.getItem('myprobe-policy-import-pending-v1')),null);
    }
    const templateResponse=await context.request.post('/api/v1/admin/notification-templates',{headers:{'X-CSRF-Token':csrf},data:{name:'Import template',event_kind:'all',title_template:'{{title}}',body_template:'{{message}}'}});
    assert.equal(templateResponse.status(),201);
    const template=(await templateResponse.json()).template;
    const nodes=(await read('/api/v1/admin/nodes')).nodes;
    const mapped=structuredClone(exported);
    mapped.policies[0].scope={kind:'nodes',node_ids:['source-node']};
    mapped.policies[0].config.template_id='source-template';
    await page.getByLabel('导入策略文件',{exact:true}).setInputFiles({name:'mapped.json',mimeType:'application/json',buffer:Buffer.from(JSON.stringify(mapped))});
    await page.getByRole('combobox',{name:'渠道：source-channel',exact:true}).selectOption(process.env.POLICY_BROWSER_CHANNEL);
    await page.getByRole('combobox',{name:'节点：source-node',exact:true}).selectOption(nodes[0].id);
    await page.getByRole('combobox',{name:'模板：source-template',exact:true}).selectOption(template.id);
    await page.getByRole('button',{name:'预览导入',exact:true}).click();
    await page.getByRole('heading',{name:'将新增 1 条策略',exact:true}).waitFor();
    const removed=await context.request.delete(`/api/v1/admin/notification-templates/${template.id}`,{headers:{'X-CSRF-Token':csrf}});
    assert.equal(removed.status(),204);
    await page.getByRole('button',{name:'确认新增策略',exact:true}).click();
    await page.getByText('本次请求未创建策略，请调整配置后重新预览。',{exact:false}).waitFor();
    assert.equal(await page.getByLabel('导入策略文件',{exact:true}).isEnabled(),true);
    assert.equal(await page.getByRole('button',{name:'预览导入',exact:true}).isEnabled(),true);
    assert.equal(await page.evaluate(()=>sessionStorage.getItem('myprobe-policy-import-pending-v1')),null);
    assert.equal((await read('/api/v1/admin/alert-policies')).policies.length,2,'deleted template must reject the entire import');
    const replacementResponse=await context.request.post('/api/v1/admin/notification-templates',{headers:{'X-CSRF-Token':csrf},data:{name:'Replacement import template',event_kind:'all',title_template:'{{title}}',body_template:'{{message}}'}});
    assert.equal(replacementResponse.status(),201);
    const replacement=(await replacementResponse.json()).template;
    assert.equal(nodes.length,2);
    mapped.policies[0].name='Mapped two-node policy';
    mapped.policies[0].scope.node_ids.push('second-source-node');
    await page.getByLabel('导入策略文件',{exact:true}).setInputFiles({name:'corrected.json',mimeType:'application/json',buffer:Buffer.from(JSON.stringify(mapped))});
    await page.getByRole('combobox',{name:'渠道：source-channel',exact:true}).selectOption(process.env.POLICY_BROWSER_CHANNEL);
    await page.getByRole('combobox',{name:'节点：source-node',exact:true}).selectOption(nodes[0].id);
    await page.getByRole('combobox',{name:'节点：second-source-node',exact:true}).selectOption(nodes[1].id);
    await page.getByRole('combobox',{name:'模板：source-template',exact:true}).selectOption(replacement.id);
    await page.getByRole('button',{name:'预览导入',exact:true}).click();
    await page.getByRole('heading',{name:'将新增 1 条策略',exact:true}).waitFor();
    // Editing a mapping invalidates the confirmation, even if changed back.
    await page.getByRole('combobox',{name:'节点：source-node',exact:true}).selectOption('');
    assert.equal(await page.getByRole('button',{name:'确认新增策略',exact:true}).count(),0);
    assert.equal(await page.getByRole('button',{name:'预览导入',exact:true}).isDisabled(),true);
    await page.getByRole('combobox',{name:'节点：source-node',exact:true}).selectOption(nodes[0].id);
    await page.getByRole('button',{name:'预览导入',exact:true}).click();
    await page.getByRole('heading',{name:'将新增 1 条策略',exact:true}).waitFor();
    await page.getByRole('button',{name:'确认新增策略',exact:true}).click();
    await page.getByText('导入成功：1 条策略。',{exact:false}).waitFor();
    const finalPolicies=(await read('/api/v1/admin/alert-policies')).policies;
    assert.equal(finalPolicies.length,3);
    const imported=finalPolicies.find(p=>p.name==='Mapped two-node policy');
    assert.deepEqual(imported.scope.node_ids,[nodes[0].id,nodes[1].id]);
    assert.equal(imported.config.template_id,replacement.id);
    assert.equal(imported.enabled,false);
    assert.deepEqual(errors,[]);
    console.log(`Live policy create, evaluation, webhook delivery, owner navigation and disable passed: ${output}`);
  } finally {await browser.close();}
})().catch(error=>{console.error(error);process.exitCode=1;});
